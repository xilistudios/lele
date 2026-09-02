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
	"strings"
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
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		UserMessage:  "Go",
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
	sm := session.NewSessionManager()
	agent := &AgentInstance{Sessions: sm}

	sm.AddMessage("skey", "user", "hello")
	sm.AddMessage("skey", "assistant", "first reply")
	sm.AddMessage("skey", "user", "again")
	sm.AddMessage("skey", "assistant", "second reply")

	if got := lastAssistantResponse(agent, "skey"); got != "second reply" {
		t.Errorf("lastAssistantResponse = %q, want %q", got, "second reply")
	}

	// No assistant messages -> empty.
	sm2 := session.NewSessionManager()
	if got := lastAssistantResponse(&AgentInstance{Sessions: sm2}, "other"); got != "" {
		t.Errorf("lastAssistantResponse = %q, want empty", got)
	}
}

// guidanceGoalJudge returns CONTINUE with a specific next step on the first
// call and DONE on the second, so tests can verify that runGoalContinuation
// uses the judge's guidance as the next turn's prompt.
type guidanceGoalJudge struct {
	calls int32
}

func (j *guidanceGoalJudge) JudgeGoal(_ context.Context, _, _, _ string) (bool, string, error) {
	if atomic.AddInt32(&j.calls, 1) == 1 {
		return false, "CONTINUE: fix the auth bug", nil
	}
	return true, "DONE", nil
}

// recordingContinuationProvider records every user message it receives so the
// test can assert that the continuation prompt (injected as the user message)
// contains the judge's specific guidance.
type recordingContinuationProvider struct {
	responses    []*providers.LLMResponse
	userMessages []string
}

func (m *recordingContinuationProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	for _, msg := range messages {
		if msg.Role == "user" {
			m.userMessages = append(m.userMessages, msg.Content)
		}
	}
	if len(m.responses) > 0 {
		resp := m.responses[0]
		m.responses = m.responses[1:]
		return resp, nil
	}
	return &providers.LLMResponse{
		Content:   "Mock response",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *recordingContinuationProvider) GetDefaultModel() string {
	return "mock-model"
}

// TestGoalContinuation_UsesJudgeGuidance verifies that when the subagent judge
// returns CONTINUE with a specific next step, the continuation loop injects
// that guidance as the next turn's prompt (instead of the generic one).
func TestGoalContinuation_UsesJudgeGuidance(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "goal-guidance-test-*")
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
	gm.SetJudge(&guidanceGoalJudge{})
	al.goalManager = gm
	al.goalStopCtx, al.goalStopCancel = context.WithCancel(context.Background())

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	provider := &recordingContinuationProvider{
		responses: []*providers.LLMResponse{
			{Content: "Working on the goal...", ToolCalls: []providers.ToolCall{}},
			{Content: "Finished the goal.", ToolCalls: []providers.ToolCall{}},
		},
	}
	agent.Provider = provider

	gm.Set("test-session", "Fix all lint errors", 10)

	ctx := context.Background()
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
	if _, err := runner.runAgentLoop(ctx, agent, firstOpts); err != nil {
		t.Fatalf("first runAgentLoop error: %v", err)
	}

	runner.maybeRunGoalContinuation(agent, processOptions{
		SessionKey: "test-session",
		Channel:    "test-channel",
		ChatID:     "test-chat-id",
	}, "Working on the goal...")

	goal := gm.Get("test-session")
	if goal == nil || goal.Status != GoalDone {
		t.Fatalf("expected goal to be DONE after continuation, got: %+v", goal)
	}

	// The continuation turn's user message must contain the judge's guidance.
	found := false
	for _, um := range provider.userMessages {
		if strings.Contains(um, "fix the auth bug") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("continuation prompt did not contain judge guidance 'fix the auth bug'; user messages: %v", provider.userMessages)
	}
}

