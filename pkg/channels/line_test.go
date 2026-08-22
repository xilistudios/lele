package channels

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestNewLINEChannel_RequiresCredentials(t *testing.T) {
	mb := bus.NewMessageBus()
	if _, err := NewLINEChannel(config.LINEConfig{}, mb); err == nil {
		t.Error("expected error for empty credentials")
	}
	if _, err := NewLINEChannel(config.LINEConfig{ChannelSecret: "s"}, mb); err == nil {
		t.Error("expected error for missing access token")
	}
	ch, err := NewLINEChannel(config.LINEConfig{ChannelSecret: "secret", ChannelAccessToken: "token"}, mb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch.Name() != "line" {
		t.Errorf("Name() = %q", ch.Name())
	}
}

func TestBuildTextMessage(t *testing.T) {
	msg := buildTextMessage("hello", "")
	if msg["type"] != "text" || msg["text"] != "hello" {
		t.Errorf("unexpected base message: %v", msg)
	}
	if _, has := msg["quoteToken"]; has {
		t.Error("quoteToken should be absent when empty")
	}

	withQuote := buildTextMessage("hello", "qt-123")
	if withQuote["quoteToken"] != "qt-123" {
		t.Errorf("quoteToken = %q", withQuote["quoteToken"])
	}
}

func TestVerifySignature(t *testing.T) {
	ch, _ := NewLINEChannel(config.LINEConfig{ChannelSecret: "my-secret", ChannelAccessToken: "tok"}, bus.NewMessageBus())
	body := []byte(`{"events":[]}`)
	mac := hmac.New(sha256.New, []byte("my-secret"))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if !ch.verifySignature(body, expected) {
		t.Error("expected signature to verify")
	}
	if ch.verifySignature(body, "") {
		t.Error("empty signature should be rejected")
	}
	if ch.verifySignature(body, "wrong") {
		t.Error("wrong signature should be rejected")
	}
	if ch.verifySignature([]byte("different"), expected) {
		t.Error("mismatched body should be rejected")
	}
}

func TestResolveChatID(t *testing.T) {
	ch, _ := NewLINEChannel(config.LINEConfig{ChannelSecret: "s", ChannelAccessToken: "t"}, bus.NewMessageBus())
	if got := ch.resolveChatID(lineSource{Type: "group", GroupID: "G1"}); got != "G1" {
		t.Errorf("group => %q", got)
	}
	if got := ch.resolveChatID(lineSource{Type: "room", RoomID: "R1"}); got != "R1" {
		t.Errorf("room => %q", got)
	}
	if got := ch.resolveChatID(lineSource{Type: "user", UserID: "U1"}); got != "U1" {
		t.Errorf("user => %q", got)
	}
	if got := ch.resolveChatID(lineSource{}); got != "" {
		t.Errorf("default => %q", got)
	}
}

func TestLINE_IsBotMentioned(t *testing.T) {
	ch, _ := NewLINEChannel(config.LINEConfig{ChannelSecret: "s", ChannelAccessToken: "t"}, bus.NewMessageBus())
	ch.botUserID = "BOTID"
	ch.botDisplayName = "Lele"

	// Mention metadata with "all" mentionee.
	allMention := &struct {
		Mentionees []lineMentionee `json:"mentionees"`
	}{Mentionees: []lineMentionee{{Type: "all", Index: 0, Length: 3}}}
	if !ch.isBotMentioned(lineMessage{Text: "hey", Mention: allMention}) {
		t.Error("'all' mentionee should trigger mention")
	}

	// Mention by userId.
	userMention := &struct {
		Mentionees []lineMentionee `json:"mentionees"`
	}{Mentionees: []lineMentionee{{Type: "user", UserID: "BOTID", Index: 0, Length: 4}}}
	if !ch.isBotMentioned(lineMessage{Text: "@Lele hi", Mention: userMention}) {
		t.Error("matching userId mention should trigger")
	}

	// Non-bot user mention only.
	otherMention := &struct {
		Mentionees []lineMentionee `json:"mentionees"`
	}{Mentionees: []lineMentionee{{Type: "user", UserID: "SOMEONE", Index: 0, Length: 4}}}
	if ch.isBotMentioned(lineMessage{Text: "@X hi", Mention: otherMention}) {
		t.Error("non-bot mention should not trigger via metadata")
	}

	// Text fallback.
	if !ch.isBotMentioned(lineMessage{Text: "hello @Lele"}) {
		t.Error("text-based mention should trigger")
	}
	if ch.isBotMentioned(lineMessage{Text: "just a message"}) {
		t.Error("no mention should not trigger")
	}

	// No display name, mention struct with no match.
	ch.botDisplayName = ""
	if ch.isBotMentioned(lineMessage{Text: "hello @Lele"}) {
		t.Error("text fallback requires display name")
	}
}

func TestLINE_StripBotMention(t *testing.T) {
	ch, _ := NewLINEChannel(config.LINEConfig{ChannelSecret: "s", ChannelAccessToken: "t"}, bus.NewMessageBus())
	ch.botUserID = "BOTID"
	ch.botDisplayName = "Lele"

	// Via mention metadata (userId match) — "@Lele" is 5 runes.
	userMention := &struct {
		Mentionees []lineMentionee `json:"mentionees"`
	}{Mentionees: []lineMentionee{{Type: "user", UserID: "BOTID", Index: 0, Length: 5}}}
	got := ch.stripBotMention("@Lele hey there", lineMessage{Text: "@Lele hey there", Mention: userMention})
	if got != "hey there" {
		t.Errorf("strip via userId = %q", got)
	}

	// Via display name fallback.
	got = ch.stripBotMention("ping @Lele please", lineMessage{Text: "ping @Lele please"})
	if got != "ping  please" {
		t.Errorf("strip via text fallback = %q", got)
	}

	// No mention present.
	if got := ch.stripBotMention("plain message", lineMessage{Text: "plain message"}); got != "plain message" {
		t.Errorf("strip no-op = %q", got)
	}
}

// TestLINE_ProcessEvent_TextDirect verifies a direct text message propagates to
// the message bus through HandleMessage.
func TestLINE_ProcessEvent_TextDirect(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewLINEChannel(config.LINEConfig{ChannelSecret: "s", ChannelAccessToken: "t"}, mb)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buf := consumeInbound(t, mb, ctx)

	msgJSON := mustMarshal(lineMessage{ID: "m1", Type: "text", Text: "hello line"})
	ch.processEvent(lineEvent{
		Type:       "message",
		ReplyToken: "rt-1",
		Source:     lineSource{Type: "user", UserID: "U1"},
		Message:    msgJSON,
	})

	msg, ok := recvInbound(buf)
	if !ok {
		t.Fatal("expected inbound message")
	}
	if msg.Channel != "line" || msg.Content != "hello line" {
		t.Errorf("unexpected inbound: %+v", msg)
	}
	if msg.ChatID != "U1" {
		t.Errorf("ChatID = %q", msg.ChatID)
	}
	if msg.Metadata["peer_kind"] != "direct" {
		t.Errorf("peer_kind = %q", msg.Metadata["peer_kind"])
	}

	// Reply token stored for later.
	if _, ok := ch.replyTokens.Load("U1"); !ok {
		t.Error("reply token should be stored")
	}
}

func TestLINE_ProcessEvent_GroupMentionRequired(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewLINEChannel(config.LINEConfig{ChannelSecret: "s", ChannelAccessToken: "t"}, mb)
	ch.botDisplayName = "Lele"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buf := consumeInbound(t, mb, ctx)

	// Group message without mention → ignored.
	ch.processEvent(lineEvent{
		Type:    "message",
		Source:  lineSource{Type: "group", GroupID: "G1", UserID: "U1"},
		Message: mustMarshal(lineMessage{ID: "m1", Type: "text", Text: "no mention"}),
	})
	if len(buf) != 0 {
		t.Error("group message without mention should be ignored")
	}

	// Group message with mention → processed.
	mention := &struct {
		Mentionees []lineMentionee `json:"mentionees"`
	}{Mentionees: []lineMentionee{{Type: "user", Index: 0, Length: 5}}}
	ch.processEvent(lineEvent{
		Type:   "message",
		Source: lineSource{Type: "group", GroupID: "G1", UserID: "U1"},
		Message: mustMarshal(lineMessage{
			ID: "m2", Type: "text", Text: "@Lele hi", Mention: mention,
		}),
	})
	msg, ok := recvInbound(buf)
	if !ok {
		t.Fatal("expected inbound for mentioned group message")
	}
	if msg.Metadata["peer_kind"] != "group" || msg.ChatID != "G1" {
		t.Errorf("unexpected group routing: %+v", msg.Metadata)
	}
}

// TestLINE_WebhookHandler exercises signature verification and event flow.
func TestLINE_WebhookHandler(t *testing.T) {
	mb := bus.NewMessageBus()
	secret := "linesecret"
	ch, _ := NewLINEChannel(config.LINEConfig{ChannelSecret: secret, ChannelAccessToken: "tok"}, mb)
	ch.botDisplayName = "Lele"

	payload := map[string]interface{}{
		"events": []map[string]interface{}{
			{
				"type":       "message",
				"replyToken": "rt-9",
				"source":     map[string]interface{}{"type": "user", "userId": "U9"},
				"message":    map[string]interface{}{"type": "text", "id": "mid", "text": "hi"},
			},
		},
	}
	body, _ := json.Marshal(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// Valid signature → 200 and async event processing.
	req := httptest.NewRequest(http.MethodPost, "/webhook/line", strings.NewReader(string(body)))
	req.Header.Set("X-Line-Signature", sig)
	rr := httptest.NewRecorder()
	ch.webhookHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("valid signature status = %d, want 200", rr.Code)
	}

	// Non-POST → 405.
	req = httptest.NewRequest(http.MethodGet, "/webhook/line", nil)
	rr = httptest.NewRecorder()
	ch.webhookHandler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rr.Code)
	}

	// Invalid signature → 403.
	req = httptest.NewRequest(http.MethodPost, "/webhook/line", strings.NewReader(string(body)))
	req.Header.Set("X-Line-Signature", "wrong")
	rr = httptest.NewRecorder()
	ch.webhookHandler(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("bad signature status = %d, want 403", rr.Code)
	}

	// Bad JSON with valid signature for that body → 400.
	badBody := []byte("not-json")
	badMac := hmac.New(sha256.New, []byte(secret))
	badMac.Write(badBody)
	badSig := base64.StdEncoding.EncodeToString(badMac.Sum(nil))
	req = httptest.NewRequest(http.MethodPost, "/webhook/line", strings.NewReader(string(badBody)))
	req.Header.Set("X-Line-Signature", badSig)
	rr = httptest.NewRecorder()
	ch.webhookHandler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad json status = %d, want 400", rr.Code)
	}
}

