package agent

import (
	"context"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

func semLen(t *testing.T, al *AgentLoop, key string) (n int, exists bool) {
	t.Helper()
	raw, ok := al.sessionProcessing.Load(key)
	if !ok {
		return 0, false
	}
	ch, ok := raw.(chan struct{})
	if !ok {
		t.Fatalf("sessionProcessing[%q] is %T, want chan struct{}", key, raw)
	}
	return len(ch), true
}

func TestRegression_GroupTurnReleasesSessionKey(t *testing.T) {
	response := &providers.LLMResponse{
		Content:   "done",
		ToolCalls: []providers.ToolCall{},
	}
	lr, _, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	const key = "group:cleanup-1:test-agent"
	if _, exists := semLen(t, lr.al, key); exists {
		t.Fatalf("sessionProcessing already holds %q before the turn", key)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := lr.runGroupTurn(ctx, group.TurnRequest{
		GroupID:      "cleanup-1",
		Speaker:      "test-agent",
		SystemPrompt: "sys",
		Instruction:  "go",
	}); err != nil {
		t.Fatalf("runGroupTurn: %v", err)
	}

	if _, exists := semLen(t, lr.al, key); exists {
		t.Errorf("sessionProcessing still holds %q after the turn — semaphore leak (B6)", key)
	}
}

func TestRegression_GroupTurnHoldsSessionKeyWhileRunning(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})

	lr, _, cleanup := createGroupTurnTestHarness(t, &providers.LLMResponse{Content: "ok"})
	defer cleanup()

	lr.al.registry.mu.Lock()
	lr.al.registry.agents["test-agent"].Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
			close(entered)
			<-release
			return &providers.LLMResponse{Content: "ok"}, nil
		},
	}
	lr.al.registry.mu.Unlock()

	const key = "group:cleanup-2:test-agent"
	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, _, err := lr.runGroupTurn(context.Background(), group.TurnRequest{
			GroupID:      "cleanup-2",
			Speaker:      "test-agent",
			SystemPrompt: "sys",
			Instruction:  "go",
		})
		done <- result{err}
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("provider never entered")
	}

	if n, exists := semLen(t, lr.al, key); !exists {
		t.Errorf("sessionProcessing missing %q during the turn", key)
	} else if n != 1 {
		t.Errorf("semaphore %q holds %d tokens, want 1", key, n)
	}
	if !lr.al.isSessionProcessing(key) {
		t.Errorf("isSessionProcessing(%q) = false during the turn, want true", key)
	}

	close(release)
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("runGroupTurn: %v", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runGroupTurn never returned")
	}

	deadline := time.Now().Add(time.Second)
	for {
		if _, exists := semLen(t, lr.al, key); !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("sessionProcessing still holds %q after the turn — semaphore leak (B6)", key)
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRegression_GroupTurnKeepsForeignSemaphore(t *testing.T) {
	lr, _, cleanup := createGroupTurnTestHarness(t, &providers.LLMResponse{Content: "unused"})
	defer cleanup()

	const key = "group:cleanup-3:test-agent"
	sem, _ := lr.al.sessionProcessing.LoadOrStore(key, make(chan struct{}, 1))
	foreign := sem.(chan struct{})
	foreign <- struct{}{}
	defer func() { <-foreign }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := lr.runGroupTurn(ctx, group.TurnRequest{
		GroupID:      "cleanup-3",
		Speaker:      "test-agent",
		SystemPrompt: "sys",
		Instruction:  "go",
	}); err == nil {
		t.Fatal("expected context error while the semaphore was busy")
	}

	if n, exists := semLen(t, lr.al, key); !exists {
		t.Error("failed turn deleted a semaphore owned by another holder")
	} else if n != 1 {
		t.Errorf("foreign semaphore tokens = %d, want 1", n)
	}
}

func TestRegression_GroupTurnReleasesKeyOnLLMError(t *testing.T) {
	lr, _, cleanup := createGroupTurnTestHarness(t, &providers.LLMResponse{Content: "unused"})
	defer cleanup()

	lr.retryWait = instantRetryWait

	lr.al.registry.mu.Lock()
	lr.al.registry.agents["test-agent"].Provider = &llmRunnerMockLLMProvider{
		err: context.DeadlineExceeded,
	}
	lr.al.registry.mu.Unlock()

	const key = "group:cleanup-4:test-agent"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := lr.runGroupTurn(ctx, group.TurnRequest{
		GroupID:      "cleanup-4",
		Speaker:      "test-agent",
		SystemPrompt: "sys",
		Instruction:  "go",
	}); err == nil {
		t.Fatal("expected LLM error, got nil")
	}

	if _, exists := semLen(t, lr.al, key); exists {
		t.Errorf("sessionProcessing still holds %q after a failed turn — semaphore leak (B6)", key)
	}
}
