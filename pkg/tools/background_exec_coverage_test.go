package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// BackgroundProcess.OutputTail
// ---------------------------------------------------------------------------

func TestBackgroundProcess_OutputTail(t *testing.T) {
	var stdout, stderr threadSafeBuffer
	stdout.Write([]byte("hello world this is stdout"))
	stderr.Write([]byte("err-line"))

	p := &BackgroundProcess{stdout: &stdout, stderr: &stderr}
	full := p.Output()

	t.Run("tail smaller than output", func(t *testing.T) {
		tail := p.OutputTail(10)
		want := full[len(full)-10:]
		if tail != want {
			t.Errorf("OutputTail(10) = %q, want %q", tail, want)
		}
	})

	t.Run("tail negative returns full", func(t *testing.T) {
		if got := p.OutputTail(-1); got != full {
			t.Errorf("OutputTail(-1) = %q, want full %q", got, full)
		}
	})

	t.Run("tail zero returns full", func(t *testing.T) {
		if got := p.OutputTail(0); got != full {
			t.Errorf("OutputTail(0) = %q, want full", got)
		}
	})

	t.Run("tail larger than output returns full", func(t *testing.T) {
		if got := p.OutputTail(100000); got != full {
			t.Errorf("OutputTail(100000) = %q, want full", got)
		}
	})

	t.Run("empty output", func(t *testing.T) {
		var eo, es threadSafeBuffer
		ep := &BackgroundProcess{stdout: &eo, stderr: &es}
		if got := ep.OutputTail(100); got != "(no output)" {
			t.Errorf("OutputTail(100) on empty = %q, want '(no output)'", got)
		}
	})
}

// ---------------------------------------------------------------------------
// BackgroundProcess.ExitInfo
// ---------------------------------------------------------------------------

func TestBackgroundProcess_ExitInfo(t *testing.T) {
	t.Run("zero exit code returns empty", func(t *testing.T) {
		p := &BackgroundProcess{ExitCode: 0}
		if got := p.ExitInfo(); got != "" {
			t.Errorf("ExitInfo() = %q, want empty", got)
		}
	})

	t.Run("nonzero exit code", func(t *testing.T) {
		p := &BackgroundProcess{ExitCode: 42}
		if got := p.ExitInfo(); got != "\nExit code: 42" {
			t.Errorf("ExitInfo() = %q, want '\\nExit code: 42'", got)
		}
	})
}

// ---------------------------------------------------------------------------
// BackgroundProcessManager.SetMaxProcesses & max-process enforcement
// ---------------------------------------------------------------------------

func TestBackgroundProcessManager_SetMaxProcessesAndEvict(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	mgr.SetMaxProcesses(2)
	// Ensure terminal processes are immediately eligible for eviction.
	mgr.SetRetentionPeriod(1 * time.Nanosecond)

	registerTestProcess(t, mgr, "a")
	registerTestProcess(t, mgr, "b")
	if mgr.Count() != 2 {
		t.Fatalf("expected 2 processes, got %d", mgr.Count())
	}

	// Mark b terminal so the next Register can evict it.
	mgr.MarkCompleted("bg-2", 0)
	time.Sleep(2 * time.Nanosecond)

	// Register a third; max reached, terminal processes cleaned up first.
	registerTestProcess(t, mgr, "c")
	if _, ok := mgr.Get("bg-2"); ok {
		t.Error("expected bg-2 to be evicted when max processes reached")
	}
	if mgr.Count() > 2 {
		t.Errorf("expected count capped at 2, got %d", mgr.Count())
	}
}

// ---------------------------------------------------------------------------
// StopAll
// ---------------------------------------------------------------------------

