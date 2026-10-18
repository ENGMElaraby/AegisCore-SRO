package main

import (
	"context"
	"net/netip"
	"testing"

	"github.com/Harvi8/AegisCore/framework/vfilter"
	"github.com/Harvi8/AegisCore/gateway-server/handler"
)

func testGatewayVFilter(snapshot *vfilter.Snapshot, mode string) *gatewayVFilter {
	v := &gatewayVFilter{config: vfilter.NewConfigWithSnapshot(snapshot)}
	v.setMode(mode)
	return v
}

func TestGateFollowsGatewayMode(t *testing.T) {
	snapshot := &vfilter.Snapshot{
		Settings: map[string]string{inspectionRefusalKey: "4"},
		Lists:    map[string][]string{allowedIPsList: {"10.0.0.0/8"}, blockedIPsList: {"6.6.6.6"}},
	}
	staff, player, blocked := netip.MustParseAddr("10.1.2.3"), netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("6.6.6.6")

	cases := []struct {
		mode     string
		ip       netip.Addr
		refused  bool
		wantCode byte
	}{
		{modeGM, staff, false, 0},
		{modeGM, player, true, handler.LoginFailureShardUnavailable},
		{modeInspection, player, true, 4},
		{modeInspection, staff, false, 0},
		{modePublic, player, false, 0},
		{modePublic, blocked, true, handler.LoginFailureBlocked},
	}
	for _, c := range cases {
		failure, refused := testGatewayVFilter(snapshot, c.mode).Gate("sara", c.ip)
		if refused != c.refused || failure.Code != c.wantCode {
			t.Errorf("mode %s ip %s: refused=%v code=%d, want %v/%d", c.mode, c.ip, refused, failure.Code, c.refused, c.wantCode)
		}
	}
}

func TestSetGatewayModeCommand(t *testing.T) {
	v := testGatewayVFilter(&vfilter.Snapshot{}, modeGM)

	out := v.handleCommand(context.Background(), vfilter.Command{Name: "set_gateway_mode", Target: "gateway", Payload: []byte(`{"mode":"public"}`)})
	if !out.OK || v.Mode() != modePublic || !v.modeSet.Load() {
		t.Fatalf("%+v mode=%s", out, v.Mode())
	}
	if out := v.handleCommand(context.Background(), vfilter.Command{Target: "gateway", Payload: []byte(`{"mode":"party"}`)}); out.OK {
		t.Fatal("bad mode accepted")
	}
	if out := v.handleCommand(context.Background(), vfilter.Command{Target: "agent1", Payload: []byte(`{"mode":"gm"}`)}); out.OK {
		t.Fatal("non-gateway target accepted")
	}
}

func TestStartModeAppliesOnceAndNeverOverridesThePanel(t *testing.T) {
	v := testGatewayVFilter(&vfilter.Snapshot{Settings: map[string]string{startInInspectionKey: "1"}}, modeGM)
	v.applyStartSetting()
	if v.Mode() != modeInspection {
		t.Fatalf("start mode %s", v.Mode())
	}
	v.setMode(modePublic)
	v.applyStartSetting()
	if v.Mode() != modePublic {
		t.Fatal("start setting applied twice")
	}
}
