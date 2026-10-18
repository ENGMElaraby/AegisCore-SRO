package main

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harvi8/AegisCore/framework/server"
	"github.com/Harvi8/AegisCore/framework/vfilter"
	"github.com/Harvi8/AegisCore/gateway-server/handler"
	log "github.com/sirupsen/logrus"
)

// VFilter panel settings and lists read by the Gateway (shared contract 0.3.2/0.3.3).
const (
	gatewayModuleName       = "Gateway"
	allowedIPsList          = "allowed_ips"
	blockedIPsList          = "blocked_ips"
	startInInspectionKey    = "server.start_in_inspection"
	gmRefusalCodeKey        = "gateway.gm_refusal_code"
	inspectionRefusalKey    = "gateway.inspection_refusal_code"
	defaultRefusalCode      = int(handler.LoginFailureShardUnavailable)
	modeGM, modeInspection  = "gm", "inspection"
	modePublic              = "public"
	vfilterStopWaitDuration = 5 * time.Second
)

// gatewayVFilter connects the Gateway to the VFilter panel: login gate (gateway modes, blocked IPs), heartbeat,
// set_gateway_mode, and login events. See _docs/vfilter-integration.md.
type gatewayVFilter struct {
	client  *vfilter.Client
	config  *vfilter.Config
	events  *vfilter.EventLog
	server  *server.Server
	started time.Time
	mode    atomic.Pointer[string]
	modeSet atomic.Bool // the start mode was applied, or a panel command chose the mode
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// newGatewayVFilter returns nil when VFILTER_DSN is not set.
func newGatewayVFilter(s *server.Server) (*gatewayVFilter, error) {
	client, err := vfilter.Open(context.Background(), gatewayModuleName)
	if err != nil || client == nil {
		return nil, err
	}
	v := &gatewayVFilter{client: client, config: vfilter.NewConfig(client), events: vfilter.NewEventLog(client), server: s, started: time.Now()}
	gm := modeGM // contract: the gateway starts in GM mode unless server.start_in_inspection is set
	v.mode.Store(&gm)
	return v, nil
}

// Gate is the login handler's PreAuthGate.
func (v *gatewayVFilter) Gate(username string, ip netip.Addr) (handler.LoginFailure, bool) {
	snapshot := v.config.Snapshot()
	if vfilter.AnyContains(snapshot.Lists[blockedIPsList], ip) {
		v.events.Write(vfilter.Event{Level: "security", Category: "login", Message: "login refused: blocked IP", IP: ip.String(), Username: username})
		return handler.LoginFailure{Code: handler.LoginFailureBlocked}, true
	}

	mode := v.Mode()
	if mode == modePublic || vfilter.AnyContains(snapshot.Lists[allowedIPsList], ip) {
		return handler.LoginFailure{}, false
	}

	key := gmRefusalCodeKey
	if mode == modeInspection {
		key = inspectionRefusalKey
	}
	code := snapshot.Int(key, defaultRefusalCode)
	if code < 1 || code > 255 {
		code = defaultRefusalCode
	}
	v.events.Write(vfilter.Event{Level: "info", Category: "login", Message: "login refused: gateway in " + mode + " mode", IP: ip.String(), Username: username})
	return handler.LoginFailure{Code: byte(code)}, true
}

// Mode is the current gateway mode: gm, inspection or public.
func (v *gatewayVFilter) Mode() string { return *v.mode.Load() }

func (v *gatewayVFilter) setMode(mode string) { v.mode.Store(&mode) }

// Start runs the pollers until Stop.
func (v *gatewayVFilter) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	v.cancel = cancel
	v.applyStartMode(ctx)
	run := func(f func(context.Context)) {
		v.wg.Add(1)
		go func() { defer v.wg.Done(); f(ctx) }()
	}
	run(v.config.Run)
	run(v.events.Run)
	run(func(ctx context.Context) { v.client.RunCommands(ctx, []string{"set_gateway_mode"}, v.handleCommand) })
	run(func(ctx context.Context) { v.client.RunHeartbeat(ctx, v.status) })
	run(v.watchStartMode)
}

// Stop ends the pollers (the heartbeat writes a final "stopped") and closes the connection.
func (v *gatewayVFilter) Stop() {
	if v.cancel != nil {
		v.cancel()
	}
	done := make(chan struct{})
	go func() { v.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(vfilterStopWaitDuration):
		log.Warn("VFILTER background work did not stop in time")
	}
	_ = v.client.Close()
}

func (v *gatewayVFilter) status() vfilter.Status {
	return vfilter.Status{
		Module:      v.client.Module,
		State:       "running",
		Connections: v.server.SessionCount(),
		GatewayMode: v.Mode(),
		Version:     vfilter.BuildVersion(),
		Uptime:      time.Since(v.started),
	}
}

func (v *gatewayVFilter) handleCommand(_ context.Context, cmd vfilter.Command) vfilter.Outcome {
	if !strings.EqualFold(cmd.Target, "gateway") {
		return vfilter.Failed("gateway mode applies to the gateway only")
	}
	mode := cmd.PayloadString("mode")
	if mode != modeGM && mode != modeInspection && mode != modePublic {
		return vfilter.Failed("bad payload: mode must be gm, inspection or public")
	}
	previous := v.Mode()
	v.modeSet.Store(true) // the start setting must not override a mode chosen in the panel
	v.setMode(mode)
	v.events.Write(vfilter.Event{Level: "info", Category: "command", Message: "gateway mode " + previous + " -> " + mode})
	return vfilter.Done("gateway mode " + previous + " → " + mode)
}

// applyStartMode loads the configuration once (briefly) so server.start_in_inspection applies from the first login.
func (v *gatewayVFilter) applyStartMode(ctx context.Context) {
	loadCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := v.config.Refresh(loadCtx, true); err != nil {
		log.WithError(err).Warn("VFILTER configuration not loaded yet; gateway stays in GM mode until it is")
		return
	}
	v.applyStartSetting()
}

// watchStartMode applies server.start_in_inspection once, when the configuration first arrives late.
func (v *gatewayVFilter) watchStartMode(ctx context.Context) {
	ticker := time.NewTicker(vfilter.ConfigPollInterval)
	defer ticker.Stop()
	for !v.modeSet.Load() {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if v.config.Loaded() {
				v.applyStartSetting()
			}
		}
	}
}

// applyStartSetting runs once: the start setting decides the mode at startup only, as in MaxiGuard.
func (v *gatewayVFilter) applyStartSetting() {
	if !v.modeSet.CompareAndSwap(false, true) {
		return
	}
	if v.config.Snapshot().Bool(startInInspectionKey, false) {
		v.setMode(modeInspection)
	}
}
