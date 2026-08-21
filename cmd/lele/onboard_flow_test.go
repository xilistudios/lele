package main

import (
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// Tests for the interactive prompt helpers in onboard.go.

func TestAskYesNo_DefaultYes_Empty(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("\n") // empty -> default yes
	p.close()

	_ = captureStdout(t)
	if !askYesNo("Proceed?", true) {
		t.Error("askYesNo(defaultYes) with empty input should return true")
	}
}

func TestAskYesNo_DefaultNo_Empty(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("\n")
	p.close()

	_ = captureStdout(t)
	if askYesNo("Proceed?", false) {
		t.Error("askYesNo(defaultNo) with empty input should return false")
	}
}

func TestAskYesNo_YesVariants(t *testing.T) {
	for _, in := range []string{"y\n", "Y\n", "yes\n", "YES\n", " y \n"} {
		p := newStdinPipe(t)
		p.feed(in)
		p.close()
		_ = captureStdout(t)
		if !askYesNo("Proceed?", false) {
			t.Errorf("askYesNo with input %q should return true", in)
		}
	}
}

func TestAskYesNo_NoVariants(t *testing.T) {
	for _, in := range []string{"n\n", "N\n", "no\n", "not-yes\n"} {
		p := newStdinPipe(t)
		p.feed(in)
		p.close()
		_ = captureStdout(t)
		if askYesNo("Proceed?", true) {
			t.Errorf("askYesNo with input %q should return false", in)
		}
	}
}

func TestAskString_ReturnsValue(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("Hello\n")
	p.close()
	_ = captureStdout(t)
	if got := askString("Name", "def"); got != "Hello" {
		t.Errorf("askString = %q, want %q", got, "Hello")
	}
}

func TestAskString_TrimsWhitespace(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("  spaced  \n")
	p.close()
	_ = captureStdout(t)
	if got := askString("Name", "def"); got != "spaced" {
		t.Errorf("askString trims whitespace, got %q", got)
	}
}

func TestAskString_EmptyReturnsDefault(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("\n")
	p.close()
	_ = captureStdout(t)
	if got := askString("Name", "default-value"); got != "default-value" {
		t.Errorf("askString empty -> default, got %q", got)
	}
}

func TestAskString_NoDefaultEmpty(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("\n")
	p.close()
	_ = captureStdout(t)
	if got := askString("Name", ""); got != "" {
		t.Errorf("askString empty no-default, got %q", got)
	}
}

func TestAskSelect_DefaultIndex(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("\n") // empty -> default
	p.close()
	_ = captureStdout(t)
	got := askSelect("Pick", []string{"a", "b", "c"}, 1)
	if got != 1 {
		t.Errorf("askSelect default = %d, want 1", got)
	}
}

func TestAskSelect_ValidChoice(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("5\n")
	p.close()
	_ = captureStdout(t)
	got := askSelect("Pick", []string{"a", "b", "c", "d", "e", "f"}, 0)
	if got != 4 {
		t.Errorf("askSelect choice 5 = %d, want 4", got)
	}
}

func TestAskSelect_OutOfRangeFallsBackToDefault(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("99\n")
	p.close()
	_ = captureStdout(t)
	got := askSelect("Pick", []string{"a", "b", "c"}, 2)
	if got != 2 {
		t.Errorf("askSelect out-of-range = %d, want default 2", got)
	}
}

func TestAskSelect_ZeroFallsBackToDefault(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("0\n")
	p.close()
	_ = captureStdout(t)
	got := askSelect("Pick", []string{"a", "b", "c"}, 0)
	if got != 0 {
		t.Errorf("askSelect zero = %d, want default 0", got)
	}
}

func TestAskSelect_NonNumericFallsBackToDefault(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("abc\n")
	p.close()
	_ = captureStdout(t)
	got := askSelect("Pick", []string{"a", "b", "c"}, 0)
	if got != 0 {
		t.Errorf("askSelect non-numeric = %d, want default 0", got)
	}
}

func TestAskSelect_SingleOption(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("1\n")
	p.close()
	_ = captureStdout(t)
	got := askSelect("Pick", []string{"only"}, 0)
	if got != 0 {
		t.Errorf("askSelect single = %d, want 0", got)
	}
}

func TestAskInt_Default(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("\n")
	p.close()
	_ = captureStdout(t)
	if got := askInt("Num", 42); got != 42 {
		t.Errorf("askInt default = %d, want 42", got)
	}
}

func TestAskInt_Value(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("100\n")
	p.close()
	_ = captureStdout(t)
	if got := askInt("Num", 42); got != 100 {
		t.Errorf("askInt = %d, want 100", got)
	}
}

func TestAskInt_ZeroFallsBackToDefault(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("0\n")
	p.close()
	_ = captureStdout(t)
	if got := askInt("Num", 42); got != 42 {
		t.Errorf("askInt zero = %d, want default 42", got)
	}
}

func TestAskInt_NonNumericFallsBackToDefault(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("abc\n")
	p.close()
	_ = captureStdout(t)
	if got := askInt("Num", 42); got != 42 {
		t.Errorf("askInt non-numeric = %d, want default 42", got)
	}
}

func TestAskFloat_Default(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("\n")
	p.close()
	_ = captureStdout(t)
	if got := askFloat("Temp", 0.7); got != 0.7 {
		t.Errorf("askFloat default = %v, want 0.7", got)
	}
}

func TestAskFloat_Value(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("1.5\n")
	p.close()
	_ = captureStdout(t)
	if got := askFloat("Temp", 0.7); got != 1.5 {
		t.Errorf("askFloat = %v, want 1.5", got)
	}
}

func TestAskFloat_ZeroFallsBackToDefault(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("0\n")
	p.close()
	_ = captureStdout(t)
	if got := askFloat("Temp", 0.7); got != 0.7 {
		t.Errorf("askFloat zero = %v, want default 0.7", got)
	}
}

func TestAskFloat_NonNumericFallsBackToDefault(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("abc\n")
	p.close()
	_ = captureStdout(t)
	if got := askFloat("Temp", 0.7); got != 0.7 {
		t.Errorf("askFloat non-numeric = %v, want default 0.7", got)
	}
}

func TestAskSecret_ReadsLine(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("sk-super-secret-key\n")
	p.close()
	_ = captureStdout(t)
	if got := askSecret("API Key"); got != "sk-super-secret-key" {
		t.Errorf("askSecret = %q, want %q", got, "sk-super-secret-key")
	}
}

func TestAskSecret_EOFReturnsEmpty(t *testing.T) {
	p := newStdinPipe(t)
	p.close() // EOF immediately
	_ = captureStdout(t)
	if got := askSecret("API Key"); got != "" {
		t.Errorf("askSecret on EOF = %q, want empty", got)
	}
}

func TestPrintSummary_WithProvidersAndAgents(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	temp := 0.8
	cfg.Agents.Defaults.Temperature = &temp
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"anthropic": {
			Type: "anthropic",
			ProviderConfig: config.ProviderConfig{
				APIKey:  "sk-1234abcd5678efgh",
				APIBase: "https://api.anthropic.com/v1",
			},
			Models: map[string]config.ProviderModelConfig{
				"opus": {Model: "claude-opus-4", Vision: true},
			},
		},
		"empty": {Type: "empty"}, // skipped (no key/base)
	}
	agentTemp := 0.5
	cfg.Agents.List = []config.AgentConfig{
		{Name: "helper", Model: &config.AgentModelConfig{Primary: "gpt-4"}, Temperature: &agentTemp},
	}

	out := runCmd(func() { printSummary(cfg) })

	if !strings.Contains(out, "anthropic") {
		t.Errorf("summary should contain provider name 'anthropic'")
	}
	if !strings.Contains(out, "sk-1...efgh") {
		t.Errorf("summary should contain masked key 'sk-1...efgh', got: %s", out)
	}
	if !strings.Contains(out, "[vision]") {
		t.Errorf("summary should include vision marker")
	}
	if !strings.Contains(out, "helper") {
		t.Errorf("summary should include extra agent name")
	}
}

