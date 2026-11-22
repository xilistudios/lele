package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers/common"
)

func fixedRand(v float64) func() float64 {
	return func() float64 { return v }
}

func TestComputeBackoff_ExponentialGrowth(t *testing.T) {
	randFn := fixedRand(1)
	steps := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	for attempt, want := range steps {
		got := computeBackoff(attempt, time.Second, time.Minute, 0, randFn)
		if got != want {
			t.Errorf("attempt %d: computeBackoff = %v, want %v", attempt, got, want)
		}
	}
}

func TestComputeBackoff_SaturatesAtMaxBackoff(t *testing.T) {
	randFn := fixedRand(1)
	for _, attempt := range []int{6, 10, 20, 63, 1000} {
		got := computeBackoff(attempt, time.Second, 60*time.Second, 0, randFn)
		if got != 60*time.Second {
			t.Errorf("attempt %d: computeBackoff = %v, want 60s (saturated, no overflow)", attempt, got)
		}
	}
}

func TestComputeBackoff_JitterWithinEqualBand(t *testing.T) {
	for _, attempt := range []int{0, 1, 2, 3, 7} {
		for _, r := range []float64{0, 0.25, 0.5, 0.75, 1} {
			base := time.Duration(1<<uint(attempt)) * time.Second
			if base > 60*time.Second {
				base = 60 * time.Second
			}
			got := computeBackoff(attempt, time.Second, 60*time.Second, 0, fixedRand(r))
			if got < base/2 || got > base {
				t.Errorf("attempt %d r=%v: computeBackoff = %v, want in [%v, %v]",
					attempt, r, got, base/2, base)
			}
			want := base/2 + time.Duration(r*float64(base/2))
			if got != want {
				t.Errorf("attempt %d r=%v: computeBackoff = %v, want %v", attempt, r, got, want)
			}
		}
	}
}

func TestComputeBackoff_RetryAfterIsFloor(t *testing.T) {
	got := computeBackoff(0, time.Second, 60*time.Second, 30*time.Second, fixedRand(1))
	if got < 30*time.Second {
		t.Errorf("computeBackoff(attempt 0, retryAfter 30s) = %v, want >= 30s", got)
	}

	got = computeBackoff(6, time.Second, 60*time.Second, 90*time.Second, fixedRand(1))
	if got != 90*time.Second {
		t.Errorf("computeBackoff(retryAfter 90s) = %v, want 90s", got)
	}

	got = computeBackoff(0, time.Second, 5*time.Second, 30*time.Second, fixedRand(1))
	if got != 30*time.Second {
		t.Errorf("computeBackoff(maxBackoff 5s, retryAfter 30s) = %v, want 30s", got)
	}

	got = computeBackoff(5, time.Second, 60*time.Second, 10*time.Second, fixedRand(1))
	if got != 32*time.Second {
		t.Errorf("computeBackoff(attempt 5, retryAfter 10s) = %v, want 32s", got)
	}
}

func TestComputeBackoff_RetryAfterClampedToTrustCeiling(t *testing.T) {
	got := computeBackoff(0, time.Second, 60*time.Second, 10*time.Minute, fixedRand(1))
	if got != common.MaxRetryAfter {
		t.Errorf("computeBackoff(retryAfter 10m) = %v, want %v (clamped)", got, common.MaxRetryAfter)
	}

	got = computeBackoff(1, time.Second, 60*time.Second, -time.Minute, fixedRand(1))
	if got < time.Second || got > 2*time.Second {
		t.Errorf("computeBackoff(negative hint) = %v, want the plain ladder [1s,2s]", got)
	}
}

func TestComputeBackoff_NilRandFnIsSafe(t *testing.T) {
	got := computeBackoff(2, time.Second, 60*time.Second, 0, nil)
	if got != 2*time.Second {
		t.Errorf("computeBackoff(nil randFn) = %v, want 2s", got)
	}
}

func newFakeTimingChain(maxRetries int, maxBackoff, budget time.Duration) (*FallbackChain, *time.Time, *[]time.Duration) {
	fc := NewFallbackChain(NewCooldownTracker()).WithRetryBudget(maxRetries, maxBackoff, budget)
	clock := time.Time{}
	var waits []time.Duration
	fc.nowFn = func() time.Time { return clock }
	fc.randFn = fixedRand(1)
	fc.sleepFn = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		clock = clock.Add(d)
		return nil
	}
	return fc, &clock, &waits
}

