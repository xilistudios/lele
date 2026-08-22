package main

import (
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// configureProviderSwitch runs configureProvider for a single provider type
// using a local (no API key prompt) config with decline-all answers.
func configureProviderSwitch(t *testing.T, name, displayName, typeKey string) {
	t.Helper()
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
	// local=true skips the API key prompt and validation.
	info := providerInfo{name: name, displayName: displayName, typeKey: typeKey,
		apiBase: "localhost:4321", authHeader: "Bearer", local: true}
	p := newStdinPipe(t)
	p.feedLines(
		"localhost:4321\n", // API Base
		"n\n",              // proxy? no
		"n\n",              // model aliases? no
	)
	p.close()
	_ = captureStdout(t)
	configureProvider(cfg, info)

	if cfg.Providers.Named[name].APIBase != "localhost:4321" {
		t.Errorf("%s named base not set", name)
	}
}

// These cover the configureProvider typeKey switch cases.
func TestConfigureProvider_SwitchOpenAI(t *testing.T)  { configureProviderSwitch(t, "openai", "OpenAI", "openai") }
func TestConfigureProvider_SwitchOpenRouter(t *testing.T) {
	configureProviderSwitch(t, "openrouter", "OpenRouter", "openrouter")
}
func TestConfigureProvider_SwitchGroq(t *testing.T)  { configureProviderSwitch(t, "groq", "Groq", "groq") }
func TestConfigureProvider_SwitchDeepSeek(t *testing.T) {
	configureProviderSwitch(t, "deepseek", "DeepSeek", "deepseek")
}
func TestConfigureProvider_SwitchGemini(t *testing.T) {
	configureProviderSwitch(t, "gemini", "Gemini", "gemini")
}
func TestConfigureProvider_SwitchZhipu(t *testing.T) { configureProviderSwitch(t, "zhipu", "Zhipu", "zhipu") }
func TestConfigureProvider_SwitchOllama(t *testing.T) {
	configureProviderSwitch(t, "ollama", "Ollama", "ollama")
}
func TestConfigureProvider_SwitchNvidia(t *testing.T) {
	configureProviderSwitch(t, "nvidia", "NVIDIA", "nvidia")
}
func TestConfigureProvider_SwitchMoonshot(t *testing.T) {
	configureProviderSwitch(t, "moonshot", "Moonshot", "moonshot")
}
func TestConfigureProvider_SwitchVLLM(t *testing.T) { configureProviderSwitch(t, "vllm", "VLLM", "vllm") }
func TestConfigureProvider_SwitchShengSuanYun(t *testing.T) {
	configureProviderSwitch(t, "shengsuanyun", "ShengSuanYun", "shengsuanyun")
}
func TestConfigureProvider_SwitchAlibabaCodingPlan(t *testing.T) {
	configureProviderSwitch(t, "alibaba_coding_plan", "Alibaba", "alibaba_coding_plan")
}
func TestConfigureProvider_SwitchGitHubCopilot(t *testing.T) {
	configureProviderSwitch(t, "github_copilot", "Copilot", "github_copilot")
}
func TestConfigureProvider_SwitchNanoGPT(t *testing.T) {
	configureProviderSwitch(t, "nanogpt", "NanoGPT", "nanogpt")
}
func TestConfigureProvider_SwitchAnthropic(t *testing.T) {
	configureProviderSwitch(t, "anthropic", "Anthropic", "anthropic")
}

// TestConfigureProvider_CustomType exercises an unknown typeKey (default case;
// the switch falls through without a top-level field assignment).
func TestConfigureProvider_UnknownTypeV4(t *testing.T) {
	configureProviderSwitch(t, "weird", "Weird", "totally_unknown")
}

var _ = config.DefaultConfig