func TestPrintSummary_SkipEmptyModels(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	temp := 0.7
	cfg.Agents.Defaults.Temperature = &temp
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			ProviderConfig: config.ProviderConfig{
				APIKey:  "sk-openai-key-1234",
				APIBase: "https://api.openai.com/v1",
			},
		},
	}
	out := runCmd(func() { printSummary(cfg) })
	if !strings.Contains(out, "default") {
		t.Errorf("summary should show 'default' when provider has no model aliases: %s", out)
	}
}

func TestPrintSummary_NoAPIKeyShowsNoKey(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	temp := 0.7
	cfg.Agents.Defaults.Temperature = &temp
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			ProviderConfig: config.ProviderConfig{
				APIBase: "https://api.openai.com/v1",
			},
		},
	}
	out := runCmd(func() { printSummary(cfg) })
	if !strings.Contains(out, "(no key)") {
		t.Errorf("summary should show '(no key)' when APIKey empty: %s", out)
	}
}

func TestConfigureProvider_Local(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	info := providerInfo{name: "ollama", displayName: "Ollama (local)", typeKey: "ollama", apiBase: "http://localhost:11434/v1", authHeader: "Bearer", local: true}

	p := newStdinPipe(t)
	p.feedLines("", "n", "n") // api base (default), no proxy, no models
	p.close()
	_ = captureStdout(t)
	configureProvider(cfg, info)

	if cfg.Providers.Ollama.APIBase == "" {
		t.Error("Ollama APIBase should be set")
	}
	if _, ok := cfg.Providers.Named["ollama"]; !ok {
		t.Error("Ollama should be registered in Named map")
	}
}

