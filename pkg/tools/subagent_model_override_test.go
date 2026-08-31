package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// recordingProvider captures the model name each Chat call was made with so
// tests can assert which model the tool loop actually ran on.
type recordingProvider struct {
	calls int
	model string
}

func (r *recordingProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	r.calls++
	r.model = model
	return &providers.LLMResponse{Content: "STATUS: completed\nSUMMARY: Done\nDETAILS:\nCompleted"}, nil
}

func (r *recordingProvider) GetDefaultModel() string { return "default-model" }
func (r *recordingProvider) SupportsTools() bool     { return false }
func (r *recordingProvider) GetContextWindow() int   { return 4096 }

// waitForSubagentDone blocks until the task signals completion (terminal
// state reached and the runner goroutine finished) or the test times out.
func waitForSubagentDone(t *testing.T, task *SubagentTask) {
	t.Helper()
	select {
	case <-task.DoneChannel():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for subagent task to finish")
	}
}

// spawnAndWait spawns a task with the given options and waits for completion:
// first the callback (the runner delivers it before the task goroutine signals
// done), then the task's DoneChannel, and finally a consistent snapshot of the
// terminal state. It returns the task snapshot, the first callback result (nil
// if the callback never fired), and the callback channel so tests can assert
// that no second callback was delivered.
func spawnAndWait(t *testing.T, sm *SubagentManager, opts SpawnOptions) (*SubagentTask, *ToolResult, chan *ToolResult) {
	t.Helper()

	results := make(chan *ToolResult, 4)
	msg, err := sm.SpawnWithOptions(context.Background(), "do a thing", "override-test", "", "cli", "direct",
		func(ctx context.Context, result *ToolResult) { results <- result },
		opts)
	if err != nil {
		t.Fatalf("SpawnWithOptions failed: %v", err)
	}

	taskID := "subagent-1"
	if idx := strings.Index(msg, "subagent-"); idx >= 0 {
		rest := msg[idx:]
		if end := strings.IndexAny(rest, " '"); end > 0 {
			taskID = rest[:end]
		} else {
			taskID = rest
		}
	}

	// Wait for the completion signal (closed by the task goroutine after
	// runTask returns, which is after any callback delivery). GetTask returns
	// a snapshot that shares the live task's doneCh, so it can be waited on;
	// re-fetch afterwards to read the terminal state consistently.
	task, ok := sm.GetTask(taskID)
	if !ok {
		t.Fatalf("task %s not found after spawn", taskID)
	}
	waitForSubagentDone(t, task)

	var cbResult *ToolResult
	select {
	case cbResult = <-results:
	case <-time.After(2 * time.Second):
		// Callback may legitimately be absent (nil callback scenarios); tests
		// that require it assert on cbResult == nil.
	}
	fresh, ok := sm.GetTask(taskID)
	if !ok {
		t.Fatalf("task %s disappeared after completion", taskID)
	}
	return fresh, cbResult, results
}

