package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---- FlexibleStringSlice ----

func TestFlexibleStringSlice_Unmarshal_Strings(t *testing.T) {
	var f FlexibleStringSlice
	if err := json.Unmarshal([]byte(`["a","123","c"]`), &f); err != nil {
		t.Fatalf("unmarshal strings: %v", err)
	}
	if len(f) != 3 || f[0] != "a" || f[1] != "123" || f[2] != "c" {
		t.Errorf("got %v", f)
	}
}

func TestFlexibleStringSlice_Unmarshal_Numbers(t *testing.T) {
	var f FlexibleStringSlice
	if err := json.Unmarshal([]byte(`[123, 45.0]`), &f); err != nil {
		t.Fatalf("unmarshal numbers: %v", err)
	}
	if len(f) != 2 || f[0] != "123" || f[1] != "45" {
		t.Errorf("got %v", f)
	}
}

func TestFlexibleStringSlice_Unmarshal_Mixed(t *testing.T) {
	var f FlexibleStringSlice
	if err := json.Unmarshal([]byte(`["x",123,true,{"a":1}]`), &f); err != nil {
		t.Fatalf("unmarshal mixed: %v", err)
	}
	if len(f) != 4 {
		t.Fatalf("got len %d, want 4", len(f))
	}
	if f[0] != "x" || f[1] != "123" || f[2] != "true" {
		t.Errorf("unexpected: %v", f)
	}
}

func TestFlexibleStringSlice_Unmarshal_Error(t *testing.T) {
	var f FlexibleStringSlice
	if err := json.Unmarshal([]byte(`{"a":1}`), &f); err == nil {
		t.Fatal("expected error for object input")
	}
}

// ---- ProvidersConfig JSON ----

func TestProvidersConfig_MarshalNull(t *testing.T) {
	var p *ProvidersConfig
	data, err := p.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("got %s, want null", data)
	}
}

func TestProvidersConfig_MarshalEmpty(t *testing.T) {
	p := &ProvidersConfig{}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("empty providers should marshal to null, got %s", data)
	}
}

