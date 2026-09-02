package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TestThreadSafeBuffer
// ---------------------------------------------------------------------------

func TestThreadSafeBuffer(t *testing.T) {
	t.Run("basic write and read", func(t *testing.T) {
		var buf threadSafeBuffer
		n, err := buf.Write([]byte("hello world"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 11 {
			t.Fatalf("expected 11 bytes written, got %d", n)
		}
		if buf.String() != "hello world" {
			t.Fatalf("expected 'hello world', got %q", buf.String())
		}
		if buf.Len() != 11 {
			t.Fatalf("expected Len() == 11, got %d", buf.Len())
		}
	})

	t.Run("empty buffer", func(t *testing.T) {
		var buf threadSafeBuffer
		if buf.String() != "" {
			t.Fatalf("expected empty string, got %q", buf.String())
		}
		if buf.Len() != 0 {
			t.Fatalf("expected Len() == 0, got %d", buf.Len())
		}
	})

	t.Run("concurrent writes", func(t *testing.T) {
		var buf threadSafeBuffer
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				data := strings.Repeat("x", 100)
				buf.Write([]byte(data))
			}()
		}
		wg.Wait()
		if buf.Len() != 1000 {
			t.Fatalf("expected Len() == 1000, got %d", buf.Len())
		}
	})
}

// helper to create a registered process quickly
func registerTestProcess(t *testing.T, mgr *BackgroundProcessManager, command string) *BackgroundProcess {
	t.Helper()
	cmd := exec.Command("echo", "test")
	var stdout, stderr threadSafeBuffer
	return mgr.Register(cmd, command, "/tmp", &stdout, &stderr, func() {}, "")
}

// ---------------------------------------------------------------------------
// TestBackgroundProcessManager_RegisterAndGet
// ---------------------------------------------------------------------------

func TestBackgroundProcessManager_RegisterAndGet(t *testing.T) {
	mgr := NewBackgroundProcessManager()

	cmd := exec.Command("echo", "hello")
	var stdout, stderr threadSafeBuffer

	proc := mgr.Register(cmd, "echo hello", "/tmp", &stdout, &stderr, func() {}, "")

	if proc.ID != "bg-1" {
		t.Fatalf("expected ID 'bg-1', got %q", proc.ID)
	}
	if proc.Status != BgExecStatusRunning {
		t.Fatalf("expected status 'running', got %q", proc.Status)
	}
	if proc.Command != "echo hello" {
		t.Fatalf("expected command 'echo hello', got %q", proc.Command)
	}

	// Register a second process
	proc2 := registerTestProcess(t, mgr, "echo world")
	if proc2.ID != "bg-2" {
		t.Fatalf("expected ID 'bg-2', got %q", proc2.ID)
	}

	// Get the first process
	got, ok := mgr.Get("bg-1")
	if !ok {
		t.Fatal("expected Get('bg-1') to return true")
	}
	if got.ID != "bg-1" || got.Command != "echo hello" {
		t.Fatalf("unexpected process: ID=%q Command=%q", got.ID, got.Command)
	}

	// Get nonexistent
	_, ok = mgr.Get("bg-999")
	if ok {
		t.Fatal("expected Get('bg-999') to return false")
	}
}

// ---------------------------------------------------------------------------
// TestBackgroundProcessManager_List
// ---------------------------------------------------------------------------

func TestBackgroundProcessManager_List(t *testing.T) {
	mgr := NewBackgroundProcessManager()

	registerTestProcess(t, mgr, "cmd-1")
	registerTestProcess(t, mgr, "cmd-2")
	registerTestProcess(t, mgr, "cmd-3")

	list := mgr.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 processes, got %d", len(list))
	}
	// Verify sorted by ID
	expectedIDs := []string{"bg-1", "bg-2", "bg-3"}
	for i, p := range list {
		if p.ID != expectedIDs[i] {
			t.Fatalf("list[%d].ID = %q, want %q", i, p.ID, expectedIDs[i])
		}
	}

	// All running
	running := mgr.ListRunning()
	if len(running) != 3 {
		t.Fatalf("expected 3 running, got %d", len(running))
	}

	// Mark one completed
	mgr.MarkCompleted("bg-2", 0)
	running = mgr.ListRunning()
	if len(running) != 2 {
		t.Fatalf("expected 2 running after completing bg-2, got %d", len(running))
	}
}

