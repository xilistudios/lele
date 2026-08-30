// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

// ---------------------------------------------------------------------------
// Regression tests for B6 (agent half): the per-(group, speaker) processing
// semaphore used to leak.
//
// runGroupTurn acquires the semaphore with key "group:<groupID>:<speaker>" via
// sessionProcessing.LoadOrStore but previously never removed the entry, so the
// loop's sync.Map grew one 1-slot channel per group+speaker pair for the whole
// lifetime of the process. The rule under test:
//
//   - while a group turn runs, the key is present and its channel is held;
//   - once the turn returns, the key is gone (idle channels are deleted);
//   - a turn that never acquired the semaphore (context cancelled while the
//     key was busy) must NOT delete a channel another holder owns.
// ---------------------------------------------------------------------------

// semLen reports the number of tokens currently in the semaphore channel stored
// under key, plus whether the key exists at all. Reading len() on a channel is
// non-blocking and safe.
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

// TestRegression_GroupTurnReleasesSessionKey is the core B6 assertion: a
// completed group turn leaves nothing behind in sessionProcessing.
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

// TestRegression_GroupTurnHoldsSessionKeyWhileRunning proves the cleanup does
// not delete the entry too early: with the provider blocked, the key must be
// present and held, so isSessionProcessing keeps reporting the group speaker as
// busy for the whole turn.
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

	// The turn is parked inside the LLM call: key present and held.
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

	// Give the deferred cleanup a moment (it runs before the return reaches us,
	// but keep the assertion robust against scheduling noise).
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

// TestRegression_GroupTurnKeepsForeignSemaphore covers the race guard: when
// another holder already owns the semaphore for the key, a turn that fails to
// acquire it (context cancelled) must leave the entry and its token untouched.
func TestRegression_GroupTurnKeepsForeignSemaphore(t *testing.T) {
	lr, _, cleanup := createGroupTurnTestHarness(t, &providers.LLMResponse{Content: "unused"})
	defer cleanup()

	const key = "group:cleanup-3:test-agent"
	sem, _ := lr.al.sessionProcessing.LoadOrStore(key, make(chan struct{}, 1))
	foreign := sem.(chan struct{})
	foreign <- struct{}{} // someone else holds it
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

// TestRegression_GroupTurnReleasesKeyOnLLMError checks the cleanup is not
// limited to the happy path: an error from the provider still unwinds through
// the defer and frees both the token and the map entry.
func TestRegression_GroupTurnReleasesKeyOnLLMError(t *testing.T) {
	lr, _, cleanup := createGroupTurnTestHarness(t, &providers.LLMResponse{Content: "unused"})
	defer cleanup()

	// The mock below returns a "network timeout" error, which executeWithRetry
	// retries with a backoff; make those waits instant so the test is fast.
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
