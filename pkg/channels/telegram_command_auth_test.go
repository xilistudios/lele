package channels

import (
	"bytes"
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/mymmrac/telego"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func newTelegramAuthTestChannel(t *testing.T, allowList []string) (*TelegramChannel, *recordingRoundTripper) {
	t.Helper()

	cfg := &config.Config{}
	cfg.Channels.Telegram.Token = "123456:" + string(bytes.Repeat([]byte("a"), 35))

	rt := &recordingRoundTripper{}
	bot, err := telego.NewBot(
		cfg.Channels.Telegram.Token,
		telego.WithHTTPClient(&http.Client{Transport: rt}),
		telego.WithDefaultLogger(false, false),
	)
	if err != nil {
		t.Fatalf("telego.NewBot: %v", err)
	}

	return &TelegramChannel{
		BaseChannel:  NewBaseChannel("telegram", cfg.Channels.Telegram, bus.NewMessageBus(), allowList),
		config:       cfg,
		bot:          bot,
		deleteHTTP:   &http.Client{Transport: rt},
		chatIDs:      make(map[string]int64),
		processedIDs: make(map[string]int64),
	}, rt
}

func callAllowlistedHandler(t *testing.T, fn func() error) (err error) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler ran past the allowlist guard (panic: %v)", r)
		}
	}()

	return fn()
}

func TestTelegramCommandRejectedForUnallowedSender(t *testing.T) {
	for _, spec := range telegramCommandRegistry {
		t.Run(spec.name, func(t *testing.T) {
			channel, rt := newTelegramAuthTestChannel(t, []string{"42"})

			message := &telego.Message{
				Text:      "/" + spec.name + " extra-args",
				MessageID: 21,
				Chat:      telego.Chat{ID: 555, Type: "group"},
				From:      &telego.User{ID: 99, Username: "mallory"},
			}

			err := callAllowlistedHandler(t, func() error {
				return channel.handleCommandWithSession(context.Background(), message, spec.name)
			})
			if err != nil {
				t.Fatalf("handleCommandWithSession() error = %v, want nil", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if inbound, ok := channel.bus.ConsumeInbound(ctx); ok {
				t.Fatalf("non-allowlisted sender published inbound message: %#v", inbound)
			}

			if calls := rt.calls(); calls != 0 {
				t.Fatalf("non-allowlisted sender triggered %d bot API calls, want 0", calls)
			}
		})
	}
}

func TestTelegramCommandAllowedSenderStillWorks(t *testing.T) {
	channel, _ := newTelegramAuthTestChannel(t, []string{"99|alice"})

	message := &telego.Message{
		Text:      "/subagents continue subagent-3 use branch main",
		MessageID: 22,
		Chat:      telego.Chat{ID: 12345},
		From:      &telego.User{ID: 99, Username: "alice"},
	}

	if err := channel.handleCommandWithSession(context.Background(), message, "subagents"); err != nil {
		t.Fatalf("handleCommandWithSession() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inbound, ok := channel.bus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("allowlisted sender produced no inbound command")
	}

	if inbound.Channel != "system" {
		t.Fatalf("channel = %q, want system", inbound.Channel)
	}
	if inbound.SenderID != "99" {
		t.Fatalf("sender_id = %q, want 99", inbound.SenderID)
	}
	if inbound.SessionKey != "telegram:12345" {
		t.Fatalf("session_key = %q, want telegram:12345", inbound.SessionKey)
	}
	if inbound.Content != "/subagents continue subagent-3 use branch main" {
		t.Fatalf("content = %q", inbound.Content)
	}
}

func TestTelegramGuardedCallbackRejectsUnallowedSender(t *testing.T) {
	channel, _ := newTelegramAuthTestChannel(t, []string{"42"})

	calls := 0
	spy := func(context.Context, telego.CallbackQuery) error {
		calls++
		return nil
	}

	unallowed := telego.CallbackQuery{
		ID:   "cb-unallowed",
		From: telego.User{ID: 99, Username: "mallory"},
		Data: "think:high",
	}
	if err := channel.guardedCallback(spy)(context.Background(), unallowed); err != nil {
		t.Fatalf("guardedCallback() error = %v, want nil", err)
	}
	if calls != 0 {
		t.Fatalf("callback handler ran for a non-allowlisted sender (%d calls)", calls)
	}

	allowed := telego.CallbackQuery{
		ID:   "cb-allowed",
		From: telego.User{ID: 42},
		Data: "think:high",
	}
	if err := channel.guardedCallback(spy)(context.Background(), allowed); err != nil {
		t.Fatalf("guardedCallback() error = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("callback handler calls = %d, want 1 for an allowlisted sender", calls)
	}
}

func TestTelegramCallbackRegistryIsGuarded(t *testing.T) {
	channel, _ := newTelegramAuthTestChannel(t, []string{"42"})

	raw := channel.telegramCallbackRegistry()
	guarded := channel.guardedCallbacks()
	if len(raw) != len(guarded) {
		t.Fatalf("guarded callbacks = %d, want %d", len(guarded), len(raw))
	}

	for i := range raw {
		if raw[i].prefix != guarded[i].prefix {
			t.Fatalf("guarded callback %d prefix = %q, want %q", i, guarded[i].prefix, raw[i].prefix)
		}
		if reflect.ValueOf(raw[i].handler).Pointer() == reflect.ValueOf(guarded[i].handler).Pointer() {
			t.Fatalf("callback prefix %q is not allowlist-wrapped; setupBotHandler would dispatch it unguarded", raw[i].prefix)
		}
	}
}

func TestPublishSystemCommandRejectsUnallowedSender(t *testing.T) {
	channel, _ := newTelegramAuthTestChannel(t, []string{"42"})

	channel.publishSystemCommand("99", 555, 23, "/agent research", nil, "telegram:555")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if inbound, ok := channel.bus.ConsumeInbound(ctx); ok {
		t.Fatalf("publishSystemCommand accepted a non-allowlisted sender: %#v", inbound)
	}
}
