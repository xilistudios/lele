package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMain guards against infinite recursion from the selfExec-related
// test: when Restart() spawns a detached copy of the test binary, that
// child inherits LELE_UPDATE_CHILD=1 and exits immediately instead of
// re-running the whole suite (which would call Restart() again).
func TestMain(m *testing.M) {
	if os.Getenv("LELE_UPDATE_CHILD") == "1" {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeSystemctlScript replaces the real systemctl so tests can control
// every branch deterministically without touching real services.
const fakeSystemctlScript = `#!/bin/sh
scope="system"
if [ "$1" = "--user" ]; then scope="user"; shift; fi
cmd="$1"
shift
case "$cmd" in
  show)
    if [ "$scope" = "user" ]; then echo "$FAKE_INVOCATION_USER"; else echo "$FAKE_INVOCATION_SYSTEM"; fi
    exit 0 ;;
  is-active)
    unit="$1"
    if [ "$scope" = "user" ]; then active="$FAKE_ACTIVE_USER"; else active="$FAKE_ACTIVE_SYSTEM"; fi
    if [ -n "$active" ] && [ "$active" = "$unit" ]; then echo "active"; exit 0; fi
    echo "inactive"; exit 3 ;;
  restart)
    if [ "$FAKE_RESTART_FAIL" = "1" ]; then echo "fake restart failed"; exit 1; fi
    echo "restart:$scope:$1"; exit 0 ;;
  *) echo "unknown"; exit 1 ;;
esac
`

// setupFakeSystemctl writes the fake systemctl into PATH and applies the
// given environment variables for one test.
func setupFakeSystemctl(t *testing.T, env map[string]string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(script, []byte(fakeSystemctlScript), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestNewRestarter(t *testing.T) {
	r := NewRestarter()
	if len(r.UnitNames) != 2 {
		t.Fatalf("expected 2 candidate units, got %d: %v", len(r.UnitNames), r.UnitNames)
	}
	if r.UnitNames[0] != "lele.service" || r.UnitNames[1] != "lele-gateway.service" {
		t.Errorf("unexpected unit names: %v", r.UnitNames)
	}
}

func TestDetectSupervisor_None(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")
	if got := NewRestarter().DetectSupervisor(); got != SupervisorNone {
		t.Errorf("DetectSupervisor = %q, want none", got)
	}
}

func TestDetectSupervisor_SystemdUser(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":          "inv-user",
		"FAKE_INVOCATION_USER":   "inv-user",
		"FAKE_INVOCATION_SYSTEM": "something-else",
	})
	if got := NewRestarter().DetectSupervisor(); got != SupervisorSystemdUser {
		t.Errorf("DetectSupervisor = %q, want systemd-user", got)
	}
}

func TestDetectSupervisor_SystemdSystem(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":          "inv-sys",
		"FAKE_INVOCATION_USER":   "other",
		"FAKE_INVOCATION_SYSTEM": "inv-sys",
		// ensure user scope does NOT match so it falls to system scope
		"FAKE_ACTIVE_USER": "",
	})
	// Because DetectSupervisor tries user scope first (matching none),
	// then system scope. But user-scope show returns "other" -> no match;
	// system-scope show returns "inv-sys" -> match.
	if got := NewRestarter().DetectSupervisor(); got != SupervisorSystemdSystem {
		t.Errorf("DetectSupervisor = %q, want systemd-system", got)
	}
}

func TestDetectSupervisor_SystemdUnidentifiedAssumesUser(t *testing.T) {
	// Under systemd (INVOCATION_ID set) but no unit matches -> assume user.
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":          "some-invocation",
		"FAKE_INVOCATION_USER":   "nope",
		"FAKE_INVOCATION_SYSTEM": "nope",
	})
	if got := NewRestarter().DetectSupervisor(); got != SupervisorSystemdUser {
		t.Errorf("DetectSupervisor = %q, want systemd-user (default)", got)
	}
}

func TestInvocationMatches_NoInvocationID(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{"INVOCATION_ID": ""})
	r := NewRestarter()
	if invocationMatches(r, true) {
		t.Error("expected false with empty INVOCATION_ID")
	}
}

func TestFindUnit_ByInvocation(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":        "inv-user",
		"FAKE_INVOCATION_USER": "inv-user",
	})
	r := NewRestarter()
	unit, ok := r.findUnit(true)
	if !ok {
		t.Fatal("expected a unit found")
	}
	if unit != "lele.service" {
		t.Errorf("unit = %q, want lele.service", unit)
	}
}

func TestFindUnit_ByActiveState(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":    "",
		"FAKE_ACTIVE_USER": "lele-gateway.service",
	})
	r := NewRestarter()
	unit, ok := r.findUnit(true)
	if !ok {
		t.Fatal("expected a unit found by active state")
	}
	if unit != "lele-gateway.service" {
		t.Errorf("unit = %q, want lele-gateway.service", unit)
	}
}

