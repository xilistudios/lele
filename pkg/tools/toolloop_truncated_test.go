package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestRunToolLoop_TruncatedArgumentsAreNotExecuted(t *testing.T) {
	rec := &toolCallTestRecorder{}
	writer := &countingTool{name: "write_file"}
	provider := &toolCallScriptProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{
			ID:   "call_cut",
			Type: "function",
			Function: &providers.FunctionCall{
				Name:      "write_file",
				Arguments: `{"path":"/tmp/big.go","content":"package main`,
			},
		}}},
		{Content: "done"},
	}}

	cfg := newToolCallLoopConfig(provider, writer)
	cfg.SessionRecorder = rec

	if _, err := RunToolLoop(context.Background(), cfg,
		[]providers.Message{{Role: "user", Content: "write it"}}, "cli", "direct"); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}

	if got := writer.count(); got != 0 {
		t.Fatalf("a call with cut-off arguments reached the tool %d time(s)", got)
	}

	msgs := rec.all()
	var toolMsg *providers.Message
	for i := range msgs {
		if msgs[i].Role == "tool" {
			toolMsg = &msgs[i]
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool result recorded: the model got no feedback about the call")
	}
	if toolMsg.ToolCallID != "call_cut" {
		t.Fatalf("tool result keyed to %q, want call_cut", toolMsg.ToolCallID)
	}
	for _, want := range []string{"cut off", "output token limit", "append"} {
		if !strings.Contains(toolMsg.Content, want) {
			t.Fatalf("tool result does not mention %q: %q", want, toolMsg.Content)
		}
	}
	if !strings.Contains(toolMsg.Content, "path") {
		t.Fatalf("tool result lost the surviving arguments: %q", toolMsg.Content)
	}

	assertNoPoisonedToolCalls(t, msgs)
}

func TestRunToolLoop_CompleteArgumentsAreExecuted(t *testing.T) {
	writer := &countingTool{name: "write_file"}
	provider := &toolCallScriptProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{
			ID:   "call_ok",
			Type: "function",
			Function: &providers.FunctionCall{
				Name:      "write_file",
				Arguments: `{"path":"/tmp/a.go","content":"package main"}`,
			},
		}}},
		{Content: "done"},
	}}

	cfg := newToolCallLoopConfig(provider, writer)
	cfg.SessionRecorder = &toolCallTestRecorder{}

	if _, err := RunToolLoop(context.Background(), cfg,
		[]providers.Message{{Role: "user", Content: "write it"}}, "cli", "direct"); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if got := writer.count(); got != 1 {
		t.Fatalf("complete call executed %d times, want 1", got)
	}
}
