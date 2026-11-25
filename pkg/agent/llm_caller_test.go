package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/catalog"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

func TestCallWithFallback_StripsImagesForNonVisionCandidate(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	al.cfg().Providers = &config.ProvidersConfig{
		Named: map[string]config.NamedProviderConfig{
			"prov-a": {
				Type: "openai",
				Models: map[string]config.ProviderModelConfig{
					"vision-model": {Vision: true, Video: true},
				},
			},
			"prov-b": {
				Type: "openai",
				Models: map[string]config.ProviderModelConfig{
					"text-model": {Vision: false},
				},
			},
		},
	}

	al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker()).
		WithRetryConfig(1, time.Millisecond)

	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	var receivedMessages [][]providers.Message
	callCount := 0

	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			msgCopy := make([]providers.Message, len(messages))
			copy(msgCopy, messages)
			receivedMessages = append(receivedMessages, msgCopy)

			if callCount == 1 {
				return nil, fmt.Errorf("status: 500 internal server error")
			}
			return &providers.LLMResponse{Content: "fallback response"}, nil
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Describe this image"},
		{Role: "user", ContentParts: []providers.ContentPart{
			{Type: "text", Text: "Here is the image"},
			{Type: "image_url", ImageURL: &providers.ImageURL{URL: "data:image/png;base64,abcd", Detail: "auto"}},
			{Type: "video_url", VideoURL: &providers.VideoURL{URL: "https://example.com/clip.mp4", FPS: 1}},
		}},
	}

	caller := newLLMCaller(al)
	opts := llmCallOptions{
		ctx:      context.Background(),
		agent:    agent,
		messages: messages,
		toolDefs: []providers.ToolDefinition{},
		model:    "prov-a:vision-model",
		candidates: []providers.FallbackCandidate{
			{Provider: "prov-a", Model: "vision-model"},
			{Provider: "prov-b", Model: "text-model"},
		},
		sessionKey: "test-session",
	}

	resp, err := caller.call(opts)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if resp.Content != "fallback response" {
		t.Errorf("Expected 'fallback response', got: %s", resp.Content)
	}

	if callCount != 2 {
		t.Fatalf("Expected 2 calls, got %d", callCount)
	}

	firstCallHasImage := false
	firstCallHasVideo := false
	for _, msg := range receivedMessages[0] {
		for _, part := range msg.ContentParts {
			if part.Type == "image_url" {
				firstCallHasImage = true
			}
			if part.Type == "video_url" {
				firstCallHasVideo = true
			}
		}
	}
	if !firstCallHasImage {
		t.Error("Expected first call (vision model) to receive image_url content parts")
	}
	if !firstCallHasVideo {
		t.Error("Expected first call (video-capable model) to receive video_url content parts")
	}

	for _, msg := range receivedMessages[1] {
		for _, part := range msg.ContentParts {
			if part.Type == "image_url" {
				t.Fatal("Expected second call (non-vision model) to have image_url content parts stripped")
			}
			if part.Type == "video_url" {
				t.Fatal("Expected second call (non-video model) to have video_url content parts stripped")
			}
		}
	}
}

func TestCallWithFallback_PreservesImagesForVisionCandidate(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	al.cfg().Providers = &config.ProvidersConfig{
		Named: map[string]config.NamedProviderConfig{
			"prov-a": {
				Type: "openai",
				Models: map[string]config.ProviderModelConfig{
					"vision-model": {Vision: true, Video: true},
				},
			},
		},
	}

	al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker()).
		WithRetryConfig(1, time.Millisecond)

	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	var receivedMessages []providers.Message
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			receivedMessages = messages
			return &providers.LLMResponse{Content: "ok"}, nil
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", ContentParts: []providers.ContentPart{
			{Type: "text", Text: "Analyze"},
			{Type: "image_url", ImageURL: &providers.ImageURL{URL: "data:image/png;base64,xyz", Detail: "auto"}},
			{Type: "video_url", VideoURL: &providers.VideoURL{URL: "https://example.com/clip.mp4", FPS: 1}},
		}},
	}

	caller := newLLMCaller(al)
	opts := llmCallOptions{
		ctx:      context.Background(),
		agent:    agent,
		messages: messages,
		toolDefs: []providers.ToolDefinition{},
		model:    "prov-a:vision-model",
		candidates: []providers.FallbackCandidate{
			{Provider: "prov-a", Model: "vision-model"},
		},
		sessionKey: "test-session",
	}

	_, err := caller.call(opts)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	hasImage := false
	hasVideo := false
	for _, msg := range receivedMessages {
		for _, part := range msg.ContentParts {
			if part.Type == "image_url" {
				hasImage = true
			}
			if part.Type == "video_url" {
				hasVideo = true
			}
		}
	}
	if !hasImage {
		t.Error("Expected vision model to receive image_url content parts")
	}
	if !hasVideo {
		t.Error("Expected video-capable model to receive video_url content parts")
	}
}

