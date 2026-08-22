package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidStrategy(t *testing.T) {
	tests := []struct {
		strategy string
		want     bool
	}{
		{StrategyRoundRobin, true},
		{StrategyMoA, true},
		{StrategyModerator, true},
		{StrategyPipeline, true},
		{"unknown", false},
		{"", false},
		{StrategyRoundRobin + " ", false},
	}
	for _, tt := range tests {
		t.Run(tt.strategy, func(t *testing.T) {
			if got := ValidStrategy(tt.strategy); got != tt.want {
				t.Errorf("ValidStrategy(%q) = %v, want %v", tt.strategy, got, tt.want)
			}
		})
	}
}

func TestConfig_clone(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agents.Defaults.Model = "model-x"
	cfg.Bindings = []AgentBinding{{AgentID: "a", Match: BindingMatch{Channel: "tg"}}}
	cfg.Session.Ephemeral = true

	cloned := cfg.clone()
	if cloned == nil {
		t.Fatal("clone returned nil")
	}
	if cloned.Agents.Defaults.Model != "model-x" {
		t.Errorf("clone model = %q, want model-x", cloned.Agents.Defaults.Model)
	}
	if len(cloned.Bindings) != 1 {
		t.Fatalf("clone bindings len = %d, want 1", len(cloned.Bindings))
	}

	// Deep-copy semantics: mutating the clone must not affect the original.
	cloned.Agents.Defaults.Model = "mutated"
	cloned.Bindings[0].AgentID = "mutated"
	if cfg.Agents.Defaults.Model != "model-x" {
		t.Errorf("original model mutated = %q", cfg.Agents.Defaults.Model)
	}
	if cfg.Bindings[0].AgentID != "a" {
		t.Errorf("original binding mutated = %q", cfg.Bindings[0].AgentID)
	}
}

func TestConfig_cloneNil(t *testing.T) {
	var cfg *Config
	if got := cfg.clone(); got != nil {
		t.Errorf("clone of nil = %v, want nil", got)
	}
}

func TestConfig_Snapshot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Session.Ephemeral = false
	cfg.Agents.Defaults.Provider = "snapshot-provider"

	snap := cfg.Snapshot()
	if snap == nil {
		t.Fatal("snapshot returned nil")
	}
	if snap.Agents.Defaults.Provider != "snapshot-provider" {
		t.Errorf("snapshot provider = %q", snap.Agents.Defaults.Provider)
	}
	// Snapshot is a copy; mutating source later should not affect it.
	cfg.Agents.Defaults.Provider = "changed"
	if snap.Agents.Defaults.Provider != "snapshot-provider" {
		t.Errorf("snapshot was mutated = %q", snap.Agents.Defaults.Provider)
	}
}

func TestConfig_Snapshot_CloneIsolation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agents.Defaults.Provider = "isolated-provider"
	snap := cfg.Snapshot()
	if snap == nil {
		t.Fatal("snapshot returned nil")
	}
	if snap.Agents.Defaults.Provider != "isolated-provider" {
		t.Errorf("snapshot provider = %q, want isolated-provider", snap.Agents.Defaults.Provider)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", "")
	dir := os.Getenv("LELE_CONFIG_DIR")
	_ = dir
	orig := os.Getenv("LELE_CONFIG_DIR")
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".lele", "config.json")

	os.Unsetenv("LELE_CONFIG_DIR")
	defer func() {
		if orig != "" {
			os.Setenv("LELE_CONFIG_DIR", orig)
		} else {
			os.Unsetenv("LELE_CONFIG_DIR")
		}
	}()

	if got := DefaultConfigPath(); got != want {
		t.Errorf("DefaultConfigPath() = %q, want %q", got, want)
	}
}

func TestDefaultConfigPath_CustomDir(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", "~/custom-lele")
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "custom-lele", "config.json")
	if got := DefaultConfigPath(); got != want {
		t.Errorf("DefaultConfigPath() = %q, want %q", got, want)
	}
}

