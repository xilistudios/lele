package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEditableDocument_FileNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "nonexistent", "config.json")

	doc, meta, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	if doc == nil {
		t.Fatal("expected document, got nil")
	}
	if meta == nil {
		t.Fatal("expected metadata, got nil")
	}

	// Verify defaults.
	if doc.Agents.Defaults.Workspace == "" {
		t.Error("expected default workspace")
	}
	if doc.Agents.Defaults.Provider == "" {
		t.Error("expected default provider")
	}
}

func TestLoadEditableDocument_BasicConfig(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	config := `{
		"agents": {
			"defaults": {
				"workspace": "/test/workspace",
				"provider": "openai",
				"model": "gpt-4"
			}
		},
		"channels": {
			"native": {
				"enabled": true,
				"port": 8080
			}
		}
	}`

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	doc, meta, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	if doc.Agents.Defaults.Workspace != "/test/workspace" {
		t.Errorf("workspace = %q, want /test/workspace", doc.Agents.Defaults.Workspace)
	}
	if doc.Agents.Defaults.Provider != "openai" {
		t.Errorf("provider = %q, want openai", doc.Agents.Defaults.Provider)
	}
	if doc.Channels.Native.Port != 8080 {
		t.Errorf("port = %d, want 8080", doc.Channels.Native.Port)
	}
	if meta.ConfigPath != path {
		t.Errorf("config path = %q, want %q", meta.ConfigPath, path)
	}
}

func TestLoadEditableDocument_WithEnvPlaceholder(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	config := `{
		"channels": {
			"telegram": {
				"enabled": true,
				"token": "{{ENV_TELEGRAM_BOT_TOKEN}}"
			}
		}
	}`

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	doc, meta, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	if doc.Channels.Telegram.Token.Mode != SecretModeEnv {
		t.Errorf("token mode = %q, want env", doc.Channels.Telegram.Token.Mode)
	}
	if doc.Channels.Telegram.Token.EnvName != "TELEGRAM_BOT_TOKEN" {
		t.Errorf("env name = %q, want TELEGRAM_BOT_TOKEN", doc.Channels.Telegram.Token.EnvName)
	}
	if meta.SecretsByPath["channels.telegram.token"] != "env" {
		t.Error("expected secret path to be marked as env")
	}
}

func TestLoadEditableDocument_WithEnvPlaceholderAndDefault(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	config := `{
		"providers": {
			"openai": {
				"api_key": "{{ENV_OPENAI_API_KEY:sk-default}}"
			}
		}
	}`

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	if doc.Providers["openai"].APIKey.Mode != SecretModeEnv {
		t.Errorf("api_key mode = %q, want env", doc.Providers["openai"].APIKey.Mode)
	}
	if doc.Providers["openai"].APIKey.EnvName != "OPENAI_API_KEY" {
		t.Errorf("env name = %q, want OPENAI_API_KEY", doc.Providers["openai"].APIKey.EnvName)
	}
}

func TestLoadEditableDocument_WithLiteralSecret(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	config := `{
		"channels": {
			"discord": {
				"enabled": true,
				"token": "literal-token-value"
			}
		}
	}`

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	if doc.Channels.Discord.Token.Mode != SecretModeLiteral {
		t.Errorf("token mode = %q, want literal", doc.Channels.Discord.Token.Mode)
	}
	if doc.Channels.Discord.Token.Value != "literal-token-value" {
		t.Errorf("token value = %q, want literal-token-value", doc.Channels.Discord.Token.Value)
	}
}

func TestLoadEditableDocument_WithEmptySecret(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	config := `{
		"channels": {
			"telegram": {
				"enabled": false,
				"token": ""
			}
		}
	}`

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	if doc.Channels.Telegram.Token.Mode != SecretModeEmpty {
		t.Errorf("token mode = %q, want empty", doc.Channels.Telegram.Token.Mode)
	}
}