func TestRunLLMIteration_ReadImageExposedWhenPrimaryHasVision_FallbackDoesNot(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	al.cfg().Providers = &config.ProvidersConfig{
		Named: map[string]config.NamedProviderConfig{
			"test-provider": {
				Type: "openai",
				Models: map[string]config.ProviderModelConfig{
					"test-model":    {Vision: true},
					"fallback-text": {Vision: false},
				},
			},
		},
	}

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	agent.Candidates = []providers.FallbackCandidate{
		{Provider: "test-provider", Model: "test-model"},
		{Provider: "test-provider", Model: "fallback-text"},
	}

	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "read_image",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult("image loaded")
		},
	})

	var receivedToolDefs []providers.ToolDefinition
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			receivedToolDefs = toolDefs
			return &providers.LLMResponse{Content: "done"}, nil
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Hello"},
	}

	opts := processOptions{
		SessionKey: "test-session",
	}

	_, _, err := runner.runLLMIteration(context.Background(), agent, messages, opts)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	found := false
	for _, def := range receivedToolDefs {
		if def.Function.Name == "read_image" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected read_image tool to be exposed when primary model has vision, even if fallback does not")
	}
}

func TestRunLLMIteration_ReadImageHiddenWhenPrimaryLacksVision(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	al.cfg().Providers = &config.ProvidersConfig{
		Named: map[string]config.NamedProviderConfig{
			"test-provider": {
				Type: "openai",
				Models: map[string]config.ProviderModelConfig{
					"test-model": {Vision: false},
				},
			},
		},
	}

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "read_image",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult("image loaded")
		},
	})

	var receivedToolDefs []providers.ToolDefinition
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			receivedToolDefs = toolDefs
			return &providers.LLMResponse{Content: "done"}, nil
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Hello"},
	}

	opts := processOptions{
		SessionKey: "test-session",
	}

	_, _, err := runner.runLLMIteration(context.Background(), agent, messages, opts)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	for _, def := range receivedToolDefs {
		if def.Function.Name == "read_image" {
			t.Fatal("Expected read_image tool to be hidden when primary model does not support vision")
		}
	}
}

func strPtr(s string) *string { return &s }

