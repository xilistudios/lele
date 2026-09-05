package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// captureProvider is a minimal LLMProvider used in tests. It records the tool
// definitions passed to each Chat call so tests can assert what was (or was
// not) advertised to the model.
type captureProvider struct {
	mu       sync.Mutex
	lastDefs []providers.ToolDefinition
}

func (p *captureProvider) Chat(_ context.Context, _ []providers.Message, tools []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.lastDefs = tools
	p.mu.Unlock()
	// Return a direct answer (no tool calls) so the loop exits after one iteration.
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *captureProvider) GetDefaultModel() string {
	return "test-model"
}

// scriptedProvider returns a fixed sequence of responses, then fails if called
// beyond that. Used to verify that RunToolLoop retries empty responses instead
// of terminating early.
type scriptedProvider struct {
	mu        sync.Mutex
	responses []*providers.LLMResponse
	calls     int
}

func (p *scriptedProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls >= len(p.responses) {
		return nil, fmt.Errorf("unexpected extra Chat call")
	}
	resp := p.responses[p.calls]
	p.calls++
	return resp, nil
}

func (p *scriptedProvider) GetDefaultModel() string {
	return "test-model"
}

func (p *scriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *captureProvider) toolNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.lastDefs))
	for _, def := range p.lastDefs {
		names = append(names, def.Function.Name)
	}
	return names
}

func contains(name string, names []string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// mockTool is a trivial Tool used to verify that non-image tools survive the
// vision filtering in RunToolLoop.
type mockTool struct{}

func (mockTool) Name() string        { return "echo" }
func (mockTool) Description() string { return "echoes input" }
func (mockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (mockTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult {
	return &ToolResult{ForLLM: "ok"}
}

func TestRunToolLoop_FiltersReadImageWithoutVision(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(NewReadImageTool(t.TempDir(), false))
	registry.Register(mockTool{})

	provider := &captureProvider{}
	messages := []providers.Message{{Role: "user", Content: "hello"}}

	t.Run("filters read_image when vision unsupported", func(t *testing.T) {
		_, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:        provider,
			Model:           "test-model",
			Tools:           registry,
			MaxIterations:   1,
			VisionSupported: false,
		}, messages, "cli", "direct")
		if err != nil {
			t.Fatalf("RunToolLoop returned error: %v", err)
		}
		names := provider.toolNames()
		if contains("read_image", names) {
			t.Fatalf("read_image should be filtered out when VisionSupported=false, got tools: %v", names)
		}
		if !contains("echo", names) {
			t.Fatalf("non-image tools should be preserved, got tools: %v", names)
		}
	})

	t.Run("keeps read_image when vision supported", func(t *testing.T) {
		_, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:        provider,
			Model:           "test-model",
			Tools:           registry,
			MaxIterations:   1,
			VisionSupported: true,
		}, messages, "cli", "direct")
		if err != nil {
			t.Fatalf("RunToolLoop returned error: %v", err)
		}
		names := provider.toolNames()
		if !contains("read_image", names) {
			t.Fatalf("read_image should be present when VisionSupported=true, got tools: %v", names)
		}
	})

	t.Run("filters read_image by default (zero value)", func(t *testing.T) {
		_, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:      provider,
			Model:         "test-model",
			Tools:         registry,
			MaxIterations: 1,
		}, messages, "cli", "direct")
		if err != nil {
			t.Fatalf("RunToolLoop returned error: %v", err)
		}
		names := provider.toolNames()
		if contains("read_image", names) {
			t.Fatalf("read_image should be filtered out by default (zero value), got tools: %v", names)
		}
	})
}

func TestRunToolLoop_EmptyResponseRetries(t *testing.T) {
	p := &scriptedProvider{
		responses: []*providers.LLMResponse{
			{Content: ""},
			{Content: ""},
			{Content: "done"},
		},
	}

	// RetryWait never actually sleeps — it returns a channel that is
	// immediately ready, so the empty-response backoff adds no real delay.
	result, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      p,
		Model:         "test-model",
		MaxIterations: 0,
		RetryWait: func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	}, []providers.Message{{Role: "user", Content: "task"}}, "", "")

	if err != nil {
		t.Fatalf("RunToolLoop returned error: %v", err)
	}
	if result.Content != "done" {
		t.Fatalf("expected final content %q, got %q", "done", result.Content)
	}
	if strings.Contains(result.Content, "Maximum iterations reached") {
		t.Fatalf("content should not contain the max-iterations fallback, got %q", result.Content)
	}
	if got := p.callCount(); got != 3 {
		t.Fatalf("expected 3 provider calls (2 empty retries + 1 final), got %d", got)
	}
}

// ctxCaptureTool records the agent tool context visible during Execute.
// All methods use a pointer receiver: Execute mutates the struct, and mixing
// receiver kinds on one type is a lint error (recvcheck).
type ctxCaptureTool struct{ agentID, sessionKey string }