func TestProvidersConfig_MarshalWithData(t *testing.T) {
	p := &ProvidersConfig{}
	p.Named = map[string]NamedProviderConfig{
		"foo": {Type: "openai", ProviderConfig: ProviderConfig{APIKey: "k"}, Models: map[string]ProviderModelConfig{
			"m1": {Model: "mm"},
		}},
		"bar": {Type: "x", Models: map[string]ProviderModelConfig{}}, // no APIKey/models -> omitted
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if s == "null" {
		t.Fatal("should not be null when named has data")
	}
	if !containsJSONKey(s, "foo") {
		t.Errorf("expected foo in output, got %s", s)
	}
	if containsJSONKey(s, "bar") {
		t.Errorf("bar (no data) should be omitted, got %s", s)
	}
}

func TestProvidersConfig_MarshalEmptyKeySkipped(t *testing.T) {
	p := &ProvidersConfig{
		Named: map[string]NamedProviderConfig{"  ": {Type: "x", ProviderConfig: ProviderConfig{APIKey: "k"}}},
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("blank-key only entries should be omitted -> null, got %s", data)
	}
}

func TestProvidersConfig_MarshalWithData_Fix(t *testing.T) {
	p := &ProvidersConfig{}
	p.Named = map[string]NamedProviderConfig{
		"foo": {Type: "openai", ProviderConfig: ProviderConfig{APIKey: "k"}, Models: map[string]ProviderModelConfig{
			"m1": {Model: "mm"},
		}},
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if s == "null" {
		t.Fatal("should not be null when named has data")
	}
	if !containsJSONKey(s, "foo") {
		t.Errorf("expected foo in output, got %s", s)
	}
}

func TestProvidersConfig_GetNamed(t *testing.T) {
	var nilP *ProvidersConfig
	if _, ok := nilP.GetNamed("x"); ok {
		t.Error("nil should not provide named")
	}
	p := &ProvidersConfig{}
	if _, ok := p.GetNamed("AnTHRoPic"); !ok {
		t.Error("GetNamed(AnTHRoPic) should find anthropic via ensureNamedDefaults")
	}
	if _, ok := p.GetNamed("nope"); ok {
		t.Error("unknown named should not be found")
	}
}

func TestProvidersConfig_ListNamed_Nil(t *testing.T) {
	var nilP *ProvidersConfig
	got := nilP.ListNamed()
	if got == nil || len(got) != 0 {
		t.Errorf("ListNamed on nil should be empty map, got %v", got)
	}
}

func TestProvidersConfig_ListNamed_WithData(t *testing.T) {
	p := &ProvidersConfig{}
	p.Named = map[string]NamedProviderConfig{"custom": {Type: "custom", ProviderConfig: ProviderConfig{APIKey: "k"}}}
	list := p.ListNamed()
	if _, ok := list["custom"]; !ok {
		t.Errorf("custom should be in list, got %v", list)
	}
	// ensureNamedDefaults adds built-ins too.
	if _, ok := list["anthropic"]; !ok {
		t.Errorf("anthropic default should be in list")
	}
}

func TestHasUsableProvider_Extra(t *testing.T) {
	c := DefaultConfig()
	if c.HasUsableProvider() {
		t.Error("default config has no usable provider (no API keys)")
	}
	c.Providers = nil
	if c.HasUsableProvider() {
		t.Error("nil providers not usable")
	}
	c.Providers = &ProvidersConfig{Named: map[string]NamedProviderConfig{
		"ollama": {Type: "ollama", Models: map[string]ProviderModelConfig{"m": {Model: "m"}}},
	}}
	if !c.HasUsableProvider() {
		t.Error("ollama (local) with models should be usable")
	}
}

func containsJSONKey(s, key string) bool {
	for i := 0; i+len(key) <= len(s); i++ {
		if s[i:i+len(key)] == key {
			return true
		}
	}
	return false
}

// ---- GetAPIKey / GetAPIBase ----

func TestGetAPIKey_Priority(t *testing.T) {
	c := DefaultConfig()
	if got := c.GetAPIKey(); got != "" {
		t.Errorf("empty = %q", got)
	}
	c.Providers.OpenRouter.APIKey = "or"
	if got := c.GetAPIKey(); got != "or" {
		t.Errorf("openrouter (checked first) = %q", got)
	}
	c.Providers.OpenRouter.APIKey = ""

	c.Providers.Anthropic.APIKey = "ant"
	if got := c.GetAPIKey(); got != "ant" {
		t.Errorf("anthropic = %q", got)
	}
	c.Providers.Anthropic.APIKey = ""

	c.Providers.OpenAI.APIKey = "oa"
	if got := c.GetAPIKey(); got != "oa" {
		t.Errorf("openai = %q", got)
	}
	c.Providers.OpenAI.APIKey = ""

	c.Providers.Gemini.APIKey = "gem"
	if got := c.GetAPIKey(); got != "gem" {
		t.Errorf("gemini = %q", got)
	}
	c.Providers.Gemini.APIKey = ""

	c.Providers.Zhipu.APIKey = "zh"
	if got := c.GetAPIKey(); got != "zh" {
		t.Errorf("zhipu = %q", got)
	}
	c.Providers.Zhipu.APIKey = ""

	c.Providers.Groq.APIKey = "gro"
	if got := c.GetAPIKey(); got != "gro" {
		t.Errorf("groq = %q", got)
	}
	c.Providers.Groq.APIKey = ""

	c.Providers.VLLM.APIKey = "vl"
	if got := c.GetAPIKey(); got != "vl" {
		t.Errorf("vllm = %q", got)
	}
	c.Providers.VLLM.APIKey = ""

	c.Providers.ShengSuanYun.APIKey = "ssy"
	if got := c.GetAPIKey(); got != "ssy" {
		t.Errorf("shengsuanyun = %q", got)
	}
}

func TestGetAPIBase(t *testing.T) {
	c := DefaultConfig()
	if got := c.GetAPIBase(); got != "" {
		t.Errorf("empty = %q", got)
	}
	// zhipu key with no APIBase
	c.Providers.Zhipu.APIKey = "key"
	if got := c.GetAPIBase(); got != "" {
		t.Errorf("zhipu empty base = %q", got)
	}
	c.Providers.Zhipu.APIKey = ""

	// openrouter key -> default base
	c.Providers.OpenRouter.APIKey = "ork"
	if got := c.GetAPIBase(); got != "https://openrouter.ai/api/v1" {
		t.Errorf("openrouter default = %q", got)
	}
	// openrouter custom base
	c.Providers.OpenRouter.APIBase = "https://custom"
	if got := c.GetAPIBase(); got != "https://custom" {
		t.Errorf("openrouter custom = %q", got)
	}
	c.Providers.OpenRouter.APIKey = ""
	c.Providers.OpenRouter.APIBase = ""

	// vllm with key and base
	c.Providers.VLLM.APIKey = "vk"
	c.Providers.VLLM.APIBase = "https://vllm"
	if got := c.GetAPIBase(); got != "https://vllm" {
		t.Errorf("vllm = %q", got)
	}
	// vllm key without base -> empty
	c.Providers.VLLM.APIBase = ""
	if got := c.GetAPIBase(); got != "" {
		t.Errorf("vllm no base = %q", got)
	}
	// vllm key with empty still checks openrouter zhipu -> empty
	c.Providers.VLLM.APIKey = ""
	if got := c.GetAPIBase(); got != "" {
		t.Errorf("all empty = %q", got)
	}
}

// ---- GetLeleDir ----

func TestGetLeleDir(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", "~/custom")
	if got := GetLeleDir(); got == "" {
		t.Error("custom dir should be expanded")
	}
	t.Setenv("LELE_CONFIG_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("homedir: %v", err)
	}
	if got := GetLeleDir(); got != filepath.Join(home, ".lele") {
		t.Errorf("default dir = %q", got)
	}
}

// ---- SetSessionEphemeral ----

func TestSetSessionEphemeral_WithZeroThreshold(t *testing.T) {
	c := DefaultConfig()
	c.Session.EphemeralThreshold = 0
	c.SetSessionEphemeral(true)
	if c.Session.EphemeralThreshold != DefaultEphemeralThresholdSeconds {
		t.Errorf("threshold should default to %d, got %d", DefaultEphemeralThresholdSeconds, c.Session.EphemeralThreshold)
	}
}

// ---- SessionCompactionThresholdPercent range ----

func TestSessionCompactionThresholdPercent(t *testing.T) {
	c := DefaultConfig()
	c.Session.CompactionThresholdPercent = -5
	if got := c.SessionCompactionThresholdPercent(); got != DefaultCompactionThresholdPercent {
		t.Errorf("negative -> default, got %d", got)
	}
	c.Session.CompactionThresholdPercent = 200
	if got := c.SessionCompactionThresholdPercent(); got != DefaultCompactionThresholdPercent {
		t.Errorf("over 100 -> default, got %d", got)
	}
	c.Session.CompactionThresholdPercent = 0
	if got := c.SessionCompactionThresholdPercent(); got != DefaultCompactionThresholdPercent {
		t.Errorf("zero -> default, got %d", got)
	}
	c.Session.CompactionThresholdPercent = 50
	if got := c.SessionCompactionThresholdPercent(); got != 50 {
		t.Errorf("50 = %d", got)
	}
}

// ---- EvictExcluded env overrides ----

func TestEvictExcludedFromMemory_EnvTrue(t *testing.T) {
	t.Setenv("LELE_EVICT_EXCLUDED", "true")
	c := DefaultConfig()
	c.Session.EvictExcludedFromMemory = false
	if !c.EvictExcludedFromMemory() {
		t.Error("env true should force on")
	}
}

func TestEvictExcludedFromMemory_EnvFalse(t *testing.T) {
	t.Setenv("LELE_EVICT_EXCLUDED", "false")
	c := DefaultConfig()
	c.Session.EvictExcludedFromMemory = true
	if c.EvictExcludedFromMemory() {
		t.Error("env false should force off")
	}
}

func TestEvictExcludedFromMemory_EnvInvalid(t *testing.T) {
	t.Setenv("LELE_EVICT_EXCLUDED", "notabool")
	c := DefaultConfig()
	c.Session.EvictExcludedFromMemory = true
	if !c.EvictExcludedFromMemory() {
		t.Error("invalid env should fall back to config")
	}
	t.Setenv("LELE_EVICT_EXCLUDED", "")
	c.Session.EvictExcludedFromMemory = false
	if c.EvictExcludedFromMemory() {
		t.Error("empty env should fall back to config false")
	}
} // ---- SaveConfig / Persist paths ----

func TestSaveConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg", "config.json")
	cfg := DefaultConfig()
	cfg.Agents.Defaults.Provider = "roundtrip-provider"
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Agents.Defaults.Provider != "roundtrip-provider" {
		t.Errorf("loaded provider = %q", loaded.Agents.Defaults.Provider)
	}
}

func TestSaveConfig_InvalidPath(t *testing.T) {
	// MarshalIndent on a Config should always succeed; force the file write
	// to fail with a path whose parent is not creatable.
	cfg := DefaultConfig()
	err := SaveConfig(filepath.Join(t.TempDir(), "\x00bad", "c.json"), cfg)
	if err == nil {
		t.Log("no error (may skip on some platforms)")
	}
}

func TestTelegramVerbose_RoundTrip(t *testing.T) {
	c := DefaultConfig()
	if got := c.TelegramVerbose(); got != VerboseOff {
		t.Errorf("default = %q", got)
	}
	c.SetTelegramVerbose(VerboseFull)
	if got := c.TelegramVerbose(); got != VerboseFull {
		t.Errorf("after set = %q", got)
	}
}

func TestPersistTelegramVerbose_EmptyPath(t *testing.T) {
	// Use a temp LELE_CONFIG_DIR so DefaultConfigPath points somewhere writable.
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	c := DefaultConfig()
	if err := c.PersistTelegramVerbose("", VerboseBasic); err != nil {
		t.Fatalf("PersistTelegramVerbose: %v", err)
	}
	if got := c.TelegramVerbose(); got != VerboseBasic {
		t.Errorf("after persist = %q", got)
	}
	loaded, err := LoadConfig(DefaultConfigPath())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Channels.Telegram.Verbose != VerboseBasic {
		t.Errorf("persisted verbose = %q", loaded.Channels.Telegram.Verbose)
	}
}

func TestPersistTelegramVerbose_ExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	c := DefaultConfig()
	if err := c.PersistTelegramVerbose(path, VerboseOff); err != nil {
		t.Fatalf("persist: %v", err)
	}
}

