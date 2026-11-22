package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestGetConfiguredModels_Empty(t *testing.T) {
	cfg := &config.Config{
		Providers: &config.ProvidersConfig{},
	}

	models := getConfiguredModels(cfg)
	if len(models) != 0 {
		t.Errorf("Expected 0 models, got %d: %v", len(models), models)
	}
}

func TestGetConfiguredModels_WithModels(t *testing.T) {
	cfg := &config.Config{
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"anthropic": {
					Type: "anthropic",
					ProviderConfig: config.ProviderConfig{
						APIKey:  "sk-test",
						APIBase: "https://api.anthropic.com/v1",
					},
					Models: map[string]config.ProviderModelConfig{
						"opus":   {Model: "claude-3-opus", Vision: true},
						"sonnet": {Model: "claude-3-sonnet"},
					},
				},
				"openai": {
					Type: "openai",
					ProviderConfig: config.ProviderConfig{
						APIKey:  "sk-openai",
						APIBase: "https://api.openai.com/v1",
					},
					Models: map[string]config.ProviderModelConfig{
						"gpt4": {Model: "gpt-4"},
					},
				},
			},
		},
	}

	models := getConfiguredModels(cfg)
	if len(models) != 3 {
		t.Errorf("Expected 3 models, got %d: %v", len(models), models)
	}
}

func TestGetConfiguredModels_NoModels(t *testing.T) {
	cfg := &config.Config{
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"anthropic": {
					Type: "anthropic",
					ProviderConfig: config.ProviderConfig{
						APIKey:  "sk-test",
						APIBase: "https://api.anthropic.com/v1",
					},
					Models: map[string]config.ProviderModelConfig{},
				},
				"openai": {
					Type: "openai",
					ProviderConfig: config.ProviderConfig{
						APIKey:  "sk-openai",
						APIBase: "https://api.openai.com/v1",
					},
					Models: map[string]config.ProviderModelConfig{},
				},
			},
		},
	}

	models := getConfiguredModels(cfg)
	if len(models) != 2 {
		t.Errorf("Expected 2 default models, got %d: %v", len(models), models)
	}
}

func TestGetConfiguredModels_NoAPIKey(t *testing.T) {
	cfg := &config.Config{
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"anthropic": {
					Type:           "anthropic",
					ProviderConfig: config.ProviderConfig{},
					Models: map[string]config.ProviderModelConfig{
						"opus": {Model: "claude-3-opus"},
					},
				},
				"openai": {
					Type: "openai",
					ProviderConfig: config.ProviderConfig{
						APIKey:  "sk-openai",
						APIBase: "https://api.openai.com/v1",
					},
					Models: map[string]config.ProviderModelConfig{
						"gpt4": {Model: "gpt-4"},
					},
				},
			},
		},
	}

	models := getConfiguredModels(cfg)
	if len(models) != 1 {
		t.Errorf("Expected 1 model (openai only), got %d: %v", len(models), models)
	}
}

func TestGetConfiguredModels_NoNamed(t *testing.T) {
	cfg := &config.Config{
		Providers: &config.ProvidersConfig{},
	}

	models := getConfiguredModels(cfg)
	if len(models) != 0 {
		t.Errorf("Expected 0 models, got %d: %v", len(models), models)
	}
}

func TestGetConfiguredModels_OnlyDefault(t *testing.T) {
	cfg := &config.Config{
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"anthropic": {
					Type:           "anthropic",
					ProviderConfig: config.ProviderConfig{APIKey: "sk-test"},
					Models:         map[string]config.ProviderModelConfig{},
				},
				"openai": {
					Type:           "openai",
					ProviderConfig: config.ProviderConfig{APIKey: "sk-openai"},
					Models:         map[string]config.ProviderModelConfig{},
				},
			},
		},
	}

	models := getConfiguredModels(cfg)
	if len(models) != 2 {
		t.Fatalf("Expected 2 models, got %d: %v", len(models), models)
	}
}

func TestCreateWorkspaceTemplates(t *testing.T) {
	tmpDir := t.TempDir()

	workspace := filepath.Join(tmpDir, "workspace")
	createWorkspaceTemplates(workspace)

	expectedFiles := []string{"AGENT.md", "MEMORY.md"}
	for _, f := range expectedFiles {
		path := filepath.Join(workspace, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Expected file %s not found at %s", f, path)
		}
	}
}

func TestCreateWorkspaceTemplates_DirCreation(t *testing.T) {
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "new-workspace")

	createWorkspaceTemplates(workspace)

	if _, err := os.Stat(workspace); os.IsNotExist(err) {
		t.Error("Workspace directory was not created")
	}
}

func TestGetConfigPath_Absolute(t *testing.T) {
	path := getConfigPath()

	if !filepath.IsAbs(path) {
		t.Errorf("getConfigPath() = %q, want absolute path", path)
	}

	if !strings.Contains(path, ".lele") {
		t.Errorf("getConfigPath() = %q, want path containing '.lele'", path)
	}

	if !strings.HasSuffix(path, "config.json") {
		t.Errorf("getConfigPath() = %q, want path ending with 'config.json'", path)
	}
}

func TestGetLeleDir_Home(t *testing.T) {
	dir := getLeleDir()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home directory: %v", err)
	}

	expected := filepath.Join(home, ".lele")
	if dir != expected {
		t.Errorf("getLeleDir() = %q, want %q", dir, expected)
	}
}
