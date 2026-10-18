package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Harvi8/AegisCore/framework/vfilter"
)

type fakeDocker struct {
	calls   []string
	running map[string]bool
	exit    map[string]int
	oom     map[string]bool
	fail    map[string]bool
}

func (f *fakeDocker) inspect(_ context.Context, c string) (containerState, error) {
	if f.fail[c] {
		return containerState{}, errors.New("no such container")
	}
	status := "exited"
	if f.running[c] {
		status = "running"
	}
	return containerState{Running: f.running[c], Status: status, ExitCode: f.exit[c], OOMKilled: f.oom[c]}, nil
}

func (f *fakeDocker) action(_ context.Context, c, verb string) (bool, error) {
	f.calls = append(f.calls, verb+" "+c)
	if f.fail[c] {
		return false, errors.New("boom")
	}
	was := f.running[c]
	f.running[c] = verb != "stop"
	return !(verb == "start" && was) && !(verb == "stop" && !was), nil
}

func newTestSupervisor() (*supervisor, *fakeDocker) {
	docker := &fakeDocker{running: map[string]bool{"gw": true, "ag": true}, exit: map[string]int{}, fail: map[string]bool{}, oom: map[string]bool{}}
	mods, _ := parseModules("Gateway=gw, Agent1=ag")
	return &supervisor{modules: mods, docker: docker}, docker
}

func TestParseModulesRejectsBadEntries(t *testing.T) {
	if _, err := parseModules("Gateway"); err == nil {
		t.Fatal("want error")
	}
	if _, err := parseModules(""); err == nil {
		t.Fatal("want error")
	}
}

func TestAllStopsGatewayFirstAndStartsItLast(t *testing.T) {
	s, docker := newTestSupervisor()
	out := s.handle(context.Background(), vfilter.Command{Name: "stop", Target: "all"})
	if !out.OK || strings.Join(docker.calls, ",") != "stop gw,stop ag" {
		t.Fatalf("%+v %v", out, docker.calls)
	}
	docker.calls = nil
	out = s.handle(context.Background(), vfilter.Command{Name: "start", Target: "all"})
	if !out.OK || strings.Join(docker.calls, ",") != "start ag,start gw" || out.Result != "Agent1 started, Gateway started" {
		t.Fatalf("%+v %v", out, docker.calls)
	}
}

func TestSingleTargetsAndErrors(t *testing.T) {
	s, docker := newTestSupervisor()
	if out := s.handle(context.Background(), vfilter.Command{Name: "start", Target: "agent1"}); out.Result != "Agent1 already running" {
		t.Fatalf("%+v", out)
	}
	if out := s.handle(context.Background(), vfilter.Command{Name: "restart", Target: "download"}); out.OK {
		t.Fatalf("unknown target should fail: %+v", out)
	}
	docker.fail["gw"] = true
	if out := s.handle(context.Background(), vfilter.Command{Name: "restart", Target: "gateway"}); out.OK || !strings.Contains(out.Result, "Gateway failed") {
		t.Fatalf("%+v", out)
	}
}

func TestDownStatusesSkipRunningModules(t *testing.T) {
	s, docker := newTestSupervisor()
	docker.running["ag"] = false
	docker.exit["ag"] = 2
	statuses := s.downStatuses(context.Background())
	if len(statuses) != 1 || statuses[0].Module != "Agent1" || statuses[0].State != "error" {
		t.Fatalf("%+v", statuses)
	}
	docker.exit["ag"] = 0
	if got := s.downStatuses(context.Background()); got[0].State != "stopped" {
		t.Fatalf("%+v", got)
	}
}

func TestDownStatusesTreatDockerStopSignalsAsStopped(t *testing.T) {
	s, docker := newTestSupervisor()
	docker.running["ag"] = false
	for _, code := range []int{exitSIGTERM, exitSIGKILL} {
		docker.exit["ag"] = code
		if got := s.downStatuses(context.Background()); got[0].State != "stopped" {
			t.Fatalf("exit %d: %+v", code, got)
		}
	}
	docker.oom["ag"] = true
	if got := s.downStatuses(context.Background()); got[0].State != "error" {
		t.Fatalf("oom kill: %+v", got)
	}
}
