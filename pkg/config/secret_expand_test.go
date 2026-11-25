package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func withKeyringResolver(t *testing.T, fn func(name string) (string, error)) {
	t.Helper()
	previous := keyringResolver
	t.Cleanup(func() { RegisterKeyringResolver(previous) })
	RegisterKeyringResolver(fn)
}

func loadSecretFixture(t *testing.T, content string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

const secretFixture = `{
	"channels": {
		"telegram": {"enabled": true, "token": "{{SECRET:telegram.token}}"}
	},
	"providers": {
		"openai": {"type": "openai", "api_key": "{{SECRET:openai.api_key}}"}
	}
}`

func TestLoadConfig_ResolvesKeyringPlaceholder(t *testing.T) {
	withKeyringResolver(t, func(name string) (string, error) {
		switch name {
		case "telegram.token":
			return "tg-from-keyring", nil
		case "openai.api_key":
			return "sk-from-keyring", nil
		}
		return "", errors.New("unknown secret")
	})

	cfg := loadSecretFixture(t, secretFixture)

	if cfg.Channels.Telegram.Token != "tg-from-keyring" {
		t.Errorf("telegram token = %q, want tg-from-keyring", cfg.Channels.Telegram.Token)
	}
	if cfg.Providers == nil {
		t.Fatal("providers missing")
	}
	if got := cfg.Providers.Named["openai"].APIKey; got != "sk-from-keyring" {
		t.Errorf("provider api key = %q, want sk-from-keyring", got)
	}
}

func TestLoadConfig_KeyringNilResolverLeavesPlaceholder(t *testing.T) {
	withKeyringResolver(t, nil)

	cfg := loadSecretFixture(t, secretFixture)

	if cfg.Channels.Telegram.Token != "{{SECRET:telegram.token}}" {
		t.Errorf("telegram token = %q, want the placeholder preserved", cfg.Channels.Telegram.Token)
	}
}

func TestLoadConfig_KeyringResolverErrorLeavesPlaceholder(t *testing.T) {
	withKeyringResolver(t, func(name string) (string, error) {
		return "", errors.New("vault locked")
	})

	cfg := loadSecretFixture(t, secretFixture)

	if cfg.Channels.Telegram.Token != "{{SECRET:telegram.token}}" {
		t.Errorf("telegram token = %q, want the placeholder preserved", cfg.Channels.Telegram.Token)
	}
}

func TestLoadConfig_KeyringExpansionIsInMemoryOnly(t *testing.T) {
	const content = `{"channels":{"telegram":{"enabled":true,"token":"{{SECRET:telegram.token}}"}}}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	withKeyringResolver(t, func(string) (string, error) { return "resolved-secret", nil })

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Channels.Telegram.Token != "resolved-secret" {
		t.Fatalf("token = %q, want resolved-secret", cfg.Channels.Telegram.Token)
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var onDisk struct {
		Channels struct {
			Telegram struct {
				Token string `json:"token"`
			} `json:"telegram"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(written, &onDisk); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	if onDisk.Channels.Telegram.Token != "{{SECRET:telegram.token}}" {
		t.Fatalf("saved token = %q, want the placeholder preserved", onDisk.Channels.Telegram.Token)
	}
}

func TestLoadConfig_KeyringPlaceholderInsideLargerStringIsUntouched(t *testing.T) {
	withKeyringResolver(t, func(string) (string, error) { return "resolved-secret", nil })

	cfg := loadSecretFixture(t, `{"channels":{"telegram":{"enabled":true,"token":"bearer {{SECRET:telegram.token}}"}}}`)

	if cfg.Channels.Telegram.Token != "bearer {{SECRET:telegram.token}}" {
		t.Errorf("token = %q, want the embedded placeholder untouched", cfg.Channels.Telegram.Token)
	}
}
