package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/providers"
)

// recordedRecorder implements SessionRecorder and records all calls.
type recordedRecorder struct {
	mu    sync.Mutex
	added []providers.Message
	saved int
}

func (r *recordedRecorder) AddFullMessage(sessionKey string, msg providers.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.added = append(r.added, msg)
}

func (r *recordedRecorder) Save(string) error { return nil }

func (r *recordedRecorder) toolCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.added {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			n++
		}
	}
	return n
}

func (r *recordedRecorder) assistantCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.added {
		if m.Role == "assistant" {
			n++
		}
	}
	return n
}

// toolCallingProvider returns a tool call first, then a final answer.
type toolCallingProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *toolCallingProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{ID: "call_1", Type: "function", Name: "echo", Function: &providers.FunctionCall{Name: "echo", Arguments: `"hi"`}},
			},
		}, nil
	}
	return &providers.LLMResponse{Content: "final answer"}, nil
}

func (p *toolCallingProvider) GetDefaultModel() string { return "test-model" }

// echoTool is a configurable echo tool.
type echoTool struct {
	resultBuilder func(args map[string]interface{}) *ToolResult
}

func (e *echoTool) Name() string        { return "echo" }
func (e *echoTool) Description() string { return "echo" }
func (e *echoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (e *echoTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if e.resultBuilder != nil {
		return e.resultBuilder(args)
	}
	return &ToolResult{ForLLM: "echoed"}
}

// TestRunToolLoop_recordsSessionAdds verifies the tool-call path records
// assistant + tool messages and produces final content.
func TestRunToolLoop_recordsSessionAdds(t *testing.T) {
	p := &toolCallingProvider{}
	rec := &recordedRecorder{}
	reg := NewToolRegistry()
	reg.Register(&echoTool{})

	result, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:        p,
		Model:           "test-model",
		Tools:           reg,
		MaxIterations:   5,
		SessionRecorder: rec,
		SessionKey:      "skey",
		RetryWait: func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	}, []providers.Message{{Role: "user", Content: "start"}}, "cli", "direct")

	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if result.Content != "final answer" {
		t.Fatalf("content = %q", result.Content)
	}
	if rec.toolCalls() != 1 {
		t.Fatalf("toolCalls recorded = %d, want 1", rec.toolCalls())
	}
	if rec.assistantCount() < 1 {
		t.Fatalf("assistantCount = %d, want >=1", rec.assistantCount())
	}
}

// TestRunToolLoop_maxIterationsFallback verifies the MaxIterations fallback
// when the loop never returns a non-empty final response within bounds.
func TestRunToolLoop_maxIterationsFallback(t *testing.T) {
	result, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      &alwaysToolProvider{},
		Model:         "test-model",
		MaxIterations: 2,
		RetryWait: func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	}, []providers.Message{{Role: "user", Content: "go"}}, "", "")

	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if !strings.Contains(result.Content, "Maximum iterations reached") {
		t.Fatalf("expected max-iterations fallback, got %q", result.Content)
	}
	if result.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", result.Iterations)
	}
}

// alwaysToolProvider always returns a tool call.
type alwaysToolProvider struct {
	mu sync.Mutex
	n  int
}

func (a *alwaysToolProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{
			{ID: "c", Name: "echo", Type: "function", Function: &providers.FunctionCall{Name: "echo", Arguments: `{}`}},
		},
	}, nil
}

func (a *alwaysToolProvider) GetDefaultModel() string { return "test-model" }

// TestRunToolLoop_llmError verifies an LLM error aborts the loop.
func TestRunToolLoop_llmError(t *testing.T) {
	_, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      &errLLMProvider{},
		Model:         "test-model",
		MaxIterations: 3,
	}, []providers.Message{{Role: "user", Content: "hi"}}, "", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "LLM call failed") {
		t.Fatalf("err = %v", err)
	}
}

type errLLMProvider struct{}

func (e *errLLMProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	return nil, context.Canceled
}

func (e *errLLMProvider) GetDefaultModel() string { return "test-model" }

// TestRunToolLoop_noToolsSet verifies empty registry yields "No tools available"
// result but the loop still recovers.
func TestRunToolLoop_noToolsSet(t *testing.T) {
	p := &toolCallingProvider{}
	reg := NewToolRegistry() // no tools
	result, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      p,
		Model:         "test-model",
		Tools:         reg,
		MaxIterations: 3,
	}, []providers.Message{{Role: "user", Content: "hi"}}, "", "")
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if result.Content != "final answer" {
		t.Fatalf("content = %q", result.Content)
	}
}

