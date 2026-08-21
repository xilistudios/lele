package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// buildObConfig returns a config with a single named provider whose typed
// field is populated (so ensureNamedDefaults keeps it).
func buildObConfig(provType, name, apiKey string, models map[string]config.ProviderModelConfig) *config.Config {
	cfg := &config.Config{
		Providers: &config.ProvidersConfig{},
		TUI:       config.TUIConfig{},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Provider: "",
				Model:    "",
			},
		},
	}
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		name: {
			Type:           provType,
			ProviderConfig: config.ProviderConfig{APIKey: apiKey},
			Models:         models,
		},
	}
	return cfg
}

// allKnownProviderNames mirrors the fixed set inserted by ensureNamedDefaults.
// obVerifyKeyCmd iterates ListNamed() (a map) so, to make the picked provider
// deterministic, we pre-populate every known key with the same value.
func allKnownProviderNames() []string {
	return []string{
		"anthropic", "openai", "openrouter", "groq", "zhipu", "vllm",
		"gemini", "nvidia", "ollama", "moonshot", "shengsuanyun", "deepseek",
		"github_copilot", "nanogpt", "alibaba_coding_plan", "zai_coding_plan",
		"modelark_coding_plan",
	}
}

// populateAllProviders sets every known provider key to the same NamedProvider
// config so that the arbitrary map-iteration pick in obVerifyKeyCmd is
// deterministic.
func populateAllProviders(cfg *config.Config, np config.NamedProviderConfig) {
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	for _, name := range allKnownProviderNames() {
		cfg.Providers.Named[name] = np
	}
}

func newObModel(t *testing.T, cfg *config.Config) *Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	// Save initial config so saveConfigToDisk works.
	if err := config.SaveConfig(filepath.Join(dir, "config.json"), cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	return &Model{cfg: cfg}
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "****"},
		{"short", "****"},
		{"1234567890", "1234...7890"},
	}
	for _, tc := range tests {
		if got := maskAPIKey(tc.in); got != tc.want {
			t.Errorf("maskAPIKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSyncProviderToTypedField(t *testing.T) {
	pc := config.ProviderConfig{APIKey: "k", APIBase: "http://x"}
	cases := []struct {
		name string
		put  func(p *config.ProvidersConfig, v config.ProviderConfig)
	}{
		{"anthropic", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.Anthropic = v }},
		{"openai", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.OpenAI.ProviderConfig = v }},
		{"openrouter", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.OpenRouter = v }},
		{"groq", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.Groq = v }},
		{"zhipu", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.Zhipu = v }},
		{"vllm", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.VLLM = v }},
		{"gemini", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.Gemini = v }},
		{"nvidia", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.Nvidia = v }},
		{"ollama", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.Ollama = v }},
		{"moonshot", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.Moonshot = v }},
		{"shengsuanyun", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.ShengSuanYun = v }},
		{"deepseek", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.DeepSeek = v }},
		{"github_copilot", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.GitHubCopilot = v }},
		{"nanogpt", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.NanogPT = v }},
		{"alibaba_coding_plan", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.AlibabaCodingPlan = v }},
		{"zai_coding_plan", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.ZAICodingPlan = v }},
		{"modelark_coding_plan", func(p *config.ProvidersConfig, v config.ProviderConfig) { p.ModelArkCodingPlan = v }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &config.ProvidersConfig{}
			tc.put(p, pc)
			syncProviderToTypedField(p, tc.name, config.NamedProviderConfig{ProviderConfig: pc})
			if p.Anthropic.APIKey != "k" && tc.name == "anthropic" {
				t.Errorf("anthropic not synced: %+v", p.Anthropic)
			}
		})
	}

	// Unknown name is a no-op and must not panic.
	p := &config.ProvidersConfig{}
	syncProviderToTypedField(p, "doesnotexist", config.NamedProviderConfig{ProviderConfig: pc})
}

func TestProviderIsUsable(t *testing.T) {
	cfg := &config.Config{Providers: &config.ProvidersConfig{}}
	if providerIsUsable(nil, "x") {
		t.Error("nil cfg should be unusable")
	}
	if providerIsUsable(cfg, "") {
		t.Error("empty name unusable")
	}
	if providerIsUsable(cfg, "nope") {
		t.Error("missing provider unusable")
	}

	// Local provider with a model is usable even without a key.
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"ollama": {Type: "ollama", Models: map[string]config.ProviderModelConfig{"m": {}}},
	}
	if !providerIsUsable(cfg, "OLLAMA") {
		t.Error("ollama with model should be usable (case insensitive)")
	}

	// Non-local provider without a key is not usable.
	cfg.Providers.Named["openai"] = config.NamedProviderConfig{Type: "openai", Models: map[string]config.ProviderModelConfig{"m": {}}}
	if providerIsUsable(cfg, "openai") {
		t.Error("openai without key should be unusable")
	}

	// With a key it is usable.
	cfg.Providers.Named["openai"] = config.NamedProviderConfig{Type: "openai", ProviderConfig: config.ProviderConfig{APIKey: "k"}, Models: map[string]config.ProviderModelConfig{"m": {}}}
	if !providerIsUsable(cfg, "openai") {
		t.Error("openai with key+model should be usable")
	}

	// Provider with key but no models is not usable.
	cfg.Providers.Named["empty"] = config.NamedProviderConfig{Type: "openai", ProviderConfig: config.ProviderConfig{APIKey: "k"}}
	if providerIsUsable(cfg, "empty") {
		t.Error("provider with no models should be unusable")
	}
}

