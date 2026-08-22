package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// runMainSubprocessExpectExit runs the main subprocess harness expecting a
// non-zero exit (e.g. commands that os.Exit(1) on error) and returns combined
// output.
func runMainSubprocessExpectExit(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestMainPlaceholder")
	cmd.Env = append(os.Environ(), "LELE_TEST_MAIN="+strings.Join(args, recordSep))
	out, _ := cmd.CombinedOutput() // expected non-zero exit
	return string(out)
}

// --- updateCmd / migrateCmd in-process flag handling ---
// updateCmd and migrateCmd call os.Exit(1) on some error paths, but their
// --help branches return normally and can be exercised in-process via
// replaceArgs.

// TestUpdateCmd_HelpV4 runs updateCmd --help in-process (returns normally).
func TestUpdateCmd_HelpV4(t *testing.T) {
	replaceArgs(t, []string{"lele", "update", "--help"})
	out := runCmd(updateCmd)
	if !strings.Contains(out, "lele update - Update") {
		t.Errorf("expected update help, got: %s", out)
	}
}

// TestMigrateCmd_HelpV4 runs migrateCmd --help in-process (returns normally).
func TestMigrateCmd_HelpV4(t *testing.T) {
	replaceArgs(t, []string{"lele", "migrate", "--help"})
	out := runCmd(migrateCmd)
	if !strings.Contains(out, "Migrate from OpenClaw to Lele") {
		t.Errorf("expected migrate help, got: %s", out)
	}
}

// --- config-backed helper coverage ---

// Note: migrateStorageCmd's --rollback and unknown-flag paths call os.Exit(1)
// and therefore cannot contribute in-process coverage; they are excluded.

// TestGetConfiguredModelsV4 covers getConfiguredModels' branches for named
// providers with/without models.
func TestGetConfiguredModelsV4(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = make(map[string]config.NamedProviderConfig)
	}
	// Provider with models -> prov:alias
	cfg.Providers.Named["openai"] = config.NamedProviderConfig{
		Type:           "openai",
		ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: "http://x"},
		Models:         map[string]config.ProviderModelConfig{"gpt4": {Model: "gpt-4"}},
	}
	// Provider without models but with key -> prov/default
	cfg.Providers.Named["groq"] = config.NamedProviderConfig{
		Type:           "groq",
		ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: "http://y"},
	}
	// Provider with empty key+base -> skipped
	cfg.Providers.Named["skipme"] = config.NamedProviderConfig{}

	models := getConfiguredModels(cfg)
	found := map[string]bool{}
	for _, m := range models {
		found[m] = true
	}
	if !found["openai:gpt4"] {
		t.Errorf("expected openai:gpt4 in models, got %v", models)
	}
	if !found["groq/default"] {
		t.Errorf("expected groq/default in models, got %v", models)
	}
	if found["skipme"] {
		t.Errorf("skipme provider should not appear, got %v", models)
	}
}

// TestConfigureProvider_LocalNoValidation covers configureProvider for a local
// provider (no API key prompt, no validation call).
func TestConfigureProvider_FillsConfigAndSwitch(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = make(map[string]config.NamedProviderConfig)
	}

	// Local provider: skip API key, enter base, decline advanced and models.
	p := newStdinPipe(t)
	p.feedLines(
		"http://localhost:11434/v1\n", // API Base
		"n\n",                         // proxy? no
		"n\n",                         // model aliases? no
	)
	p.close()

	_ = captureStdout(t)
	configureProvider(cfg, providerInfo{
		name: "ollama", displayName: "Ollama", typeKey: "ollama",
		apiBase: "http://localhost:11434/v1", authHeader: "Bearer", local: true,
	})

	if cfg.Providers.Ollama.APIBase != "http://localhost:11434/v1" {
		t.Errorf("ollama api base = %q", cfg.Providers.Ollama.APIBase)
	}
	if cfg.Providers.Named["ollama"].APIBase != "http://localhost:11434/v1" {
		t.Errorf("named ollama base = %q", cfg.Providers.Named["ollama"].APIBase)
	}

	// Verify the typeKey switch also routes to the correct field for openai.
	cfg2, _ := defaultTestConfig()
	cfg2.Providers = &config.ProvidersConfig{}
	cfg2.Providers.Named = map[string]config.NamedProviderConfig{}
	configureProvider(cfg2, providerInfo{
		name: "openai", displayName: "OpenAI", typeKey: "openai",
		apiBase: "http://localhost:11434/v1", authHeader: "Bearer", local: true,
	})
	if cfg2.Providers.OpenAI.APIBase != "http://localhost:11434/v1" {
		t.Errorf("openai base = %q", cfg2.Providers.OpenAI.APIBase)
	}
	_ = dir
}
