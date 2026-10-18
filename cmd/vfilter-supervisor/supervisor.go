package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Harvi8/AegisCore/framework/vfilter"
)

// module is one panel module the supervisor controls.
type module struct {
	Name      string // filter_modules.name, e.g. "Gateway"
	Container string // compose container, e.g. "aegiscore-gateway-server-1"
}

func (m module) target() string { return strings.ToLower(m.Name) }

// startOrder: gateway last, so players never reach a gateway before its agent is up; stop runs in reverse.
func (m module) startOrder() int {
	if strings.EqualFold(m.Name, "Gateway") {
		return 100
	}
	return 0
}

// parseModules reads "Gateway=container,Agent1=container".
func parseModules(spec string) ([]module, error) {
	var modules []module
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, container, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(container) == "" {
			return nil, fmt.Errorf("VFILTER_MODULE_CONTAINERS entry %q must be Name=container", part)
		}
		modules = append(modules, module{Name: strings.TrimSpace(name), Container: strings.TrimSpace(container)})
	}
	if len(modules) == 0 {
		return nil, fmt.Errorf("VFILTER_MODULE_CONTAINERS is empty")
	}
	return modules, nil
}

type containers interface {
	inspect(ctx context.Context, container string) (containerState, error)
	action(ctx context.Context, container, verb string) (bool, error)
}

type supervisor struct {
	modules []module
	docker  containers
}

// handle carries out start / stop / restart / reload (the commands the supervisor owns).
func (s *supervisor) handle(ctx context.Context, cmd vfilter.Command) vfilter.Outcome {
	switch cmd.Name {
	case "reload":
		return vfilter.Done("AegisCore reloads changed configuration automatically within 2 s")
	case "start", "stop", "restart":
	default:
		return vfilter.Failed("unknown command " + cmd.Name)
	}

	targets, err := s.resolve(cmd.Target)
	if err != nil {
		return vfilter.Failed(err.Error())
	}
	if cmd.Name != "start" {
		sort.SliceStable(targets, func(i, j int) bool { return targets[i].startOrder() > targets[j].startOrder() })
	}

	var results []string
	failed := false
	for _, m := range targets {
		changed, err := s.docker.action(ctx, m.Container, cmd.Name)
		switch {
		case err != nil:
			failed = true
			results = append(results, m.Name+" failed: "+err.Error())
		case !changed && cmd.Name == "start":
			results = append(results, m.Name+" already running")
		case !changed:
			results = append(results, m.Name+" already stopped")
		default:
			results = append(results, m.Name+" "+pastTense(cmd.Name))
		}
	}
	if failed {
		return vfilter.Failed(strings.Join(results, ", "))
	}
	return vfilter.Done(strings.Join(results, ", "))
}

func (s *supervisor) resolve(target string) ([]module, error) {
	if strings.EqualFold(target, "all") {
		all := append([]module(nil), s.modules...)
		sort.SliceStable(all, func(i, j int) bool { return all[i].startOrder() < all[j].startOrder() })
		return all, nil
	}
	for _, m := range s.modules {
		if m.target() == strings.ToLower(target) {
			return []module{m}, nil
		}
	}
	return nil, fmt.Errorf("unknown target %s (not managed by the supervisor)", target)
}

// downStatuses reports modules whose container is not running; running modules heartbeat themselves.
func (s *supervisor) downStatuses(ctx context.Context) []vfilter.Status {
	var statuses []vfilter.Status
	for _, m := range s.modules {
		state, err := s.docker.inspect(ctx, m.Container)
		switch {
		case err != nil:
			statuses = append(statuses, vfilter.Status{Module: m.Name, State: "error", LastError: err.Error()})
		case state.Running:
		case state.ExitCode != 0 && state.Status != "created" && !stoppedBySignal(state):
			statuses = append(statuses, vfilter.Status{Module: m.Name, State: "error", LastError: fmt.Sprintf("container %s (exit code %d)", state.Status, state.ExitCode)})
		default:
			statuses = append(statuses, vfilter.Status{Module: m.Name, State: "stopped"})
		}
	}
	return statuses
}

// stoppedBySignal: docker stop sends SIGTERM (exit 143) and SIGKILL after the grace period (exit 137).
// Those are deliberate stops, not failures, unless the kernel killed the process for memory.
func stoppedBySignal(state containerState) bool {
	return (state.ExitCode == exitSIGTERM || state.ExitCode == exitSIGKILL) && !state.OOMKilled
}

const (
	exitSIGKILL = 128 + 9
	exitSIGTERM = 128 + 15
)

func pastTense(verb string) string {
	switch verb {
	case "stop":
		return "stopped"
	case "restart":
		return "restarted"
	default:
		return "started"
	}
}
