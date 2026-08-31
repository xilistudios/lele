package providers

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers/common"
)

// Default retry configuration (can be overridden via WithRetryConfig /
// WithRetryBudget for testing).
const (
	defaultMaxRetryAttempts   = 10
	defaultMaxBackoffDuration = 60 * time.Second
	// defaultRetryBaseDelay is the first rung of the exponential ladder
	// (baseDelay * 2^attempt, saturated at maxBackoff).
	defaultRetryBaseDelay = time.Second

	// defaultRetryBudget is the wall-clock budget granted to EACH candidate
	// before the chain gives up on it. A budget, not just an attempt count, is
	// what makes a provider's rate-limit window survivable: a 429 carrying
	// `Retry-After: 30` used to be retried at t=1s and t=3s, so a single
	// 60s quota window was guaranteed to kill the session (defect A).
	defaultRetryBudget = 5 * time.Minute

	// minAttemptsPerCandidate is the floor the time budget may never cut
	// below. Long server-mandated waits consume the budget quickly; aborting
	// on the first or second attempt would make the hint actively harmful.
	minAttemptsPerCandidate = 4

	// defaultMaxCooldownWait caps how long Execute() is willing to block when
	// every candidate is in cooldown. The cooldown ladder starts at 1 minute,
	// so waiting is always cheaper than returning a transient error the agent
	// will retry instantly (defect C).
	defaultMaxCooldownWait = 90 * time.Second

	// maxCooldownWaits bounds how many times Execute() blocks on an all-out
	// cooldown before giving up, so a 1h punishment cannot wedge a turn.
	maxCooldownWaits = 2

	// cooldownJitter spreads out the wake-up of concurrent chains that all
	// hit the same cooldown wall.
	cooldownJitter = 500 * time.Millisecond
)

// FallbackChain orchestrates model fallback across multiple candidates.
type FallbackChain struct {
	cooldown   *CooldownTracker
	maxRetries int
	maxBackoff time.Duration
	// totalBudget is the per-candidate wall-clock retry budget.
	totalBudget time.Duration
	// maxCooldownWait caps the all-in-cooldown wait inside Execute.
	maxCooldownWait time.Duration

	// Seams for deterministic tests (no real sleeping, no flaky timing):
	// nowFn drives the budget clock, sleepFn performs the waits, randFn feeds
	// the jitter. Production values are time.Now, realSleep and rand.Float64.
	nowFn   func() time.Time
	sleepFn func(ctx context.Context, d time.Duration) error
	randFn  func() float64
}

// FallbackCandidate represents one model/provider to try.
type FallbackCandidate struct {
	Provider string
	Model    string
}

// FallbackResult contains the successful response and metadata about all attempts.
type FallbackResult struct {
	Response *LLMResponse
	Provider string
	Model    string
	Attempts []FallbackAttempt
}

// FallbackAttempt records one attempt in the fallback chain.
type FallbackAttempt struct {
	Provider string
	Model    string
	Error    error
	Reason   FailoverReason
	Duration time.Duration
	Skipped  bool // true if skipped due to cooldown
}

// NewFallbackChain creates a new fallback chain with the given cooldown tracker.
func NewFallbackChain(cooldown *CooldownTracker) *FallbackChain {
	return &FallbackChain{
		cooldown:        cooldown,
		maxRetries:      defaultMaxRetryAttempts,
		maxBackoff:      defaultMaxBackoffDuration,
		totalBudget:     defaultRetryBudget,
		maxCooldownWait: defaultMaxCooldownWait,
		nowFn:           time.Now,
		sleepFn:         realSleep,
		randFn:          rand.Float64,
	}
}

// WithRetryConfig creates a new FallbackChain with custom retry settings (for testing).
// It keeps the default per-candidate time budget; use WithRetryBudget to change it.
func (fc *FallbackChain) WithRetryConfig(maxRetries int, maxBackoff time.Duration) *FallbackChain {
	return fc.WithRetryBudget(maxRetries, maxBackoff, fc.totalBudget)
}

