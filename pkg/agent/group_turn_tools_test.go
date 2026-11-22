package agent

import (
	"context"
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

func toolNames(defs []providers.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Function.Name)
	}
	return names
}

func assertNoTool(t *testing.T, defs []providers.ToolDefinition, name string) {
	t.Helper()
	for _, d := range defs {
		if d.Function.Name == name {
			t.Fatalf("tool %q must not be offered to group participants, got %v", name, toolNames(defs))
		}
	}
}

func assertTool(t *testing.T, defs []providers.ToolDefinition, name string) {
	t.Helper()
	found := false
	for _, d := range defs {
		if d.Function.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tool %q should be offered, got %v", name, toolNames(defs))
	}
}

func groupTurnSampleToolDefs() []providers.ToolDefinition {
	mk := func(name string) providers.ToolDefinition {
		return providers.ToolDefinition{
			Type:     "function",
			Function: providers.ToolFunctionDefinition{Name: name, Description: "tool " + name},
		}
	}
	return []providers.ToolDefinition{mk("group_chat"), mk("read_image"), mk("read_video"), mk("exec")}
}

func TestRegression_GroupTurnExcludesGroupChat(t *testing.T) {
	withVision := filterToolDefs(groupTurnSampleToolDefs(), true, true, groupTurnExcludedTools)
	assertNoTool(t, withVision, "group_chat")
	assertTool(t, withVision, "exec")
	assertTool(t, withVision, "read_image")
	assertTool(t, withVision, "read_video")

	withoutVision := filterToolDefs(groupTurnSampleToolDefs(), false, true, groupTurnExcludedTools)
	assertNoTool(t, withoutVision, "group_chat")
	assertNoTool(t, withoutVision, "read_image")
	assertTool(t, withoutVision, "read_video")
	assertTool(t, withoutVision, "exec")

	if !groupTurnExcludedTools["group_chat"] {
		t.Fatal("groupTurnExcludedTools must exclude \"group_chat\"")
	}
	if len(groupTurnExcludedTools) != 1 {
		t.Errorf("groupTurnExcludedTools = %v, want only group_chat", groupTurnExcludedTools)
	}
}

func TestFilterToolDefs_PureBehaviour(t *testing.T) {
	defs := groupTurnSampleToolDefs()

	got := filterToolDefs(defs, true, true, nil)
	assertTool(t, got, "group_chat")
	assertTool(t, got, "read_image")
	assertTool(t, got, "read_video")
	assertTool(t, got, "exec")

	visionOnly := filterToolDefs(defs, true, false, nil)
	assertTool(t, visionOnly, "read_video")
	assertTool(t, visionOnly, "read_image")
	assertTool(t, visionOnly, "group_chat")
	assertTool(t, visionOnly, "exec")

	videoOnly := filterToolDefs(defs, false, true, nil)
	assertNoTool(t, videoOnly, "read_image")
	assertTool(t, videoOnly, "read_video")
	assertTool(t, videoOnly, "group_chat")
	assertTool(t, videoOnly, "exec")

	neither := filterToolDefs(defs, false, false, nil)
	assertNoTool(t, neither, "read_image")
	assertNoTool(t, neither, "read_video")
	assertTool(t, neither, "group_chat")
	assertTool(t, neither, "exec")

	if len(defs) != 4 {
		t.Fatalf("input slice mutated: len = %d, want 4", len(defs))
	}

	if names := toolNames(filterToolDefs(defs, true, true, map[string]bool{"read_image": true})); len(names) != 3 ||
		names[0] != "group_chat" || names[1] != "read_video" || names[2] != "exec" {
		t.Errorf("order not preserved: got %v", names)
	}

	if out := filterToolDefs(nil, true, true, groupTurnExcludedTools); len(out) != 0 {
		t.Errorf("empty input produced %v, want no definitions", toolNames(out))
	}
}

