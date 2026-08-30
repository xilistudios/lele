package channels

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// Issue #240, layer A: the agent loop publishes a terminal "turn.end" event on
// every turn exit, so a channel's typing indicator can never outlive the turn
// that started it — not even when the turn produced no final message.

// --- shouldDropEventSignal (pure predicate behind the dispatcher guard) -----

// channelsWithEventSupport mirrors the channels that declare (and implement)
// event handling, so the predicate is tested against the real capability
// surface rather than a name.
func TestShouldDropEventSignal(t *testing.T) {
	native := &NativeChannel{}
	telegram := &TelegramChannel{}
	dumb := newMockChannel("whatsapp", nil)

	cases := []struct {
		name string
		ch   Channel
		msg  bus.OutboundMessage
		want bool
		why  string
	}{
		{
			name: "native keeps empty message.stream done",
			ch:   native,
			msg:  bus.OutboundMessage{Event: "message.stream", Content: "", Metadata: map[string]string{"done": "true"}},
			want: false,
			why:  "native consumes its own events inside Send; dropping them would break stream finalization",
		},
		{
			name: "native keeps empty turn.end",
			ch:   native,
			msg:  bus.OutboundMessage{Event: "turn.end"},
			want: false,
			why:  "native must receive turn.end and ignore it explicitly, never see it as a message",
		},
		{
			name: "telegram keeps turn.end",
			ch:   telegram,
			msg:  bus.OutboundMessage{Event: "turn.end"},
			want: false,
			why: "telegram stops its typing indicator on turn.end: dropping the signal here " +
				"would reintroduce the stuck-indicator bug #240",
		},
		{
			name: "eventless message is never dropped",
			ch:   dumb,
			msg:  bus.OutboundMessage{Content: "real answer"},
			want: false,
			why:  "a plain message is content, not a signal",
		},
		{
			name: "eventless empty message is outside this guard",
			ch:   dumb,
			msg:  bus.OutboundMessage{Content: ""},
			want: false,
			why:  `the guard scopes to signals (Event != ""); empty non-event messages belong to other layers' contract`,
		},
		{
			name: "eventless whitespace message is outside this guard",
			ch:   dumb,
			msg:  bus.OutboundMessage{Content: "   \n\t "},
			want: false,
			why:  "same as above: Event is empty",
		},
		{
			name: "channel without event support drops empty turn.end",
			ch:   dumb,
			msg:  bus.OutboundMessage{Event: "turn.end"},
			want: true,
			why:  "whatsapp does not understand turn.end; forwarding it would render an empty bubble",
		},
		{
			name: "event with content passes through",
			ch:   dumb,
			msg:  bus.OutboundMessage{Event: "tool.result", Content: "verbose tool output"},
			want: false,
			why:  "content-carrying events are user-visible today; dropping them would be a regression",
		},
		{
			name: "event with whitespace-only content is dropped",
			ch:   dumb,
			msg:  bus.OutboundMessage{Event: "tool.result", Content: "  \t "},
			want: true,
			why:  "whitespace renders nothing; it is a signal in disguise",
		},
		{
			name: "event with attachments passes through",
			ch:   dumb,
			msg:  bus.OutboundMessage{Event: "tool.result", Content: "", Attachments: []bus.FileAttachment{{Path: "/tmp/x.png"}}},
			want: false,
			why:  "attachments are real payload even without text",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldDropEventSignal(tc.ch, tc.msg); got != tc.want {
				t.Fatalf("shouldDropEventSignal(%T, event=%q content=%q) = %v, want %v (%s)",
					tc.ch, tc.msg.Event, tc.msg.Content, got, tc.want, tc.why)
			}
		})
	}
}

// TestConsumesEventMatchesSendHandler is the structural guard against the
// allowlist rot that a name-based exemption would introduce: every event a
// channel declares it consumes must actually be branched on in its Send, and
// every event Send branches on must be declared. A channel that adds an event
// to one side only would either render an empty message or silently lose the
// signal (stuck typing).
func TestConsumesEventMatchesSendHandler(t *testing.T) {
	// Events the agent protocol can emit as contentless signals.
	protocolEvents := []string{
		"turn.end", "message.stream", "message.thinking", "tool.executing",
		"tool.result", "subagent.result", "group.status", "group.turn",
		"group.tool", "group.complete", "approval.request",
	}

	tg := &TelegramChannel{}
	for _, event := range protocolEvents {
		handled := telegramSendHandlesEvent(event)
		if got := tg.ConsumesEvent(event); got != handled {
			t.Errorf("TelegramChannel.ConsumesEvent(%q) = %v, but Send handles it = %v: "+
				"declaration and handler must agree", event, got, handled)
		}
	}

	n := &NativeChannel{}
	for _, event := range protocolEvents {
		if !n.ConsumesEvent(event) {
			t.Errorf("NativeChannel must consume every protocol event (dispatchOutboundMessage switches on all of them), got false for %q", event)
		}
	}
}