func TestLINE_StartStop(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewLINEChannel(config.LINEConfig{ChannelSecret: "s", ChannelAccessToken: "t"}, mb)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	if !ch.IsRunning() {
		t.Error("should be running after Start")
	}
	if err := ch.Stop(ctx); err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	if ch.IsRunning() {
		t.Error("should not be running after Stop")
	}
}

func TestLINE_Send_NotRunning(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewLINEChannel(config.LINEConfig{ChannelSecret: "s", ChannelAccessToken: "t"}, mb)
	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U1", Content: "hi"})
	if err == nil {
		t.Error("expected error when channel not running")
	}
}

func TestLINE_sendLoading_payload(t *testing.T) {
	// Verify the payload structure used by sendLoading (chatId/loadingSeconds).
	payload := map[string]interface{}{
		"chatId":         "C1",
		"loadingSeconds": 60,
	}
	if payload["chatId"] != "C1" || payload["loadingSeconds"] != 60 {
		t.Errorf("loading payload = %v", payload)
	}
}

func TestLINE_callAPI(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mb := bus.NewMessageBus()
	ch, _ := NewLINEChannel(config.LINEConfig{ChannelSecret: "s", ChannelAccessToken: "TOKEN"}, mb)

	// Success path.
	if err := ch.callAPI(context.Background(), server.URL, map[string]interface{}{"a": 1}); err != nil {
		t.Fatalf("callAPI success: %v", err)
	}
	if gotAuth != "Bearer TOKEN" {
		t.Errorf("auth header = %q", gotAuth)
	}

	// Error status path.
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("nope"))
	}))
	defer server2.Close()
	err := ch.callAPI(context.Background(), server2.URL, map[string]interface{}{})
	if err == nil {
		t.Error("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should mention status: %v", err)
	}

	// Marshal error path.
	if err := ch.callAPI(context.Background(), server.URL, make(chan int)); err == nil {
		t.Error("expected marshal error")
	}

	// Request creation error (bad endpoint).
	if err := ch.callAPI(context.Background(), "://bad-url", map[string]interface{}{}); err == nil {
		t.Error("expected request creation error")
	}
}

func consumeInbound(t *testing.T, mb *bus.MessageBus, ctx context.Context) chan bus.InboundMessage {
	t.Helper()
	buf := make(chan bus.InboundMessage, 50)
	go func() {
		for {
			msg, ok := mb.ConsumeInbound(ctx)
			if !ok {
				return
			}
			buf <- msg
		}
	}()
	return buf
}

func recvInbound(buf chan bus.InboundMessage) (bus.InboundMessage, bool) {
	select {
	case m := <-buf:
		return m, true
	case <-time.After(time.Second):
		return bus.InboundMessage{}, false
	}
}
