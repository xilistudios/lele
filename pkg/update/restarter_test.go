// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package update

import (
	"errors"
	"strings"
	"testing"
)

// newTestRestarter returns a Restarter that never touches the real service
// manager and never terminates the test binary.
func newTestRestarter(supervisor Supervisor) *Restarter {
	r := NewRestarter()
	r.Detect = func() Supervisor { return supervisor }
	r.runSystemctl = func(bool, ...string) (string, error) {
		return "", errors.New("systemctl must not be called in tests")
	}
	r.Exit = func(int) {}
	return r
}

// TestRestartSelfExecOrder is the core of the self-exec fix: Restart must
// spawn the replacement, run OnRestart (the gateway wires its shutdown
// coordinator here), and only then exit the parent. Exiting before the
// teardown - or not exiting at all - is what left the old process running
// alongside the new one.
func TestRestartSelfExecOrder(t *testing.T) {
	t.Setenv("LELE_RESTART_DRY_RUN", "1") // spawn nothing, never really exit

	var events []string
	r := newTestRestarter(SupervisorNone)
	r.OnRestart = func(method string) { events = append(events, "on_restart:"+method) }
	r.Exit = func(int) { events = append(events, "exit") }

	method, err := r.Restart()
	if err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	if method != "self-exec" {
		t.Errorf("method = %q, want %q", method, "self-exec")
	}

	want := []string{"on_restart:self-exec", "exit"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", events, want)
	}
}

// TestRestartSpawnFailureKeepsParentRunning is the safety half of the ordering:
// if the replacement cannot be spawned, the parent must not run its teardown and
// must not exit, because then there would be no instance at all.
func TestRestartSpawnFailureKeepsParentRunning(t *testing.T) {
	t.Setenv("LELE_RESTART_DRY_RUN", "")

	spawned := false
	r := newTestRestarter(SupervisorNone)
	r.spawn = func(string, []string) error {
		spawned = true
		return errors.New("boom")
	}
	r.OnRestart = func(string) { t.Error("OnRestart ran after a failed spawn") }
	r.Exit = func(int) { t.Error("parent exited after a failed spawn") }

	if _, err := r.Restart(); err == nil {
		t.Fatal("Restart() error = nil, want the spawn failure")
	}
	if !spawned {
		t.Error("spawn was never attempted")
	}
}

// TestRestartSystemdDoesNotExit documents why the systemd paths never call
// OnRestart or Exit: the supervisor sends SIGTERM, which runs the normal
// signal-driven graceful path. Exiting here would cut systemd's restart short,
// so the contract is "issue the restart, return, let systemd do its job".
func TestRestartSystemdDoesNotExit(t *testing.T) {
	cases := []struct {
		name       string
		supervisor Supervisor
		want       string
	}{
		{"user", SupervisorSystemdUser, "systemctl --user restart lele.service"},
		{"system", SupervisorSystemdSystem, "systemctl restart lele.service"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var restarts []string
			r := newTestRestarter(tc.supervisor)
			// findUnit probes units before restarting, so only the actual
			// restart calls are recorded here.
			r.runSystemctl = func(userScope bool, args ...string) (string, error) {
				if len(args) > 0 && args[0] == "restart" {
					restarts = append(restarts, strings.Join(args, " "))
				}
				return "", nil
			}
			r.OnRestart = func(string) { restarts = append(restarts, "on_restart") }
			r.Exit = func(int) { restarts = append(restarts, "exit") }

			method, err := r.Restart()
			if err != nil {
				t.Fatalf("Restart() error = %v", err)
			}
			if method != tc.want {
				t.Errorf("method = %q, want %q", method, tc.want)
			}
			if len(restarts) != 1 || restarts[0] != "restart lele.service" {
				t.Errorf("calls = %v, want [restart lele.service]: systemd paths must not tear down or exit", restarts)
			}
		})
	}
}

// TestRestartChildEnvMarksReplacement verifies the marker that makes the handoff
// work: the child must be told it is a restart replacement so it waits for the
// desktop instance lock instead of reporting "already_running" and exiting while
// the parent is still draining.
func TestRestartChildEnvMarksReplacement(t *testing.T) {
	t.Setenv(RestartChildEnvKey, "")

	env := restartChildEnv()
	found := false
	for _, kv := range env {
		if kv == RestartChildEnvKey+"=1" {
			found = true
		}
		if strings.HasPrefix(kv, RestartChildEnvKey+"=") && kv != RestartChildEnvKey+"=1" {
			t.Errorf("duplicate marker %q must be overridden by the trailing one", kv)
		}
	}
	if !found {
		t.Fatalf("environment %v does not mark the child as a restart replacement", env)
	}
}

// TestRestartSelfExecSkippedInDryRun confirms dry-run leaves the process alive
// while still reporting the self-exec method, so the update flow can be
// exercised end to end without killing the gateway.
func TestRestartSelfExecSkippedInDryRun(t *testing.T) {
	t.Setenv("LELE_RESTART_DRY_RUN", "1")

	r := newTestRestarter(SupervisorNone)
	r.spawn = func(string, []string) error {
		t.Error("spawn ran in dry-run mode")
		return nil
	}
	exited := false
	r.Exit = func(int) { exited = true }

	if _, err := r.Restart(); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	// The injected Exit always runs: it is the seam tests use to observe the
	// sequence, and in dry-run the real os.Exit is what gets skipped.
	if !exited {
		t.Error("Exit seam was not called")
	}
}
