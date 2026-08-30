package channels

import (
	"bytes"
	"context"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestTelegramMenuCommands(t *testing.T) {
	commands := telegramMenuCommands(telegramCommandRegistry)
	got := make([]string, 0, len(commands))
	for _, command := range commands {
		got = append(got, command.Command)
		if command.Description == "" {
			t.Fatalf("command %s should have a description", command.Command)
		}
	}

	want := []string{"models", "new", "clear", "stop", "model", "status", "compact", "subagents", "toggle", "verbose", "think", "agent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected command menu order: got %v want %v", got, want)
	}
}

// recordingRoundTripper captures requests instead of sending them, so the
// hook tests can assert deleteMessage behavior without a real bot/network.
type recordingRoundTripper struct {
	mu    sync.Mutex
	count int
}

func (r *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

func (r *recordingRoundTripper) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// newTelegramHookTestChannel builds a minimal TelegramChannel (no bot, no
// polling) wired exactly like NewTelegramChannel for testing the inbound-drop
// rollback hook.
func newTelegramHookTestChannel(t *testing.T) (*TelegramChannel, *recordingRoundTripper) {
	t.Helper()

	cfg := &config.Config{}
	cfg.Channels.Telegram.Token = "TESTTOKEN"
	rt := &recordingRoundTripper{}
	return &TelegramChannel{
		BaseChannel: NewBaseChannel("telegram", cfg.Channels.Telegram, nil, nil),
		config:      cfg,
		deleteHTTP:  &http.Client{Transport: rt},
	}, rt
}

// TestTelegramInboundDroppedHookWiredByConstructor asserts the invariant that
// NewTelegramChannel installs the rollback hook: a channel built by the real
// constructor must have InboundDroppedHook set, so a dropped inbound message
// can never leave a stuck typing indicator behind.
func TestTelegramInboundDroppedHookWiredByConstructor(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	cfg := &config.Config{}
	// telego.NewBot validates the token format offline (no network call).
	cfg.Channels.Telegram.Token = "123456:" + string(bytes.Repeat([]byte("a"), 35))

	ch, err := NewTelegramChannel(cfg, bus.NewMessageBus(), nil, nil)
	if err != nil {
		t.Fatalf("NewTelegramChannel failed: %v", err)
	}
	if ch.InboundDroppedHook == nil {
		t.Fatal("NewTelegramChannel must wire BaseChannel.InboundDroppedHook")
	}
}

// TestTelegramInboundDroppedHookCancelsTyping verifies the rollback hook that
// NewTelegramChannel installs on BaseChannel: when the bus rejects an inbound
// message, the thinking indicator for the exact chatID:messageID key is
// cancelled and removed from stopThinking, and the pending "Thinking... 💭"
// placeholder is deleted and removed from placeholders.
func TestTelegramInboundDroppedHookCancelsTyping(t *testing.T) {
	ch, rt := newTelegramHookTestChannel(t)

	// Wire the hook exactly as the constructor does.
	ch.InboundDroppedHook = ch.handleInboundDropped

	const (
		chatIDStr = "12345"
		messageID = "678"
	)
	thinkingKey := chatIDStr + ":" + messageID // mirrors fmt.Sprintf("%d:%d", chatID, msgID)

	// Thinking indicator: cancel func + closed doneChan (Cancel() waits on
	// doneChan with a timeout, so a closed channel keeps it fast).
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	ch.stopThinking.Store(thinkingKey, &thinkingCancel{fn: cancel, doneChan: done})

	// Sibling entry for another message in the same chat: only the
	// stopAllThinkingForChat safety net should remove it.
	sibCtx, sibCancel := context.WithCancel(context.Background())
	sibDone := make(chan struct{})
	close(sibDone)
	ch.stopThinking.Store(chatIDStr+":999", &thinkingCancel{fn: sibCancel, doneChan: sibDone})

	// Entry belonging to a DIFFERENT chat: must survive, proving the
	// rollback is scoped to this chat and does not over-cancel.
	otherCtx, otherCancel := context.WithCancel(context.Background())
	otherDone := make(chan struct{})
	close(otherDone)
	otherKey := "99999:1"
	ch.stopThinking.Store(otherKey, &thinkingCancel{fn: otherCancel, doneChan: otherDone})

	// Placeholder for the same chat (stored keyed by chatID string, value
	// is the int Telegram message ID, as in telegram_messages.go).
	ch.placeholders.Store(chatIDStr, 4242)

	msg := bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "111",
		ChatID:   chatIDStr,
		Content:  "hello",
		Metadata: map[string]string{"message_id": messageID},
	}

	ch.InboundDroppedHook(msg)

	// Exact thinking key removed and its context cancelled.
	if _, ok := ch.stopThinking.Load(thinkingKey); ok {
		t.Error("thinking entry for exact key was not removed from stopThinking")
	}
	select {
	case <-ctx.Done():
	default:
		t.Error("typing indicator context for exact key was not cancelled")
	}

	// Safety net removed the sibling entry too.
	if _, ok := ch.stopThinking.Load(chatIDStr + ":999"); ok {
		t.Error("stopAllThinkingForChat safety net did not remove sibling entry")
	}
	select {
	case <-sibCtx.Done():
	default:
		t.Error("sibling typing indicator context was not cancelled")
	}

	// Placeholder deleted from the map and a deleteMessage call issued.
	if _, ok := ch.placeholders.Load(chatIDStr); ok {
		t.Error("placeholder entry was not removed from placeholders")
	}
	if got := rt.calls(); got != 1 {
		t.Errorf("expected exactly 1 deleteMessage HTTP call, got %d", got)
	}

	// The other chat's indicator must be untouched.
	if _, ok := ch.stopThinking.Load(otherKey); !ok {
		t.Error("rollback must not cancel thinking entries of other chats")
	}
	select {
	case <-otherCtx.Done():
		t.Error("other chat's typing indicator context was cancelled")
	default:
	}
}