func TestBuildLLMOptions_Reasoning(t *testing.T) {
	maxTok := 2048

	tests := []struct {
		name           string
		sessionLevel   string
		persistedLevel string
		agentLevel     string
		reasoning      *config.ReasoningConfig
		model          string

		wantReasoning       map[string]interface{}
		wantReasoningAbsent bool
		wantThinking        interface{}
		wantThinkingAbsent  bool
	}{
		{
			name:          "off without agent config emits explicit disable",
			sessionLevel:  "off",
			reasoning:     nil,
			model:         "test-model",
			wantReasoning: map[string]interface{}{"enabled": false},
		},
		{
			name:          "off does not leak config effort",
			sessionLevel:  "off",
			reasoning:     &config.ReasoningConfig{Enable: true, Effort: strPtr("high")},
			model:         "test-model",
			wantReasoning: map[string]interface{}{"enabled": false},
		},
		{
			name:               "off suppresses DeepSeek thinking adapter",
			sessionLevel:       "off",
			reasoning:          &config.ReasoningConfig{Enable: true},
			model:              "deepseek-chat",
			wantReasoning:      map[string]interface{}{"enabled": false},
			wantThinkingAbsent: true,
		},

		{
			name:                "no override and no config emits no reasoning",
			sessionLevel:        "",
			reasoning:           nil,
			model:               "test-model",
			wantReasoningAbsent: true,
		},
		{
			name:          "no override keeps config default path",
			sessionLevel:  "",
			reasoning:     &config.ReasoningConfig{Enable: true, Effort: strPtr("medium")},
			model:         "test-model",
			wantReasoning: map[string]interface{}{"enabled": true, "effort": "medium"},
		},
		{
			name:          "no override keeps DeepSeek thinking adapter",
			sessionLevel:  "",
			reasoning:     &config.ReasoningConfig{Enable: true},
			model:         "deepseek-chat",
			wantReasoning: map[string]interface{}{"enabled": true},
			wantThinking:  true,
		},

		{
			name:          "low without config still emits enabled true",
			sessionLevel:  "low",
			reasoning:     nil,
			model:         "test-model",
			wantReasoning: map[string]interface{}{"effort": "low", "enabled": true},
		},
		{
			name:         "low merges config max_tokens",
			sessionLevel: "low",
			reasoning:    &config.ReasoningConfig{Enable: true, MaxTokens: &maxTok},
			model:        "test-model",
			wantReasoning: map[string]interface{}{
				"effort":     "low",
				"enabled":    true,
				"max_tokens": maxTok,
			},
		},
		{
			name:          "high overrides config enable false",
			sessionLevel:  "high",
			reasoning:     &config.ReasoningConfig{Enable: false},
			model:         "test-model",
			wantReasoning: map[string]interface{}{"effort": "high", "enabled": true},
		},

		{
			name:           "persisted off emits explicit disable",
			persistedLevel: "off",
			reasoning:      nil,
			model:          "test-model",
			wantReasoning:  map[string]interface{}{"enabled": false},
		},

		{
			name:          "agent medium without session override emits effort",
			agentLevel:    "medium",
			reasoning:     nil,
			model:         "test-model",
			wantReasoning: map[string]interface{}{"effort": "medium", "enabled": true},
		},
		{
			name:               "agent off emits explicit disable even with model reasoning enabled",
			agentLevel:         "off",
			reasoning:          &config.ReasoningConfig{Enable: true, Effort: strPtr("high")},
			model:              "deepseek-chat",
			wantReasoning:      map[string]interface{}{"enabled": false},
			wantThinkingAbsent: true,
		},
		{
			name:          "session off beats agent high",
			sessionLevel:  "off",
			agentLevel:    "high",
			reasoning:     nil,
			model:         "test-model",
			wantReasoning: map[string]interface{}{"enabled": false},
		},
		{
			name:          "session low beats agent high",
			sessionLevel:  "low",
			agentLevel:    "high",
			reasoning:     nil,
			model:         "test-model",
			wantReasoning: map[string]interface{}{"effort": "low", "enabled": true},
		},
		{
			name:          "session xhigh passes through",
			sessionLevel:  "xhigh",
			agentLevel:    "high",
			reasoning:     nil,
			model:         "test-model",
			wantReasoning: map[string]interface{}{"effort": "xhigh", "enabled": true},
		},
		{
			name:          "agent max passes through",
			agentLevel:    "max",
			reasoning:     nil,
			model:         "test-model",
			wantReasoning: map[string]interface{}{"effort": "max", "enabled": true},
		},
		{
			name:          "agent level unset keeps legacy config path",
			agentLevel:    "",
			reasoning:     &config.ReasoningConfig{Enable: true, Effort: strPtr("high")},
			model:         "test-model",
			wantReasoning: map[string]interface{}{"effort": "high", "enabled": true},
		},
		{
			name:          "agent invalid level falls back to legacy config path",
			agentLevel:    resolveAgentThinkingLevel(&config.AgentConfig{ThinkingLevel: strPtr("ultra")}, &config.AgentDefaults{}),
			reasoning:     &config.ReasoningConfig{Enable: true, Effort: strPtr("high")},
			model:         "test-model",
			wantReasoning: map[string]interface{}{"effort": "high", "enabled": true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			al, tmpDir := createLLMRunnerTestAgentLoop(t)
			defer os.RemoveAll(tmpDir)
			agent := createLLMRunnerTestAgentInstance(t, tmpDir)
			agent.Reasoning = tc.reasoning
			agent.ThinkingLevel = tc.agentLevel

			al.registry.mu.Lock()
			al.registry.agents["main"] = agent
			al.registry.mu.Unlock()

			const sessionKey = "test-session"
			switch {
			case tc.sessionLevel != "":
				al.sessionThinking.Store(sessionKey, tc.sessionLevel)
			case tc.persistedLevel != "":
				if err := agent.Sessions.SetThinkingLevel(sessionKey, tc.persistedLevel); err != nil {
					t.Fatalf("SetThinkingLevel: %v", err)
				}
			}

			caller := newLLMCaller(al)
			opts := llmCallOptions{
				ctx:        context.Background(),
				agent:      agent,
				messages:   []providers.Message{{Role: "user", Content: "hi"}},
				model:      tc.model,
				sessionKey: sessionKey,
			}

			got := caller.buildLLMOptions(opts)

			reasoningRaw, hasReasoning := got["reasoning"]
			if tc.wantReasoningAbsent {
				if hasReasoning {
					t.Fatalf("reasoning key present, want absent: %v", reasoningRaw)
				}
			} else {
				if !hasReasoning {
					t.Fatalf("reasoning key absent, want %v", tc.wantReasoning)
				}
				reasoning, ok := reasoningRaw.(map[string]interface{})
				if !ok {
					t.Fatalf("reasoning is %T, want map[string]interface{}", reasoningRaw)
				}
				if len(reasoning) != len(tc.wantReasoning) {
					t.Fatalf("reasoning = %v (len %d), want %v (len %d)",
						reasoning, len(reasoning), tc.wantReasoning, len(tc.wantReasoning))
				}
				for k, want := range tc.wantReasoning {
					gv, ok := reasoning[k]
					if !ok {
						t.Fatalf("reasoning missing key %q (got %v)", k, reasoning)
					}
					if !reflect.DeepEqual(gv, want) {
						t.Errorf("reasoning[%q] = %#v, want %#v", k, gv, want)
					}
				}
			}

			thinkingRaw, hasThinking := got["thinking"]
			switch {
			case tc.wantThinkingAbsent:
				if hasThinking {
					t.Fatalf("thinking key present (%v), want absent", thinkingRaw)
				}
			case tc.wantThinking != nil:
				if !hasThinking {
					t.Fatalf("thinking key absent, want %v", tc.wantThinking)
				}
				if !reflect.DeepEqual(thinkingRaw, tc.wantThinking) {
					t.Errorf("thinking = %#v, want %#v", thinkingRaw, tc.wantThinking)
				}
			default:
				if hasThinking {
					t.Errorf("unexpected thinking key: %#v", thinkingRaw)
				}
			}
		})
	}
}