func TestValidateEditableDocument_Valid(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Channels: EditableChannelsConfig{
			Native: EditableNativeConfig{
				Enabled: true,
				Port:    8080,
			},
		},
		Heartbeat: HeartbeatConfig{
			Enabled:  true,
			Interval: 30,
		},
	}

	errors := ValidateEditableDocument(doc)
	if len(errors) > 0 {
		t.Errorf("expected no errors, got %d: %v", len(errors), errors)
	}
}

func TestValidateEditableDocument_MissingRequired(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "",
				Provider:          "",
				Model:             "",
				MaxTokens:         0,
				MaxToolIterations: 0,
			},
		},
	}

	errors := ValidateEditableDocument(doc)
	if len(errors) == 0 {
		t.Error("expected validation errors")
	}

	// There should be errors for workspace, provider, model, max_tokens, and max_tool_iterations.
	if len(errors) < 5 {
		t.Errorf("expected at least 5 validation errors, got %d", len(errors))
	}
}

func TestValidateEditableDocument_InvalidPort(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Channels: EditableChannelsConfig{
			Native: EditableNativeConfig{
				Enabled: true,
				Port:    99999, // Invalid port
			},
		},
	}

	errors := ValidateEditableDocument(doc)

	foundPortError := false
	for _, err := range errors {
		if err.Path == "channels.native.port" {
			foundPortError = true
			break
		}
	}

	if !foundPortError {
		t.Error("expected port validation error")
	}
}

func TestValidateEditableDocument_InvalidVerbose(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Channels: EditableChannelsConfig{
			Telegram: EditableTelegramConfig{
				Verbose: "invalid",
			},
		},
	}

	errors := ValidateEditableDocument(doc)

	foundVerboseError := false
	for _, err := range errors {
		if err.Path == "channels.telegram.verbose" {
			foundVerboseError = true
			break
		}
	}

	if !foundVerboseError {
		t.Error("expected verbose validation error")
	}
}

func TestValidateEditableDocument_InvalidRotation(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Logs: EditableLogsConfig{
			Rotation: "monthly", // Invalid
		},
	}

	errors := ValidateEditableDocument(doc)

	foundRotationError := false
	for _, err := range errors {
		if err.Path == "logs.rotation" {
			foundRotationError = true
			break
		}
	}

	if !foundRotationError {
		t.Error("expected rotation validation error")
	}
}

func TestEditableDocument_ToConfig(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Channels: EditableChannelsConfig{
			Native: EditableNativeConfig{
				Enabled: true,
				Port:    8080,
			},
			Telegram: EditableTelegramConfig{
				Enabled: true,
				Token:   SecretValue{Mode: SecretModeLiteral, Value: "test-token"},
			},
		},
	}

	cfg, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig failed: %v", err)
	}

	if cfg.Agents.Defaults.Workspace != "/test" {
		t.Errorf("workspace = %q, want /test", cfg.Agents.Defaults.Workspace)
	}
	if cfg.Channels.Telegram.Token != "test-token" {
		t.Errorf("telegram token = %q, want test-token", cfg.Channels.Telegram.Token)
	}
}

func TestEditableDocument_Roundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	// Create a document with multiple secret types.
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Channels: EditableChannelsConfig{
			Native: EditableNativeConfig{
				Enabled: true,
				Port:    8080,
			},
			Telegram: EditableTelegramConfig{
				Enabled: true,
				Token:   SecretValue{Mode: SecretModeEnv, EnvName: "TEST_TOKEN"},
			},
			Discord: EditableDiscordConfig{
				Enabled: true,
				Token:   SecretValue{Mode: SecretModeLiteral, Value: "literal-token"},
			},
		},
	}

	// Save.
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument failed: %v", err)
	}

	// Load again.
	loadedDoc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	// Verify that the secret type was preserved.
	if loadedDoc.Channels.Telegram.Token.Mode != SecretModeEnv {
		t.Errorf("telegram token mode = %q, want env", loadedDoc.Channels.Telegram.Token.Mode)
	}
	if loadedDoc.Channels.Telegram.Token.EnvName != "TEST_TOKEN" {
		t.Errorf("telegram env name = %q, want TEST_TOKEN", loadedDoc.Channels.Telegram.Token.EnvName)
	}
	if loadedDoc.Channels.Discord.Token.Mode != SecretModeLiteral {
		t.Errorf("discord token mode = %q, want literal", loadedDoc.Channels.Discord.Token.Mode)
	}
	if loadedDoc.Channels.Discord.Token.Value != "literal-token" {
		t.Errorf("discord token value = %q, want literal-token", loadedDoc.Channels.Discord.Token.Value)
	}
}

