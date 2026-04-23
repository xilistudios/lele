package main

import (
	"testing"
)

// TestProviderRegistry verifies the provider registry count
func TestProviderRegistry(t *testing.T) {
	registry := providerRegistry()
	if len(registry) != 16 {
		t.Errorf("Expected 16 providers, got %d", len(registry))
	}
}

// TestProviderRegistry_LocalProvider verifies Ollama is local
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

// TestProviderRegistry_DisplayNames verifies all providers have display names
func TestProviderRegistry_DisplayNames(t *testing.T) {
	providers := providerRegistry()
	for _, p := range providers {
		if p.displayName == "" {
			t.Errorf("Provider %s has empty displayName", p.name)
		}
		// vllm and custom providers intentionally have empty apiBase (user-configured)
		if p.apiBase == "" && p.name != "vllm" && p.name != "custom" {
			t.Errorf("Provider %s has empty apiBase", p.name)
		}
	}
}

// TestProviderRegistry_TypeKeys verifies all providers have type keys (except custom)
func TestProviderRegistry_TypeKeys(t *testing.T) {
	registry := providerRegistry()

	for _, p := range registry {
		if p.typeKey == "" && p.name != "custom" {
			t.Errorf("Provider %s has empty typeKey", p.name)
		}
	}
}

// TestProviderRegistry_APIBaseURLs verifies API base URLs
func TestProviderRegistry_APIBaseURLs(t *testing.T) {
	registry := providerRegistry()

	// Check specific providers
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

// TestProviderRegistry_AuthHeaders verifies auth headers
func TestProviderRegistry_AuthHeaders(t *testing.T) {
	registry := providerRegistry()

	// Anthropic should use x-api-key
	for _, p := range registry {
		if p.name == "anthropic" {
			if p.authHeader != "x-api-key" {
				t.Errorf("Anthropic authHeader = %q, want 'x-api-key'", p.authHeader)
			}
			break
		}
	}

	// Others should use Bearer
	for _, p := range registry {
		if p.name != "anthropic" {
			if p.authHeader != "Bearer" {
				t.Errorf("Provider %s authHeader = %q, want 'Bearer'", p.name, p.authHeader)
			}
		}
	}
}

// TestValidateProvider_EdgeCases tests validateProvider edge cases
func TestValidateProvider_EdgeCases(t *testing.T) {
	// Empty apiKey returns false immediately (before localhost check)
	if validateProvider("ollama", "", "localhost:11434/v1", "Bearer") {
		t.Error("validateProvider with empty apiKey should return false")
	}

	// Empty apiBase returns false immediately
	if validateProvider("ollama", "somekey", "", "Bearer") {
		t.Error("validateProvider with empty apiBase should return false")
	}

	// Localhost with valid key returns true
	if !validateProvider("ollama", "somekey", "localhost:11434/v1", "Bearer") {
		t.Error("validateProvider with localhost and key should return true")
	}

	// http://localhost with valid key returns true
	if !validateProvider("ollama", "somekey", "http://localhost:11434/v1", "Bearer") {
		t.Error("validateProvider with http://localhost and key should return true")
	}
}

// TestValidateProvider_FalseCases tests validateProvider returning false
func TestValidateProvider_FalseCases(t *testing.T) {
	// Empty API base should return false
	if validateProvider("anthropic", "sk-test", "", "x-api-key") {
		t.Error("validateProvider with empty apiBase should return false")
	}

	// Empty apiKey should return false (even with valid apiBase)
	if validateProvider("anthropic", "", "https://api.anthropic.com/v1", "x-api-key") {
		t.Error("validateProvider with empty apiKey should return false")
	}
}

// TestProviderRegistry_CustomProvider verifies custom provider
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

// TestProviderRegistry_NoDuplicates verifies no duplicate provider names
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

// TestProviderRegistry_ValidAuthHeaders verifies all auth headers are valid
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

// TestProviderRegistry_AllHaveDisplayName verifies all providers have display names
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