func (c *ctxCaptureTool) Name() string        { return "ctxprobe" }
func (c *ctxCaptureTool) Description() string { return "records tool context" }
func (c *ctxCaptureTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (c *ctxCaptureTool) Execute(ctx context.Context, _ map[string]interface{}) *ToolResult {
	c.agentID, c.sessionKey = AgentToolContextFromCtx(ctx)
	return &ToolResult{ForLLM: "ok"}
}

// toolCallProvider emits one tool call on the first turn, then a final answer.
type toolCallProvider struct {
	calls int
}

func (p *toolCallProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID:        "call-1",
			Name:      "ctxprobe",
			Arguments: map[string]interface{}{},
		}}}, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *toolCallProvider) GetDefaultModel() string { return "test-model" }

// TestRunToolLoop_InjectsOwnerContext verifies that a tool loop configured
// with an owner injects the agent tool context into tool executions, so
// nested spawns/background processes can be attributed to the owning session
// for cancellation (issue #230).
func TestRunToolLoop_InjectsOwnerContext(t *testing.T) {
	reg := NewToolRegistry()
	probe := &ctxCaptureTool{}
	reg.Register(probe)

	_, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:        &toolCallProvider{},
		Model:           "test-model",
		Tools:           reg,
		MaxIterations:   3,
		OwnerAgentID:    "main",
		OwnerSessionKey: "agent:main:native:uuid-1",
	}, []providers.Message{{Role: "user", Content: "go"}}, "", "")
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if probe.sessionKey != "agent:main:native:uuid-1" {
		t.Errorf("tool saw session key %q, want %q", probe.sessionKey, "agent:main:native:uuid-1")
	}
	if probe.agentID != "main" {
		t.Errorf("tool saw agent id %q, want %q", probe.agentID, "main")
	}
}

// TestRunToolLoop_InheritsOwnerFromContext verifies that without an explicit
// owner the loop keeps the context's owner (used by the sync subagent tool,
// whose caller context already carries it). An explicit owner wins.
func TestRunToolLoop_InheritsOwnerFromContext(t *testing.T) {
	reg := NewToolRegistry()
	probe := &ctxCaptureTool{}
	reg.Register(probe)

	ctx := WithAgentToolContext(context.Background(), "inherited-agent", "inherited-key")
	if _, err := RunToolLoop(ctx, ToolLoopConfig{
		Provider:      &toolCallProvider{},
		Model:         "test-model",
		Tools:         reg,
		MaxIterations: 3,
	}, []providers.Message{{Role: "user", Content: "go"}}, "", ""); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if probe.sessionKey != "inherited-key" {
		t.Errorf("tool saw session key %q, want inherited %q", probe.sessionKey, "inherited-key")
	}

	// Explicit owner overrides the inherited one.
	probe2 := &ctxCaptureTool{}
	reg2 := NewToolRegistry()
	reg2.Register(probe2)
	if _, err := RunToolLoop(ctx, ToolLoopConfig{
		Provider:        &toolCallProvider{},
		Model:           "test-model",
		Tools:           reg2,
		MaxIterations:   3,
		OwnerAgentID:    "explicit",
		OwnerSessionKey: "explicit-key",
	}, []providers.Message{{Role: "user", Content: "go"}}, "", ""); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if probe2.sessionKey != "explicit-key" || probe2.agentID != "explicit" {
		t.Errorf("explicit owner not honored: %q/%q", probe2.agentID, probe2.sessionKey)
	}
}

// unlimitedProvider returns a fixed response for every Chat call.
type unlimitedProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *unlimitedProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return &providers.LLMResponse{Content: ""}, nil
}

func (p *unlimitedProvider) GetDefaultModel() string { return "test-model" }

func (p *unlimitedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestRunToolLoop_EmptyResponsesNotPersisted verifies that blank assistant
// responses never reach the session recorder (they used to be saved before
// the empty-retry check, contaminating subagent sessions), while the task
// prompt and the eventual real response are.
func TestRunToolLoop_EmptyResponsesNotPersisted(t *testing.T) {
	p := &scriptedProvider{
		responses: []*providers.LLMResponse{
			{Content: "", ReasoningContent: "burned the whole budget"},
			{Content: "real answer"},
		},
	}
	rec := &recordingSessionRecorder{}

	result, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:        p,
		Model:           "test-model",
		MaxIterations:   0,
		SessionRecorder: rec,
		SessionKey:      "origin:task-1",
		RetryWait:       instantToolLoopWait,
	}, []providers.Message{{Role: "user", Content: "task"}}, "", "")
	if err != nil {
		t.Fatalf("RunToolLoop returned error: %v", err)
	}
	if result.Content != "real answer" {
		t.Fatalf("expected %q, got %q", "real answer", result.Content)
	}

	saved := rec.messages["origin:task-1"]
	if len(saved) != 2 {
		t.Fatalf("expected 2 persisted messages (user prompt + real answer), got %d: %+v", len(saved), saved)
	}
	if saved[0].Role != "user" || saved[0].Content != "task" {
		t.Errorf("unexpected first persisted message: %+v", saved[0])
	}
	if saved[1].Role != "assistant" || saved[1].Content != "real answer" {
		t.Errorf("unexpected second persisted message: %+v", saved[1])
	}
}

