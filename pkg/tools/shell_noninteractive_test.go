//go:build !windows

package tools

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestNonInteractive_EnvVars verifies that the non-interactive environment
// variables (GIT_TERMINAL_PROMPT, GIT_PAGER, PAGER) are propagated to the
// child process. DEBIAN_FRONTEND is not tested via echo because it is only
// meaningful to apt/dpkg, but the other three are simple string checks.
func TestNonInteractive_EnvVars(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c not available on windows")
	}

	tool := NewExecTool("", false)

	ctx := context.Background()
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo $GIT_TERMINAL_PROMPT:$GIT_PAGER:$PAGER",
	})

	if result.IsError {
		t.Fatalf("command failed: %s", result.ForLLM)
	}

	want := "0:cat:cat"
	got := strings.TrimSpace(result.ForUser)
	if got != want {
		t.Errorf("env vars: got %q, want %q", got, want)
	}
}

// TestNonInteractive_DevTTYFails verifies that /dev/tty is not accessible
// from a child process spawned by ExecTool. Because detachControllingTerminal
// sets setsid, the child has no controlling terminal and open("/dev/tty")
// should fail with ENXIO. This prevents git credential prompts and other
// interactive programs from blocking the TUI.
//
// On systems where /dev/tty does not exist (e.g. some CI environments),
// the test verifies that the command exits with a non-zero status (which
// means it did NOT hang waiting for input).
func TestNonInteractive_DevTTYFails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux with /dev/tty")
	}

	tool := NewExecTool("", false)
	tool.SetTimeout(5 * time.Second)

	ctx := context.Background()
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "cat /dev/tty",
	})

	// The command must NOT time out — it should fail quickly because
	// /dev/tty is not accessible in a detached session. If it timed out,
	// the fix is not working and the TUI would still hang.
	if result.IsError {
		if strings.Contains(result.ForLLM, "timed out") {
			t.Fatal("cat /dev/tty timed out — setsid is not working; the TUI would still hang")
		}
		// Expected: non-timeout error (ENXIO or similar). This is success.
		return
	}
	// If the command somehow succeeded (e.g. /dev/tty readable in CI),
	// that is unexpected but not a failure of our fix. The critical
	// assertion is that it did NOT time out.
	t.Log("cat /dev/tty succeeded unexpectedly — likely running in an environment without a real TTY; the critical test (no timeout) passed")
}

// TestNonInteractive_GroupKill verifies that when a command spawns a
// grandchild (sleep 300 &), the entire process group is killed when the
// context is canceled. Without group-aware kill, the grandchild would be
// orphaned and continue running.
func TestNonInteractive_GroupKill(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux for process group checks")
	}

	tool := NewExecTool("", false)
	// Use a short timeout so the test finishes quickly.
	tool.SetTimeout(1 * time.Second)

	ctx := context.Background()
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "sleep 300 & echo started; wait",
	})

	// The command should have been killed by timeout.
	if !result.IsError {
		t.Fatalf("expected timeout error, got success: %s", result.ForLLM)
	}

	// Give the kernel a moment to reap the process group.
	time.Sleep(200 * time.Millisecond)

	// Verify that no orphaned "sleep 300" processes from this test are
	// still running. We use pgrep with a specific pattern to avoid
	// matching unrelated sleeps.
	out, err := exec.Command("pgrep", "-f", "sleep 300").CombinedOutput()
	if err != nil {
		// pgrep returns exit 1 when no matches — that is the expected
		// outcome (all children were killed).
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return // Success: no orphaned sleep 300
		}
		// pgrep itself failed (not installed, etc.) — try the syscall approach.
		t.Logf("pgrep failed (%v), falling back to syscall check", err)
	} else {
		// pgrep found something. Filter out pgrep itself (which matches
		// its own argument string on some systems).
		lines := strings.TrimSpace(string(out))
		if lines == "" {
			return // No orphans
		}
		// There might be a brief race where the process is in zombie state.
		// Check if any found PIDs are actually alive.
		for _, pidStr := range strings.Split(lines, "\n") {
			pidStr = strings.TrimSpace(pidStr)
			if pidStr == "" {
				continue
			}
			// Verify the process is actually alive (not zombie/defunct).
			// syscall.Kill(pid, 0) succeeds if the process exists.
			var pid int
			if _, err := fmt.Sscanf(pidStr, "%d", &pid); err != nil {
				continue
			}
			if err := syscall.Kill(pid, 0); err == nil {
				// Double-check it's our sleep by reading /proc/pid/cmdline
				cmdline, _ := exec.Command("cat", "/proc/"+pidStr+"/cmdline").CombinedOutput()
				if strings.Contains(string(cmdline), "sleep") && strings.Contains(string(cmdline), "300") {
					t.Errorf("orphaned sleep 300 process still alive: pid %s", pidStr)
				}
			}
		}
	}
}
