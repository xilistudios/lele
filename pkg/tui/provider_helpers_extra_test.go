package tui

import (
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// buildProviderModel builds a model configured with a named provider so that
// listProviders / listProviderModels return values.
func buildProviderModel(t *testing.T) *Model {
	t.Helper()
	cfg := testModelConfig(t)
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "sk-xxx"},
			Models: map[string]config.ProviderModelConfig{
				"gpt-4o":      {Model: "gpt-4o", ContextWindow: 128000},
				"gpt-4o-mini": {Model: "gpt-4o-mini"},
			},
		},
		"anthropic": {
			Type:           "anthropic",
			ProviderConfig: config.ProviderConfig{APIKey: "sk-ant"},
			Models: map[string]config.ProviderModelConfig{
				"claude": {Model: "claude-3"},
			},
		},
	}
	return newTestModelWithConfig(t, cfg, true)
}

func TestListProviders(t *testing.T) {
	m := buildProviderModel(t)
	got := m.listProviders()
	if len(got) != 2 {
		t.Fatalf("expected 2 providers, got %v", got)
	}
	if got[0] != "anthropic" || got[1] != "openai" {
		t.Errorf("expected sorted providers, got %v", got)
	}
}

func TestListProvidersEmpty(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Providers = &config.ProvidersConfig{}
	m := newTestModelWithConfig(t, cfg, true)
	if got := m.listProviders(); len(got) != 0 {
		t.Errorf("expected empty when no providers configured, got %v", got)
	}
}

func TestListProviderModels(t *testing.T) {
	m := buildProviderModel(t)
	got := m.listProviderModels("openai")
	if len(got) != 2 {
		t.Fatalf("expected 2 models, got %v", got)
	}
	if got[0] != "gpt-4o" || got[1] != "gpt-4o-mini" {
		t.Errorf("expected sorted model aliases, got %v", got)
	}
	if got := m.listProviderModels("missing"); got != nil {
		t.Errorf("expected nil for missing provider, got %v", got)
	}
}

func TestMissingProviderReturnsNoModels(t *testing.T) {
	m := buildProviderModel(t)
	if got := m.listProviderModels("any-missing"); got != nil {
		t.Errorf("expected nil for missing provider, got %v", got)
	}
}

func TestUpdateProvider(t *testing.T) {
	m := buildProviderModel(t)
	if err := m.updateProvider("OpenAI", "sk-new", "https://api.example.com"); err != nil {
		t.Fatalf("updateProvider: %v", err)
	}
	np := m.cfg.Providers.Named["openai"]
	if np.APIKey != "sk-new" || np.APIBase != "https://api.example.com" {
		t.Errorf("provider not updated: %+v", np)
	}
	// Config file should exist on disk (via DefaultConfigPath / LELE_CONFIG_DIR).
	if _, err := os.Stat(config.DefaultConfigPath()); err != nil {
		t.Errorf("expected config saved to disk: %v", err)
	}
}

func TestUpdateProviderErrors(t *testing.T) {
	// nil cfg
	m := &Model{}
	if err := m.updateProvider("x", "k", "b"); err == nil {
		t.Error("expected error when cfg nil")
	}
	// missing provider
	m2 := buildProviderModel(t)
	if err := m2.updateProvider("not-there", "k", "b"); err == nil {
		t.Error("expected error for missing provider")
	}
}

func TestUpdateProviderNoProviders(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Providers = &config.ProvidersConfig{}
	m := newTestModelWithConfig(t, cfg, true)
	if err := m.updateProvider("x", "k", "b"); err == nil {
		t.Error("expected error when no named providers")
	}
}

func TestDeleteProvider(t *testing.T) {
	m := buildProviderModel(t)
	if err := m.deleteProvider("openai"); err != nil {
		t.Fatalf("deleteProvider: %v", err)
	}
	if _, ok := m.cfg.Providers.Named["openai"]; ok {
		t.Error("expected provider removed")
	}
}

func TestDeleteProviderErrors(t *testing.T) {
	m := &Model{}
	if err := m.deleteProvider("x"); err == nil {
		t.Error("expected error when cfg nil")
	}
	m2 := buildProviderModel(t)
	if err := m2.deleteProvider("not-there"); err == nil {
		t.Error("expected error for missing provider")
	}
}

func TestAddProviderErrors(t *testing.T) {
	m := &Model{}
	if err := m.addProvider("", "openai", "k", ""); err == nil {
		t.Error("expected error for empty name")
	}
	m2 := &Model{cfg: &config.Config{Providers: &config.ProvidersConfig{Named: map[string]config.NamedProviderConfig{}}}}
	if err := m2.addProvider("openai", "openai", "k", ""); err != nil {
		t.Fatalf("addProvider: %v", err)
	}
	// duplicate
	if err := m2.addProvider("openai", "openai", "k2", ""); err == nil {
		t.Error("expected error for duplicate provider")
	}
}

func TestAddModelToProvider(t *testing.T) {
	m := buildProviderModel(t)
	if err := m.addModelToProvider("openai", "gpt-5", "gpt-5", 200000, 100000, true); err != nil {
		t.Fatalf("addModelToProvider: %v", err)
	}
	mod := m.cfg.Providers.Named["openai"].Models["gpt-5"]
	if mod.Model != "gpt-5" || !mod.Vision || mod.ContextWindow != 200000 || mod.MaxTokens != 100000 {
		t.Errorf("model not added correctly: %+v", mod)
	}
}

func TestAddModelToProviderErrors(t *testing.T) {
	m := &Model{}
	if err := m.addModelToProvider("p", "a", "m", 1, 2, false); err == nil {
		t.Error("expected error when cfg nil")
	}
	m2 := buildProviderModel(t)
	if err := m2.addModelToProvider("missing", "a", "m", 1, 2, false); err == nil {
		t.Error("expected error for missing provider")
	}
	if err := m2.addModelToProvider("openai", "", "m", 1, 2, false); err == nil {
		t.Error("expected error for empty alias")
	}
}

func TestDeleteModelFromProvider(t *testing.T) {
	m := buildProviderModel(t)
	if err := m.deleteModelFromProvider("openai", "gpt-4o"); err != nil {
		t.Fatalf("deleteModelFromProvider: %v", err)
	}
	if _, ok := m.cfg.Providers.Named["openai"].Models["gpt-4o"]; ok {
		t.Error("expected model removed")
	}
}

func TestDeleteModelFromProviderErrors(t *testing.T) {
	m := &Model{}
	if err := m.deleteModelFromProvider("p", "a"); err == nil {
		t.Error("expected error when cfg nil")
	}
	m2 := buildProviderModel(t)
	if err := m2.deleteModelFromProvider("missing", "a"); err == nil {
		t.Error("expected error for missing provider")
	}
	// missing model shouldn't panic even if Models nil
	m3 := &Model{cfg: &config.Config{Providers: &config.ProvidersConfig{Named: map[string]config.NamedProviderConfig{
		"some": {Models: nil},
	}}}}
	if err := m3.deleteModelFromProvider("some", "nothere"); err == nil {
		t.Error("expected error for missing model when Models nil")
	}
	if err := m3.deleteModelFromProvider("some", "nothere2"); err == nil {
		t.Error("expected error for missing model")
	}
}

func TestSaveConfigToDisk(t *testing.T) {
	m := buildProviderModel(t)
	if err := m.saveConfigToDisk(); err != nil {
		t.Fatalf("saveConfigToDisk: %v", err)
	}
}