func TestBuildLLMOptions_StampsWireSessionID(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	lc := newLLMCaller(al)

	base := "native:951a0f6e-8f6e-4f2e"
	sessionKey := base + ":chat:3"
	opts := llmCallOptions{
		agent:      agent,
		model:      "test-model",
		sessionKey: sessionKey,
	}

	want := providers.WireSessionID(sessionKey)
	if want == "" {
		t.Fatal("test setup: WireSessionID must derive a value for a non-empty key")
	}

	got := lc.buildLLMOptions(opts)
	stamped, ok := got[providers.OptWireSessionID]
	if !ok {
		t.Fatalf("buildLLMOptions did not stamp %s (options: %#v)", providers.OptWireSessionID, got)
	}
	if stamped != want {
		t.Fatalf("%s = %v (%T), want %q", providers.OptWireSessionID, stamped, stamped, want)
	}

	again := lc.buildLLMOptions(opts)
	if again[providers.OptWireSessionID] != want {
		t.Fatalf("second turn stamped %v, want the same %q", again[providers.OptWireSessionID], want)
	}

	rotated := al.nextConversationSessionKey(base)
	if rotated == sessionKey {
		t.Fatal("test setup: rotation must yield a different session key")
	}
	opts.sessionKey = rotated
	gotRotated := lc.buildLLMOptions(opts)
	if gotRotated[providers.OptWireSessionID] == want {
		t.Fatalf("rotated session key %q still stamped %q; the gateway could not tell the conversations apart", rotated, want)
	}
	if gotRotated[providers.OptWireSessionID] != providers.WireSessionID(rotated) {
		t.Fatalf("rotated stamp = %v, want %q", gotRotated[providers.OptWireSessionID], providers.WireSessionID(rotated))
	}
}

func TestBuildLLMOptions_StampsUnkeyedBucketWithoutSessionKey(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	lc := newLLMCaller(al)

	got := lc.buildLLMOptions(llmCallOptions{
		agent:      agent,
		model:      "test-model",
		sessionKey: "",
	})
	v, ok := got[providers.OptWireSessionID]
	if !ok {
		t.Fatalf("%s absent with no session key: buildLLMOptions must always stamp (%#v)", providers.OptWireSessionID, got)
	}
	if want := providers.UnkeyedSessionBucket; v != want {
		t.Fatalf("%s = %v (%T), want the unkeyed bucket %q", providers.OptWireSessionID, v, v, want)
	}
}