// TestRunToolLoop_EmptyResponsesBounded verifies that a provider that ALWAYS
// returns blank content cannot hang the subagent loop: the run ends after a
// bounded number of retries with a not_done status and persists no blanks.
func TestRunToolLoop_EmptyResponsesBounded(t *testing.T) {
	p := &unlimitedProvider{}
	rec := &recordingSessionRecorder{}

	result, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:        p,
		Model:           "test-model",
		MaxIterations:   0, // unlimited iterations — empty-retry cap is the guard
		SessionRecorder: rec,
		SessionKey:      "origin:task-2",
		RetryWait:       instantToolLoopWait,
	}, []providers.Message{{Role: "user", Content: "task"}}, "", "")
	if err != nil {
		t.Fatalf("RunToolLoop returned error: %v", err)
	}
	if !strings.Contains(result.Content, "STATUS: not_done") {
		t.Fatalf("expected not_done fallback, got %q", result.Content)
	}
	wantCalls := maxConsecutiveEmptyResponses + 1 // 1 initial + N retries
	if got := p.callCount(); got != wantCalls {
		t.Fatalf("expected %d provider calls, got %d", wantCalls, got)
	}
	for _, m := range rec.messages["origin:task-2"] {
		if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" {
			t.Fatalf("blank assistant message persisted: %+v", m)
		}
	}
}

// instantToolLoopWait returns an already-ready channel so empty-response
// backoffs add no real delay in tests.
func instantToolLoopWait(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Now()
	return ch
}

// identityProbeTool records the agent identity visible to the tool during
// Execute (issue #234). It is registered under the name "ctxprobe" so the
// existing toolCallProvider (which emits a call to that tool) can drive it.
type identityProbeTool struct {
	gotAgent string
	gotSess  string
}

func (t *identityProbeTool) Name() string        { return "ctxprobe" }
func (t *identityProbeTool) Description() string { return "records the acting agent identity" }
func (t *identityProbeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (t *identityProbeTool) Execute(ctx context.Context, _ map[string]interface{}) *ToolResult {
	t.gotAgent, t.gotSess = AgentToolContextFromCtx(ctx)
	return &ToolResult{ForLLM: "ok"}
}

// TestRunToolLoop_InjectsOwnerIdentityWithSubagentSession covers issue #234:
// a standalone tool loop (subagent tool, cron, cron spawn) must expose its
// owning agent identity to the tools it runs, otherwise identity-scoped tools
// such as the secret lookup (keyring.GetScoped) fail with no agent id.
func TestRunToolLoop_InjectsOwnerIdentityWithSubagentSession(t *testing.T) {
	reg := NewToolRegistry()
	probe := &identityProbeTool{}
	reg.Register(probe)

	if _, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:        &toolCallProvider{},
		Model:           "test-model",
		Tools:           reg,
		MaxIterations:   3,
		OwnerAgentID:    "planner",
		OwnerSessionKey: "subagent:test-234",
	}, []providers.Message{{Role: "user", Content: "go"}}, "", ""); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if probe.gotAgent != "planner" {
		t.Errorf("tool saw agent id %q, want %q", probe.gotAgent, "planner")
	}
	if probe.gotSess != "subagent:test-234" {
		t.Errorf("tool saw session key %q, want %q", probe.gotSess, "subagent:test-234")
	}
}

// TestRunToolLoop_InjectsOwnerIdentityWithoutSessionKey covers the cron case:
// the owning agent is known but no session key exists yet. The agent identity
// must still reach the tools, and it must not be wiped by context inheritance.
func TestRunToolLoop_InjectsOwnerIdentityWithoutSessionKey(t *testing.T) {
	reg := NewToolRegistry()
	probe := &identityProbeTool{}
	reg.Register(probe)

	if _, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      &toolCallProvider{},
		Model:         "test-model",
		Tools:         reg,
		MaxIterations: 3,
		OwnerAgentID:  "planner",
	}, []providers.Message{{Role: "user", Content: "go"}}, "", ""); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if probe.gotAgent != "planner" {
		t.Errorf("tool saw agent id %q, want %q", probe.gotAgent, "planner")
	}
	if probe.gotSess != "" {
		t.Errorf("tool saw session key %q, want empty", probe.gotSess)
	}
}
