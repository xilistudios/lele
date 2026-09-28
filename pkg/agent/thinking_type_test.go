// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// thinkingTypeFixture wires a config with a single provider-model entry that
// carries a thinking_type, plus the usual test agent loop/instance pair.
func thinkingTypeFixture(t *testing.T, modelAlias, thinkingType string) (*AgentLoop, *AgentInstance, func()) {
	t.Helper()

	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	al.registry.mu.Lock()
	al.registry.agents["main"] = agent
	al.registry.mu.Unlock()

	cfg := al.cfg()
	cfg.Providers = &config.ProvidersConfig{
		Named: map[string]config.NamedProviderConfig{
			"xiaomi": {
				Type: "openai",
				Models: map[string]config.ProviderModelConfig{
					modelAlias: {Model: modelAlias, ThinkingType: thinkingType},
				},
			},
		},
	}

	return al, agent, func() { os.RemoveAll(tmpDir) }
}

func TestBuildLLMOptions_ThinkingType(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		modelAlias  string
		thinkingTyp string
		wantPresent bool
		want        string
	}{
		{
			name:        "explicit deepseek is passed through",
			model:       "xiaomi:mimo-v2.5-pro",
			modelAlias:  "mimo-v2.5-pro",
			thinkingTyp: "deepseek",
			wantPresent: true,
			want:        "deepseek",
		},
		{
			name:        "explicit qwen is passed through",
			model:       "xiaomi:qwen3-32b",
			modelAlias:  "qwen3-32b",
			thinkingTyp: "qwen",
			wantPresent: true,
			want:        "qwen",
		},
		{
			name:        "auto is omitted so legacy heuristics apply",
			model:       "xiaomi:mimo-v2.5-pro",
			modelAlias:  "mimo-v2.5-pro",
			thinkingTyp: "auto",
			wantPresent: false,
		},
		{
			name:        "unset is omitted",
			model:       "xiaomi:mimo-v2.5-pro",
			modelAlias:  "mimo-v2.5-pro",
			thinkingTyp: "",
			wantPresent: false,
		},
		{
			name:        "unconfigured model is omitted",
			model:       "xiaomi:other-model",
			modelAlias:  "mimo-v2.5-pro",
			thinkingTyp: "deepseek",
			wantPresent: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			al, agent, cleanup := thinkingTypeFixture(t, tc.modelAlias, tc.thinkingTyp)
			defer cleanup()

			caller := newLLMCaller(al)
			got := caller.buildLLMOptions(llmCallOptions{
				ctx:   context.Background(),
				agent: agent,
				model: tc.model,
				// The thinking_type lookup must not depend on any session
				// thinking override.
				sessionKey: "test-session",
			})

			value, has := got["thinking_type"]
			if tc.wantPresent != has {
				t.Fatalf("thinking_type present = %v (%v), want present = %v", has, value, tc.wantPresent)
			}
			if tc.wantPresent && value != tc.want {
				t.Fatalf("thinking_type = %v, want %q", value, tc.want)
			}
		})
	}
}

// TestSummaryGoalJudge_ThinkingTypePassthrough pins that the goal judge ships
// the per-model think system alongside its explicit reasoning disable, so the
// provider can render the disable in the endpoint's dialect (e.g. MiMo/DeepSeek
// `thinking: {"type": "disabled"}`).
func TestSummaryGoalJudge_ThinkingTypePassthrough(t *testing.T) {
	provider := &mockProvider{mockResponse: "DONE"}
	judge := NewSummaryGoalJudge(provider, "xiaomi:mimo-v2.5-pro", &mockSummaryProvider{summary: ""}, nil)
	judge.SetConfig(&config.Config{
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"xiaomi": {
					Type: "openai",
					Models: map[string]config.ProviderModelConfig{
						"mimo-v2.5-pro": {Model: "mimo-v2.5-pro", ThinkingType: "deepseek"},
					},
				},
			},
		},
	})

	if _, _, err := judge.JudgeGoal(context.Background(), "skey", "Refactor auth", "Added tests."); err != nil {
		t.Fatalf("JudgeGoal error = %v", err)
	}

	if provider.lastOptions == nil {
		t.Fatal("provider was not called")
	}
	if got := provider.lastOptions["thinking_type"]; got != "deepseek" {
		t.Fatalf("thinking_type = %v, want deepseek", got)
	}
	reasoning, ok := provider.lastOptions["reasoning"].(map[string]interface{})
	if !ok || reasoning["enabled"] != false {
		t.Fatalf("reasoning = %#v, want {enabled: false}", provider.lastOptions["reasoning"])
	}
}

// TestGetThinkingType_Resolution covers the config lookup helper directly,
// including the "auto means unset" contract and invalid values.
func TestGetThinkingType_Resolution(t *testing.T) {
	cfg := &config.Config{
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"xiaomi": {
					Type: "openai",
					Models: map[string]config.ProviderModelConfig{
						"a": {ThinkingType: "deepseek"},
						"b": {ThinkingType: "auto"},
						"c": {ThinkingType: ""},
						"d": {ThinkingType: "banana"}, // invalid: treated as unset
					},
				},
			},
		},
	}

	tests := []struct {
		model string
		want  string
	}{
		{"xiaomi:a", "deepseek"},
		{"xiaomi:b", ""},
		{"xiaomi:c", ""},
		{"xiaomi:d", ""},
		{"xiaomi:unknown", ""},
		{"other:a", ""},
	}

	for _, tc := range tests {
		if got := getThinkingType(cfg, tc.model, ""); got != tc.want {
			t.Errorf("getThinkingType(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}
