package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestListSubagentsTool_Name(t *testing.T) {
	tool := NewListSubagentsTool(nil)
	if tool.Name() != "list_active_subagents" {
		t.Errorf("Expected name 'list_active_subagents', got '%s'", tool.Name())
	}
}

func TestListSubagentsTool_Description(t *testing.T) {
	tool := NewListSubagentsTool(nil)
	desc := tool.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
	if !strings.Contains(desc, "subagent") {
		t.Errorf("Description should mention 'subagent', got: %s", desc)
	}
}

func TestListSubagentsTool_Parameters(t *testing.T) {
	tool := NewListSubagentsTool(nil)
	params := tool.Parameters()
	if params == nil {
		t.Fatal("Parameters should not be nil")
	}
	if params["type"] != "object" {
		t.Errorf("Expected type 'object', got %v", params["type"])
	}
}

func TestListSubagentsTool_NilManager(t *testing.T) {
	tool := NewListSubagentsTool(nil)
	result := tool.Execute(context.Background(), map[string]interface{}{})

	if !result.IsError {
		t.Error("Expected error for nil manager")
	}
}

func TestListSubagentsTool_NoTasks(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	tool := NewListSubagentsTool(manager)

	result := tool.Execute(context.Background(), map[string]interface{}{})

	if result.IsError {
		t.Errorf("Expected success, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "No subagent tasks") {
		t.Errorf("Expected 'No subagent tasks', got: %s", result.ForLLM)
	}
}

func TestListSubagentsTool_ActiveTasks(t *testing.T) {
	// Use a delayed provider so tasks stay running long enough to list them.
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 2)

	// Spawn two tasks.
	_, err := manager.Spawn(context.Background(), "Task 1", "first", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn 1 failed: %v", err)
	}

	_, err = manager.Spawn(context.Background(), "Task 2", "second", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn 2 failed: %v", err)
	}

	// List active subagents while they're running.
	tool := NewListSubagentsTool(manager)
	result := tool.Execute(context.Background(), map[string]interface{}{})

	if result.IsError {
		t.Errorf("Expected success, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Active subagents:") {
		t.Errorf("Expected 'Active subagents:' header, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected 'subagent-1' in list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Expected 'subagent-2' in list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "first") {
		t.Errorf("Expected label 'first' in list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "second") {
		t.Errorf("Expected label 'second' in list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Total: 2") {
		t.Errorf("Expected 'Total: 2 task(s)', got: %s", result.ForLLM)
	}

	// Wait for tasks to complete.
	<-resultCh
	<-resultCh
}

func TestListSubagentsTool_IncludeCompleted(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{
		"STATUS: completed\nSUMMARY: Done\nDETAILS:\nCompleted.",
	}}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Quick task", "quick", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	<-resultCh // Wait for completion.

	tool := NewListSubagentsTool(manager)

	// Without include_completed: should show "no active subagents".
	result := tool.Execute(context.Background(), map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "No active subagents") {
		t.Errorf("Expected 'No active subagents', got: %s", result.ForLLM)
	}

	// With include_completed: should show the completed task.
	result = tool.Execute(context.Background(), map[string]interface{}{
		"include_completed": true,
	})
	if !strings.Contains(result.ForLLM, "All subagent tasks:") {
		t.Errorf("Expected 'All subagent tasks:' header, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected 'subagent-1' in list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "completed") {
		t.Errorf("Expected 'completed' status in list, got: %s", result.ForLLM)
	}
}

func TestListSubagentsTool_NeedsContextShown(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{
		"STATUS: needs_context\nSUMMARY: Missing info\nCONTEXT_NEEDED: Need a path\nDETAILS:\nWaiting for path.",
	}}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Context task", "ctx", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	<-resultCh // Wait for needs_context.

	tool := NewListSubagentsTool(manager)
	result := tool.Execute(context.Background(), map[string]interface{}{})

	// needs_context tasks should show in the active list.
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected 'subagent-1' in active list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "needs_context") {
		t.Errorf("Expected 'needs_context' status, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Need a path") {
		t.Errorf("Expected context request in list, got: %s", result.ForLLM)
	}
}

func TestListSubagentsTool_SessionScoping(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 3)

	// Spawn tasks from two different sessions.
	_, err := manager.Spawn(context.Background(), "Task A", "alpha", "", "native", "session-A",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn A failed: %v", err)
	}
	_, err = manager.Spawn(context.Background(), "Task B", "beta", "", "native", "session-B",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn B failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)

	// Without session context: all tasks visible (backwards compatibility).
	result := tool.Execute(context.Background(), map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") || !strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Expected both tasks without session context, got: %s", result.ForLLM)
	}

	// With session A context: only task A visible.
	ctxA := WithAgentToolContext(context.Background(), "test-agent", "native:session-A")
	result = tool.Execute(ctxA, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected task A with session A context, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Task B from another session should be hidden, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Total: 1") {
		t.Errorf("Expected 'Total: 1 task(s)' with session scoping, got: %s", result.ForLLM)
	}

	// With session B context: only task B visible.
	ctxB := WithAgentToolContext(context.Background(), "test-agent", "native:session-B")
	result = tool.Execute(ctxB, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Expected task B with session B context, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Task A from another session should be hidden, got: %s", result.ForLLM)
	}

	// From an unrelated session: no tasks.
	ctxC := WithAgentToolContext(context.Background(), "test-agent", "native:session-C")
	result = tool.Execute(ctxC, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "No active subagents in this session") {
		t.Errorf("Expected session-scoped empty message, got: %s", result.ForLLM)
	}

	// A subagent child session (native:session-A:subagent-1) should still see
	// tasks of its origin session.
	ctxChild := WithAgentToolContext(context.Background(), "test-agent", "native:session-A:subagent-1")
	result = tool.Execute(ctxChild, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected child session to see origin session tasks, got: %s", result.ForLLM)
	}

	// Wait for tasks to complete.
	<-resultCh
	<-resultCh
}

func TestListSubagentsTool_SessionScopingViaChatIDFallback(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 2)

	_, err := manager.Spawn(context.Background(), "Task A", "alpha", "", "native", "session-A",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn A failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)

	// Subagent toolloop passes channel/chatID but no agent context; chatID
	// carries the full subagent session key.
	ctx := WithToolContext(context.Background(), "native", "native:session-A:subagent-1")
	result := tool.Execute(ctx, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected chatID fallback to scope to session A, got: %s", result.ForLLM)
	}

	<-resultCh
}