// ---------------------------------------------------------------------------
// TestBackgroundProcessManager_Stop
// ---------------------------------------------------------------------------

func TestBackgroundProcessManager_Stop(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	registerTestProcess(t, mgr, "long-running")

	// Stop returns true for running process
	if !mgr.Stop("bg-1") {
		t.Fatal("expected Stop('bg-1') to return true")
	}

	// Verify status
	p, _ := mgr.Get("bg-1")
	if p.Status != BgExecStatusStopped {
		t.Fatalf("expected status 'stopped', got %q", p.Status)
	}
	if p.EndTime == nil {
		t.Fatal("expected EndTime to be set after stop")
	}

	// Stop again returns false (already stopped)
	if mgr.Stop("bg-1") {
		t.Fatal("expected Stop('bg-1') to return false when already stopped")
	}

	// Stop nonexistent
	if mgr.Stop("nonexistent") {
		t.Fatal("expected Stop('nonexistent') to return false")
	}
}

// ---------------------------------------------------------------------------
// TestBackgroundProcessManager_MarkCompleted
// ---------------------------------------------------------------------------

func TestBackgroundProcessManager_MarkCompleted(t *testing.T) {
	mgr := NewBackgroundProcessManager()

	registerTestProcess(t, mgr, "success-cmd")
	mgr.MarkCompleted("bg-1", 0)
	p, _ := mgr.Get("bg-1")
	if p.Status != BgExecStatusCompleted {
		t.Fatalf("expected status 'completed', got %q", p.Status)
	}
	if p.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", p.ExitCode)
	}

	registerTestProcess(t, mgr, "fail-cmd")
	mgr.MarkCompleted("bg-2", 1)
	p2, _ := mgr.Get("bg-2")
	if p2.Status != BgExecStatusFailed {
		t.Fatalf("expected status 'failed', got %q", p2.Status)
	}
	if p2.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", p2.ExitCode)
	}

	// MarkCompleted on nonexistent ID should not panic
	mgr.MarkCompleted("nonexistent", 0)
}

// ---------------------------------------------------------------------------
// TestBackgroundProcessManager_CleanupTerminal
// ---------------------------------------------------------------------------

func TestBackgroundProcessManager_CleanupTerminal(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	mgr.SetRetentionPeriod(1 * time.Nanosecond)

	// Register and mark completed — should be immediately eligible for cleanup
	registerTestProcess(t, mgr, "done-cmd")
	mgr.MarkCompleted("bg-1", 0)
	time.Sleep(2 * time.Nanosecond) // ensure retention period passes

	mgr.CleanupTerminal()
	if _, ok := mgr.Get("bg-1"); ok {
		t.Fatal("expected bg-1 to be cleaned up")
	}

	// Register another but keep it running — should NOT be cleaned up
	registerTestProcess(t, mgr, "still-running")
	mgr.CleanupTerminal()
	if _, ok := mgr.Get("bg-2"); !ok {
		t.Fatal("expected bg-2 to still exist (running)")
	}
}

// ---------------------------------------------------------------------------
// TestBackgroundProcess_Output
// ---------------------------------------------------------------------------

