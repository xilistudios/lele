package tools

import (
	"context"
	"testing"

	"github.com/xilistudios/lele/pkg/group"
)

// groupManagerForTest builds a manager backed by the shared test executor/publisher.
func groupManagerForTest(exec *testExecutor, pub *testPublisher) *group.GroupManager {
	return group.NewGroupManager(testResolve, exec.execute, pub.publish)
}

// TestGroupChatTool_Metadata covers the otherwise-uncovered Name/Description/Parameters.
func TestGroupChatTool_Metadata(t *testing.T) {
	tool := NewGroupChatTool(nil)
	if tool.Name() != "group_chat" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("expected description")
	}
	params := tool.Parameters()
	if params == nil {
		t.Fatal("nil parameters")
	}
	props := params["properties"].(map[string]interface{})
	for _, k := range []string{"task", "participants", "strategy", "rounds", "moderator", "parallel", "max_turns", "max_tokens_per_turn", "total_token_budget", "stop_keywords"} {
		if _, ok := props[k]; !ok {
			t.Errorf("missing param %q", k)
		}
	}
	// Required array includes task and participants.
	req, ok := params["required"].([]interface{})
	if !ok || len(req) != 2 {
		t.Fatalf("required = %v", params["required"])
	}
}

// TestGroupChatTool_SetContextAndAllowlist covers the setters.
func TestGroupChatTool_SetContextAndAllowlist(t *testing.T) {
	tool := NewGroupChatTool(nil)
	tool.SetContext("ws", "c1")
	if tool.originChannel != "ws" || tool.originChatID != "c1" {
		t.Fatalf("origin = %s:%s", tool.originChannel, tool.originChatID)
	}
	call := 0
	tool.SetAllowlistChecker(func(id string) bool {
		call++
		return true
	})
	if !tool.allowlistCheck("x") {
		t.Fatal("expected allowed")
	}
	if call != 1 {
		t.Fatal("expected allowlist checker invoked")
	}
}

// TestGroupChatTool_InterfaceCompliance verifies the contextual tool interface.
func TestGroupChatTool_InterfaceComplianceExtra(t *testing.T) {
	var _ ContextualTool = NewGroupChatTool(nil)
}

// TestGroupChatTool_UntrimmedTaskSpaces verifies task with only spaces is rejected.
func TestGroupChatTool_UntrimmedTaskSpaces(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := groupManagerForTest(exec, pub)
	tool := NewGroupChatTool(gm)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"task":         "   ",
		"participants": []interface{}{"a"},
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for whitespace-only task")
	}
}

// TestGroupChatTool_NonStringParticipant verifies a participant list with
// non-string members is filtered and can produce an empty list.
func TestGroupChatTool_NonStringParticipant(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := groupManagerForTest(exec, pub)
	tool := NewGroupChatTool(gm)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"task":         "solve",
		"participants": []interface{}{123},
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for empty participant list after filtering")
	}
}

// TestGroupChatTool_Roles_ModeratorAndMOA exercises the moa/moderator role assignment
// branches with a moderator designation.
func TestGroupChatTool_Roles_ModeratorAndMOA(t *testing.T) {
	tests := []struct {
		name     string
		strategy string
	}{
		{name: "moa", strategy: "moa"},
		{name: "moderator", strategy: "moderator"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exec := &testExecutor{}
			pub := &testPublisher{}
			gm := groupManagerForTest(exec, pub)
			tool := NewGroupChatTool(gm)
			res := tool.Execute(context.Background(), map[string]interface{}{
				"task":         "solve roles",
				"participants": []interface{}{"a", "b"},
				"strategy":     tc.strategy,
				"moderator":    "b",
				"rounds":       float64(1),
				"parallel":     true,
			})
			if res == nil || res.IsError {
				t.Fatalf("expected success, got %+v", res)
			}
		})
	}
}

// TestGroupChatTool_StartError_Unresolvable verifies the start-error path when a
// participant cannot be resolved.
func TestGroupChatTool_StartError_Unresolvable(t *testing.T) {
	exec := &testExecutor{}
	pub := &testPublisher{}
	gm := group.NewGroupManager(func(id string) (group.AgentContext, bool) {
		if id == "zz" {
			return group.AgentContext{AgentID: id, Name: "zz"}, true
		}
		return group.AgentContext{}, false
	}, exec.execute, pub.publish)

	tool := NewGroupChatTool(gm)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"task":         "solve",
		"participants": []interface{}{"a"},
		"strategy":     "round_robin",
		"rounds":       float64(1),
	})
	if res == nil || !res.IsError {
		t.Fatal("expected start error for unresolvable participant")
	}
	if !containsStr(res.ForLLM, "failed to start") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}