func TestSaveEditableDocument_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	// Path in a subdirectory that does not exist.
	path := filepath.Join(tmpDir, "nested", "deep", "config.json")

	doc := defaultEditableDocument()
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument failed: %v", err)
	}

	// Verify that the file exists.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("config file should exist: %v", err)
	}
}

func TestSaveEditableDocument_FilePermissions(t *testing.T) {
	if os.Getenv("CI") != "" && os.Getenv("RUNNER_OS") == "Windows" {
		t.Skip("file permission bits are not enforced on Windows")
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	doc := defaultEditableDocument()
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("config file has permission %04o, want 0600", perm)
	}
}

func TestEditableDocument_WithNamedProviders(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	config := `{
		"providers": {
			"my-openai": {
				"type": "openai",
				"api_key": "{{ENV_MY_API_KEY}}",
				"api_base": "https://api.example.com",
				"models": {
					"fast": {"model": "gpt-4o-mini"}
				}
			}
		}
	}`

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	provider, ok := doc.Providers["my-openai"]
	if !ok {
		t.Fatal("expected named provider 'my-openai'")
	}

	if provider.Type != "openai" {
		t.Errorf("provider type = %q, want openai", provider.Type)
	}
	if provider.APIKey.Mode != SecretModeEnv {
		t.Errorf("api_key mode = %q, want env", provider.APIKey.Mode)
	}
	if provider.APIBase != "https://api.example.com" {
		t.Errorf("api_base = %q, want https://api.example.com", provider.APIBase)
	}
	if provider.Models["fast"].Model != "gpt-4o-mini" {
		t.Errorf("model fast = %q, want gpt-4o-mini", provider.Models["fast"].Model)
	}
}

func TestEditableDocument_WithAgentsList(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	config := `{
		"agents": {
			"defaults": {
				"workspace": "/test",
				"provider": "openai",
				"model": "gpt-4",
				"max_tokens": 8192,
				"max_tool_iterations": 20
			},
			"list": [
				{
					"id": "sales",
					"default": true,
					"name": "Sales Bot",
					"model": {
						"primary": "claude-opus",
						"fallbacks": ["gpt-4o-mini"]
					}
				}
			]
		}
	}`

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	if len(doc.Agents.List) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(doc.Agents.List))
	}

	agent := doc.Agents.List[0]
	if agent.ID != "sales" {
		t.Errorf("agent id = %q, want sales", agent.ID)
	}
	if !agent.Default {
		t.Error("expected agent to be default")
	}
	if agent.Model == nil || agent.Model.Primary != "claude-opus" {
		t.Errorf("agent model primary = %q, want claude-opus", agent.Model.Primary)
	}
	if len(agent.Model.Fallbacks) != 1 || agent.Model.Fallbacks[0] != "gpt-4o-mini" {
		t.Errorf("agent model fallbacks = %v", agent.Model.Fallbacks)
	}
}

func TestValidateEditableDocument_DuplicateProviderNames(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Providers: EditableProvidersConfig{
			"my-provider": {Type: "openai"},
			"My-Provider": {Type: "anthropic"}, // Duplicado (case insensitive)
			"MY-PROVIDER": {Type: "gemini"},    // Duplicado
		},
	}

	errors := ValidateEditableDocument(doc)

	foundDuplicate := false
	for _, err := range errors {
		if err.Code == "duplicate" {
			foundDuplicate = true
			break
		}
	}

	if !foundDuplicate {
		t.Error("expected duplicate provider validation error")
	}
}