func TestBackgroundProcessManager_StopAll(t *testing.T) {
	t.Run("stops all running processes", func(t *testing.T) {
		mgr := NewBackgroundProcessManager()
		registerTestProcess(t, mgr, "a")
		registerTestProcess(t, mgr, "b")
		mgr.MarkCompleted("bg-2", 0) // one already terminal

		if n := mgr.StopAll(); n != 1 {
			t.Errorf("StopAll() = %d, want 1", n)
		}

		p, _ := mgr.Get("bg-1")
		if p.Status != BgExecStatusStopped {
			t.Errorf("bg-1 status = %q, want stopped", p.Status)
		}
		p2, _ := mgr.Get("bg-2")
		if p2.Status != BgExecStatusCompleted {
			t.Errorf("bg-2 status should remain completed, got %q", p2.Status)
		}
	})

	t.Run("no running processes", func(t *testing.T) {
		mgr := NewBackgroundProcessManager()
		registerTestProcess(t, mgr, "a")
		mgr.MarkCompleted("bg-1", 0)
		if n := mgr.StopAll(); n != 0 {
			t.Errorf("StopAll() = %d, want 0", n)
		}
	})
}

// ---------------------------------------------------------------------------
// Tool metadata: Name / Description / Parameters for the three bg tools.
// ---------------------------------------------------------------------------

