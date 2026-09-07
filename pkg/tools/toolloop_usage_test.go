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

	"github.com/xilistudios/lele/pkg/providers"
)

// usageProvider answers with a scripted sequence of responses, each carrying
// an explicit usage block, so tests can assert exactly what the loop billed.
type usageProvider struct {
	mu        sync.Mutex
	responses []*providers.LLMResponse
	calls     int
}

func (p *usageProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	resp := p.responses[p.calls%len(p.responses)]
	p.calls++
	return resp, nil
}

func (p *usageProvider) GetDefaultModel() string { return "test-model" }

// usageRecorder collects OnTokenUsage callbacks from RunToolLoop.
type usageRecorder struct {
	mu    sync.Mutex
	calls [][2]int
}

func (r *usageRecorder) report(in, out int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [2]int{in, out})
}

func (r *usageRecorder) snapshot() [][2]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][2]int, len(r.calls))
	copy(out, r.calls)
	return out
}

func usage(prompt, completion int) *providers.UsageInfo {
	return &providers.UsageInfo{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
}

// TestRunToolLoop_OnTokenUsage_BillsEveryResponse verifies the hook fires once
// per successful LLM response (tool-call iterations included) with the
// provider-reported counts, so a multi-step run is fully accounted for.
func TestRunToolLoop_OnTokenUsage_BillsEveryResponse(t *testing.T) {
	provider := &usageProvider{responses: []*providers.LLMResponse{
		{
			ToolCalls: []providers.ToolCall{{
				ID:        "call-1",
				Name:      "echo",
				Arguments: map[string]interface{}{"msg": "hi"},
			}},
			Usage: usage(100, 10),
		},
		{Content: "final answer", Usage: usage(200, 20)},
	}}

	rec := NewToolRegistry()
	rec.Register(mockTool{})

	recorder := &usageRecorder{}
	res, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      provider,
		Model:         "test-model",
		Tools:         rec,
		MaxIterations: 5,
		OnTokenUsage:  recorder.report,
	}, []providers.Message{{Role: "user", Content: "go"}}, "test", "chat1")
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if res.Content != "final answer" {
		t.Fatalf("content = %q, want %q", res.Content, "final answer")
	}

	got := recorder.snapshot()
	want := [][2]int{{100, 10}, {200, 20}}
	if len(got) != len(want) {
		t.Fatalf("billed %d responses (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("billing %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestRunToolLoop_OnTokenUsage_EstimatesWithoutUsage verifies responses that
// carry no usage block are still billed using the shared heuristic instead of
// being silently dropped from the totals.
func TestRunToolLoop_OnTokenUsage_EstimatesWithoutUsage(t *testing.T) {
	provider := &usageProvider{responses: []*providers.LLMResponse{
		{Content: "answer without usage"},
	}}

	recorder := &usageRecorder{}
	if _, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      provider,
		Model:         "test-model",
		MaxIterations: 3,
		OnTokenUsage:  recorder.report,
	}, []providers.Message{{Role: "user", Content: "question"}}, "test", "chat1"); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}

	got := recorder.snapshot()
	if len(got) != 1 {
		t.Fatalf("billed %d responses, want 1", len(got))
	}
	// Same numbers providers.ResponseTokenCounts computes for this input;
	// asserting the call happened with non-zero estimates is the contract
	// under test (the arithmetic itself is covered in pkg/providers).
	wantIn, wantOut := providers.ResponseTokenCounts(
		[]providers.Message{{Role: "user", Content: "question"}},
		&providers.LLMResponse{Content: "answer without usage"},
	)
	if got[0] != [2]int{wantIn, wantOut} {
		t.Errorf("billing = %v, want (%d, %d)", got[0], wantIn, wantOut)
	}
	if wantIn == 0 || wantOut == 0 {
		t.Errorf("expected non-zero heuristic estimates, got (%d, %d)", wantIn, wantOut)
	}
}

// TestRunToolLoop_OnTokenUsage_NilHookIsSafe pins that the feature is opt-in:
// loops configured without a reporter behave exactly as before.
func TestRunToolLoop_OnTokenUsage_NilHookIsSafe(t *testing.T) {
	provider := &usageProvider{responses: []*providers.LLMResponse{
		{Content: "ok", Usage: usage(5, 5)},
	}}
	res, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      provider,
		Model:         "test-model",
		MaxIterations: 3,
		OnTokenUsage:  nil,
	}, []providers.Message{{Role: "user", Content: "hi"}}, "test", "chat1")
	if err != nil {
		t.Fatalf("RunToolLoop with nil hook: %v", err)
	}
	if res.Content != "ok" {
		t.Errorf("content = %q, want %q", res.Content, "ok")
	}
}
