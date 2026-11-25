package channels

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

const restConfigSecretsFixture = `{
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

var restConfigSecretPaths = []string{
	"channels.telegram.token",
	"channels.feishu.app_id",
	"channels.feishu.app_secret",
	"channels.feishu.encrypt_key",
	"channels.feishu.verification_token",
	"channels.discord.token",
	"channels.qq.app_id",
	"channels.qq.app_secret",
	"channels.dingtalk.client_id",
	"channels.dingtalk.client_secret",
	"channels.slack.bot_token",
	"channels.slack.app_token",
	"channels.line.channel_secret",
	"channels.line.channel_access_token",
	"channels.onebot.access_token",
	"providers.openai.api_key",
	"tools.web.brave.api_key",
	"tools.web.perplexity.api_key",
}

var restConfigLiteralValues = []string{
	"tg-token",
	"feishu-id",
	"feishu-secret",
	"feishu-encrypt",
	"feishu-verify",
	"discord-token",
	"qq-id",
	"qq-secret",
	"dingtalk-id",
	"dingtalk-secret",
	"slack-bot",
	"slack-app",
	"line-secret",
	"line-token",
	"onebot-token",
	"sk-openai",
	"brave-key",
	"perplexity-key",
}

func newRestConfigChannel(t *testing.T, content string) (*NativeChannel, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return &NativeChannel{configPath: path}, path
}

func getConfigDocument(t *testing.T, nc *NativeChannel) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	w := httptest.NewRecorder()
	nc.handleGetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /config status = %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal GET response: %v", err)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(response.Config, &document); err != nil {
		t.Fatalf("unmarshal config document: %v", err)
	}
	return document
}

func secretNode(t *testing.T, document map[string]interface{}, path string) map[string]interface{} {
	t.Helper()
	var current interface{} = document
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]interface{})
		if !ok {
			t.Fatalf("%s: %q is not an object", path, part)
		}
		current, ok = object[part]
		if !ok {
			t.Fatalf("%s: key %q missing", path, part)
		}
	}
	node, ok := current.(map[string]interface{})
	if !ok {
		t.Fatalf("%s: value is not a secret object: %#v", path, current)
	}
	return node
}

func putConfigDocument(t *testing.T, nc *NativeChannel, document map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(ConfigUpdateRequest{Config: document})
	if err != nil {
		t.Fatalf("marshal PUT body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	nc.handlePutConfig(w, req)
	return w
}

func diskConfigTree(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var tree map[string]interface{}
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}
	return tree
}

func diskSecret(t *testing.T, path, secretPath string) interface{} {
	t.Helper()
	var current interface{} = diskConfigTree(t, path)
	for _, part := range strings.Split(secretPath, ".") {
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}

func TestGetConfig_MasksLiteralSecrets(t *testing.T) {
	nc, _ := newRestConfigChannel(t, restConfigSecretsFixture)

	document := getConfigDocument(t, nc)

	for _, path := range restConfigSecretPaths {
		node := secretNode(t, document, path)
		if node["mode"] != "literal" {
			t.Fatalf("%s mode = %v, want literal", path, node["mode"])
		}
		if value, ok := node["value"]; ok && value != "" {
			t.Fatalf("%s leaked value %q", path, value)
		}
		if node["has_value"] != true {
			t.Fatalf("%s has_value = %v, want true", path, node["has_value"])
		}
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	for _, literal := range restConfigLiteralValues {
		if strings.Contains(string(encoded), literal) {
			t.Fatalf("GET /config leaked %q: %s", literal, encoded)
		}
	}
}

func TestGetConfig_EnvKeyringReferencesOnly(t *testing.T) {
	const fixture = `{
		"agents": {"defaults": {"workspace": "/ws", "provider": "openai", "model": "gpt-4", "max_tokens": 4096}},
		"channels": {
			"telegram": {"enabled": true, "token": "{{ENV_LELE_REST_TG_TOKEN}}"},
			"discord": {"enabled": true, "token": "{{SECRET:discord.token}}"}
		}
	}`
	t.Setenv("LELE_REST_TG_TOKEN", "env-secret-value")
	nc, _ := newRestConfigChannel(t, fixture)

	document := getConfigDocument(t, nc)

	telegram := secretNode(t, document, "channels.telegram.token")
	if telegram["mode"] != "env" || telegram["env_name"] != "LELE_REST_TG_TOKEN" {
		t.Fatalf("telegram token = %#v, want the env reference only", telegram)
	}
	if value, ok := telegram["value"]; ok && value != "" {
		t.Fatalf("telegram token leaked %q", value)
	}

	discord := secretNode(t, document, "channels.discord.token")
	if discord["mode"] != "keyring" || discord["secret_name"] != "discord.token" {
		t.Fatalf("discord token = %#v, want the keyring reference only", discord)
	}
	if value, ok := discord["value"]; ok && value != "" {
		t.Fatalf("discord token leaked %q", value)
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	if strings.Contains(string(encoded), "env-secret-value") {
		t.Fatalf("GET /config leaked the resolved env value: %s", encoded)
	}
}

func TestPutConfig_MaskedRoundTripKept(t *testing.T) {
	nc, path := newRestConfigChannel(t, restConfigSecretsFixture)

	document := getConfigDocument(t, nc)
	w := putConfigDocument(t, nc, document)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /config status = %d: %s", w.Code, w.Body.String())
	}

	for _, secretPath := range restConfigSecretPaths {
		stored := diskSecret(t, path, secretPath)
		want := secretValueForPath(t, secretPath)
		if stored != want {
			t.Fatalf("%s on disk = %v, want %q (credential destroyed by the masked round-trip)", secretPath, stored, want)
		}
	}
}

func TestPutConfig_ReplaceLiteral(t *testing.T) {
	nc, path := newRestConfigChannel(t, restConfigSecretsFixture)

	document := getConfigDocument(t, nc)
	secretNode(t, document, "channels.telegram.token")["value"] = "tg-rotated"
	w := putConfigDocument(t, nc, document)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /config status = %d: %s", w.Code, w.Body.String())
	}

	if stored := diskSecret(t, path, "channels.telegram.token"); stored != "tg-rotated" {
		t.Fatalf("channels.telegram.token on disk = %v, want tg-rotated", stored)
	}
	if stored := diskSecret(t, path, "channels.discord.token"); stored != "discord-token" {
		t.Fatalf("channels.discord.token on disk = %v, want the untouched stored value", stored)
	}
}

func TestPutConfig_EmptyModeClears(t *testing.T) {
	nc, path := newRestConfigChannel(t, restConfigSecretsFixture)

	document := getConfigDocument(t, nc)
	telegram := document["channels"].(map[string]interface{})["telegram"].(map[string]interface{})
	telegram["token"] = map[string]interface{}{"mode": "empty"}
	w := putConfigDocument(t, nc, document)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /config status = %d: %s", w.Code, w.Body.String())
	}

	if stored := diskSecret(t, path, "channels.telegram.token"); stored != nil {
		t.Fatalf("channels.telegram.token on disk = %v, want it cleared", stored)
	}
	if stored := diskSecret(t, path, "channels.discord.token"); stored != "discord-token" {
		t.Fatalf("channels.discord.token on disk = %v, want the untouched stored value", stored)
	}
}

func TestPutConfig_OmittedSecretKept(t *testing.T) {
	nc, path := newRestConfigChannel(t, restConfigSecretsFixture)

	document := getConfigDocument(t, nc)
	telegram := document["channels"].(map[string]interface{})["telegram"].(map[string]interface{})
	delete(telegram, "token")
	w := putConfigDocument(t, nc, document)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /config status = %d: %s", w.Code, w.Body.String())
	}

	if stored := diskSecret(t, path, "channels.telegram.token"); stored != "tg-token" {
		t.Fatalf("channels.telegram.token on disk = %v, want tg-token", stored)
	}
}

func TestPutConfig_ValidationSeesRealValue(t *testing.T) {
	nc, _ := newRestConfigChannel(t, restConfigSecretsFixture)

	document := getConfigDocument(t, nc)
	w := putConfigDocument(t, nc, document)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /config status = %d: %s", w.Code, w.Body.String())
	}
	var response ConfigUpdateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal PUT response: %v", err)
	}
	if len(response.Errors) > 0 {
		t.Fatalf("unexpected validation errors: %v", response.Errors)
	}

	doc, _, err := config.LoadEditableDocument(nc.getConfigPath())
	if err != nil {
		t.Fatalf("reload document: %v", err)
	}
	cfg, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	if cfg.Channels.Telegram.Token != "tg-token" {
		t.Fatalf("runtime telegram token = %q, want tg-token", cfg.Channels.Telegram.Token)
	}
}

func TestPutConfig_ResponseMasked(t *testing.T) {
	nc, _ := newRestConfigChannel(t, restConfigSecretsFixture)

	document := getConfigDocument(t, nc)
	secretNode(t, document, "channels.telegram.token")["value"] = "tg-rotated"
	w := putConfigDocument(t, nc, document)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /config status = %d: %s", w.Code, w.Body.String())
	}

	for _, literal := range append([]string{"tg-rotated"}, restConfigLiteralValues...) {
		if strings.Contains(w.Body.String(), literal) {
			t.Fatalf("PUT /config response leaked %q: %s", literal, w.Body.String())
		}
	}

	var response struct {
		Config map[string]interface{} `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal PUT response: %v", err)
	}
	token := secretNode(t, response.Config, "channels.telegram.token")
	if token["mode"] != "literal" || token["has_value"] != true {
		t.Fatalf("PUT response token = %#v, want a masked literal", token)
	}
}

func secretValueForPath(t *testing.T, secretPath string) string {
	t.Helper()
	values := map[string]string{
		"channels.telegram.token":            "tg-token",
		"channels.feishu.app_id":             "feishu-id",
		"channels.feishu.app_secret":         "feishu-secret",
		"channels.feishu.encrypt_key":        "feishu-encrypt",
		"channels.feishu.verification_token": "feishu-verify",
		"channels.discord.token":             "discord-token",
		"channels.qq.app_id":                 "qq-id",
		"channels.qq.app_secret":             "qq-secret",
		"channels.dingtalk.client_id":        "dingtalk-id",
		"channels.dingtalk.client_secret":    "dingtalk-secret",
		"channels.slack.bot_token":           "slack-bot",
		"channels.slack.app_token":           "slack-app",
		"channels.line.channel_secret":       "line-secret",
		"channels.line.channel_access_token": "line-token",
		"channels.onebot.access_token":       "onebot-token",
		"providers.openai.api_key":           "sk-openai",
		"tools.web.brave.api_key":            "brave-key",
		"tools.web.perplexity.api_key":       "perplexity-key",
	}
	value, ok := values[secretPath]
	if !ok {
		t.Fatalf("no fixture value for %s", secretPath)
	}
	return value
}