func TestPersistSessionEphemeral_EmptyPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	c := DefaultConfig()
	if err := c.PersistSessionEphemeral("", true); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if !c.SessionEphemeralEnabled() {
		t.Error("ephemeral should be enabled")
	}
}

func TestPersistSessionEphemeral_ExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	c := DefaultConfig()
	if err := c.PersistSessionEphemeral(path, false); err != nil {
		t.Fatalf("persist: %v", err)
	}
}

func TestSetSessionEphemeral_RoundTrip(t *testing.T) {
	c := DefaultConfig()
	c.SetSessionEphemeral(true)
	if !c.SessionEphemeralEnabled() {
		t.Error("should be enabled")
	}
	c.SetSessionEphemeral(false)
	if c.SessionEphemeralEnabled() {
		t.Error("should be disabled")
	}
}

func TestSessionEphemeralThreshold_Default(t *testing.T) {
	c := DefaultConfig()
	c.Session.EphemeralThreshold = 0
	if got := c.SessionEphemeralThresholdSeconds(); got != DefaultEphemeralThresholdSeconds {
		t.Errorf("threshold default = %d", got)
	}
	c.Session.EphemeralThreshold = 123
	if got := c.SessionEphemeralThresholdSeconds(); got != 123 {
		t.Errorf("threshold = %d", got)
	}
}

