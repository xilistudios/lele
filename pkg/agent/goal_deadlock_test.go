// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

// sequentialMockGoalJudge returns CONTINUE for the first N calls and DONE
// afterwards. It is used to drive multiple goal continuation turns.
type sequentialMockGoalJudge struct {
	continueCalls int32
	doneAfter     int32
}

func (j *sequentialMockGoalJudge) JudgeGoal(_ context.Context, _, _, _ string) (bool, string, error) {
	if atomic.AddInt32(&j.continueCalls, 1) <= j.doneAfter {
		return false, "CONTINUE", nil
	}
	return true, "DONE", nil
}

// TestGoalContinuation_NoSemaphoreDeadlock is a regression test for the
// critical semaphore deadlock: runGoalContinuation used to re-enter
// runAgentLoop while the per-session semaphore was still held, blocking
// forever on the 2nd continuation turn. The continuation trigger is now
// invoked by the caller AFTER runAgentLoop returns and releases the
// semaphore, so each continuation turn acquires and releases it
// independently.
func TestGoalContinuation_NoSemaphoreDeadlock(t *testing.T) {
	// Timeout guard: if the deadlock regresses, this test fails fast
	// instead of hanging the whole suite.
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("goal continuation deadlocked (triggered after 10s timeout guard)")
		}
	}()
	defer close(done)

	tmpDir, err := os.MkdirTemp("", "goal-deadlock-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				Provider:          "test-provider",
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	// Wire a goal manager with a mock judge that continues for 2 turns then
	// reports DONE. This forces 2+ recursive runAgentLoop continuation turns.
	gm := NewGoalManager(filepath.Join(tmpDir, "goals"))
	gm.SetJudge(&sequentialMockGoalJudge{doneAfter: 2})
	al.goalManager = gm
	al.goalStopCtx, al.goalStopCancel = context.WithCancel(context.Background())

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Working on the goal...",
			ToolCalls: []providers.ToolCall{},
		},
	}

	// Set an active goal with a generous budget.
	gm.Set("test-session", "Fix all lint errors", 10)

	// First turn: runAgentLoop returns and releases the semaphore. The goal
	// continuation is triggered by the caller afterwards.
	firstOpts := processOptions{
		SessionKey:      "test-session",
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "Start working on the goal",
		DefaultResponse: "Default response",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       false,
	}
	ctx := context.Background()
	if _, err := runner.runAgentLoop(ctx, agent, firstOpts); err != nil {
		t.Fatalf("first runAgentLoop error: %v", err)
	}

	// Trigger the continuation loop (as processMessage does after runAgentLoop
	// returns). This must NOT deadlock.
	runner.maybeRunGoalContinuation(agent, processOptions{
		SessionKey: "test-session",
		Channel:    "test-channel",
		ChatID:     "test-chat-id",
	}, "Working on the goal...")

	// After the loop, the goal must be marked done by the judge.
	goal := gm.Get("test-session")
	if goal == nil || goal.Status != GoalDone {
		t.Fatalf("expected goal to be DONE after continuation, got: %+v", goal)
	}

	// The semaphore must be released (capacity reads back as empty).
	if raw, ok := al.sessionProcessing.Load("test-session"); ok {
		sem := raw.(chan struct{})
		if len(sem) != 0 {
			t.Errorf("session semaphore not released after continuation loop (len=%d)", len(sem))
		}
	}
}

// TestGoalContinuation_BudgetExhaustionMarksBlocked verifies that when the
// continuation budget is exhausted without the judge reporting DONE, the goal
// is marked blocked (safety net).
func TestGoalContinuation_BudgetExhaustionMarksBlocked(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "goal-budget-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				Provider:          "test-provider",
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	gm := NewGoalManager(filepath.Join(tmpDir, "goals"))
	// Judge always continues -> budget will be exhausted.
	gm.SetJudge(&sequentialMockGoalJudge{doneAfter: 100000})
	al.goalManager = gm
	al.goalStopCtx, al.goalStopCancel = context.WithCancel(context.Background())

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Still working...",
			ToolCalls: []providers.ToolCall{},
		},
	}

	ctx := context.Background()

	// Set a goal with a very small budget (1 turn) so the loop exhausts quickly.
	gm.Set("test-session", "Impossible goal", 1)

	firstOpts := processOptions{
		SessionKey:  "test-session",
		Channel:     "test-channel",
		ChatID:      "test-chat-id",
		UserMessage: "Go",
		SendResponse: false,
	}
	if _, err := runner.runAgentLoop(ctx, agent, firstOpts); err != nil {
		t.Fatalf("first runAgentLoop error: %v", err)
	}
	runner.maybeRunGoalContinuation(agent, processOptions{
		SessionKey: "test-session",
		Channel:    "test-channel",
		ChatID:     "test-chat-id",
	}, "Still working...")

	goal := gm.Get("test-session")
	if goal == nil || goal.Status != GoalBlocked {
		t.Fatalf("expected goal to be BLOCKED after budget exhaustion, got: %+v", goal)
	}
}

// TestLastAssistantResponse verifies the session helper used by the caller-side
// goal trigger returns the most recent assistant content.
func TestLastAssistantResponse(t *testing.T) {
	tmpDir := t.TempDir()
	sm := session.NewSessionManager(tmpDir)
	agent := &AgentInstance{Sessions: sm}

	sm.AddMessage("skey", "user", "hello")
	sm.AddMessage("skey", "assistant", "first reply")
	sm.AddMessage("skey", "user", "again")
	sm.AddMessage("skey", "assistant", "second reply")

	if got := lastAssistantResponse(agent, "skey"); got != "second reply" {
		t.Errorf("lastAssistantResponse = %q, want %q", got, "second reply")
	}

	// No assistant messages -> empty.
	sm2 := session.NewSessionManager(tmpDir)
	if got := lastAssistantResponse(&AgentInstance{Sessions: sm2}, "other"); got != "" {
		t.Errorf("lastAssistantResponse = %q, want empty", got)
	}
}