func TestConfigureProvider_OpenAI(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	info := providerInfo{name: "openai", displayName: "OpenAI (GPT)", typeKey: "openai", apiBase: "http://localhost:11434/v1", authHeader: "Bearer", local: true}

	p := newStdinPipe(t)
	// api base (default), proxy? no, model aliases? no
	p.feedLines("", "n", "n")
	p.close()
	_ = captureStdout(t)
	configureProvider(cfg, info)

	if cfg.Providers.OpenAI.APIBase != "http://localhost:11434/v1" {
		t.Errorf("OpenAI APIBase = %q", cfg.Providers.OpenAI.APIBase)
	}
	np, ok := cfg.Providers.Named["openai"]
	if !ok {
		t.Fatal("openai not in Named map")
	}
	if np.APIBase != "http://localhost:11434/v1" {
		t.Errorf("Named openai APIBase = %q", np.APIBase)
	}
}

func TestConfigureProvider_AnthropicTypeMapping(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	info := providerInfo{name: "anthropic", displayName: "Anthropic (Claude)", typeKey: "anthropic", apiBase: "http://localhost:11434/v1", authHeader: "x-api-key", local: true}

	p := newStdinPipe(t)
	p.feedLines("", "n", "n") // base default, no proxy, no models
	p.close()
	_ = captureStdout(t)
	configureProvider(cfg, info)

	if cfg.Providers.Anthropic.APIBase != "http://localhost:11434/v1" {
		t.Errorf("Anthropic APIBase not set: %q", cfg.Providers.Anthropic.APIBase)
	}
}

func TestConfigureProvider_ConfigureModels(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	info := providerInfo{name: "openai", displayName: "OpenAI (GPT)", typeKey: "openai", apiBase: "http://localhost:11434/v1", authHeader: "Bearer", local: true}

	p := newStdinPipe(t)
	p.feedLines(
		"",
		"n",
		"y",
		"gpt4",
		"gpt-4o",
		"",    // vision? default yes
		"n",   // configure context window? no
		"n",   // add another? no
	)
	p.close()
	_ = captureStdout(t)
	configureProvider(cfg, info)

	np := cfg.Providers.Named["openai"]
	if len(np.Models) != 1 {
		t.Fatalf("expected 1 model, got %d: %v", len(np.Models), np.Models)
	}
	mc := np.Models["gpt4"]
	if mc.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", mc.Model)
	}
	if !mc.Vision {
		t.Error("vision should be true (default yes)")
	}
}

func TestConfigureProvider_CustomTypeNoMatch(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	info := providerInfo{name: "customthing", displayName: "Custom...", typeKey: "customthing", apiBase: "http://localhost:9999/v1", authHeader: "Bearer", local: true}

	p := newStdinPipe(t)
	p.feedLines("", "n", "n")
	p.close()
	_ = captureStdout(t)
	configureProvider(cfg, info)

	np, ok := cfg.Providers.Named["customthing"]
	if !ok {
		t.Fatal("customthing not stored in Named")
	}
	if np.Type != "customthing" {
		t.Errorf("type = %q", np.Type)
	}
}