func TestBackgroundProcess_Output(t *testing.T) {
	t.Run("combined stdout and stderr", func(t *testing.T) {
		var stdout, stderr threadSafeBuffer
		stdout.Write([]byte("hello stdout"))
		stderr.Write([]byte("hello stderr"))

		p := &BackgroundProcess{stdout: &stdout, stderr: &stderr}
		out := p.Output()

		expected := "hello stdout\nSTDERR:\nhello stderr"
		if out != expected {
			t.Fatalf("Output() = %q, want %q", out, expected)
		}
	})

	t.Run("empty output", func(t *testing.T) {
		var stdout, stderr threadSafeBuffer
		p := &BackgroundProcess{stdout: &stdout, stderr: &stderr}
		if p.Output() != "(no output)" {
			t.Fatalf("Output() = %q, want '(no output)'", p.Output())
		}
	})

	t.Run("stdout only", func(t *testing.T) {
		var stdout, stderr threadSafeBuffer
		stdout.Write([]byte("only stdout"))
		p := &BackgroundProcess{stdout: &stdout, stderr: &stderr}
		if p.Output() != "only stdout" {
			t.Fatalf("Output() = %q, want 'only stdout'", p.Output())
		}
	})
}

// ---------------------------------------------------------------------------
// TestBackgroundProcess_Elapsed
// ---------------------------------------------------------------------------

func TestBackgroundProcess_Elapsed(t *testing.T) {
	t.Run("running process", func(t *testing.T) {
		p := &BackgroundProcess{StartTime: time.Now()}
		elapsed := p.Elapsed()
		if elapsed < 0 {
			t.Fatalf("expected positive duration, got %v", elapsed)
		}
	})

	t.Run("finished process", func(t *testing.T) {
		start := time.Now()
		end := start.Add(5 * time.Second)
		p := &BackgroundProcess{
			StartTime: start,
			EndTime:   &end,
		}
		elapsed := p.Elapsed()
		diff := elapsed - 5*time.Second
		if diff < -time.Millisecond || diff > time.Millisecond {
			t.Fatalf("expected ~5s, got %v", elapsed)
		}
	})
}

// ---------------------------------------------------------------------------
// TestListBackgroundExecsTool
// ---------------------------------------------------------------------------

func TestListBackgroundExecsTool(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	registerTestProcess(t, mgr, "cmd-a")
	registerTestProcess(t, mgr, "cmd-b")

	tool := NewListBackgroundExecsTool(mgr)

	// List running (both are running)
	result := tool.Execute(context.Background(), map[string]interface{}{})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "bg-1") || !strings.Contains(result.ForLLM, "bg-2") {
		t.Fatalf("expected both bg-1 and bg-2 in output, got: %s", result.ForLLM)
	}

	// Mark one completed, list running only
	mgr.MarkCompleted("bg-1", 0)
	result = tool.Execute(context.Background(), map[string]interface{}{"include_completed": false})
	if strings.Contains(result.ForLLM, "bg-1") {
		t.Fatal("bg-1 should not appear in running-only list")
	}
	if !strings.Contains(result.ForLLM, "bg-2") {
		t.Fatal("bg-2 should appear in running-only list")
	}

	// List all (include completed)
	result = tool.Execute(context.Background(), map[string]interface{}{"include_completed": true})
	if !strings.Contains(result.ForLLM, "bg-1") || !strings.Contains(result.ForLLM, "bg-2") {
		t.Fatalf("expected both bg-1 and bg-2 in include_completed list, got: %s", result.ForLLM)
	}
}

// ---------------------------------------------------------------------------
// TestGetBackgroundExecOutputTool
// ---------------------------------------------------------------------------

