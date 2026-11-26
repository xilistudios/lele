package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func reflectedSecretPaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	var walk func(v reflect.Value, path string, depth int)
	walk = func(v reflect.Value, path string, depth int) {
		if depth > 24 {
			t.Fatalf("reflection walk did not terminate at %q", path)
		}
		switch v.Kind() {
		case reflect.Struct:
			if v.Type() == reflect.TypeOf(SecretValue{}) {
				paths = append(paths, path)
				return
			}
			for i := 0; i < v.NumField(); i++ {
				field := v.Type().Field(i)
				if field.PkgPath != "" {
					continue
				}
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if name == "" || name == "-" {
					continue
				}
				next := name
				if path != "" {
					next = path + "." + name
				}
				walk(v.Field(i), next, depth+1)
			}
		case reflect.Ptr:
			walk(reflect.New(v.Type().Elem()).Elem(), path, depth+1)
		case reflect.Slice:
			walk(reflect.New(v.Type().Elem()).Elem(), path+".<index>", depth+1)
		case reflect.Map:
			if v.Type().Elem().Kind() == reflect.Struct {
				walk(reflect.New(v.Type().Elem()).Elem(), path+".<name>", depth+1)
			}
		}
	}
	walk(reflect.ValueOf(EditableDocument{}), "", 0)
	sort.Strings(paths)
	return paths
}

func normalizedSecretPaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		if parts := strings.Split(path, "."); len(parts) == 3 && parts[0] == "providers" && parts[2] == "api_key" {
			path = "providers.<name>.api_key"
		}
		normalized = append(normalized, path)
	}
	sort.Strings(normalized)
	return normalized
}

func enumeratedSecretPaths(doc *EditableDocument) []string {
	var paths []string
	doc.forEachSecret(func(path string, _ *SecretValue) {
		paths = append(paths, path)
	})
	sort.Strings(paths)
	return paths
}

