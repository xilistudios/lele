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

func TestGoalContinuation_NoSemaphoreDeadlock(t *testing.T) {
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

	gm.Set("test-session", "Fix all lint errors", 10)

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

	runner.maybeRunGoalContinuation(agent, processOptions{
		SessionKey: "test-session",
		Channel:    "test-channel",
		ChatID:     "test-chat-id",
	}, "Working on the goal...")

	goal := gm.Get("test-session")
	if goal == nil || goal.Status != GoalDone {
		t.Fatalf("expected goal to be DONE after continuation, got: %+v", goal)
	}

	if raw, ok := al.sessionProcessing.Load("test-session"); ok {
		sem := raw.(chan struct{})
		if len(sem) != 0 {
			t.Errorf("session semaphore not released after continuation loop (len=%d)", len(sem))
		}
	}
}

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

	sm2 := session.NewSessionManager()
	if got := lastAssistantResponse(&AgentInstance{Sessions: sm2}, "other"); got != "" {
		t.Errorf("lastAssistantResponse = %q, want empty", got)
	}
}

type guidanceGoalJudge struct {
	calls int32
}

func (j *guidanceGoalJudge) JudgeGoal(_ context.Context, _, _, _ string) (bool, string, error) {
	if atomic.AddInt32(&j.calls, 1) == 1 {
		return false, "CONTINUE: fix the auth bug", nil
	}
	return true, "DONE", nil
}

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

type signalingGoalJudge struct {
	evalStarted chan struct{}
	release     chan struct{}
	observed    chan bool
	al          *AgentLoop
	sessionKey  string
}

func (j *signalingGoalJudge) JudgeGoal(_ context.Context, _, _, _ string) (bool, string, error) {
	close(j.evalStarted)
	j.observed <- j.al.isGoalLoopActive(j.sessionKey)
	<-j.release
	return true, "DONE", nil
}

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

	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		runner.runGoalContinuation(context.Background(), agent, processOptions{
			SessionKey: "test-session",
			Channel:    "test-channel",
			ChatID:     "test-chat-id",
		}, "Working on the goal...")
	}()

	select {
	case <-judge.evalStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("judge evaluation never started")
	}

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

	close(judge.release)
	select {
	case <-loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("goal continuation loop did not finish after judge DONE")
	}

	if al.isGoalLoopActive("test-session") {
		t.Error("session still marked goal-loop-active after loop finished")
	}

	goal := gm.Get("test-session")
	if goal == nil || goal.Status != GoalDone {
		t.Fatalf("expected goal DONE, got: %+v", goal)
	}
}

func TestLastAssistantResponse_SkipsBlanks(t *testing.T) {
	sm := session.NewSessionManager()
	agent := &AgentInstance{Sessions: sm}

	sm.AddMessage("skey", "user", "hello")
	sm.AddMessage("skey", "assistant", "real reply")
	sm.AddMessage("skey", "user", "again")
	sm.AddFullMessage("skey", providers.Message{Role: "assistant", Content: "", ReasoningContent: "thinking..."})
	sm.AddFullMessage("skey", providers.Message{Role: "assistant", Content: "  "})

	if got := lastAssistantResponse(agent, "skey"); got != "real reply" {
		t.Errorf("lastAssistantResponse = %q, want %q", got, "real reply")
	}
}
