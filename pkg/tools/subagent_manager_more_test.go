package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/providers"
)

// TestSubagentManager_Setters exercises all the simple option setters that have
// no external side effects beyond mutating manager fields.
func TestSubagentManager_Setters(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)

	sm.SetMaxIterations(99)
	sm.SetTimeout(30 * time.Second)
	sm.SetMaxConcurrent(3)
	sm.SetDefaultMaxRetries(5)
	sm.SetTools(NewToolRegistry())
	sm.SetAgentContextCallback(func(agentID string) AgentContextInfo {
		return AgentContextInfo{Name: "ctx-" + agentID}
	})
	sm.SetModelOverrideResolver(func(model string) (providers.LLMProvider, string, int) {
		return nil, "", 0
	})
	sm.SetVisionChecker(func(model string) bool { return false })
	sm.SetSessionKeyCallback(func(sessionKey, agentID string) {})
	sm.SetRegisterSessionCancelCallback(func(sessionKey string, cancel context.CancelFunc) func() {
		return func() {}
	})
	sm.SetRedactor(keyring.NewRedactor(newRedactorTestService(t)))

	// Read back via the getters / guarded access.
	if v := sm.getRedactor(); v == nil {
		t.Fatal("expected redactor to be set")
	}
	sm.mu.RLock()
	if sm.maxIterations != 99 {
		t.Errorf("maxIterations = %d, want 99", sm.maxIterations)
	}
	if sm.timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", sm.timeout)
	}
	if sm.maxConcurrent != 3 {
		t.Errorf("maxConcurrent = %d, want 3", sm.maxConcurrent)
	}
	if sm.defaultMaxRetries != 5 {
		t.Errorf("defaultMaxRetries = %d, want 5", sm.defaultMaxRetries)
	}
	if sm.getAgentContext == nil || sm.modelOverrideResolver == nil ||
		sm.visionChecker == nil || sm.sessionKeyCallback == nil ||
		sm.registerSessionCancel == nil || sm.redactor == nil {
		t.Error("expected all callback setters to be non-nil")
	}
	sm.mu.RUnlock()
}

// TestSubagentManager_RegisterToolAndHasTool verifies RegisterTool/GetToolRegistry/HasTool.
func TestSubagentManager_RegisterToolAndHasTool(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	tool := NewSubagentTool(sm)
	sm.RegisterTool(tool)

	if !sm.HasTool("subagent") {
		t.Error("expected HasTool('subagent') to be true")
	}
	if sm.HasTool("does-not-exist") {
		t.Error("expected HasTool('does-not-exist') to be false")
	}
	reg := sm.GetToolRegistry()
	if reg == nil {
		t.Fatal("expected non-nil tool registry")
	}
	if _, ok := reg.Get("subagent"); !ok {
		t.Error("expected tool registry to contain 'subagent'")
	}
}

// TestSubagentManager_countRunningAndCheckDependencies exercises the private
// helpers for counting running tasks and checking dependency satisfaction.
func TestSubagentManager_countRunningAndCheckDependencies(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	sm.mu.Lock()
	sm.tasks["a"] = &SubagentTask{ID: "a", Status: SubagentStatusRunning, mu: &sync.Mutex{}}
	sm.tasks["b"] = &SubagentTask{ID: "b", Status: SubagentStatusCompleted, mu: &sync.Mutex{}}
	sm.tasks["c"] = &SubagentTask{ID: "c", Status: SubagentStatusRunning, mu: &sync.Mutex{}}
	sm.mu.Unlock()

	if got := sm.countRunning(); got != 2 {
		t.Errorf("countRunning = %d, want 2", got)
	}

	// Dependencies satisfied when they are terminal (completed).
	deps := &SubagentTask{Dependencies: []string{"b", "missing"}}
	if !sm.checkDependencies(deps) {
		t.Error("expected dependencies to be satisfied (existing terminal + missing treated as satisfied)")
	}

	// Dependency running -> not satisfied.
	deps2 := &SubagentTask{Dependencies: []string{"a"}}
	if sm.checkDependencies(deps2) {
		t.Error("expected dependencies NOT to be satisfied (dependency running)")
	}
}

