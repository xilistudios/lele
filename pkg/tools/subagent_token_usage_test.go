// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package tools

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// ============================================================================
// Subagent token accounting.
//
// Subagent LLM calls run through RunToolLoop, which has no session manager of
// its own. Without a reporter wired, only the main agent loop's own responses
// reach the cumulative token counters the TUI /status and WebUI display - the
// subagent's spend is invisible. These tests pin the contract of the reporter:
// every response a subagent loop produces is billed, incrementally, to the
// session that OWNS the task (spawner runtime key, falling back to the routing
// origin key), never to the subagent's own child session.
// ============================================================================

// usageBilledProvider answers each conversation with a tool-free completion
// carrying a fixed usage block.
type usageBilledProvider struct{}

func (usageBilledProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content: "STATUS: completed\nSUMMARY: Done\nDETAILS:\nok",
		Usage:   &providers.UsageInfo{PromptTokens: 111, CompletionTokens: 22, TotalTokens: 133},
	}, nil
}

func (usageBilledProvider) GetDefaultModel() string { return "test-model" }

// billingCollector records (sessionKey, in, out) triples reported by the
// subagent token-usage reporter.
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

// awaitTaskDone waits for a subagent task to reach a terminal state. The task
// returned is a fresh snapshot taken AFTER the done signal: the manager mutates
// the live task under its lock, so a snapshot captured before completion still
// reads "running".
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

// TestSubagentTokens_BilledToSpawnerSession covers the primary fix: an async
// spawn made from inside an agent turn (tool context carries the parent's
// runtime session key) must bill the parent session, not the child.
func TestSubagentTokens_BilledToSpawnerSession(t *testing.T) {
	sm := NewSubagentManager(usageBilledProvider{}, "test-model", t.TempDir(), nil, 10)
	collector := &billingCollector{}
	sm.SetTokenUsageReporter(collector.report)

	ctx := WithAgentToolContext(context.Background(), "main", "agent:main:telegram:42")
	result, err := sm.SpawnWithOptions(ctx, "do work", "label", "main", "telegram", "42", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}
	awaitTaskDone(t, sm, spawnTaskID(t, result))

	entries := collector.snapshot()
	in, out, keys := totalBilled(entries)
	if len(keys) != 1 || keys[0] != "agent:main:telegram:42" {
		t.Fatalf("billed keys = %v, want exactly [%q]", keys, "agent:main:telegram:42")
	}
	if in != 111 || out != 22 {
		t.Errorf("billed (%d, %d), want (111, 22)", in, out)
	}
}

// TestSubagentTokens_BilledToOriginWithoutSpawnerKey covers tasks spawned
// outside an agent turn (no tool context): billing falls back to the
// routing-derived origin key so the spend still lands on the parent family
// instead of vanishing.
func TestSubagentTokens_BilledToOriginWithoutSpawnerKey(t *testing.T) {
	sm := NewSubagentManager(usageBilledProvider{}, "test-model", t.TempDir(), nil, 10)
	collector := &billingCollector{}
	sm.SetTokenUsageReporter(collector.report)

	result, err := sm.SpawnWithOptions(context.Background(), "do work", "label", "", "telegram", "42", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}
	awaitTaskDone(t, sm, spawnTaskID(t, result))

	entries := collector.snapshot()
	_, _, keys := totalBilled(entries)
	if len(keys) != 1 || keys[0] != "telegram:42" {
		t.Fatalf("billed keys = %v, want exactly [%q]", keys, "telegram:42")
	}
}

// TestSubagentTokens_NotBilledWithoutReporter pins opt-in behavior: a manager
// with no reporter (standalone, tests) runs tasks unchanged.
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

// TestSubagentTokens_BilledPerAttempt verifies retries are billed for each
// attempt they make: two provider responses with usage means double billing.
func TestSubagentTokens_BilledPerAttempt(t *testing.T) {
	sm := NewSubagentManager(usageBilledProvider{}, "test-model", t.TempDir(), nil, 10)
	collector := &billingCollector{}
	sm.SetTokenUsageReporter(collector.report)

	// Drive the loop directly with two iterations (tool call + final answer)
	// so one run produces two billable responses.
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

// TestSubagentTokens_SyncToolBillsCallerSession covers the synchronous
// `subagent` tool: its loop must bill the caller's session (from the tool
// context), same as the async path.
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
