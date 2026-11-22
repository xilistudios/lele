package channels

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

func waitForDone(t *testing.T, done <-chan struct{}, within time.Duration) bool {
	t.Helper()
	select {
	case <-done:
		return true
	case <-time.After(within):
		return false
	}
}

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
	if elapsed < ttl {
		t.Fatalf("loop exited after %v, before the %v TTL", elapsed, ttl)
	}
	if elapsed > ttl+2*time.Second {
		t.Fatalf("loop exited after %v, far beyond the %v TTL", elapsed, ttl)
	}
}

func TestRunTypingLoopCancelDoesNotReportExpiry(t *testing.T) {
	var sends atomic.Int64
	var expires atomic.Int64

	ctx, cancel := context.WithCancel(context.Background())
	done := runTypingLoop(ctx, 10*time.Millisecond, time.Hour,
		func(context.Context) error { sends.Add(1); return nil },
		func() { expires.Add(1) },
	)

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

func TestStartTypingIndicatorLoopIsCancellable(t *testing.T) {
	ch, rt := newTelegramBotTestChannel(t)

	tc := ch.startTypingIndicator(4242)
	if tc == nil {
		t.Fatal("startTypingIndicator returned nil")
	}

	deadline := time.Now().Add(2 * time.Second)
	for rt.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if rt.calls() == 0 {
		t.Fatal("startTypingIndicator never sent a chat action")
	}

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
	time.Sleep(300 * time.Millisecond)
	if after := rt.calls(); after != atCancel {
		t.Fatalf("typing loop kept sending after Cancel: %d at cancel, %d after", atCancel, after)
	}
}

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
