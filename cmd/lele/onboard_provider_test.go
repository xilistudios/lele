package main

import (
	"testing"
)

func TestProviderRegistry(t *testing.T) {
	registry := providerRegistry()
	if len(registry) != 40 {
		t.Errorf("Expected 40 providers, got %d", len(registry))
	}
}

func TestProviderRegistry_OpenCodeGo(t *testing.T) {
	registry := providerRegistry()

	found := false
	for _, p := range registry {
		if p.name == "opencode_go" {
			found = true
			if p.displayName != "OpenCode Go" {
				t.Errorf("displayName = %q, want %q", p.displayName, "OpenCode Go")
			}
			if p.typeKey != "opencode_go" {
				t.Errorf("typeKey = %q, want %q", p.typeKey, "opencode_go")
			}
			if p.apiBase != "https://opencode.ai/zen/go/v1" {
				t.Errorf("apiBase = %q, want %q", p.apiBase, "https://opencode.ai/zen/go/v1")
			}
			if p.authHeader != "Bearer" {
				t.Errorf("authHeader = %q, want %q", p.authHeader, "Bearer")
			}
			if p.local {
				t.Error("opencode_go should not be marked as local")
			}
			break
		}
	}
	if !found {
		t.Error("opencode_go provider not found in registry")
	}
}

func TestProviderRegistry_LocalProvider(t *testing.T) {
	registry := providerRegistry()

	found := false
	for _, p := range registry {
		if p.name == "ollama" {
			found = true
			if !p.local {
				t.Error("Ollama should be marked as local")
			}
			break
		}
	}
	if !found {
		t.Error("Ollama provider not found")
	}
}

func TestProviderRegistry_DisplayNames(t *testing.T) {
	providers := providerRegistry()
	for _, p := range providers {
		if p.displayName == "" {
			t.Errorf("Provider %s has empty displayName", p.name)
		}
		if p.apiBase == "" && p.name != "vllm" && p.name != "custom" {
			t.Errorf("Provider %s has empty apiBase", p.name)
		}
	}
}

func TestProviderRegistry_TypeKeys(t *testing.T) {
	registry := providerRegistry()

	for _, p := range registry {
		if p.typeKey == "" && p.name != "custom" {
			t.Errorf("Provider %s has empty typeKey", p.name)
		}
	}
}

func TestProviderRegistry_APIBaseURLs(t *testing.T) {
	registry := providerRegistry()

	expectedBases := map[string]string{
		"anthropic": "https://api.anthropic.com/v1",
		"openai":    "https://api.openai.com/v1",
		"groq":      "https://api.groq.com/openai/v1",
		"deepseek":  "https://api.deepseek.com/v1",
		"ollama":    "http://localhost:11434/v1",
	}

	for _, p := range registry {
		if expected, ok := expectedBases[p.name]; ok {
			if p.apiBase != expected {
				t.Errorf("Provider %s apiBase = %q, want %q", p.name, p.apiBase, expected)
			}
		}
	}
}

func TestProviderRegistry_AuthHeaders(t *testing.T) {
	registry := providerRegistry()

	for _, p := range registry {
		if p.name == "anthropic" {
			if p.authHeader != "x-api-key" {
				t.Errorf("Anthropic authHeader = %q, want 'x-api-key'", p.authHeader)
			}
			break
		}
	}

	for _, p := range registry {
		if p.name != "anthropic" {
			if p.authHeader != "Bearer" {
				t.Errorf("Provider %s authHeader = %q, want 'Bearer'", p.name, p.authHeader)
			}
		}
	}
}

func TestValidateProvider_EdgeCases(t *testing.T) {
	if validateProvider("ollama", "", "localhost:11434/v1", "Bearer") {
		t.Error("validateProvider with empty apiKey should return false")
	}

	if validateProvider("ollama", "somekey", "", "Bearer") {
		t.Error("validateProvider with empty apiBase should return false")
	}

	if !validateProvider("ollama", "somekey", "localhost:11434/v1", "Bearer") {
		t.Error("validateProvider with localhost and key should return true")
	}

	if !validateProvider("ollama", "somekey", "http://localhost:11434/v1", "Bearer") {
		t.Error("validateProvider with http://localhost and key should return true")
	}
}

func TestValidateProvider_FalseCases(t *testing.T) {
	if validateProvider("anthropic", "sk-test", "", "x-api-key") {
		t.Error("validateProvider with empty apiBase should return false")
	}

	if validateProvider("anthropic", "", "https://api.anthropic.com/v1", "x-api-key") {
		t.Error("validateProvider with empty apiKey should return false")
	}
}

func TestProviderRegistry_CustomProvider(t *testing.T) {
	registry := providerRegistry()

	found := false
	for _, p := range registry {
		if p.name == "custom" {
			found = true
			if p.typeKey != "" {
				t.Error("Custom provider should have empty typeKey")
			}
			if p.apiBase != "" {
				t.Error("Custom provider should have empty apiBase")
			}
			break
		}
	}
	if !found {
		t.Error("Custom provider not found")
	}
}

func TestProviderRegistry_NoDuplicates(t *testing.T) {
	registry := providerRegistry()
	seen := make(map[string]int, len(registry))
	for _, p := range registry {
		seen[p.name]++
		if seen[p.name] > 1 {
			t.Errorf("Duplicate provider name: %q", p.name)
		}
	}
}

func TestProviderRegistry_ValidAuthHeaders(t *testing.T) {
	registry := providerRegistry()
	validHeaders := map[string]bool{
		"x-api-key": true,
		"Bearer":    true,
	}

	for _, p := range registry {
		if !validHeaders[p.authHeader] {
			t.Errorf("Provider %s has invalid authHeader: %q", p.name, p.authHeader)
		}
	}
}

func TestProviderRegistry_AllHaveDisplayName(t *testing.T) {
	registry := providerRegistry()

	for _, p := range registry {
		if p.displayName == "" {
			t.Errorf("Provider %s has empty displayName", p.name)
		}
		if p.typeKey == "" && p.name != "custom" {
			t.Errorf("Provider %s has empty typeKey", p.name)
		}
	}
}
