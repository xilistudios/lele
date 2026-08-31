package tools

import (
	"context"
	"math"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

// RetryConfig configures the retry behavior for LLM calls.
type RetryConfig struct {
	MaxRetries int           // Maximum number of retry attempts (0 = no retry)
	BaseDelay  time.Duration // Initial delay before first retry
	MaxDelay   time.Duration // Maximum delay between retries
	Multiplier float64       // Backoff multiplier (e.g., 2.0 for exponential)
	RetryOnAll bool          // If true, retry on all errors (not just timeouts/retryable)
}

// DefaultRetryConfig returns sensible defaults for retry behavior.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		BaseDelay:  5 * time.Second,
		MaxDelay:   60 * time.Second,
		Multiplier: 2.0,
		RetryOnAll: false,
	}
}

// isRetryableError determines if an error is worth retrying.
//
// It delegates to providers.IsRetriableError, the single source of truth for
// "was this LLM failure transient?". The parent agent loop (pkg/agent) already
// asks that predicate; this function used to carry its own whitelist of ~20
// substrings over err.Error(), so the SAME error could be transient for the
// parent and fatal for a subagent. A whitelist is also structurally wrong: any
// transport failure it did not enumerate (unexpected EOF variants, TLS
// handshake timeout, "no such host", SDK stream errors, ...) fell through to
// false and killed the session, even though it is fully recoverable.
//
// providers.IsRetriableError is default-to-transient with an explicit terminal
// blacklist (cancellation, format/auth/billing-only chains), so adding a new
// provider can never reintroduce that bug.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	return providers.IsRetriableError(err)
}

// retrySleep is the seam used for every backoff wait in this package (the
// per-call retry loop below and the subagent task retry in subagent_runner.go).
// It exists only so tests can run retry paths without real wall-clock sleeps;
// production always uses time.After, which the callers already select on
// alongside ctx.Done(), so cancellation behaviour is unchanged.
var retrySleep = time.After

// calculateDelay computes the delay for a given retry attempt using exponential backoff.
func calculateDelay(config RetryConfig, attempt int) time.Duration {
	delay := float64(config.BaseDelay) * math.Pow(config.Multiplier, float64(attempt))
	if delay > float64(config.MaxDelay) {
		delay = float64(config.MaxDelay)
	}
	return time.Duration(delay)
}

// ChatWithRetry wraps a provider's Chat call with retry logic.
// It retries only errors that providers.IsRetriableError considers transient
// (see isRetryableError), unless RetryOnAll is set.
func ChatWithRetry(
	ctx context.Context,
	provider providers.LLMProvider,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
	config RetryConfig,
) (*providers.LLMResponse, error) {
	var lastErr error

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		// Check if context is done before attempting
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Prefer the streaming transport so long-reasoning calls are bounded by
		// an idle timeout instead of a fixed total-duration cap.
		response, err := providers.ChatIdle(ctx, provider, messages, tools, model, options)
		if err == nil {
			// Success - log if we retried
			if attempt > 0 {
				logger.InfoCF("retry", "LLM call succeeded after retry",
					map[string]any{
						"attempt": attempt,
						"model":   model,
					})
			}
			return response, nil
		}

		lastErr = err

		// Check if this error is retryable
		if !config.RetryOnAll && !isRetryableError(err) {
			logger.DebugCF("retry", "Non-retryable error, not retrying",
				map[string]any{
					"error":   err.Error(),
					"attempt": attempt,
				})
			return nil, err
		}

		// Don't sleep after the last attempt
		if attempt >= config.MaxRetries {
			break
		}

		delay := calculateDelay(config, attempt)

		logger.WarnCF("retry", "LLM call failed, retrying",
			map[string]any{
				"error":       err.Error(),
				"attempt":     attempt + 1,
				"max_retries": config.MaxRetries,
				"delay":       delay.String(),
				"model":       model,
			})

		// Sleep with context cancellation support
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-retrySleep(delay):
		}
	}

	logger.ErrorCF("retry", "All retry attempts exhausted",
		map[string]any{
			"max_retries": config.MaxRetries,
			"model":       model,
			"last_error":  lastErr.Error(),
		})

	return nil, lastErr
}
