// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/tools"
)

// messageSignature tracks repeated LLM responses to detect loops
type messageSignature struct {
	toolCalls []toolCallSignature
	count     int
}

// toolCallSignature represents a unique signature of a tool call for loop detection
type toolCallSignature struct {
	name      string
	arguments string
}

// messageSignaturesEqual compares two sets of tool call signatures
func messageSignaturesEqual(a, b []toolCallSignature) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].name != b[i].name || a[i].arguments != b[i].arguments {
			return false
		}
	}
	return true
}

// knownDeepSeekPrefixes lists model name prefixes that support DeepSeek's
// native thinking mode.
var knownDeepSeekPrefixes = []string{
	"deepseek/",
	"deepseek-",
	"deepseek_v",
}

// isDeepSeekModel checks if a model name matches known DeepSeek prefixes
func isDeepSeekModel(model string) bool {
	lower := strings.ToLower(model)
	for _, prefix := range knownDeepSeekPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// knownToolNames returns a sorted list of the tool names visible to one
// session: it takes the session's registry (see toolCoordinator.sessionTools)
// rather than an agent, because MCP tools live in a per-session layer and must
// be reported only to the session that loaded them.
func knownToolNames(registry *tools.ToolRegistry) []string {
	if registry == nil {
		return nil
	}
	names := registry.List()
	sort.Strings(names)
	return names
}

// containsPlainToolCall checks if the response content contains plain-text tool
// invocations like `read_file{"path":"..."}` instead of proper function calling.
func containsPlainToolCall(content string, registry *tools.ToolRegistry) bool {
	if len(strings.TrimSpace(content)) == 0 {
		return false
	}
	if registry == nil {
		return false
	}

	toolNames := registry.List()
	for _, name := range toolNames {
		pattern := name + "{"
		if strings.Contains(content, pattern) {
			idx := strings.Index(content, pattern)
			if idx >= 0 {
				remaining := content[idx+len(pattern):]
				endIdx := strings.Index(remaining, "}")
				if endIdx > 0 {
					inner := strings.TrimSpace(remaining[:endIdx])
					if strings.Contains(inner, "\"") {
						return true
					}
				}
			}
		}
	}

	return false
}