func TestSameSessionKey(t *testing.T) {
	cases := []struct {
		origin, session string
		want            bool
	}{
		{"native:abc", "native:abc", true},
		{"native:abc", "native:abc:subagent-1", true}, // child subagent session
		{"native:abc", "native:abc:cron-1:subagent-2", true},
		{"native:abc", "native:abd", false},
		{"native:abc", "", false},
		{"", "native:abc", false},
		{"native:abc:subagent-1", "native:abc", false}, // origin is more specific
		{"telegram:123", "telegram:123", true},
	}
	for _, tc := range cases {
		if got := sameSessionKey(tc.origin, tc.session); got != tc.want {
			t.Errorf("sameSessionKey(%q, %q) = %v, want %v", tc.origin, tc.session, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Regression tests: list_active_subagents session-key asymmetry.
//
// SpawnWithOptions records a task's OriginSessionKey via BuildOriginSessionKey
// ("<channel>:<chatID>"), but the list tool used to take the caller's key from
// the agent-loop session key, which at runtime carries NO channel prefix
// (rest_chat publishes the raw UUID as ChatID/SessionKey). The comparison
// therefore filtered out every task ("No active subagents in this session"
// while subagents were running). The tool now derives the caller's key with
// the same builder, so both sides share one invariant by construction.
// ---------------------------------------------------------------------------

const (
	reproChatUUID = "da9ad89c-08fd-4db8-b30f-fb8687eb5230"
	otherChatUUID = "11111111-2222-3333-4444-555555555555"
)

// TestBuildOriginSessionKey pins the canonical key-building rules shared by
// the spawn side and the list side.
func TestBuildOriginSessionKey(t *testing.T) {
	cases := []struct {
		channel, chatID string
		want            string
	}{
		{"native", "abc", "native:abc"},
		{"native", "native:abc", "native:abc"}, // chatID already embeds the channel prefix
		{"", "abc", "abc"},                     // no channel -> chatID as-is (never ":abc")
		{"native", "", "native"},               // no chatID -> channel as-is (never "native:")
		{"", "", ""},                           // both empty -> empty (no phantom key)
		{"telegram", "123", "telegram:123"},
	}
	for _, tc := range cases {
		if got := BuildOriginSessionKey(tc.channel, tc.chatID); got != tc.want {
			t.Errorf("BuildOriginSessionKey(%q, %q) = %q, want %q", tc.channel, tc.chatID, got, tc.want)
		}
	}
}

// TestListSubagentsTool_MatchesUnprefixedRuntimeSessionKey replicates the live
// bug: a task spawned with channel "native" and a bare-UUID chatID must be
// visible to the caller that spawned it, even though the runtime session key
// injected by the agent tool executor carries no channel prefix.
func TestListSubagentsTool_MatchesUnprefixedRuntimeSessionKey(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Task 1", "one", "", "native", reproChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)

	t.Run("tool context only", func(t *testing.T) {
		ctx := WithToolContext(context.Background(), "native", reproChatUUID)
		result := tool.Execute(ctx, map[string]interface{}{})
		if result.IsError {
			t.Fatalf("Expected success, got error: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "subagent-1") {
			t.Errorf("Expected 'subagent-1' in list, got: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "Total: 1") {
			t.Errorf("Expected 'Total: 1 task(s)', got: %s", result.ForLLM)
		}
	})

	t.Run("runtime parity with unprefixed session key", func(t *testing.T) {
		// Real agent turn: the tool executor injects BOTH the channel/chatID
		// (same values spawn sees) and the session key, which at runtime is
		// the raw chat UUID without the "native:" prefix.
		ctx := WithAgentToolContext(
			WithToolContext(context.Background(), "native", reproChatUUID),
			"test-agent", reproChatUUID)
		result := tool.Execute(ctx, map[string]interface{}{})
		if result.IsError {
			t.Fatalf("Expected success, got error: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "subagent-1") {
			t.Errorf("Expected 'subagent-1' in list, got: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "Total: 1") {
			t.Errorf("Expected 'Total: 1 task(s)', got: %s", result.ForLLM)
		}
	})

	<-resultCh
}

// TestListSubagentsTool_AgentsIsolationPreserved confirms the per-chat
// isolation introduced in #224 survives the new key derivation: a caller
// scoped to chat A must not see chat B's tasks.
func TestListSubagentsTool_AgentsIsolationPreserved(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 2)

	_, err := manager.Spawn(context.Background(), "Task A", "alpha", "", "native", reproChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn A failed: %v", err)
	}
	_, err = manager.Spawn(context.Background(), "Task B", "beta", "", "native", otherChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn B failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)
	ctxA := WithToolContext(context.Background(), "native", reproChatUUID)
	result := tool.Execute(ctxA, map[string]interface{}{})

	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected caller's own task 'subagent-1', got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Task from another chat must stay hidden, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Total: 1") {
		t.Errorf("Expected 'Total: 1 task(s)', got: %s", result.ForLLM)
	}

	<-resultCh
	<-resultCh
}

// TestListSubagentsTool_SubagentToolLoopSeesParentTasks verifies that the
// subagent toolloop path (subagent_runner passes task.OriginChannel /
// task.OriginChatID, which ExecuteWithContext re-injects) derives a key equal
// to the PARENT's OriginSessionKey, so a child sees its parent's tasks — and
// only those.
func TestListSubagentsTool_SubagentToolLoopSeesParentTasks(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 2)

	_, err := manager.Spawn(context.Background(), "Parent task", "parent", "", "native", reproChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn parent failed: %v", err)
	}
	_, err = manager.Spawn(context.Background(), "Unrelated task", "other-chat", "", "native", otherChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn unrelated failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)

	// Child toolloop context: channel + the parent's OriginChatID, no agent
	// session-key context (RunToolLoop does not inject one).
	childCtx := WithToolContext(context.Background(), "native", reproChatUUID)
	result := tool.Execute(childCtx, map[string]interface{}{})

	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected child to see parent's 'subagent-1', got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Unrelated chat's task must stay hidden from the child, got: %s", result.ForLLM)
	}

	<-resultCh
	<-resultCh
}

// TestListSubagentsTool_RoutedSessionKeyWithChannelChatID checks precedence:
// when the caller carries BOTH channel/chatID and a routed agent session key
// ("agent:<id>:main"), the channel/chatID-derived key wins and the caller's
// own tasks are listed.
func TestListSubagentsTool_RoutedSessionKeyWithChannelChatID(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Task 1", "one", "", "native", reproChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)
	ctx := WithAgentToolContext(
		WithToolContext(context.Background(), "native", reproChatUUID),
		"software-engineer", "agent:software-engineer:main")
	result := tool.Execute(ctx, map[string]interface{}{})

	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected 'subagent-1' despite routed session key, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Total: 1") {
		t.Errorf("Expected 'Total: 1 task(s)', got: %s", result.ForLLM)
	}

	<-resultCh
}

// TestListSubagentsTool_ScopingHidesCount makes the empty result debuggable:
// when tasks exist but the session scoping discards all of them, the message
// must report how many were hidden instead of suggesting there are none.
func TestListSubagentsTool_ScopingHidesCount(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Task X", "xray", "", "native", reproChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)
	ctx := WithToolContext(context.Background(), "native", otherChatUUID)

	result := tool.Execute(ctx, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "No active subagents in this session") {
		t.Errorf("Expected session-scoped empty message, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "1 task(s) from other sessions were hidden") {
		t.Errorf("Expected hidden-task count in message, got: %s", result.ForLLM)
	}

	result = tool.Execute(ctx, map[string]interface{}{"include_completed": true})
	if !strings.Contains(result.ForLLM, "1 task(s) from other sessions were hidden") {
		t.Errorf("Expected hidden-task count with include_completed, got: %s", result.ForLLM)
	}

	<-resultCh
}

// TestListSubagentsTool_SessionKeyOnlyFallback covers precedence 2 of
// Execute's key resolution: the context carries NO channel/chatID (no
// WithToolContext, so ToolContextFromCtx returns empty strings) but DOES
// carry a session key injected via WithAgentToolContext. In that case the
// tool must scope with the raw session key as-is, and sameSessionKey must
// still let a child/alias session see its origin's tasks.
//
// This is the CLI/test path (and any caller that only gets the agent-loop
// session key); precedence 1 (channel+chatID) is covered by the tests above.
func TestListSubagentsTool_SessionKeyOnlyFallback(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 2)

	// Two tasks spawned from two different sessions/chats.
	_, err := manager.Spawn(context.Background(), "Task A", "alpha", "", "native", "uuid-A",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn A failed: %v", err)
	}
	_, err = manager.Spawn(context.Background(), "Task B", "beta", "", "native", "uuid-B",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn B failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)

	// Precondition: without any context at all, both tasks are visible
	// (precedence 3 -> no scoping). This pins that the filtering below is
	// caused by the session key, not by something else.
	result := tool.Execute(context.Background(), map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") || !strings.Contains(result.ForLLM, "subagent-2") {
		t.Fatalf("Expected both tasks with no context, got: %s", result.ForLLM)
	}

	cases := []struct {
		name       string
		sessionKey string
	}{
		{"raw session key", "native:uuid-A"},
		// Conversation alias: ":chat:N" suffix on the origin key.
		{"conversation alias", "native:uuid-A:chat:2"},
		// Subagent child session of the origin.
		{"child subagent session", "native:uuid-A:subagent-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Only WithAgentToolContext: channel/chatID stay empty, so
			// Execute falls through to precedence 2.
			ctx := WithAgentToolContext(context.Background(), "test-agent", tc.sessionKey)
			result := tool.Execute(ctx, map[string]interface{}{})

			if result.IsError {
				t.Fatalf("Expected success, got error: %s", result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "subagent-1") {
				t.Errorf("Expected own task 'subagent-1' for key %q, got: %s", tc.sessionKey, result.ForLLM)
			}
			if strings.Contains(result.ForLLM, "subagent-2") {
				t.Errorf("Task of another session must stay hidden for key %q, got: %s", tc.sessionKey, result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "Total: 1") {
				t.Errorf("Expected 'Total: 1 task(s)' for key %q, got: %s", tc.sessionKey, result.ForLLM)
			}
		})
	}

	// Wait for tasks to complete (drain the callback channel).
	<-resultCh
	<-resultCh
}
