package channels

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// newSpawnModelValidator builds a NativeChannel whose config snapshot is the
// given cfg, exposing validateSpawnModel for direct unit testing.
func newSpawnModelValidator(cfg *config.Config) *NativeChannel {
	loop := newNativeTestAgentLoop(cfg)
	return &NativeChannel{
		cfg:       &cfg.Channels.Native,
		agentLoop: loop,
	}
}

// spawnModelTestConfig returns a config with one creatable provider (has an
// API key) and one configured-but-unusable provider (exists in GetNamed but
// has no API key or api_base, so CreateProviderForCandidate fails at runtime).
func spawnModelTestConfig() *config.Config {
	cfg := config.DefaultConfig()
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "test-openai-key"},
			Models: map[string]config.ProviderModelConfig{
				"gpt-4o": {Model: "gpt-4o"},
			},
		},
		"keyless": {
			Type:   "openai",
			Models: map[string]config.ProviderModelConfig{"orphan-model": {Model: "orphan-model"}},
		},
	}
	return cfg
}

func TestValidateSpawnModel(t *testing.T) {
	n := newSpawnModelValidator(spawnModelTestConfig())

	t.Run("accepts provider with creatable config", func(t *testing.T) {
		if err := n.validateSpawnModel("openai:gpt-4o"); err != nil {
			t.Fatalf("validateSpawnModel(openai:gpt-4o) = %v, want nil", err)
		}
	})

	t.Run("rejects provider that exists but cannot be created", func(t *testing.T) {
		// "keyless" is present in cfg.Providers.GetNamed but has no API key or
		// api_base: the runtime resolver would fail, so creation must fail too.
		err := n.validateSpawnModel("keyless:orphan-model")
		if err == nil {
			t.Fatal("validateSpawnModel(keyless:orphan-model) = nil, want error")
		}
		if !strings.Contains(err.Error(), "cannot be used") {
			t.Errorf("error = %q, want it to mention the model cannot be used", err.Error())
		}
	})

	t.Run("rejects unknown provider", func(t *testing.T) {
		if err := n.validateSpawnModel("nonexistent:some-model"); err == nil {
			t.Fatal("validateSpawnModel(nonexistent:some-model) = nil, want error")
		}
	})

	t.Run("accepts bare model that resolves to a usable provider", func(t *testing.T) {
		// Bare names go through ResolveModelAlias against the default provider;
		// "gpt-4o" is configured under openai, which is creatable.
		if err := n.validateSpawnModel("gpt-4o"); err != nil {
			t.Fatalf("validateSpawnModel(gpt-4o) = %v, want nil", err)
		}
	})

	t.Run("rejects invalid model shape", func(t *testing.T) {
		if err := n.validateSpawnModel("openai:"); err == nil {
			t.Fatal("validateSpawnModel(openai:) = nil, want error")
		}
	})
}
