// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

// TestCallWithFallback_StripsImagesForNonVisionCandidate verifies that when
// the fallback chain fails over to a non-vision model, image_url content
// parts are stripped from messages before being sent to that candidate.
func TestCallWithFallback_StripsImagesForNonVisionCandidate(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	// Configure two providers: prov-a (vision) and prov-b (non-vision).
	// Neither has an API key, so CreateProviderForCandidate will fail and
	// the mock agent.Provider will be used instead.
	al.cfg().Providers = &config.ProvidersConfig{
		Named: map[string]config.NamedProviderConfig{
			"prov-a": {
				Type: "openai",
				Models: map[string]config.ProviderModelConfig{
					"vision-model": {Vision: true},
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

	// Use a fallback chain with minimal retries so the test is fast.
	al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker()).
		WithRetryConfig(1, time.Millisecond)

	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	// Track messages received by each call.
	var receivedMessages [][]providers.Message
	callCount := 0

	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			// Capture a copy of messages for later inspection.
			msgCopy := make([]providers.Message, len(messages))
			copy(msgCopy, messages)
			receivedMessages = append(receivedMessages, msgCopy)

			if callCount == 1 {
				// First candidate (prov-a/vision-model) fails with a
				// retriable error to trigger fallback.
				return nil, fmt.Errorf("status: 500 internal server error")
			}
			// Second candidate (prov-b/text-model) succeeds.
			return &providers.LLMResponse{Content: "fallback response"}, nil
		},
	}

	// Messages containing image content (simulates a read_image result).
	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Describe this image"},
		{Role: "user", ContentParts: []providers.ContentPart{
			{Type: "text", Text: "Here is the image"},
			{Type: "image_url", ImageURL: &providers.ImageURL{URL: "data:image/png;base64,abcd", Detail: "auto"}},
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

	// Should have been called twice (once per candidate).
	if callCount != 2 {
		t.Fatalf("Expected 2 calls, got %d", callCount)
	}

	// First call (vision model) should have image_url parts.
	firstCallHasImage := false
	for _, msg := range receivedMessages[0] {
		for _, part := range msg.ContentParts {
			if part.Type == "image_url" {
				firstCallHasImage = true
			}
		}
	}
	if !firstCallHasImage {
		t.Error("Expected first call (vision model) to receive image_url content parts")
	}

	// Second call (non-vision model) should NOT have image_url parts.
	for _, msg := range receivedMessages[1] {
		for _, part := range msg.ContentParts {
			if part.Type == "image_url" {
				t.Fatal("Expected second call (non-vision model) to have image_url content parts stripped")
			}
		}
	}
}

// TestCallWithFallback_PreservesImagesForVisionCandidate verifies that when
// the fallback chain uses a vision-capable model, image_url content parts
// are preserved.
func TestCallWithFallback_PreservesImagesForVisionCandidate(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	al.cfg().Providers = &config.ProvidersConfig{
		Named: map[string]config.NamedProviderConfig{
			"prov-a": {
				Type: "openai",
				Models: map[string]config.ProviderModelConfig{
					"vision-model": {Vision: true},
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

	// Vision model should receive image_url parts.
	hasImage := false
	for _, msg := range receivedMessages {
		for _, part := range msg.ContentParts {
			if part.Type == "image_url" {
				hasImage = true
			}
		}
	}
	if !hasImage {
		t.Error("Expected vision model to receive image_url content parts")
	}
}

// TestRunLLMIteration_ReadImageExposedWhenPrimaryHasVision_FallbackDoesNot
// verifies that read_image is exposed to the LLM when the primary model
// supports vision, even if a fallback model in the chain does not.
func TestRunLLMIteration_ReadImageExposedWhenPrimaryHasVision_FallbackDoesNot(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	// Primary model has vision; fallback does not.
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

	// Set up candidates: primary (vision) + fallback (non-vision).
	agent.Candidates = []providers.FallbackCandidate{
		{Provider: "test-provider", Model: "test-model"},
		{Provider: "test-provider", Model: "fallback-text"},
	}

	// Register read_image tool.
	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "read_image",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult("image loaded")
		},
	})

	// Capture tool definitions sent to the LLM.
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

	// read_image should be present because the primary model has vision.
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

// TestRunLLMIteration_ReadImageHiddenWhenPrimaryLacksVision verifies that
// read_image is hidden when the primary (session) model does not support
// vision, regardless of fallback configuration.
func TestRunLLMIteration_ReadImageHiddenWhenPrimaryLacksVision(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	// Primary model does NOT have vision.
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

	// Register read_image tool.
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

	// read_image should NOT be present because the primary model lacks vision.
	for _, def := range receivedToolDefs {
		if def.Function.Name == "read_image" {
			t.Fatal("Expected read_image tool to be hidden when primary model does not support vision")
		}
	}
}

// ============================================================================
// buildLLMOptions — reasoning / thinking option resolution
// ============================================================================
//
// These tests lock the semantics of the per-session thinking override
// (`/think off|low|medium|high`) against the agent-level reasoning config.
//
// The bug they guard against: `/think off` used to emit NO `reasoning` key at
// all. A transparent proxy (llmproxy) then applied its own server-side default
// — thinking ON — so disabling reasoning was indistinguishable from enabling
// it. The fix makes "off" send an explicit `{"enabled": false}` (same payload
// the goal judge ships in production, see goal.go).

func strPtr(s string) *string { return &s }

func TestBuildLLMOptions_Reasoning(t *testing.T) {
	// maxTok is referenced by the low-with-config merge case.
	maxTok := 2048

	tests := []struct {
		name string
		// sessionLevel is the in-memory per-session override stored in
		// al.sessionThinking ("" means no override).
		sessionLevel string
		// persistedLevel, when non-empty, is written to the session manager
		// instead of the in-memory map to exercise the persisted fallback path
		// (the path that survives a gateway restart).
		persistedLevel string
		reasoning      *config.ReasoningConfig
		model          string

		// wantReasoning, when non-nil, is compared against the whole
		// `reasoning` map (exact key set and values).
		wantReasoning map[string]interface{}
		// wantReasoningAbsent asserts the `reasoning` key is not emitted.
		wantReasoningAbsent bool
		// wantThinking, when non-nil, is compared against `thinking`.
		wantThinking interface{}
		// wantThinkingAbsent asserts the DeepSeek `thinking` key is not emitted.
		wantThinkingAbsent bool
	}{
		// --- off: explicit disable, unconditional -------------------------
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

		// --- no override: config path unchanged ---------------------------
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
			name:         "no override keeps DeepSeek thinking adapter",
			sessionLevel: "",
			reasoning:    &config.ReasoningConfig{Enable: true},
			model:        "deepseek-chat",
			// Config path emits enabled:true (no effort configured)...
			wantReasoning: map[string]interface{}{"enabled": true},
			// ...plus the DeepSeek wire adapter.
			wantThinking: true,
		},

		// --- session override low/medium/high -----------------------------
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

		// --- persisted fallback (survives restarts) -----------------------
		{
			name:           "persisted off emits explicit disable",
			persistedLevel: "off",
			reasoning:      nil,
			model:          "test-model",
			wantReasoning:  map[string]interface{}{"enabled": false},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			al, tmpDir := createLLMRunnerTestAgentLoop(t)
			defer os.RemoveAll(tmpDir)
			agent := createLLMRunnerTestAgentInstance(t, tmpDir)
			agent.Reasoning = tc.reasoning

			// buildLLMOptions resolves the override through the loop's
			// registry, so the agent under test must be the one the loop
			// returns for the session key.
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

			// reasoning map
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

			// thinking key (DeepSeek wire adapter)
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
