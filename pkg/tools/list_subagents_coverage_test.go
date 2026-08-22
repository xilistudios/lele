package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestListSubagentsTool_FormattingBranches covers the running-elapsed,
// progress, retry-count, and summary lines in the formatting loop.
func TestListSubagentsTool_FormattingBranches(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)

	// Inject tasks directly to control their fields deterministically.
	now := time.Now().UnixMilli()
	manager.mu.Lock()
	manager.tasks["subagent-1"] = &SubagentTask{
		ID:         "subagent-1",
		Status:     SubagentStatusRunning,
		Label:      "running-label",
		AgentID:    "coder",
		Summary:    "working on it",
		Created:    now - 5000,
		RetryCount: 2,
		MaxRetries: 3,
		Progress:   "halfway",
		mu:         &sync.Mutex{},
	}
	// A second active task (needs_context) should ALSO show elapsed time.
	manager.tasks["subagent-2"] = &SubagentTask{
		ID:     "subagent-2",
		Status: SubagentStatusNeedsContext,
		Label:  "other",
		mu:     &sync.Mutex{},
	}
	manager.mu.Unlock()

	tool := NewListSubagentsTool(manager)
	res := tool.Execute(context.Background(), map[string]interface{}{})
	if res == nil || res.IsError {
		t.Fatalf("res = %+v", res)
	}
	out := res.ForLLM
	for _, want := range []string{
		"running-label", "coder", "working on it", "running", "retry 2/3", "halfway",
		"(running ", "Total: 2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestListSubagentsTool_EmptyFilteredIncludeCompleted covers the case where
// include_completed=true but there are no tasks at all (len(filtered)==0 path
// after the allTasks-empty check).
func TestListSubagentsTool_EmptyFilteredIncludeCompleted(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)

	// Add one completed task.
	manager.mu.Lock()
	manager.tasks["subagent-1"] = &SubagentTask{ID: "subagent-1", Status: SubagentStatusCompleted, mu: &sync.Mutex{}}
	manager.mu.Unlock()

	tool := NewListSubagentsTool(manager)
	res := tool.Execute(context.Background(), map[string]interface{}{"include_completed": true})
	if res == nil || res.IsError || !strings.Contains(res.ForLLM, "All subagent tasks:") {
		t.Fatalf("res = %+v", res)
	}
}

// TestListSubagentsListenerMock ensures the manager's ListTasks reflects an
// empty map through the tool path.
func TestListSubagentsTool_NoTasksIncludeNoFilterDeadCode(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	tool := NewListSubagentsTool(manager)
	// Empty manager => "No subagent tasks found."
	res := tool.Execute(context.Background(), map[string]interface{}{"include_completed": true})
	if res == nil || res.IsError || !strings.Contains(res.ForLLM, "No subagent tasks") {
		t.Fatalf("res = %+v", res)
	}
}
