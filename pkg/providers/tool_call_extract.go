package providers

import (
	"encoding/json"
	"strings"
)

// extractToolCallsFromText parses tool call JSON from response text.
// CodexCliProvider (and any provider whose model emits tool calls as text)
// uses this to extract tool calls the model outputs in its response text.
func extractToolCallsFromText(text string) []ToolCall {
	start := strings.Index(text, `{"tool_calls"`)
	if start == -1 {
		return nil
	}

	end := findMatchingBrace(text, start)
	if end == start {
		return nil
	}

	jsonStr := text[start:end]

	var wrapper struct {
		ToolCalls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &wrapper); err != nil {
		return nil
	}

	var result []ToolCall
	for _, tc := range wrapper.ToolCalls {
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			// If arguments can't be parsed as JSON, keep them as-is in Function.Arguments
			// and set Arguments to nil to avoid partial data
			args = nil
		}

		result = append(result, ToolCall{
			ID:        tc.ID,
			Type:      tc.Type,
			Name:      tc.Function.Name,
			Arguments: args,
			Function: &FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}

	return result
}

// stripToolCallsFromText removes tool call JSON from response text.
func stripToolCallsFromText(text string) string {
	start := strings.Index(text, `{"tool_calls"`)
	if start == -1 {
		return text
	}

	end := findMatchingBrace(text, start)
	if end == start {
		return text
	}

	return strings.TrimSpace(text[:start] + text[end:])
}

// findMatchingBrace finds the index after the closing brace matching the opening brace at pos.
// It is string-aware: braces inside JSON string literals (delimited by double quotes,
// with backslash-escaped characters) are ignored.
func findMatchingBrace(text string, pos int) int {
	depth := 0
	inString := false
	escaped := false
	for i := pos; i < len(text); i++ {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if text[i] == '\\' {
				escaped = true
				continue
			}
			if text[i] == '"' {
				inString = false
			}
			continue
		}
		// not in a string
		if text[i] == '"' {
			inString = true
		} else if text[i] == '{' {
			depth++
		} else if text[i] == '}' {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return pos
}