// TestRunTask_ModelOverrideResolverError_FailsTask proves the fail-loudly
// contract: when a task carries a model override and the resolver reports an
// error, the task must end FAILED with the override name surfaced in
// Summary/Result and the callback must receive an IsError result — the agent
// default model must NOT be used.
func TestRunTask_ModelOverrideResolverError_FailsTask(t *testing.T) {
	defaultProvider := &recordingProvider{}
	sm := NewSubagentManager(defaultProvider, "default-model", "/tmp/test", nil, 10)
	sm.SetModelOverrideResolver(func(model string) (providers.LLMProvider, string, int, error) {
		return nil, "", 0, errors.New("no API key configured for provider")
	})

	task, cbResult, cbResults := spawnAndWait(t, sm, SpawnOptions{ModelOverride: "anthropic:claude-opus"})

	if task.Status != SubagentStatusFailed {
		t.Fatalf("task status = %q, want %q", task.Status, SubagentStatusFailed)
	}
	if !strings.Contains(task.Summary, "anthropic:claude-opus") {
		t.Errorf("Summary = %q, want it to contain the override model name", task.Summary)
	}
	if !strings.Contains(task.Result, "anthropic:claude-opus") {
		t.Errorf("Result = %q, want it to contain the override model name", task.Result)
	}
	if !strings.Contains(task.Result, "no API key configured for provider") {
		t.Errorf("Result = %q, want it to include the resolver error text", task.Result)
	}
	if defaultProvider.calls != 0 {
		t.Errorf("default provider was called %d times, want 0 (task must fail before the tool loop)", defaultProvider.calls)
	}

	if cbResult == nil {
		t.Fatal("callback was not invoked")
	}
	if !cbResult.IsError {
		t.Errorf("callback result IsError = false, want true")
	}
	if !strings.Contains(cbResult.ForLLM, "anthropic:claude-opus") {
		t.Errorf("callback ForLLM = %q, want it to contain the override model name", cbResult.ForLLM)
	}
	// The callback must fire exactly once: the failure return must not double
	// deliver (e.g. once inline and once via the deferred block).
	select {
	case extra := <-cbResults:
		t.Fatalf("callback fired a second time: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestRunTask_ModelOverrideResolverNilProvider_FailsTask covers resolvers that
// return a nil provider without an error: the task must still fail instead of
// falling back to the default model.
func TestRunTask_ModelOverrideResolverNilProvider_FailsTask(t *testing.T) {
	defaultProvider := &recordingProvider{}
	sm := NewSubagentManager(defaultProvider, "default-model", "/tmp/test", nil, 10)
	sm.SetModelOverrideResolver(func(model string) (providers.LLMProvider, string, int, error) {
		return nil, "", 0, nil
	})

	task, cbResult, _ := spawnAndWait(t, sm, SpawnOptions{ModelOverride: "openai:gpt-5"})

	if task.Status != SubagentStatusFailed {
		t.Fatalf("task status = %q, want %q", task.Status, SubagentStatusFailed)
	}
	if !strings.Contains(task.Result, "openai:gpt-5") {
		t.Errorf("Result = %q, want it to contain the override model name", task.Result)
	}
	if defaultProvider.calls != 0 {
		t.Errorf("default provider was called %d times, want 0", defaultProvider.calls)
	}
	if cbResult == nil || !cbResult.IsError {
		t.Fatalf("callback result = %+v, want non-nil IsError result", cbResult)
	}
}

// TestRunTask_ModelOverrideNoResolver_FailsTask covers the case where a task
// carries a model override but no resolver was registered at all: the task
// must fail with a "no model resolver configured" reason.
func TestRunTask_ModelOverrideNoResolver_FailsTask(t *testing.T) {
	defaultProvider := &recordingProvider{}
	sm := NewSubagentManager(defaultProvider, "default-model", "/tmp/test", nil, 10)
	// Intentionally do NOT call SetModelOverrideResolver.

	task, cbResult, _ := spawnAndWait(t, sm, SpawnOptions{ModelOverride: "anthropic:claude-opus"})

	if task.Status != SubagentStatusFailed {
		t.Fatalf("task status = %q, want %q", task.Status, SubagentStatusFailed)
	}
	if !strings.Contains(task.Result, "no model resolver configured") {
		t.Errorf("Result = %q, want it to mention the missing resolver", task.Result)
	}
	if !strings.Contains(task.Result, "anthropic:claude-opus") {
		t.Errorf("Result = %q, want it to contain the override model name", task.Result)
	}
	if defaultProvider.calls != 0 {
		t.Errorf("default provider was called %d times, want 0", defaultProvider.calls)
	}
	if cbResult == nil || !cbResult.IsError {
		t.Fatalf("callback result = %+v, want non-nil IsError result", cbResult)
	}
}

// TestRunTask_ModelOverrideSuccess_AppliesOverride verifies the success path
// is unchanged: a resolvable override is applied to the tool loop.
func TestRunTask_ModelOverrideSuccess_AppliesOverride(t *testing.T) {
	defaultProvider := &recordingProvider{}
	overrideProvider := &recordingProvider{}
	sm := NewSubagentManager(defaultProvider, "default-model", "/tmp/test", nil, 10)
	sm.SetModelOverrideResolver(func(model string) (providers.LLMProvider, string, int, error) {
		return overrideProvider, model, 0, nil
	})

	task, cbResult, _ := spawnAndWait(t, sm, SpawnOptions{ModelOverride: "custom:model-x"})

	if task.Status != SubagentStatusCompleted {
		t.Fatalf("task status = %q, want %q (result: %s)", task.Status, SubagentStatusCompleted, task.Result)
	}
	if defaultProvider.calls != 0 {
		t.Errorf("default provider was called %d times, want 0", defaultProvider.calls)
	}
	if overrideProvider.calls == 0 {
		t.Fatal("override provider was never called")
	}
	// RunToolLoop strips provider prefixes it doesn't recognize before calling
	// the provider, so accept either the raw or the bare model name.
	if overrideProvider.model != "custom:model-x" && overrideProvider.model != "model-x" {
		t.Errorf("override provider model = %q, want %q", overrideProvider.model, "custom:model-x")
	}
	if cbResult == nil || cbResult.IsError {
		t.Fatalf("callback result = %+v, want non-nil success result", cbResult)
	}
}

// TestRunTask_NoModelOverride_Unaffected verifies tasks without an override
// never consult the resolver and run on the default provider as before.
func TestRunTask_NoModelOverride_Unaffected(t *testing.T) {
	defaultProvider := &recordingProvider{}
	sm := NewSubagentManager(defaultProvider, "default-model", "/tmp/test", nil, 10)
	sm.SetModelOverrideResolver(func(model string) (providers.LLMProvider, string, int, error) {
		t.Error("resolver must not be called when no model override is set")
		return nil, "", 0, errors.New("should not be called")
	})

	task, _, _ := spawnAndWait(t, sm, SpawnOptions{})

	if task.Status != SubagentStatusCompleted {
		t.Fatalf("task status = %q, want %q (result: %s)", task.Status, SubagentStatusCompleted, task.Result)
	}
	if defaultProvider.calls == 0 {
		t.Fatal("default provider was never called")
	}
}
