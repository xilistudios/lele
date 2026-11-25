package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const literalSecretsFixture = `{
	"agents": {
		"defaults": {
			"workspace": "/ws",
			"provider": "openai",
			"model": "gpt-4",
			"max_tokens": 4096
		}
	},
	"channels": {
		"telegram": {"enabled": true, "token": "tg-token"},
		"feishu": {
			"enabled": true,
			"app_id": "feishu-id",
			"app_secret": "feishu-secret",
			"encrypt_key": "feishu-encrypt",
			"verification_token": "feishu-verify"
		},
		"discord": {"enabled": true, "token": "discord-token"},
		"qq": {"enabled": true, "app_id": "qq-id", "app_secret": "qq-secret"},
		"dingtalk": {"enabled": true, "client_id": "dingtalk-id", "client_secret": "dingtalk-secret"},
		"slack": {"enabled": true, "bot_token": "slack-bot", "app_token": "slack-app"},
		"line": {"enabled": true, "channel_secret": "line-secret", "channel_access_token": "line-token"},
		"onebot": {"enabled": true, "access_token": "onebot-token"}
	},
	"providers": {
		"openai": {"type": "openai", "api_key": "sk-openai"}
	},
	"tools": {
		"web": {
			"brave": {"enabled": true, "api_key": "brave-key"},
			"perplexity": {"enabled": true, "api_key": "perplexity-key"}
		}
	}
}`

func loadFixtureDocument(t *testing.T, content string) *EditableDocument {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	return doc
}

func expectedSecretPaths() []string {
	return []string{
		"channels.dingtalk.client_id",
		"channels.dingtalk.client_secret",
		"channels.discord.token",
		"channels.feishu.app_id",
		"channels.feishu.app_secret",
		"channels.feishu.encrypt_key",
		"channels.feishu.verification_token",
		"channels.line.channel_access_token",
		"channels.line.channel_secret",
		"channels.onebot.access_token",
		"channels.qq.app_id",
		"channels.qq.app_secret",
		"channels.slack.app_token",
		"channels.slack.bot_token",
		"channels.telegram.token",
		"providers.openai.api_key",
		"tools.web.brave.api_key",
		"tools.web.perplexity.api_key",
	}
}

func enumeratedSecretPaths(doc *EditableDocument) []string {
	var paths []string
	doc.forEachSecret(func(path string, _ *SecretValue) {
		paths = append(paths, path)
	})
	sort.Strings(paths)
	return paths
}