// signalingGoalJudge blocks on JudgeGoal until released, reporting the// observed goal-loop state via a channel so the test can assert that the
// session is marked as goal-loop-active while the judge is evaluating.
type signalingGoalJudge struct {
	evalStarted chan struct{} // closed when JudgeGoal is entered
	release     chan struct{} // JudgeGoal blocks until this is closed
	observed    chan bool     // receives al.isGoalLoopActive result mid-eval
	al          *AgentLoop
	sessionKey  string
}

func (j *signalingGoalJudge) JudgeGoal(_ context.Context, _, _, _ string) (bool, string, error) {
	close(j.evalStarted)
	// Report whether the session is tracked as goal-loop-active while we are
	// inside the evaluation gap (semaphore released between turns).
	j.observed <- j.al.isGoalLoopActive(j.sessionKey)
	<-j.release
	return true, "DONE", nil
}

// TestGoalContinuation_SessionMarkedActiveDuringJudgeEval is a regression
// test for the TUI loading indicator dropping out during the goal loop. The
// per-session semaphore is released between continuation turns, so
// IsSessionProcessing would return false during the judge-evaluation gap.
// runGoalContinuation must mark the session as goal-loop-active for the
// whole loop so the TUI loading state stays on, and clear it when done.
func TestGoalContinuation_SessionMarkedActiveDuringJudgeEval(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "goal-loop-active-test-*")
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
	judge := &signalingGoalJudge{
		evalStarted: make(chan struct{}),
		release:     make(chan struct{}),
		observed:    make(chan bool, 1),
		al:          al,
		sessionKey:  "test-session",
	}
	gm.SetJudge(judge)
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

	gm.Set("test-session", "Fix all lint errors", 10)

	// Run the continuation loop in a goroutine; it will block inside the
	// judge's JudgeGoal call.
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		runner.runGoalContinuation(context.Background(), agent, processOptions{
			SessionKey: "test-session",
			Channel:    "test-channel",
			ChatID:     "test-chat-id",
		}, "Working on the goal...")
	}()

	// Wait for the judge evaluation to start.
	select {
	case <-judge.evalStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("judge evaluation never started")
	}

	// While inside the judge gap, the session must be marked active.
	select {
	case active := <-judge.observed:
		if !active {
			t.Error("session not marked goal-loop-active during judge evaluation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for judge observation")
	}
	if !al.isGoalLoopActive("test-session") {
		t.Error("isGoalLoopActive = false during judge evaluation, want true")
	}

	// Release the judge (returns DONE) and wait for the loop to finish.
	close(judge.release)
	select {
	case <-loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("goal continuation loop did not finish after judge DONE")
	}

	// After the loop, the session must no longer be marked active.
	if al.isGoalLoopActive("test-session") {
		t.Error("session still marked goal-loop-active after loop finished")
	}

	// Goal must be marked done.
	goal := gm.Get("test-session")
	if goal == nil || goal.Status != GoalDone {
		t.Fatalf("expected goal DONE, got: %+v", goal)
	}
}

// TestLastAssistantResponse_SkipsBlanks verifies that legacy blank assistant
// messages (persisted by the old empty-response bug) do not mask the last
// real assistant reply from the goal judge.
func TestLastAssistantResponse_SkipsBlanks(t *testing.T) {
	sm := session.NewSessionManager()
	agent := &AgentInstance{Sessions: sm}

	sm.AddMessage("skey", "user", "hello")
	sm.AddMessage("skey", "assistant", "real reply")
	sm.AddMessage("skey", "user", "again")
	// Legacy blanks: no content, no tool calls.
	sm.AddFullMessage("skey", providers.Message{Role: "assistant", Content: "", ReasoningContent: "thinking..."})
	sm.AddFullMessage("skey", providers.Message{Role: "assistant", Content: "  "})

	if got := lastAssistantResponse(agent, "skey"); got != "real reply" {
		t.Errorf("lastAssistantResponse = %q, want %q", got, "real reply")
	}
}
