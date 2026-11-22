package providers

import (
	"context"
	"errors"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers/common"
)

func ChatIdle(
	ctx context.Context,
	provider LLMProvider,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]interface{},
) (*LLMResponse, error) {
	sp, ok := provider.(StreamingLLMProvider)
	if !ok {
		return provider.Chat(ctx, messages, tools, model, options)
	}

	streamCtx, cancel := context.WithTimeout(ctx, common.MaxRequestLifetime)
	defer cancel()

	var started bool
	markStarted := func(chunk string, done bool) {
		if chunk != "" {
			started = true
		}
	}

	resp, err := sp.ChatStream(streamCtx, messages, tools, model, options, markStarted, nil)
	if err == nil {
		return resp, nil
	}
	if ctx.Err() != nil {
		return resp, err
	}
	if started {
		return resp, err
	}
	if errors.Is(err, common.ErrMissingWireSession) {
		return resp, err
	}
	if classified := ClassifyError(err, "", ""); classified != nil {
		switch classified.Reason {
		case FailoverTimeout, FailoverAuth, FailoverBilling, FailoverRateLimit:
			return resp, err
		}
	}

	logger.DebugCF("providers", "Streaming transport failed before first chunk; retrying non-streaming", map[string]interface{}{
		"model": model,
		"error": err.Error(),
	})
	return provider.Chat(ctx, messages, tools, model, options)
}
