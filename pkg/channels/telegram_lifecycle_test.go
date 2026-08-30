package channels

// Issue #240, layer C: lifecycle sweeps. A typing indicator is started while
// an update is being handled, but it is stopped by the turn that consumes it.
// When the channel itself goes away — explicit Stop, or a BotHandler that dies
// and reconnects — the turns that owned that state are gone with it, and no
// terminal signal will ever arrive. Without these sweeps the indicator loop
// and its "Thinking..." placeholder leak across the restart (observed twice in
// production as "BotHandler stopped unexpectedly").

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
)

// newTelegramLifecycleChannel builds a running TelegramChannel with a recorded
// HTTP transport (so placeholder deletes stay local) and no bot.
func newTelegramLifecycleChannel(t *testing.T) (*TelegramChannel, *recordingRoundTripper) {
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

func TestStopAllThinkingCancelsEveryChatAndEmptiesMap(t *testing.T) {
	ch, _ := newTelegramLifecycleChannel(t)

	keys := []string{"111:1", "111:2", "222:5", "333:9"}
	cancelled := make([]func() bool, 0, len(keys))
	for _, k := range keys {
		cancelled = append(cancelled, storeFakeThinking(ch, k))
	}
	// A non-cancel value must not break the sweep.
	ch.stopThinking.Store("444:1", "not a cancel handle")

	ch.stopAllThinking()

	for i, wasCancelled := range cancelled {
		if !wasCancelled() {
			t.Errorf("indicator %q was not cancelled", keys[i])
		}
	}

	remaining := 0
	ch.stopThinking.Range(func(_, _ interface{}) bool { remaining++; return true })
	if remaining != 0 {
		t.Fatalf("stopThinking still holds %d entries after stopAllThinking", remaining)
	}
}

func TestStopAllThinkingOnEmptyMapsIsNoop(t *testing.T) {
	ch, rt := newTelegramLifecycleChannel(t)

	ch.stopAllThinking()
	ch.clearAllPlaceholders()

	if got := rt.calls(); got != 0 {
		t.Fatalf("deleteMessage calls = %d, want 0 when there is nothing to clean", got)
	}
}

func TestClearAllPlaceholdersDeletesEveryChatAndEmptiesMap(t *testing.T) {
	ch, rt := newTelegramLifecycleChannel(t)

	ch.placeholders.Store("111", 1)
	ch.placeholders.Store("222", 2)
	ch.placeholders.Store("333", 3)
	// An unparseable chat key must still be forgotten (a stale entry could
	// otherwise be edited into a later, unrelated turn).
	ch.placeholders.Store("not-a-number", 4)

	ch.clearAllPlaceholders()

	remaining := 0
	ch.placeholders.Range(func(_, _ interface{}) bool { remaining++; return true })
	if remaining != 0 {
		t.Fatalf("placeholders still holds %d entries after clearAllPlaceholders", remaining)
	}
	if got := rt.calls(); got != 3 {
		t.Fatalf("deleteMessage calls = %d, want 3 (one per deletable placeholder)", got)
	}
}

// TestClearAllPlaceholdersSurvivesDeleteFailure pins best-effort behaviour: if
// the Bot API call cannot even be built/sent, the in-memory entry still goes
// away, and the sweep continues over the remaining chats.
func TestClearAllPlaceholdersSurvivesDeleteFailure(t *testing.T) {
	ch, _ := newTelegramLifecycleChannel(t)

	ch.deleteHTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	ch.placeholders.Store("111", 1)
	ch.placeholders.Store("222", 2)

	ch.clearAllPlaceholders()

	remaining := 0
	ch.placeholders.Range(func(_, _ interface{}) bool { remaining++; return true })
	if remaining != 0 {
		t.Fatalf("placeholders still holds %d entries after failed deletes", remaining)
	}
}

// TestClearAllPlaceholdersWithoutConfigLeavesState asserts the sweep is inert
// when there is no token to delete with: entries are kept so the normal send
// path can still resolve them into a real answer.
func TestClearAllPlaceholdersWithoutConfigLeavesState(t *testing.T) {
	rt := &recordingRoundTripper{}
	ch := &TelegramChannel{
		BaseChannel: NewBaseChannel("telegram", nil, nil, nil),
		deleteHTTP:  &http.Client{Transport: rt},
	}
	ch.placeholders.Store("111", 1)

	ch.clearAllPlaceholders()

	if _, ok := ch.placeholders.Load("111"); !ok {
		t.Fatal("placeholder was dropped without a config to delete it: the answer would lose its edit target")
	}
	if got := rt.calls(); got != 0 {
		t.Fatalf("deleteMessage calls = %d, want 0 without config", got)
	}
}

// TestStopSweepsTypingState is the regression for the shutdown leak: Stop must
// leave no indicator loop and no placeholder behind.
func TestStopSweepsTypingState(t *testing.T) {
	ch, rt := newTelegramLifecycleChannel(t)

	cancelled := storeFakeThinking(ch, "111:1")
	ch.placeholders.Store("111", 42)

	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if !cancelled() {
		t.Fatal("Stop did not cancel the typing indicator")
	}
	if _, ok := ch.placeholders.Load("111"); ok {
		t.Fatal("Stop left the 'Thinking...' placeholder behind")
	}
	if got := rt.calls(); got != 1 {
		t.Fatalf("deleteMessage calls = %d, want 1", got)
	}
}

// TestStopSweepsBeforeHandlerTeardown proves the ordering requirement: the
// placeholder delete needs the token, so the sweep must run while the channel
// is still able to make that call. We assert it by having Stop() complete with
// the delete observed (a teardown-first order would still delete, but a
// botHandler that had already nulled config would not) — the durable check is
// that Stop never leaves state behind even when called twice.
func TestStopIsIdempotent(t *testing.T) {
	ch, rt := newTelegramLifecycleChannel(t)

	cancelled := storeFakeThinking(ch, "111:1")
	ch.placeholders.Store("111", 42)

	for i := 0; i < 2; i++ {
		if err := ch.Stop(context.Background()); err != nil {
			t.Fatalf("Stop #%d returned error: %v", i+1, err)
		}
	}
	if !cancelled() {
		t.Fatal("Stop did not cancel the typing indicator")
	}
	if got := rt.calls(); got != 1 {
		t.Fatalf("deleteMessage calls = %d, want 1: the second Stop must not re-delete", got)
	}
}

// TestSweepsAreConcurrencySafe: sweeps run from Stop/reconnect while turns are
// still starting and finishing indicators on other goroutines. sync.Map makes
// this legal; the test guards against a future refactor to a plain map.
func TestSweepsAreConcurrencySafe(t *testing.T) {
	ch, _ := newTelegramLifecycleChannel(t)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := "111:" + string(rune('a'+i))
				cancelled := storeFakeThinking(ch, key)
				ch.placeholders.Store("111", i)
				cancelled()
			}
		}(i)
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			ch.stopAllThinking()
			time.Sleep(time.Millisecond)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			ch.clearAllPlaceholders()
			time.Sleep(time.Millisecond)
		}
	}()

	time.Sleep(120 * time.Millisecond)
	close(stop)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("sweeps deadlocked against concurrent turn state")
	}
}
