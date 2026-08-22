package config

import (
	"encoding/json"
	"testing"
)

func TestParseAgentsWithPlaceholders(t *testing.T) {
	raw := json.RawMessage(`{
		"defaults": {
			"workspace": "/ws",
			"provider": "openai",
			"model": "gpt-4",
			"subagent_timeout_minutes": 15
		},
		"list": [
			{"id": "a1", "name": "Agent One"},
			{"id": "a2", "name": "Agent Two"}
		]
	}`)

	cfg := parseAgentsWithPlaceholders(raw, "agents", nil)
	if cfg.Defaults.Workspace != "/ws" {
		t.Errorf("workspace = %q, want /ws", cfg.Defaults.Workspace)
	}
	if cfg.Defaults.Provider != "openai" {
		t.Errorf("provider = %q, want openai", cfg.Defaults.Provider)
	}
	if cfg.Defaults.SubagentTimeoutMinutes != 15 {
		t.Errorf("subagent_timeout_minutes = %d, want 15", cfg.Defaults.SubagentTimeoutMinutes)
	}
	if len(cfg.List) != 2 {
		t.Fatalf("list len = %d, want 2", len(cfg.List))
	}
	if cfg.List[1].ID != "a2" {
		t.Errorf("list[1].ID = %q, want a2", cfg.List[1].ID)
	}
}

func TestParseAgentsWithPlaceholders_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`"not an object"`)
	cfg := parseAgentsWithPlaceholders(raw, "agents", nil)
	// Should not panic; falls back to plain unmarshal into the zero struct.
	if cfg.Defaults.Provider != "" || len(cfg.List) != 0 {
		t.Errorf("unexpected content for invalid JSON: %+v", cfg)
	}
}

func TestParseAgentsWithPlaceholders_UnmarshalErrorJSON(t *testing.T) {
	// Valid JSON object but with type mismatch on defaults: the map unmarshal
	// succeeds, then json.Unmarshal(defaultsRaw) fails and is ignored.
	raw := json.RawMessage(`{"defaults": true}`)
	cfg := parseAgentsWithPlaceholders(raw, "agents", nil)
	_ = cfg // should not panic
}

func TestParseAgentsWithPlaceholders_MissingSections(t *testing.T) {
	raw := json.RawMessage(`{}`)
	cfg := parseAgentsWithPlaceholders(raw, "agents", nil)
	if cfg.Defaults.Provider != "" || len(cfg.List) != 0 {
		t.Errorf("expected zero config for empty object, got %+v", cfg)
	}
}

func TestParseToolsWithPlaceholders(t *testing.T) {
	raw := json.RawMessage(`{
		"web": {
			"brave": {"enabled": true, "api_key": "brave-key", "max_results": 7},
			"perplexity": {"enabled": true, "api_key": "pplx-key", "max_results": 3}
		},
		"cron": {"exec_timeout_minutes": 9},
		"exec": {"timeout_seconds": 30, "enable_deny_patterns": false}
	}`)

	cfg := parseToolsWithPlaceholders(raw, "tools", map[string]string{})
	if !cfg.Web.Brave.Enabled {
		t.Error("brave should be enabled")
	}
	if cfg.Web.Brave.MaxResults != 7 {
		t.Errorf("brave max_results = %d, want 7", cfg.Web.Brave.MaxResults)
	}
	if cfg.Web.Brave.APIKey.Value != "brave-key" {
		t.Errorf("brave api_key = %q", cfg.Web.Brave.APIKey.Value)
	}
	if !cfg.Web.Perplexity.Enabled {
		t.Error("perplexity should be enabled")
	}
	if cfg.Web.Perplexity.APIKey.Value != "pplx-key" {
		t.Errorf("perplexity api_key = %q", cfg.Web.Perplexity.APIKey.Value)
	}
	if cfg.Cron.ExecTimeoutMinutes != 9 {
		t.Errorf("cron timeout = %d, want 9", cfg.Cron.ExecTimeoutMinutes)
	}
	if cfg.Exec.TimeoutSeconds != 30 {
		t.Errorf("exec timeout = %d, want 30", cfg.Exec.TimeoutSeconds)
	}
	if cfg.Exec.EnableDenyPatterns {
		t.Error("exec enable_deny_patterns should be false")
	}
}

