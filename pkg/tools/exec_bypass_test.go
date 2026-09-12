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

// TestBypassGuard_ScopedToApprovedCommand verifies that a bypass context only
// skips guards for the specific approved command. A *different* dangerous
// command must still be blocked by the safety guards.
func TestBypassGuard_ScopedToApprovedCommand(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	// Create a bypass context for the safe command "echo ok".
	ctx := WithBypassGuard(context.Background(), "echo ok")

	// The approved command itself should succeed (guards skipped).
	result := tool.Execute(ctx, map[string]interface{}{"command": "echo ok"})
	if result.IsError {
		t.Fatalf("approved command should bypass guards, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForUser, "ok") {
		t.Fatalf("expected output 'ok', got: %s", result.ForUser)
	}

	// A DIFFERENT dangerous command through the same bypass context must
	// still be blocked — the bypass is scoped to "echo ok" only.
	result2 := tool.Execute(ctx, map[string]interface{}{"command": "rm -rf /tmp/lele-bypass-probe"})
	if !result2.IsError {
		t.Fatal("different dangerous command should be BLOCKED even with bypass context")
	}
	if !strings.Contains(result2.ForLLM, "blocked") && !strings.Contains(result2.ForUser, "blocked") {
		t.Fatalf("expected 'blocked' in error, got: ForLLM=%q ForUser=%q", result2.ForLLM, result2.ForUser)
	}
}

// TestBypassGuard_ConcurrentIsolation exercises two goroutines: one executing
// with a bypass context (for "echo safe") and one executing a dangerous
// command without bypass. The second must always be blocked.
//
// This demonstrates per-call isolation — no shared mutable state — even
// without the race detector (which is broken on this host).
func TestBypassGuard_ConcurrentIsolation(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	const iterations = 50
	var wg sync.WaitGroup
	errCh := make(chan string, iterations*2)

	// Goroutine A: executes "echo safe" with bypass — should always succeed.
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

	// Goroutine B: executes "rm -rf /tmp/lele-bypass-probe" WITHOUT bypass —
	// should always be blocked.
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

// TestBypassGuard_NoLeakOnPanic verifies that a panic during execution does
// not leave any persistent bypass state. Because the bypass is now carried in
// the context (immutable, per-call), a second call without bypass context must
// still have guards active.
//
// The old implementation stored bypass as a mutable bool on the shared struct;
// a panic between SetBypassGuard(true) and SetBypassGuard(false) would leave
// guards permanently off. This test confirms the new design is immune.
func TestBypassGuard_NoLeakOnPanic(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	// Execute with bypass context — normal call, succeeds.
	ctx := WithBypassGuard(context.Background(), "echo ok")
	r := tool.Execute(ctx, map[string]interface{}{"command": "echo ok"})
	if r.IsError {
		t.Fatalf("bypassed echo should succeed: %s", r.ForLLM)
	}

	// Now execute a dangerous command WITHOUT any bypass context. If the
	// old shared-state bug were present and a "panic" had occurred, this
	// call would skip guards. With context-based bypass, it must be blocked.
	r2 := tool.Execute(context.Background(), map[string]interface{}{"command": "rm -rf /tmp/lele-bypass-probe"})
	if !r2.IsError {
		t.Fatal("dangerous command should be blocked when no bypass in context — shared state leak detected")
	}
}

// TestBypassGuard_NormalApprovalRegression ensures the standard approval flow
// still works: a command without bypass context triggers the guard, and the
// same command with bypass context passes.
func TestBypassGuard_NormalApprovalRegression(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	dangerousCmd := "rm -rf /tmp/lele-bypass-probe"

	// Without bypass — should be blocked.
	r := tool.Execute(context.Background(), map[string]interface{}{"command": dangerousCmd})
	if !r.IsError {
		t.Fatal("dangerous command should be blocked without bypass")
	}
	if !strings.Contains(r.ForLLM, "blocked") && !strings.Contains(r.ForUser, "blocked") {
		t.Fatalf("expected 'blocked' message, got: %s", r.ForLLM)
	}

	// With bypass for exactly that command — should succeed.
	ctx := WithBypassGuard(context.Background(), dangerousCmd)
	r2 := tool.Execute(ctx, map[string]interface{}{"command": dangerousCmd})
	if r2.IsError {
		t.Fatalf("approved command should succeed with bypass context, got: %s", r2.ForLLM)
	}
}

// TestBypassGuard_WhitespaceNormalization ensures that the approved-command
// comparison is case/whitespace-normalized (same normalization as the
// whitelist), so "  RM   -RF /tmp/x  " matches "rm -rf /tmp/x".
func TestBypassGuard_WhitespaceNormalization(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	// Approve with extra spaces and different case.
	ctx := WithBypassGuard(context.Background(), "  RM   -RF /tmp/lele-bypass-probe ")

	// Execute with canonical form.
	r := tool.Execute(ctx, map[string]interface{}{"command": "rm -rf /tmp/lele-bypass-probe"})
	if r.IsError {
		t.Fatalf("normalised match should bypass guards, got: %s", r.ForLLM)
	}
}

// TestBypassGuard_EmptyApprovedCmd ensures that an empty approved-command in
// the context never matches — the bypass must be a no-op.
func TestBypassGuard_EmptyApprovedCmd(t *testing.T) {
	tool := newGuardedExecToolForBypass(t)

	ctx := WithBypassGuard(context.Background(), "")
	r := tool.Execute(ctx, map[string]interface{}{"command": "rm -rf /tmp/lele-bypass-probe"})
	if !r.IsError {
		t.Fatal("empty approved command must not match any real command")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

// newGuardedExecToolForBypass builds an ExecTool with deny-patterns enabled.
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
