package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

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

func waitForSubagentDone(t *testing.T, task *SubagentTask) {
	t.Helper()
	select {
	case <-task.DoneChannel():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for subagent task to finish")
	}
}

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

	task, ok := sm.GetTask(taskID)
	if !ok {
		t.Fatalf("task %s not found after spawn", taskID)
	}
	waitForSubagentDone(t, task)

	var cbResult *ToolResult
	select {
	case cbResult = <-results:
	case <-time.After(2 * time.Second):
	}
	fresh, ok := sm.GetTask(taskID)
	if !ok {
		t.Fatalf("task %s disappeared after completion", taskID)
	}
	return fresh, cbResult, results
}

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
	select {
	case extra := <-cbResults:
		t.Fatalf("callback fired a second time: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

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

func TestRunTask_ModelOverrideNoResolver_FailsTask(t *testing.T) {
	defaultProvider := &recordingProvider{}
	sm := NewSubagentManager(defaultProvider, "default-model", "/tmp/test", nil, 10)

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
	if overrideProvider.model != "custom:model-x" && overrideProvider.model != "model-x" {
		t.Errorf("override provider model = %q, want %q", overrideProvider.model, "custom:model-x")
	}
	if cbResult == nil || cbResult.IsError {
		t.Fatalf("callback result = %+v, want non-nil success result", cbResult)
	}
}

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
