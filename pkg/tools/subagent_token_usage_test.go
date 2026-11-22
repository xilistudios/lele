package tools

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

type usageBilledProvider struct{}

func (usageBilledProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content: "STATUS: completed\nSUMMARY: Done\nDETAILS:\nok",
		Usage:   &providers.UsageInfo{PromptTokens: 111, CompletionTokens: 22, TotalTokens: 133},
	}, nil
}

func (usageBilledProvider) GetDefaultModel() string { return "test-model" }

type billingCollector struct {
	mu     sync.Mutex
	billed []billingEntry
}

type billingEntry struct {
	sessionKey string
	in, out    int
}

func (c *billingCollector) report(sessionKey string, in, out int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.billed = append(c.billed, billingEntry{sessionKey: sessionKey, in: in, out: out})
}

func (c *billingCollector) snapshot() []billingEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]billingEntry, len(c.billed))
	copy(out, c.billed)
	return out
}

func awaitTaskDone(t *testing.T, sm *SubagentManager, id string) *SubagentTask {
	t.Helper()
	task, ok := sm.GetTask(id)
	if !ok {
		t.Fatalf("task %s not found", id)
	}
	select {
	case <-task.DoneChannel():
	case <-time.After(10 * time.Second):
		t.Fatalf("task %s did not finish in time", id)
	}
	fresh, ok := sm.GetTask(id)
	if !ok {
		t.Fatalf("task %s disappeared after completion", id)
	}
	return fresh
}

func totalBilled(entries []billingEntry) (in, out int, keys []string) {
	seen := map[string]bool{}
	for _, e := range entries {
		in += e.in
		out += e.out
		if !seen[e.sessionKey] {
			seen[e.sessionKey] = true
			keys = append(keys, e.sessionKey)
		}
	}
	return
}

func TestSubagentTokens_BilledToSpawnerSession(t *testing.T) {
	sm := NewSubagentManager(usageBilledProvider{}, "test-model", t.TempDir(), nil, 10)
	collector := &billingCollector{}
	sm.SetTokenUsageReporter(collector.report)

	ctx := WithAgentToolContext(context.Background(), "main", "agent:main:telegram:42")
	result, err := sm.SpawnWithOptions(ctx, "do work", "label", "main", "telegram", "42", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}
	taskID := spawnTaskID(t, result)
	awaitTaskDone(t, sm, taskID)

	entries := collector.snapshot()
	in, out, keys := totalBilled(entries)

	ownerKey := "agent:main:telegram:42"
	childKey := "telegram:42:" + taskID
	if len(keys) != 2 {
		t.Fatalf("billed %d keys, want 2 (owner + child): %v", len(keys), keys)
	}
	keySet := map[string]bool{}
	for _, k := range keys {
		keySet[k] = true
	}
	if !keySet[ownerKey] {
		t.Errorf("owner key %q not in billed keys %v", ownerKey, keys)
	}
	if !keySet[childKey] {
		t.Errorf("child key %q not in billed keys %v", childKey, keys)
	}
	if in != 222 || out != 44 {
		t.Errorf("total billed (%d, %d), want (222, 44) [2×(111,22)]", in, out)
	}
}

func TestSubagentTokens_BilledToOriginWithoutSpawnerKey(t *testing.T) {
	sm := NewSubagentManager(usageBilledProvider{}, "test-model", t.TempDir(), nil, 10)
	collector := &billingCollector{}
	sm.SetTokenUsageReporter(collector.report)

	result, err := sm.SpawnWithOptions(context.Background(), "do work", "label", "", "telegram", "42", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}
	taskID := spawnTaskID(t, result)
	awaitTaskDone(t, sm, taskID)

	entries := collector.snapshot()
	_, _, keys := totalBilled(entries)

	ownerKey := "telegram:42"
	childKey := "telegram:42:" + taskID
	if len(keys) != 2 {
		t.Fatalf("billed %d keys, want 2 (owner + child): %v", len(keys), keys)
	}
	keySet := map[string]bool{}
	for _, k := range keys {
		keySet[k] = true
	}
	if !keySet[ownerKey] {
		t.Errorf("owner key %q not in billed keys %v", ownerKey, keys)
	}
	if !keySet[childKey] {
		t.Errorf("child key %q not in billed keys %v", childKey, keys)
	}
}