func TestValidateEditableDocument_DuplicateAgentIDs(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
			List: []EditableAgentConfig{
				{ID: "agent1", Name: "Agent 1"},
				{ID: "agent1", Name: "Agent 2"}, // Duplicado
			},
		},
	}

	errors := ValidateEditableDocument(doc)

	foundDuplicate := false
	for _, err := range errors {
		if err.Code == "duplicate" {
			foundDuplicate = true
			break
		}
	}

	if !foundDuplicate {
		t.Error("expected duplicate agent validation error")
	}
}

func TestEditableDocument_Serialization(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Channels: EditableChannelsConfig{
			Telegram: EditableTelegramConfig{
				Token: SecretValue{Mode: SecretModeEnv, EnvName: "BOT_TOKEN"},
			},
		},
		Providers: EditableProvidersConfig{
			"openai": {
				Type:   "openai",
				APIKey: SecretValue{Mode: SecretModeLiteral, Value: "sk-test"},
			},
		},
	}

	serializable := doc.toSerializable()

	// Verify that ENV placeholders are preserved.
	channels := serializable["channels"].(map[string]interface{})
	telegram := channels["telegram"].(map[string]interface{})
	if telegram["token"] != "{{ENV_BOT_TOKEN}}" {
		t.Errorf("telegram token = %q, want {{ENV_BOT_TOKEN}}", telegram["token"])
	}

	// Verify that literal values are preserved.
	providers := serializable["providers"].(map[string]interface{})
	openai := providers["openai"].(map[string]interface{})
	if openai["api_key"] != "sk-test" {
		t.Errorf("openai api_key = %q, want sk-test", openai["api_key"])
	}
}

func TestSecretValue_Resolve(t *testing.T) {
	// Literal
	literal := SecretValue{Mode: SecretModeLiteral, Value: "test-value"}
	if literal.resolve() != "test-value" {
		t.Errorf("literal resolve = %q, want test-value", literal.resolve())
	}

	// Empty
	empty := SecretValue{Mode: SecretModeEmpty}
	if empty.resolve() != "" {
		t.Errorf("empty resolve = %q, want empty string", empty.resolve())
	}

	// Env (sin variable seteada)
	env := SecretValue{Mode: SecretModeEnv, EnvName: "NONEXISTENT_VAR"}
	if env.resolve() != "" {
		t.Errorf("env resolve = %q, want empty string", env.resolve())
	}

	// Env (with the variable set).
	os.Setenv("TEST_RESOLVE_VAR", "resolved-value")
	defer os.Unsetenv("TEST_RESOLVE_VAR")
	envWithValue := SecretValue{Mode: SecretModeEnv, EnvName: "TEST_RESOLVE_VAR"}
	if envWithValue.resolve() != "resolved-value" {
		t.Errorf("env resolve = %q, want resolved-value", envWithValue.resolve())
	}
}

func TestEditableDocument_WithToolsConfig(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	config := `{
		"tools": {
			"web": {
				"brave": {
					"enabled": true,
					"api_key": "{{ENV_BRAVE_API_KEY}}",
					"max_results": 10
				},
				"duckduckgo": {
					"enabled": true,
					"max_results": 5
				},
				"perplexity": {
					"enabled": false,
					"api_key": "",
					"max_results": 5
				}
			},
			"cron": {
				"exec_timeout_minutes": 10
			},
			"exec": {
				"enable_deny_patterns": true,
				"custom_deny_patterns": ["rm -rf /"]
			}
		}
	}`

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	// Verify Brave.
	if !doc.Tools.Web.Brave.Enabled {
		t.Error("expected brave to be enabled")
	}
	if doc.Tools.Web.Brave.APIKey.Mode != SecretModeEnv {
		t.Errorf("brave api_key mode = %q, want env", doc.Tools.Web.Brave.APIKey.Mode)
	}
	if doc.Tools.Web.Brave.MaxResults != 10 {
		t.Errorf("brave max_results = %d, want 10", doc.Tools.Web.Brave.MaxResults)
	}

	// Verify cron.
	if doc.Tools.Cron.ExecTimeoutMinutes != 10 {
		t.Errorf("cron exec_timeout_minutes = %d, want 10", doc.Tools.Cron.ExecTimeoutMinutes)
	}

	// Verify exec.
	if !doc.Tools.Exec.EnableDenyPatterns {
		t.Error("expected exec enable_deny_patterns to be true")
	}
	if len(doc.Tools.Exec.CustomDenyPatterns) != 1 {
		t.Errorf("exec custom_deny_patterns len = %d, want 1", len(doc.Tools.Exec.CustomDenyPatterns))
	}
}

