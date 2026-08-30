// Lele - Ultra-lightweight personal AI agent
// Copyright (c) 2026 Lele contributors

package providers

import (
	"context"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers/common"
)

// ChatIdle performs a chat completion preferring the streaming transport.
//
// Why: a non-streaming HTTP chat request produces no bytes until the model has
// finished, so it can only be guarded by a fixed total-duration timeout
// (common.DefaultRequestTimeout). A reasoning model that thinks for minutes
// while actively producing output gets killed by that timeout even though the
// upstream connection is healthy.
//
// The SSE transport emits bytes continuously, so it can be guarded by an idle
// timeout instead (see common.NewIdleTimeoutReader): the request survives as
// long as data keeps flowing, and only a genuinely stalled connection fails.
//
// This helper is about the transport only. Whether chunks are *published* to a
// client is decided by the caller via its own callbacks; here they are no-ops,
// so channels that cannot render a live stream (Telegram, WhatsApp, cron,
// subagents) still receive one complete response at the end.
//
// Providers without streaming support fall through to Chat(). If the streaming
// attempt fails before producing any chunk, Chat() is tried once as a safety
// net (some proxies reject SSE); if chunks already started, the error is
// returned as-is so a partially generated response is never re-billed.
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

	// Safety net around the stream only: an idle timeout fires on silence, but
	// a misbehaving upstream that dribbles a byte forever would never trip it,
	// so cap the total lifetime of the streamed request as defence in depth.
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
	// Hard abort (user stop / outer deadline): never re-issue the request.
	if ctx.Err() != nil {
		return resp, err
	}
	// Partial output already received: retrying would duplicate work and cost.
	if started {
		return resp, err
	}
	// Decide whether a non-streaming retry can plausibly succeed. Note this is
	// not IsRetriable(): that predicate answers "should we fail over to another
	// candidate", whereas here the provider, key and payload stay the same and
	// only the transport changes. So propagate anything whose outcome does not
	// depend on the transport:
	//   - timeout/idle: Chat() applies a stricter fixed-duration cap, so a
	//     second request only adds latency to the same failure. The caller's
	//     retry layer owns those.
	//   - auth/billing/rate-limit: a credential or quota problem repeats
	//     identically over any transport.
	// Everything else (notably a proxy rejecting stream=true with a 400/406,
	// or an unclassifiable transport error) is worth one non-streaming retry.
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
	// Fallback uses the parent ctx so Chat() applies its own request timeout.
	return provider.Chat(ctx, messages, tools, model, options)
}