func TestGetBackgroundExecOutputTool(t *testing.T) {
	mgr := NewBackgroundProcessManager()

	cmd := exec.Command("echo", "test")
	var stdout, stderr threadSafeBuffer
	stdout.Write([]byte("test output"))
	mgr.Register(cmd, "echo test", "/tmp", &stdout, &stderr, func() {}, "")

	tool := NewGetBackgroundExecOutputTool(mgr)

	// Get existing process output
	result := tool.Execute(context.Background(), map[string]interface{}{"id": "bg-1"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "test output") {
		t.Fatalf("expected 'test output' in result, got: %s", result.ForLLM)
	}

	// Nonexistent process
	result = tool.Execute(context.Background(), map[string]interface{}{"id": "nonexistent"})
	if !result.IsError {
		t.Fatal("expected IsError for nonexistent process")
	}

	// Missing id
	result = tool.Execute(context.Background(), map[string]interface{}{})
	if !result.IsError {
		t.Fatal("expected IsError for missing id")
	}
}

// ---------------------------------------------------------------------------
// TestStopBackgroundExecTool
// ---------------------------------------------------------------------------

func TestStopBackgroundExecTool(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	registerTestProcess(t, mgr, "long-cmd")

	tool := NewStopBackgroundExecTool(mgr)

	// Stop the process
	result := tool.Execute(context.Background(), map[string]interface{}{"id": "bg-1"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	// Verify it's stopped
	p, _ := mgr.Get("bg-1")
	if p.Status != BgExecStatusStopped {
		t.Fatalf("expected status 'stopped', got %q", p.Status)
	}

	// Stop again → error (already stopped)
	result = tool.Execute(context.Background(), map[string]interface{}{"id": "bg-1"})
	if !result.IsError {
		t.Fatal("expected IsError when stopping already-stopped process")
	}

	// Stop nonexistent → error
	result = tool.Execute(context.Background(), map[string]interface{}{"id": "nonexistent"})
	if !result.IsError {
		t.Fatal("expected IsError for nonexistent process")
	}

	// Missing id → error
	result = tool.Execute(context.Background(), map[string]interface{}{})
	if !result.IsError {
		t.Fatal("expected IsError for missing id")
	}
}

// ---------------------------------------------------------------------------
// Bonus: verify the background process manager generates sequential IDs
// ---------------------------------------------------------------------------

func TestBackgroundProcessManager_SequentialIDs(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	for i := 0; i < 5; i++ {
		proc := registerTestProcess(t, mgr, fmt.Sprintf("cmd-%d", i))
		expected := fmt.Sprintf("bg-%d", i+1)
		if proc.ID != expected {
			t.Fatalf("iteration %d: expected ID %q, got %q", i, expected, proc.ID)
		}
	}
	if mgr.Count() != 5 {
		t.Fatalf("expected Count() == 5, got %d", mgr.Count())
	}
}

// ---------------------------------------------------------------------------
// TestThreadSafeBuffer_MaxSize
// ---------------------------------------------------------------------------

func TestThreadSafeBuffer_MaxSize(t *testing.T) {
	t.Run("ring buffer trims to maxSize", func(t *testing.T) {
		buf := newThreadSafeBuffer(100)

		// Write 200 bytes
		data := make([]byte, 200)
		for i := range data {
			data[i] = byte('A' + i%26)
		}
		n, err := buf.Write(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 200 {
			t.Fatalf("expected 200 bytes written, got %d", n)
		}

		// Should only keep the last 100 bytes
		if buf.Len() != 100 {
			t.Fatalf("expected Len() == 100, got %d", buf.Len())
		}

		// Verify the content is the LAST 100 bytes
		expected := string(data[100:])
		got := buf.String()
		if got != expected {
			t.Fatalf("expected last 100 bytes, got %q (len=%d)", got, len(got))
		}
	})

	t.Run("zero maxSize means unlimited", func(t *testing.T) {
		var buf threadSafeBuffer // maxSize=0 (zero value)
		data := strings.Repeat("x", 5000)
		buf.Write([]byte(data))
		if buf.Len() != 5000 {
			t.Fatalf("expected Len() == 5000 with unlimited, got %d", buf.Len())
		}
	})

	t.Run("exact maxSize does not trim", func(t *testing.T) {
		buf := newThreadSafeBuffer(50)
		data := make([]byte, 50)
		for i := range data {
			data[i] = byte(i)
		}
		buf.Write(data)
		if buf.Len() != 50 {
			t.Fatalf("expected Len() == 50, got %d", buf.Len())
		}
		if buf.String() != string(data) {
			t.Fatal("content should be unchanged when exactly at maxSize")
		}
	})

	t.Run("multiple small writes accumulate then trim", func(t *testing.T) {
		buf := newThreadSafeBuffer(10)
		for i := 0; i < 20; i++ {
			buf.Write([]byte{byte('0' + i%10)})
		}
		if buf.Len() != 10 {
			t.Fatalf("expected Len() == 10, got %d", buf.Len())
		}
		// Last 10 writes: bytes '0'+10%10='0', '0'+11%10='1', ... '0'+19%10='9'
		expected := "0123456789"
		if buf.String() != expected {
			t.Fatalf("expected %q, got %q", expected, buf.String())
		}
	})

	t.Run("constructor returns pointer", func(t *testing.T) {
		buf := newThreadSafeBuffer(256)
		if buf == nil {
			t.Fatal("newThreadSafeBuffer should not return nil")
		}
		if buf.maxSize != 256 {
			t.Fatalf("expected maxSize=256, got %d", buf.maxSize)
		}
	})
}

// ---------------------------------------------------------------------------
// StopForSession / session-family visibility (issue #230)
// ---------------------------------------------------------------------------

func registerOwnedProc(t *testing.T, mgr *BackgroundProcessManager, owner string) *BackgroundProcess {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	var stdout, stderr threadSafeBuffer
	p := mgr.Register(cmd, "sleep 30", "/tmp", &stdout, &stderr, func() {}, owner)
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return p
}

func TestBackgroundProcessManager_StopForSession(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	own := registerOwnedProc(t, mgr, "native:uuid-1")
	child := registerOwnedProc(t, mgr, "agent:main:native:uuid-1") // subagent-loop proc
	sibling := registerOwnedProc(t, mgr, "native:uuid-2")
	unowned := registerOwnedProc(t, mgr, "")

	// Bare runtime key stops the owner's family (qualified + child forms).
	if n := mgr.StopForSession("uuid-1"); n != 2 {
		t.Fatalf("StopForSession(bare uuid) = %d, want 2", n)
	}
	if own.Status != BgExecStatusStopped || child.Status != BgExecStatusStopped {
		t.Errorf("owned/child status = %q/%q, want stopped", own.Status, child.Status)
	}
	if sibling.Status != BgExecStatusRunning {
		t.Errorf("sibling status = %q, want running (untouched)", sibling.Status)
	}
	if unowned.Status != BgExecStatusRunning {
		t.Errorf("unowned status = %q, want running (empty key never matches)", unowned.Status)
	}

	// Empty sessionKey is a no-op (destructive guard).
	if n := mgr.StopForSession(""); n != 0 {
		t.Errorf("StopForSession(\"\") = %d, want 0", n)
	}
	if sibling.Status != BgExecStatusRunning {
		t.Errorf("sibling status after no-op = %q, want running", sibling.Status)
	}

	// Stopping an already-stopped family counts nothing new.
	if n := mgr.StopForSession("uuid-1"); n != 0 {
		t.Errorf("second StopForSession = %d, want 0", n)
	}
}

func TestBackgroundProcessVisibleTo_SessionFamily(t *testing.T) {
	tests := []struct {
		name    string
		owner   string
		caller  string
		wantVis bool
	}{
		{"exact", "telegram:123", "telegram:123", true},
		{"ancestor prefix", "telegram:123:subagent-1", "telegram:123", true},
		{"descendant sees parent-owned", "telegram:123", "telegram:123:subagent-1", true},
		{"qualified vs bare (issue #230)", "native:uuid", "uuid", true},
		{"agent runtime vs origin", "agent:main:telegram:123", "telegram:123", true},
		{"sibling isolated", "telegram:123:chat:1", "telegram:123:chat:2", false},
		{"cross-channel isolated", "native:uuid", "telegram:uuid", false},
		{"unowned visible", "", "telegram:123", true},
		{"operator sees all", "telegram:123", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &BackgroundProcess{ID: "bg-1", OwnerSessionKey: tc.owner}
			if got := p.VisibleTo(tc.caller); got != tc.wantVis {
				t.Errorf("VisibleTo(%q) for owner %q = %v, want %v", tc.caller, tc.owner, got, tc.wantVis)
			}
		})
	}
}
