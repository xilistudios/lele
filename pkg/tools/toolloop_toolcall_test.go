// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// The subagent tool loop shares the failure mode the main agent loop has: a
// malformed tool call that gets recorded makes every later request for that
// session fail with 400 "function.arguments must be in JSON format", and a tool
// result recorded for a call that was dropped leaves an orphan message, which is
// rejected too. These tests pin the two invariants for RunToolLoop.

// toolCallTestRecorder captures everything the loop writes to the session.
type toolCallTestRecorder struct {
	mu       sync.Mutex
	messages []providers.Message
}

func (r *toolCallTestRecorder) AddFullMessage(sessionKey string, msg providers.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msg)
}

func (r *toolCallTestRecorder) Save(string) error { return nil }

func (r *toolCallTestRecorder) all() []providers.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]providers.Message(nil), r.messages...)
}

// toolCallScriptProvider replays a canned sequence of responses.
type toolCallScriptProvider struct {
	mu        sync.Mutex
	responses []*providers.LLMResponse
	calls     int
}

func (p *toolCallScriptProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.calls
	p.calls++
	if i < len(p.responses) {
		return p.responses[i], nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *toolCallScriptProvider) GetDefaultModel() string { return "test-model" }

// countingTool records how many times it was executed.
type countingTool struct {
	name     string
	mu       sync.Mutex
	executed int
}

func (c *countingTool) Name() string        { return c.name }
func (c *countingTool) Description() string { return "test tool" }
func (c *countingTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (c *countingTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult {
	c.mu.Lock()
	c.executed++
	c.mu.Unlock()
	return SilentResult("ok")
}

func (c *countingTool) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.executed
}

func newToolCallLoopConfig(p providers.LLMProvider, toolList ...Tool) ToolLoopConfig {
	registry := NewToolRegistry()
	for _, tl := range toolList {
		registry.Register(tl)
	}
	return ToolLoopConfig{
		Provider:      p,
		Tools:         registry,
		MaxIterations: 5,
		SessionKey:    "subagent-test",
		RetryWait: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time)
			close(ch)
			return ch
		},
	}
}

// assertNoPoisonedToolCalls checks the raw recorded values: a stored
// "null"/empty/non-object arguments payload or a nameless call reproduces the
// production 400 on the next turn.
func assertNoPoisonedToolCalls(t *testing.T, msgs []providers.Message) {
	t.Helper()

	declared := map[string]struct{}{}
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if strings.TrimSpace(tc.FunctionName()) == "" {
				t.Fatalf("recorded a nameless tool call: %+v", tc)
			}
			if tc.Function == nil {
				t.Fatalf("recorded tool call %s without a function", tc.ID)
			}
			args := strings.TrimSpace(tc.Function.Arguments)
			if !json.Valid([]byte(args)) || !strings.HasPrefix(args, "{") {
				t.Fatalf("recorded arguments that are not a JSON object: %q", args)
			}
			if tc.Arguments == nil {
				t.Fatalf("recorded tool call %s without a decoded map", tc.ID)
			}
			declared[tc.ID] = struct{}{}
		}
	}
	for _, m := range msgs {
		if m.Role == "tool" {
			if _, ok := declared[m.ToolCallID]; !ok {
				t.Fatalf("recorded an orphan tool result for call id %q", m.ToolCallID)
			}
		}
	}
}

// A nameless call with nil arguments is what killed sessions in production. It
// must not be recorded, must not run, and the model must be asked to retry.
func TestRunToolLoop_NamelessToolCallIsNeverRecorded(t *testing.T) {
	rec := &toolCallTestRecorder{}
	tool := &countingTool{name: "leaky"}
	provider := &toolCallScriptProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "call_dead", Type: "function", Arguments: nil}}},
		{Content: "recovered"},
	}}

	cfg := newToolCallLoopConfig(provider, tool)
	cfg.SessionRecorder = rec

	if _, err := RunToolLoop(context.Background(), cfg,
		[]providers.Message{{Role: "user", Content: "go"}}, "cli", "direct"); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}

	if tool.count() != 0 {
		t.Fatalf("an invalid tool call was executed %d time(s)", tool.count())
	}

	msgs := rec.all()
	assertNoPoisonedToolCalls(t, msgs)
	for _, m := range msgs {
		if m.Role == "tool" {
			t.Fatalf("a tool result was recorded for a rejected call: %+v", m)
		}
	}

	var guided bool
	for _, m := range msgs {
		if m.Role == "user" && strings.Contains(m.Content, "malformed") {
			guided = true
		}
	}
	if !guided {
		t.Fatal("the model was never asked to retry the malformed call")
	}
}

// Mixed batch: the valid call must be the one executed and recorded, and its
// result must reference the id that was recorded.
func TestRunToolLoop_ToolResultsMatchRecordedCalls(t *testing.T) {
	rec := &toolCallTestRecorder{}
	tool := &countingTool{name: "good"}
	provider := &toolCallScriptProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{
			{ID: "call_bad", Type: "function"},
			{ID: "call_good", Type: "function", Name: "good", Arguments: map[string]interface{}{"a": "b"}},
		}},
		{Content: "recovered"},
	}}

	cfg := newToolCallLoopConfig(provider, tool)
	cfg.SessionRecorder = rec

	if _, err := RunToolLoop(context.Background(), cfg,
		[]providers.Message{{Role: "user", Content: "go"}}, "cli", "direct"); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}

	if tool.count() != 1 {
		t.Fatalf("expected exactly the valid call to run, got %d", tool.count())
	}

	msgs := rec.all()
	assertNoPoisonedToolCalls(t, msgs)

	var results []string
	for _, m := range msgs {
		if m.Role == "tool" {
			results = append(results, m.ToolCallID)
		}
	}
	if len(results) != 1 || results[0] != "call_good" {
		t.Fatalf("tool results must reference the surviving call, got %v", results)
	}
}

// Arguments that are not a JSON object must be normalised before recording,
// otherwise the subagent session is poisoned for good.
func TestRunToolLoop_RecordsCanonicalArguments(t *testing.T) {
	rec := &toolCallTestRecorder{}
	tool := &countingTool{name: "good"}
	provider := &toolCallScriptProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Name:     "good",
			Function: &providers.FunctionCall{Name: "good", Arguments: "null"},
		}}},
		{Content: "recovered"},
	}}

	cfg := newToolCallLoopConfig(provider, tool)
	cfg.SessionRecorder = rec

	if _, err := RunToolLoop(context.Background(), cfg,
		[]providers.Message{{Role: "user", Content: "go"}}, "cli", "direct"); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}

	msgs := rec.all()
	assertNoPoisonedToolCalls(t, msgs)

	var recorded *providers.Message
	for i := range msgs {
		if msgs[i].Role == "assistant" && len(msgs[i].ToolCalls) > 0 {
			recorded = &msgs[i]
		}
	}
	if recorded == nil {
		t.Fatal("no assistant tool-call message was recorded")
	}
	if got := strings.TrimSpace(recorded.ToolCalls[0].Function.Arguments); got != "{}" {
		t.Fatalf("null arguments were recorded verbatim: %q", got)
	}
	if tool.count() != 1 {
		t.Fatalf("the repaired call should still run, executed %d", tool.count())
	}
}
