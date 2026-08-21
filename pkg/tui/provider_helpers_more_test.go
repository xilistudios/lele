package tui

import (
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestAddProviderNilConfig(t *testing.T) {
	m := &Model{}
	if err := m.addProvider("x", "openai", "k", ""); err == nil {
		t.Error("expected error when cfg nil")
	}
}

func TestAddProviderNeedsProvidersInit(t *testing.T) {
	// cfg non-nil but Providers nil — addProvider should initialize it.
	m := &Model{cfg: &config.Config{}}
	if err := m.addProvider("newprov", "openai", "k", ""); err != nil {
		t.Fatalf("addProvider with nil Providers: %v", err)
	}
	if m.cfg.Providers == nil || m.cfg.Providers.Named == nil {
		t.Fatal("expected Providers and Named initialized")
	}
	if _, ok := m.cfg.Providers.Named["newprov"]; !ok {
		t.Error("expected provider added")
	}
}

func TestAddProviderNormalizesKey(t *testing.T) {
	m := buildProviderModel(t)
	if err := m.addProvider("  MyProv  ", "openai", "k", ""); err != nil {
		t.Fatalf("addProvider: %v", err)
	}
	if _, ok := m.cfg.Providers.Named["myprov"]; !ok {
		t.Error("expected lowercased trimmed key stored")
	}
}

func TestAddProviderPersistsToDisk(t *testing.T) {
	m := buildProviderModel(t)
	if err := m.addProvider("persisted-p", "openai", "k", ""); err != nil {
		t.Fatalf("addProvider: %v", err)
	}
	if err := m.saveConfigToDisk(); err != nil {
		t.Fatalf("save failed: %v", err)
	}
}

func TestDeleteModelNilModelsEdge(t *testing.T) {
	m := &Model{cfg: &config.Config{Providers: &config.ProvidersConfig{Named: map[string]config.NamedProviderConfig{}}}}
	if err := m.deleteModelFromProvider("missing", "a"); err == nil {
		t.Error("expected error for missing provider with nil models")
	}
}