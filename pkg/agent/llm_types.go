package agent

import (
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/tools"
)

type messageSignature struct {
	toolCalls []toolCallSignature
	count     int
}

type toolCallSignature struct {
	name      string
	arguments string
}

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

var knownDeepSeekPrefixes = []string{
	"deepseek/",
	"deepseek-",
	"deepseek_v",
}

func isDeepSeekModel(model string) bool {
	lower := strings.ToLower(model)
	for _, prefix := range knownDeepSeekPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func knownToolNames(registry *tools.ToolRegistry) []string {
	if registry == nil {
		return nil
	}
	names := registry.List()
	sort.Strings(names)
	return names
}

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