func TestSubagentTokens_NotBilledWithoutReporter(t *testing.T) {
	sm := NewSubagentManager(usageBilledProvider{}, "test-model", t.TempDir(), nil, 10)

	result, err := sm.SpawnWithOptions(context.Background(), "do work", "label", "", "telegram", "42", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}
	task := awaitTaskDone(t, sm, spawnTaskID(t, result))
	if task.Status != SubagentStatusCompleted {
		t.Fatalf("task status = %s, want completed", task.Status)
	}
}

func TestSubagentTokens_BilledPerAttempt(t *testing.T) {
	sm := NewSubagentManager(usageBilledProvider{}, "test-model", t.TempDir(), nil, 10)
	collector := &billingCollector{}
	sm.SetTokenUsageReporter(collector.report)

	task := &SubagentTask{
		ID:               "multi-1",
		Task:             "do work",
		AgentID:          "main",
		OriginSessionKey: "telegram:42",
		Status:           SubagentStatusRunning,
	}
	task.InitDoneChannel()

	loopOwner := subagentLoopOwner(task, task.OriginSessionKey+":"+task.ID)
	provider := &usageProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "c1", Name: "echo", Arguments: map[string]interface{}{}}}, Usage: usage(10, 1)},
		{Content: "done", Usage: usage(20, 2)},
	}}
	reg := NewToolRegistry()
	reg.Register(mockTool{})

	if _, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      provider,
		Model:         "test-model",
		Tools:         reg,
		MaxIterations: 5,
		SessionKey:    task.OriginSessionKey + ":" + task.ID,
		OnTokenUsage: func(in, out int) {
			collector.report(loopOwner, in, out)
		},
	}, []providers.Message{{Role: "user", Content: "go"}}, "telegram", "42"); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}

	entries := collector.snapshot()
	in, out, keys := totalBilled(entries)
	if len(keys) != 1 || keys[0] != "telegram:42" {
		t.Fatalf("billed keys = %v, want [%q]", keys, "telegram:42")
	}
	if len(entries) != 2 {
		t.Fatalf("billed %d responses, want 2", len(entries))
	}
	if in != 30 || out != 3 {
		t.Errorf("total billed (%d, %d), want (30, 3)", in, out)
	}
}

func TestSubagentTokens_SyncToolBillsCallerSession(t *testing.T) {
	sm := NewSubagentManager(usageBilledProvider{}, "test-model", t.TempDir(), nil, 10)
	collector := &billingCollector{}
	sm.SetTokenUsageReporter(collector.report)

	tool := NewSubagentTool(sm)
	ctx := WithAgentToolContext(context.Background(), "main", "agent:main:native:abc")
	res := tool.Execute(ctx, map[string]interface{}{"task": "do work"})
	if res.IsError {
		t.Fatalf("subagent tool failed: %v", res.ForLLM)
	}

	entries := collector.snapshot()
	in, out, keys := totalBilled(entries)
	if len(keys) != 1 || keys[0] != "agent:main:native:abc" {
		t.Fatalf("billed keys = %v, want exactly [%q]", keys, "agent:main:native:abc")
	}
	if in != 111 || out != 22 {
		t.Errorf("billed (%d, %d), want (111, 22)", in, out)
	}
}

func TestSubagentTokens_OwnerAndChildReceiveIdenticalAmounts(t *testing.T) {
	sm := NewSubagentManager(usageBilledProvider{}, "test-model", t.TempDir(), nil, 10)
	collector := &billingCollector{}
	sm.SetTokenUsageReporter(collector.report)

	ctx := WithAgentToolContext(context.Background(), "main", "agent:main:telegram:42")
	result, err := sm.SpawnWithOptions(ctx, "do work", "label", "main", "telegram", "42", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}
	taskID := spawnTaskID(t, result)
	awaitTaskDone(t, sm, taskID)

	entries := collector.snapshot()
	if len(entries) != 2 {
		t.Fatalf("expected 2 billing entries (1 owner + 1 child), got %d: %v", len(entries), entries)
	}

	ownerKey := "agent:main:telegram:42"
	childKey := "telegram:42:" + taskID

	if entries[0].in != entries[1].in || entries[0].out != entries[1].out {
		t.Errorf("owner and child amounts differ: %v vs %v", entries[0], entries[1])
	}
	if entries[0].in != 111 || entries[0].out != 22 {
		t.Errorf("billed amount = (%d, %d), want (111, 22)", entries[0].in, entries[0].out)
	}
	keySet := map[string]bool{}
	for _, e := range entries {
		keySet[e.sessionKey] = true
	}
	if !keySet[ownerKey] || !keySet[childKey] {
		t.Errorf("expected keys {%q, %q}, got %v", ownerKey, childKey, keySet)
	}
}
