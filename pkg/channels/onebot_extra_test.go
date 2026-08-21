package channels

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestOneBot_NewChannel_SetTranscriber(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)
	ch.SetTranscriber(nil)
	if ch.Name() != "onebot" {
		t.Errorf("Name = %q", ch.Name())
	}
}

func TestOneBot_BuildMessageSegments(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())

	// no lastMessage => just text
	seg := ch.buildMessageSegments("private:1", "hello")
	if len(seg) != 1 || seg[0].Type != "text" {
		t.Fatalf("segments = %+v", seg)
	}

	// with lastMessage => reply + text
	ch.lastMessageID.Store("private:1", "99")
	seg = ch.buildMessageSegments("private:1", "hello")
	if len(seg) != 2 || seg[0].Type != "reply" || seg[1].Type != "text" {
		t.Fatalf("reply segments = %+v", seg)
	}
}

func TestOneBot_BuildSendRequest(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())

	// group
	action, params, err := ch.buildSendRequest(bus.OutboundMessage{ChatID: "group:5", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if action != "send_group_msg" {
		t.Errorf("group action = %q", action)
	}
	pm := params.(map[string]interface{})
	if pm["group_id"].(int64) != 5 {
		t.Errorf("group_id = %v", pm["group_id"])
	}

	// private with prefix
	action, params, err = ch.buildSendRequest(bus.OutboundMessage{ChatID: "private:7", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if action != "send_private_msg" {
		t.Errorf("private action = %q", action)
	}

	// bare id
	action, _, err = ch.buildSendRequest(bus.OutboundMessage{ChatID: "7", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if action != "send_private_msg" {
		t.Errorf("bare action = %q", action)
	}

	// invalid id
	if _, _, err = ch.buildSendRequest(bus.OutboundMessage{ChatID: "group:abc", Content: "hi"}); err == nil {
		t.Fatal("expected error for invalid group id")
	}
}

func TestOneBot_HandleRawEvent_NoopTypes(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)

	ch.handleRawEvent(&oneBotRawEvent{PostType: "message_sent"})
	ch.handleRawEvent(&oneBotRawEvent{PostType: "meta_event"})
	ch.handleRawEvent(&oneBotRawEvent{PostType: "notice"})
	ch.handleRawEvent(&oneBotRawEvent{PostType: "request"})
	ch.handleRawEvent(&oneBotRawEvent{PostType: ""})
	ch.handleRawEvent(&oneBotRawEvent{PostType: "unknown"})

	// message event with disallowed user gets filtered
	ch.handleRawEvent(&oneBotRawEvent{
		PostType:    "message",
		MessageType: "private",
		UserID:      json.RawMessage(`555`),
		Message:     json.RawMessage(`"hi"`),
		MessageID:   json.RawMessage(`"m1"`),
	})
}

func TestOneBot_HandleMessage_Private(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)

	raw := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "private",
		UserID:      json.RawMessage(`100`),
		MessageID:   json.RawMessage(`"m2"`),
		Message:     json.RawMessage(`"hello private"`),
		RawMessage:  `hello private`,
	}
	ch.handleRawEvent(raw)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inb, ok := mb.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no inbound published")
	}
	if inb.ChatID != "private:100" {
		t.Errorf("chatID = %q", inb.ChatID)
	}
	if inb.Content != "hello private" {
		t.Errorf("content = %q", inb.Content)
	}
	if inb.Metadata["peer_kind"] != "direct" {
		t.Errorf("peer_kind = %q", inb.Metadata["peer_kind"])
	}
}

func TestOneBot_HandleMessage_GroupMention(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)
	ch.selfID = 100

	raw := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "group",
		UserID:      json.RawMessage(`100`),
		GroupID:     json.RawMessage(`5`),
		SelfID:      json.RawMessage(`100`),
		MessageID:   json.RawMessage(`"g1"`),
		Message:     json.RawMessage(`"[CQ:at,qq=100] hey team"`),
		RawMessage:  `"[CQ:at,qq=100] hey team"`,
		Sender:      json.RawMessage(`{"user_id":100,"nickname":"Nicky","card":"Card"}`),
	}
	ch.handleRawEvent(raw)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inb, ok := mb.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no inbound published")
	}
	if inb.ChatID != "group:5" {
		t.Errorf("chatID = %q", inb.ChatID)
	}
	if inb.Metadata["peer_kind"] != "group" {
		t.Errorf("peer_kind = %q", inb.Metadata["peer_kind"])
	}
	if inb.Metadata["sender_name"] != "Card" {
		t.Errorf("sender_name = %q", inb.Metadata["sender_name"])
	}
}