func TestConfigureProviders_SelectCustom(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	// First askSelect: common 8 + "[Show all]" (9 options). Choose 9 -> all shown.
	// Second askSelect: 16 providers. Custom at index 15 -> choose 16.
	// Then configureProviders requires provider name, then configureProvider:
	// api key (empty, non-local), base (default), proxy (no), models (no).
	p := newStdinPipe(t)
	p.feedLines("9", "16", "mycustom", "", "", "n", "n", "n")
	p.close()
	_ = captureStdout(t)
	configureProviders(cfg)

	np, ok := cfg.Providers.Named["mycustom"]
	if !ok {
		t.Fatalf("custom provider not registered: %v", cfg.Providers.Named)
	}
	if np.Type != "mycustom" {
		t.Errorf("custom provider type = %q, want mycustom", np.Type)
	}
}

func TestSelectModel_NoConfiguredModels(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	p := newStdinPipe(t)
	p.feed("my-model\n")
	p.close()
	_ = captureStdout(t)
	if got := selectModel(cfg, "Default model", "fallback"); got != "my-model" {
		t.Errorf("selectModel = %q, want my-model", got)
	}
}

func TestSelectModel_WithModelsEnterManual(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: "b"},
			Models:         map[string]config.ProviderModelConfig{"gpt4": {Model: "gpt-4o"}},
		},
	}
	// options = ["openai:gpt4", "[Enter manually]"] -> len 2; choose index1 = "2"
	p := newStdinPipe(t)
	p.feed("2\ncustom\n")
	p.close()
	_ = captureStdout(t)
	if got := selectModel(cfg, "Model", "def"); got != "custom" {
		t.Errorf("selectModel manual = %q, want custom", got)
	}
}

func TestSelectModel_ChooseFirst(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: "b"},
			Models:         map[string]config.ProviderModelConfig{"gpt4": {Model: "gpt-4o"}},
		},
	}
	p := newStdinPipe(t)
	p.feed("\n") // empty -> default index 0
	p.close()
	_ = captureStdout(t)
	if got := selectModel(cfg, "Model", "def"); got != "openai:gpt4" {
		t.Errorf("selectModel = %q, want openai:gpt4", got)
	}
}

func TestConfigureAgentDefaults(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Providers.Named = nil
	p := newStdinPipe(t)
	p.feedLines("openai:gpt4-5", "4096", "0.9", "10")
	p.close()
	_ = captureStdout(t)
	configureAgentDefaults(cfg)

	if cfg.Agents.Defaults.Model != "openai:gpt4-5" {
		t.Errorf("model = %q", cfg.Agents.Defaults.Model)
	}
	if cfg.Agents.Defaults.Provider != "openai" {
		t.Errorf("provider = %q, want openai", cfg.Agents.Defaults.Provider)
	}
	if cfg.Agents.Defaults.MaxTokens != 4096 {
		t.Errorf("maxTokens = %d", cfg.Agents.Defaults.MaxTokens)
	}
	if cfg.Agents.Defaults.Temperature == nil || *cfg.Agents.Defaults.Temperature != 0.9 {
		t.Errorf("temp = %v", cfg.Agents.Defaults.Temperature)
	}
	if cfg.Agents.Defaults.MaxToolIterations != 10 {
		t.Errorf("maxToolIterations = %d", cfg.Agents.Defaults.MaxToolIterations)
	}
}

func TestConfigureAgentDefaults_UseFirstConfigured(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"anthropic": {
			Type: "anthropic", ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: "b"},
			Models: map[string]config.ProviderModelConfig{"opus": {Model: "claude-opus"}},
		},
	}
	p := newStdinPipe(t)
	p.feedLines("", "", "", "")
	p.close()
	_ = captureStdout(t)
	configureAgentDefaults(cfg)
	if cfg.Agents.Defaults.Model != "anthropic:opus" {
		t.Errorf("model = %q, want anthropic:opus", cfg.Agents.Defaults.Model)
	}
	if cfg.Agents.Defaults.Provider != "anthropic" {
		t.Errorf("provider = %q", cfg.Agents.Defaults.Provider)
	}
}

func TestConfigureAdditionalAgents_No(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	p := newStdinPipe(t)
	p.feed("n\n")
	p.close()
	_ = captureStdout(t)
	configureAdditionalAgents(cfg)
	if len(cfg.Agents.List) != 0 {
		t.Errorf("expected no agents, got %d", len(cfg.Agents.List))
	}
}

