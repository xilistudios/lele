package channels

import (
	"context"
	"testing"
	"time"

	"github.com/tencent-connect/botgo/dto"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func newTestQQChannel() *QQChannel {
	ch, _ := NewQQChannel(config.QQConfig{}, bus.NewMessageBus())
	return ch
}

func TestQQ_IsDuplicate(t *testing.T) {
	c := newTestQQChannel()
	if c.isDuplicate("m1") {
		t.Error("first occurrence should not be duplicate")
	}
	if !c.isDuplicate("m1") {
		t.Error("second occurrence should be duplicate")
	}
	if c.isDuplicate("") {
		t.Error("empty id should not be duplicate")
	}
	if c.isDuplicate("m2") {
		t.Error("new different id should be unique")
	}
}

func TestQQ_HandleC2C(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewQQChannel(config.QQConfig{}, mb)
	handler := ch.handleC2CMessage()

	ev := &dto.WSPayload{}
	data := &dto.WSC2CMessageData{}
	data.ID = "cc1"
	data.Content = "hello qq"
	data.Author = &dto.User{ID: "user1"}

	if err := handler(ev, data); err != nil {
		t.Fatalf("handler: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inb, ok := mb.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no inbound published")
	}
	if inb.ChatID != "user1" {
		t.Errorf("chatID = %q", inb.ChatID)
	}
	if inb.Content != "hello qq" {
		t.Errorf("content = %q", inb.Content)
	}
	if inb.Metadata["peer_kind"] != "direct" {
		t.Errorf("peer_kind = %q", inb.Metadata["peer_kind"])
	}
}

func TestQQ_HandleC2C_Duplicate(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewQQChannel(config.QQConfig{}, mb)
	handler := ch.handleC2CMessage()

	ev := &dto.WSPayload{}
	data := &dto.WSC2CMessageData{ID: "dup1", Content: "hi", Author: &dto.User{ID: "u"}}

	if err := handler(ev, data); err != nil {
		t.Fatal(err)
	}
	if err := handler(ev, data); err != nil {
		t.Fatal(err)
	}
	// No inbound should be published for the duplicates (only 1 total consumed).
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, ok := mb.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("expected first inbound")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	if inb, ok := mb.ConsumeInbound(ctx2); ok {
		t.Fatalf("unexpected duplicate inbound: %+v", inb)
	}
}

func TestQQ_HandleC2C_NoSender(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewQQChannel(config.QQConfig{}, mb)
	handler := ch.handleC2CMessage()
	if err := handler(&dto.WSPayload{}, &dto.WSC2CMessageData{ID: "x", Content: "hi"}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if inb, ok := mb.ConsumeInbound(ctx); ok {
		t.Fatalf("unexpected inbound without sender: %+v", inb)
	}
}

func TestQQ_HandleC2C_EmptyContent(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewQQChannel(config.QQConfig{}, mb)
	handler := ch.handleC2CMessage()
	if err := handler(&dto.WSPayload{}, &dto.WSC2CMessageData{
		ID: "y", Author: &dto.User{ID: "u"},
	}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if inb, ok := mb.ConsumeInbound(ctx); ok {
		t.Fatalf("unexpected inbound with empty content: %+v", inb)
	}
}

func TestQQ_HandleGroupAT(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewQQChannel(config.QQConfig{}, mb)
	handler := ch.handleGroupATMessage()

	data := &dto.WSGroupATMessageData{}
	data.ID = "g1"
	data.GroupID = "grp9"
	data.Content = "hello group"
	data.Author = &dto.User{ID: "u2"}

	if err := handler(&dto.WSPayload{}, data); err != nil {
		t.Fatalf("handler: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inb, ok := mb.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no inbound published")
	}
	if inb.ChatID != "grp9" {
		t.Errorf("chatID = %q", inb.ChatID)
	}
	if inb.Metadata["peer_kind"] != "group" {
		t.Errorf("peer_kind = %q", inb.Metadata["peer_kind"])
	}
	if inb.Metadata["group_id"] != "grp9" {
		t.Errorf("group_id = %q", inb.Metadata["group_id"])
	}
}

func TestQQ_HandleGroupAT_DuplicateAndNoSender(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewQQChannel(config.QQConfig{}, mb)
	handler := ch.handleGroupATMessage()
	data := &dto.WSGroupATMessageData{ID: "g2", Content: "hi", Author: &dto.User{ID: "u"}}
	if err := handler(&dto.WSPayload{}, data); err != nil {
		t.Fatal(err)
	}
	// no sender -> message dropped, no inbound added
	if err := handler(&dto.WSPayload{}, &dto.WSGroupATMessageData{ID: "g3", Content: "hey"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, ok := mb.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("expected first inbound")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	if inb, ok := mb.ConsumeInbound(ctx2); ok {
		t.Fatalf("unexpected extra inbound: %+v", inb)
	}
}

func TestQQ_NewChannel_Config_StartRequiresCreds(t *testing.T) {
	c := newTestQQChannel()
	err := c.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for empty app_id/app_secret")
	}
	if c.Name() != "qq" {
		t.Errorf("name = %q", c.Name())
	}
}

func TestQQ_Send_NotRunning(t *testing.T) {
	c := newTestQQChannel()
	if err := c.Send(context.Background(), bus.OutboundMessage{ChatID: "5", Content: "hi"}); err == nil {
		t.Fatal("expected not-running error")
	}
	_ = c.Stop(context.Background())
}