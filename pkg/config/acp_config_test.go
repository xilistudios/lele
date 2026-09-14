package config

import "testing"

func TestDefaultConfig_ACPDisabled(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ACP.Enabled {
		t.Fatal("ACP should be disabled by default")
	}
	if cfg.ACP.Token != "" {
		t.Fatalf("ACP token default = %q, want empty", cfg.ACP.Token)
	}
}

func TestEditableDocumentPreservesACP(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ACP.Enabled = true
	cfg.ACP.Token = "tok"

	doc := editableDocumentFromConfig(cfg)
	if !doc.ACP.Enabled || doc.ACP.Token != "tok" {
		t.Fatalf("doc ACP = %+v", doc.ACP)
	}

	back, err := doc.ToConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !back.ACP.Enabled || back.ACP.Token != "tok" {
		t.Fatalf("roundtrip ACP = %+v", back.ACP)
	}
}
