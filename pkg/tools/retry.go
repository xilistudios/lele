package tools

import (
	"context"
	"math"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Multiplier float64
	RetryOnAll bool
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		BaseDelay:  5 * time.Second,
		MaxDelay:   60 * time.Second,
		Multiplier: 2.0,
		RetryOnAll: false,
	}
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	return providers.IsRetriableError(err)
}

var retrySleep = time.After

func calculateDelay(config RetryConfig, attempt int) time.Duration {
	delay := float64(config.BaseDelay) * math.Pow(config.Multiplier, float64(attempt))
	if delay > float64(config.MaxDelay) {
		delay = float64(config.MaxDelay)
	}
	return time.Duration(delay)
}

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
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		response, err := providers.ChatIdle(ctx, provider, messages, tools, model, options)
		if err == nil {
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

		if !config.RetryOnAll && !isRetryableError(err) {
			logger.DebugCF("retry", "Non-retryable error, not retrying",
				map[string]any{
					"error":   err.Error(),
					"attempt": attempt,
				})
			return nil, err
		}

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