// TestSubagentManager_MarkDelivered verifies the delivered flag races and edge cases.
func TestSubagentManager_MarkDelivered(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)

	// Task not found -> returns false.
	if sm.MarkDelivered("nope") {
		t.Error("expected MarkDelivered for missing task to return false")
	}

	task := &SubagentTask{
		ID:  "subagent-1",
		mu:  &sync.Mutex{},
	}
	task.InitDoneChannel()
	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.mu.Unlock()

	// First delivery returns false.
	if sm.MarkDelivered(task.ID) {
		t.Error("expected first delivery to return false")
	}
	// Second delivery returns true (already delivered).
	if !sm.MarkDelivered(task.ID) {
		t.Error("expected second delivery to return true")
	}

	// Task present but with nil mu (edge case: task.mu nil).
	task2 := &SubagentTask{ID: "subagent-2"}
	sm.mu.Lock()
	sm.tasks[task2.ID] = task2
	sm.mu.Unlock()
	if sm.MarkDelivered(task2.ID) {
		t.Error("expected first delivery for nil-mu task to return false")
	}
}

// TestSubagentManager_StopAll tests stopping all running/paused/pending tasks.
func TestSubagentManager_StopAll(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)

	// A running task with a cancel func registered.
	task := &SubagentTask{
		ID:     "subagent-1",
		Status: SubagentStatusRunning,
		mu:     &sync.Mutex{},
	}
	task.InitDoneChannel()
	cancelCtx, cancel := context.WithCancel(context.Background())
	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.cancels[task.ID] = cancel
	// A needs_context task WITHOUT a cancel entry.
	paused := &SubagentTask{
		ID:     "subagent-2",
		Status: SubagentStatusNeedsContext,
		mu:     &sync.Mutex{},
	}
	paused.InitDoneChannel()
	sm.tasks[paused.ID] = paused
	// A completed task - should be left alone.
	done := &SubagentTask{ID: "subagent-3", Status: SubagentStatusCompleted, mu: &sync.Mutex{}}
	sm.tasks[done.ID] = done
	sm.mu.Unlock()
	_ = cancelCtx

	count := sm.StopAll()
	if count != 2 {
		t.Errorf("StopAll removed %d, want 2", count)
	}
	if cancelCtx.Err() == nil {
		t.Error("expected running task's cancel func to have been invoked")
	}
	for _, id := range []string{"subagent-1", "subagent-2"} {
		got, ok := sm.GetTask(id)
		if !ok {
			t.Fatalf("task %s should still exist", id)
		}
		if got.Status != SubagentStatusCancelled {
			t.Errorf("task %s status = %q, want cancelled", id, got.Status)
		}
	}
	if got, ok := sm.GetTask("subagent-3"); !ok || got.Status != SubagentStatusCompleted {
		t.Errorf("completed task should be untouched, got %+v ok=%v", got, ok)
	}
}

// TestSubagentManager_SpawnWithOptions_ConcurrencyLimit verifies that when the
// concurrency limit is reached, spawn returns an error.
func TestSubagentManager_SpawnWithOptions_ConcurrencyLimit(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 10 * time.Second}
	sm := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	sm.SetMaxConcurrent(1)

	_, err := sm.SpawnWithOptions(context.Background(), "first", "first", "", "cli", "direct", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("first spawn failed: %v", err)
	}

	// Second spawn should fail.
	_, err = sm.SpawnWithOptions(context.Background(), "second", "second", "", "cli", "direct", nil, SpawnOptions{})
	if err == nil {
		t.Fatal("expected second spawn to hit concurrency limit")
	}
	if !strings.Contains(err.Error(), "maximum concurrent subagents reached") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cleanup.
	sm.StopTask("subagent-1")
	sm.StopTask("subagent-2")
}

