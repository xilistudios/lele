package agent

// Regression test for the per-iteration streaming draft reset.
//
// Streamed chunks are accumulated per SESSION (pkg/session/streaming.go), while
// the runner reasons per ITERATION. The iterations that `continue` without
// persisting anything — the bounded empty-response retry, and an
// all-tool-calls-invalid response with no content — never finalize the session
// tail, so without the reset the next iteration's reasoning is appended on top
// of the discarded iteration's and the surviving message shows N copies of the
// same thinking text.
//
// The provider below stands in for the native channel, which is what actually
// feeds the session from a stream (pkg/channels/native.go calls
// AppendReasoningChunk from its stream callback): every call streams one
// reasoning chunk into the session before returning the scripted response.

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

// streamingDraftMockProvider streams a per-call reasoning chunk into the session
// (mirroring pkg/channels/native.go) and then returns blank content, which
// drives the loop into its bounded empty-response retry path.
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

// TestRunAgentLoop_DiscardedIterationReasoningIsNotConcatenated pins the fix at
// the agent level: with every iteration discarded (empty responses all the way
// to the limit) the turn persists nothing, so the session's streaming tail IS
// the surviving record of the turn. It must carry the LAST iteration's
// reasoning only.
func TestRunAgentLoop_DiscardedIterationReasoningIsNotConcatenated(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.MaxIterations = 0 // unlimited: only the empty-retry cap ends the turn

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

	// 1 initial call + maxConsecutiveEmptyResponses retries; the last of them
	// tripped the limit and ended the turn.
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
