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

type Supervisor string

const (
	SupervisorSystemdUser   Supervisor = "systemd-user"
	SupervisorSystemdSystem Supervisor = "systemd-system"
	SupervisorNone          Supervisor = "none"
)

type Restarter struct {
	UnitNames []string

	Detect func() Supervisor

	OnRestart func(method string)

	Exit func(code int)

	spawn func(exe string, args []string) error

	runSystemctl func(userScope bool, args ...string) (string, error)
}

func NewRestarter() *Restarter {
	return &Restarter{UnitNames: []string{"lele.service", "lele-gateway.service"}}
}

func (r *Restarter) detect() Supervisor {
	if r.Detect != nil {
		return r.Detect()
	}
	return r.DetectSupervisor()
}

func (r *Restarter) systemctl(userScope bool, args ...string) (string, error) {
	if r.runSystemctl != nil {
		return r.runSystemctl(userScope, args...)
	}
	return systemctlOutput(userScope, args...)
}

func (r *Restarter) DetectSupervisor() Supervisor {
	if os.Getenv("INVOCATION_ID") != "" {
		if invocationMatches(r, true) {
			return SupervisorSystemdUser
		}
		if invocationMatches(r, false) {
			return SupervisorSystemdSystem
		}
		return SupervisorSystemdUser
	}
	return SupervisorNone
}

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

func (r *Restarter) findUnit(userScope bool) (string, bool) {
	invocationID := os.Getenv("INVOCATION_ID")
	for _, unit := range r.UnitNames {
		if invocationID != "" {
			if id, err := r.systemctl(userScope, "show", "-p", "InvocationID", "--value", unit); err == nil &&
				strings.TrimSpace(id) == invocationID {
				return unit, true
			}
			continue
		}
		if state, err := r.systemctl(userScope, "is-active", unit); err == nil &&
			strings.TrimSpace(state) == "active" {
			return unit, true
		}
	}
	return "", false
}

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

func (r *Restarter) systemctlRestart(userScope bool, unit string) error {
	_, err := r.systemctl(userScope, "restart", unit)
	return err
}

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

func restartDryRun() bool {
	return os.Getenv("LELE_RESTART_DRY_RUN") == "1"
}

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

func spawnReplacement(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.Env = restartChildEnv()
	setDetachFlags(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawning new process: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	time.Sleep(200 * time.Millisecond)
	return nil
}

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

const RestartChildEnvKey = "LELE_RESTART_CHILD"