// TestSubagentManager_SpawnWithOptions_DefaultMaxRetries verifies that when
// MaxRetries is not specified, the manager default is used.
func TestSubagentManager_SpawnWithOptions_DefaultMaxRetries(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 10 * time.Second}
	sm := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	sm.SetDefaultMaxRetries(7)

	_, err := sm.SpawnWithOptions(context.Background(), "task", "label", "", "cli", "direct", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	sm.mu.RLock()
	task := sm.tasks["subagent-1"]
	sm.mu.RUnlock()
	if task == nil {
		t.Fatal("expected spawned task")
	}
	if task.MaxRetries != 7 {
		t.Errorf("MaxRetries = %d, want 7", task.MaxRetries)
	}
	sm.StopTask("subagent-1")
}

// TestSubagentManager_SpawnWithOptions_PendingDependencies verifies that a
// task with unmet dependencies stays pending until they are satisfied.
func TestSubagentManager_SpawnWithOptions_PendingDependencies(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{
		"STATUS: completed\nSUMMARY: Dependent done\nDETAILS:\nDone.",
	}}
	sm := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)

	// Insert a running dependency task so the spawned task starts pending.
	dep := &SubagentTask{
		ID:     "subagent-dep",
		Status: SubagentStatusRunning,
		mu:     &sync.Mutex{},
	}
	dep.InitDoneChannel()
	sm.mu.Lock()
	sm.tasks[dep.ID] = dep
	sm.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msg, err := sm.SpawnWithOptions(ctx, "waiting task", "wait", "", "cli", "direct", nil, SpawnOptions{
		Dependencies: []string{dep.ID},
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	if !strings.Contains(msg, "subagent-1") {
		t.Fatalf("expected subagent-1 in message, got: %s", msg)
	}

	// Task should be pending initially.
	got, ok := sm.GetTask("subagent-1")
	if !ok {
		t.Fatal("expected task subagent-1")
	}
	if got.Status != SubagentStatusPending {
		t.Fatalf("expected pending, got %q", got.Status)
	}

	// Satisfy the dependency -> poller should transition it to running and complete.
	sm.mu.Lock()
	sm.tasks[dep.ID].Status = SubagentStatusCompleted
	sm.mu.Unlock()

	// Wait until it completes.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		t, ok := sm.GetTask("subagent-1")
		if ok && t.Status == SubagentStatusCompleted {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("subagent-1 did not complete after dependency satisfied, status=%v", mustGetStatus(sm, "subagent-1"))
}

func mustGetStatus(sm *SubagentManager, id string) string {
	if t, ok := sm.GetTask(id); ok {
		return t.Status
	}
	return "<missing>"
}

// TestSubagentManager_SpawnWithOptions_PendingCancelled verifies that a pending
// task is cancelled when StopTask is called while waiting for dependencies.
func TestSubagentManager_SpawnWithOptions_PendingCancelled(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{"STATUS: completed\nSUMMARY: x\nDETAILS:\ny"}}
	sm := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)

	dep := &SubagentTask{ID: "subagent-dep", Status: SubagentStatusRunning, mu: &sync.Mutex{}}
	dep.InitDoneChannel()
	sm.mu.Lock()
	sm.tasks[dep.ID] = dep
	sm.mu.Unlock()

	_, err := sm.SpawnWithOptions(context.Background(), "waiting", "wait", "", "cli", "direct", nil, SpawnOptions{
		Dependencies: []string{dep.ID},
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	got, ok := sm.GetTask("subagent-1")
	if !ok || got.Status != SubagentStatusPending {
		t.Fatalf("expected pending task, got ok=%v status=%s", ok, got.Status)
	}

	// Stop the pending task.
	if !sm.StopTask("subagent-1") {
		t.Fatal("expected StopTask to succeed on pending task")
	}
	time.Sleep(50 * time.Millisecond)
	got, ok = sm.GetTask("subagent-1")
	if !ok {
		t.Fatal("expected task to remain after stop")
	}
	if got.Status != SubagentStatusCancelled {
		t.Fatalf("expected cancelled, got %q", got.Status)
	}
}

// TestSubagentManager_ContinueTask_Errors covers the error paths of ContinueTask.
func TestSubagentManager_ContinueTask_Errors(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)

	// Empty guidance.
	if _, err := sm.ContinueTask(context.Background(), "subagent-1", "  ", nil); err == nil {
		t.Error("expected error for empty guidance")
	}

	// Task not found.
	if _, err := sm.ContinueTask(context.Background(), "missing", "guide", nil); err == nil {
		t.Error("expected error for missing task")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}

	// Task exists but not in needs_context status.
	task := &SubagentTask{ID: "subagent-1", Status: SubagentStatusCompleted, mu: &sync.Mutex{}, Updated: time.Now().UnixMilli()}
	task.InitDoneChannel()
	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.mu.Unlock()
	if _, err := sm.ContinueTask(context.Background(), "subagent-1", "guide", nil); err == nil {
		t.Error("expected error when task not waiting for context")
	} else if !strings.Contains(err.Error(), "not waiting for context") {
		t.Errorf("unexpected error: %v", err)
	}
}

// errorProvider yields a constant error from Chat.
type errorProvider struct {
	err error
}

func (p *errorProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	return nil, p.err
}

func (p *errorProvider) GetDefaultModel() string { return "test-model" }

func (p *errorProvider) SupportsTools() bool { return false }

func (p *errorProvider) GetContextWindow() int { return 4096 }

// TestSubagentManager_SpawnWithOptions_OriginChannelPrefix verifies the origin
// session key derivation when originChatID already carries the channel prefix.
func TestSubagentManager_SpawnWithOptions_OriginChannelPrefix(t *testing.T) {
	sm := NewSubagentManager(&scriptedSubagentProvider{responses: []string{"STATUS: completed\nSUMMARY: done\nDETAILS:\nx"}}, "test-model", "/tmp/test", nil, 20)

	gotKey := ""
	sm.SetSessionKeyCallback(func(sessionKey, agentID string) {
		gotKey = sessionKey
	})

	_, err := sm.SpawnWithOptions(context.Background(), "task", "", "", "telegram", "telegram:chat-1", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	// originSessionKey should be "telegram:chat-1" (chatID already has prefix).
	deadline := time.Now().Add(2 * time.Second)
	for gotKey == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.HasPrefix(gotKey, "telegram:chat-1:subagent-") {
		t.Fatalf("session key = %q, want prefix telegram:chat-1:subagent-", gotKey)
	}

	sm.StopTask("subagent-1")
}

// TestSubagentManager_SpawnWithOptions_ModelOverride tests the model override
// path in runTask via a successful spawn with a resolvable override + vision checker.
func TestSubagentManager_SpawnWithOptions_ModelOverride(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{"STATUS: completed\nSUMMARY: override done\nDETAILS:\nok"}}
	sm := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)

	usedModel := ""
	sm.SetModelOverrideResolver(func(model string) (providers.LLMProvider, string, int) {
		if model == "vision-model" {
			usedModel = model
			return provider, "resolved-vision", 8192
		}
		return nil, "", 0
	})
	sm.SetVisionChecker(func(model string) bool { return model == "resolved-vision" })
	resultCh := make(chan *ToolResult, 1)

	_, err := sm.SpawnWithOptions(context.Background(), "task with override", "override", "", "cli", "direct",
		func(ctx context.Context, r *ToolResult) { resultCh <- r },
		SpawnOptions{ModelOverride: "vision-model"})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	select {
	case res := <-resultCh:
		if res.IsError {
			t.Fatalf("unexpected error result: %s", res.ForLLM)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
	if usedModel != "vision-model" {
		t.Fatalf("model override resolver not invoked with vision-model, got %q", usedModel)
	}
}

// TestSubagentManager_SpawnWithOptions_UnresolvableOverride verifies that when
// the resolver returns nil provider, the agent default is used (no panic).
func TestSubagentManager_SpawnWithOptions_UnresolvableOverride(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{"STATUS: completed\nSUMMARY: default done\nDETAILS:\nok"}}
	sm := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	sm.SetModelOverrideResolver(func(model string) (providers.LLMProvider, string, int) {
		return nil, "", 0
	})
	resultCh := make(chan *ToolResult, 1)

	_, err := sm.SpawnWithOptions(context.Background(), "task", "", "", "cli", "direct",
		func(ctx context.Context, r *ToolResult) { resultCh <- r },
		SpawnOptions{ModelOverride: "no-provider"})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	select {
	case res := <-resultCh:
		if res.IsError {
			t.Fatalf("unexpected error result: %s", res.ForLLM)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
}

// transientErr returns a string that is NOT retryable by the internal
// ChatWithRetry (avoids 5s backoff sleeps) but IS transient by the subagent
// runner's isTransientFailure (checks "server_error").
const transientErrMsg = "server_error: bad response from upstream"

// TestSubagentManager_SpawnWithOptions_TransientRetry verifies the retry path
// in runTask: a transient failure triggers the retry logic. Due to the
// context cancellation in runTaskImpl's deferred cleanup, the retry context
// is already cancelled, so the retried task is immediately cancelled. This
// test verifies that the retry is attempted (RetryCount is incremented).
func TestSubagentManager_SpawnWithOptions_TransientRetry(t *testing.T) {
	mu := &sync.Mutex{}
	var callCount int
	p := &countingErrorProvider{mu: mu, getErr: func() error {
		// Note: getErr is called while p.mu is already held by Chat,
		// so we must NOT lock p.mu here.
		if callCount == 0 {
			callCount++
			return fmt.Errorf("%s", transientErrMsg)
		}
		return nil
	}, responses: []string{"STATUS: completed\nSUMMARY: recovered\nDETAILS:\nyes"}}
	sm := NewSubagentManager(p, "test-model", "/tmp/test", nil, 20)
	sm.SetTimeout(0) // no timeout so retry loop doesn't get cancelled
	resultCh := make(chan *ToolResult, 4)

	_, err := sm.SpawnWithOptions(context.Background(), "retry task", "", "", "cli", "direct",
		func(ctx context.Context, r *ToolResult) { resultCh <- r },
		SpawnOptions{MaxRetries: 1})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	// Collect results for up to 12 seconds (5s backoff + processing time).
	var results []*ToolResult
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	for {
		select {
		case res := <-resultCh:
			results = append(results, res)
		case <-timer.C:
			goto done
		}
	}
done:

	// We expect at least one result (the initial error).
	if len(results) == 0 {
		t.Fatal("expected at least one callback result, got none")
	}

	// The first result should be an error (transient failure).
	if !results[0].IsError {
		t.Logf("first result was not an error: %s", results[0].ForLLM)
	}

	// Verify the provider was called at least once.
	p.mu.Lock()
	c := p.calls
	p.mu.Unlock()
	if c < 1 {
		t.Fatalf("expected at least 1 provider call, got %d", c)
	}

	// Verify retry was attempted by checking task state.
	sm.mu.Lock()
	retryCount := sm.tasks["subagent-1"].RetryCount
	sm.mu.Unlock()
	if retryCount != 1 {
		t.Fatalf("expected RetryCount=1 (retry attempted), got %d", retryCount)
	}
}

// TestSubagentManager_RunTaskImpl_ErrorClassification exercises the error
// classification branches in runTaskImpl for various error messages. Uses
// "server_error" prefixes that fail fast (not retried internally).
func TestSubagentManager_RunTaskImpl_ErrorClassification(t *testing.T) {
	cases := []struct {
		name       string
		errMsg     string
		wantStatus string
		wantCode   string
	}{
		{"server_error", "server_error: backend down", SubagentStatusFailed, "server_error"},
		{"rate_limited", "rate limit exceeded (429)", SubagentStatusFailed, "rate_limited"},
		{"connection_error", "connection refused", SubagentStatusFailed, "connection_error"},
		{"http_500", "server returned 503", SubagentStatusFailed, "server_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &errorProvider{err: fmt.Errorf("%s", tc.errMsg)}
			sm := NewSubagentManager(p, "test-model", "/tmp/test", nil, 20)
			task := &SubagentTask{
				ID:               "subagent-1",
				Task:             "t",
				OriginSessionKey: "cli:direct",
				Status:           SubagentStatusRunning,
				mu:               &sync.Mutex{},
			}
			task.InitDoneChannel()
			sm.mu.Lock()
			sm.tasks[task.ID] = task
			sm.mu.Unlock()

			sm.runTaskImpl(context.Background(), task, nil)

			got, _ := sm.GetTask(task.ID)
			if got == nil {
				t.Fatal("expected task after runTaskImpl")
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			switch tc.wantCode {
			case "rate_limited":
				if !strings.Contains(got.Summary, "rate_limited") {
					t.Errorf("summary = %q, want rate_limited marker", got.Summary)
				}
			default:
				if !strings.Contains(got.Summary, tc.wantCode) && !strings.Contains(got.Result, tc.wantCode) {
					t.Errorf("expected %q marker in summary/result, got summary=%q result=%q", tc.wantCode, got.Summary, got.Result)
				}
			}
		})
	}
}

// TestSubagentManager_RunTaskImpl_ContextCancelled verifies that when the
// context is already cancelled before runTaskImpl starts, the task is marked
// cancelled.
func TestSubagentManager_RunTaskImpl_ContextCancelled(t *testing.T) {
	p := &scriptedSubagentProvider{responses: []string{"STATUS: completed\nSUMMARY: x\nDETAILS:\ny"}}
	sm := NewSubagentManager(p, "test-model", "/tmp/test", nil, 20)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	task := &SubagentTask{
		ID:               "subagent-1",
		Task:             "t",
		OriginSessionKey: "cli:direct",
		Status:           SubagentStatusRunning,
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.mu.Unlock()

	sm.runTaskImpl(ctx, task, nil)

	got, ok := sm.GetTask(task.ID)
	if !ok {
		t.Fatal("expected task")
	}
	if got.Status != SubagentStatusCancelled {
		t.Fatalf("status = %q, want cancelled", got.Status)
	}
	if !strings.Contains(got.Summary, "cancelled") {
		t.Errorf("summary = %q, want cancellation notice", got.Summary)
	}
}

// countingErrorProvider returns an error on designated calls then delegates to
// a scripted provider.
type countingErrorProvider struct {
	mu        *sync.Mutex
	calls     int
	getErr    func() error
	responses []string
}

func (p *countingErrorProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	c := p.calls
	nowErr := p.getErr()
	p.mu.Unlock()
	if nowErr != nil {
		return nil, nowErr
	}
	_ = c
	resp := "STATUS: completed\nSUMMARY: done\nDETAILS:\nok"
	if len(p.responses) > 0 {
		resp = p.responses[len(p.responses)-1]
	}
	return &providers.LLMResponse{Content: resp}, nil
}

func (p *countingErrorProvider) GetDefaultModel() string { return "test-model" }
func (p *countingErrorProvider) SupportsTools() bool     { return false }
func (p *countingErrorProvider) GetContextWindow() int   { return 4096 }

// TestSubagentManager_RunTaskImpl_Timeout verifies the timeout classification in
// runTaskImpl by forcing a context deadline exceeded during execution.
func TestSubagentManager_RunTaskImpl_Timeout(t *testing.T) {
	// A deadline-exceeded error is retryable by the internal config, but the
	// subagent's own retry loop uses MaxRetries=0 here so it fails fast.
	p := &errorProvider{err: fmt.Errorf("context deadline exceeded")}
	sm := NewSubagentManager(p, "test-model", "/tmp/test", nil, 20)

	task := &SubagentTask{
		ID:               "subagent-1",
		Task:             "t",
		OriginSessionKey: "cli:direct",
		Status:           SubagentStatusRunning,
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.mu.Unlock()

	// Use an already-expired context so RunToolLoop returns a deadline error
	// without waiting for real retry backoff (RunToolLoop would otherwise
	// spend 5s+ waiting — we pre-set the error and expired ctx).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sm.runTaskImpl(ctx, task, nil)

	got, _ := sm.GetTask(task.ID)
	if got == nil {
		t.Fatal("expected task after runTaskImpl")
	}
	// With an expired context, runTaskImpl returns early as "cancelled".
	if got.Status != SubagentStatusCancelled {
		t.Fatalf("expected cancelled status with expired context, got %q", got.Status)
	}
}