func TestFindUnit_NoMatch(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":      "",
		"FAKE_ACTIVE_USER":   "",
		"FAKE_ACTIVE_SYSTEM": "",
	})
	r := NewRestarter()
	if unit, ok := r.findUnit(true); ok {
		t.Errorf("expected no unit, got %q", unit)
	}
}

func TestRestart_SystemdUser_Found(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":          "inv-user",
		"FAKE_INVOCATION_USER":   "inv-user",
		"FAKE_INVOCATION_SYSTEM": "",
	})
	r := NewRestarter()
	desc, err := r.Restart()
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	wantUser := fmt.Sprintf("systemctl --user restart %s", "lele.service")
	if desc != wantUser {
		t.Errorf("desc = %q, want %q", desc, wantUser)
	}
}

func TestRestart_SystemdUser_Fallback(t *testing.T) {
	// INVOCATION set, but no unit matches -> fallback to lele.service.
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":          "x",
		"FAKE_INVOCATION_USER":   "x",
		"FAKE_INVOCATION_SYSTEM": "",
	})
	r := NewRestarter()
	desc, err := r.Restart()
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if desc != "systemctl --user restart lele.service" {
		t.Errorf("desc = %q", desc)
	}
}

func TestRestart_SystemdSystem(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":          "inv-sys",
		"FAKE_INVOCATION_USER":   "other",
		"FAKE_INVOCATION_SYSTEM": "inv-sys",
	})
	r := NewRestarter()
	desc, err := r.Restart()
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if desc != "systemctl restart lele.service" {
		t.Errorf("desc = %q", desc)
	}
}

func TestRestart_Systemd_Failure(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":        "inv-user",
		"FAKE_INVOCATION_USER": "inv-user",
		"FAKE_RESTART_FAIL":    "1",
	})
	r := NewRestarter()
	if _, err := r.Restart(); err == nil {
		t.Fatal("expected restart failure")
	}
}

func TestRestart_SelfExec(t *testing.T) {
	// No supervisor -> spawn a detached copy of the test binary with
	// LELE_UPDATE_CHILD=1 so the child exits immediately (guarded by
	// TestMain). Restart() waits 200ms before returning; give it time.
	t.Setenv("INVOCATION_ID", "")
	t.Setenv("LELE_UPDATE_CHILD", "1")
	r := NewRestarter()
	desc, err := r.Restart()
	if err != nil {
		t.Fatalf("Restart self-exec: %v", err)
	}
	if desc != "self-exec" {
		t.Errorf("desc = %q, want self-exec", desc)
	}
}

func TestRestartService_UserFound(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":      "",
		"FAKE_ACTIVE_USER":   "lele.service",
		"FAKE_ACTIVE_SYSTEM": "",
	})
	r := NewRestarter()
	desc, err := r.RestartService()
	if err != nil {
		t.Fatalf("RestartService: %v", err)
	}
	if desc != "systemctl --user restart lele.service" {
		t.Errorf("desc = %q", desc)
	}
}

func TestRestartService_SystemFound(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":      "",
		"FAKE_ACTIVE_USER":   "",
		"FAKE_ACTIVE_SYSTEM": "lele-gateway.service",
	})
	r := NewRestarter()
	desc, err := r.RestartService()
	if err != nil {
		t.Fatalf("RestartService: %v", err)
	}
	if desc != "systemctl restart lele-gateway.service" {
		t.Errorf("desc = %q", desc)
	}
}

func TestRestartService_None(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"INVOCATION_ID":      "",
		"FAKE_ACTIVE_USER":   "",
		"FAKE_ACTIVE_SYSTEM": "",
	})
	r := NewRestarter()
	if _, err := r.RestartService(); err == nil {
		t.Fatal("expected error when no managed service found")
	}
}

func TestSystemctlOutput(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{
		"FAKE_INVOCATION_USER": "abc",
	})
	out, err := systemctlOutput(true, "show", "lele.service")
	if err != nil {
		t.Fatalf("systemctlOutput: %v", err)
	}
	if out != "abc\n" {
		t.Errorf("output = %q, want %q", out, "abc\n")
	}
}

func TestSystemctlRestart(t *testing.T) {
	setupFakeSystemctl(t, nil)
	if err := systemctlRestart(true, "lele.service"); err != nil {
		t.Fatalf("systemctlRestart: %v", err)
	}
}

func TestSystemctlRestart_Fail(t *testing.T) {
	setupFakeSystemctl(t, map[string]string{"FAKE_RESTART_FAIL": "1"})
	if err := systemctlRestart(true, "lele.service"); err == nil {
		t.Fatal("expected restart failure")
	}
}

func TestSetDetachFlags(t *testing.T) {
	cmd := exec.Command("true")
	setDetachFlags(cmd)
	if cmd.SysProcAttr == nil {
		t.Error("expected SysProcAttr to be set")
	}
}
