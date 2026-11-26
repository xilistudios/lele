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
		{name: "exactly the margin", timeout: autoBackgroundMargin, want: 0},
		{name: "below the margin", timeout: 2 * time.Second, want: 0},
		{name: "no deadline", timeout: 0, want: 60 * time.Second},
		{name: "negative deadline", timeout: -time.Second, want: 60 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoBackgroundDelay(tc.timeout); got != tc.want {
				t.Fatalf("autoBackgroundDelay(%v) = %v, want %v", tc.timeout, got, tc.want)
			}
		})
	}
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

func TestExec_NoDeadlineBackgroundsAt60s(t *testing.T) {
	if got := autoBackgroundDelay(0); got != 60*time.Second {
		t.Fatalf("autoBackgroundDelay(0) = %v, want 60s: a command without a deadline keeps the historical 60s auto-background", got)
	}

	tool := NewExecTool("", false)
	tool.SetTimeout(0)
	if got := autoBackgroundDelay(tool.timeout); got != 60*time.Second {
		t.Fatalf("tool without a deadline resolves an auto-background delay of %v, want 60s", got)
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