func configureGroupTurnVision(t *testing.T, lr *llmRunnerImpl) {
	t.Helper()
	lr.al.cfg().Providers = &config.ProvidersConfig{
		Named: map[string]config.NamedProviderConfig{
			"test-provider": {
				Type: "openai",
				Models: map[string]config.ProviderModelConfig{
					"vision-model": {Vision: true},
					"text-model":   {Vision: false},
				},
			},
		},
	}
}

func TestRunGroupTurn_ExcludesGroupChatFromProviderDefs(t *testing.T) {
	cases := []struct {
		name          string
		model         string
		wantReadImage bool
	}{
		{name: "vision_model", model: "test-provider:vision-model", wantReadImage: true},
		{name: "non_vision_model", model: "test-provider:text-model", wantReadImage: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := &providers.LLMResponse{Content: "done", ToolCalls: []providers.ToolCall{}}
			lr, agent, cleanup := createGroupTurnTestHarness(t, response)
			defer cleanup()

			configureGroupTurnVision(t, lr)
			agent.Model = tc.model

			agent.Tools.Register(&llmRunnerMockCustomTool{name: "group_chat"})
			agent.Tools.Register(&llmRunnerMockCustomTool{name: "read_image"})
			agent.Tools.Register(&llmRunnerMockCustomTool{name: "exec"})

			var captured []providers.ToolDefinition
			lr.al.registry.mu.Lock()
			lr.al.registry.agents["test-agent"].Provider = &llmRunnerMockLLMProvider{
				onChatCalled: func(_ context.Context, _ []providers.Message, defs []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
					captured = defs
					return response, nil
				},
			}
			lr.al.registry.mu.Unlock()

			_, _, err := lr.runGroupTurn(context.Background(), group.TurnRequest{
				GroupID:      "g1",
				Speaker:      "test-agent",
				SystemPrompt: "sys",
				Instruction:  "go",
				EnableTools:  true,
			})
			if err != nil {
				t.Fatalf("runGroupTurn: %v", err)
			}

			if _, ok := agent.Tools.Get("group_chat"); !ok {
				t.Fatal("test setup broken: registry should advertise group_chat")
			}

			assertNoTool(t, captured, "group_chat")
			assertTool(t, captured, "exec")
			if tc.wantReadImage {
				assertTool(t, captured, "read_image")
			} else {
				assertNoTool(t, captured, "read_image")
			}
		})
	}
}

func TestRunGroupTurn_ToolsStillOfferedWhenEnabled(t *testing.T) {
	response := &providers.LLMResponse{Content: "done", ToolCalls: []providers.ToolCall{}}
	lr, agent, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	configureGroupTurnVision(t, lr)
	agent.Model = "test-provider:vision-model"
	agent.Tools.Register(&llmRunnerMockCustomTool{name: "group_chat"})
	agent.Tools.Register(&llmRunnerMockCustomTool{name: "exec"})

	var captured []providers.ToolDefinition
	calls := 0
	lr.al.registry.mu.Lock()
	lr.al.registry.agents["test-agent"].Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(_ context.Context, _ []providers.Message, defs []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
			calls++
			captured = defs
			return response, nil
		},
	}
	lr.al.registry.mu.Unlock()

	if _, _, err := lr.runGroupTurn(context.Background(), group.TurnRequest{
		GroupID: "g1", Speaker: "test-agent", SystemPrompt: "sys", Instruction: "go",
		EnableTools: true,
	}); err != nil {
		t.Fatalf("runGroupTurn: %v", err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
	assertTool(t, captured, "exec")
	assertNoTool(t, captured, "group_chat")

	captured = nil
	if _, _, err := lr.runGroupTurn(context.Background(), group.TurnRequest{
		GroupID: "g2", Speaker: "test-agent", SystemPrompt: "sys", Instruction: "go",
		EnableTools: false,
	}); err != nil {
		t.Fatalf("runGroupTurn: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("tools offered with EnableTools=false: %v", toolNames(captured))
	}
	_ = os.TempDir()
}