func loadCatalogFixture(t *testing.T, providerID string) catalog.Provider {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "catalog", "providers", providerID+".json"))
	if err != nil {
		t.Fatalf("read catalog fixture %q: %v", providerID, err)
	}
	var provider catalog.Provider
	if err := json.Unmarshal(raw, &provider); err != nil {
		t.Fatalf("parse catalog fixture %q: %v", providerID, err)
	}
	if len(provider.Models) == 0 {
		t.Fatalf("catalog fixture %q has no models", providerID)
	}
	return provider
}

func seedCatalog(t *testing.T, providers ...catalog.Provider) {
	t.Helper()
	catalog.SetCacheDir(t.TempDir())
	catalog.ResetLive()
	t.Cleanup(func() {
		catalog.ResetLive()
		catalog.ResetCacheDir()
	})
	for _, provider := range providers {
		if err := catalog.SeedProvider(provider); err != nil {
			t.Fatalf("seed catalog provider %q: %v", provider.ID, err)
		}
	}
}

func captureThinkingClampWarnings(t *testing.T) *[]map[string]interface{} {
	t.Helper()
	var warnings []map[string]interface{}
	previous := warnThinkingClamp
	warnThinkingClamp = func(component string, message string, fields map[string]interface{}) {
		warnings = append(warnings, fields)
	}
	t.Cleanup(func() { warnThinkingClamp = previous })
	return &warnings
}

func TestClampEffortToCatalog(t *testing.T) {
	anthropic := loadCatalogFixture(t, "anthropic")
	var sonnet catalog.Model
	ok := false
	for _, m := range anthropic.Models {
		if m.ID == "claude-sonnet-4-6" {
			sonnet, ok = m, true
		}
	}
	if !ok {
		t.Fatal("catalog fixture anthropic is missing claude-sonnet-4-6")
	}
	if !reflect.DeepEqual(sonnet.ThinkingLevels, []string{"low", "medium", "high"}) {
		t.Fatalf("clamp fixtures assume claude-sonnet-4-6 thinking_levels [low medium high], got %v", sonnet.ThinkingLevels)
	}

	levelLess := catalog.Provider{
		ID:     "level-less",
		Name:   "Level-less",
		Models: []catalog.Model{{ID: "plain-model", Name: "Plain", Reasoning: true, ToolCall: true}},
	}
	highOnly := catalog.Provider{
		ID:     "high-only",
		Name:   "High only",
		Models: []catalog.Model{{ID: "only-high", Name: "Only High", ThinkingLevels: []string{"high"}}},
	}
	seedCatalog(t, anthropic, levelLess, highOnly)

	tests := []struct {
		name     string
		effort   string
		provider string
		model    string
		want     string
		wantWarn bool
	}{
		{name: "xhigh clamps to high", effort: "xhigh", provider: "anthropic", model: "claude-sonnet-4-6", want: "high", wantWarn: true},
		{name: "max clamps to high", effort: "max", provider: "anthropic", model: "claude-sonnet-4-6", want: "high", wantWarn: true},
		{name: "supported level is unchanged", effort: "high", provider: "anthropic", model: "claude-sonnet-4-6", want: "high"},
		{name: "lowest supported level is unchanged", effort: "low", provider: "anthropic", model: "claude-sonnet-4-6", want: "low"},
		{name: "second catalog model clamps", effort: "xhigh", provider: "anthropic", model: "claude-opus-4-5", want: "high", wantWarn: true},
		{name: "unknown model passes through", effort: "xhigh", provider: "anthropic", model: "claude-not-in-catalog", want: "xhigh"},
		{name: "unknown provider passes through", effort: "xhigh", provider: "nope", model: "claude-sonnet-4-6", want: "xhigh"},
		{name: "empty thinking levels pass through", effort: "xhigh", provider: "level-less", model: "plain-model", want: "xhigh"},
		{name: "off stays off", effort: "off", provider: "anthropic", model: "claude-sonnet-4-6", want: "off"},
		{name: "empty effort stays empty", effort: "", provider: "anthropic", model: "claude-sonnet-4-6", want: ""},
		{name: "below every supported level picks the lowest", effort: "low", provider: "high-only", model: "only-high", want: "high", wantWarn: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warnings := captureThinkingClampWarnings(t)

			if got := clampEffortToCatalog(tc.effort, tc.provider, tc.model); got != tc.want {
				t.Fatalf("clampEffortToCatalog(%q, %q, %q) = %q, want %q", tc.effort, tc.provider, tc.model, got, tc.want)
			}

			switch {
			case tc.wantWarn:
				if len(*warnings) != 1 {
					t.Fatalf("clamp warnings = %d, want exactly 1 (%v)", len(*warnings), *warnings)
				}
				fields := (*warnings)[0]
				if fields["requested"] != tc.effort {
					t.Errorf("warning requested = %v, want %q", fields["requested"], tc.effort)
				}
				if fields["clamped"] != tc.want {
					t.Errorf("warning clamped = %v, want %q", fields["clamped"], tc.want)
				}
				if fields["model"] != tc.model {
					t.Errorf("warning model = %v, want %q", fields["model"], tc.model)
				}
			case len(*warnings) != 0:
				t.Fatalf("clamp warnings = %v, want none", *warnings)
			}
		})
	}
}

