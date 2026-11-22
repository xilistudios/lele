package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

func TestRunLLMIteration_LoopGuidanceDoesNotLeaveStreamingOrphan(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "exec",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult("done")
		},
	})

	const sessionKey = "test-session-orphan"

	toolCalls := func() []providers.ToolCall {
		return []providers.ToolCall{{
			ID:        "call_loop",
			Type:      "function",
			Name:      "exec",
			Arguments: map[string]interface{}{"command": "true"},
		}}
	}

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			agent.Sessions.AppendAssistantChunk(sessionKey, "streamed")
			if callCount <= 3 {
				return &providers.LLMResponse{Content: "", ToolCalls: toolCalls()}, nil
			}
			return &providers.LLMResponse{Content: "done"}, nil
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "go"},
	}
	opts := processOptions{
		SessionKey:   sessionKey,
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	content, _, err := runner.runLLMIteration(context.Background(), agent, messages, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "done" {
		t.Fatalf("the turn did not finish with the plain-text answer, got %q", content)
	}
	if callCount != 4 {
		t.Fatalf("expected the loop guidance to be injected on iteration 3, got %d calls", callCount)
	}

	history := agent.Sessions.GetHistory(sessionKey)

	orphanIdx := -1
	for i, m := range history {
		if m.Streaming {
			t.Fatalf("message %d is still Streaming=true after the turn: %+v", i, m)
		}
		if m.Role == "assistant" && m.Content == "streamed" {
			orphanIdx = i
		}
	}
	if orphanIdx < 0 {
		t.Fatalf("the streamed assistant message is not in the history: %+v", history)
	}
	if orphanIdx+1 >= len(history) {
		t.Fatalf("the loop guidance was not appended after the streamed assistant: %+v", history)
	}
	next := history[orphanIdx+1]
	if next.Role != "user" || !strings.Contains(next.Content, "GUIDANCE") {
		t.Fatalf("expected the loop guidance immediately after the finalized streamed assistant, got %+v", next)
	}
}