func TestForEachSecret_EnumeratesEverySecretField(t *testing.T) {
	doc := loadFixtureDocument(t, literalSecretsFixture)

	for _, path := range expectedSecretPaths() {
		if value, ok := doc.SecretValues()[path]; !ok || value.Mode != SecretModeLiteral {
			t.Fatalf("%s missing from SecretValues: %#v (ok=%v)", path, value, ok)
		}
	}

	got := enumeratedSecretPaths(doc)
	want := expectedSecretPaths()
	if len(got) != len(want) {
		t.Fatalf("enumerated %d secret paths, want %d:\n got %v\nwant %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("secret path %d = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestMaskedCopy_MasksLiteralSecrets(t *testing.T) {
	doc := loadFixtureDocument(t, literalSecretsFixture)

	masked, err := doc.MaskedCopy()
	if err != nil {
		t.Fatalf("MaskedCopy: %v", err)
	}

	masked.forEachSecret(func(path string, sv *SecretValue) {
		if sv.Mode != SecretModeLiteral {
			t.Fatalf("%s mode = %q, want literal", path, sv.Mode)
		}
		if sv.Value != "" {
			t.Fatalf("%s leaked a literal value %q", path, sv.Value)
		}
		if !sv.HasValue {
			t.Fatalf("%s has_value = false, want true", path)
		}
	})

	encoded, err := json.Marshal(masked)
	if err != nil {
		t.Fatalf("marshal masked doc: %v", err)
	}
	for _, secret := range []string{"tg-token", "feishu-secret", "discord-token", "sk-openai", "brave-key"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("masked document still contains %q: %s", secret, encoded)
		}
	}

	if doc.Channels.Telegram.Token.Value != "tg-token" {
		t.Fatalf("MaskedCopy mutated the source document: token = %q", doc.Channels.Telegram.Token.Value)
	}
	if doc.Providers["openai"].APIKey.Value != "sk-openai" {
		t.Fatalf("MaskedCopy mutated the source provider: api_key = %q", doc.Providers["openai"].APIKey.Value)
	}
}

func TestMaskedCopy_EnvAndKeyringStayReferencesOnly(t *testing.T) {
	const fixture = `{
		"agents": {"defaults": {"workspace": "/ws", "provider": "openai", "model": "gpt-4", "max_tokens": 4096}},
		"channels": {
			"telegram": {"enabled": true, "token": "{{ENV_LELE_TEST_TG_TOKEN}}"},
			"discord": {"enabled": true, "token": "{{SECRET:discord.token}}"}
		}
	}`
	doc := loadFixtureDocument(t, fixture)

	masked, err := doc.MaskedCopy()
	if err != nil {
		t.Fatalf("MaskedCopy: %v", err)
	}

	telegram := masked.Channels.Telegram.Token
	if telegram.Mode != SecretModeEnv || telegram.EnvName != "LELE_TEST_TG_TOKEN" || telegram.Value != "" {
		t.Fatalf("telegram token = %#v, want env reference only", telegram)
	}
	if telegram.HasValue {
		t.Fatalf("env reference must not claim a stored literal: %#v", telegram)
	}

	discord := masked.Channels.Discord.Token
	if discord.Mode != SecretModeKeyring || discord.SecretName != "discord.token" || discord.Value != "" {
		t.Fatalf("discord token = %#v, want keyring reference only", discord)
	}
}

func TestMaskedCopy_DoesNotAliasProviders(t *testing.T) {
	doc := loadFixtureDocument(t, literalSecretsFixture)

	masked, err := doc.MaskedCopy()
	if err != nil {
		t.Fatalf("MaskedCopy: %v", err)
	}
	masked.Providers["openai"] = EditableNamedProviderConfig{APIKey: SecretValue{Mode: SecretModeLiteral, Value: "mutated"}}
	masked.Providers["injected"] = EditableNamedProviderConfig{APIKey: SecretValue{Mode: SecretModeLiteral, Value: "injected"}}

	if len(doc.Providers) != 1 {
		t.Fatalf("masked copy shares the providers map: source has %d entries", len(doc.Providers))
	}
	if doc.Providers["openai"].APIKey.Value != "sk-openai" {
		t.Fatalf("masked copy shares the provider entry: api_key = %q", doc.Providers["openai"].APIKey.Value)
	}
}

func TestRestoreMaskedSecrets_KeepsStoredLiteral(t *testing.T) {
	doc := loadFixtureDocument(t, literalSecretsFixture)

	masked, err := doc.MaskedCopy()
	if err != nil {
		t.Fatalf("MaskedCopy: %v", err)
	}
	roundTripped := roundTripDocument(t, masked)

	stored := doc.SecretValues()
	roundTripped.RestoreMaskedSecrets(stored)

	roundTripped.forEachSecret(func(path string, sv *SecretValue) {
		want, _ := stored[path]
		if sv.Mode != SecretModeLiteral || sv.Value != want.Value {
			t.Fatalf("%s = %#v, want the stored literal %q", path, sv, want.Value)
		}
	})
}

func TestRestoreMaskedSecrets_ReplacesLiteral(t *testing.T) {
	doc := loadFixtureDocument(t, literalSecretsFixture)
	stored := doc.SecretValues()

	incoming := roundTripDocument(t, doc)
	incoming.Channels.Telegram.Token = SecretValue{Mode: SecretModeLiteral, Value: "tg-rotated"}
	incoming.RestoreMaskedSecrets(stored)

	if incoming.Channels.Telegram.Token.Value != "tg-rotated" {
		t.Fatalf("token = %q, want the incoming replacement", incoming.Channels.Telegram.Token.Value)
	}
	if incoming.Channels.Discord.Token.Value != "discord-token" {
		t.Fatalf("discord token = %q, want the stored value", incoming.Channels.Discord.Token.Value)
	}
}

func TestRestoreMaskedSecrets_EmptyModeClears(t *testing.T) {
	doc := loadFixtureDocument(t, literalSecretsFixture)
	stored := doc.SecretValues()

	incoming := roundTripDocument(t, doc)
	incoming.Channels.Telegram.Token = SecretValue{Mode: SecretModeEmpty}
	incoming.RestoreMaskedSecrets(stored)

	if incoming.Channels.Telegram.Token.Mode != SecretModeEmpty || incoming.Channels.Telegram.Token.Value != "" {
		t.Fatalf("token = %#v, want empty", incoming.Channels.Telegram.Token)
	}
}

func TestRestoreMaskedSecrets_KeepsEnvAndKeyringReferences(t *testing.T) {
	const fixture = `{
		"agents": {"defaults": {"workspace": "/ws", "provider": "openai", "model": "gpt-4", "max_tokens": 4096}},
		"channels": {"telegram": {"enabled": true, "token": "{{SECRET:telegram.token}}"}}
	}`
	doc := loadFixtureDocument(t, fixture)
	stored := doc.SecretValues()

	incoming := roundTripDocument(t, doc)
	incoming.RestoreMaskedSecrets(stored)

	token := incoming.Channels.Telegram.Token
	if token.Mode != SecretModeKeyring || token.SecretName != "telegram.token" || token.Value != "" {
		t.Fatalf("token = %#v, want the keyring reference untouched", token)
	}
}

func TestRestoredDocument_ToConfigResolvesStoredSecrets(t *testing.T) {
	doc := loadFixtureDocument(t, literalSecretsFixture)

	masked, err := doc.MaskedCopy()
	if err != nil {
		t.Fatalf("MaskedCopy: %v", err)
	}
	roundTripped := roundTripDocument(t, masked)
	roundTripped.RestoreMaskedSecrets(doc.SecretValues())

	cfg, err := roundTripped.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	if cfg.Channels.Telegram.Token != "tg-token" {
		t.Errorf("telegram token = %q, want tg-token", cfg.Channels.Telegram.Token)
	}
	if cfg.Channels.Slack.AppToken != "slack-app" {
		t.Errorf("slack app token = %q, want slack-app", cfg.Channels.Slack.AppToken)
	}
	if cfg.Providers == nil {
		t.Fatal("providers missing")
	}
	if got := cfg.Providers.Named["openai"].APIKey; got != "sk-openai" {
		t.Errorf("provider api key = %q, want sk-openai", got)
	}
	if cfg.Tools.Web.Brave.APIKey != "brave-key" {
		t.Errorf("brave api key = %q, want brave-key", cfg.Tools.Web.Brave.APIKey)
	}
}

func roundTripDocument(t *testing.T, doc *EditableDocument) *EditableDocument {
	t.Helper()
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	var out EditableDocument
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("unmarshal document: %v", err)
	}
	return &out
}