func TestConfigureAdditionalAgents_OneAgentWithSkills(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Providers.Named = map[string]config.NamedProviderConfig{}

	p := newStdinPipe(t)
	p.feedLines(
		"y",      // add additional agents
		"Helper", // name
		"",       // id default
		"",       // model default
		"0.6",    // temp
		"y",      // add skills?
		"weather", // skill
		"n",      // another skill?
		"n",      // add another agent?
	)
	p.close()
	_ = captureStdout(t)
	configureAdditionalAgents(cfg)

	if len(cfg.Agents.List) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Agents.List))
	}
	a := cfg.Agents.List[0]
	if a.Name != "Helper" {
		t.Errorf("agent name = %q", a.Name)
	}
	if a.ID != "helper" {
		t.Errorf("agent id = %q, want helper", a.ID)
	}
	if len(a.Skills) != 1 || a.Skills[0] != "weather" {
		t.Errorf("skills = %v", a.Skills)
	}
}

func TestConfigureWebUI(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	p := newStdinPipe(t)
	p.feedLines("9999", "n")
	p.close()
	_ = captureStdout(t)
	configureWebUI(cfg, t.TempDir())

	if !cfg.Channels.Web.Enabled {
		t.Error("web should be enabled")
	}
	if cfg.Server.Port != 9999 {
		t.Errorf("port = %d, want 9999", cfg.Server.Port)
	}
	if !cfg.Channels.Native.Enabled {
		t.Error("native channel should be enabled")
	}
}

func TestConfigureNativeAdvanced(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	p := newStdinPipe(t)
	p.feedLines("10", "60")
	p.close()
	_ = captureStdout(t)
	configureNativeAdvanced(cfg)

	if cfg.Channels.Native.MaxClients != 10 {
		t.Errorf("max clients = %d", cfg.Channels.Native.MaxClients)
	}
	if cfg.Channels.Native.TokenExpiryDays != 60 {
		t.Errorf("token expiry = %d", cfg.Channels.Native.TokenExpiryDays)
	}
}

func TestMaybeGeneratePIN_WebDisabled(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Channels.Web.Enabled = false
	_ = captureStdout(t)
	maybeGeneratePIN(cfg, t.TempDir())
}

func TestMaybeGeneratePIN_NoPINRequested(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Channels.Web.Enabled = true
	p := newStdinPipe(t)
	p.feed("n\n")
	p.close()
	_ = captureStdout(t)
	maybeGeneratePIN(cfg, t.TempDir())
}

func TestMaybeGeneratePIN_Generate(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Channels.Web.Enabled = true
	dir := t.TempDir()
	p := newStdinPipe(t)
	p.feedLines("y", "My Device")
	p.close()
	out := runCmd(func() { maybeGeneratePIN(cfg, dir) })
	if !strings.Contains(out, "Pairing PIN") {
		t.Errorf("output should mention PIN, got: %s", out)
	}
}

func TestMaybeStartServices_WebDisabled(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Channels.Web.Enabled = false
	_ = captureStdout(t)
	maybeStartServices(cfg)
}

func TestMaybeStartServices_WontStart(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Channels.Web.Enabled = true
	p := newStdinPipe(t)
	p.feed("n\n")
	p.close()
	out := runCmd(func() { maybeStartServices(cfg) })
	if !strings.Contains(out, "lele gateway") {
		t.Errorf("should print manual start instructions, got: %s", out)
	}
}

func TestConfigureModels_NoAliasStops(t *testing.T) {
	// enter empty alias -> break immediately
	p := newStdinPipe(t)
	p.feed("\n")
	p.close()
	_ = captureStdout(t)
	models := configureModels("test")
	if len(models) != 0 {
		t.Errorf("expected 0 models, got %d", len(models))
	}
}

func TestConfigureModels_SingleModel(t *testing.T) {
	// alias, model, vision (default yes via empty), ctx? no, add another? no
	p := newStdinPipe(t)
	p.feedLines("fast", "gpt-4o-mini", "", "n", "n")
	p.close()
	_ = captureStdout(t)
	models := configureModels("test")
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models["fast"].Model != "gpt-4o-mini" {
		t.Errorf("model = %q", models["fast"].Model)
	}
}

func TestOnboard_ConfigExistsNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfgPath := getConfigPath()
	if err := os.WriteFile(cfgPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	p := newStdinPipe(t)
	p.feed("n\n")
	p.close()
	out := runCmd(func() { onboard() })
	if !strings.Contains(out, "Aborted") {
		t.Errorf("onboard with no-overwrite should print Aborted, got: %s", out)
	}
}