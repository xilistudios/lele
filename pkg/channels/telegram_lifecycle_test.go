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
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
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
	ch.clearAllPlaceholders(context.Background())

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

	ch.clearAllPlaceholders(context.Background())

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

	ch.clearAllPlaceholders(context.Background())

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

	ch.clearAllPlaceholders(context.Background())

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
			ch.clearAllPlaceholders(context.Background())
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

// --- turn.end ordering vs the running guard (review note on #240) -----------

// TestTelegramSendTurnEndIsProcessedAfterStop pins the ordering requirement:
// turn.end is a cleanup signal, not content, so it must be handled even when
// the channel is already stopped. During shutdown a turn.end can still be
// queued behind Stop() having set running=false; rejecting it would (a) log a
// misleading "Error sending message to channel" and (b) lose the last chance to
// clean up anything Stop()'s sweep did not catch.
func TestTelegramSendTurnEndIsProcessedAfterStop(t *testing.T) {
	ch, rt := newTelegramSendTestChannel(t)

	const chatKey, msgID = "123", "45"
	cancelled := storeFakeThinking(ch, chatKey+":"+msgID)
	ch.placeholders.Store(chatKey, 999)

	// The channel is already down: the guard below must not reject the signal.
	ch.setRunning(false)

	if err := ch.Send(context.Background(), bus.OutboundMessage{
		Channel:  "telegram",
		ChatID:   chatKey,
		Event:    "turn.end",
		Metadata: map[string]string{"message_id": msgID},
	}); err != nil {
		t.Fatalf("Send(turn.end) after Stop returned error: %v", err)
	}
	if !cancelled() {
		t.Fatal("turn.end was rejected by the running guard: typing indicator left behind")
	}
	if _, ok := ch.stopThinking.Load(chatKey + ":" + msgID); ok {
		t.Fatal("indicator entry left in stopThinking")
	}
	if _, ok := ch.placeholders.Load(chatKey); ok {
		t.Fatal("placeholder entry left after turn.end")
	}
	// deleteMessage is best-effort and must still have been attempted.
	if got := rt.calls(); got != 1 {
		t.Fatalf("deleteMessage calls = %d, want 1", got)
	}
}

// TestTelegramSendContentIsStillRejectedWhenStopped is the other side of the
// same door: only the contentless turn.end bypasses the running guard, real
// outbound content must keep failing fast so the dispatcher can report it.
func TestTelegramSendContentIsStillRejectedWhenStopped(t *testing.T) {
	ch, rt := newTelegramSendTestChannel(t)
	ch.setRunning(false)

	err := ch.Send(context.Background(), bus.OutboundMessage{
		Channel: "telegram",
		ChatID:  "123",
		Content: "hello",
	})
	if err == nil {
		t.Fatal("Send(content) must fail while the channel is stopped")
	}
	if got := rt.calls(); got != 0 {
		t.Fatalf("deleteMessage calls = %d, want 0", got)
	}
}

// --- sweep deadline cap (review note on #240) -------------------------------

// blockingRoundTripper simulates a degraded network: each deleteMessage call
// blocks until either its own context is done (as a real transport would) or
// hold elapses. hold should be longer than any deadline under test, so the
// only way a call returns is the context — which is exactly what makes the
// "one shared deadline vs. one deadline per placeholder" difference visible.
type blockingRoundTripper struct {
	hold  time.Duration
	start time.Time

	mu    sync.Mutex
	calls int
	// per request: how long after the sweep started it was sent, and the
	// deadline the request carried. hasDeadline guards against misreading an
	// already-expired budget as "no deadline at all".
	reqs []blockedRequest
}

// newBlockingRoundTripper starts the clock used to place each request on the
// sweep's timeline.
func newBlockingRoundTripper(hold time.Duration) *blockingRoundTripper {
	return &blockingRoundTripper{hold: hold, start: time.Now()}
}

type blockedRequest struct {
	sentAt      time.Duration
	hasDeadline bool
	budget      time.Duration // time.Until(deadline) at send time, may be <= 0
}

func (b *blockingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	b.mu.Lock()
	b.calls++
	br := blockedRequest{sentAt: time.Since(b.start)}
	if d, ok := req.Context().Deadline(); ok {
		br.hasDeadline = true
		br.budget = time.Until(d)
	}
	b.reqs = append(b.reqs, br)
	b.mu.Unlock()

	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-time.After(b.hold):
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
	}
}

func (b *blockingRoundTripper) stats() (calls int, reqs []blockedRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls, append([]blockedRequest(nil), b.reqs...)
}

// countPlaceholders counts the entries still held by a sync.Map.
func countPlaceholders(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ interface{}) bool { n++; return true })
	return n
}