// WithRetryBudget creates a new FallbackChain with an explicit retry budget:
// maxRetries attempts AND maxBackoff between them AND totalBudget of wall
// clock per candidate, whichever runs out first (never fewer than
// minAttemptsPerCandidate attempts). Zero/negative values fall back to the
// package defaults.
func (fc *FallbackChain) WithRetryBudget(maxRetries int, maxBackoff, totalBudget time.Duration) *FallbackChain {
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetryAttempts
	}
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoffDuration
	}
	if totalBudget <= 0 {
		totalBudget = defaultRetryBudget
	}
	return &FallbackChain{
		cooldown:        fc.cooldown,
		maxRetries:      maxRetries,
		maxBackoff:      maxBackoff,
		totalBudget:     totalBudget,
		maxCooldownWait: fc.maxCooldownWait,
		nowFn:           fc.nowFn,
		sleepFn:         fc.sleepFn,
		randFn:          fc.randFn,
	}
}

// WithMaxCooldownWait caps how long Execute() blocks when every candidate is
// in cooldown (0 or less keeps the default).
func (fc *FallbackChain) WithMaxCooldownWait(d time.Duration) *FallbackChain {
	if d > 0 {
		fc.maxCooldownWait = d
	}
	return fc
}

// realSleep waits d unless ctx is cancelled first. The sleep stays tied to ctx
// on purpose: decoupling it from the turn deadline is a separate change.
func realSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// computeBackoff returns how long to wait before the next attempt.
//
// The ladder is baseDelay * 2^attempt saturated at maxBackoff, then equal
// jitter (half stays fixed, half is random) so concurrent clients spread out
// without any client waiting less than half the intended delay:
//
//	delay = base/2 + rand[0, base/2]
//
// retryAfter is the server's own hint (Retry-After and friends) and acts as a
// FLOOR: when present it wins over the exponential ladder, because a provider
// that says "come back in 30s" will still be at 30s after our third attempt.
// maxBackoff bounds OUR ladder, not the server's order, so the hint is allowed
// to exceed it; its only ceiling is common.MaxRetryAfter, the trust limit
// already applied when the hint was parsed.
func computeBackoff(attempt int, baseDelay, maxBackoff time.Duration, retryAfter time.Duration, randFn func() float64) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if baseDelay <= 0 {
		baseDelay = defaultRetryBaseDelay
	}

	base := baseDelay
	for i := 0; i < attempt; i++ {
		base *= 2
		if base >= maxBackoff {
			base = maxBackoff
			break
		}
	}
	if maxBackoff > 0 && base > maxBackoff {
		base = maxBackoff
	}

	r := 0.0
	if randFn != nil {
		r = randFn()
	}
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	delay := base/2 + time.Duration(r*float64(base/2))

	if retryAfter <= 0 {
		return delay
	}
	floor := retryAfter
	if floor > common.MaxRetryAfter {
		floor = common.MaxRetryAfter
	}
	if floor > delay {
		return floor
	}
	return delay
}

// ResolveCandidates parses model config into a deduplicated candidate list.
func ResolveCandidates(cfg ModelConfig, defaultProvider string) []FallbackCandidate {
	seen := make(map[string]bool)
	var candidates []FallbackCandidate

	addCandidate := func(raw string) {
		ref := ParseModelRef(raw, defaultProvider)
		if ref == nil {
			return
		}
		key := ModelKey(ref.Provider, ref.Model)
		if seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, FallbackCandidate{
			Provider: ref.Provider,
			Model:    ref.Model,
		})
	}

	// Primary first.
	addCandidate(cfg.Primary)

	// Then fallbacks.
	for _, fb := range cfg.Fallbacks {
		addCandidate(fb)
	}

	return candidates
}

