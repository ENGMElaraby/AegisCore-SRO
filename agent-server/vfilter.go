package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Harvi8/AegisCore/agent-server/model"
	agentruntime "github.com/Harvi8/AegisCore/agent-server/runtime"
	"github.com/Harvi8/AegisCore/framework/server"
	"github.com/Harvi8/AegisCore/framework/vfilter"
	log "github.com/sirupsen/logrus"
)

const (
	agentModuleName      = "Agent1"
	sessionSyncInterval  = 2 * time.Second
	agentVFilterStopWait = 5 * time.Second
	defaultKickReason    = "kicked by an admin"
)

// worldPlayers is the part of the WorldService the panel integration needs.
type worldPlayers interface {
	GetActivePlayers() []*model.Player
	BroadcastNotice(message string) (int, error)
}

// agentVFilter connects the Agent to the VFilter panel: heartbeat, in-world session mirror, and the kick, ban
// and notice commands. See _docs/vfilter-integration.md.
type agentVFilter struct {
	client    *vfilter.Client
	events    *vfilter.EventLog
	mirror    *vfilter.SessionMirror
	world     worldPlayers
	engine    *server.Server
	lifecycle *agentruntime.Lifecycle
	ban       func(ctx context.Context, accountID uint32) error
	started   time.Time
	mirrored  map[string]struct{}
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// newAgentVFilter returns nil when VFILTER_DSN is not set.
func newAgentVFilter(world worldPlayers, engine *server.Server, lifecycle *agentruntime.Lifecycle, account *sql.DB) (*agentVFilter, error) {
	client, err := vfilter.Open(context.Background(), agentModuleName)
	if err != nil || client == nil {
		return nil, err
	}
	moderation, err := model.NewAccountModerationRepository(account)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return &agentVFilter{
		client: client, events: vfilter.NewEventLog(client), mirror: vfilter.NewSessionMirror(client),
		world: world, engine: engine, lifecycle: lifecycle, ban: moderation.BanAccount,
		started: time.Now(), mirrored: map[string]struct{}{},
	}, nil
}

// Start runs the pollers until Stop.
func (v *agentVFilter) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	v.cancel = cancel
	run := func(f func(context.Context)) {
		v.wg.Add(1)
		go func() { defer v.wg.Done(); f(ctx) }()
	}
	run(v.events.Run)
	run(v.mirror.Run)
	run(v.syncSessions)
	run(func(ctx context.Context) {
		v.client.RunCommands(ctx, []string{"kick", "ban", "notice"}, v.handleCommand)
	})
	run(func(ctx context.Context) { v.client.RunHeartbeat(ctx, v.status) })
}

// Stop ends the pollers (the heartbeat writes a final "stopped") and closes the connection.
func (v *agentVFilter) Stop() {
	if v.cancel != nil {
		v.cancel()
	}
	done := make(chan struct{})
	go func() { v.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(agentVFilterStopWait):
		log.Warn("VFILTER background work did not stop in time")
	}
	_ = v.client.Close()
}

func (v *agentVFilter) status() vfilter.Status {
	state, lastError := "running", ""
	if v.lifecycle != nil {
		snapshot := v.lifecycle.Snapshot()
		switch snapshot.State {
		case agentruntime.Starting, agentruntime.Loading:
			state = "starting"
		case agentruntime.Degraded:
			state, lastError = "error", snapshot.Reason
		case agentruntime.Draining, agentruntime.Stopped:
			state = "stopped"
		}
	}
	return vfilter.Status{
		Module:      v.client.Module,
		State:       state,
		OnlineCount: len(v.world.GetActivePlayers()),
		Connections: v.engine.SessionCount(),
		Version:     vfilter.BuildVersion(),
		Uptime:      time.Since(v.started),
		LastError:   lastError,
	}
}

// syncSessions mirrors in-world players every 2 s by diffing the active snapshot, so no game handler changes.
func (v *agentVFilter) syncSessions(ctx context.Context) {
	ticker := time.NewTicker(sessionSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			v.syncOnce()
		}
	}
}

