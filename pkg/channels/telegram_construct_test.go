package channels

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mymmrac/telego"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/voice"
)

func TestNewTelegramChannel_Success(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Channels.Telegram.Token = testTelegramBotToken

	ch, err := NewTelegramChannel(cfg, bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewTelegramChannel: %v", err)
	}
	if ch.Name() != "telegram" {
		t.Errorf("Name = %q", ch.Name())
	}
	if ch.commands == nil {
		t.Error("commands not wired")
	}
	if ch.offsetFilePath == "" {
		t.Error("offsetFilePath not set")
	}
	// SetTranscriber coverage
	ch.SetTranscriber(nil) // nil is safe; SetTranscriber just assigns
	if ch.transcriber != nil {
		t.Error("transcriber should be nil")
	}
	ch.SetTranscriber((*voice.GroqTranscriber)(nil))
}

func TestNewTelegramChannel_InvalidToken(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Channels.Telegram.Token = "invalid-token"
	_, err := NewTelegramChannel(cfg, bus.NewMessageBus(), nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestNewTelegramChannel_InvalidProxy(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Channels.Telegram.Token = testTelegramBotToken
	cfg.Channels.Telegram.Proxy = "://bad-proxy"
	_, err := NewTelegramChannel(cfg, bus.NewMessageBus(), nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid proxy")
	}
}

func TestNewTelegramChannel_ValidProxy(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Channels.Telegram.Token = testTelegramBotToken
	cfg.Channels.Telegram.Proxy = "http://localhost:8080"
	if _, err := NewTelegramChannel(cfg, bus.NewMessageBus(), nil, nil); err != nil {
		t.Fatalf("valid proxy should succeed: %v", err)
	}
}

func TestNewTelegramChannel_LoadsOffsetFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "telegram_offset.txt"), []byte("31\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Channels.Telegram.Token = testTelegramBotToken
	ch, err := NewTelegramChannel(cfg, bus.NewMessageBus(), nil, nil)
	if err != nil {
		t.Fatalf("NewTelegramChannel: %v", err)
	}
	if ch.lastUpdateID != 31 {
		t.Fatalf("lastUpdateID = %d want 31", ch.lastUpdateID)
	}
}

func TestTelegramSend_IntermediateMessage(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	ch.setRunning(true)
	// IsIntermediate sends to placeholder / normal path but does not stop thinking.
	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:         "123",
		Content:        "thinking...",
		IsIntermediate: true,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !m.hadMethod("sendMessage") {
		t.Fatal("expected sendMessage")
	}
}

func TestTelegramSend_PlaceholderEditPath(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	ch.setRunning(true)
	ch.placeholders.Store("123", 777)
	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "123", Content: "final answer"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !m.hadMethod("editMessageText") {
		t.Fatal("expected editMessageText to replace placeholder")
	}
}

func TestTelegramSend_WithReplyTo(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	ch.setRunning(true)
	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "123",
		Content: "reply",
		ReplyTo: "55",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !m.hadMethod("sendMessage") {
		t.Fatal("expected sendMessage")
	}
}

func TestTelegramSend_InReplyMarkupInline(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	ch.setRunning(true)
	markup := &telego.InlineKeyboardMarkup{InlineKeyboard: [][]telego.InlineKeyboardButton{{{Text: "btn", CallbackData: "cb"}}}}
	msg := bus.OutboundMessage{ChatID: "9", Content: "hi", ReplyMarkup: markup}
	if err := ch.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestTelegramResolvePlaceholderWithText_NoPlaceholder(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	if ch.resolvePlaceholderWithText(context.Background(), 1, "chat", "text") {
		t.Fatal("expected false when no placeholder")
	}
}

func TestTelegramStopActiveThinking_NonCancelType(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	// Store an entry that is not a *thinkingCancel
	ch.stopThinking.Store("1:k", 42)
	ch.stopActiveThinking("1:k") // should not panic
}

func TestTelegramStopAllThinking_CoverAllChats(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	cf := &thinkingCancel{fn: func() {}, doneChan: nil}
	// Different chats
	ch.stopThinking.Store("1:a", cf)
	ch.stopThinking.Store("2:b", cf)
	// A key not matching any chat prefix
	ch.stopThinking.Store("xyz", cf)
	ch.stopAllThinkingForChat("1")
	if _, ok := ch.stopThinking.Load("1:a"); ok {
		t.Fatal("1:a should be removed")
	}
	if _, ok := ch.stopThinking.Load("2:b"); !ok {
		t.Fatal("2:b should remain")
	}
	if _, ok := ch.stopThinking.Load("xyz"); !ok {
		t.Fatal("xyz should remain")
	}
}

func TestTelegramSend_AttachmentWithText(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	ch.setRunning(true)
	// Attachment with a missing file -> sendTextMessage succeeds, then
	// sendDocument fails (file missing). Expect an error.
	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "123",
		Content: "text with file",
		Attachments: []bus.FileAttachment{
			{Name: "x.txt", Path: "/missing/x.txt"},
		},
	})
	if err == nil {
		t.Fatal("expected sendDocument error for missing file")
	}
}// ---------------------------------------------------------------------------
// telegram_transport.go sendPlainTextFallback direct coverage
// ---------------------------------------------------------------------------

func TestTelegramSendPlainTextFallback(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()

	msg := bus.OutboundMessage{ChatID: "123", Content: "plain fallback text"}
	err := ch.sendPlainTextFallback(context.Background(), 123, msg, msg.Content, true)
	if err != nil {
		t.Fatalf("sendPlainTextFallback: %v", err)
	}
	if !m.hadMethod("sendMessage") {
		t.Fatal("expected sendMessage")
	}

	// With a placeholder that will be edited (then deleted on edit failure is
	// the fallback; here edit succeeds so it returns early).
	ch.placeholders.Store("123", 777)
	err = ch.sendPlainTextFallback(context.Background(), 123, msg, msg.Content, false)
	if err != nil {
		t.Fatalf("sendPlainTextFallback placeholder: %v", err)
	}
	if !m.hadMethod("editMessageText") {
		t.Fatal("expected editMessageText")
	}

	// With ReplyTo and ReplyMarkup
	markup := &telego.InlineKeyboardMarkup{InlineKeyboard: [][]telego.InlineKeyboardButton{{}}}
	msg2 := bus.OutboundMessage{ChatID: "123", Content: "x", ReplyTo: "5", ReplyMarkup: markup}
	if err := ch.sendPlainTextFallback(context.Background(), 123, msg2, "x", true); err != nil {
		t.Fatalf("sendPlainTextFallback with reply: %v", err)
	}
}