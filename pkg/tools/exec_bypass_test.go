package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestBypassGuard_ScopedToApprovedCommand(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	ctx := WithBypassGuard(context.Background(), "echo ok")

	result := tool.Execute(ctx, map[string]interface{}{"command": "echo ok"})
	if result.IsError {
		t.Fatalf("approved command should bypass guards, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForUser, "ok") {
		t.Fatalf("expected output 'ok', got: %s", result.ForUser)
	}

	result2 := tool.Execute(ctx, map[string]interface{}{"command": "rm -rf /tmp/lele-bypass-probe"})
	if !result2.IsError {
		t.Fatal("different dangerous command should be BLOCKED even with bypass context")
	}
	if !strings.Contains(result2.ForLLM, "blocked") && !strings.Contains(result2.ForUser, "blocked") {
		t.Fatalf("expected 'blocked' in error, got: ForLLM=%q ForUser=%q", result2.ForLLM, result2.ForUser)
	}
}

func TestBypassGuard_ConcurrentIsolation(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	const iterations = 50
	var wg sync.WaitGroup
	errCh := make(chan string, iterations*2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			ctx := WithBypassGuard(context.Background(), "echo safe")
			r := tool.Execute(ctx, map[string]interface{}{"command": "echo safe"})
			if r.IsError {
				errCh <- "A: echo safe was blocked unexpectedly"
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			r := tool.Execute(context.Background(), map[string]interface{}{"command": "rm -rf /tmp/lele-bypass-probe"})
			if !r.IsError {
				errCh <- "B: rm -rf was NOT blocked (guards bypassed)"
			}
		}
	}()

	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
}

func TestBypassGuard_NoLeakOnPanic(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	ctx := WithBypassGuard(context.Background(), "echo ok")
	r := tool.Execute(ctx, map[string]interface{}{"command": "echo ok"})
	if r.IsError {
		t.Fatalf("bypassed echo should succeed: %s", r.ForLLM)
	}

	r2 := tool.Execute(context.Background(), map[string]interface{}{"command": "rm -rf /tmp/lele-bypass-probe"})
	if !r2.IsError {
		t.Fatal("dangerous command should be blocked when no bypass in context — shared state leak detected")
	}
}

func TestBypassGuard_NormalApprovalRegression(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	dangerousCmd := "rm -rf /tmp/lele-bypass-probe"

	r := tool.Execute(context.Background(), map[string]interface{}{"command": dangerousCmd})
	if !r.IsError {
		t.Fatal("dangerous command should be blocked without bypass")
	}
	if !strings.Contains(r.ForLLM, "blocked") && !strings.Contains(r.ForUser, "blocked") {
		t.Fatalf("expected 'blocked' message, got: %s", r.ForLLM)
	}

	ctx := WithBypassGuard(context.Background(), dangerousCmd)
	r2 := tool.Execute(ctx, map[string]interface{}{"command": dangerousCmd})
	if r2.IsError {
		t.Fatalf("approved command should succeed with bypass context, got: %s", r2.ForLLM)
	}
}

func TestBypassGuard_WhitespaceNormalization(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	ctx := WithBypassGuard(context.Background(), "  RM   -RF /tmp/lele-bypass-probe ")

	r := tool.Execute(ctx, map[string]interface{}{"command": "rm -rf /tmp/lele-bypass-probe"})
	if r.IsError {
		t.Fatalf("normalised match should bypass guards, got: %s", r.ForLLM)
	}
}

func TestBypassGuard_EmptyApprovedCmd(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	ctx := WithBypassGuard(context.Background(), "")
	r := tool.Execute(ctx, map[string]interface{}{"command": "rm -rf /tmp/lele-bypass-probe"})
	if !r.IsError {
		t.Fatal("empty approved command must not match any real command")
	}
}

func newGuardedExecToolForBypass(t *testing.T) *ExecTool {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	return NewExecToolWithConfig(ws, false, cfg)
}
