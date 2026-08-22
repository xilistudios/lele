package channels

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/skills"
)

// ---------------------------------------------------------------------------
// rest_system.go — remaining handler branches
// ---------------------------------------------------------------------------

// TestHandleSkillsAvailable_Error covers the upstream fetch failure path (no
// network) which returns a 500.
func TestHandleSkillsAvailable_Error(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.skillInstaller = skills.NewSkillInstaller(t.TempDir())

	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/skills/available")
	ts.channel.handleSkillsAvailable(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("available with no network = %d, want 500", rec.Code)
	}
}

// TestHandleSkillScan_ErrorPaths covers invalid body, missing repo, and the
// upstream scan failure.
func TestHandleSkillScan_ErrorPaths(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.skillInstaller = skills.NewSkillInstaller(t.TempDir())

	// Invalid body.
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/skills/scan")
	req.Method = http.MethodPost
	req.Body = io.NopCloser(strings.NewReader(`not-json`))
	ts.channel.handleSkillScan(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid body = %d, want 400", rec.Code)
	}

	// Missing repo.
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/skills/scan")
	req.Method = http.MethodPost
	req.Body = io.NopCloser(strings.NewReader(`{"repo":""}`))
	ts.channel.handleSkillScan(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing repo = %d, want 400", rec.Code)
	}

	// Scan failure (upstream error) -> 500. Cancel the request context so the
	// installer's HTTP client fails deterministically without touching the
	// network.
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/skills/scan")
	req.Method = http.MethodPost
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	req.Body = io.NopCloser(strings.NewReader(`{"repo":"x/y"}`))
	ts.channel.handleSkillScan(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("scan upstream error = %d, want 500", rec.Code)
	}
}

// TestHandleSkillWorkspaceConfig covers both the nil-config and non-nil paths.
func TestHandleSkillWorkspaceConfig_v6(t *testing.T) {
	ts := newNativeTestServer(t)

	// No config manager set -> empty skills config.
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/skills/workspace-config")
	ts.channel.handleSkillWorkspaceConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("nil cfg status = %d, want 200", rec.Code)
	}

	// With a real skills loader + config manager.
	ts.channel.skillsLoader = skills.NewSkillsLoader(t.TempDir(), t.TempDir(), t.TempDir())
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/skills/workspace-config")
	ts.channel.handleSkillWorkspaceConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("with cfg status = %d, want 200", rec.Code)
	}
}

// TestCfgSnapshot covers the fallback path where agentLoop has a nil snapshot.
func TestCfgSnapshot(t *testing.T) {
	ts := newNativeTestServer(t)

	// Normal path: agent loop returns a config.
	cfg := ts.channel.cfgSnapshot()
	if cfg == nil {
		t.Fatal("cfgSnapshot() = nil, want config")
	}

	// Null loop: cfgSnapshot falls back to defaults + native cfg.
	ts.channel.agentLoop = nil
	cfg = ts.channel.cfgSnapshot()
	if cfg == nil {
		t.Fatal("cfgSnapshot() with nil loop = nil")
	}
	if !cfg.Channels.Native.Enabled {
		t.Error("native config should be preserved in fallback snapshot")
	}
}