// Execute runs the fallback chain for text/chat requests.
// It tries each candidate in order, respecting cooldowns and error classification.
//
// Behavior:
//   - If EVERY candidate is in cooldown, wait (bounded by maxCooldownWait and
//     maxCooldownWaits rounds) for the earliest one to free up before failing.
//   - Candidates still in cooldown after that are skipped (logged as skipped
//     attempt).
//   - context.Canceled aborts immediately (user abort, no fallback).
//   - Non-retriable errors (format) abort immediately.
//   - Retriable errors retry per candidate within the retry budget: up to
//     maxRetries attempts and totalBudget of wall clock (whichever ends first,
//     never fewer than minAttemptsPerCandidate), backing off with exponential
//     jitter floored by the server's Retry-After hint.
//   - After a candidate's budget is exhausted, mark provider as failed and try
//     next candidate.
//   - Success marks provider as good (resets cooldown).
//   - If all fail, returns aggregate error with all attempts.
func (fc *FallbackChain) Execute(
	ctx context.Context,
	candidates []FallbackCandidate,
	run func(ctx context.Context, provider, model string) (*LLMResponse, error),
) (*FallbackResult, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("fallback: no candidates configured")
	}

	result := &FallbackResult{
		Attempts: make([]FallbackAttempt, 0, len(candidates)),
	}

	// Defect C: "every candidate is in cooldown" is not a failure, it is a
	// "wait and try again". The cooldown ladder starts at 1 minute, so
	// returning immediately made the caller's instant retry 100% wasted work
	// that burned its own retry budget in milliseconds. Block here - bounded
	// by maxCooldownWaits so a 1h punishment cannot wedge a turn - and let the
	// earliest candidate free up. Skipped attempts are recorded only by the
	// main loop below, so a successful wait leaves no phantom attempts.
	for waits := 0; ; {
		allBusy, minRemaining := fc.cooldownGate(candidates)
		if !allBusy || minRemaining <= 0 || waits >= maxCooldownWaits {
			break
		}
		wait := minRemaining + time.Duration(fc.jitter()*float64(cooldownJitter))
		if capWait := fc.cooldownWaitCap(); wait > capWait {
			wait = capWait
		}
		names := make([]string, 0, len(candidates))
		for _, c := range candidates {
			names = append(names, c.Provider)
		}
		logger.InfoCF("fallback", "all candidates in cooldown, waiting",
			map[string]interface{}{
				"waiting":   wait.Round(time.Millisecond).String(),
				"providers": strings.Join(names, ","),
				"round":     waits + 1,
			})
		if err := fc.sleep(ctx, wait); err != nil {
			return nil, err
		}
		waits++
	}

	// Check context before each attempt.
	for i, candidate := range candidates {
		if ctx.Err() == context.Canceled {
			return nil, context.Canceled
		}

		// Check cooldown.
		if !fc.cooldown.IsAvailable(candidate.Provider) {
			remaining := fc.cooldown.CooldownRemaining(candidate.Provider)
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Skipped:  true,
				Reason:   FailoverRateLimit,
				Error:    fmt.Errorf("provider %s in cooldown (%s remaining)", candidate.Provider, remaining.Round(time.Second)),
			})
			continue
		}

		// Execute the run function with retry logic.
		start := time.Now()
		resp, err := fc.executeWithRetry(ctx, candidate.Provider, candidate.Model, run)
		elapsed := time.Since(start)

		if err == nil {
			// Success.
			fc.cooldown.MarkSuccess(candidate.Provider)
			result.Response = resp
			result.Provider = candidate.Provider
			result.Model = candidate.Model
			return result, nil
		}

		// Context cancellation: abort immediately, no fallback.
		if ctx.Err() == context.Canceled {
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Error:    err,
				Duration: elapsed,
			})
			return nil, context.Canceled
		}

		// Classify the error.
		failErr := ClassifyError(err, candidate.Provider, candidate.Model)

		// Guard kept for symmetry only: after default-to-transient, ClassifyError
		// returns nil exclusively for context cancellation, which is already
		// handled by the ctx.Err() checks above. So this branch is effectively
		// dead but harmless.
		if failErr == nil {
			// Unclassifiable error: do not fallback, return immediately.
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Error:    err,
				Duration: elapsed,
			})
			return nil, fmt.Errorf("fallback: unclassified error from %s/%s: %w",
				candidate.Provider, candidate.Model, err)
		}

		// Non-retriable error: abort immediately.
		if !failErr.IsRetriable() {
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Error:    failErr,
				Reason:   failErr.Reason,
				Duration: elapsed,
			})
			return nil, failErr
		}

		// Retriable error: mark failure and continue to next candidate.
		// Only apply cooldown when there are fallback candidates to switch to.
		// With a single candidate (no fallbacks) a cooldown would just block
		// the agent from retrying the same provider on the next turn.
		if len(candidates) > 1 {
			fc.cooldown.MarkFailure(candidate.Provider, failErr.Reason)
		}
		result.Attempts = append(result.Attempts, FallbackAttempt{
			Provider: candidate.Provider,
			Model:    candidate.Model,
			Error:    failErr,
			Reason:   failErr.Reason,
			Duration: elapsed,
		})

		// If this was the last candidate, return aggregate error.
		if i == len(candidates)-1 {
			return nil, &FallbackExhaustedError{Attempts: result.Attempts}
		}
	}

	// All candidates were skipped (all in cooldown).
	return nil, &FallbackExhaustedError{Attempts: result.Attempts}
}

