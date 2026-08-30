package channels

// Issue #240, layer B: the typing indicator must have a hard ceiling of its
// own. Layers A/C/D stop it on known exits; the TTL is the backstop for exits
// nobody predicted (a crash mid-turn, a signal dropped by a full queue, a
// channel restarted under a live turn). Without it a leaked indicator refresh
// loop runs for the lifetime of the process.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mymmrac/telego"

	"github.com/xilistudios/lele/pkg/config"
)

// waitForDone waits for the loop to exit, reporting whether it did.
func waitForDone(t *testing.T, done <-chan struct{}, within time.Duration) bool {
	t.Helper()
	select {
	case <-done:
		return true
	case <-time.After(within):
		return false
	}
}

// TestRunTypingLoopExpiresOnTTLOnItsOwn is the core guarantee: nobody cancels
// the context, the loop still stops and reports the expiry exactly once.
func TestRunTypingLoopExpiresOnTTLOnItsOwn(t *testing.T) {
	const (
		interval = 10 * time.Millisecond
		ttl      = 100 * time.Millisecond
	)

	var sends atomic.Int64
	var expires atomic.Int64

	start := time.Now()
	done := runTypingLoop(context.Background(), interval, ttl,
		func(context.Context) error { sends.Add(1); return nil },
		func() { expires.Add(1) },
	)

	if !waitForDone(t, done, 3*time.Second) {
		t.Fatal("runTypingLoop did not exit on TTL: the indicator would leak forever")
	}
	elapsed := time.Since(start)

	if got := expires.Load(); got != 1 {
		t.Fatalf("onExpire calls = %d, want 1", got)
	}
	if sends.Load() < 2 {
		t.Fatalf("sends = %d, want >= 2 (initial send plus at least one refresh)", sends.Load())
	}
	// The loop must not exit early: it has to survive at least one interval.
	if elapsed < ttl {
		t.Fatalf("loop exited after %v, before the %v TTL", elapsed, ttl)
	}
	// And not much later — the TTL is a ceiling, not a suggestion. Generous
	// margin for a loaded CI machine.
	if elapsed > ttl+2*time.Second {
		t.Fatalf("loop exited after %v, far beyond the %v TTL", elapsed, ttl)
	}
}

// TestRunTypingLoopCancelDoesNotReportExpiry pins the other half: a normal
// turn end must be silent. If onExpire fired on cancellation, every completed
// turn would log a bogus TTL warning and (in production) cancel state that is
// already being cleaned up.
func TestRunTypingLoopCancelDoesNotReportExpiry(t *testing.T) {
	var sends atomic.Int64
	var expires atomic.Int64

	ctx, cancel := context.WithCancel(context.Background())
	done := runTypingLoop(ctx, 10*time.Millisecond, time.Hour,
		func(context.Context) error { sends.Add(1); return nil },
		func() { expires.Add(1) },
	)

	// Let the loop establish itself, then cancel like a final message would.
	time.Sleep(50 * time.Millisecond)
	cancel()

	if !waitForDone(t, done, 2*time.Second) {
		t.Fatal("runTypingLoop did not exit on context cancellation")
	}
	if got := expires.Load(); got != 0 {
		t.Fatalf("onExpire calls = %d, want 0 on cancellation", got)
	}
	if sends.Load() < 1 {
		t.Fatal("the initial send never happened")
	}
}

// TestRunTypingLoopSurvivesSendErrors: a transient Bot API failure must not
// kill the indicator for the rest of a long turn.
func TestRunTypingLoopSurvivesSendErrors(t *testing.T) {
	const ttl = 120 * time.Millisecond

	var sends atomic.Int64
	var expires atomic.Int64

	done := runTypingLoop(context.Background(), 10*time.Millisecond, ttl,
		func(context.Context) error {
			sends.Add(1)
			return errors.New("telegram: 429 too many requests")
		},
		func() { expires.Add(1) },
	)

	if !waitForDone(t, done, 3*time.Second) {
		t.Fatal("runTypingLoop exited on a send error instead of running to the TTL")
	}
	if sends.Load() < 2 {
		t.Fatalf("sends = %d, want >= 2: the loop stopped after the first error", sends.Load())
	}
	if expires.Load() != 1 {
		t.Fatalf("onExpire calls = %d, want 1 (TTL path still reported)", expires.Load())
	}
}

// TestRunTypingLoopStopsPromptlyOnCancel guards against a leaked goroutine:
// after the loop returns, no further send may be issued.
func TestRunTypingLoopStopsPromptlyOnCancel(t *testing.T) {
	var mu sync.Mutex
	var sends int

	ctx, cancel := context.WithCancel(context.Background())
	done := runTypingLoop(ctx, 5*time.Millisecond, time.Hour,
		func(context.Context) error {
			mu.Lock()
			sends++
			mu.Unlock()
			return nil
		},
		nil,
	)

	time.Sleep(30 * time.Millisecond)
	cancel()
	if !waitForDone(t, done, 2*time.Second) {
		t.Fatal("runTypingLoop did not exit on cancellation")
	}

	mu.Lock()
	atExit := sends
	mu.Unlock()

	time.Sleep(80 * time.Millisecond)

	mu.Lock()
	after := sends
	mu.Unlock()

	if after != atExit {
		t.Fatalf("loop kept sending after exit: %d at exit, %d after", atExit, after)
	}
}

