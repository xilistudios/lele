// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

// Regression test for the streaming orphan produced by the agent loop itself.
//
// The loop detector (llm_runner.go) injects its guidance with
// AddMessage(sessionKey, "user", guidance) BEFORE the assistant message with
// the tool calls is persisted. When the stream handler has already written an
// in-progress assistant message, that AddMessage used to append the guidance
// right after the streamed assistant and leave it Streaming=true forever: it
// was persisted, survived restarts and was replayed to the model on every
// later turn. The session-layer fix finalizes the streamed tail in place before
// the non-assistant append, so the guidance lands immediately after a finalized
// assistant and no streaming flag survives the turn.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

// TestRunLLMIteration_LoopGuidanceDoesNotLeaveStreamingOrphan drives the REAL
// runner with a provider that returns the SAME tool-call signature on
// iterations 1-3 (loopDetector.Check triggers at MaxLoopRepetitions) and plain
// text on iteration 4. Every call also appends a streamed chunk, exactly like
// the stream handler does while the response is being produced.
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

	// The same signature on every iteration is what the loop detector compares.
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
			// The stream handler writes the in-progress assistant message
			// before the response completes, exactly like a real stream.
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
