package main

import (
	"testing"
)

func TestConfigureProvider_LocalProvider(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	info := providerInfo{
		name:        "ollama",
		displayName: "Ollama (local)",
		typeKey:     "ollama",
		apiBase:     "http://localhost:11434/v1",
		authHeader:  "Bearer",
		local:       true,
	}

	// For a local provider: no API key prompt; asks API Base, proxy (no),
	// and model aliases (no).
	p := newStdinPipe(t)
	p.feedLines(
		"http://localhost:11434/v1", // API Base
		"n",                         // proxy? no
		"n",                         // alias models? no
	)
	p.close()
	_ = captureStdout(t)
	configureProvider(cfg, info)

	if cfg.Providers.Named["ollama"].APIBase != "http://localhost:11434/v1" {
		t.Errorf("expected API base set, got %q", cfg.Providers.Named["ollama"].APIBase)
	}
	if cfg.Providers.Ollama.APIBase != "http://localhost:11434/v1" {
		t.Errorf("expected Ollama API base set, got %q", cfg.Providers.Ollama.APIBase)
	}
}