func TestBuildLLMOptions_ClampsEffortToModelCatalog(t *testing.T) {
	seedCatalog(t, loadCatalogFixture(t, "anthropic"))

	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	al.registry.mu.Lock()
	al.registry.agents["main"] = agent
	al.registry.mu.Unlock()

	const sessionKey = "clamp-session"
	al.sessionThinking.Store(sessionKey, "xhigh")

	warnings := captureThinkingClampWarnings(t)

	caller := newLLMCaller(al)
	got := caller.buildLLMOptions(llmCallOptions{
		ctx:        context.Background(),
		agent:      agent,
		messages:   []providers.Message{{Role: "user", Content: "hi"}},
		model:      "anthropic:claude-sonnet-4-6",
		sessionKey: sessionKey,
	})

	reasoning, ok := got["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatalf("reasoning = %#v, want a map", got["reasoning"])
	}
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning effort = %v, want the catalog-clamped %q", reasoning["effort"], "high")
	}
	if reasoning["enabled"] != true {
		t.Fatalf("reasoning enabled = %v, want true", reasoning["enabled"])
	}
	if len(*warnings) != 1 {
		t.Fatalf("clamp warnings = %d, want exactly 1 (%v)", len(*warnings), *warnings)
	}

	raw, stillStored := al.sessionThinking.Load(sessionKey)
	if !stillStored || raw != "xhigh" {
		t.Fatalf("session thinking level = %v, want the untouched %q", raw, "xhigh")
	}
}

func TestBuildLLMOptions_ClampLeavesPersistedSessionLevelUntouched(t *testing.T) {
	seedCatalog(t, loadCatalogFixture(t, "anthropic"))

	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	al.registry.mu.Lock()
	al.registry.agents["main"] = agent
	al.registry.mu.Unlock()

	const sessionKey = "persisted-clamp-session"
	if err := agent.Sessions.SetThinkingLevel(sessionKey, "xhigh"); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}

	warnings := captureThinkingClampWarnings(t)

	caller := newLLMCaller(al)
	got := caller.buildLLMOptions(llmCallOptions{
		ctx:        context.Background(),
		agent:      agent,
		messages:   []providers.Message{{Role: "user", Content: "hi"}},
		model:      "anthropic:claude-sonnet-4-6",
		sessionKey: sessionKey,
	})

	reasoning, ok := got["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatalf("reasoning = %#v, want a map", got["reasoning"])
	}
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning effort = %v, want the catalog-clamped %q", reasoning["effort"], "high")
	}
	if len(*warnings) != 1 {
		t.Fatalf("clamp warnings = %d, want exactly 1 (%v)", len(*warnings), *warnings)
	}
	if stored := agent.Sessions.GetThinkingLevel(sessionKey); stored != "xhigh" {
		t.Fatalf("persisted thinking level = %q, want the untouched %q", stored, "xhigh")
	}
}

func TestBuildLLMOptions_ClampsLegacyReasoningEffort(t *testing.T) {
	seedCatalog(t, loadCatalogFixture(t, "anthropic"))

	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Reasoning = &config.ReasoningConfig{Enable: true, Effort: strPtr("xhigh")}
	al.registry.mu.Lock()
	al.registry.agents["main"] = agent
	al.registry.mu.Unlock()

	warnings := captureThinkingClampWarnings(t)

	caller := newLLMCaller(al)
	got := caller.buildLLMOptions(llmCallOptions{
		ctx:      context.Background(),
		agent:    agent,
		messages: []providers.Message{{Role: "user", Content: "hi"}},
		model:    "anthropic:claude-sonnet-4-6",
	})

	reasoning, ok := got["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatalf("reasoning = %#v, want a map", got["reasoning"])
	}
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning effort = %v, want the catalog-clamped %q", reasoning["effort"], "high")
	}
	if len(*warnings) != 1 {
		t.Fatalf("clamp warnings = %d, want exactly 1 (%v)", len(*warnings), *warnings)
	}
}