// executeWithRetry executes the LLM call with retry logic.
//
// The budget is three-dimensional and the loop stops when ANY of them runs
// out: maxRetries attempts, totalBudget of wall clock for this candidate, or a
// non-retriable error. It never stops before minAttemptsPerCandidate attempts,
// otherwise a single long server-mandated wait could eat the whole budget and
// make the chain give up on a provider that was about to recover.
//
// Backoff is exponential with equal jitter, floored by the server's Retry-After
// hint when the error carries one.
func (fc *FallbackChain) executeWithRetry(
	ctx context.Context,
	provider string,
	model string,
	run func(ctx context.Context, provider, model string) (*LLMResponse, error),
) (*LLMResponse, error) {
	var lastErr error

	started := fc.now()
	deadline := started.Add(fc.retryBudget())
	attempts := 0

	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		resp, err := run(ctx, provider, model)
		if err == nil {
			return resp, nil
		}
		attempts++
		lastErr = err

		failErr := ClassifyError(err, provider, model)
		if failErr == nil || !failErr.IsRetriable() {
			return nil, err
		}

		// Budget check happens after recording the attempt. Two independent
		// ceilings: the explicit attempt count (caller-configured, honoured
		// verbatim so tests and single-shot callers keep their semantics) and
		// the wall-clock budget, which may only stop the loop once the
		// candidate has been given minAttemptsPerCandidate real tries - a
		// single long server-mandated wait must never make us abandon a
		// provider that was about to recover.
		budgetLeft := deadline.Sub(fc.now())
		if attempts >= fc.maxRetries {
			break // retry count exhausted
		}
		if attempts >= minAttemptsPerCandidate && budgetLeft <= 0 {
			logger.WarnCF("fallback", "backoff: retry budget exhausted",
				map[string]interface{}{
					"provider":   provider,
					"model":      model,
					"attempts":   attempts,
					"budget":     fc.retryBudget().String(),
					"last_error": lastErr.Error(),
				})
			break
		}

		if !failErr.ShouldBackoff() {
			// Retriable but no backoff policy (auth/billing for this
			// provider): retrying the same request is pointless, hand the
			// chain a chance to switch candidates.
			return nil, lastErr
		}

		wait := computeBackoff(attempt, defaultRetryBaseDelay, fc.maxBackoff, failErr.RetryAfter, fc.jitter)
		if wait > budgetLeft && attempts >= minAttemptsPerCandidate {
			wait = budgetLeft
		}
		if wait < 0 {
			wait = 0
		}

		logFields := map[string]interface{}{
			"provider": provider,
			"model":    model,
			"attempt":  attempt + 1,
			"waiting":  wait.Round(time.Millisecond).String(),
			"reason":   string(failErr.Reason),
			"error":    lastErr.Error(),
		}
		if failErr.RetryAfter > 0 && wait >= failErr.RetryAfter {
			// Worth a distinct line: this wait is the provider's order, not
			// our ladder, and it is the reason the budget is wall-clock.
			logFields["retry_after"] = failErr.RetryAfter.Round(time.Second).String()
			logger.InfoCF("fallback", "backoff: honoring server Retry-After", logFields)
		} else {
			logger.InfoCF("fallback", "backoff: retry attempt", logFields)
		}

		if serr := fc.sleep(ctx, wait); serr != nil {
			return nil, serr
		}
	}

	logger.WarnCF("fallback", "backoff: retries exhausted",
		map[string]interface{}{
			"provider":   provider,
			"model":      model,
			"attempts":   attempts,
			"last_error": lastErr.Error(),
		})

	return nil, lastErr
}

