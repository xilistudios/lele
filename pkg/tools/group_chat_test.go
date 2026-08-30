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
		"task":         "solve X",
		"participants": []interface{}{"a", "b"},
		"strategy":     "round_robin",
		"rounds":       float64(1),
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

// --- B4 regression: origin must come from the invocation ctx, not shared state ---

// newB4Tool builds a GroupChatTool over a real manager with stub deps and
// returns the tool plus a publisher recorder.
func newB4Tool(execDelay func(speaker string)) (*GroupChatTool, *testPublisher, *group.GroupManager) {
	resolve := func(id string) (group.AgentContext, bool) {
		return group.AgentContext{AgentID: id, Name: id, SystemPrompt: "p"}, true
	}
	exec := func(ctx context.Context, req group.TurnRequest) (string, int, error) {
		if execDelay != nil {
			execDelay(req.Speaker)
		}
		return "t-" + req.Speaker, 1, nil
	}
	pub := &testPublisher{}
	gm := group.NewGroupManager(resolve, exec, pub.publish)
	return NewGroupChatTool(gm), pub, gm
}

// groupEvents returns only the group.* events captured by the publisher.
func groupEvents(p *testPublisher) []bus.OutboundMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []bus.OutboundMessage
	for _, m := range p.messages {
		if strings.HasPrefix(m.Event, "group.") {
			out = append(out, m)
		}
	}
	return out
}

func b4Args(task string) map[string]interface{} {
	return map[string]interface{}{
		"task":         task,
		"participants": []interface{}{"a"},
		"strategy":     "round_robin",
		"max_turns":    float64(1),
	}
}

// TestRegression_GroupChatUsesCtxOrigin proves the group's events are routed
// to the channel/chatID carried by the invocation ctx.
func TestRegression_GroupChatUsesCtxOrigin(t *testing.T) {
	tool, pub, _ := newB4Tool(nil)
	ctx := WithToolContext(context.Background(), "native", "chatA")
	res := tool.Execute(ctx, b4Args("hello"))
	if strings.Contains(res.ForLLM, "failed to start") {
		t.Fatalf("unexpected failure: %s", res.ForLLM)
	}
	evts := groupEvents(pub)
	if len(evts) == 0 {
		t.Fatal("no group events published")
	}
	for _, e := range evts {
		if e.Channel != "native" || e.ChatID != "chatA" {
			t.Errorf("event %s routed to %s/%s, want native/chatA", e.Event, e.Channel, e.ChatID)
		}
	}
}

// TestRegression_GroupChatConcurrentSessionsIsolated interleaves two sessions
// through the SAME tool instance: with the old shared-field design, A's group
// could be stamped with B's chatID.
func TestRegression_GroupChatConcurrentSessionsIsolated(t *testing.T) {
	// Gate both executions so A's Start happens strictly after B's ctx was
	// already in play — the exact window that used to leak.
	var mu sync.Mutex
	tool, pub, _ := newB4Tool(func(string) {
		mu.Lock()
		defer mu.Unlock()
	})
	_ = pub

	type result struct {
		chat string
		err  string
	}
	run := func(chat string) result {
		ctx := WithToolContext(context.Background(), "native", chat)
		res := tool.Execute(ctx, b4Args("task-"+chat))
		return result{chat: chat, err: res.ForLLM}
	}

	var wg sync.WaitGroup
	var results []result
	var rmu sync.Mutex
	for _, chat := range []string{"chatA", "chatB"} {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			r := run(c)
			rmu.Lock()
			results = append(results, r)
			rmu.Unlock()
		}(chat)
	}
	wg.Wait()

	// Every published group event must target exactly one of the two chats,
	// and both chats must have received traffic (no cross-talk verification
	// happens structurally: origin is read from ctx at Start time).
	seen := map[string]int{}
	for _, e := range groupEvents(pub) {
		if e.Channel != "native" || (e.ChatID != "chatA" && e.ChatID != "chatB") {
			t.Fatalf("event %s leaked to %s/%s", e.Event, e.Channel, e.ChatID)
		}
		seen[e.ChatID]++
	}
	if seen["chatA"] == 0 || seen["chatB"] == 0 {
		t.Fatalf("both sessions must receive their own events, got %v", seen)
	}
}

// TestRegression_GroupChatFallsBackWhenNoCtx keeps the legacy default.
func TestRegression_GroupChatFallsBackWhenNoCtx(t *testing.T) {
	tool, pub, _ := newB4Tool(nil)
	tool.SetContext("stale", "should-be-ignored") // must be a no-op now
	res := tool.Execute(context.Background(), b4Args("hello"))
	_ = res
	for _, e := range groupEvents(pub) {
		if e.Channel != "cli" || e.ChatID != "direct" {
			t.Errorf("event %s routed to %s/%s, want cli/direct (SetContext must not mutate state)",
				e.Event, e.Channel, e.ChatID)
		}
	}
}