// TestTelegramInboundDroppedHookMissingMetadata exercises the degraded paths:
// no metadata (key rebuild impossible — safety net must still sweep) and no
// placeholder (no HTTP delete must fire).
func TestTelegramInboundDroppedHookMissingMetadata(t *testing.T) {
	ch, rt := newTelegramHookTestChannel(t)
	ch.InboundDroppedHook = ch.handleInboundDropped

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	ch.stopThinking.Store("555:777", &thinkingCancel{fn: cancel, doneChan: done})

	ch.InboundDroppedHook(bus.InboundMessage{Channel: "telegram", ChatID: "555"})

	if _, ok := ch.stopThinking.Load("555:777"); ok {
		t.Error("safety net should sweep thinking entries even without message_id metadata")
	}
	select {
	case <-ctx.Done():
	default:
		t.Error("swept thinking entry was not cancelled")
	}
	if calls := rt.calls(); calls != 0 {
		t.Errorf("expected no deleteMessage calls without a placeholder, got %d", calls)
	}
}

// TestTelegramInboundDroppedHookEmptyChatID verifies the hook is a no-op for
// messages without a chat ID (e.g. session-keyed system publishes).
func TestTelegramInboundDroppedHookEmptyChatID(t *testing.T) {
	ch, rt := newTelegramHookTestChannel(t)
	ch.InboundDroppedHook = ch.handleInboundDropped

	ch.InboundDroppedHook(bus.InboundMessage{Channel: "telegram", ChatID: ""})

	if calls := rt.calls(); calls != 0 {
		t.Errorf("expected no HTTP activity for empty ChatID, got %d calls", calls)
	}
}

// TestTelegramHandleInboundDroppedHonorsDeadline ensures the placeholder
// deletion uses a bounded context (5s timeout per spec).
func TestTelegramHandleInboundDroppedHonorsDeadline(t *testing.T) {
	ch, _ := newTelegramHookTestChannel(t)

	var gotDeadline bool
	var deadline time.Duration
	ch.deleteHTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if d, ok := req.Context().Deadline(); ok {
			gotDeadline = true
			deadline = time.Until(d)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
	})}

	ch.placeholders.Store("42", 99)
	ch.handleInboundDropped(bus.InboundMessage{Channel: "telegram", ChatID: "42"})

	if !gotDeadline {
		t.Error("deleteMessage request must carry a bounded context deadline")
	}
	// Spec: 5s timeout inside the hook.
	if deadline <= 0 || deadline > 5*time.Second {
		t.Errorf("expected a bounded deadline of at most 5s, got %v", deadline)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
