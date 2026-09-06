package update

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
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

	// Detect reports how the current process is managed. Defaults to
	// DetectSupervisor; injectable so tests can pin a supervisor path without a
	// real systemd.
	Detect func() Supervisor

	// OnRestart, when set, is called with the restart method right before the
	// process terminates itself on the self-exec path. The gateway uses it to
	// run its shutdown coordinator, so a self-restart tears down as gracefully
	// as a SIGTERM does. It is never called on the systemd paths, where the
	// supervisor sends SIGTERM and the normal graceful path runs.
	OnRestart func(method string)

	// Exit terminates the process with the given code. Defaults to os.Exit.
	// Injectable so tests can assert that the self-exec path exits the parent
	// without killing the test binary.
	Exit func(code int)

	// spawn launches the replacement process and waits long enough to know it
	// started. Defaults to spawnReplacement. Injectable so tests can assert the
	// parent/child handoff (what the child inherits, and that a failed spawn
	// keeps the parent alive) without exec'ing anything real.
	spawn func(exe string, args []string) error

	// runSystemctl executes systemctl and returns its combined output. Defaults
	// to systemctlOutput. Injectable so tests never touch the real service
	// manager: a stray "systemctl restart lele.service" from a unit test would
	// take down the host service.
	runSystemctl func(userScope bool, args ...string) (string, error)
}

// NewRestarter creates a Restarter with default candidate unit names.
func NewRestarter() *Restarter {
	return &Restarter{UnitNames: []string{"lele.service", "lele-gateway.service"}}
}

// detect returns the supervisor for the current process, honouring the test
// seam in Detect.
func (r *Restarter) detect() Supervisor {
	if r.Detect != nil {
		return r.Detect()
	}
	return r.DetectSupervisor()
}

// systemctl runs systemctl through the (possibly injected) runner.
func (r *Restarter) systemctl(userScope bool, args ...string) (string, error) {
	if r.runSystemctl != nil {
		return r.runSystemctl(userScope, args...)
	}
	return systemctlOutput(userScope, args...)
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
		id, err := r.systemctl(userScope, "show", "-p", "InvocationID", "--value", unit)
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
			if id, err := r.systemctl(userScope, "show", "-p", "InvocationID", "--value", unit); err == nil &&
				strings.TrimSpace(id) == invocationID {
				return unit, true
			}
			continue
		}
		// No invocation ID: fall back to checking active state.
		if state, err := r.systemctl(userScope, "is-active", unit); err == nil &&
			strings.TrimSpace(state) == "active" {
			return unit, true
		}
	}
	return "", false
}

// Restart restarts the lele service from within the running process.
// It returns a description of the method used.
//
// When no supervisor is detected it spawns a detached copy of the current
// executable (self-exec) and then terminates THIS process: the spawned child
// cannot acquire the desktop instance lock while the parent lives, because
// pkg/lockfile is a PID file with a kill(pid,0) liveness check, so returning
// here used to leave the restart half-done (the child saw *AlreadyRunningError
// and exited). Before exiting, OnRestart is called so the owner of the
// Restarter can run its graceful teardown (shutdown coordinator) exactly as it
// would on SIGTERM.
//
// On the systemd paths it does NOT exit: systemd sends SIGTERM to this process,
// which runs the normal graceful path.
func (r *Restarter) Restart() (string, error) {
	switch r.detect() {
	case SupervisorSystemdUser:
		if unit, ok := r.findUnit(true); ok {
			return "systemctl --user restart " + unit, r.systemctlRestart(true, unit)
		}
		return "systemctl --user restart lele.service", r.systemctlRestart(true, "lele.service")
	case SupervisorSystemdSystem:
		if unit, ok := r.findUnit(false); ok {
			return "systemctl restart " + unit, r.systemctlRestart(false, unit)
		}
		return "systemctl restart lele.service", r.systemctlRestart(false, "lele.service")
	}

	if err := r.selfExec(); err != nil {
		return "", err
	}
	method := "self-exec"
	if r.OnRestart != nil {
		r.OnRestart(method)
	}
	r.exitParent()
	return method, nil
}