// cooldownGate reports whether every candidate is currently unavailable and,
// if so, how long until the FIRST one frees up. When at least one candidate is
// available there is nothing to wait for, so minRemaining is 0.
func (fc *FallbackChain) cooldownGate(candidates []FallbackCandidate) (allBusy bool, minRemaining time.Duration) {
	for _, c := range candidates {
		if fc.cooldown.IsAvailable(c.Provider) {
			return false, 0
		}
	}
	for _, c := range candidates {
		r := fc.cooldown.CooldownRemaining(c.Provider)
		if r > 0 && (minRemaining == 0 || r < minRemaining) {
			minRemaining = r
		}
	}
	return true, minRemaining
}

// cooldownWaitCap returns the maximum blocking wait for an all-cooldown chain.
func (fc *FallbackChain) cooldownWaitCap() time.Duration {
	if fc.maxCooldownWait > 0 {
		return fc.maxCooldownWait
	}
	return defaultMaxCooldownWait
}

// retryBudget returns the per-candidate wall-clock budget (default when unset).
func (fc *FallbackChain) retryBudget() time.Duration {
	if fc.totalBudget > 0 {
		return fc.totalBudget
	}
	return defaultRetryBudget
}

// --- test seams: production uses the real clock/sleep/RNG -------------------

func (fc *FallbackChain) now() time.Time {
	if fc.nowFn != nil {
		return fc.nowFn()
	}
	return time.Now()
}

func (fc *FallbackChain) sleep(ctx context.Context, d time.Duration) error {
	if fc.sleepFn != nil {
		return fc.sleepFn(ctx, d)
	}
	return realSleep(ctx, d)
}

func (fc *FallbackChain) jitter() float64 {
	if fc.randFn != nil {
		return fc.randFn()
	}
	return rand.Float64()
}