// telegramSendHandlesEvent mirrors the events TelegramChannel.Send actually
// branches on. It is the test-side oracle for the consistency check above;
// keep it in sync with Send (a mismatch fails TestConsumesEventMatchesSendHandler).
func telegramSendHandlesEvent(event string) bool {
	switch event {
	case "turn.end":
		return true
	default:
		return false
	}
}

// --- the guard wired into the real per-channel dispatcher -------------------

// eventCapableMock is a mock channel that declares event capability, the way
// native and telegram do.
type eventCapableMock struct {
	*mockChannel
}

func (e *eventCapableMock) ConsumesEvent(string) bool { return true }

// TestDispatcherGuardAppliedForumslessChannels exercises the guard where it
// lives (Manager.startChannelDispatcher): a channel that never inspects
// msg.Event must not receive contentless signals, while a channel that declares
// capability must receive them untouched.
func TestDispatcherGuardAppliedForChannels(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	dumb := newMockChannel("dumb", messageBus)
	capable := &eventCapableMock{newMockChannel(ChannelName, messageBus)}

	mgr, err := NewManager(config.DefaultConfig(), messageBus, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	mgr.RegisterChannel("dumb", dumb)
	mgr.dispatchQueues["dumb"] = make(chan bus.OutboundMessage, 10)
	mgr.RegisterChannel(ChannelName, capable)
	mgr.dispatchQueues[ChannelName] = make(chan bus.OutboundMessage, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.startChannelDispatcher(ctx, "dumb", dumb, mgr.dispatchQueues["dumb"])
	go mgr.startChannelDispatcher(ctx, ChannelName, capable, mgr.dispatchQueues[ChannelName])

	// Push straight into the per-channel queues: this test is about
	// startChannelDispatcher, not about the bus routing done by dispatchOutbound.
	//
	// Signals that must never reach a channel that does not handle events.
	mgr.dispatchQueues["dumb"] <- bus.OutboundMessage{Channel: "dumb", ChatID: "c1", Event: "turn.end"}
	mgr.dispatchQueues["dumb"] <- bus.OutboundMessage{Channel: "dumb", ChatID: "c1", Event: "tool.result", Content: "   "}
	// Payloads that must always reach it.
	mgr.dispatchQueues["dumb"] <- bus.OutboundMessage{Channel: "dumb", ChatID: "c1", Content: "hello"}
	mgr.dispatchQueues["dumb"] <- bus.OutboundMessage{Channel: "dumb", ChatID: "c1", Event: "tool.result", Content: "verbose result"}
	// A capability-declaring channel receives its signals.
	mgr.dispatchQueues[ChannelName] <- bus.OutboundMessage{Channel: ChannelName, ChatID: "s1", Event: "turn.end"}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(dumb.getSent()) == 2 && len(capable.getSent()) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	sent := dumb.getSent()
	if len(sent) != 2 {
		t.Fatalf("dumb channel received %d messages, want 2 (contentless signals must be dropped): %+v", len(sent), sent)
	}
	if sent[0].Content != "hello" || sent[1].Content != "verbose result" {
		t.Fatalf("unexpected payloads delivered: %q, %q", sent[0].Content, sent[1].Content)
	}

	got := capable.getSent()
	if len(got) != 1 {
		t.Fatalf("capable channel received %d messages, want 1 (it declared the capability): %+v", len(got), got)
	}
	if got[0].Event != "turn.end" {
		t.Fatalf("capable channel must receive turn.end, got %q", got[0].Event)
	}
}

// TestDispatcherDeliversTurnEndToRealTelegramChannel is the regression that
// makes the guard safe: the real TelegramChannel must still get turn.end
// through the dispatcher, because stopping the typing indicator depends on it.
// A name-based exemption would have silently swallowed this signal.
func TestDispatcherDeliversTurnEndToRealTelegramChannel(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	tg, _ := newTelegramSendTestChannel(t)

	mgr, err := NewManager(config.DefaultConfig(), messageBus, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	mgr.RegisterChannel("telegram", tg)
	mgr.dispatchQueues["telegram"] = make(chan bus.OutboundMessage, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.startChannelDispatcher(ctx, "telegram", tg, mgr.dispatchQueues["telegram"])

	cancelled := storeFakeThinking(tg, "777:1")
	mgr.dispatchQueues["telegram"] <- bus.OutboundMessage{
		Channel: "telegram", ChatID: "777", Event: "turn.end", Metadata: map[string]string{"message_id": "1"},
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !cancelled() {
		time.Sleep(10 * time.Millisecond)
	}
	if !cancelled() {
		t.Fatal("turn.end was dropped before reaching Telegram.Send: typing indicator would stay on forever")
	}
}

// --- Telegram.Send handles turn.end as a terminal, silent, idempotent signal -

// newTelegramSendTestChannel builds a TelegramChannel that is running but has
// no bot: the turn.end path must clean state and return before touching c.bot,
// which is what makes the test network-free.
func newTelegramSendTestChannel(t *testing.T) (*TelegramChannel, *recordingRoundTripper) {
	t.Helper()

	cfg := &config.Config{}
	cfg.Channels.Telegram.Token = "TESTTOKEN"
	rt := &recordingRoundTripper{}
	ch := &TelegramChannel{
		BaseChannel: NewBaseChannel("telegram", cfg.Channels.Telegram, nil, nil),
		config:      cfg,
		deleteHTTP:  &http.Client{Transport: rt},
	}
	ch.setRunning(true)
	return ch, rt
}

// storeFakeThinking registers a typing indicator whose loop is already dead
// (doneChan closed), so Cancel() returns immediately without a real bot. It
// reports whether the indicator has been cancelled.
func storeFakeThinking(ch *TelegramChannel, key string) func() bool {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	ch.stopThinking.Store(key, &thinkingCancel{fn: cancel, doneChan: done})
	return func() bool { return ctx.Err() != nil }
}

func TestTelegramSendTurnEndStopsTypingAndSendsNothing(t *testing.T) {
	ch, rt := newTelegramSendTestChannel(t)

	const chatKey, msgID = "12345", "678"
	cancelled := storeFakeThinking(ch, chatKey+":"+msgID)
	sibCancelled := storeFakeThinking(ch, chatKey+":999")
	ch.placeholders.Store(chatKey, 4242)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		Channel:  "telegram",
		ChatID:   chatKey,
		Event:    "turn.end",
		Metadata: map[string]string{"message_id": msgID},
	})
	if err != nil {
		t.Fatalf("Send(turn.end) returned error: %v", err)
	}
	if !cancelled() {
		t.Fatal("turn.end did not cancel the typing indicator for the exact key")
	}
	if !sibCancelled() {
		t.Fatal("turn.end did not sweep sibling indicators of the same chat")
	}
	if _, ok := ch.stopThinking.Load(chatKey + ":" + msgID); ok {
		t.Fatal("indicator entry left in stopThinking")
	}
	if _, ok := ch.placeholders.Load(chatKey); ok {
		t.Fatal("placeholder entry left after turn.end")
	}
	if got := rt.calls(); got != 1 {
		t.Fatalf("deleteMessage calls = %d, want 1 (placeholder deleted exactly once)", got)
	}
}

func TestTelegramSendTurnEndIsIdempotent(t *testing.T) {
	ch, rt := newTelegramSendTestChannel(t)

	storeFakeThinking(ch, "12345:1")
	ch.placeholders.Store("12345", 77)

	sig := bus.OutboundMessage{Channel: "telegram", ChatID: "12345", Event: "turn.end", Metadata: map[string]string{"message_id": "1"}}
	for i := 0; i < 3; i++ {
		if err := ch.Send(context.Background(), sig); err != nil {
			t.Fatalf("Send(turn.end) #%d returned error: %v", i+1, err)
		}
	}
	if got := rt.calls(); got != 1 {
		t.Fatalf("deleteMessage calls = %d, want 1: repeated turn.end must be a no-op", got)
	}
}

// TestTelegramSendTurnEndWithInvalidChatIDStillStopsTyping is the CAPA D
// invariant stated early: cleanup happens before chat-ID parsing, so a
// malformed ChatID can never leave the indicator running.
func TestTelegramSendTurnEndWithInvalidChatIDStillStopsTyping(t *testing.T) {
	ch, _ := newTelegramSendTestChannel(t)

	cancelled := storeFakeThinking(ch, "not-a-number:5")

	if err := ch.Send(context.Background(), bus.OutboundMessage{
		Channel: "telegram", ChatID: "not-a-number", Event: "turn.end",
	}); err != nil {
		t.Fatalf("Send(turn.end) must not fail on an unparseable chat ID: %v", err)
	}
	if !cancelled() {
		t.Fatal("typing indicator survived turn.end because the chat ID could not be parsed")
	}
	if _, ok := ch.stopThinking.Load("not-a-number:5"); ok {
		t.Fatal("indicator entry left in stopThinking")
	}
}

func TestTelegramSendTurnEndScopedToChat(t *testing.T) {
	ch, rt := newTelegramSendTestChannel(t)

	mineCancelled := storeFakeThinking(ch, "111:1")
	otherCancelled := storeFakeThinking(ch, "222:2")
	ch.placeholders.Store("222", 99)

	if err := ch.Send(context.Background(), bus.OutboundMessage{Channel: "telegram", ChatID: "111", Event: "turn.end"}); err != nil {
		t.Fatalf("Send(turn.end) error: %v", err)
	}
	if !mineCancelled() {
		t.Fatal("own indicator was not cancelled")
	}
	if otherCancelled() {
		t.Fatal("turn.end for chat 111 cancelled the indicator of chat 222")
	}
	if _, ok := ch.placeholders.Load("222"); !ok {
		t.Fatal("turn.end for chat 111 deleted the placeholder of chat 222")
	}
	if got := rt.calls(); got != 0 {
		t.Fatalf("deleteMessage calls = %d, want 0 (other chat's placeholder must survive)", got)
	}
}

func TestTelegramSendTurnEndEmptyChatIDIsSafe(t *testing.T) {
	ch, rt := newTelegramSendTestChannel(t)

	if err := ch.Send(context.Background(), bus.OutboundMessage{Channel: "telegram", Event: "turn.end"}); err != nil {
		t.Fatalf("Send(turn.end) with empty ChatID returned error: %v", err)
	}
	if got := rt.calls(); got != 0 {
		t.Fatalf("deleteMessage calls = %d, want 0", got)
	}
}

// TestTelegramSendTurnEndNotGatedByRateLimiter proves the terminal signal does
// no network work and takes no rate-limit token: a burst must complete at once
// instead of queueing behind the outbound limiter.
func TestTelegramSendTurnEndNotGatedByRateLimiter(t *testing.T) {
	ch, _ := newTelegramSendTestChannel(t)

	cancelled := storeFakeThinking(ch, "12345:1")

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i := 0; i < 30; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := ch.Send(context.Background(), bus.OutboundMessage{
					Channel: "telegram", ChatID: "12345", Event: "turn.end",
				}); err != nil {
					t.Errorf("Send(turn.end) error: %v", err)
				}
			}()
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("turn.end sends blocked (rate limiter or lock contention)")
	}
	if !cancelled() {
		t.Fatal("indicator not cancelled by turn.end burst")
	}
}

// TestNativeSendTurnEndEmitsNothing asserts the native channel consumes the
// signal silently. Without the explicit `case "turn.end"` in the dispatch
// switch, an event with empty content falls through to the full-message path
// and emits message.complete + history.updated — a spurious empty completion
// bubble in the WebUI on every turn.
func TestNativeSendTurnEndEmitsNothing(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-turnend"
	client := registerFakeWSClient(t, ts, sessionKey)

	if err := ts.channel.Send(context.Background(), bus.OutboundMessage{
		Channel: ChannelName, ChatID: sessionKey, Event: "turn.end",
	}); err != nil {
		t.Fatalf("native Send(turn.end) returned error: %v", err)
	}

	if events := drainWSEvents(t, client); len(events) != 0 {
		t.Fatalf("native emitted %d WS frame(s) for turn.end, want 0: %v", len(events), eventNames(events))
	}
}

// TestNativeDispatchControlFinalMessageStillEmits is the liveness control for
// the test above: the same channel/session does emit events for a normal final
// message, so "zero frames" can only mean turn.end was consumed.
func TestNativeDispatchControlFinalMessageStillEmits(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-turnend-ctl"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel: ChannelName, ChatID: sessionKey, Content: "hi", MessageID: "m1",
	})

	events := drainWSEvents(t, client)
	if findWSEvent(events, "message.stream") == nil {
		t.Fatalf("control: expected message.stream for a normal message, got %v", eventNames(events))
	}
}
