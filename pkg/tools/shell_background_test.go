package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAutoBackgroundDelay_Matrix(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "default 60s timeout", timeout: 60 * time.Second, want: 55 * time.Second},
		{name: "just above margin", timeout: 6 * time.Second, want: 1 * time.Second},
		{name: "above the cap boundary", timeout: 61 * time.Second, want: 56 * time.Second},
		{name: "exactly the cap boundary", timeout: autoBackgroundMargin + autoBackgroundCap, want: autoBackgroundCap},
		{name: "five minute timeout", timeout: 5 * time.Minute, want: autoBackgroundCap},
		{name: "ten minute timeout", timeout: 10 * time.Minute, want: autoBackgroundCap},
		{name: "exactly the margin", timeout: autoBackgroundMargin, want: 0},
		{name: "below the margin", timeout: 2 * time.Second, want: 0},
		{name: "no deadline", timeout: 0, want: autoBackgroundCap},
		{name: "negative deadline", timeout: -time.Second, want: autoBackgroundCap},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoBackgroundDelay(tc.timeout); got != tc.want {
				t.Fatalf("autoBackgroundDelay(%v) = %v, want %v", tc.timeout, got, tc.want)
			}
		})
	}
}

func TestAutoBackgroundDelayFor_DeadlineDerived(t *testing.T) {
	t.Run("no deadline keeps the cap", func(t *testing.T) {
		if got := autoBackgroundDelayFor(context.Background()); got != autoBackgroundCap {
			t.Fatalf("autoBackgroundDelayFor(no deadline) = %v, want %v", got, autoBackgroundCap)
		}
	})

	t.Run("deadline drives the delay", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()

		got := autoBackgroundDelayFor(ctx)
		if got > time.Second || got <= 900*time.Millisecond {
			t.Fatalf("autoBackgroundDelayFor(6s deadline) = %v, want ~1s", got)
		}
	})

	t.Run("long deadline is capped at 60s", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		if got := autoBackgroundDelayFor(ctx); got != autoBackgroundCap {
			t.Fatalf("autoBackgroundDelayFor(10m deadline) = %v, want %v", got, autoBackgroundCap)
		}
	})

	t.Run("expired deadline disables auto-background", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()

		if got := autoBackgroundDelayFor(ctx); got != 0 {
			t.Fatalf("autoBackgroundDelayFor(expired deadline) = %v, want 0", got)
		}
	})

	t.Run("deadline inside the margin disables auto-background", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if got := autoBackgroundDelayFor(ctx); got != 0 {
			t.Fatalf("autoBackgroundDelayFor(3s deadline) = %v, want 0", got)
		}
	})
}

func TestExec_BackgroundWinsBeforeTimeout(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	t.Cleanup(func() { mgr.StopAll() })

	tool := NewExecTool("", false)
	tool.SetBackgroundManager(mgr)
	tool.SetTimeout(6 * time.Second)

	result := tool.Execute(context.Background(), map[string]interface{}{"command": "sleep 3"})

	if result.IsError {
		t.Fatalf("expected auto-background to win over the 6s timeout, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Moved to background") {
		t.Fatalf("expected 'Moved to background' before the timeout, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "after 1s") {
		t.Errorf("expected the reported delay to be timeout-margin (1s), got: %s", result.ForLLM)
	}

	procs := mgr.List()
	if len(procs) != 1 {
		t.Fatalf("expected exactly 1 registered background process, got %d", len(procs))
	}
	if procs[0].Command != "sleep 3" {
		t.Errorf("expected the background record to keep the original command, got %q", procs[0].Command)
	}
}

func TestExec_AutoBackgroundFollowsTheDeadlineNotTheNominalTimeout(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	t.Cleanup(func() { mgr.StopAll() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tool := NewExecTool("", false)
	tool.SetBackgroundManager(mgr)
	tool.SetTimeout(10 * time.Minute)

	result := tool.Execute(ctx, map[string]interface{}{"command": "sleep 20"})

	if result.IsError {
		t.Fatalf("expected the deadline-derived auto-background to win, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Moved to background") {
		t.Fatalf("expected 'Moved to background' 5s before the inherited deadline, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "after 5s") {
		t.Errorf("expected the reported delay to be deadline-margin (5s), got: %s", result.ForLLM)
	}
	if procs := mgr.List(); len(procs) != 1 {
		t.Fatalf("expected exactly 1 registered background process, got %d", len(procs))
	}
}

func TestExec_ShortTimeoutTimesOutNoBackground(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	t.Cleanup(func() { mgr.StopAll() })

	tool := NewExecTool("", false)
	tool.SetBackgroundManager(mgr)
	tool.SetTimeout(2 * time.Second)

	result := tool.Execute(context.Background(), map[string]interface{}{"command": "sleep 30"})

	if !result.IsError {
		t.Fatalf("expected a timeout at or below the auto-background margin, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "timed out") {
		t.Fatalf("expected the timeout outcome, got: %s", result.ForLLM)
	}
	if procs := mgr.List(); len(procs) != 0 {
		t.Fatalf("expected no background process for a 2s timeout, got %d", len(procs))
	}
}

func TestExec_NoDeadlineKeepsThe60sCap(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	t.Cleanup(func() { mgr.StopAll() })

	if got := autoBackgroundDelayFor(context.Background()); got != autoBackgroundCap {
		t.Fatalf("a command without a deadline resolves an auto-background delay of %v, want %v", got, autoBackgroundCap)
	}

	tool := NewExecTool("", false)
	tool.SetBackgroundManager(mgr)
	tool.SetTimeout(0)

	result := tool.Execute(context.Background(), map[string]interface{}{"command": "sleep 1"})

	if result.IsError {
		t.Fatalf("a command without a deadline must not time out: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "Moved to background") {
		t.Fatalf("a command without a deadline must not background after 1s, got: %s", result.ForLLM)
	}
	if procs := mgr.List(); len(procs) != 0 {
		t.Fatalf("expected no background process for a command without a deadline, got %d", len(procs))
	}
}

func TestExec_ExplicitBackgroundUnaffected(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	t.Cleanup(func() { mgr.StopAll() })

	tool := NewExecTool("", false)
	tool.SetBackgroundManager(mgr)
	tool.SetTimeout(6 * time.Second)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"command":    "sleep 2",
		"background": true,
	})

	if result.IsError {
		t.Fatalf("explicit background exec failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Command started in background") {
		t.Fatalf("expected the immediate explicit background path, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "Moved to background") {
		t.Fatalf("explicit background must not take the auto-background path, got: %s", result.ForLLM)
	}
	if procs := mgr.List(); len(procs) != 1 {
		t.Fatalf("expected exactly 1 registered background process, got %d", len(procs))
	}
}