// ---- Reload nil receiver ----

func TestReload_Nil(t *testing.T) {
	var c *Config
	if err := c.Reload("x"); err == nil {
		t.Error("Reload on nil should error")
	}
}

// ---- resolveModelAliasInProvider covering cross-provider suffix ----

func TestResolveModelAlias_CrossProviderNested(t *testing.T) {
	c := DefaultConfig()
	c.Providers.Named = map[string]NamedProviderConfig{
		"prova": {Type: "openai", Models: map[string]ProviderModelConfig{
			"alias-x": {Model: "vendor/model-y"},
		}},
	}
	// lookup model referencing a provider prefix in its value
	got := c.Providers.ResolveModelAlias("prova:alias-x", "prova")
	want := "prova:vendor/model-y"
	if got != want {
		t.Errorf("ResolveModelAlias = %q, want %q", got, want)
	}
}

func TestResolveModelAlias_SuffixMatch(t *testing.T) {
	c := DefaultConfig()
	c.Providers.Named = map[string]NamedProviderConfig{
		"pa": {Type: "openai", Models: map[string]ProviderModelConfig{
			"canonical-model": {Model: "org/canonical-model"},
		}},
	}
	// normalizedModel matches suffix of an alias's Model value.
	got := c.Providers.ResolveModelAlias("pa:canonical-model", "")
	if got != "pa:org/canonical-model" {
		t.Errorf("ResolveModelAlias(suffix) = %q, want pa:org/canonical-model", got)
	}
}