func TestParseToolsWithPlaceholders_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`42`)
	cfg := parseToolsWithPlaceholders(raw, "tools", map[string]string{})
	if cfg.Web.Brave.APIKey.Value != "" {
		t.Errorf("unexpected brave key for invalid JSON: %+v", cfg)
	}
}

func TestOverlayToolsWithPlaceholders(t *testing.T) {
	var cfg EditableToolsConfig
	overlay := json.RawMessage(`{
		"web": {"brave": {"enabled": true, "api_key": "k", "max_results": 5}},
		"exec": {"timeout_seconds": 45}
	}`)
	overlayToolsWithPlaceholders(&cfg, overlay, "tools", map[string]string{})
	if !cfg.Web.Brave.Enabled {
		t.Error("brave should be enabled after overlay")
	}
	if cfg.Web.Brave.APIKey.Value != "k" {
		t.Errorf("brave api_key = %q", cfg.Web.Brave.APIKey.Value)
	}
	if cfg.Exec.TimeoutSeconds != 45 {
		t.Errorf("exec timeout = %d, want 45", cfg.Exec.TimeoutSeconds)
	}
}

func TestOverlayToolsWithPlaceholders_InvalidOverlay(t *testing.T) {
	var cfg EditableToolsConfig
	cfg.Exec.TimeoutSeconds = 10
	overlayToolsWithPlaceholders(&cfg, json.RawMessage(`"junk"`), "tools", map[string]string{})
	if cfg.Exec.TimeoutSeconds != 10 {
		t.Errorf("invalid overlay should leave existing value untouched, got %d", cfg.Exec.TimeoutSeconds)
	}
}

func TestParseWebToolsWithPlaceholders(t *testing.T) {
	raw := json.RawMessage(`{
		"brave": {"enabled": true, "api_key": "bk", "max_results": 6},
		"duckduckgo": {"enabled": true, "max_results": 4},
		"perplexity": {"enabled": true, "api_key": "pk", "max_results": 2},
		"searxng": {"instance_url": "https://sx", "max_results": 8}
	}`)

	cfg := parseWebToolsWithPlaceholders(raw, "tools.web", map[string]string{})
	if !cfg.Brave.Enabled || cfg.Brave.APIKey.Value != "bk" || cfg.Brave.MaxResults != 6 {
		t.Errorf("brave = %+v", cfg.Brave)
	}
	if !cfg.DuckDuckGo.Enabled || cfg.DuckDuckGo.MaxResults != 4 {
		t.Errorf("ddg = %+v", cfg.DuckDuckGo)
	}
	if !cfg.Perplexity.Enabled || cfg.Perplexity.APIKey.Value != "pk" || cfg.Perplexity.MaxResults != 2 {
		t.Errorf("perplexity = %+v", cfg.Perplexity)
	}
	if cfg.SearXNG.InstanceURL != "https://sx" || cfg.SearXNG.MaxResults != 8 {
		t.Errorf("searxng = %+v", cfg.SearXNG)
	}
}

func TestParseWebToolsWithPlaceholders_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`true`)
	cfg := parseWebToolsWithPlaceholders(raw, "tools.web", map[string]string{})
	_ = cfg // must not panic
}

func TestParseBraveWithPlaceholders(t *testing.T) {
	raw := json.RawMessage(`{"enabled": true, "api_key": "bk", "max_results": 9}`)
	cfg := parseBraveWithPlaceholders(raw, "tools.web.brave", map[string]string{})
	if !cfg.Enabled || cfg.APIKey.Value != "bk" || cfg.MaxResults != 9 {
		t.Errorf("brave = %+v", cfg)
	}
}

func TestParseBraveWithPlaceholders_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`"not-object"`)
	cfg := parseBraveWithPlaceholders(raw, "tools.web.brave", map[string]string{})
	_ = cfg
}

func TestParsePerplexityWithPlaceholders(t *testing.T) {
	raw := json.RawMessage(`{"enabled": true, "api_key": "pk", "max_results": 3}`)
	cfg := parsePerplexityWithPlaceholders(raw, "tools.web.perplexity", map[string]string{})
	if !cfg.Enabled || cfg.APIKey.Value != "pk" || cfg.MaxResults != 3 {
		t.Errorf("perplexity = %+v", cfg)
	}
}

func TestParsePerplexityWithPlaceholders_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`123`)
	cfg := parsePerplexityWithPlaceholders(raw, "tools.web.perplexity", map[string]string{})
	_ = cfg
}
