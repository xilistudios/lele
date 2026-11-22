package anthropicmessages

import "github.com/xilistudios/lele/pkg/providers/protocoltypes"

func applyPromptCacheBreakpoints(result map[string]any, ttl string) {
	cc := protocoltypes.CacheControlJSON(ttl)

	if sys, ok := result["system"].(string); ok && sys != "" {
		result["system"] = []map[string]any{
			{"type": "text", "text": sys, "cache_control": cc},
		}
	}

	if tools, ok := result["tools"].([]any); ok && len(tools) > 0 {
		if last, ok := tools[len(tools)-1].(map[string]any); ok {
			last["cache_control"] = cc
		}
	}

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
