package channels

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

func TestTelegramSendTurnEndIsProcessedAfterStop(t *testing.T) {
	ch, rt := newTelegramSendTestChannel(t)

	const chatKey, msgID = "123", "45"
	cancelled := storeFakeThinking(ch, chatKey+":"+msgID)
	ch.placeholders.Store(chatKey, 999)

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
	if got := rt.calls(); got != 1 {
		t.Fatalf("deleteMessage calls = %d, want 1", got)
	}
}

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

type blockingRoundTripper struct {
	hold  time.Duration
	start time.Time

	mu    sync.Mutex
	calls int
	reqs  []blockedRequest
}

func newBlockingRoundTripper(hold time.Duration) *blockingRoundTripper {
	return &blockingRoundTripper{hold: hold, start: time.Now()}
}

type blockedRequest struct {
	sentAt      time.Duration
	hasDeadline bool
	budget      time.Duration
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

func countPlaceholders(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ interface{}) bool { n++; return true })
	return n
}

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
		if budget := r.sentAt + r.budget; budget > 350*time.Millisecond {
			t.Fatalf("request #%d carries deadline %v sent at %v (budget %v): the caller deadline was not propagated", i+1, r.budget, r.sentAt, budget)
		}
	}
	if got := countPlaceholders(&ch.placeholders); got != 0 {
		t.Fatalf("placeholders still holds %d entries", got)
	}
}

func TestClearAllPlaceholdersCapsTotalTimeWithoutParentDeadline(t *testing.T) {
	ch, _ := newTelegramLifecycleChannel(t)
	const n = 5
	rt := newBlockingRoundTripper(time.Hour)
	ch.deleteHTTP = &http.Client{Transport: rt}

	for i := 0; i < n; i++ {
		ch.placeholders.Store(strconv.Itoa(200+i), i+1)
	}

	start := time.Now()
	ch.clearAllPlaceholders(context.Background())
	elapsed := time.Since(start)

	if elapsed < time.Second {
		t.Fatalf("clearAllPlaceholders returned in %v: the deletes were not actually attempted/blocked", elapsed)
	}
	if elapsed > 12*time.Second {
		t.Fatalf("clearAllPlaceholders took %v for %d placeholders: sweep is not capped by placeholderSweepDeadline (%v)", elapsed, n, placeholderSweepDeadline)
	}
	if remaining := countPlaceholders(&ch.placeholders); remaining != 0 {
		t.Fatalf("placeholders still holds %d entries after the capped sweep", remaining)
	}
}

func TestSweepOrphanedTurnStateEmptyIsNoop(t *testing.T) {
	ch, rt := newTelegramLifecycleChannel(t)

	ch.sweepOrphanedTurnState(context.Background(), "nothing happened")

	if got := rt.calls(); got != 0 {
		t.Fatalf("deleteMessage calls = %d, want 0 when there is nothing to clean", got)
	}
}

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
