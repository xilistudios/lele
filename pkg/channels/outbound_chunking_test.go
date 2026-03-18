package channels

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

type recordingChannel struct {
	messages []bus.OutboundMessage
	failAt   int
}

func (c *recordingChannel) Name() string {
	return "recording"
}

func (c *recordingChannel) Start(ctx context.Context) error {
	return nil
}

func (c *recordingChannel) Stop(ctx context.Context) error {
	return nil
}

func (c *recordingChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if c.failAt > 0 && len(c.messages)+1 == c.failAt {
		return errors.New("forced send failure")
	}
	c.messages = append(c.messages, msg)
	return nil
}

func (c *recordingChannel) IsRunning() bool {
	return true
}

func (c *recordingChannel) IsAllowed(senderID string) bool {
	return true
}

func TestSplitOutboundMessage_ShortMessageIsUnchanged(t *testing.T) {
	markup := &struct{ label string }{label: "approval"}
	msg := bus.OutboundMessage{
		Channel:     "telegram",
		ChatID:      "123",
		Content:     "short reply",
		ReplyTo:     "77",
		ReplyMarkup: markup,
		Attachments: []bus.FileAttachment{{Name: "report.txt", Path: "/tmp/report.txt"}},
	}

	got := splitOutboundMessage(msg)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if !reflect.DeepEqual(got[0], msg) {
		t.Fatalf("expected unchanged outbound message, got %#v", got[0])
	}
}

func TestSplitOutboundMessage_TelegramLongMessagePreservesOnlyFirstReplyContext(t *testing.T) {
	markup := &struct{ label string }{label: "approval"}
	content := strings.Repeat("a", telegramTextChunkMaxLen+350)
	msg := bus.OutboundMessage{
		Channel:     "telegram",
		ChatID:      "123",
		Content:     content,
		ReplyTo:     "77",
		ReplyMarkup: markup,
	}

	got := splitOutboundMessage(msg)
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(got))
	}
	if got[0].ReplyTo != msg.ReplyTo {
		t.Fatalf("first chunk replyTo = %q, want %q", got[0].ReplyTo, msg.ReplyTo)
	}
	if got[0].ReplyMarkup != markup {
		t.Fatal("first chunk should preserve reply markup")
	}
	if got[1].ReplyTo != "" {
		t.Fatalf("second chunk replyTo = %q, want empty", got[1].ReplyTo)
	}
	if got[1].ReplyMarkup != nil {
		t.Fatal("second chunk should clear reply markup")
	}
	if strings.Join([]string{got[0].Content, got[1].Content}, "") != content {
		t.Fatal("chunk content did not reconstruct the original message")
	}
	for index, chunk := range got {
		if len(chunk.Content) > telegramTextChunkMaxLen {
			t.Fatalf("chunk %d exceeded telegram max len: %d", index, len(chunk.Content))
		}
		if len(chunk.Attachments) != 0 {
			t.Fatalf("chunk %d unexpectedly retained attachments", index)
		}
	}
}

func TestSplitOutboundMessage_WhatsAppLongMessageMovesAttachmentsToFinalSend(t *testing.T) {
	attachment := bus.FileAttachment{Name: "report.txt", Path: "/tmp/report.txt"}
	content := strings.Repeat("b", whatsappTextChunkMaxLen+275)
	msg := bus.OutboundMessage{
		Channel:     "whatsapp",
		ChatID:      "5511999999999",
		Content:     content,
		ReplyTo:     "external-id",
		ReplyMarkup: &struct{ label string }{label: "ignored"},
		Attachments: []bus.FileAttachment{attachment},
	}

	got := splitOutboundMessage(msg)
	if len(got) != 3 {
		t.Fatalf("expected 3 outbound sends, got %d", len(got))
	}
	if got[0].Content == "" || got[1].Content == "" {
		t.Fatal("text chunks should keep content before attachment send")
	}
	if len(got[0].Attachments) != 0 || len(got[1].Attachments) != 0 {
		t.Fatal("text chunks should not include attachments")
	}
	if got[2].Content != "" {
		t.Fatalf("attachment send content = %q, want empty", got[2].Content)
	}
	if got[2].ReplyTo != msg.ReplyTo {
		t.Fatalf("attachment send replyTo = %q, want %q", got[2].ReplyTo, msg.ReplyTo)
	}
	if got[2].ReplyMarkup != nil {
		t.Fatal("attachment send should not carry reply markup")
	}
	if !reflect.DeepEqual(got[2].Attachments, msg.Attachments) {
		t.Fatalf("attachment send attachments = %#v, want %#v", got[2].Attachments, msg.Attachments)
	}
	if strings.Join([]string{got[0].Content, got[1].Content}, "") != content {
		t.Fatal("text chunks did not reconstruct the original whatsapp message")
	}
}

func TestSendOutboundMessage_SendsExpandedChunksInOrder(t *testing.T) {
	channel := &recordingChannel{}
	msg := bus.OutboundMessage{
		Channel:     "telegram",
		ChatID:      "123",
		Content:     strings.Repeat("c", telegramTextChunkMaxLen+250),
		ReplyTo:     "77",
		Attachments: []bus.FileAttachment{{Name: "report.txt", Path: "/tmp/report.txt"}},
	}

	if err := sendOutboundMessage(context.Background(), channel, msg); err != nil {
		t.Fatalf("sendOutboundMessage returned error: %v", err)
	}
	if len(channel.messages) != 3 {
		t.Fatalf("expected 3 sends, got %d", len(channel.messages))
	}
	if channel.messages[0].ReplyTo != "77" {
		t.Fatalf("first send replyTo = %q, want %q", channel.messages[0].ReplyTo, "77")
	}
	if channel.messages[1].ReplyTo != "" {
		t.Fatalf("second send replyTo = %q, want empty", channel.messages[1].ReplyTo)
	}
	if len(channel.messages[2].Attachments) != 1 {
		t.Fatalf("attachment send should include 1 attachment, got %d", len(channel.messages[2].Attachments))
	}
}

func TestSendOutboundMessage_StopsOnChunkError(t *testing.T) {
	channel := &recordingChannel{failAt: 2}
	msg := bus.OutboundMessage{
		Channel: "telegram",
		ChatID:  "123",
		Content: strings.Repeat("d", telegramTextChunkMaxLen+250),
	}

	err := sendOutboundMessage(context.Background(), channel, msg)
	if err == nil {
		t.Fatal("expected an error when a chunk send fails")
	}
	if len(channel.messages) != 1 {
		t.Fatalf("expected to stop after the first successful send, got %d sends", len(channel.messages))
	}
}