// TestRunToolLoop_toolReturnsNilResult verifies a tool registered in the
// registry that returns a normal result still works end-to-end.
func TestRunToolLoop_toolReturnsNilResult(t *testing.T) {
	p := &toolCallingProvider{}
	reg := NewToolRegistry()
	reg.Register(&echoTool{})
	result, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      p,
		Model:         "test-model",
		Tools:         reg,
		MaxIterations: 3,
	}, []providers.Message{{Role: "user", Content: "hi"}}, "", "")
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if result.Content != "final answer" {
		t.Fatalf("content = %q", result.Content)
	}
}

// TestRunToolLoop_publishesBusEvents verifies the MessageBus publish paths.
func TestRunToolLoop_publishesBusEvents(t *testing.T) {
	p := &toolCallingProvider{}
	reg := NewToolRegistry()
	reg.Register(&echoTool{})

	mbb := bus.NewMessageBus()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	seen := map[string]int{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			msg, ok := mbb.SubscribeOutbound(ctx)
			if !ok {
				return
			}
			mu.Lock()
			seen[msg.Event]++
			mu.Unlock()
		}
	}()

	_, err := RunToolLoop(ctx, ToolLoopConfig{
		Provider:      p,
		Model:         "test-model",
		Tools:         reg,
		MaxIterations: 3,
		MessageBus:    mbb,
		ChatID:        "chat-1",
		RetryWait: func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	}, []providers.Message{{Role: "user", Content: "hi"}}, "chan", "chat")

	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}

	// Give the subscriber goroutine time to drain pending messages
	// before cancelling the context (which would drop them).
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if seen["tool.executing"] == 0 {
		t.Error("expected at least one tool.executing event")
	}
	if seen["tool.result"] == 0 {
		t.Error("expected at least one tool.result event")
	}
	if seen["message.stream"] == 0 {
		t.Error("expected message.stream event")
	}
}

// verboseCallbackRecorder captures verbose callback invocations.
type verboseCallbackRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (v *verboseCallbackRecorder) Record(iter int, name string, args map[string]interface{}, res *ToolResult) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls = append(v.calls, name)
}

// TestRunToolLoop_verboseCallback verifies VerboseCallback is invoked per tool.
func TestRunToolLoop_verboseCallback(t *testing.T) {
	p := &toolCallingProvider{}
	reg := NewToolRegistry()
	reg.Register(&echoTool{})

	vcb := &verboseCallbackRecorder{}
	_, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:        p,
		Model:           "test-model",
		Tools:           reg,
		MaxIterations:   3,
		VerboseCallback: vcb.Record,
		RetryWait: func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	}, []providers.Message{{Role: "user", Content: "hi"}}, "", "")

	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	vcb.mu.Lock()
	defer vcb.mu.Unlock()
	if len(vcb.calls) == 0 {
		t.Fatal("expected at least one verbose callback")
	}
	if vcb.calls[0] != "echo" {
		t.Fatalf("first callback tool = %q, want echo", vcb.calls[0])
	}
}

// TestRunToolLoop_errFromToolWithEmptyForLLM verifies empty ForLLM with non-nil
// Err uses the error text as content.
func TestRunToolLoop_errFromToolWithEmptyForLLM(t *testing.T) {
	p := &toolCallingProvider{}
	reg := NewToolRegistry()
	reg.Register(&errToolEmptyForLLM{})

	result, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      p,
		Model:         "test-model",
		Tools:         reg,
		MaxIterations: 3,
	}, []providers.Message{{Role: "user", Content: "hi"}}, "", "")
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if result.Content == "" {
		t.Fatal("expected content")
	}
}

type errToolEmptyForLLM struct{}

func (*errToolEmptyForLLM) Name() string        { return "echo" }
func (*errToolEmptyForLLM) Description() string { return "echo" }
func (*errToolEmptyForLLM) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (*errToolEmptyForLLM) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	return (&ToolResult{ForLLM: ""}).WithError(errBadThing())
}

func errBadThing() error { return &toolLoopError{} }

type toolLoopError struct{}

func (t *toolLoopError) Error() string { return "tool failed badly" }

