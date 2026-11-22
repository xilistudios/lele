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

const (
	defaultMaxRetryAttempts   = 10
	defaultMaxBackoffDuration = 60 * time.Second
	defaultRetryBaseDelay     = time.Second

	defaultRetryBudget = 5 * time.Minute

	minAttemptsPerCandidate = 4

	defaultMaxCooldownWait = 90 * time.Second

	maxCooldownWaits = 2

	cooldownJitter = 500 * time.Millisecond
)

type FallbackChain struct {
	cooldown        *CooldownTracker
	maxRetries      int
	maxBackoff      time.Duration
	totalBudget     time.Duration
	maxCooldownWait time.Duration

	nowFn   func() time.Time
	sleepFn func(ctx context.Context, d time.Duration) error
	randFn  func() float64
}

type FallbackCandidate struct {
	Provider string
	Model    string
}

type FallbackResult struct {
	Response *LLMResponse
	Provider string
	Model    string
	Attempts []FallbackAttempt
}

type FallbackAttempt struct {
	Provider string
	Model    string
	Error    error
	Reason   FailoverReason
	Duration time.Duration
	Skipped  bool
}

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

func (fc *FallbackChain) WithRetryConfig(maxRetries int, maxBackoff time.Duration) *FallbackChain {
	return fc.WithRetryBudget(maxRetries, maxBackoff, fc.totalBudget)
}

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

func (fc *FallbackChain) WithMaxCooldownWait(d time.Duration) *FallbackChain {
	if d > 0 {
		fc.maxCooldownWait = d
	}
	return fc
}

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

	addCandidate(cfg.Primary)

	for _, fb := range cfg.Fallbacks {
		addCandidate(fb)
	}

	return candidates
}

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

	for i, candidate := range candidates {
		if ctx.Err() == context.Canceled {
			return nil, context.Canceled
		}

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

		start := time.Now()
		resp, err := fc.executeWithRetry(ctx, candidate.Provider, candidate.Model, run)
		elapsed := time.Since(start)

		if err == nil {
			fc.cooldown.MarkSuccess(candidate.Provider)
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

		failErr := ClassifyError(err, candidate.Provider, candidate.Model)

		if failErr == nil {
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Error:    err,
				Duration: elapsed,
			})
			return nil, fmt.Errorf("fallback: unclassified error from %s/%s: %w",
				candidate.Provider, candidate.Model, err)
		}

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

		if i == len(candidates)-1 {
			return nil, &FallbackExhaustedError{Attempts: result.Attempts}
		}
	}

	return nil, &FallbackExhaustedError{Attempts: result.Attempts}
}

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

		budgetLeft := deadline.Sub(fc.now())
		if attempts >= fc.maxRetries {
			break
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
			return nil, lastErr
		}

		jitterDraw := fc.jitter()
		fixedRand := func() float64 { return jitterDraw }
		ladder := computeBackoff(attempt, defaultRetryBaseDelay, fc.maxBackoff, 0, fixedRand)
		wait := computeBackoff(attempt, defaultRetryBaseDelay, fc.maxBackoff, failErr.RetryAfter, fixedRand)
		honoringHint := failErr.RetryAfter > 0 && wait > ladder
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
		if honoringHint {
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

func (fc *FallbackChain) cooldownWaitCap() time.Duration {
	if fc.maxCooldownWait > 0 {
		return fc.maxCooldownWait
	}
	return defaultMaxCooldownWait
}

func (fc *FallbackChain) retryBudget() time.Duration {
	if fc.totalBudget > 0 {
		return fc.totalBudget
	}
	return defaultRetryBudget
}

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

func IsRetriableError(err error) bool {
	if err == nil {
		return false
	}
	if isCancellation(err) {
		return false
	}
	var exhausted *FallbackExhaustedError
	if errors.As(err, &exhausted) {
		for _, a := range exhausted.Attempts {
			if a.Skipped {
				return true
			}
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
	if classified := ClassifyError(err, "", ""); classified != nil && classified.IsTerminal() {
		return false
	}
	return true
}