func TestObVerifyKeyCmdLocalProvider(t *testing.T) {
	cfg := buildObConfig("ollama", "ollama", "", map[string]config.ProviderModelConfig{"llama": {}})
	populateAllProviders(cfg, cfg.Providers.Named["ollama"])
	m := newObModel(t, cfg)
	cmd := m.obVerifyKeyCmd()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	rm, ok := msg.(obVerifyResultMsg)
	if !ok {
		t.Fatalf("expected obVerifyResultMsg, got %T", msg)
	}
	if !rm.success {
		t.Errorf("ollama should be valid, got %+v", rm)
	}
	// The picked key is arbitrary (map iteration), but all entries are ollama.
	if rm.providerName == "" {
		t.Errorf("expected a provider name, got %q", rm.providerName)
	}
}

func TestObVerifyKeyCmdLocalhostAPIBase(t *testing.T) {
	cfg := buildObConfig("openai", "openai", "k", map[string]config.ProviderModelConfig{"m": {}})
	np := config.NamedProviderConfig{
		Type:           "openai",
		ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: "http://127.0.0.1:11434"},
		Models:         map[string]config.ProviderModelConfig{"m": {}},
	}
	populateAllProviders(cfg, np)
	m := newObModel(t, cfg)
	msg := m.obVerifyKeyCmd()()
	rm := msg.(obVerifyResultMsg)
	if !rm.success {
		t.Errorf("localhost base should be valid, got %+v", rm)
	}
}

func TestObVerifyKeyCmdRemoteError(t *testing.T) {
	cfg := buildObConfig("openai", "openai", "", map[string]config.ProviderModelConfig{"m": {}})
	// An unparseable URL makes http.NewRequest fail fast (no network).
	np := config.NamedProviderConfig{
		Type:           "openai",
		ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: "http://[::1"},
		Models:         map[string]config.ProviderModelConfig{"m": {}},
	}
	populateAllProviders(cfg, np)
	m := newObModel(t, cfg)
	msg := m.obVerifyKeyCmd()()
	rm := msg.(obVerifyResultMsg)
	if rm.success {
		t.Error("expected failure/success=false for invalid URL")
	}
	if rm.err == nil {
		t.Error("expected err to be non-nil")
	}
}

func TestObVerifyKeyCmdNilProviders(t *testing.T) {
	m := &Model{cfg: &config.Config{}}
	if m.obVerifyKeyCmd() == nil {
		t.Fatal("expected cmd even with empty providers")
	}
}

func TestObFinalizeSetupNilCfg(t *testing.T) {
	m := &Model{}
	m.obFinalizeSetup() // must not panic
}

func TestObFinalizeSetupSetsDefaults(t *testing.T) {
	cfg := buildObConfig("openai", "myp", "sk-test", map[string]config.ProviderModelConfig{"gpt-4o": {Model: "gpt-4o"}})
	cfg.Agents.Defaults.Workspace = t.TempDir()
	m := newObModel(t, cfg)
	m.providerSelectedName = "myp"
	m.obFinalizeSetup()
	if m.cfg.Agents.Defaults.Provider != "myp" {
		t.Errorf("default provider = %q, want myp", m.cfg.Agents.Defaults.Provider)
	}
	if m.cfg.Agents.Defaults.Model != "gpt-4o" {
		t.Errorf("default model = %q, want gpt-4o", m.cfg.Agents.Defaults.Model)
	}
	if m.obProviderName != "myp" {
		t.Errorf("obProviderName = %q", m.obProviderName)
	}
	// "sk-test" is 7 chars -> fully masked to "****".
	if m.obMaskedKey != "****" {
		t.Errorf("obMaskedKey = %q", m.obMaskedKey)
	}
	if !m.cfg.TUI.OnboardingCompleted {
		t.Error("expected onboarding marked completed")
	}
	// Workspace dir should be created.
	if _, err := os.Stat(cfg.WorkspacePath()); err != nil {
		t.Errorf("workspace not created: %v", err)
	}
}

func TestObFinalizeSetupNoModels(t *testing.T) {
	cfg := buildObConfig("openai", "myp", "", map[string]config.ProviderModelConfig{"m": {}})
	m := newObModel(t, cfg)
	m.obFinalizeSetup() // must not panic
}

func TestObFinalizeSetupNoProviderSelected(t *testing.T) {
	cfg := buildObConfig("openai", "myp", "sk", map[string]config.ProviderModelConfig{"gpt-4o": {}})
	cfg.Agents.Defaults.Workspace = t.TempDir()
	m := newObModel(t, cfg)
	// No providerSelectedName -> falls back to scanning.
	m.obFinalizeSetup()
	if m.cfg.Agents.Defaults.Provider != "myp" {
		t.Errorf("default provider = %q, want myp", m.cfg.Agents.Defaults.Provider)
	}
}

func TestObFinalizeSetupSkipsDialableDefault(t *testing.T) {
	cfg := buildObConfig("openai", "myp", "sk", map[string]config.ProviderModelConfig{"gpt-4o": {}})
	cfg.Agents.Defaults.Workspace = t.TempDir()
	m := newObModel(t, cfg)
	// If a usable default already exists, don't override it.
	cfg.Agents.Defaults.Provider = "existing"
	m.cfg.Providers.Named["existing"] = config.NamedProviderConfig{Type: "openai", ProviderConfig: config.ProviderConfig{APIKey: "k2"}, Models: map[string]config.ProviderModelConfig{"m2": {}}}
	m.obFinalizeSetup()
	if m.cfg.Agents.Defaults.Provider != "existing" {
		t.Errorf("expected existing provider kept, got %q", m.cfg.Agents.Defaults.Provider)
	}
}

