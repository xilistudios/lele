package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// newTestSlack sets up a SlackChannel backed by a mock Slack API server.
// Returns the channel, its message bus, the mock server, and a record of
// received API calls.
func newTestSlack(t *testing.T, allowFrom []string) (*SlackChannel, *bus.MessageBus, *slackMockRecorder) {
	t.Helper()
	rec := &slackMockRecorder{mu: sync.Mutex{}, calls: map[string]int{}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		rec.mu.Lock()
		rec.calls[path]++
		rec.mu.Unlock()

		switch path {
		case "auth.test":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(struct {
				OK     bool   `json:"ok"`
				UserID string `json:"user_id"`
				TeamID string `json:"team_id"`
			}{OK: true, UserID: "U123BOT", TeamID: "T123"})
		case "chat.postMessage":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(slack.SlackResponse{Ok: true})
		case "reactions.add":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(slack.SlackResponse{Ok: true})
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		}
	}))

	cfg := config.SlackConfig{
		BotToken:  "xoxb-test",
		AppToken:  "xapp-test",
		AllowFrom: allowFrom,
	}
	msgBus := bus.NewMessageBus()
	ch, err := NewSlackChannel(cfg, msgBus)
	if err != nil {
		server.Close()
		t.Fatalf("NewSlackChannel: %v", err)
	}
	// Swap in the mock-backed API client.
	ch.api = slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	ch.botUserID = "U123BOT"
	ch.teamID = "T123"
	ch.running = true
	t.Cleanup(server.Close)
	return ch, msgBus, rec
}

type slackMockRecorder struct {
	mu    sync.Mutex
	calls map[string]int
}

func (r *slackMockRecorder) count(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[path]
}