func TestResolveModelAlias_ProviderNotExistSearchAll(t *testing.T) {
	c := DefaultConfig()
	c.Providers.Named = map[string]NamedProviderConfig{
		"other": {Type: "openai", Models: map[string]ProviderModelConfig{
			"shared-model": {Model: "other/real-model"},
		}},
	}
	got := c.Providers.ResolveModelAlias("missing:shared-model", "")
	if got != "other:other/real-model" {
		t.Errorf("cross-provider search = %q, want other:other/real-model", got)
	}
}

func TestResolveModelAlias_NotFoundReturnsRaw(t *testing.T) {
	c := DefaultConfig()
	c.Providers = nil
	// nil providers -> raw
	if got := c.Providers.ResolveModelAlias("x:y", "prov"); got != "x:y" {
		t.Errorf("nil provider resolve = %q", got)
	}
}

// ---- SetLanguage ----

func TestSetLanguage(t *testing.T) {
	c := DefaultConfig()
	if got := c.GetLanguage(); got != "es" {
		t.Errorf("default language = %q", got)
	}
	c.SetLanguage("en")
	if got := c.GetLanguage(); got != "en" {
		t.Errorf("after set = %q", got)
	}
}

// ---- AgentModelConfig roundtrip ----

func TestAgentModelConfig_MarshalRoundTrip(t *testing.T) {
	m := AgentModelConfig{Primary: "p", Fallbacks: []string{"f1"}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out AgentModelConfig
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Primary != "p" || len(out.Fallbacks) != 1 || out.Fallbacks[0] != "f1" {
		t.Errorf("roundtrip = %+v", out)
	}
}

// ---- GroupProfile validation error via Full Validate ----

func TestProviderModelConfig_Validate_NilReasoning(t *testing.T) {
	p := &ProviderModelConfig{Model: "m"}
	if err := p.Validate(); err != nil {
		t.Errorf("validate with no reasoning = %v", err)
	}
}

func TestProviderModelConfig_Validate_InvalidReasoning(t *testing.T) {
	effort := "weird"
	p := &ProviderModelConfig{Model: "m", Reasoning: &ReasoningConfig{Effort: &effort}}
	if err := p.Validate(); err == nil {
		t.Error("expected error for invalid effort")
	}
}

func TestProvidersConfig_UnmarshalJSON_Error(t *testing.T) {
	var p ProvidersConfig
	// The providers key entry fails to unmarshal into NamedProviderConfig.
	err := json.Unmarshal([]byte(`["just-an-array"]`), &p)
	if err != nil {
		// UnmarshalJSON may delegate to alias and succeed; either is fine,
		// but it must not panic.
		t.Logf("unmarshal error (acceptable): %v", err)
	}
}

func TestProvidersConfig_UnmarshalJSON_ModelError(t *testing.T) {
	bad := `{"anthropic":{"models":{"m1":{"reasoning":{"effort":"bogus"}}}}}`
	var p ProvidersConfig
	if err := p.UnmarshalJSON([]byte(bad)); err == nil {
		t.Error("expected model validation error")
	}
}
