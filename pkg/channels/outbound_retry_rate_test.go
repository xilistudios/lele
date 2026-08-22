package channels

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"timeout", errors.New("operation timeout"), true},
		{"deadline", errors.New("context deadline exceeded"), true},
		{"connection refused", errors.New("dial tcp: connection refused"), true},
		{"temporary", errors.New("temporary failure"), true},
		{"rate limit", errors.New("rate limit exceeded"), true},
		{"429", errors.New("status 429"), true},
		{"503", errors.New("status 503"), true},
		{"502", errors.New("status 502"), true},
		{"network", errors.New("network is unreachable"), true},
		{"i/o timeout", errors.New("i/o timeout"), true},
		{"permission denied", errors.New("permission denied"), false},
		{"not found", errors.New("404 not found"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientError(tt.err); got != tt.want {
				t.Errorf("isTransientError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// flakyChannel fails with a transient error on the first N attempts then succeeds.
type flakyChannel struct {
	failBefore int
	count      atomic.Int32
}

func (c *flakyChannel) Name() string                    { return "flaky" }
func (c *flakyChannel) Start(ctx context.Context) error { return nil }
func (c *flakyChannel) Stop(ctx context.Context) error  { return nil }
func (c *flakyChannel) IsRunning() bool                 { return true }
func (c *flakyChannel) IsAllowed(string) bool           { return true }

func (c *flakyChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if int(c.count.Add(1)) <= c.failBefore {
		return errors.New("temporary network error")
	}
	return nil
}

func TestSendChunkWithRetry_TransientThenSucceeds(t *testing.T) {
	ch := &flakyChannel{failBefore: 2}
	err := sendChunkWithRetry(context.Background(), ch, bus.OutboundMessage{ChatID: "c"})
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if ch.count.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", ch.count.Load())
	}
}

func TestSendChunkWithRetry_NonTransientFailsImmediately(t *testing.T) {
	ch := &failingChannel{err: errors.New("permission denied")}
	err := sendChunkWithRetry(context.Background(), ch, bus.OutboundMessage{ChatID: "c"})
	if err == nil {
		t.Fatal("expected error for non-transient failure")
	}
	if ch.count.Load() != 1 {
		t.Errorf("expected 1 attempt for non-transient error, got %d", ch.count.Load())
	}
}

func TestSendChunkWithRetry_TransientExhaustsRetries(t *testing.T) {
	ch := &flakyChannel{failBefore: 100}
	err := sendChunkWithRetry(context.Background(), ch, bus.OutboundMessage{ChatID: "c"})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if ch.count.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", ch.count.Load())
	}
}

type failingChannel struct {
	count atomic.Int32
	err   error
}

func (c *failingChannel) Name() string                    { return "failing" }
func (c *failingChannel) Start(ctx context.Context) error { return nil }
func (c *failingChannel) Stop(ctx context.Context) error  { return nil }
func (c *failingChannel) IsRunning() bool                 { return true }
func (c *failingChannel) IsAllowed(string) bool           { return true }
func (c *failingChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	c.count.Add(1)
	return c.err
}

func TestSendChunkWithRetry_ContextCancelledDuringBackoff(t *testing.T) {
	ch := &failingChannel{err: errors.New("temporary")}
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel quickly so a retry is aborted.
	go func() {
		// The first attempt happens immediately; cancel before the backoff completes.
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := sendChunkWithRetry(ctx, ch, bus.OutboundMessage{ChatID: "c"})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Errorf("retry did not abort promptly on context cancel: %v", time.Since(start))
	}
}

func TestRateLimiter_allowWindowExpiry(t *testing.T) {
	rl := newRateLimiter(2, 40*time.Millisecond)
	defer rl.Stop()

	if !rl.allow("k1") {
		t.Fatal("first allow should pass")
	}
	if !rl.allow("k1") {
		t.Fatal("second allow within window should pass (rate=2)")
	}
	if rl.allow("k1") {
		t.Fatal("third allow should be rejected (rate=2)")
	}
	// Different key is independent.
	if !rl.allow("k2") {
		t.Fatal("different key should be allowed")
	}

	// Wait for the window to expire, then allow again.
	time.Sleep(60 * time.Millisecond)
	if !rl.allow("k1") {
		t.Fatal("allow should pass after window expiry")
	}
}

func TestRateLimiter_cleanup(t *testing.T) {
	rl := newRateLimiter(1, 30*time.Millisecond)
	defer rl.Stop()
	rl.cleanupInterval = 20 * time.Millisecond

	rl.allow("expirable")
	time.Sleep(60 * time.Millisecond)
	rl.mu.Lock()
	_, exists := rl.entries["expirable"]
	rl.mu.Unlock()
	if exists {
		t.Error("cleanup should have removed expired entries")
	}
}

func TestRateLimiter_StopIdempotent(t *testing.T) {
	rl := newRateLimiter(5, time.Minute)
	rl.Stop()
	rl.Stop() // must not panic
}

func TestRateLimitMiddleware_RejectsWhenLimited(t *testing.T) {
	limiter := newRateLimiter(1, time.Minute)
	defer limiter.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := (&NativeChannel{}).rateLimitMiddleware(limiter, handler)

	first := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "1.2.3.4:1234"
	mw.ServeHTTP(first, req1)
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "1.2.3.4:1234"
	mw.ServeHTTP(second, req2)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", second.Code)
	}
}
