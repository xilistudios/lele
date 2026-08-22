//go:build amd64 || arm64 || riscv64 || mips64 || ppc64

package channels

import (
	"context"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestFeishu_NewChannel_MissingCredentials(t *testing.T) {
	mb := bus.NewMessageBus()
	if _, err := NewFeishuChannel(config.FeishuConfig{}, mb); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFeishu_Start_RequiresCredentials(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewFeishuChannel(config.FeishuConfig{AppID: "app", AppSecret: ""}, mb)
	if err := ch.Start(context.Background()); err == nil {
		t.Error("expected error for missing app_secret")
	}
}

func TestFeishu_Send_NotRunning(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewFeishuChannel(config.FeishuConfig{AppID: "app", AppSecret: "sec"}, mb)
	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "oc_1", Content: "hi"})
	if err == nil {
		t.Error("expected error when channel not running")
	}

	ch.setRunning(true)
	err = ch.Send(context.Background(), bus.OutboundMessage{ChatID: "", Content: "hi"})
	if err == nil {
		t.Error("expected error for empty chat ID")
	}
}

func TestFeishu_handleMessageReceive(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewFeishuChannel(config.FeishuConfig{AppID: "app", AppSecret: "sec"}, mb)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buf := consumeInbound(t, mb, ctx)

	contentJSON := `{"text":"hello feishu"}`
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId:  &larkim.UserId{UserId: ptrStr("ou_user1")},
				TenantKey: ptrStr("tenant1"),
			},
			Message: &larkim.EventMessage{
				MessageId:   ptrStr("om_1"),
				ChatId:      ptrStr("oc_chat1"),
				ChatType:    ptrStr("p2p"),
				MessageType: ptrStr("text"),
				Content:     &contentJSON,
			},
		},
	}

	if err := ch.handleMessageReceive(context.Background(), event); err != nil {
		t.Fatalf("handleMessageReceive error: %v", err)
	}

	msg, ok := recvInbound(buf)
	if !ok {
		t.Fatal("expected inbound message")
	}
	if msg.Channel != "feishu" {
		t.Errorf("Channel = %q", msg.Channel)
	}
	if msg.ChatID != "oc_chat1" {
		t.Errorf("ChatID = %q", msg.ChatID)
	}
	if msg.Metadata["peer_kind"] != "direct" {
		t.Errorf("peer_kind = %q", msg.Metadata["peer_kind"])
	}
	if msg.Metadata["tenant_key"] != "tenant1" {
		t.Errorf("tenant_key = %q", msg.Metadata["tenant_key"])
	}
	if msg.Metadata["message_id"] != "om_1" {
		t.Errorf("message_id = %q", msg.Metadata["message_id"])
	}
}

func TestFeishu_handleMessageReceive_NilAndGroup(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewFeishuChannel(config.FeishuConfig{AppID: "app", AppSecret: "sec"}, mb)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buf := consumeInbound(t, mb, ctx)

	// Nil event / nil message → no error, no message.
	if err := ch.handleMessageReceive(context.Background(), nil); err != nil {
		t.Errorf("nil event: %v", err)
	}
	if err := ch.handleMessageReceive(context.Background(), &larkim.P2MessageReceiveV1{}); err != nil {
		t.Errorf("empty event: %v", err)
	}
	if len(buf) != 0 {
		t.Error("nil/empty events should not produce inbound")
	}

	// Group message, empty content, no sender → still routed as group.
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:   ptrStr("oc_group1"),
				ChatType: ptrStr("group"),
				Content:  ptrStr(""),
			},
		},
	}
	if err := ch.handleMessageReceive(context.Background(), event); err != nil {
		t.Fatalf("group event: %v", err)
	}
	msg, ok := recvInbound(buf)
	if !ok {
		t.Fatal("expected inbound for group event")
	}
	if msg.Metadata["peer_kind"] != "group" {
		t.Errorf("peer_kind = %q", msg.Metadata["peer_kind"])
	}
	if msg.SenderID != "unknown" {
		t.Errorf("SenderID = %q", msg.SenderID)
	}
	if msg.Metadata["message_type"] != "" {
		t.Errorf("message_type should be empty, got %q", msg.Metadata["message_type"])
	}
}

func TestFeishu_extractFeishuSenderID(t *testing.T) {
	if got := extractFeishuSenderID(nil); got != "" {
		t.Errorf("nil sender = %q", got)
	}
	if got := extractFeishuSenderID(&larkim.EventSender{}); got != "" {
		t.Errorf("empty sender = %q", got)
	}

	// userId preferred.
	s := &larkim.EventSender{SenderId: &larkim.UserId{
		UserId: ptrStr("uid"), OpenId: ptrStr("oid"), UnionId: ptrStr("unid"),
	}}
	if got := extractFeishuSenderID(s); got != "uid" {
		t.Errorf("userid pick = %q", got)
	}

	// openId fallback when userId empty.
	s2 := &larkim.EventSender{SenderId: &larkim.UserId{OpenId: ptrStr("oid2")}}
	if got := extractFeishuSenderID(s2); got != "oid2" {
		t.Errorf("openid fallback = %q", got)
	}

	// unionId fallback.
	s3 := &larkim.EventSender{SenderId: &larkim.UserId{UnionId: ptrStr("unid3")}}
	if got := extractFeishuSenderID(s3); got != "unid3" {
		t.Errorf("unionid fallback = %q", got)
	}
}

func TestFeishu_extractFeishuMessageContent(t *testing.T) {
	if got := extractFeishuMessageContent(nil); got != "" {
		t.Errorf("nil message = %q", got)
	}
	if got := extractFeishuMessageContent(&larkim.EventMessage{}); got != "" {
		t.Errorf("empty message = %q", got)
	}
	if got := extractFeishuMessageContent(&larkim.EventMessage{Content: ptrStr("")}); got != "" {
		t.Errorf("empty content = %q", got)
	}

	// Text type → parse JSON.
	contentJSON := `{"text":"hi there"}`
	msg := &larkim.EventMessage{MessageType: ptrStr("text"), Content: &contentJSON}
	if got := extractFeishuMessageContent(msg); got != "hi there" {
		t.Errorf("text content = %q", got)
	}

	// Text type but invalid JSON → fallback to raw.
	badJSON := `not-json`
	msg2 := &larkim.EventMessage{MessageType: ptrStr("text"), Content: &badJSON}
	if got := extractFeishuMessageContent(msg2); got != "not-json" {
		t.Errorf("bad json fallback = %q", got)
	}

	// Non-text type → raw content.
	msg3 := &larkim.EventMessage{MessageType: ptrStr("image"), Content: ptrStr("{\"image_key\":\"x\"}")}
	if got := extractFeishuMessageContent(msg3); got != `{"image_key":"x"}` {
		t.Errorf("image content = %q", got)
	}
}

func TestFeishu_stringValue(t *testing.T) {
	if got := stringValue(nil); got != "" {
		t.Errorf("nil = %q", got)
	}
	if got := stringValue(ptrStr("abc")); got != "abc" {
		t.Errorf("val = %q", got)
	}
}

func ptrStr(s string) *string {
	return &s
}