func TestSlackSend(t *testing.T) {
	ch, _, rec := newTestSlack(t, nil)

	ctx := context.Background()
	err := ch.Send(ctx, bus.OutboundMessage{ChatID: "C123", Content: "hello"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rec.count("chat.postMessage") != 1 {
		t.Errorf("expected chat.postMessage call, got %d", rec.count("chat.postMessage"))
	}
}

func TestSlackSend_NotRunning(t *testing.T) {
	ch, _, _ := newTestSlack(t, nil)
	ch.running = false
	ctx := context.Background()
	if err := ch.Send(ctx, bus.OutboundMessage{ChatID: "C123", Content: "hi"}); err == nil {
		t.Error("expected error when not running")
	}
}

func TestSlackSend_InvalidChatID(t *testing.T) {
	ch, _, _ := newTestSlack(t, nil)
	ctx := context.Background()
	if err := ch.Send(ctx, bus.OutboundMessage{ChatID: "", Content: "hi"}); err == nil {
		t.Error("expected error for empty chat ID")
	}
}

func TestSlackSend_AddsReactionOnPendingAck(t *testing.T) {
	ch, _, rec := newTestSlack(t, nil)
	// Pre-populate a pending ack for the chat.
	ch.pendingAcks.Store("C123", slackMessageRef{ChannelID: "C123", Timestamp: "123.456"})

	ctx := context.Background()
	if err := ch.Send(ctx, bus.OutboundMessage{ChatID: "C123", Content: "done"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rec.count("reactions.add") != 1 {
		t.Errorf("expected reactions.add call, got %d", rec.count("reactions.add"))
	}
}

func TestSlackSend_WithThread(t *testing.T) {
	ch, _, rec := newTestSlack(t, nil)
	ctx := context.Background()
	if err := ch.Send(ctx, bus.OutboundMessage{ChatID: "C123/123.456", Content: "thread reply"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rec.count("chat.postMessage") != 1 {
		t.Errorf("expected chat.postMessage call, got %d", rec.count("chat.postMessage"))
	}
}

func newSlackMessageEvent(userID, channelID, text string) *slackevents.MessageEvent {
	return &slackevents.MessageEvent{
		User:      userID,
		Channel:   channelID,
		Text:      text,
		TimeStamp: "1000.000",
	}
}

func TestSlackHandleMessageEvent(t *testing.T) {
	ctx := context.Background()

	t.Run("direct channel message", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := newSlackMessageEvent("UUSER", "D1", "hello there")
		ch.handleMessageEvent(ev)
		inbound, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if inbound.Content != "hello there" {
			t.Errorf("content = %q", inbound.Content)
		}
		if inbound.Metadata["peer_kind"] != "direct" {
			t.Errorf("peer_kind = %q", inbound.Metadata["peer_kind"])
		}
	})

	t.Run("own bot message ignored", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := newSlackMessageEvent("U123BOT", "C1", "hi")
		ch.handleMessageEvent(ev)
		consumeCtx, cancel := context.WithTimeout(context.Background(), 50)
		defer cancel()
		if msg, ok := msgBus.ConsumeInbound(consumeCtx); ok {
			t.Fatalf("bot message should be ignored, got %+v", msg)
		}
	})

	t.Run("bot_id message ignored", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := newSlackMessageEvent("U1", "C1", "hi")
		ev.BotID = "B123"
		ch.handleMessageEvent(ev)
		consumeCtx, cancel := context.WithTimeout(context.Background(), 50)
		defer cancel()
		if msg, ok := msgBus.ConsumeInbound(consumeCtx); ok {
			t.Fatalf("bot_id message should be ignored, got %+v", msg)
		}
	})

	t.Run("empty user ignored", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := newSlackMessageEvent("", "C1", "hi")
		ch.handleMessageEvent(ev)
		consumeCtx, cancel := context.WithTimeout(context.Background(), 50)
		defer cancel()
		if msg, ok := msgBus.ConsumeInbound(consumeCtx); ok {
			t.Fatalf("empty-user message should be ignored, got %+v", msg)
		}
	})

	t.Run("subtype ignored", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := newSlackMessageEvent("U1", "C1", "joined")
		ev.SubType = "channel_join"
		ch.handleMessageEvent(ev)
		consumeCtx, cancel := context.WithTimeout(context.Background(), 50)
		defer cancel()
		if msg, ok := msgBus.ConsumeInbound(consumeCtx); ok {
			t.Fatalf("subtype message should be ignored, got %+v", msg)
		}
	})

	t.Run("file_share subtype is processed", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := newSlackMessageEvent("U1", "C1", "here is a file")
		ev.SubType = "file_share"
		ch.handleMessageEvent(ev)
		inbound, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("file_share message should be processed")
		}
		if inbound.Content != "here is a file" {
			t.Errorf("content = %q", inbound.Content)
		}
	})

	t.Run("rejected by allowlist", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, []string{"UALLOWED"})
		ev := newSlackMessageEvent("UOTHER", "C1", "hi")
		ch.handleMessageEvent(ev)
		consumeCtx, cancel := context.WithTimeout(context.Background(), 50)
		defer cancel()
		if msg, ok := msgBus.ConsumeInbound(consumeCtx); ok {
			t.Fatalf("blocked user message should be ignored, got %+v", msg)
		}
	})

	t.Run("channel message with thread", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := newSlackMessageEvent("UUSER", "C123", "threaded msg")
		ev.ThreadTimeStamp = "1000.001"
		ch.handleMessageEvent(ev)
		inbound, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if inbound.ChatID != "C123/1000.001" {
			t.Errorf("chatID = %q", inbound.ChatID)
		}
		if inbound.Metadata["peer_kind"] != "channel" {
			t.Errorf("peer_kind = %q", inbound.Metadata["peer_kind"])
		}
	})

	t.Run("empty content ignored", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := newSlackMessageEvent("U1", "C1", "<@U123BOT>   ")
		ch.handleMessageEvent(ev)
		consumeCtx, cancel := context.WithTimeout(context.Background(), 50)
		defer cancel()
		if msg, ok := msgBus.ConsumeInbound(consumeCtx); ok {
			t.Fatalf("empty content should be ignored, got %+v", msg)
		}
	})
}