func TestConfig_Reload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	cfg.Agents.Defaults.Model = "old-model"

	content := `{"session":{"ephemeral_threshold":100,"compaction_threshold_percent":60},"logs":{"path":"/tmp/custom-logs"}}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := cfg.Reload(path); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if cfg.Session.EphemeralThreshold != 100 {
		t.Errorf("threshold = %d, want 100", cfg.Session.EphemeralThreshold)
	}
	if cfg.Session.CompactionThresholdPercent != 60 {
		t.Errorf("compaction = %d, want 60", cfg.Session.CompactionThresholdPercent)
	}
	if cfg.Logs.Path != "/tmp/custom-logs" {
		t.Errorf("logs path = %q", cfg.Logs.Path)
	}
}

func TestConfig_ReloadNil(t *testing.T) {
	var cfg *Config
	if err := cfg.Reload("/tmp/nonexistent.json"); err == nil {
		t.Error("Reload on nil config should error")
	}
}

func TestConfig_TelegramVerbose(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.TelegramVerbose(); got != VerboseOff {
		t.Errorf("TelegramVerbose() = %q, want %q", got, VerboseOff)
	}
	cfg.SetTelegramVerbose(VerboseFull)
	if got := cfg.TelegramVerbose(); got != VerboseFull {
		t.Errorf("TelegramVerbose() = %q, want %q", got, VerboseFull)
	}
}

func TestConfig_SessionEphemeralEnabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Session.Ephemeral = true
	if !cfg.SessionEphemeralEnabled() {
		t.Error("SessionEphemeralEnabled = false, want true")
	}
	cfg.Session.Ephemeral = false
	if cfg.SessionEphemeralEnabled() {
		t.Error("SessionEphemeralEnabled = true, want false")
	}
}

func TestConfig_SessionEphemeralThresholdSeconds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Session.EphemeralThreshold = 100
	if got := cfg.SessionEphemeralThresholdSeconds(); got != 100 {
		t.Errorf("threshold = %d, want 100", got)
	}
	// <=0 falls back to default.
	cfg.Session.EphemeralThreshold = 0
	if got := cfg.SessionEphemeralThresholdSeconds(); got != DefaultEphemeralThresholdSeconds {
		t.Errorf("threshold = %d, want default %d", got, DefaultEphemeralThresholdSeconds)
	}
	cfg.Session.EphemeralThreshold = -5
	if got := cfg.SessionEphemeralThresholdSeconds(); got != DefaultEphemeralThresholdSeconds {
		t.Errorf("threshold = %d, want default %d", got, DefaultEphemeralThresholdSeconds)
	}
}

func TestConfig_SessionCompactionThresholdPercent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Session.CompactionThresholdPercent = 50
	if got := cfg.SessionCompactionThresholdPercent(); got != 50 {
		t.Errorf("compaction = %d, want 50", got)
	}
	// Out of range falls back to default.
	cfg.Session.CompactionThresholdPercent = 0
	if got := cfg.SessionCompactionThresholdPercent(); got != DefaultCompactionThresholdPercent {
		t.Errorf("compaction = %d, want default", got)
	}
	cfg.Session.CompactionThresholdPercent = 101
	if got := cfg.SessionCompactionThresholdPercent(); got != DefaultCompactionThresholdPercent {
		t.Errorf("compaction = %d, want default", got)
	}
	cfg.Session.CompactionThresholdPercent = -1
	if got := cfg.SessionCompactionThresholdPercent(); got != DefaultCompactionThresholdPercent {
		t.Errorf("compaction = %d, want default", got)
	}
}

func TestConfig_CompactionModel(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.CompactionModel(); got != "" {
		t.Errorf("CompactionModel = %q, want default empty", got)
	}
	cfg.Session.CompactionModel = "claude-haiku"
	if got := cfg.CompactionModel(); got != "claude-haiku" {
		t.Errorf("CompactionModel = %q, want claude-haiku", got)
	}
}

func TestConfig_EvictExcluded_EnvInvalid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Session.EvictExcludedFromMemory = true
	t.Setenv("LELE_EVICT_EXCLUDED", "not-a-bool")
	if !cfg.EvictExcludedFromMemory() {
		t.Error("invalid env value should be ignored, default true expected")
	}
}

func TestConfig_LogsPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Logs.Path = ""
	t.Setenv("LELE_CONFIG_DIR", "")
	home, _ := os.UserHomeDir()
	if got := cfg.LogsPath(); got != filepath.Join(home, ".lele", "logs") {
		t.Errorf("LogsPath() = %q, want %q", got, filepath.Join(home, ".lele", "logs"))
	}

	cfg.Logs.Path = "/abs/path"
	if got := cfg.LogsPath(); got != "/abs/path" {
		t.Errorf("LogsPath() = %q, want /abs/path", got)
	}

	cfg.Logs.Path = "~/home-logs"
	home2, _ := os.UserHomeDir()
	if got := cfg.LogsPath(); got != filepath.Join(home2, "home-logs") {
		t.Errorf("LogsPath() = %q, want %q", got, filepath.Join(home2, "home-logs"))
	}
}

func TestConfig_KeyringVaultPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Keyring.Path = ""
	t.Setenv("LELE_CONFIG_DIR", "")
	home, _ := os.UserHomeDir()
	if got := cfg.KeyringVaultPath(); got != filepath.Join(home, ".lele", "keyring.enc") {
		t.Errorf("KeyringVaultPath() = %q, want %q", got, filepath.Join(home, ".lele", "keyring.enc"))
	}

	cfg.Keyring.Path = "/vault/file.enc"
	if got := cfg.KeyringVaultPath(); got != "/vault/file.enc" {
		t.Errorf("KeyringVaultPath() = %q, want /vault/file.enc", got)
	}

	cfg.Keyring.Path = "~/vault.enc"
	home2, _ := os.UserHomeDir()
	if got := cfg.KeyringVaultPath(); got != filepath.Join(home2, "vault.enc") {
		t.Errorf("KeyringVaultPath() = %q, want %q", got, filepath.Join(home2, "vault.enc"))
	}
}

func TestConfig_WorkspacePathExpandHome(t *testing.T) {
	cfg := DefaultConfig()
	home, _ := os.UserHomeDir()
	cfg.Agents.Defaults.Workspace = "~/ws"
	if got := cfg.WorkspacePath(); got != filepath.Join(home, "ws") {
		t.Errorf("WorkspacePath() = %q, want %q", got, filepath.Join(home, "ws"))
	}
}

func TestConfig_GetAPIKey(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.GetAPIKey(); got != "" {
		t.Errorf("GetAPIKey empty = %q, want empty", got)
	}

	tests := []struct {
		name   string
		setup  func(*Config)
		expect string
	}{
		{"openrouter", func(c *Config) { c.Providers.OpenRouter.APIKey = "k-or" }, "k-or"},
		{"anthropic", func(c *Config) { c.Providers.Anthropic.APIKey = "k-ant" }, "k-ant"},
		{"openai", func(c *Config) { c.Providers.OpenAI.APIKey = "k-oa" }, "k-oa"},
		{"gemini", func(c *Config) { c.Providers.Gemini.APIKey = "k-gem" }, "k-gem"},
		{"zhipu", func(c *Config) { c.Providers.Zhipu.APIKey = "k-zp" }, "k-zp"},
		{"groq", func(c *Config) { c.Providers.Groq.APIKey = "k-gq" }, "k-gq"},
		{"vllm", func(c *Config) { c.Providers.VLLM.APIKey = "k-vl" }, "k-vl"},
		{"shengsuanyun", func(c *Config) { c.Providers.ShengSuanYun.APIKey = "k-ssy" }, "k-ssy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DefaultConfig()
			tt.setup(c)
			if got := c.GetAPIKey(); got != tt.expect {
				t.Errorf("GetAPIKey() = %q, want %q", got, tt.expect)
			}
		})
	}

	// Priority: openrouter wins over others.
	c := DefaultConfig()
	c.Providers.Anthropic.APIKey = "k-ant"
	c.Providers.OpenRouter.APIKey = "k-or"
	if got := c.GetAPIKey(); got != "k-or" {
		t.Errorf("GetAPIKey priority = %q, want k-or", got)
	}
}

func TestConfig_GetAPIBase(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.GetAPIBase(); got != "" {
		t.Errorf("GetAPIBase empty = %q, want empty", got)
	}

	// OpenRouter without explicit base falls back to default.
	c := DefaultConfig()
	c.Providers.OpenRouter.APIKey = "k"
	if got := c.GetAPIBase(); got != "https://openrouter.ai/api/v1" {
		t.Errorf("GetAPIBase openrouter default = %q", got)
	}

	// OpenRouter with explicit base.
	c = DefaultConfig()
	c.Providers.OpenRouter.APIKey = "k"
	c.Providers.OpenRouter.APIBase = "https://custom"
	if got := c.GetAPIBase(); got != "https://custom" {
		t.Errorf("GetAPIBase openrouter custom = %q", got)
	}

	// Zhipu: returns APIBase (even if empty) when key present.
	c = DefaultConfig()
	c.Providers.Zhipu.APIKey = "k"
	c.Providers.Zhipu.APIBase = "https://zhipu"
	if got := c.GetAPIBase(); got != "https://zhipu" {
		t.Errorf("GetAPIBase zhipu = %q", got)
	}

	// VLLM: requires both key and base.
	c = DefaultConfig()
	c.Providers.VLLM.APIKey = "k"
	if got := c.GetAPIBase(); got != "" {
		t.Errorf("GetAPIBase vllm without base = %q, want empty", got)
	}
	c.Providers.VLLM.APIBase = "https://vllm"
	if got := c.GetAPIBase(); got != "https://vllm" {
		t.Errorf("GetAPIBase vllm = %q", got)
	}
}

func TestConfig_GetModelConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agents.Defaults.Model = "primary-model"
	cfg.Agents.Defaults.ModelFallbacks = []string{"f1", "f2"}

	mc := cfg.GetModelConfig()
	if mc.Primary != "primary-model" {
		t.Errorf("primary = %q", mc.Primary)
	}
	if len(mc.Fallbacks) != 2 || mc.Fallbacks[0] != "f1" || mc.Fallbacks[1] != "f2" {
		t.Errorf("fallbacks = %v", mc.Fallbacks)
	}
}

func TestConfig_GetImageModelConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agents.Defaults.ImageModel = "image-model"
	cfg.Agents.Defaults.ImageModelFallbacks = []string{"img-f1"}

	mc := cfg.GetImageModelConfig()
	if mc.Primary != "image-model" {
		t.Errorf("primary = %q", mc.Primary)
	}
	if len(mc.Fallbacks) != 1 || mc.Fallbacks[0] != "img-f1" {
		t.Errorf("fallbacks = %v", mc.Fallbacks)
	}
}

func TestConfig_GetLanguage(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.GetLanguage(); got != "es" {
		t.Errorf("GetLanguage() default = %q, want es", got)
	}
	cfg.SetLanguage("en")
	if got := cfg.GetLanguage(); got != "en" {
		t.Errorf("GetLanguage() = %q, want en", got)
	}
}

func TestConfig_GetAvailableLanguages(t *testing.T) {
	cfg := DefaultConfig()
	languages := cfg.GetAvailableLanguages()
	if len(languages) == 0 {
		t.Error("GetAvailableLanguages() returned empty list")
	}
	// Should include "es", "en", "pt" at minimum.
	seen := map[string]bool{}
	for _, l := range languages {
		seen[l] = true
	}
	for _, want := range []string{"es", "en", "pt"} {
		if !seen[want] {
			t.Errorf("GetAvailableLanguages() missing %q, got %v", want, languages)
		}
	}
}

func TestConfig_EffectiveServerHost(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		expect string
	}{
		{"all-default", &Config{}, "0.0.0.0"},
		{"server", &Config{Server: ServerConfig{Host: "srv"}}, "srv"},
		{"gateway-fallback", &Config{Gateway: GatewayConfig{Host: "gw"}}, "gw"},
		{"native-fallback", &Config{Channels: ChannelsConfig{Native: NativeConfig{Host: "native"}}}, "native"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.EffectiveServerHost(); got != tt.expect {
				t.Errorf("EffectiveServerHost() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestConfig_EffectiveServerPort(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		expect int
	}{
		{"all-default", &Config{}, 8080},
		{"server", &Config{Server: ServerConfig{Port: 9000}}, 9000},
		{"gateway-fallback", &Config{Gateway: GatewayConfig{Port: 9001}}, 9001},
		{"native-fallback", &Config{Channels: ChannelsConfig{Native: NativeConfig{Port: 9002}}}, 9002},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.EffectiveServerPort(); got != tt.expect {
				t.Errorf("EffectiveServerPort() = %d, want %d", got, tt.expect)
			}
		})
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "/abs/path", "/abs/path"},
		{"tilde-only", "~", home},
		{"tilde-slash", "~/dir", filepath.Join(home, "dir")},
		{"tilde-noslash", "~dir", home}, // path like ~dir: source returns home
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandHome(tt.in); got != tt.want {
				t.Errorf("expandHome(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeProviderKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"z.ai", "zai"},
		{"Z.AI", "zai"},
		{"z-ai", "zai"},
		{"opencode-zen", "opencode"},
		{"qwen", "qwen-portal"},
		{"kimi-code", "kimi-coding"},
		{"gpt", "openai"},
		{"claude", "anthropic"},
		{"glm", "zhipu"},
		{"google", "gemini"},
		{"  openrouter  ", "openrouter"},
		{"", ""},
		{"custom", "custom"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := normalizeProviderKey(tt.in); got != tt.want {
				t.Errorf("normalizeProviderKey(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveModelAlias_EdgeCases(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Providers.Named = map[string]NamedProviderConfig{
		"p1": {Type: "openai", Models: map[string]ProviderModelConfig{
			"alias1":   {Model: ""}, // empty model -> returns map key
			"dot.name": {Model: "resolved-dot"},
		}},
	}

	// nil providers returns raw.
	if got := (&ProvidersConfig{}).ResolveModelAlias("x", "p1"); got != "x" {
		t.Errorf("nil providers ResolveModelAlias = %q, want x", got)
	}

	// empty model returns raw.
	if got := cfg.Providers.ResolveModelAlias("  ", "p1"); got != "" {
		t.Errorf("whitespace model ResolveModelAlias = %q, want empty (trimmed)", got)
	}

	// unknown returns raw.
	if got := cfg.Providers.ResolveModelAlias("nope", "p1"); got != "nope" {
		t.Errorf("unknown ResolveModelAlias = %q, want nope", got)
	}

	// provider:model with empty model returns raw.
	if got := cfg.Providers.ResolveModelAlias("p1:", "x"); got != "p1:" {
		t.Errorf("empty-model-after-colon = %q, want p1:", got)
	}

	// provider prefix strip finds alias with empty model (key becomes model).
	if got := cfg.Providers.ResolveModelAlias("p1:alias1", ""); got != "p1:alias1" {
		t.Errorf("key-only alias = %q, want p1:alias1", got)
	}

	// dot normalized to hyphen.
	if got := cfg.Providers.ResolveModelAlias("p1:dot.name", ""); got != "p1:resolved-dot" {
		t.Errorf("dot->hyphen ResolveModelAlias = %q, want p1:resolved-dot", got)
	}
}

func TestResolveModelAliasInProvider_EdgeCases(t *testing.T) {
	var nilP *ProvidersConfig
	if _, found := nilP.resolveModelAliasInProvider("x", "y", false); found {
		t.Error("nil provider should not resolve")
	}

	p := &ProvidersConfig{}
	if _, found := p.resolveModelAliasInProvider("", "m", false); found {
		t.Error("empty provider should not resolve")
	}
	if _, found := p.resolveModelAliasInProvider("nonexistent", "m", false); found {
		t.Error("unknown provider should not resolve")
	}
}