// TestRunTypingLoopNilSendDoesNotPanic covers the degenerate caller; the loop
// is about lifetime management, so a missing send must still honour the TTL.
func TestRunTypingLoopNilSendDoesNotPanic(t *testing.T) {
	var expires atomic.Int64
	done := runTypingLoop(context.Background(), 5*time.Millisecond, 40*time.Millisecond, nil, func() {
		expires.Add(1)
	})
	if !waitForDone(t, done, 2*time.Second) {
		t.Fatal("runTypingLoop with nil send did not exit on TTL")
	}
	if expires.Load() != 1 {
		t.Fatalf("onExpire calls = %d, want 1", expires.Load())
	}
}

// TestThinkingCancelAfterTTLExpiry: the handle the channel stores must stay
// usable after the loop died on its own. Cancel() waits on doneChan; if the
// TTL path did not close it, every later sweep would block for the full 100ms
// timeout and leave a dead entry behind.
func TestThinkingCancelAfterTTLExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var expires atomic.Int64
	done := runTypingLoop(ctx, 5*time.Millisecond, 30*time.Millisecond,
		func(context.Context) error { return nil },
		func() { expires.Add(1) },
	)

	if !waitForDone(t, done, 2*time.Second) {
		t.Fatal("loop did not expire")
	}

	tc := &thinkingCancel{fn: cancel, doneChan: done}

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		tc.Cancel()
	}()

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("thinkingCancel.Cancel blocked after TTL expiry")
	}
	if ctx.Err() == nil {
		t.Fatal("onExpire must cancel the context so the dead loop's resources are released")
	}
}

// TestTypingIndicatorLifetimeConstants documents the tuning decision: the
// refresh interval must stay below Telegram's ~5s "typing" window, and the
// TTL must be long enough for legitimate multi-tool / subagent turns.
func TestTypingIndicatorLifetimeConstants(t *testing.T) {
	if typingIndicatorInterval >= 5*time.Second {
		t.Fatalf("interval %v would leave visible gaps in the typing indicator", typingIndicatorInterval)
	}
	if typingIndicatorMaxLifetime < 30*time.Minute {
		t.Fatalf("TTL %v would cut off legitimate long turns (many tool iterations, 30-min subagents)", typingIndicatorMaxLifetime)
	}
	if typingIndicatorMaxLifetime > 2*time.Hour {
		t.Fatalf("TTL %v defeats the purpose of a leak backstop", typingIndicatorMaxLifetime)
	}
}

// --- startTypingIndicator wiring (the production caller of runTypingLoop) ---

// newTelegramBotTestChannel builds a TelegramChannel with a real *telego.Bot
// whose HTTP transport is recorded, so the typing loop can be exercised
// end-to-end without touching the network.
func newTelegramBotTestChannel(t *testing.T) (*TelegramChannel, *recordingRoundTripper) {
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
		BaseChannel: NewBaseChannel("telegram", cfg.Channels.Telegram, nil, nil),
		config:      cfg,
		bot:         bot,
		deleteHTTP:  &http.Client{Transport: rt},
	}, rt
}

// TestStartTypingIndicatorLoopIsCancellable pins the refactor of
// startTypingIndicator onto runTypingLoop: the returned handle must actually
// drive the chat-action requests and, once cancelled, stop them. A loop that
// ignored cancellation would keep hitting the Bot API forever.
func TestStartTypingIndicatorLoopIsCancellable(t *testing.T) {
	ch, rt := newTelegramBotTestChannel(t)

	tc := ch.startTypingIndicator(4242)
	if tc == nil {
		t.Fatal("startTypingIndicator returned nil")
	}

	// The initial chat action is sent synchronously enough to observe.
	deadline := time.Now().Add(2 * time.Second)
	for rt.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if rt.calls() == 0 {
		t.Fatal("startTypingIndicator never sent a chat action")
	}

	// Cancel must return promptly (Cancel waits on the loop's done channel).
	done := make(chan struct{})
	go func() {
		defer close(done)
		tc.Cancel()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("thinkingCancel.Cancel blocked: the loop did not close its done channel")
	}

	atCancel := rt.calls()
	// The refresh interval is 4s; sleeping well past it proves no zombie loop
	// survived the cancel.
	time.Sleep(300 * time.Millisecond)
	if after := rt.calls(); after != atCancel {
		t.Fatalf("typing loop kept sending after Cancel: %d at cancel, %d after", atCancel, after)
	}
}

// TestStartTypingIndicatorRegistersNoGoroutineLeak runs the cancel path for
// several chats and asserts the goroutine count settles back down.
func TestStartTypingIndicatorRegistersNoGoroutineLeak(t *testing.T) {
	ch, _ := newTelegramBotTestChannel(t)

	before := runtime.NumGoroutine()

	var handles []*thinkingCancel
	for i := 0; i < 5; i++ {
		handles = append(handles, ch.startTypingIndicator(int64(1000+i)))
	}
	for _, h := range handles {
		h.Cancel()
	}

	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("typing loops leaked goroutines: before=%d after=%d", before, after)
	}
}
