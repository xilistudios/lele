package anthropicmessages

import "github.com/xilistudios/lele/pkg/providers/protocoltypes"

// applyPromptCacheBreakpoints adds Anthropic cache_control breakpoints at the
// end of the three stable request prefixes — system prompt, tools, and
// conversation history — so subsequent turns can read the cached prefix
// instead of re-processing it. Anthropic matches caches on exact request
// prefixes, so breakpoints only help when placed after content that repeats
// verbatim between calls (which holds for lele: the system prompt is static
// and tools are registered at startup in deterministic order).
//
// The history breakpoint is placed on the last plain text block of the final
// message. tool_use / tool_result blocks are skipped because their position
// shifts every iteration of the agent loop, which would invalidate the cache
// entry immediately.
func applyPromptCacheBreakpoints(result map[string]any, ttl string) {
	cc := protocoltypes.CacheControlJSON(ttl)

	// System: convert the joined string into a block array with a breakpoint.
	if sys, ok := result["system"].(string); ok && sys != "" {
		result["system"] = []map[string]any{
			{"type": "text", "text": sys, "cache_control": cc},
		}
	}

	// Tools: breakpoint on the last tool definition.
	if tools, ok := result["tools"].([]any); ok && len(tools) > 0 {
		if last, ok := tools[len(tools)-1].(map[string]any); ok {
			last["cache_control"] = cc
		}
	}

	// History: breakpoint on the last text block of the last message.
	msgs, _ := result["messages"].([]any)
	if len(msgs) == 0 {
		return
	}
	last, ok := msgs[len(msgs)-1].(map[string]any)
	if !ok {
		return
	}
	switch content := last["content"].(type) {
	case string:
		if content == "" {
			return
		}
		last["content"] = []map[string]any{
			{"type": "text", "text": content, "cache_control": cc},
		}
	case []map[string]any:
		for i := len(content) - 1; i >= 0; i-- {
			if content[i]["type"] == "text" {
				content[i]["cache_control"] = cc
				return
			}
		}
	case []any:
		for i := len(content) - 1; i >= 0; i-- {
			if b, ok := content[i].(map[string]any); ok && b["type"] == "text" {
				b["cache_control"] = cc
				return
			}
		}
	}
}