// TestHandleProviderModels_ErrorPaths covers validation and unconfigured paths.
func TestHandleProviderModels_ErrorPaths(t *testing.T) {
	ts := newNativeTestServer(t)

	// Invalid provider name percent-encoding (PathValue bypasses URL parsing).
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/providers/x/models")
	req.SetPathValue("name", "%zz")
	ts.channel.handleProviderModels(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad encoding = %d, want 400", rec.Code)
	}

	// Empty provider name.
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/providers//models")
	req.SetPathValue("name", "")
	ts.channel.handleProviderModels(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty name = %d, want 400", rec.Code)
	}

	// Unknown provider.
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/providers/nonexistent/models")
	req.SetPathValue("name", "nonexistent")
	ts.channel.handleProviderModels(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown provider = %d, want 404", rec.Code)
	}

	// Configured provider with an empty api_base (no default).
	ts.loop.config.Providers.Named["vllm"] = config.NamedProviderConfig{
		Type: "vllm",
		ProviderConfig: config.ProviderConfig{
			APIKey: "key",
		},
		Models: map[string]config.ProviderModelConfig{},
	}
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/providers/vllm/models")
	req.SetPathValue("name", "vllm")
	ts.channel.handleProviderModels(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no api_base = %d, want 400", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// rest_auth.go — handleAuthStatus edge cases
// ---------------------------------------------------------------------------

func TestHandleAuthStatus_EdgeCases(t *testing.T) {
	ts := newNativeTestServer(t)

	// Missing auth header -> valid=false.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
	ts.channel.handleAuthStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("missing header status = %d, want 200", rec.Code)
	}
	var resp AuthStatusResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Valid {
		t.Error("expected Valid=false when auth header missing")
	}

	// Invalid bearer prefix.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
	req.Header.Set("Authorization", "not-bearer token")
	ts.channel.handleAuthStatus(rec, req)
	var resp2 AuthStatusResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp2)
	if resp2.Valid {
		t.Error("expected Valid=false for non-bearer auth header")
	}

	// Valid token path.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	ts.channel.handleAuthStatus(rec, req)
	var resp3 AuthStatusResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp3)
	if !resp3.Valid {
		t.Error("expected Valid=true for valid token")
	}
}

// ---------------------------------------------------------------------------
// dingtalk.go — Start error and Send no-webhook paths
// ---------------------------------------------------------------------------

// TestDingTalk_Send_NotRunning covers the not-running error.
func TestDingTalk_Send_NotRunning_v6(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewDingTalkChannel(config.DingTalkConfig{ClientID: "id", ClientSecret: "sec"}, mb)
	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "abc", Content: "hi"})
	if err == nil {
		t.Fatal("expected error when not running")
	}
}

// TestDingTalk_Send_NoWebhook covers running but no session_webhook stored.
func TestDingTalk_Send_NoWebhook(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewDingTalkChannel(config.DingTalkConfig{ClientID: "id", ClientSecret: "sec"}, mb)
	ch.setRunning(true)
	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "abc", Content: "hi"})
	if err == nil {
		t.Fatal("expected error when no session webhook stored")
	}
}

// TestDingTalk_Send_InvalidWebhookType covers a stored non-string value.
func TestDingTalk_Send_InvalidWebhookType(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewDingTalkChannel(config.DingTalkConfig{ClientID: "id", ClientSecret: "sec"}, mb)
	ch.setRunning(true)
	ch.sessionWebhooks.Store("abc", 123)
	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "abc", Content: "hi"})
	if err == nil {
		t.Fatal("expected error when stored webhook is not a string")
	}
}

// ---------------------------------------------------------------------------
// slack.go — parseSlackChatID helpers and startup validation
// ---------------------------------------------------------------------------

func TestParseSlackChatID_v6(t *testing.T) {
	cases := []struct {
		in         string
		channelID  string
		threadTS   string
	}{
		{"C123", "C123", ""},
		{"C123/ts-abc", "C123", "ts-abc"},
	}
	for _, tc := range cases {
		ch, thr := parseSlackChatID(tc.in)
		if ch != tc.channelID || thr != tc.threadTS {
			t.Errorf("parseSlackChatID(%q) = (%q,%q), want (%q,%q)",
				tc.in, ch, thr, tc.channelID, tc.threadTS)
		}
	}
}

func TestNewSlackChannel_Validation(t *testing.T) {
	mb := bus.NewMessageBus()
	_, err := NewSlackChannel(config.SlackConfig{BotToken: "x", AppToken: ""}, mb)
	if err == nil {
		t.Fatal("expected error when app_token missing")
	}
	_, err = NewSlackChannel(config.SlackConfig{BotToken: "", AppToken: "y"}, mb)
	if err == nil {
		t.Fatal("expected error when bot_token missing")
	}
}

// ---------------------------------------------------------------------------
// rest_chat.go — handleChatSend / handleChatHistory validation branches
// ---------------------------------------------------------------------------

func TestHandleChatSend_InvalidBody(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/chat")
	req.Method = http.MethodPost
	req.Body = io.NopCloser(strings.NewReader(`not-json`))
	ts.channel.handleChatSend(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid body = %d, want 400", rec.Code)
	}
}

func TestHandleChatSend_EmptyContent_v6(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/chat")
	req.Method = http.MethodPost
	req.Body = io.NopCloser(strings.NewReader(`{"content":""}`))
	ts.channel.handleChatSend(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty content = %d, want 400", rec.Code)
	}
}

