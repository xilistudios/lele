package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/xilistudios/lele/pkg/providers"
)

type captureProvider struct {
	mu       sync.Mutex
	lastDefs []providers.ToolDefinition
}

func (p *captureProvider) Chat(_ context.Context, _ []providers.Message, tools []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.lastDefs = tools
	p.mu.Unlock()
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *captureProvider) GetDefaultModel() string {
	return "test-model"
}

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

type mockTool struct{}

func (mockTool) Name() string        { return "echo" }
func (mockTool) Description() string { return "echoes input" }
func (mockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (mockTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult {
	return &ToolResult{ForLLM: "ok"}
}

type namedMockTool struct{ toolName string }

func (m namedMockTool) Name() string        { return m.toolName }
func (m namedMockTool) Description() string { return "mock " + m.toolName }
func (namedMockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (namedMockTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult {
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

func TestRunToolLoop_EmptyResponsesBounded(t *testing.T) {
	p := &unlimitedProvider{}
	rec := &recordingSessionRecorder{}

	result, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:        p,
		Model:           "test-model",
		MaxIterations:   0,
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
	wantCalls := maxConsecutiveEmptyResponses + 1
	if got := p.callCount(); got != wantCalls {
		t.Fatalf("expected %d provider calls, got %d", wantCalls, got)
	}
	for _, m := range rec.messages["origin:task-2"] {
		if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" {
			t.Fatalf("blank assistant message persisted: %+v", m)
		}
	}
}

func instantToolLoopWait(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Now()
	return ch
}

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

func TestRunToolLoop_FiltersReadVideoWithoutVideo(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(namedMockTool{toolName: "read_video"})
	registry.Register(mockTool{})

	provider := &captureProvider{}
	messages := []providers.Message{{Role: "user", Content: "hello"}}

	t.Run("filters read_video when neither vision nor video", func(t *testing.T) {
		_, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:        provider,
			Model:           "test-model",
			Tools:           registry,
			MaxIterations:   1,
			VisionSupported: false,
			VideoSupported:  false,
		}, messages, "cli", "direct")
		if err != nil {
			t.Fatalf("RunToolLoop returned error: %v", err)
		}
		names := provider.toolNames()
		if contains("read_video", names) {
			t.Fatalf("read_video should be filtered out when VisionSupported=false and VideoSupported=false, got tools: %v", names)
		}
		if !contains("echo", names) {
			t.Fatalf("non-video tools should be preserved, got tools: %v", names)
		}
	})

	t.Run("keeps read_video when vision only (frames fallback)", func(t *testing.T) {
		_, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:        provider,
			Model:           "test-model",
			Tools:           registry,
			MaxIterations:   1,
			VisionSupported: true,
			VideoSupported:  false,
		}, messages, "cli", "direct")
		if err != nil {
			t.Fatalf("RunToolLoop returned error: %v", err)
		}
		names := provider.toolNames()
		if !contains("read_video", names) {
			t.Fatalf("read_video should be present when VisionSupported=true, got tools: %v", names)
		}
	})

	t.Run("keeps read_video when video supported", func(t *testing.T) {
		_, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:        provider,
			Model:           "test-model",
			Tools:           registry,
			MaxIterations:   1,
			VisionSupported: false,
			VideoSupported:  true,
		}, messages, "cli", "direct")
		if err != nil {
			t.Fatalf("RunToolLoop returned error: %v", err)
		}
		names := provider.toolNames()
		if !contains("read_video", names) {
			t.Fatalf("read_video should be present when VideoSupported=true, got tools: %v", names)
		}
	})

	t.Run("filters read_video by default (zero value)", func(t *testing.T) {
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
		if contains("read_video", names) {
			t.Fatalf("read_video should be filtered out by default (zero value), got tools: %v", names)
		}
	})
}

func TestEstimateLoopTokens_CountsImageParts(t *testing.T) {
	base64URL := "data:image/png;base64," + strings.Repeat("A", 200_000)
	msgs := []providers.Message{
		{
			Role:    "user",
			Content: "",
			ContentParts: []providers.ContentPart{
				{Type: "text", Text: "look"},
				{Type: "image_url", ImageURL: &providers.ImageURL{URL: base64URL}},
			},
		},
	}

	got := EstimateLoopTokens(msgs)
	if min := providers.ImagePartCharCost * 2 / 5; got < min {
		t.Fatalf("EstimateLoopTokens = %d, want >= %d (image part must be charged at its fixed cost)", got, min)
	}
	if limit := 20_000; got >= limit {
		t.Fatalf("EstimateLoopTokens = %d, want < %d (fixed image cost, not base64 byte length)", got, limit)
	}
}

func TestEstimateLoopTokens_CountsMedia(t *testing.T) {
	base := providers.Message{Role: "user", Content: "Please look at these files."}
	withMedia := base
	withMedia.Media = []string{"/a.png", "/b.png"}

	delta := EstimateLoopTokens([]providers.Message{withMedia}) -
		EstimateLoopTokens([]providers.Message{base})
	if min := 2 * providers.MediaPartCharCost * 2 / 5; delta < min {
		t.Fatalf("Media raised estimate by %d tokens, want >= %d", delta, min)
	}
}

func TestEstimateLoopTokens_CountsVideoParts(t *testing.T) {
	textMsg := providers.Message{
		Role: "user",
		ContentParts: []providers.ContentPart{
			{Type: "text", Text: "Describe this clip."},
		},
	}
	videoMsg := providers.Message{
		Role: "user",
		ContentParts: []providers.ContentPart{
			{Type: "text", Text: "Describe this clip."},
			{Type: "video_url", VideoURL: &providers.VideoURL{URL: "https://example.com/clip.mp4"}},
		},
	}

	delta := EstimateLoopTokens([]providers.Message{videoMsg}) -
		EstimateLoopTokens([]providers.Message{textMsg})
	if want := providers.VideoPartCharCost * 2 / 5; delta != want {
		t.Fatalf("video part raised estimate by %d tokens, want exactly %d", delta, want)
	}
}

func TestEstimateLoopTokens_TextOnlyUnchanged(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Inspect the widget."},
		{
			Role:    "assistant",
			Content: "Reading the file now.",
			ToolCalls: []providers.ToolCall{{
				ID:       "call_1",
				Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"/tmp/a.txt"}`},
			}},
		},
		{Role: "tool", Content: "hello world", ToolCallID: "call_1"},
	}

	oldChars := 0
	for _, m := range msgs {
		oldChars += utf8.RuneCountInString(m.Content)
		oldChars += utf8.RuneCountInString(m.ReasoningContent)
		for _, tc := range m.ToolCalls {
			if tc.Function != nil {
				oldChars += utf8.RuneCountInString(tc.Function.Arguments)
			}
		}
	}
	nameChars := 0
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if tc.Function != nil {
				nameChars += utf8.RuneCountInString(tc.Function.Name)
			}
		}
	}
	structChars := 10 * len(msgs)
	wantChars := oldChars + nameChars + structChars

	got := EstimateLoopTokens(msgs)
	if want := wantChars * 2 / 5; got != want {
		t.Errorf("EstimateLoopTokens = %d, want %d (old rule %d + tool-call names %d + structural %d chars)",
			got, want, oldChars, nameChars, structChars)
	}
	if want := (wantChars - oldChars) * 2 / 5; want == 0 {
		t.Fatalf("test fixture too small: expected a nonzero documented delta, got 0")
	}
}