// TestClearAllPlaceholdersCostIsNotLinearInN proves the cap from the caller's
// side: with a short parent deadline and a network that never answers, the
// sweep must give up ONCE (the shared deadline) and still forget every entry.
// Before the fix each placeholder got its own timeout derived from
// context.Background(), so the parent deadline was ignored entirely and the
// sweep cost N x per-call timeout.
func TestClearAllPlaceholdersCostIsNotLinearInN(t *testing.T) {
	ch, _ := newTelegramLifecycleChannel(t)
	const n = 5
	rt := newBlockingRoundTripper(3 * time.Second)
	ch.deleteHTTP = &http.Client{Transport: rt}

	for i := 0; i < n; i++ {
		ch.placeholders.Store(strconv.Itoa(100+i), i+1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	start := time.Now()
	ch.clearAllPlaceholders(ctx)
	elapsed := time.Since(start)

	// Generous margins: the point is that the sweep is bounded by ONE deadline
	// (~0.4s), not by n sequential ones (~2s here, 15s with the old 3s each).
	if elapsed > 2*time.Second {
		t.Fatalf("clearAllPlaceholders took %v with a 400ms parent deadline and %d placeholders: the deadline is not shared", elapsed, n)
	}
	if remaining := countPlaceholders(&ch.placeholders); remaining != 0 {
		t.Fatalf("placeholders still holds %d entries after the sweep: a failed/timed-out delete must still forget the entry", remaining)
	}
	if calls, _ := rt.stats(); calls != n {
		t.Fatalf("delete attempts = %d, want %d (every entry is attempted even after the deadline expires)", calls, n)
	}
}

// TestClearAllPlaceholdersDeletesInheritParentDeadline asserts structurally
// that every delete request carries a deadline derived from the caller's
// context: with a 300ms parent and a transport that never answers, no request
// may be sent with a budget larger than what the parent has left. Before the
// fix the sweep built context.Background() per placeholder, so the parent was
// ignored and each request carried its own full 3s timeout.
func TestClearAllPlaceholdersDeletesInheritParentDeadline(t *testing.T) {
	ch, _ := newTelegramLifecycleChannel(t)
	rt := newBlockingRoundTripper(5 * time.Second)
	ch.deleteHTTP = &http.Client{Transport: rt}

	for _, key := range []string{"111", "222", "333"} {
		ch.placeholders.Store(key, 1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	ch.clearAllPlaceholders(ctx)

	_, reqs := rt.stats()
	if len(reqs) != 3 {
		t.Fatalf("got %d delete requests, want 3: every entry must still be attempted", len(reqs))
	}
	for i, r := range reqs {
		if !r.hasDeadline {
			t.Fatalf("request #%d was sent without any deadline", i+1)
		}
		// The budget a request carries, projected onto the sweep's timeline,
		// must never exceed what the parent allowed (300ms) plus scheduling
		// slack. A per-placeholder timeout built on context.Background()
		// overshoots it by seconds, so the margin cannot hide a regression.
		// Requests sent once the shared deadline has already expired carry a
		// negative budget and pass trivially: they were bounded by the same
		// deadline, which is exactly the fast-fail the fix relies on.
		if budget := r.sentAt + r.budget; budget > 350*time.Millisecond {
			t.Fatalf("request #%d carries deadline %v sent at %v (budget %v): the caller deadline was not propagated", i+1, r.budget, r.sentAt, budget)
		}
	}
	if got := countPlaceholders(&ch.placeholders); got != 0 {
		t.Fatalf("placeholders still holds %d entries", got)
	}
}

// TestClearAllPlaceholdersCapsTotalTimeWithoutParentDeadline is the direct
// guarantee of the global cap: a caller with no deadline at all (Stop() during
// a shutdown whose ctx is not bounded) must still not wait for N timeouts.
// n=5 placeholders on a network that only answers when the context is
// cancelled would cost n x cap without the fix; with it, one cap total.
func TestClearAllPlaceholdersCapsTotalTimeWithoutParentDeadline(t *testing.T) {
	ch, _ := newTelegramLifecycleChannel(t)
	const n = 5
	// Never answers on its own: every call is released solely by the deadline.
	rt := newBlockingRoundTripper(time.Hour)
	ch.deleteHTTP = &http.Client{Transport: rt}

	for i := 0; i < n; i++ {
		ch.placeholders.Store(strconv.Itoa(200+i), i+1)
	}

	start := time.Now()
	ch.clearAllPlaceholders(context.Background())
	elapsed := time.Since(start)

	// Lower bound: the first call really did block until the cap released it.
	if elapsed < time.Second {
		t.Fatalf("clearAllPlaceholders returned in %v: the deletes were not actually attempted/blocked", elapsed)
	}
	// Upper bound: generous vs placeholderSweepDeadline (5s) and far below the
	// linear cost (n x 5s = 25s).
	if elapsed > 12*time.Second {
		t.Fatalf("clearAllPlaceholders took %v for %d placeholders: sweep is not capped by placeholderSweepDeadline (%v)", elapsed, n, placeholderSweepDeadline)
	}
	if remaining := countPlaceholders(&ch.placeholders); remaining != 0 {
		t.Fatalf("placeholders still holds %d entries after the capped sweep", remaining)
	}
}

// TestSweepOrphanedTurnStateEmptyIsNoop pins the early-return: nothing to
// clean means no HTTP work at all (and no timer/deadline involved).
func TestSweepOrphanedTurnStateEmptyIsNoop(t *testing.T) {
	ch, rt := newTelegramLifecycleChannel(t)

	ch.sweepOrphanedTurnState(context.Background(), "nothing happened")

	if got := rt.calls(); got != 0 {
		t.Fatalf("deleteMessage calls = %d, want 0 when there is nothing to clean", got)
	}
}

// TestStopHonoursCallerDeadline: a shutdown ctx that is already gone must not
// be prolonged by the sweep.
func TestStopHonoursCallerDeadline(t *testing.T) {
	ch, _ := newTelegramLifecycleChannel(t)
	rt := newBlockingRoundTripper(time.Hour)
	ch.deleteHTTP = &http.Client{Transport: rt}

	ch.placeholders.Store("111", 1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := ch.Stop(ctx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Stop took %v despite a 50ms caller deadline", elapsed)
	}
	if got := countPlaceholders(&ch.placeholders); got != 0 {
		t.Fatalf("placeholders still holds %d entries after Stop", got)
	}
}
