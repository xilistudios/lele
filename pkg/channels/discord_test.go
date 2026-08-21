package channels

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func newTestDiscord(t *testing.T, allowFrom []string) *DiscordChannel {
	t.Helper()
	cfg := config.DiscordConfig{
		Token:     "test-token",
		AllowFrom: allowFrom,
	}
	ch, err := NewDiscordChannel(cfg, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewDiscordChannel: %v", err)
	}
	// Construct a session whose state has the bot user populated.
	sess, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	sess.State = discordgo.NewState()
	sess.State.User = &discordgo.User{ID: "BOTID", Username: "bot"}
	ch.session = sess
	return ch
}

func TestDiscordNewChannel(t *testing.T) {
	cfg := config.DiscordConfig{Token: "abc", AllowFrom: []string{}}
	ch, err := NewDiscordChannel(cfg, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewDiscordChannel: %v", err)
	}
	if ch.Name() != "discord" {
		t.Errorf("Name() = %q", ch.Name())
	}
	if ch.transcriber != nil {
		t.Error("transcriber should be nil initially")
	}
}

func TestDiscordGetContext(t *testing.T) {
	ch := newTestDiscord(t, nil)
	ch.ctx = nil
	ctx := ch.getContext()
	if ctx == nil {
		t.Error("getContext should return non-nil background context")
	}

	ch.ctx = context.Background()
	if ch.getContext() == nil {
		t.Error("getContext should return the existing context")
	}
}

func TestAppendContent(t *testing.T) {
	if got := appendContent("", "suffix"); got != "suffix" {
		t.Errorf("appendContent(empty, suffix) = %q", got)
	}
	if got := appendContent("base", "suffix"); got != "base\nsuffix" {
		t.Errorf("appendContent(base, suffix) = %q", got)
	}
}

func TestDiscordStopTyping(t *testing.T) {
	ch := newTestDiscord(t, nil)
	ch.typingStop["c1"] = make(chan struct{})
	ch.stopTyping("c1")
	if _, ok := ch.typingStop["c1"]; ok {
		t.Error("typingStop entry should be removed")
	}
	// Non-existent chat is a no-op.
	ch.stopTyping("nope")
}

func TestDiscordHandleMessage_Basic(t *testing.T) {
	ctx := context.Background()
	ch := newTestDiscord(t, nil)

	t.Run("direct message", func(t *testing.T) {
		m := &discordgo.MessageCreate{Message: &discordgo.Message{
			ID:        "m1",
			ChannelID: "D1",
			GuildID:   "",
			Content:   "hello there",
			Author:    &discordgo.User{ID: "U1", Username: "alice"},
		}}
		ch.handleMessage(ch.session, m)
		inbound, ok := ch.bus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if inbound.Content != "hello there" {
			t.Errorf("content = %q", inbound.Content)
		}
		if inbound.Metadata["peer_kind"] != "direct" {
			t.Errorf("peer_kind = %q", inbound.Metadata["peer_kind"])
		}
		if inbound.Metadata["is_dm"] != "true" {
			t.Errorf("is_dm = %q", inbound.Metadata["is_dm"])
		}
	})

	t.Run("channel message", func(t *testing.T) {
		m := &discordgo.MessageCreate{Message: &discordgo.Message{
			ID:        "m2",
			ChannelID: "C123",
			GuildID:   "G1",
			Content:   "hello channel",
			Author:    &discordgo.User{ID: "U2", Username: "bob"},
		}}
		ch.handleMessage(ch.session, m)
		inbound, ok := ch.bus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if inbound.Content != "hello channel" {
			t.Errorf("content = %q", inbound.Content)
		}
		if inbound.Metadata["peer_kind"] != "channel" {
			t.Errorf("peer_kind = %q", inbound.Metadata["peer_kind"])
		}
		if inbound.Metadata["is_dm"] != "false" {
			t.Errorf("is_dm = %q", inbound.Metadata["is_dm"])
		}
	})

	t.Run("own message ignored", func(t *testing.T) {
		m := &discordgo.MessageCreate{Message: &discordgo.Message{
			ID:      "m3",
			Content: "bot speaking",
			Author:  &discordgo.User{ID: "BOTID", Username: "bot"},
		}}
		ch.handleMessage(ch.session, m)
		if !tryConsumeEmpty(ch.bus) {
			t.Error("own message should be ignored")
		}
	})

	t.Run("nil message ignored", func(t *testing.T) {
		ch.handleMessage(ch.session, nil)
		if !tryConsumeEmpty(ch.bus) {
			t.Error("nil message should be ignored")
		}
	})

	t.Run("blocked user ignored", func(t *testing.T) {
		blocked := newTestDiscord(t, []string{"UALLOWED"})
		m := &discordgo.MessageCreate{Message: &discordgo.Message{
			ID:      "m4",
			Content: "hi",
			Author:  &discordgo.User{ID: "UOTHER", Username: "other"},
		}}
		blocked.handleMessage(blocked.session, m)
		if !tryConsumeEmpty(blocked.bus) {
			t.Error("blocked user message should be ignored")
		}
	})
}

func TestDiscordHandleMessage_WithDiscriminator(t *testing.T) {
	ctx := context.Background()
	ch := newTestDiscord(t, nil)
	m := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "m1",
		ChannelID: "C1",
		Content:   "hi",
		Author:    &discordgo.User{ID: "U1", Username: "alice", Discriminator: "1234"},
	}}
	ch.handleMessage(ch.session, m)
	inbound, ok := ch.bus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no message published")
	}
	if inbound.Metadata["display_name"] != "alice#1234" {
		t.Errorf("display_name = %q", inbound.Metadata["display_name"])
	}
}

func TestDiscordSend_NotRunning(t *testing.T) {
	ch := newTestDiscord(t, nil)
	ch.running = false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := ch.Send(ctx, bus.OutboundMessage{ChatID: "C1", Content: "hi"})
	if err == nil {
		t.Fatal("expected error when not running")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error = %v", err)
	}
}

func TestDiscordSend_EmptyChannel(t *testing.T) {
	ch := newTestDiscord(t, nil)
	ch.running = true
	ctx := context.Background()
	err := ch.Send(ctx, bus.OutboundMessage{ChatID: "", Content: "hi"})
	if err == nil {
		t.Fatal("expected error for empty channel ID")
	}
}

func TestDiscordSend_EmptyContent(t *testing.T) {
	ch := newTestDiscord(t, nil)
	ch.running = true
	ctx := context.Background()
	if err := ch.Send(ctx, bus.OutboundMessage{ChatID: "C1", Content: ""}); err != nil {
		t.Fatalf("Send with empty content should be no-op, got %v", err)
	}
}

// tryConsumeEmpty returns true if the bus has no pending inbound messages.
func tryConsumeEmpty(mb *bus.MessageBus) bool {
	cctx, cancel := context.WithTimeout(context.Background(), 50)
	defer cancel()
	_, ok := mb.ConsumeInbound(cctx)
	return !ok
}