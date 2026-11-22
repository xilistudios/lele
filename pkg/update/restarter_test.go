package update

import (
	"errors"
	"strings"
	"testing"
)

func newTestRestarter(supervisor Supervisor) *Restarter {
	r := NewRestarter()
	r.Detect = func() Supervisor { return supervisor }
	r.runSystemctl = func(bool, ...string) (string, error) {
		return "", errors.New("systemctl must not be called in tests")
	}
	r.Exit = func(int) {}
	return r
}

func TestRestartSelfExecOrder(t *testing.T) {
	t.Setenv("LELE_RESTART_DRY_RUN", "1")

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
	if !exited {
		t.Error("Exit seam was not called")
	}
}
