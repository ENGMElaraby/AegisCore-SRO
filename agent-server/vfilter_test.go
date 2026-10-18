package main

import (
	"context"
	"testing"

	"github.com/Harvi8/AegisCore/agent-server/model"
	"github.com/Harvi8/AegisCore/framework/vfilter"
)

type fakeWorld struct {
	players []*model.Player
	notices []string
}

func (w *fakeWorld) GetActivePlayers() []*model.Player { return w.players }

func (w *fakeWorld) BroadcastNotice(message string) (int, error) {
	w.notices = append(w.notices, message)
	return len(w.players), nil
}

func TestAgentPanelCommands(t *testing.T) {
	world := &fakeWorld{}
	v := &agentVFilter{world: world, mirrored: map[string]struct{}{}}
	ctx := context.Background()

	if out := v.handleCommand(ctx, vfilter.Command{Name: "kick", Target: "Knight"}); out.OK || out.Result != "player not online" {
		t.Fatalf("kick offline: %+v", out)
	}
	if out := v.handleCommand(ctx, vfilter.Command{Name: "ban", Target: "sara", Payload: []byte(`{"reason":"x","duration_minutes":1440}`)}); out.OK {
		t.Fatalf("timed ban must not become permanent: %+v", out)
	}
	if out := v.handleCommand(ctx, vfilter.Command{Name: "ban", Target: "sara", Payload: []byte(`{"reason":"x","duration_minutes":null}`)}); out.OK || out.Result != "player not online" {
		t.Fatalf("permanent ban offline: %+v", out)
	}
	if out := v.handleCommand(ctx, vfilter.Command{Name: "notice", Payload: []byte(`{"message":"  Server restart in 5 minutes "}`)}); !out.OK || world.notices[0] != "Server restart in 5 minutes" {
		t.Fatalf("notice: %+v %v", out, world.notices)
	}
	if out := v.handleCommand(ctx, vfilter.Command{Name: "notice", Payload: []byte(`{}`)}); out.OK {
		t.Fatal("empty notice accepted")
	}
	if out := v.handleCommand(ctx, vfilter.Command{Name: "kick", Payload: []byte(`{not json`)}); out.OK || out.Result != "bad payload" {
		t.Fatalf("bad payload: %+v", out)
	}
}

func TestSessionInfoIgnoresPlayersWithoutSession(t *testing.T) {
	if _, ok := sessionInfo(&model.Player{CharName: "Knight"}); ok {
		t.Fatal("player without session mirrored")
	}
	if _, ok := sessionInfo(nil); ok {
		t.Fatal("nil player mirrored")
	}
}