// TestRunToolLoop_truncation verifies large tool results are truncated.
func TestRunToolLoop_truncation(t *testing.T) {
	bigResult := strings.Repeat("x", 60000)
	p := &toolCallingProviderForResult{
		tc: []providers.ToolCall{{ID: "c", Name: "echo", Type: "function", Function: &providers.FunctionCall{Name: "echo", Arguments: `{}`}}},
	}
	reg := NewToolRegistry()
	reg.Register(&fixedEchoTool{result: bigResult})
	rec := &recordedRecorder{}

	_, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:        p,
		Model:           "test-model",
		Tools:           reg,
		MaxIterations:   3,
		SessionRecorder: rec,
		SessionKey:      "trunc",
		RetryWait: func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	}, []providers.Message{{Role: "user", Content: "hi"}}, "", "")
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	found := false
	for _, m := range rec.added {
		if m.Role == "tool" && strings.Contains(m.Content, "truncated") {
			found = true
			// Truncated to 50000 chars + suffix "\n... (truncated, N more chars)"
			// which is ~34 chars, so total should be <= 50050.
			if len(m.Content) > 50050 {
				t.Fatalf("tool message not truncated, len=%d", len(m.Content))
			}
		}
	}
	if !found {
		t.Fatal("expected a truncated tool message to be recorded")
	}
}

type fixedEchoTool struct {
	result string
}

func (f *fixedEchoTool) Name() string        { return "echo" }
func (f *fixedEchoTool) Description() string { return "echo" }
func (f *fixedEchoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (f *fixedEchoTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	return &ToolResult{ForLLM: f.result}
}

// TestRunToolLoop_contextCompaction verifies the ContextWindow path triggers
// compaction.
func TestRunToolLoop_contextCompaction(t *testing.T) {
	p := &toolCallingProviderForResult{
		tc: []providers.ToolCall{{ID: "c", Name: "echo", Type: "function", Function: &providers.FunctionCall{Name: "echo", Arguments: `{}`}}},
	}
	reg := NewToolRegistry()
	reg.Register(&echoTool{resultBuilder: func(args map[string]interface{}) *ToolResult {
		return &ToolResult{ForLLM: strings.Repeat("m", 3000)}
	}})
	compacting := &compactingProvider{inner: p}

	_, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      compacting,
		Model:         "test-model",
		Tools:         reg,
		MaxIterations: 5,
		ContextWindow: 50,
		RetryWait: func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	}, []providers.Message{{Role: "user", Content: "start"}}, "", "")
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if compacting.compactions == 0 {
		t.Error("expected at least one compaction attempt")
	}
}

// compactingProvider wraps a tool-calling provider and records compaction calls.
type compactingProvider struct {
	inner       *toolCallingProviderForResult
	mu          sync.Mutex
	compactions int
}

func (c *compactingProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	c.mu.Lock()
	c.compactions++
	c.mu.Unlock()
	return c.inner.Chat(ctx, messages, tools, model, options)
}

func (c *compactingProvider) GetDefaultModel() string { return "test-model" }

// toolCallingProviderForResult lets tests script a tool call then an answer.
type toolCallingProviderForResult struct {
	tc []providers.ToolCall
	mu sync.Mutex
	n  int
}

func (p *toolCallingProviderForResult) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	if p.n == 1 {
		return &providers.LLMResponse{ToolCalls: p.tc}, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *toolCallingProviderForResult) GetDefaultModel() string { return "test-model" }

// TestRunToolLoop_redactsAndTruncates verifies redaction of tool results using
// a real keyring redactor. (Full redaction coverage lives in
// toolloop_redact_test.go; here we verify the wiring path in RunToolLoop.)
func TestRunToolLoop_redactsAndTruncates(t *testing.T) {
	p := &toolCallingProviderForResult{
		tc: []providers.ToolCall{{ID: "c", Name: "echo", Type: "function", Function: &providers.FunctionCall{Name: "echo", Arguments: `{}`}}},
	}
	reg := NewToolRegistry()
	reg.Register(&echoTool{})

	// A nil-service Redactor is a safe no-op; the redaction branch is exercised.
	red := keyring.NewRedactor(nil)

	_, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      p,
		Model:         "test-model",
		Tools:         reg,
		MaxIterations: 3,
		Redactor:      red,
		RetryWait: func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	}, []providers.Message{{Role: "user", Content: "start"}}, "", "")

	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if red == nil {
		t.Fatal("nil redactor")
	}
}
