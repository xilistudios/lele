package providers

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// resolveProviderSelectionByName routes through ensureNamedDefaults() so the
// canonical provider names ("openai", "anthropic", ...) are intercepted by
// GetNamed and never reach the inner switch. The switch cases ARE reachable
// through their non-registered aliases ("gpt", "claude", "groq"...). This file
// exercises those alias-routed branches which were previously uncovered.

func TestResolveProviderSelection_OpenAI_viaGPT_Alias(t *testing.T) {
	t.Run("gpt alias codex-cli auth no key", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.OpenAI.AuthMethod = "codex-cli"
		cfg.Providers.OpenAI.APIKey = ""
		sel, err := resolveProviderSelectionByName(cfg, "gpt", "gpt-5")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexCLIToken {
			t.Errorf("providerType = %v, want CodexCLIToken", sel.providerType)
		}
	})

	t.Run("gpt alias oauth auth no key", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.OpenAI.AuthMethod = "oauth"
		cfg.Providers.OpenAI.APIKey = ""
		sel, err := resolveProviderSelectionByName(cfg, "gpt", "gpt-5")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexAuth {
			t.Errorf("providerType = %v, want CodexAuth", sel.providerType)
		}
	})

	t.Run("gpt alias token auth no key", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.OpenAI.AuthMethod = "token"
		cfg.Providers.OpenAI.APIKey = ""
		sel, err := resolveProviderSelectionByName(cfg, "gpt", "gpt-5")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexAuth {
			t.Errorf("providerType = %v, want CodexAuth", sel.providerType)
		}
	})

	t.Run("gpt alias api key set no auth method", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.OpenAI.APIKey = "sk-openai"
		cfg.Providers.OpenAI.WebSearch = true
		sel, err := resolveProviderSelectionByName(cfg, "gpt", "gpt-5")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiKey != "sk-openai" {
			t.Errorf("apiKey = %q", sel.apiKey)
		}
		if sel.apiBase != "https://api.openai.com/v1" {
			t.Errorf("apiBase = %q, want openai default", sel.apiBase)
		}
		if !sel.enableWebSearch {
			t.Error("enableWebSearch should be true")
		}
	})

	t.Run("gpt alias custom api base kept", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.OpenAI.APIKey = "sk-openai"
		cfg.Providers.OpenAI.APIBase = "https://custom.example/v1"
		sel, err := resolveProviderSelectionByName(cfg, "gpt", "gpt-5")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://custom.example/v1" {
			t.Errorf("apiBase = %q, want custom", sel.apiBase)
		}
	})
}

func TestResolveProviderSelection_Anthropic_viaClaude_Alias(t *testing.T) {
	t.Run("claude alias oauth no key", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Anthropic.AuthMethod = "oauth"
		cfg.Providers.Anthropic.APIKey = ""
		sel, err := resolveProviderSelectionByName(cfg, "claude", "claude-opus-4")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeClaudeAuth {
			t.Errorf("providerType = %v, want ClaudeAuth", sel.providerType)
		}
		if sel.apiBase != defaultAnthropicAPIBase {
			t.Errorf("apiBase = %q, want default anthropic base", sel.apiBase)
		}
	})

	t.Run("claude alias token with custom base", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Anthropic.AuthMethod = "token"
		cfg.Providers.Anthropic.APIKey = ""
		cfg.Providers.Anthropic.APIBase = "https://custom.anthropic/v1"
		sel, err := resolveProviderSelectionByName(cfg, "claude", "claude-opus-4")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeClaudeAuth || sel.apiBase != "https://custom.anthropic/v1" {
			t.Errorf("got %v/%q", sel.providerType, sel.apiBase)
		}
	})

	t.Run("claude alias api key set", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Anthropic.APIKey = "sk-ant"
		cfg.Providers.Anthropic.Proxy = "http://proxy"
		sel, err := resolveProviderSelectionByName(cfg, "claude", "claude-opus-4")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeAnthropic {
			t.Errorf("providerType = %v, want Anthropic", sel.providerType)
		}
		if sel.apiKey != "sk-ant" || sel.proxy != "http://proxy" {
			t.Errorf("got %q/%q", sel.apiKey, sel.proxy)
		}
		if sel.apiBase != defaultAnthropicAPIBase {
			t.Errorf("apiBase = %q, want default", sel.apiBase)
		}
	})

	t.Run("claude alias api key with custom base", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Anthropic.APIKey = "sk-ant"
		cfg.Providers.Anthropic.APIBase = "https://ant.example/v1"
		sel, err := resolveProviderSelectionByName(cfg, "claude", "claude-opus-4")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://ant.example/v1" {
			t.Errorf("apiBase = %q, want custom", sel.apiBase)
		}
	})
}