// systemctlRestart asks systemd to restart unit.
func (r *Restarter) systemctlRestart(userScope bool, unit string) error {
	_, err := r.systemctl(userScope, "restart", unit)
	return err
}

// exitParent terminates the process after the replacement child is running.
// An injected Exit hook always runs (tests assert the exit sequence); the real
// os.Exit is skipped in dry-run mode so the process survives.
func (r *Restarter) exitParent() {
	if r.Exit != nil {
		r.Exit(0)
		return
	}
	if restartDryRun() {
		return
	}
	os.Exit(0)
}

// restartDryRun reports whether a restart must be simulated instead of really
// performed. In dry-run mode self-exec spawns nothing and the default exit is
// skipped, while the restart sequence (OnRestart, reported method) stays
// observable. Used by tests and by manual debugging of the update flow.
func restartDryRun() bool {
	return os.Getenv("LELE_RESTART_DRY_RUN") == "1"
}

// RestartService restarts the lele service from an external process
// (e.g. the `lele update` CLI). It probes for an active systemd unit
// and restarts it. Returns an error if no managed service is found.
// It never terminates the calling process.
func (r *Restarter) RestartService() (string, error) {
	if unit, ok := r.findUnit(true); ok {
		return "systemctl --user restart " + unit, r.systemctlRestart(true, unit)
	}
	if unit, ok := r.findUnit(false); ok {
		return "systemctl restart " + unit, r.systemctlRestart(false, unit)
	}
	return "", fmt.Errorf("no managed lele service found (systemd); restart it manually")
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
// arguments. The caller is expected to exit afterwards (see Restart).
func (r *Restarter) selfExec() error {
	if restartDryRun() {
		logger.InfoCF("update", "Restart dry-run: skipping self-exec spawn", map[string]interface{}{
			"dry_run": true,
		})
		return nil
	}
	exe, err := CurrentBinaryPath()
	if err != nil {
		return err
	}
	spawn := r.spawn
	if spawn == nil {
		spawn = spawnReplacement
	}
	return spawn(exe, os.Args[1:])
}

// spawnReplacement launches exe with args, detached from this process, and
// marks it as a restart replacement. It waits briefly so a spawn that fails
// immediately (bad path, missing permissions) is still reported as an error and
// the parent keeps running instead of exiting into a world with no instance.
func spawnReplacement(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Mark the child as a restart replacement so it waits for the instance
	// lock instead of bailing out with "already_running" (the parent still
	// holds it while it drains and runs its shutdown hooks).
	cmd.Env = restartChildEnv()
	setDetachFlags(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawning new process: %w", err)
	}
	// Detach: don't Wait.
	go func() { _ = cmd.Wait() }()
	time.Sleep(200 * time.Millisecond)
	return nil
}

// restartChildEnv builds the environment for the replacement process: this
// process's environment plus the restart-replacement marker. Split out from
// spawnReplacement so the marker is directly testable.
//
// Any pre-existing marker is dropped rather than appended over: os/exec keeps
// the last occurrence of a duplicated key, but relying on that would make the
// handoff depend on a stdlib detail instead of on what this function returns.
func restartChildEnv() []string {
	marker := RestartChildEnvKey + "="
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, marker) {
			continue
		}
		env = append(env, kv)
	}
	return append(env, marker+"1")
}

// RestartChildEnvKey is set in the environment of the process spawned by
// selfExec. It tells the child that it is a restart replacement, not a second
// concurrent instance: the previous process is still alive while it runs its
// shutdown hooks (and holds the desktop instance lock until the very last
// hook), so the child must wait for that lock instead of reporting
// "already_running".
const RestartChildEnvKey = "LELE_RESTART_CHILD"
