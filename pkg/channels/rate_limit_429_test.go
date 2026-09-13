package channels

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mymmrac/telego/telegoapi"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
)

// Tests for SURV-07: when a channel Send returns a RateLimitError carrying an
// explicit retry-after hint, sendChunkWithRetry must wait THAT long (capped
// at maxRetryAfter) before retrying instead of the default 1s/2s backoff.
// Also covers the telego → RateLimitError mapping.

// fakeSender counts Send calls and plays back scripted errors. Embeds
// BaseChannel for the interface's Start/Stop/IsRunning/IsAllowed.
type fakeSender struct {
	*BaseChannel
	calls  int
	script []error // error for call N; nil (or exhausted) means success
}

func newFakeSender(script ...error) *fakeSender {
	return &fakeSender{
		BaseChannel: NewBaseChannel("fake", nil, nil, nil),
		script:      script,
	}
}

// TestSendChunkWithRetry_HonorsRetryAfterHint is the SURV-07 assertion: with
// a 30ms hint, the retry fires in tens of milliseconds — the default backoff
// (1s) would make this test take a second or more.
func TestSendChunkWithRetry_HonorsRetryAfterHint(t *testing.T) {
	logger.SetLevel(logger.ERROR)
	sender := newFakeSender(&RateLimitError{Channel: "test", RetryAfter: 30 * time.Millisecond})
	send := func(ctx context.Context, msg bus.OutboundMessage) error {
		if sender.calls == 0 {
			sender.calls++
			return sender.script[0]
		}
		sender.calls++
		return nil
	}

	start := time.Now()
	if err := sendChunkWithRetry(context.Background(), stubChannel{send}, bus.OutboundMessage{}); err != nil {
		t.Fatalf("sendChunkWithRetry: %v", err)
	}
	elapsed := time.Since(start)

	if sender.calls != 2 {
		t.Fatalf("send calls = %d, want 2 (one failure + one successful retry)", sender.calls)
	}
	if elapsed >= time.Second {
		t.Fatalf("retry waited %v; hint not honored (default backoff used?)", elapsed)
	}
}

// stubChannel adapts a bare Send func to the Channel interface.
type stubChannel struct {
	sendFn func(ctx context.Context, msg bus.OutboundMessage) error
}

func (s stubChannel) Name() string                    { return "stub" }
func (s stubChannel) Start(ctx context.Context) error { return nil }
func (s stubChannel) Stop(ctx context.Context) error  { return nil }
func (s stubChannel) IsRunning() bool                 { return true }
func (s stubChannel) IsAllowed(string) bool           { return true }
func (s stubChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	return s.sendFn(ctx, msg)
}

// TestRetryWaitTime_PrefersHintOverBackoff unit-tests the wait decision.
func TestRetryWaitTime_PrefersHintOverBackoff(t *testing.T) {
	cases := []struct {
		name    string
		lastErr error
		attempt int
		want    time.Duration
	}{
		{"default backoff attempt1", errors.New("timeout"), 1, 1 * time.Second},
		{"default backoff attempt2", errors.New("timeout"), 2, 2 * time.Second},
		{"hint below cap", &RateLimitError{Channel: "x", RetryAfter: 30 * time.Millisecond}, 1, 30 * time.Millisecond},
		{"hint above cap", &RateLimitError{Channel: "x", RetryAfter: time.Hour}, 1, maxRetryAfter},
		{"zero hint falls back", &RateLimitError{Channel: "x"}, 2, 2 * time.Second},
		{"non-rate-limit error", fmt.Errorf("connection refused"), 1, 1 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryWaitTime(tc.lastErr, tc.attempt); got != tc.want {
				t.Fatalf("retryWaitTime = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTelegramRateLimitError_Mapping covers the telego 429 → RateLimitError
// mapping, including passthrough of non-429 errors.
func TestTelegramRateLimitError_Mapping(t *testing.T) {
	t.Run("429 with retry_after", func(t *testing.T) {
		orig := &telegoapi.Error{ErrorCode: 429, Parameters: &telegoapi.ResponseParameters{RetryAfter: 7}}
		mapped := TelegramRateLimitError(orig)
		var rle *RateLimitError
		if !errors.As(mapped, &rle) {
			t.Fatalf("429 error not mapped to RateLimitError: %v", mapped)
		}
		if rle.Channel != "telegram" {
			t.Fatalf("channel = %q, want telegram", rle.Channel)
		}
		if rle.RetryAfter != 7*time.Second {
			t.Fatalf("retryAfter = %v, want 7s", rle.RetryAfter)
		}
	})
	t.Run("429 without parameters", func(t *testing.T) {
		mapped := TelegramRateLimitError(&telegoapi.Error{ErrorCode: 429})
		var rle *RateLimitError
		if !errors.As(mapped, &rle) {
			t.Fatalf("429 without parameters not mapped: %v", mapped)
		}
		if rle.RetryAfter != 0 {
			t.Fatalf("retryAfter = %v, want 0", rle.RetryAfter)
		}
	})
	t.Run("non-429 passthrough unchanged", func(t *testing.T) {
		orig := &telegoapi.Error{ErrorCode: 400}
		mapped := TelegramRateLimitError(orig)
		if mapped != error(orig) {
			t.Fatalf("non-429 error was transformed: %v", mapped)
		}
	})
	t.Run("plain error passthrough unchanged", func(t *testing.T) {
		orig := errors.New("some network failure")
		if mapped := TelegramRateLimitError(orig); mapped != orig {
			t.Fatalf("plain error was transformed: %v", mapped)
		}
	})
}