func TestForEachSecret_MatchesTheTypes(t *testing.T) {
	doc := loadFixtureDocument(t, literalSecretsFixture)

	for _, path := range reflectedSecretPaths(t) {
		if strings.Contains(path, "<") {
			continue
		}
		if value, ok := doc.SecretValues()[path]; !ok || value.Mode != SecretModeLiteral {
			t.Fatalf("%s missing from SecretValues: %#v (ok=%v)", path, value, ok)
		}
	}

	got := normalizedSecretPaths(enumeratedSecretPaths(doc))
	want := reflectedSecretPaths(t)
	if len(got) != len(want) {
		t.Fatalf("forEachSecret visits %d secret paths, the document types hold %d:\n got %v\nwant %v", len(got), len(want), got, want)
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

const envDefaultFixture = `{
	"agents": {"defaults": {"workspace": "/ws", "provider": "openai", "model": "gpt-4", "max_tokens": 4096}},
	"channels": {
		"telegram": {"enabled": true, "token": "{{ENV_LELE_TEST_TG_TOKEN:tg-fallback-credential}}"},
		"discord": {"enabled": true, "token": "{{ENV_LELE_TEST_DISCORD_TOKEN}}"}
	}
}`

func TestMaskedCopy_MasksEnvDefault(t *testing.T) {
	doc := loadFixtureDocument(t, envDefaultFixture)

	masked, err := doc.MaskedCopy()
	if err != nil {
		t.Fatalf("MaskedCopy: %v", err)
	}

	telegram := masked.Channels.Telegram.Token
	if telegram.Mode != SecretModeEnv || telegram.EnvName != "LELE_TEST_TG_TOKEN" {
		t.Fatalf("telegram token = %#v, want the env reference", telegram)
	}
	if telegram.EnvDefault != nil {
		t.Fatalf("masked env fallback = %q, want it cleared", *telegram.EnvDefault)
	}
	if !telegram.HasDefault {
		t.Fatalf("masked env fallback did not report has_default: %#v", telegram)
	}

	discord := masked.Channels.Discord.Token
	if discord.EnvDefault != nil || discord.HasDefault {
		t.Fatalf("discord token = %#v, want no fallback reported", discord)
	}

	encoded, err := json.Marshal(masked)
	if err != nil {
		t.Fatalf("marshal masked doc: %v", err)
	}
	if strings.Contains(string(encoded), "tg-fallback-credential") {
		t.Fatalf("masked document still contains the env fallback: %s", encoded)
	}
	if doc.Channels.Telegram.Token.EnvDefault == nil {
		t.Fatal("MaskedCopy mutated the source document: the env fallback is gone")
	}
}

func TestRestoreMaskedSecrets_KeepsEnvDefault(t *testing.T) {
	doc := loadFixtureDocument(t, envDefaultFixture)

	masked, err := doc.MaskedCopy()
	if err != nil {
		t.Fatalf("MaskedCopy: %v", err)
	}
	roundTripped := roundTripDocument(t, masked)
	roundTripped.RestoreMaskedSecrets(doc.SecretValues())

	token := roundTripped.Channels.Telegram.Token
	if token.EnvDefault == nil || *token.EnvDefault != "tg-fallback-credential" {
		t.Fatalf("telegram env fallback = %#v, want the stored fallback back", token.EnvDefault)
	}
	if !token.HasDefault {
		t.Fatalf("restored env fallback did not report has_default: %#v", token)
	}
	if got := envPlaceholderString(token); got != "{{ENV_LELE_TEST_TG_TOKEN:tg-fallback-credential}}" {
		t.Fatalf("restored placeholder = %q, want it byte-identical", got)
	}

	discord := roundTripped.Channels.Discord.Token
	if discord.EnvDefault != nil || discord.HasDefault {
		t.Fatalf("discord token = %#v, want no invented fallback", discord)
	}
}

func TestRestoreMaskedSecrets_ReplacesEnvDefault(t *testing.T) {
	doc := loadFixtureDocument(t, envDefaultFixture)
	stored := doc.SecretValues()

	rotated := "tg-rotated-fallback"
	incoming := roundTripDocument(t, doc)
	incoming.Channels.Telegram.Token = SecretValue{Mode: SecretModeEnv, EnvName: "LELE_TEST_TG_TOKEN", EnvDefault: &rotated}
	incoming.RestoreMaskedSecrets(stored)

	token := incoming.Channels.Telegram.Token
	if token.EnvDefault == nil || *token.EnvDefault != rotated {
		t.Fatalf("telegram env fallback = %#v, want the incoming replacement %q", token.EnvDefault, rotated)
	}
}

func TestRestoreMaskedSecrets_EmptyModeClearsEnvDefault(t *testing.T) {
	doc := loadFixtureDocument(t, envDefaultFixture)
	stored := doc.SecretValues()

	incoming := roundTripDocument(t, doc)
	incoming.Channels.Telegram.Token = SecretValue{Mode: SecretModeEmpty}
	incoming.RestoreMaskedSecrets(stored)

	token := incoming.Channels.Telegram.Token
	if token.Mode != SecretModeEmpty || token.EnvDefault != nil || token.HasDefault {
		t.Fatalf("telegram token = %#v, want it cleared", token)
	}
}

func TestRestoreMaskedSecrets_EnvNameChangeKeepsStoredFallback(t *testing.T) {
	doc := loadFixtureDocument(t, envDefaultFixture)
	stored := doc.SecretValues()

	incoming := roundTripDocument(t, doc)
	incoming.Channels.Telegram.Token = SecretValue{Mode: SecretModeEnv, EnvName: "LELE_TEST_TG_TOKEN_V2"}
	incoming.RestoreMaskedSecrets(stored)

	token := incoming.Channels.Telegram.Token
	if got := envPlaceholderString(token); got != "{{ENV_LELE_TEST_TG_TOKEN_V2:tg-fallback-credential}}" {
		t.Fatalf("placeholder = %q, want the stored fallback kept across an env name change", got)
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
