package tools

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// Tests for persisting terminal subagent statuses onto the subagent's
// session (SetSessionStatusCallback), so the WebUI sidebar shows the real
// outcome after eviction/restart instead of a hard-coded "completed".

// TestReportTerminalStatus_CallbackFired verifies the manager reports a
// task's terminal status to the session layer, keyed by the subagent's
// session key ({OriginSessionKey}:{ID}).
func TestReportTerminalStatus_CallbackFired(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)

	task := &SubagentTask{
		ID:               "subagent-4",
		Task:             "test task",
		Label:            "test",
		OriginChannel:    "cli",
		OriginChatID:     "direct",
		OriginSessionKey: "native:client-1",
		Status:           SubagentStatusFailed,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.mu.Unlock()

	type call struct {
		key    string
		status string
	}
	var mu sync.Mutex
	var calls []call
	sm.SetSessionStatusCallback(func(sessionKey, status string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call{sessionKey, status})
	})

	sm.reportTerminalStatus(task)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("expected 1 callback call, got %d", len(calls))
	}
	if calls[0].key != "native:client-1:subagent-4" {
		t.Errorf("session key = %q, want %q", calls[0].key, "native:client-1:subagent-4")
	}
	if calls[0].status != SubagentStatusFailed {
		t.Errorf("status = %q, want %q", calls[0].status, SubagentStatusFailed)
	}
}

// TestReportTerminalStatus_NoCallbackIsNoop ensures the manager works when no
// status callback is wired (e.g. standalone managers in tests): no panic, no
// state change.
func TestReportTerminalStatus_NoCallbackIsNoop(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	task := &SubagentTask{
		ID:               "subagent-1",
		OriginSessionKey: "native:c",
		Status:           SubagentStatusCancelled,
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	sm.reportTerminalStatus(task) // must not panic
}

// TestRunTask_PersistsTerminalStatus is the end-to-end manager-level check:
// a task that fails inside runTaskImpl (model override unresolvable) gets its
// terminal status reported through the callback. Mutation check: removing the
// reportTerminalStatus call from runTask makes this fail.
func TestRunTask_PersistsTerminalStatus(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	// Wire the model-override resolver to fail, forcing the early-fail branch
	// of runTaskImpl without any provider/network.
	sm.SetModelOverrideResolver(func(model string) (providers.LLMProvider, string, int, error) {
		return nil, "", 0, fmt.Errorf("no resolver for %q", model)
	})

	type call struct {
		key    string
		status string
	}
	var mu sync.Mutex
	var calls []call
	sm.SetSessionStatusCallback(func(sessionKey, status string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call{sessionKey, status})
	})

	task := &SubagentTask{
		ID:               "subagent-2",
		Task:             "test",
		Label:            "override-fail",
		AgentID:          "tester",
		ModelOverride:    "broken:model",
		OriginChannel:    "cli",
		OriginChatID:     "direct",
		OriginSessionKey: "native:client-2",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.mu.Unlock()

	sm.runTask(context.Background(), task, nil)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("runTask did not persist terminal status")
	}
	found := false
	for _, c := range calls {
		if c.key == "native:client-2:subagent-2" {
			found = true
			if c.status != SubagentStatusFailed {
				t.Errorf("status = %q, want %q", c.status, SubagentStatusFailed)
			}
		}
	}
	if !found {
		t.Fatalf("no call for native:client-2:subagent-2, got %+v", calls)
	}
}

// TestStopTask_PersistsCancelStatus verifies StopTask reports the cancelled
// status for tasks that may have no running goroutine (needs_context waiters).
func TestStopTask_PersistsCancelStatus(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)

	task := &SubagentTask{
		ID:               "subagent-9",
		Task:             "waiting",
		OriginChannel:    "cli",
		OriginChatID:     "direct",
		OriginSessionKey: "native:client-9",
		Status:           SubagentStatusNeedsContext,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.mu.Unlock()

	var mu sync.Mutex
	reported := ""
	sm.SetSessionStatusCallback(func(sessionKey, status string) {
		mu.Lock()
		defer mu.Unlock()
		reported = status
	})

	if !sm.StopTask("subagent-9") {
		t.Fatal("StopTask returned false for a stoppable task")
	}

	mu.Lock()
	defer mu.Unlock()
	if reported != SubagentStatusCancelled {
		t.Errorf("reported status = %q, want %q", reported, SubagentStatusCancelled)
	}
}