// TestHandleChatHistory_SubagentValidation covers subagent id length & format.
func TestHandleChatHistory_SubagentValidation(t *testing.T) {
	ts := newNativeTestServer(t)

	// Overly long subagent id.
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/chat/sessions/x/subagents/"+strings.Repeat("a", 70)+"/history")
	req.SetPathValue("sessionKey", "x")
	req.SetPathValue("subagentId", strings.Repeat("a", 70))
	ts.channel.handleChatHistory(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("long subagent id = %d, want 400", rec.Code)
	}

	// Invalid subagent id format.
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/chat/sessions/x/subagents/not-valid/history")
	req.SetPathValue("sessionKey", "x")
	req.SetPathValue("subagentId", "not-valid")
	ts.channel.handleChatHistory(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid subagent format = %d, want 400", rec.Code)
	}
}

// TestHandleChatHistory_Forbidden verifies access-denied for a foreign subagent
// session whose parent does not exist.
func TestHandleChatHistory_Forbidden(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	// A conjoined subagent session whose parent is not registered in the agent
	// loop must be rejected.
	req := authenticatedRequest(t, ts, "/api/v1/chat/sessions/unknown:subagent-1/history")
	req.SetPathValue("sessionKey", "unknown:subagent-1")
	ts.channel.handleChatHistory(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign subagent session = %d, want 403", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// manager.go — initChannels with telegram/slack/discord branches
// ---------------------------------------------------------------------------

func TestManager_InitChannels_TelegramSlackDiscord(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Telegram.Enabled = true
	cfg.Channels.Telegram.Token = "123456:YRr0UDaCEvNEsKiQ1JGDAWR74c62pYVtLiJ"
	cfg.Channels.Slack.Enabled = true
	cfg.Channels.Slack.BotToken = "xoxb-bot"
	cfg.Channels.Slack.AppToken = "xapp-app"
	cfg.Channels.Discord.Enabled = true
	cfg.Channels.Discord.Token = "discord-token"

	mgr, err := NewManager(cfg, bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	for _, name := range []string{"telegram", "slack", "discord"} {
		if _, ok := mgr.GetChannel(name); !ok {
			t.Errorf("expected channel %q to be initialized", name)
		}
	}
}

// TestSlackConfigErrorBranch verifies an unconfigured slack channel errors and
// thus bypasses registration recovery (the manager tolerates it).
func TestNewSlackChannel_MissingConfigStillNoError(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Slack.Enabled = true // enabled but no tokens
	mgr, err := NewManager(cfg, bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, ok := mgr.GetChannel("slack"); ok {
		t.Error("slack channel should not be registered without tokens")
	}
}

// TestManager_InitChannels_OtherChannels enables the remaining channel types'
// successful registration branches in initChannels (whatsapp, feishu, maixcam,
// qq, dingtalk, line, onebot).
func TestManager_InitChannels_OtherChannels(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.WhatsApp.Enabled = true
	cfg.Channels.WhatsApp.BridgeURL = "http://localhost:9000"
	cfg.Channels.Feishu.Enabled = true
	cfg.Channels.MaixCam.Enabled = true
	cfg.Channels.QQ.Enabled = true
	cfg.Channels.DingTalk.Enabled = true
	cfg.Channels.DingTalk.ClientID = "ding-client"
	cfg.Channels.DingTalk.ClientSecret = "ding-secret"
	cfg.Channels.LINE.Enabled = true
	cfg.Channels.LINE.ChannelAccessToken = "line-token"
	cfg.Channels.LINE.ChannelSecret = "line-secret"
	cfg.Channels.OneBot.Enabled = true
	cfg.Channels.OneBot.WSUrl = "ws://localhost:3001"

	mgr, err := NewManager(cfg, bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	for _, name := range []string{"whatsapp", "feishu", "maixcam", "qq", "dingtalk", "line", "onebot"} {
		if _, ok := mgr.GetChannel(name); !ok {
			t.Errorf("expected channel %q to be initialized", name)
		}
	}
}

// ---------------------------------------------------------------------------
// util: url encode helper
// ---------------------------------------------------------------------------

var _ = url.PathEscape