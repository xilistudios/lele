package agent

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

type streamingDraftMockProvider struct {
	sessions   *session.SessionManager
	sessionKey string
	callCount  int
}

func (m *streamingDraftMockProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string,
	_ map[string]interface{}) (*providers.LLMResponse, error) {
	m.callCount++
	m.sessions.AppendReasoningChunk(m.sessionKey, fmt.Sprintf("think-%d", m.callCount))
	return &providers.LLMResponse{Content: "", ToolCalls: []providers.ToolCall{}}, nil
}

func (m *streamingDraftMockProvider) GetDefaultModel() string { return "mock-model" }

func TestRunAgentLoop_DiscardedIterationReasoningIsNotConcatenated(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.MaxIterations = 0

	const sessionKey = "test-session"
	provider := &streamingDraftMockProvider{sessions: agent.Sessions, sessionKey: sessionKey}
	agent.Provider = provider

	opts := processOptions{
		SessionKey:      sessionKey,
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "Hi",
		DefaultResponse: "Default fallback",
		EnableSummary:   false,
		SendResponse:    false,
	}

	if _, err := runner.runAgentLoop(context.Background(), agent, opts); err != nil {
		t.Fatalf("runAgentLoop returned an error: %v", err)
	}

	wantCalls := maxConsecutiveEmptyResponses + 1
	if provider.callCount != wantCalls {
		t.Fatalf("provider calls = %d, want %d", provider.callCount, wantCalls)
	}

	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) == 0 {
		t.Fatal("expected the user message in the session, got an empty history")
	}
	tail := history[len(history)-1]
	if tail.Role != "assistant" {
		t.Fatalf("expected the streamed assistant as the last message, got %q: %+v", tail.Role, history)
	}

	wantReasoning := fmt.Sprintf("think-%d", wantCalls)
	if tail.ReasoningContent != wantReasoning {
		t.Fatalf("discarded iterations' reasoning was concatenated onto the tail: got %q, want %q",
			tail.ReasoningContent, wantReasoning)
	}
}