func TestOneBot_HandleMessage_GroupNoTrigger(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)

	raw := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "group",
		UserID:      json.RawMessage(`200`),
		GroupID:     json.RawMessage(`5`),
		MessageID:   json.RawMessage(`"g2"`),
		Message:     json.RawMessage(`"just talking"`),
		RawMessage:  `"just talking"`,
	}
	ch.handleRawEvent(raw)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if inb, ok := mb.ConsumeInbound(ctx); ok {
		t.Fatalf("expected no inbound for untriggered group, got %+v", inb)
	}
}

func TestOneBot_HandleMessage_EmptyContent(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)
	raw := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "private",
		UserID:      json.RawMessage(`1`),
		MessageID:   json.RawMessage(`"e1"`),
		Message:     json.RawMessage(`""`),
	}
	ch.handleRawEvent(raw)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if inb, ok := mb.ConsumeInbound(ctx); ok {
		t.Fatalf("expected no inbound, got %+v", inb)
	}
}

func TestOneBot_HandleMessage_InvalidUserID(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)
	raw := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "private",
		UserID:      json.RawMessage(`abc`),
		MessageID:   json.RawMessage(`"x"`),
		Message:     json.RawMessage(`"hi"`),
	}
	ch.handleRawEvent(raw)
}

func TestOneBot_HandleMessage_UnknownMessageType(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)
	raw := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "channel",
		UserID:      json.RawMessage(`1`),
		MessageID:   json.RawMessage(`"x"`),
		Message:     json.RawMessage(`"hi"`),
	}
	ch.handleRawEvent(raw)
}

func TestOneBot_FetchSelfID_NoConnection(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())
	ch.fetchSelfID()
}

func TestOneBot_Send_NotRunning(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())
	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "1", Content: "hi"}); err == nil {
		t.Fatal("expected not-running error")
	}
}

func TestOneBot_Send_NoConnection(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())
	ch.setRunning(true)
	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "1", Content: "hi"}); err == nil {
		t.Fatal("expected no-connection error")
	}
}

func TestOneBot_SendAPIRequest_NoConnection(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())
	if _, err := ch.sendAPIRequest("foo", nil, time.Second); err == nil {
		t.Fatal("expected error when not connected")
	}
}

func TestOneBot_StopWithoutStart(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestOneBot_Start_NoURL(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())
	if err := ch.Start(context.Background()); err == nil {
		t.Fatal("expected error for empty ws url")
	}
}

func TestOneBot_SetMsgEmojiLike_NoConnection(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())
	ch.setMsgEmojiLike("m", 1, true)
	ch.setMsgEmojiLike("m", 1, false)
}

func TestOneBot_HandleMessage_SenderNicknameOnly(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)
	ch.selfID = 300
	// sender with no card, only nickname
	raw := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "group",
		UserID:      json.RawMessage(`300`),
		GroupID:     json.RawMessage(`9`),
		SelfID:      json.RawMessage(`300`),
		MessageID:   json.RawMessage(`"g3"`),
		Message:     json.RawMessage(`"[CQ:at,qq=300]"`),
		RawMessage:  `"[CQ:at,qq=300]"`,
		Sender:      json.RawMessage(`{"user_id":300,"nickname":"NickOnly"}`),
	}
	ch.handleRawEvent(raw)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inb, ok := mb.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no inbound")
	}
	if inb.Metadata["sender_name"] != "NickOnly" {
		t.Errorf("sender_name = %q", inb.Metadata["sender_name"])
	}
}

func TestOneBot_HandleMessage_BotOwnSentHasPeerDirectFallback(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)
	raw := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "private",
		UserID:      json.RawMessage(`5`),
		MessageID:   json.RawMessage(`"m9"`),
		Message:     json.RawMessage(`"hi"`),
		RawMessage:  `"hi"`,
	}
	ch.handleRawEvent(raw)
}

func TestOneBot_HandleMessage_DuplicateID(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)
	raw := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "private",
		UserID:      json.RawMessage(`5`),
		MessageID:   json.RawMessage(`"dup"`),
		Message:     json.RawMessage(`"first"`),
		RawMessage:  `"first"`,
	}
	ch.handleRawEvent(raw)
	// second with same id => ignored
	ch.handleRawEvent(raw)
}