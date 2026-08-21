package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestGatewayCmdSubprocess runs the real gateway via the test-binary subprocess
// (LELE_TEST_MAIN=gateway), waits for it to initialize, then sends SIGTERM and
// asserts a clean shutdown. This exercises the bulk of gatewayCmd() including
// config load, agent/channel manager setup, unified server binding, cron and
// heartbeat services, and the graceful shutdown path.
func TestGatewayCmdSubprocess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Agents.Defaults.Workspace = filepath.Join(dir, "workspace")
	saveConfigAt(t, dir, cfg)

	cmd := exec.Command(os.Args[0], "-test.run=TestGatewaySubprocessPlaceholder")
	cmd.Env = append(os.Environ(),
		"LELE_TEST_MAIN=gateway",
		"LELE_CONFIG_DIR="+dir,
	)
	done := make(chan struct{})
	var output []byte
	go func() {
		output, _ = cmd.CombinedOutput()
		close(done)
	}()

	// Give the gateway time to initialize and bind before we signal it.
	time.Sleep(2 * time.Second)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		cmd.Process.Kill()
		t.Fatalf("send SIGTERM: %v", err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("gateway subprocess did not exit in time")
	}
	<-done

	out := string(output)
	for _, want := range []string{
		"Agent Status",
		"Unified server starting",
		"Cron service started",
		"Heartbeat service started",
		"Shutting down",
		"Gateway stopped",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("gateway output missing %q\noutput:\n%s", want, out)
		}
	}
}

// TestGatewaySubprocessPlaceholder exists only so the test binary can be
// selected with -test.run for the subprocess invocation.
func TestGatewaySubprocessPlaceholder(t *testing.T) {}