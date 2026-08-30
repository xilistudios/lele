package tools

import (
	"context"
	"math"
	"strings"
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
// Returns true for timeouts, rate limits, and transient server errors.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(err.Error())

	// Timeouts - server didn't respond in time
	if strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "deadline exceeded") ||
		strings.Contains(errMsg, "context deadline exceeded") {
		return true
	}

	// Rate limiting
	if strings.Contains(errMsg, "429") ||
		strings.Contains(errMsg, "rate limit") ||
		strings.Contains(errMsg, "too many requests") {
		return true
	}

	// Transient server errors (5xx)
	if strings.Contains(errMsg, "500") ||
		strings.Contains(errMsg, "502") ||
		strings.Contains(errMsg, "503") ||
		strings.Contains(errMsg, "504") ||
		strings.Contains(errMsg, "bad gateway") ||
		strings.Contains(errMsg, "service unavailable") ||
		strings.Contains(errMsg, "gateway timeout") {
		return true
	}

	// Connection errors (transient network issues)
	if strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "broken pipe") ||
		strings.Contains(errMsg, "i/o timeout") {
		return true
	}

	// EOF or unexpected EOF (connection closed prematurely)
	if strings.Contains(errMsg, "unexpected eof") ||
		strings.Contains(errMsg, "unexpected end of") {
		return true
	}

	return false
}

// calculateDelay computes the delay for a given retry attempt using exponential backoff.
func calculateDelay(config RetryConfig, attempt int) time.Duration {
	delay := float64(config.BaseDelay) * math.Pow(config.Multiplier, float64(attempt))
	if delay > float64(config.MaxDelay) {
		delay = float64(config.MaxDelay)
	}
	return time.Duration(delay)
}

// ChatWithRetry wraps a provider's Chat call with retry logic.
// It will automatically retry on transient errors (timeouts, rate limits, 5xx).
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
		case <-time.After(delay):
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