func TestEditableDocument_WithSessionConfig(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	config := `{
		"session": {
			"dm_scope": "per-peer",
			"ephemeral": true,
			"ephemeral_threshold": 300,
			"identity_links": {
				"user1": ["telegram:123", "discord:user1#1234"]
			}
		}
	}`

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	if doc.Session.DMScope != "per-peer" {
		t.Errorf("dm_scope = %q, want per-peer", doc.Session.DMScope)
	}
	if !doc.Session.Ephemeral {
		t.Error("expected ephemeral to be true")
	}
	if doc.Session.EphemeralThreshold != 300 {
		t.Errorf("ephemeral_threshold = %d, want 300", doc.Session.EphemeralThreshold)
	}
	if len(doc.Session.IdentityLinks["user1"]) != 2 {
		t.Errorf("identity_links[user1] len = %d, want 2", len(doc.Session.IdentityLinks["user1"]))
	}
}

func TestEditableDocument_WithBindings(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	config := `{
		"bindings": [
			{
				"agent_id": "sales",
				"match": {
					"channel": "telegram",
					"account_id": "*",
					"peer": {"kind": "direct", "id": "user123"}
				}
			}
		]
	}`

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	if len(doc.Bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(doc.Bindings))
	}

	binding := doc.Bindings[0]
	if binding.AgentID != "sales" {
		t.Errorf("agent_id = %q, want sales", binding.AgentID)
	}
	if binding.Match.Channel != "telegram" {
		t.Errorf("channel = %q, want telegram", binding.Match.Channel)
	}
	if binding.Match.Peer == nil || binding.Match.Peer.ID != "user123" {
		t.Error("expected peer with id user123")
	}
}

func TestEditableDocument_ToConfig_MapsEmpty(t *testing.T) {
	doc := &EditableDocument{
		Providers: nil,
	}

	cfg, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig failed: %v", err)
	}

	if cfg.Providers.Named == nil {
		t.Error("expected Named to be initialized, got nil")
	}
}

func TestLoadEditableDocument_PreservesEnvFallback(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")
	config := `{
		"channels": {
			"telegram": {
				"enabled": true,
				"token": "{{ENV_TELEGRAM_TOKEN:bot-default}}"
			}
		}
	}`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}
	if doc.Channels.Telegram.Token.EnvDefault == nil || *doc.Channels.Telegram.Token.EnvDefault != "bot-default" {
		t.Fatalf("env default = %#v, want bot-default", doc.Channels.Telegram.Token.EnvDefault)
	}

	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) == "" || !bytes.Contains(data, []byte("{{ENV_TELEGRAM_TOKEN:bot-default}}")) {
		t.Fatalf("saved config did not preserve fallback: %s", string(data))
	}
}

func TestEditableDocument_JSONSerialization(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace: "/test",
				Provider:  "openai",
				Model:     "gpt-4",
			},
		},
	}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var unmarshaled EditableDocument
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if unmarshaled.Agents.Defaults.Workspace != "/test" {
		t.Errorf("workspace = %q, want /test", unmarshaled.Agents.Defaults.Workspace)
	}
}

func TestLoadEditableDocument_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	// Invalid JSON.
	config := `{ invalid json }`

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	_, _, err := LoadEditableDocument(path)
	if err == nil {
		t.Fatal("LoadEditableDocument should fail with invalid JSON")
	}
}