func TestSlackHandleAppMention(t *testing.T) {
	ctx := context.Background()

	t.Run("mention in channel", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := &slackevents.AppMentionEvent{
			User:      "UUSER",
			Channel:   "C123",
			Text:      "<@U123BOT> please help",
			TimeStamp: "1000.000",
			Type:      "app_mention",
		}
		ch.handleAppMention(ev)
		inbound, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if inbound.Content != "please help" {
			t.Errorf("content = %q", inbound.Content)
		}
		if inbound.Metadata["is_mention"] != "true" {
			t.Errorf("is_mention = %q", inbound.Metadata["is_mention"])
		}
	})

	t.Run("own bot mention ignored", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := &slackevents.AppMentionEvent{User: "U123BOT", Channel: "C1", Text: "hi", TimeStamp: "1"}
		ch.handleAppMention(ev)
		consumeCtx, cancel := context.WithTimeout(context.Background(), 50)
		defer cancel()
		if msg, ok := msgBus.ConsumeInbound(consumeCtx); ok {
			t.Fatalf("own mention should be ignored, got %+v", msg)
		}
	})

	t.Run("mention with thread", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := &slackevents.AppMentionEvent{
			User:            "UUSER",
			Channel:         "C123",
			Text:            "hi",
			ThreadTimeStamp: "1000.999",
			TimeStamp:       "1000.100",
			Type:            "app_mention",
		}
		ch.handleAppMention(ev)
		inbound, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if inbound.ChatID != "C123/1000.999" {
			t.Errorf("chatID = %q", inbound.ChatID)
		}
	})

	t.Run("mention with empty content ignored", func(t *testing.T) {
		ch, msgBus, _ := newTestSlack(t, nil)
		ev := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@U123BOT>", TimeStamp: "1"}
		ch.handleAppMention(ev)
		consumeCtx, cancel := context.WithTimeout(context.Background(), 50)
		defer cancel()
		if msg, ok := msgBus.ConsumeInbound(consumeCtx); ok {
			t.Fatalf("empty mention should be ignored, got %+v", msg)
		}
	})
}

func TestSlackHandleSlashCommand(t *testing.T) {
	ch, msgBus, _ := newTestSlack(t, nil)
	ctx := context.Background()

	cmd := slack.SlashCommand{
		UserID:    "UUSER",
		ChannelID: "C123",
		Text:      "what is the weather",
		Command:   "/ask",
		TriggerID: "trig-1",
	}
	ch.handleSlashCommand(socketmode.Event{Type: socketmode.EventTypeSlashCommand, Data: cmd})
	inbound, ok := msgBus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no message published")
	}
	if inbound.Content != "what is the weather" {
		t.Errorf("content = %q", inbound.Content)
	}
	if inbound.Metadata["is_command"] != "true" {
		t.Errorf("is_command = %q", inbound.Metadata["is_command"])
	}
	if inbound.Metadata["trigger_id"] != "trig-1" {
		t.Errorf("trigger_id = %q", inbound.Metadata["trigger_id"])
	}
}

func TestSlackHandleSlashCommand_EmptyTextDefaultsHelp(t *testing.T) {
	ch, msgBus, _ := newTestSlack(t, nil)
	ctx := context.Background()
	cmd := slack.SlashCommand{UserID: "U1", ChannelID: "C1", Text: "  ", Command: "/ask"}
	ch.handleSlashCommand(socketmode.Event{Data: cmd})
	inbound, ok := msgBus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no message published")
	}
	if inbound.Content != "help" {
		t.Errorf("content = %q, want help", inbound.Content)
	}
}

func TestSlackHandleEventsAPI(t *testing.T) {
	ch, _, _ := newTestSlack(t, nil)

	t.Run("non-eventsAPI data ignored", func(t *testing.T) {
		ev := socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: "not-an-event"}
		ch.handleEventsAPI(ev) // should not panic
	})

	t.Run("message event dispatched", func(t *testing.T) {
		inner := slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "message",
				Data: newSlackMessageEvent("U1", "C1", "hello"),
			},
		}
		ev := socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: inner}
		ch.handleEventsAPI(ev)
	})

	t.Run("app mention event dispatched", func(t *testing.T) {
		inner := slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "app_mention",
				Data: &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "hi", TimeStamp: "1"},
			},
		}
		ev := socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: inner}
		ch.handleEventsAPI(ev)
	})
}

func TestSlackEventLoop_ClosedChannel(t *testing.T) {
	ch, _, _ := newTestSlack(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch.ctx = ctx
	ch.cancel = cancel

	fakeEvents := make(chan socketmode.Event)
	close(fakeEvents)
	if ch.socketClient != nil {
		ch.socketClient.Events = fakeEvents
	}
	ch.eventLoop() // should return once Events closes
}

func TestSlackDownloadFile_NoURL(t *testing.T) {
	ch, _, _ := newTestSlack(t, nil)
	path := ch.downloadSlackFile(slack.File{ID: "F1"})
	if path != "" {
		t.Errorf("expected empty path for file without URL, got %q", path)
	}
}

func TestSlackStop(t *testing.T) {
	ch, _, _ := newTestSlack(t, nil)
	ctx := context.Background()
	// cancel is nil so Stop just clears running.
	if err := ch.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ch.running {
		t.Error("channel should not be running after Stop")
	}
}
