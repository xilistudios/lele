package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/group"
)

// --- test helpers (mirror pkg/group/manager_test.go patterns) ---

func testResolve(agentID string) (group.AgentContext, bool) {
	known := map[string]string{
		"a": "Agent A",
		"b": "Agent B",
	}
	name, ok := known[agentID]
	if !ok {
		return group.AgentContext{}, false
	}
	return group.AgentContext{
		AgentID:      agentID,
		Name:         name,
		SystemPrompt: "persona of " + agentID,
	}, true
}

type testExecutor struct {
	mu        sync.Mutex
	callCount int
}

func (e *testExecutor) execute(_ context.Context, req group.TurnRequest) (string, int, error) {
	e.mu.Lock()
	e.callCount++
	n := e.callCount
	e.mu.Unlock()
	return fmt.Sprintf("turn-%d-%s", n, req.Speaker), 10, nil
}

type testPublisher struct {
	mu       sync.Mutex
	messages []bus.OutboundMessage
}

func (p *testPublisher) publish(msg bus.OutboundMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, msg)
}

// --- tests ---

func TestGroupChatTool_Success(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := group.NewGroupManager(testResolve, exec.execute, pub.publish)

	tool := NewGroupChatTool(gm)
	tool.SetContext("test", "chat")

	result := tool.Execute(context.Background(), map[string]interface{}{
		"task":                "solve X",
		"participants":        []interface{}{"a", "b"},
		"strategy":            "round_robin",
		"rounds":              float64(1),
		"parallel":            true,
		"max_turns":           float64(3),
		"max_tokens_per_turn": float64(512),
		"total_token_budget":  float64(2000),
		"stop_keywords":       []interface{}{"DONE", "", "converge"},
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	if result.ForLLM == "" {
		t.Fatal("expected non-empty ForLLM")
	}
	if !strings.Contains(result.ForLLM, "round_robin") {
		t.Errorf("expected header to mention strategy, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "a") || !strings.Contains(result.ForLLM, "b") {
		t.Errorf("expected header to mention participants, got: %s", result.ForLLM)
	}
}

func TestGroupChatTool_AllowlistDenied(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := group.NewGroupManager(testResolve, exec.execute, pub.publish)

	tool := NewGroupChatTool(gm)
	tool.SetAllowlistChecker(func(id string) bool {
		return id == "a"
	})

	result := tool.Execute(context.Background(), map[string]interface{}{
		"task":         "solve Y",
		"participants": []interface{}{"a", "b"},
		"strategy":     "round_robin",
		"rounds":       float64(1),
	})

	if !result.IsError {
		t.Fatal("expected error for denied participant")
	}
	if !strings.Contains(result.ForLLM, "b") {
		t.Errorf("error should mention denied agent 'b': %s", result.ForLLM)
	}
}

func TestGroupChatTool_InvalidStrategy(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := group.NewGroupManager(testResolve, exec.execute, pub.publish)

	tool := NewGroupChatTool(gm)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"task":         "solve Z",
		"participants": []interface{}{"a"},
		"strategy":     "invalid_strategy",
	})

	if !result.IsError {
		t.Fatal("expected error for invalid strategy")
	}
	if !strings.Contains(result.ForLLM, "invalid_strategy") {
		t.Errorf("error should mention the strategy: %s", result.ForLLM)
	}
}

func TestGroupChatTool_EmptyParticipants(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := group.NewGroupManager(testResolve, exec.execute, pub.publish)

	tool := NewGroupChatTool(gm)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"task":         "solve W",
		"participants": []interface{}{},
	})

	if !result.IsError {
		t.Fatal("expected error for empty participants")
	}
}

func TestGroupChatTool_EmptyTask(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := group.NewGroupManager(testResolve, exec.execute, pub.publish)

	tool := NewGroupChatTool(gm)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"task":         "",
		"participants": []interface{}{"a"},
	})

	if !result.IsError {
		t.Fatal("expected error for empty task")
	}
}

func TestGroupChatTool_MissingTask(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := group.NewGroupManager(testResolve, exec.execute, pub.publish)

	tool := NewGroupChatTool(gm)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"participants": []interface{}{"a"},
	})

	if !result.IsError {
		t.Fatal("expected error for missing task")
	}
}

func TestGroupChatTool_MissingParticipants(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := group.NewGroupManager(testResolve, exec.execute, pub.publish)

	tool := NewGroupChatTool(gm)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"task": "solve V",
	})

	if !result.IsError {
		t.Fatal("expected error for missing participants")
	}
}

func TestGroupChatTool_AllowlistAllAllowed(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := group.NewGroupManager(testResolve, exec.execute, pub.publish)

	tool := NewGroupChatTool(gm)
	tool.SetAllowlistChecker(func(id string) bool {
		return true // all allowed
	})

	result := tool.Execute(context.Background(), map[string]interface{}{
		"task":         "solve U",
		"participants": []interface{}{"a", "b"},
		"strategy":     "round_robin",
		"rounds":       float64(1),
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
}

func TestGroupChatTool_InterfaceCompliance(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := group.NewGroupManager(testResolve, exec.execute, pub.publish)

	tool := NewGroupChatTool(gm)

	// Tool interface
	var _ Tool = tool
	// ContextualTool interface
	var _ ContextualTool = tool
}