// ExecuteImage runs the fallback chain for image/vision requests.
// Simpler than Execute: no cooldown checks (image endpoints have different rate limits).
// Image dimension/size errors abort immediately (non-retriable).
func (fc *FallbackChain) ExecuteImage(
	ctx context.Context,
	candidates []FallbackCandidate,
	run func(ctx context.Context, provider, model string) (*LLMResponse, error),
) (*FallbackResult, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("image fallback: no candidates configured")
	}

	result := &FallbackResult{
		Attempts: make([]FallbackAttempt, 0, len(candidates)),
	}

	for i, candidate := range candidates {
		if ctx.Err() == context.Canceled {
			return nil, context.Canceled
		}

		start := time.Now()
		resp, err := run(ctx, candidate.Provider, candidate.Model)
		elapsed := time.Since(start)

		if err == nil {
			result.Response = resp
			result.Provider = candidate.Provider
			result.Model = candidate.Model
			return result, nil
		}

		if ctx.Err() == context.Canceled {
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Error:    err,
				Duration: elapsed,
			})
			return nil, context.Canceled
		}

		// Image dimension/size errors are non-retriable.
		errMsg := strings.ToLower(err.Error())
		if IsImageDimensionError(errMsg) || IsImageSizeError(errMsg) {
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Error:    err,
				Reason:   FailoverFormat,
				Duration: elapsed,
			})
			return nil, &FailoverError{
				Reason:   FailoverFormat,
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Wrapped:  err,
			}
		}

		// Any other error: record and try next.
		result.Attempts = append(result.Attempts, FallbackAttempt{
			Provider: candidate.Provider,
			Model:    candidate.Model,
			Error:    err,
			Duration: elapsed,
		})

		if i == len(candidates)-1 {
			return nil, &FallbackExhaustedError{Attempts: result.Attempts}
		}
	}

	return nil, &FallbackExhaustedError{Attempts: result.Attempts}
}

// FallbackExhaustedError indicates all fallback candidates were tried and failed.
type FallbackExhaustedError struct {
	Attempts []FallbackAttempt
}

func (e *FallbackExhaustedError) Error() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("fallback: all %d candidates failed:", len(e.Attempts)))
	for i, a := range e.Attempts {
		if a.Skipped {
			sb.WriteString(fmt.Sprintf("\n  [%d] %s/%s: skipped (cooldown)", i+1, a.Provider, a.Model))
		} else {
			sb.WriteString(fmt.Sprintf("\n  [%d] %s/%s: %v (reason=%s, %s)",
				i+1, a.Provider, a.Model, a.Error, a.Reason, a.Duration.Round(time.Millisecond)))
		}
	}
	return sb.String()
}

// IsRetriableError reports whether a (possibly wrapped) error returned by the
// fallback chain represents a transient failure worth retrying.
//
// It is default-to-transient: only an explicit blacklist of terminal cases
// returns false. A whitelist ("retry only timeout/rate_limit/overloaded") was
// the original bug - every transport error that was not enumerated (unexpected
// EOF, connection reset, no such host, malformed HTTP response, SDK stream
// errors, ...) fell through to false and killed the agent's turn even though it
// was fully recoverable. With a blacklist, adding a new provider or SDK can
// never reintroduce the bug: an unrecognised failure is retried by default.
func IsRetriableError(err error) bool {
	if err == nil {
		return false
	}
	// User abort (/stop) or cancelled run: terminal, never retried.
	if isCancellation(err) {
		return false
	}
	var exhausted *FallbackExhaustedError
	if errors.As(err, &exhausted) {
		// Retriable if any attempt was retriable OR merely skipped: a chain
		// where every candidate is in cooldown is not a failure, it is a
		// "wait and try again" - the cooldown will expire. Only a chain whose
		// attempts are all terminal (format) is a genuine dead end.
		for _, a := range exhausted.Attempts {
			if a.Skipped {
				return true
			}
			// An empty reason means the attempt was never classified (the image
			// chain records raw errors); default-to-transient applies.
			if a.Reason == "" || !isTerminalReason(a.Reason) {
				return true
			}
		}
		return false
	}
	var fe *FailoverError
	if errors.As(err, &fe) {
		return fe.IsRetriable()
	}
	// Anything else (raw *url.Error, SDK errors, plain errors): ask the
	// classifier and reject only the terminal reasons. ClassifyError now
	// defaults unknown errors to FailoverUnknown, so the retriable branch is
	// the fall-through rather than a list of known-good reasons.
	if classified := ClassifyError(err, "", ""); classified != nil && classified.IsTerminal() {
		return false
	}
	return true
}
