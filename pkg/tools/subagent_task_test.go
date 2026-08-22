package tools

import (
	"sync"
	"testing"
	"time"
)

// TestSubagentTask_Delivered verifies the Delivered() flag behavior.
func TestSubagentTask_Delivered(t *testing.T) {
	task := &SubagentTask{mu: &sync.Mutex{}}
	if task.Delivered() {
		t.Fatal("expected not delivered initially")
	}

	task.mu.Lock()
	task.delivered = true
	task.mu.Unlock()
	if !task.Delivered() {
		t.Fatal("expected delivered=true")
	}
}

// TestSubagentTask_Delivered_NilMutex verifies Delivered works without a mutex.
func TestSubagentTask_Delivered_NilMutex(t *testing.T) {
	task := &SubagentTask{}
	if task.Delivered() {
		t.Fatal("expected not delivered")
	}
	task.delivered = true
	if !task.Delivered() {
		t.Fatal("expected delivered true without mutex")
	}
}

// TestSubagentTask_IsPending verifies pending-status detection.
func TestSubagentTask_IsPending(t *testing.T) {
	pending := &SubagentTask{Status: SubagentStatusPending}
	if !pending.IsPending() {
		t.Fatal("pending task should be pending")
	}
	running := &SubagentTask{Status: SubagentStatusRunning}
	if running.IsPending() {
		t.Fatal("running task should not be pending")
	}
	done := &SubagentTask{Status: SubagentStatusCompleted}
	if done.IsPending() {
		t.Fatal("completed task should not be pending")
	}
}

// TestSubagentTask_IsTerminal verifies terminal-status detection.
func TestSubagentTask_IsTerminal(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{SubagentStatusCompleted, true},
		{SubagentStatusFailed, true},
		{SubagentStatusNotDone, true},
		{SubagentStatusCancelled, true},
		{SubagentStatusRunning, false},
		{SubagentStatusPending, false},
		{SubagentStatusNeedsContext, false},
	}
	for _, tc := range tests {
		if got := (&SubagentTask{Status: tc.status}).IsTerminal(); got != tc.want {
			t.Errorf("IsTerminal(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

// TestSubagentTask_Snapshot verifies the deep-copy snapshot.
func TestSubagentTask_Snapshot(t *testing.T) {
	task := &SubagentTask{
		ID:           "subagent-1",
		Guidance:     []string{"a", "b"},
		Dependencies: []string{"dep-1"},
		mu:           &sync.Mutex{},
	}
	snap := task.Snapshot()
	if snap.ID != "subagent-1" || len(snap.Guidance) != 2 || len(snap.Dependencies) != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	// Mutating the original slices must not affect the snapshot.
	task.Guidance[0] = "changed"
	if snap.Guidance[0] != "a" {
		t.Fatal("snapshot guidance aliased original")
	}
}

// TestSubagentTask_DoneChannel manages channel init/signal semantics.
func TestSubagentTask_DoneChannel(t *testing.T) {
	task := &SubagentTask{mu: &sync.Mutex{}}
	task.InitDoneChannel()
	ch := task.DoneChannel()
	if ch == nil {
		t.Fatal("expected non-nil done channel after InitDoneChannel")
	}

	task.SignalDone()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected done channel to close after SignalDone")
	}

	// SignalDone is safe to call again (channel already closed).
	task.SignalDone()
}

// TestSubagentTask_DoneChannel_NilMutexInit verifies init with nil mutex.
func TestSubagentTask_DoneChannel_NilMutexInit(t *testing.T) {
	task := &SubagentTask{}
	task.InitDoneChannel()
	if task.DoneChannel() == nil {
		t.Fatal("expected done channel")
	}
}

// TestSubagentTask_StatusMessage covers the statusMessage helper branches.
func TestSubagentTask_StatusMessage(t *testing.T) {
	base := &SubagentTask{mu: &sync.Mutex{}}
	msg := base.statusMessage()
	if msg == "" {
		t.Fatal("expected non-empty status message")
	}

	typed := &SubagentTask{
		ID:             "subagent-1",
		Label:          "  ",
		AgentID:        "coder",
		Status:         SubagentStatusNeedsContext,
		Summary:        "progressing",
		ContextRequest: "need info",
		Result:         "details",
		mu:             &sync.Mutex{},
	}
	m := typed.statusMessage()
	if m == "" {
		t.Fatal("expected status message")
	}
	// Named label branch exercised via unlabeled (empty) label.
	named := &SubagentTask{ID: "x", Label: "my-label", Status: SubagentStatusCompleted, mu: &sync.Mutex{}}
	if !containsStr(named.statusMessage(), "my-label") {
		t.Fatal("expected named label in message")
	}
}

// TestSubagentTask_DisplayLabel verifies displayLabel behavior.
func TestSubagentTask_DisplayLabel(t *testing.T) {
	task := &SubagentTask{Label: "hello"}
	if task.displayLabel() != "hello" {
		t.Fatalf("displayLabel = %q", task.displayLabel())
	}
	task2 := &SubagentTask{Label: "  "}
	if task2.displayLabel() != "(unnamed)" {
		t.Fatalf("displayLabel empty = %q", task2.displayLabel())
	}
}

// TestSubagentTask_BuildMessages covers message assembly with and without result/guidance.
func TestSubagentTask_BuildMessages(t *testing.T) {
	basic := &SubagentTask{Task: "hello task", mu: &sync.Mutex{}}
	msgs := basic.buildMessages("SYSTEM")
	if len(msgs) != 2 {
		t.Fatalf("basic messages = %d, want 2", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[1].Role != "user" {
		t.Fatalf("roles = %s/%s", msgs[0].Role, msgs[1].Role)
	}

	full := &SubagentTask{
		Task:           "do it",
		Status:         SubagentStatusRunning,
		Summary:        "summary",
		ContextRequest: "need ctx",
		Result:         "some result",
		Guidance:       []string{"g1", "g2"},
		mu:             &sync.Mutex{},
	}
	fullMsgs := full.buildMessages("SYS")
	if len(fullMsgs) < 4 {
		t.Fatalf("full messages = %d, want >= 4", len(fullMsgs))
	}
	// Find the guidance message.
	found := false
	for _, m := range fullMsgs {
		if m.Role == "user" && len(m.Content) > 0 && m.Content[0] == 'A' {
			found = true
		}
	}
	if !found {
		t.Fatal("expected guidance message")
	}
}
