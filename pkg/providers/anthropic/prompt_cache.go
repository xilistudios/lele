package anthropicprovider

import (
	"github.com/anthropics/anthropic-sdk-go"
)

// --- Prompt caching -------------------------------------------------------

// sdkCacheControl converts a normalized TTL ("5m"/"1h") into the SDK's
// ephemeral cache-control param.
func sdkCacheControl(ttl string) anthropic.CacheControlEphemeralParam {
	cc := anthropic.NewCacheControlEphemeralParam()
	if ttl == "1h" {
		cc.TTL = anthropic.CacheControlEphemeralTTLTTL1h
	} else {
		cc.TTL = anthropic.CacheControlEphemeralTTLTTL5m
	}
	return cc
}

// applyCacheBreakpoints places Anthropic cache_control breakpoints at the end
// of the three stable prefixes of a request: system prompt, tool definitions,
// and conversation history. Anthropic matches caches on request prefixes, so
// breakpoints must sit after content that repeats verbatim between turns.
// The history breakpoint is placed on the last plain-text block of the final
// message; tool_use/tool_result blocks are skipped because their position
// shifts every iteration of the agent loop.
func applyCacheBreakpoints(params *anthropic.MessageNewParams, ttl string) {
	cc := sdkCacheControl(ttl)

	// System prompt: breakpoint on the last system block.
	if n := len(params.System); n > 0 {
		params.System[n-1].CacheControl = cc
	}

	// Tools: breakpoint on the last tool that is a plain function tool.
	for i := len(params.Tools) - 1; i >= 0; i-- {
		if t := params.Tools[i].OfTool; t != nil {
			t.CacheControl = cc
			break
		}
	}

	// History: breakpoint on the last eligible block of the last message.
	if n := len(params.Messages); n > 0 {
		blocks := params.Messages[n-1].Content
		for i := len(blocks) - 1; i >= 0; i-- {
			if tb := blocks[i].OfText; tb != nil {
				tb.CacheControl = cc
				break
			}
		}
	}
}
