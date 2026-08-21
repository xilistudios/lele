package main

import (
	"testing"
)

// TestConfigureWebUI_Advanced covers configureWebUI with advanced native config.
func TestConfigureWebUI_Advanced(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	p := newStdinPipe(t)
	p.feedLines(
		"8080", // Server port (askInt)
		"y",    // Configure native channel? yes
		"7",    // Max clients
		"14",   // Token expiry days
	)
	p.close()
	_ = captureStdout(t)
	configureWebUI(cfg, t.TempDir())
	if !cfg.Channels.Web.Enabled {
		t.Error("web should be enabled")
	}
	if !cfg.Channels.Native.Enabled {
		t.Error("native should be enabled")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Server.Port)
	}
}

// TestRegisterKeyringResolver_Enabled covers the non-nil, keyring-enabled path.
func TestRegisterKeyringResolver_Enabled(t *testing.T) {
	setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Keyring.Enabled = true
	// Must not panic.
	registerKeyringResolver(cfg)
	// Clean up: reset resolver.
	registerKeyringResolver(nil)
}