func TestExecuteWithRetry_BudgetNeverCutsBelowMinAttempts(t *testing.T) {
	fc, _, waits := newFakeTimingChain(10, time.Millisecond, time.Millisecond)

	calls := 0
	_, err := fc.executeWithRetry(context.Background(), "openai", "gpt-4",
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			calls++
			return nil, &FailoverError{Reason: FailoverRateLimit, Status: 429,
				Wrapped: errors.New("rate limit exceeded")}
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls < minAttemptsPerCandidate {
		t.Errorf("calls = %d, want >= %d (min attempts floor)", calls, minAttemptsPerCandidate)
	}
	if len(*waits) != calls-1 {
		t.Errorf("waits = %d, want %d (one sleep per retry)", len(*waits), calls-1)
	}
}

func TestExecuteWithRetry_BudgetStopsAfterMinAttempts(t *testing.T) {
	fc, _, _ := newFakeTimingChain(50, time.Millisecond, 3*time.Millisecond)

	calls := 0
	_, err := fc.executeWithRetry(context.Background(), "openai", "gpt-4",
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			calls++
			return nil, errors.New("rate limit exceeded")
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 4 {
		t.Errorf("calls = %d, want 4 (budget exhausted right at the floor)", calls)
	}
}

func TestExecuteWithRetry_HonorsMaxRetries(t *testing.T) {
	fc, _, _ := newFakeTimingChain(2, time.Millisecond, time.Hour)

	calls := 0
	_, err := fc.executeWithRetry(context.Background(), "openai", "gpt-4",
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			calls++
			return nil, errors.New("rate limit exceeded")
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (maxRetries honoured)", calls)
	}
}

func TestExecuteWithRetry_LongRetryAfterDoesNotStarveAttempts(t *testing.T) {
	fc, clock, waits := newFakeTimingChain(10, 60*time.Second, 2*time.Minute)

	calls := 0
	_, err := fc.executeWithRetry(context.Background(), "openai", "gpt-4",
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			calls++
			return nil, &common.APIError{StatusCode: 429, RetryAfter: 30 * time.Second, Body: "slow down"}
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls < minAttemptsPerCandidate {
		t.Errorf("calls = %d, want >= %d despite 30s server waits", calls, minAttemptsPerCandidate)
	}
	for i, w := range *waits {
		if w < 30*time.Second {
			t.Errorf("wait[%d] = %v, want >= 30s (Retry-After floor)", i, w)
		}
	}
	if elapsed := clock.Sub(time.Time{}); elapsed < time.Minute {
		t.Errorf("total elapsed = %v, want >= 1m (a 60s quota window must be survivable)", elapsed)
	}
}

func TestExecuteWithRetry_CancellationStillAbortsTheSleep(t *testing.T) {
	fc, _, _ := newFakeTimingChain(10, time.Second, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	fc.sleepFn = func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}

	_, err := fc.executeWithRetry(ctx, "openai", "gpt-4",
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			return nil, errors.New("rate limit exceeded")
		})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestWithRetryBudget_DefaultsAreSane(t *testing.T) {
	fc := NewFallbackChain(NewCooldownTracker())
	if fc.retryBudget() != defaultRetryBudget {
		t.Errorf("default budget = %v, want %v", fc.retryBudget(), defaultRetryBudget)
	}
	if fc.cooldownWaitCap() != defaultMaxCooldownWait {
		t.Errorf("default cooldown wait cap = %v, want %v", fc.cooldownWaitCap(), defaultMaxCooldownWait)
	}
	if fc.maxRetries != defaultMaxRetryAttempts || fc.maxBackoff != defaultMaxBackoffDuration {
		t.Errorf("defaults changed: maxRetries=%d maxBackoff=%v", fc.maxRetries, fc.maxBackoff)
	}

	custom := fc.WithRetryConfig(3, 5*time.Second)
	if custom.maxRetries != 3 || custom.maxBackoff != 5*time.Second {
		t.Errorf("WithRetryConfig lost its values: %+v", custom)
	}
	if custom.retryBudget() != defaultRetryBudget {
		t.Errorf("WithRetryConfig changed the budget to %v", custom.retryBudget())
	}
	if custom.cooldownWaitCap() != defaultMaxCooldownWait {
		t.Errorf("WithRetryConfig lost the cooldown wait cap: %v", custom.cooldownWaitCap())
	}

	weird := fc.WithRetryBudget(0, -time.Second, 0)
	if weird.maxRetries != defaultMaxRetryAttempts || weird.maxBackoff != defaultMaxBackoffDuration ||
		weird.retryBudget() != defaultRetryBudget {
		t.Errorf("WithRetryBudget did not fall back to defaults: %+v", weird)
	}
}

func TestCooldownGate(t *testing.T) {
	now := time.Now()
	ct, _ := newTestTracker(now)
	fc := NewFallbackChain(ct)
	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	if busy, rem := fc.cooldownGate(candidates); busy || rem != 0 {
		t.Errorf("fresh chain: busy=%v rem=%v, want false/0", busy, rem)
	}

	ct.MarkFailure("openai", FailoverRateLimit)
	if busy, rem := fc.cooldownGate(candidates); busy || rem != 0 {
		t.Errorf("one busy: busy=%v rem=%v, want false/0", busy, rem)
	}

	ct.MarkFailure("anthropic", FailoverRateLimit)
	ct.MarkFailure("anthropic", FailoverRateLimit)
	busy, rem := fc.cooldownGate(candidates)
	if !busy {
		t.Fatal("expected allBusy")
	}
	if rem != time.Minute {
		t.Errorf("minRemaining = %v, want 1m (earliest candidate)", rem)
	}
}