func (v *agentVFilter) syncOnce() {
	seen := map[string]struct{}{}
	for _, player := range v.world.GetActivePlayers() {
		info, ok := sessionInfo(player)
		if !ok {
			continue
		}
		seen[info.Key] = struct{}{}
		if _, known := v.mirrored[info.Key]; !known {
			v.mirrored[info.Key] = struct{}{}
			v.mirror.Opened(info)
		}
	}
	for key := range v.mirrored {
		if _, ok := seen[key]; !ok {
			delete(v.mirrored, key)
			v.mirror.Closed(key)
		}
	}
}

func sessionInfo(player *model.Player) (vfilter.SessionInfo, bool) {
	if player == nil {
		return vfilter.SessionInfo{}, false
	}
	session := player.GetSession()
	if session == nil {
		return vfilter.SessionInfo{}, false
	}
	return vfilter.SessionInfo{Key: session.ID, IP: sessionIP(session), Username: session.UserContext.Username, Charname: player.CharName}, true
}

func sessionIP(session *server.Session) string {
	if session.Conn == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(session.Conn.RemoteAddr().String())
	if err != nil {
		return session.Conn.RemoteAddr().String()
	}
	return host
}

type playerCommandPayload struct {
	Reason          string `json:"reason"`
	DurationMinutes *int   `json:"duration_minutes"`
	Message         string `json:"message"`
}

func (v *agentVFilter) handleCommand(ctx context.Context, cmd vfilter.Command) vfilter.Outcome {
	var payload playerCommandPayload
	if len(cmd.Payload) > 0 && json.Unmarshal(cmd.Payload, &payload) != nil {
		return vfilter.Failed("bad payload")
	}
	switch cmd.Name {
	case "kick":
		return v.kick(cmd.Target, payload)
	case "ban":
		return v.banAccount(ctx, cmd.Target, payload)
	case "notice":
		return v.notice(payload)
	default:
		return vfilter.Failed("unknown command " + cmd.Name)
	}
}

// kick closes every in-world session whose character, username or IP is target (contract 0.3.1).
func (v *agentVFilter) kick(target string, payload playerCommandPayload) vfilter.Outcome {
	reason := payload.Reason
	if reason == "" {
		reason = defaultKickReason
	}
	kicked := 0
	for _, player := range v.world.GetActivePlayers() {
		info, ok := sessionInfo(player)
		if !ok || !(strings.EqualFold(info.Charname, target) || strings.EqualFold(info.Username, target) || info.IP == target) {
			continue
		}
		player.GetSession().Close()
		kicked++
		v.events.Write(vfilter.Event{Level: "info", Category: "command", Message: "kicked: " + reason, IP: info.IP, Username: info.Username, Charname: info.Charname})
	}
	if kicked == 0 {
		return vfilter.Failed("player not online")
	}
	return vfilter.Done(fmt.Sprintf("disconnected %d session(s)", kicked))
}

// banAccount bans the online account named target (SRO_ACCOUNT.USER.STATUS = 0, enforced at Gateway login) and
// disconnects it. AegisCore has no ban expiry, so a timed ban is refused rather than made permanent.
func (v *agentVFilter) banAccount(ctx context.Context, target string, payload playerCommandPayload) vfilter.Outcome {
	if payload.DurationMinutes != nil {
		return vfilter.Failed("timed bans not supported yet; choose Permanent")
	}
	for _, player := range v.world.GetActivePlayers() {
		info, ok := sessionInfo(player)
		session := player.GetSession()
		if !ok || !strings.EqualFold(info.Username, target) || session.UserContext.UserID == 0 {
			continue
		}
		if err := v.ban(ctx, session.UserContext.UserID); err != nil {
			return vfilter.Failed("ban failed: " + err.Error())
		}
		session.Close()
		v.events.Write(vfilter.Event{Level: "security", Category: "command", Message: "account banned: " + payload.Reason, IP: info.IP, Username: info.Username, Charname: info.Charname})
		return vfilter.Done("account banned permanently and disconnected")
	}
	return vfilter.Failed("player not online")
}

func (v *agentVFilter) notice(payload playerCommandPayload) vfilter.Outcome {
	if strings.TrimSpace(payload.Message) == "" {
		return vfilter.Failed("bad payload: message is required")
	}
	count, err := v.world.BroadcastNotice(strings.TrimSpace(payload.Message))
	if err != nil {
		return vfilter.Failed("notice refused: " + err.Error())
	}
	return vfilter.Done(fmt.Sprintf("notice sent to %d player(s)", count))
}
