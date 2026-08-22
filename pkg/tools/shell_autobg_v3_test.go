package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestExecTool_Execute_AutoBackground exercises the heartbeat auto-background
// branch (shell.go:331-351) by setting a very low background threshold while
// running a longer command without the explicit background flag.
func TestExecTool_Execute_AutoBackground(t *testing.T) {
	tool := NewExecTool("", false)
	mgr := NewBackgroundProcessManager()
	tool.SetBackgroundManager(mgr)
	tool.SetBackgroundThreshold(10 * time.Millisecond)

	ctx := WithToolContext(context.Background(), "chan-ab", "chat-ab")
	res := tool.Execute(ctx, map[string]interface{}{
		"command": "sleep 8",
	})
	if res == nil {
		t.Fatal("nil result")
	}
	if !strings.Contains(res.ForLLM, "Moved to background") {
		t.Fatalf("expected auto-background message, got: %s", res.ForLLM)
	}
	if mgr.Count() != 1 {
		t.Errorf("expected 1 background process, got %d", mgr.Count())
	}
	// Wait for the follow-up goroutine to finish.
	deadline := time.Now().Add(3 * time.Second)
	p := mgr.List()[0]
	for time.Now().Before(deadline) {
		p.mu.RLock()
		st := p.Status
		p.mu.RUnlock()
		if st != BgExecStatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mgr.StopAll()
}

// TestExecTool_Execute_ExitErrorCodeCoverage exercises the ExitError branch in
// the background goroutine (shell.go:293-298) by running a failing command in
// explicit background mode.
func TestExecTool_Execute_ExitErrorCodeCoverage(t *testing.T) {
	tool := NewExecTool("", false)
	mgr := NewBackgroundProcessManager()
	tool.SetBackgroundManager(mgr)
	tool.SetBackgroundThreshold(0)

	ctx := WithToolContext(context.Background(), "chan-ec", "chat-ec")
	res := tool.Execute(ctx, map[string]interface{}{
		"command":    "exit 7",
		"background": true,
	})
	if res.IsError {
		t.Fatalf("background execution should not report error: %s", res.ForLLM)
	}

	// Wait for the goroutine to mark completed.
	p := mgr.List()[0]
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.RLock()
		ec := p.ExitCode
		st := p.Status
		p.mu.RUnlock()
		if st != BgExecStatusRunning {
			_ = ec
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mgr.StopAll()
}