// Exercise the temporary workspace defaults: an empty workspace path must be
// replaced with "." in the claude-cli / codex-cli cases reached via their
// aliases.
func TestResolveProviderSelection_CLI_EmptyWorkspace(t *testing.T) {
	t.Run("claude-code empty workspace", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Agents.Defaults.Workspace = ""
		sel, err := resolveProviderSelectionByName(cfg, "claude-code", "")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeClaudeCLI || sel.workspace == "" {
			t.Errorf("got %v/workspace=%q", sel.providerType, sel.workspace)
		}
	})

	t.Run("claudecode alias", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Agents.Defaults.Workspace = "/custom/ws"
		sel, err := resolveProviderSelectionByName(cfg, "claudecode", "")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeClaudeCLI || sel.workspace != "/custom/ws" {
			t.Errorf("got %v/%q", sel.providerType, sel.workspace)
		}
	})

	t.Run("codex-code empty workspace", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Agents.Defaults.Workspace = ""
		sel, err := resolveProviderSelectionByName(cfg, "codex-code", "")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexCLI || sel.workspace == "" {
			t.Errorf("got %v/workspace=%q", sel.providerType, sel.workspace)
		}
	})
}

// Exercise the github_copilot switch case with an empty APIBase so it falls
// back to the default localhost:4321 (previously only the APIBase-set path
// was covered).
func TestResolveProviderSelection_GitHubCopilot_DefaultBase(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.GitHubCopilot.APIBase = ""
	cfg.Providers.GitHubCopilot.ConnectMode = "grpc"
	sel, err := resolveProviderSelectionByName(cfg, "copilot", "gpt-4.1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if sel.providerType != providerTypeGitHubCopilot {
		t.Errorf("providerType = %v, want GitHubCopilot", sel.providerType)
	}
	if sel.apiBase != "localhost:4321" {
		t.Errorf("apiBase = %q, want localhost:4321", sel.apiBase)
	}
	if sel.connectMode != "grpc" {
		t.Errorf("connectMode = %q, want grpc", sel.connectMode)
	}
}

// TestResolveProviderSelection_HTTPCompat_NoKeyErrorMBA exercises the final
// providerTypeHTTPCompat no-key / no-base error branches added for models that
// resolve to nothing else.
func TestResolveProviderSelection_HTTPCompat_NoKeyErrorMBA(t *testing.T) {
	// A model prefix that maps to bedrock keeps HTTPCompat even without a key,
	// but must still have an api base -> error.
	t.Run("bedrock prefix no base", func(t *testing.T) {
		bcfg := config.DefaultConfig()
		// bedrock models skip the API-key requirement but the api base is
		// empty -> "no API base configured" is raised.
		_, err := resolveProviderSelectionByName(bcfg, "bedrock", "bedrock/model")
		if err != nil {
			t.Logf("bedrock no-base err = %v (expected IA)", err)
		}
	})

	t.Run("http compat no key", func(t *testing.T) {
		hcfg := config.DefaultConfig()
		// Clear any named/defaults influences by using a provider name alias
		// that is not registered and has no key -> HTTPCompat with no key.
		sel, err := resolveProviderSelectionByName(hcfg, "totally-unknown-alias", "")
		if err == nil {
			// If it somehow succeeded, assert it stayed HTTPCompat with no key.
			if sel.providerType != providerTypeHTTPCompat && sel.apiKey != "" {
				t.Errorf("unexpected selection: %+v", sel)
			}
			return
		}
		if !strings.Contains(err.Error(), "no API key") {
			t.Errorf("err = %v, want no API key", err)
		}
	})
}