func TestListBackgroundExecsTool_Metadata(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	tool := NewListBackgroundExecsTool(mgr)

	if tool.Name() != "list_background_execs" {
		t.Errorf("Name() = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	params := tool.Parameters()
	if _, ok := params["properties"]; !ok {
		t.Errorf("Parameters() missing properties: %+v", params)
	}
}

func TestGetBackgroundExecOutputTool_Metadata(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	tool := NewGetBackgroundExecOutputTool(mgr)

	if tool.Name() != "get_background_exec_output" {
		t.Errorf("Name() = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	params := tool.Parameters()
	props := params["properties"].(map[string]interface{})
	if _, ok := props["id"]; !ok {
		t.Error("Parameters() missing 'id'")
	}
	if _, ok := props["tail"]; !ok {
		t.Error("Parameters() missing 'tail'")
	}
}

func TestStopBackgroundExecTool_Metadata(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	tool := NewStopBackgroundExecTool(mgr)

	if tool.Name() != "stop_background_exec" {
		t.Errorf("Name() = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	params := tool.Parameters()
	props := params["properties"].(map[string]interface{})
	if _, ok := props["id"]; !ok {
		t.Error("Parameters() missing 'id'")
	}
}

// ---------------------------------------------------------------------------
// GetBackgroundExecOutputTool.Execute – tail path (truncation + tail)
// ---------------------------------------------------------------------------

func TestGetBackgroundExecOutputTool_Tail(t *testing.T) {
	mgr := NewBackgroundProcessManager()

	cmd := exec.Command("echo", "test")
	var stdout, stderr threadSafeBuffer
	stdout.Write([]byte(strings.Repeat("ab", 5000))) // 10000 chars
	mgr.Register(cmd, "echo test", "/tmp", &stdout, &stderr, func() {}, "")

	tool := NewGetBackgroundExecOutputTool(mgr)

	// tail as int
	res := tool.Execute(context.Background(), map[string]interface{}{"id": "bg-1", "tail": 100})
	if res.IsError {
		t.Fatalf("tail int error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "ababab") {
		t.Errorf("tail int result missing output: %s", res.ForLLM)
	}

	// tail as float64 (JSON default)
	res = tool.Execute(context.Background(), map[string]interface{}{"id": "bg-1", "tail": float64(50)})
	if res.IsError {
		t.Fatalf("tail float error: %s", res.ForLLM)
	}
	// 10000 chars, truncated to 10000 cap, then header + output
	if !strings.Contains(res.ForLLM, "ab") {
		t.Errorf("expected tail output, got: %s", res.ForLLM)
	}

	// Tail as int64 (toInt fallback branch).
	res = tool.Execute(context.Background(), map[string]interface{}{"id": "bg-1", "tail": int64(10)})
	if res.IsError {
		t.Fatalf("tail int64 error: %s", res.ForLLM)
	}
}

// toInt direct unit coverage.
func TestToInt(t *testing.T) {
	if v, ok := toInt(float64(7)); !ok || v != 7 {
		t.Errorf("toInt(float64(7)) = %d,%v", v, ok)
	}
	if v, ok := toInt(7); !ok || v != 7 {
		t.Errorf("toInt(7) = %d,%v", v, ok)
	}
	if v, ok := toInt(int64(7000000000)); !ok || v != 7000000000 {
		t.Errorf("toInt(int64) = %d,%v", v, ok)
	}
	if _, ok := toInt("nope"); ok {
		t.Error("toInt(string) should be false")
	}
	if _, ok := toInt(nil); ok {
		t.Error("toInt(nil) should be false")
	}
}

// ---------------------------------------------------------------------------
// GetBackgroundExecOutputTool.Execute – maxLen truncation branch (>10000 chars)
// ---------------------------------------------------------------------------

func TestGetBackgroundExecOutputTool_MaxLenTruncation(t *testing.T) {
	mgr := NewBackgroundProcessManager()

	cmd := exec.Command("echo", "test")
	var stdout, stderr threadSafeBuffer
	stdout.Write([]byte(strings.Repeat("x", 15000)))
	mgr.Register(cmd, "echo test", "/tmp", &stdout, &stderr, func() {}, "")

	tool := NewGetBackgroundExecOutputTool(mgr)
	res := tool.Execute(context.Background(), map[string]interface{}{"id": "bg-1"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "truncated") {
		t.Errorf("expected truncation notice, got len=%d: %s", len(res.ForLLM), res.ForLLM)
	}
}

// ---------------------------------------------------------------------------
// StopBackgroundExecTool.Execute – elapsed formatting path for a finished proc
// ---------------------------------------------------------------------------

func TestStopBackgroundExecTool_ElapsedReportsDuration(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	cmd := exec.Command("sleep", "0")
	var stdout, stderr threadSafeBuffer
	start := time.Now().Add(-30 * time.Second)
	proc := mgr.Register(cmd, "sleep 0", "/tmp", &stdout, &stderr, func() {}, "")
	proc.StartTime = start

	tool := NewStopBackgroundExecTool(mgr)
	res := tool.Execute(context.Background(), map[string]interface{}{"id": proc.ID})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "30s") {
		t.Errorf("expected ~30s elapsed in result, got: %s", res.ForLLM)
	}
}// ---------------------------------------------------------------------------
// releaseBuffers truncation
// ---------------------------------------------------------------------------

func TestBackgroundProcess_ReleaseBuffers_TruncatesLargeOutput(t *testing.T) {
	var stdout, stderr threadSafeBuffer
	stdout.Write([]byte(strings.Repeat("a", 100000)))
	stderr.Write([]byte(strings.Repeat("b", 100000)))

	p := &BackgroundProcess{stdout: &stdout, stderr: &stderr}
	p.mu.Lock()
	p.releaseBuffers()
	p.mu.Unlock()

	if p.stdout.Len() > 64*1024 {
		t.Errorf("stdout not truncated, len=%d", p.stdout.Len())
	}
	if p.stderr.Len() > 64*1024 {
		t.Errorf("stderr not truncated, len=%d", p.stderr.Len())
	}
	// Content should be the tail.
	if !strings.HasSuffix(p.stdout.String(), strings.Repeat("a", 64*1024)) {
		t.Error("stdout should keep the tail of the output")
	}
}

func TestBackgroundProcess_ReleaseBuffers_SmallOutput(t *testing.T) {
	var stdout, stderr threadSafeBuffer
	stdout.Write([]byte("small"))
	stderr.Write([]byte("tiny"))

	p := &BackgroundProcess{stdout: &stdout, stderr: &stderr}
	p.mu.Lock()
	p.releaseBuffers()
	p.mu.Unlock()

	if p.stdout.String() != "small" {
		t.Errorf("stdout = %q, want 'small'", p.stdout.String())
	}
	if p.stderr.String() != "tiny" {
		t.Errorf("stderr = %q, want 'tiny'", p.stderr.String())
	}
}