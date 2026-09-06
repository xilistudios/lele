// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

// Group turns replay the same message list across iterations, so a malformed
// tool call that is appended and then answered with a tool result breaks the
// next request exactly like it does in the main agent loop. These tests pin
// that the group-turn loop keeps tool results aligned with the tool calls it
// actually appended.

// scriptedProvider replays a canned list of responses and records the messages
// of every request, so a test can inspect what the next iteration would send.
type scriptedGroupProvider struct {
	mu        sync.Mutex
	responses []*providers.LLMResponse
	calls     int
	sent      [][]providers.Message
}

func (p *scriptedGroupProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, append([]providers.Message(nil), messages...))
	i := p.calls
	p.calls++
	if i < len(p.responses) {
		return p.responses[i], nil
	}
	return &providers.LLMResponse{Content: "fallback done"}, nil
}

func (p *scriptedGroupProvider) GetDefaultModel() string { return "test-model" }

func (p *scriptedGroupProvider) requests() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sent
}

// groupTurnCounterTool counts executions.
type groupTurnCounterTool struct {
	mu       sync.Mutex
	calls    int
	toolName string
}

func (g *groupTurnCounterTool) Name() string        { return g.toolName }
func (g *groupTurnCounterTool) Description() string { return "test" }
func (g *groupTurnCounterTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (g *groupTurnCounterTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	return tools.SilentResult("ok")
}
func (g *groupTurnCounterTool) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func runGroupTurnWithProvider(t *testing.T, p *scriptedGroupProvider, tool *groupTurnCounterTool) (string, error) {
	t.Helper()

	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Provider = p
	agent.Tools.Register(tool)

	al.registry.mu.Lock()
	al.registry.agents["test-agent"] = agent
	al.registry.mu.Unlock()

	lr := newLLMRunner(al)
	content, _, err := lr.runGroupTurn(context.Background(), group.TurnRequest{
		GroupID:      "g1",
		Speaker:      "test-agent",
		SystemPrompt: "sys",
		Instruction:  "do it",
		EnableTools:  true,
	})
	return content, err
}

// When every tool call in a response is invalid, the loop must not append tool
// results for calls that were dropped - the next request would carry orphans.
func TestRunGroupTurn_AllInvalidToolCallsDoNotOrphanResults(t *testing.T) {
	tool := &groupTurnCounterTool{toolName: "exec"}
	p := &scriptedGroupProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "call_dead", Type: "function", Arguments: nil}}},
		{Content: "recovered"},
	}}

	content, err := runGroupTurnWithProvider(t, p, tool)
	if err != nil {
		t.Fatalf("runGroupTurn: %v", err)
	}
	if content != "recovered" {
		t.Fatalf("the group turn did not recover, got %q", content)
	}
	if tool.count() != 0 {
		t.Fatalf("an invalid tool call was executed %d time(s)", tool.count())
	}

	// The second request is the one that would have carried the orphan.
	reqs := p.requests()
	if len(reqs) < 2 {
		t.Fatalf("expected a retry request, got %d requests", len(reqs))
	}
	assertGroupTurnMessagesConsistent(t, reqs[1])
}

// Mixed batch: only the valid call runs, and its result must reference the
// tool_call that was actually appended to the conversation.
func TestRunGroupTurn_ToolResultsMatchAppendedCalls(t *testing.T) {
	tool := &groupTurnCounterTool{toolName: "exec"}
	p := &scriptedGroupProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{
			{ID: "call_bad", Type: "function"},
			{ID: "call_good", Type: "function", Name: "exec", Arguments: map[string]interface{}{"command": "ls"}},
		}},
		{Content: "recovered"},
	}}

	if _, err := runGroupTurnWithProvider(t, p, tool); err != nil {
		t.Fatalf("runGroupTurn: %v", err)
	}
	if tool.count() != 1 {
		t.Fatalf("expected only the valid call to run, executed %d", tool.count())
	}

	reqs := p.requests()
	if len(reqs) < 2 {
		t.Fatalf("expected a follow-up request, got %d", len(reqs))
	}
	assertGroupTurnMessagesConsistent(t, reqs[1])

	var sawGood, sawBad bool
	for _, m := range reqs[1] {
		if m.Role == "tool" {
			switch m.ToolCallID {
			case "call_good":
				sawGood = true
			case "call_bad":
				sawBad = true
			}
		}
	}
	if !sawGood || sawBad {
		t.Fatalf("tool results must reference only the surviving call (good=%v bad=%v)", sawGood, sawBad)
	}
}

// assertGroupTurnMessagesConsistent verifies that a request the group loop
// would send is provider-safe: every assistant tool call is named with JSON
// object arguments, and every tool result points at one of them.
func assertGroupTurnMessagesConsistent(t *testing.T, messages []providers.Message) {
	t.Helper()

	declared := map[string]struct{}{}
	for _, m := range messages {
		for _, tc := range m.ToolCalls {
			if tc.Function == nil {
				t.Fatalf("appended tool call %s has no function", tc.ID)
			}
			if strings.TrimSpace(tc.FunctionName()) == "" {
				t.Fatalf("appended a nameless tool call: %+v", tc)
			}
			args := strings.TrimSpace(tc.ArgumentJSON())
			if !json.Valid([]byte(args)) || !strings.HasPrefix(args, "{") {
				t.Fatalf("appended arguments that are not a JSON object: %q", args)
			}
			declared[tc.ID] = struct{}{}
		}
	}
	for _, m := range messages {
		if m.Role == "tool" {
			if _, ok := declared[m.ToolCallID]; !ok {
				t.Fatalf("request carries an orphan tool result for id %q", m.ToolCallID)
			}
		}
	}
}
