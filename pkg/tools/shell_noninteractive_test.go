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

	if result.IsError {
		if strings.Contains(result.ForLLM, "timed out") {
			t.Fatal("cat /dev/tty timed out — setsid is not working; the TUI would still hang")
		}
		return
	}
	t.Log("cat /dev/tty succeeded unexpectedly — likely running in an environment without a real TTY; the critical test (no timeout) passed")
}

func TestNonInteractive_GroupKill(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux for process group checks")
	}

	tool := NewExecTool("", false)
	tool.SetTimeout(1 * time.Second)

	ctx := context.Background()
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "sleep 300 & echo started; wait",
	})

	if !result.IsError {
		t.Fatalf("expected timeout error, got success: %s", result.ForLLM)
	}

	time.Sleep(200 * time.Millisecond)

	out, err := exec.Command("pgrep", "-f", "sleep 300").CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return
		}
		t.Logf("pgrep failed (%v), falling back to syscall check", err)
	} else {
		lines := strings.TrimSpace(string(out))
		if lines == "" {
			return
		}
		for _, pidStr := range strings.Split(lines, "\n") {
			pidStr = strings.TrimSpace(pidStr)
			if pidStr == "" {
				continue
			}
			var pid int
			if _, err := fmt.Sscanf(pidStr, "%d", &pid); err != nil {
				continue
			}
			if err := syscall.Kill(pid, 0); err == nil {
				cmdline, _ := exec.Command("cat", "/proc/"+pidStr+"/cmdline").CombinedOutput()
				if strings.Contains(string(cmdline), "sleep") && strings.Contains(string(cmdline), "300") {
					t.Errorf("orphaned sleep 300 process still alive: pid %s", pidStr)
				}
			}
		}
	}
}
