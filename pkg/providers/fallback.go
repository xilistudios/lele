package providers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// Default retry configuration (can be overridden via WithRetryConfig for testing)
const (
	defaultMaxRetryAttempts   = 10
	defaultMaxBackoffDuration = 60 * time.Second
	immediateRetryAttempts    = 5
)

// FallbackChain orchestrates model fallback across multiple candidates.
type FallbackChain struct {
	cooldown   *CooldownTracker
	maxRetries int
	maxBackoff time.Duration
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
		cooldown:   cooldown,
		maxRetries: defaultMaxRetryAttempts,
		maxBackoff: defaultMaxBackoffDuration,
	}
}

// WithRetryConfig creates a new FallbackChain with custom retry settings (for testing).
func (fc *FallbackChain) WithRetryConfig(maxRetries int, maxBackoff time.Duration) *FallbackChain {
	return &FallbackChain{
		cooldown:   fc.cooldown,
		maxRetries: maxRetries,
		maxBackoff: maxBackoff,
	}
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
//   - Candidates in cooldown are skipped (logged as skipped attempt).
//   - context.Canceled aborts immediately (user abort, no fallback).
//   - Non-retriable errors (format) abort immediately.
//   - Retriable errors trigger retry with exponential backoff (up to 10 retries, max 60s).
//   - After all retries exhausted, mark provider as failed and try next candidate.
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

	for i, candidate := range candidates {
		// Check context before each attempt.
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
		fc.cooldown.MarkFailure(candidate.Provider, failErr.Reason)
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
// - Rate limit errors: exponential backoff retry (up to maxRetries).
// - Other retriable errors: immediate retry (up to immediateRetryAttempts, no backoff).
// After all retries are exhausted, returns the last error.
func (fc *FallbackChain) executeWithRetry(
	ctx context.Context,
	provider string,
	model string,
	run func(ctx context.Context, provider, model string) (*LLMResponse, error),
) (*LLMResponse, error) {
	var lastErr error

	for attempt := 0; attempt < fc.maxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		resp, err := run(ctx, provider, model)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		failErr := ClassifyError(err, provider, model)
		if failErr == nil || !failErr.IsRetriable() {
			return nil, err
		}

		if failErr.ShouldBackoff() {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			if backoff > fc.maxBackoff {
				backoff = fc.maxBackoff
			}

			log.Printf("[INFO] backoff: retry attempt for provider=%s/model=%s, attempt=%d, waiting=%s, reason=%s, error=%v",
				provider, model, attempt+1, backoff.Round(time.Second), failErr.Reason, lastErr)

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else {
			if attempt >= immediateRetryAttempts {
				log.Printf("[WARN] immediate retries exhausted for provider=%s/model=%s after %d attempts, reason=%s, last_error=%v",
					provider, model, attempt+1, failErr.Reason, lastErr)
				return nil, lastErr
			}
			log.Printf("[INFO] immediate retry for provider=%s/model=%s, attempt=%d, reason=%s, error=%v",
				provider, model, attempt+1, failErr.Reason, lastErr)
		}
	}

	log.Printf("[WARN] backoff: retries exhausted for provider=%s/model=%s after %d attempts, last_error=%v",
		provider, model, fc.maxRetries, lastErr)

	return nil, lastErr
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
