package update

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Supervisor describes how the lele process is managed.
type Supervisor string

const (
	SupervisorSystemdUser   Supervisor = "systemd-user"
	SupervisorSystemdSystem Supervisor = "systemd-system"
	SupervisorNone          Supervisor = "none"
)

// Restarter restarts the lele service after an update.
type Restarter struct {
	// UnitNames are candidate systemd unit names to probe.
	UnitNames []string
}

// NewRestarter creates a Restarter with default candidate unit names.
func NewRestarter() *Restarter {
	return &Restarter{UnitNames: []string{"lele.service", "lele-gateway.service"}}
}

// DetectSupervisor determines how the current process is managed.
func (r *Restarter) DetectSupervisor() Supervisor {
	if os.Getenv("INVOCATION_ID") != "" {
		// Running under systemd. Determine user vs system scope.
		if invocationMatches(r, true) {
			return SupervisorSystemdUser
		}
		if invocationMatches(r, false) {
			return SupervisorSystemdSystem
		}
		// Under systemd but unit not identified; assume user scope.
		return SupervisorSystemdUser
	}
	return SupervisorNone
}

// invocationMatches checks if $INVOCATION_ID matches any candidate unit.
func invocationMatches(r *Restarter, userScope bool) bool {
	invocationID := os.Getenv("INVOCATION_ID")
	if invocationID == "" {
		return false
	}
	for _, unit := range r.UnitNames {
		id, err := systemctlOutput(userScope, "show", "-p", "InvocationID", "--value", unit)
		if err == nil && strings.TrimSpace(id) == invocationID {
			return true
		}
	}
	return false
}

// findUnit returns the candidate unit that is currently loaded, if any.
func (r *Restarter) findUnit(userScope bool) (string, bool) {
	// Prefer matching by invocation ID when available.
	invocationID := os.Getenv("INVOCATION_ID")
	for _, unit := range r.UnitNames {
		if invocationID != "" {
			if id, err := systemctlOutput(userScope, "show", "-p", "InvocationID", "--value", unit); err == nil &&
				strings.TrimSpace(id) == invocationID {
				return unit, true
			}
			continue
		}
		// No invocation ID: fall back to checking active state.
		if state, err := systemctlOutput(userScope, "is-active", unit); err == nil &&
			strings.TrimSpace(state) == "active" {
			return unit, true
		}
	}
	return "", false
}

// Restart restarts the lele service from within the running process.
// It returns a description of the method used. When no supervisor is
// detected, it spawns a detached copy of the current executable
// (self-exec); the caller should exit afterwards.
func (r *Restarter) Restart() (string, error) {
	switch r.DetectSupervisor() {
	case SupervisorSystemdUser:
		if unit, ok := r.findUnit(true); ok {
			return "systemctl --user restart " + unit, systemctlRestart(true, unit)
		}
		return "systemctl --user restart lele.service", systemctlRestart(true, "lele.service")
	case SupervisorSystemdSystem:
		if unit, ok := r.findUnit(false); ok {
			return "systemctl restart " + unit, systemctlRestart(false, unit)
		}
		return "systemctl restart lele.service", systemctlRestart(false, "lele.service")
	}

	if err := r.selfExec(); err != nil {
		return "", err
	}
	return "self-exec", nil
}

// RestartService restarts the lele service from an external process
// (e.g. the `lele update` CLI). It probes for an active systemd unit
// and restarts it. Returns an error if no managed service is found.
func (r *Restarter) RestartService() (string, error) {
	if unit, ok := r.findUnit(true); ok {
		return "systemctl --user restart " + unit, systemctlRestart(true, unit)
	}
	if unit, ok := r.findUnit(false); ok {
		return "systemctl restart " + unit, systemctlRestart(false, unit)
	}
	return "", fmt.Errorf("no managed lele service found (systemd); restart it manually")
}

func systemctlRestart(userScope bool, unit string) error {
	_, err := systemctlOutput(userScope, "restart", unit)
	return err
}

func systemctlOutput(userScope bool, args ...string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("systemctl not available on Windows")
	}
	full := args
	if userScope {
		full = append([]string{"--user"}, args...)
	}
	cmd := exec.Command("systemctl", full...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// selfExec spawns a detached copy of the current process with the same
// arguments, then the caller should exit.
func (r *Restarter) selfExec() error {
	exe, err := CurrentBinaryPath()
	if err != nil {
		return err
	}
	args := os.Args[1:]
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	setDetachFlags(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawning new process: %w", err)
	}
	// Detach: don't Wait.
	go func() { _ = cmd.Wait() }()
	time.Sleep(200 * time.Millisecond)
	return nil
}
