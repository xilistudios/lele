package protocoltypes

import (
	"encoding/json"
	"strings"
)

type toolCallWire struct {
	ID               string          `json:"id"`
	Type             string          `json:"type,omitempty"`
	Function         *FunctionCall   `json:"function,omitempty"`
	Name             string          `json:"name,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	ThoughtSignature string          `json:"thought_signature,omitempty"`
	ExtraContent     *ExtraContent   `json:"extra_content,omitempty"`
}

func (tc ToolCall) MarshalJSON() ([]byte, error) {
	name := tc.FunctionName()
	args := tc.ArgumentJSON()

	function := &FunctionCall{Name: name, Arguments: args}
	if tc.Function != nil && tc.Function.ThoughtSignature != "" {
		function.ThoughtSignature = tc.Function.ThoughtSignature
	}

	callType := tc.Type
	if callType == "" {
		callType = "function"
	}

	out := toolCallWire{
		ID:               tc.ID,
		Type:             callType,
		Function:         function,
		ThoughtSignature: tc.ThoughtSignature,
		ExtraContent:     tc.ExtraContent,
	}
	return json.Marshal(out)
}

func (tc *ToolCall) UnmarshalJSON(data []byte) error {
	var raw toolCallWire
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	tc.ID = raw.ID
	tc.Type = raw.Type
	tc.ThoughtSignature = raw.ThoughtSignature
	tc.ExtraContent = raw.ExtraContent

	name := ""
	if raw.Function != nil {
		name = raw.Function.Name
	}
	if name == "" {
		name = raw.Name
	}

	argsJSON := ""
	if raw.Function != nil {
		argsJSON = raw.Function.Arguments
	}
	if argsJSON == "" {
		argsJSON = rawArgumentsToJSONString(raw.Arguments)
	}
	argsJSON, truncated := normalizeArgumentsJSONWithFlag(argsJSON)
	tc.ArgumentsTruncated = truncated

	tc.Name = name
	if tc.ThoughtSignature == "" && raw.Function != nil {
		tc.ThoughtSignature = raw.Function.ThoughtSignature
	}
	tc.Function = &FunctionCall{Name: name, Arguments: argsJSON}
	if tc.Type == "" {
		tc.Type = "function"
	}
	tc.Arguments = decodeArgumentsMap(argsJSON, name)
	return nil
}

func (tc *ToolCall) FunctionName() string {
	if tc == nil {
		return ""
	}
	if tc.Function != nil && strings.TrimSpace(tc.Function.Name) != "" {
		return tc.Function.Name
	}
	return tc.Name
}

func (tc *ToolCall) ArgumentJSON() string {
	if tc == nil {
		return emptyArgumentsJSON
	}
	if tc.Function != nil {
		if normalized := normalizeArgumentsJSON(tc.Function.Arguments); normalized != emptyArgumentsJSON || tc.Arguments == nil {
			return normalized
		}
	}
	encoded, err := json.Marshal(tc.Arguments)
	if err != nil || !json.Valid(encoded) {
		return emptyArgumentsJSON
	}
	return normalizeArgumentsJSON(string(encoded))
}

const emptyArgumentsJSON = "{}"

func normalizeArgumentsJSON(raw string) string {
	normalized, _ := normalizeArgumentsJSONWithFlag(raw)
	return normalized
}

func normalizeArgumentsJSONWithFlag(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return emptyArgumentsJSON, false
	}
	if json.Valid([]byte(trimmed)) {
		switch trimmed[0] {
		case '{':
			return trimmed, false
		case '"':
			var inner string
			if err := json.Unmarshal([]byte(trimmed), &inner); err == nil {
				unwrapped, truncated := normalizeArgumentsJSONWithFlag(inner)
				if unwrapped != emptyArgumentsJSON || strings.TrimSpace(inner) == "" {
					return unwrapped, truncated
				}
			}
			return emptyArgumentsJSON, false
		default:
			return emptyArgumentsJSON, false
		}
	}

	if repaired, ok, lost := repairTruncatedObject(trimmed); ok {
		return repaired, lost
	}
	return emptyArgumentsJSON, strings.HasPrefix(trimmed, "{")
}

func canonicalArgumentsJSON(tc *ToolCall) (string, bool) {
	if tc.Function != nil {
		normalized, truncated := normalizeArgumentsJSONWithFlag(tc.Function.Arguments)
		if normalized != emptyArgumentsJSON || tc.Arguments == nil {
			return normalized, truncated || tc.ArgumentsTruncated
		}
	}
	encoded, err := json.Marshal(tc.Arguments)
	if err != nil || !json.Valid(encoded) {
		return emptyArgumentsJSON, tc.ArgumentsTruncated
	}
	normalized, truncated := normalizeArgumentsJSONWithFlag(string(encoded))
	return normalized, truncated || tc.ArgumentsTruncated
}

func repairTruncatedObject(raw string) (repaired string, ok bool, lost bool) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "{") {
		return "", false, false
	}

	type frame struct {
		closer       byte
		lastComplete int
	}
	var stack []frame

	rootEnd := -1

	var inString, escaped bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, frame{closer: closingBracket(c), lastComplete: i + 1})
		case '}', ']':
			if len(stack) == 0 {
				return emptyArgumentsJSON, true, true
			}
			top := stack[len(stack)-1]
			if c != top.closer {
				return emptyArgumentsJSON, true, true
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				rootEnd = i + 1
			} else {
				stack[len(stack)-1].lastComplete = i + 1
			}
		case ',':
			if len(stack) > 0 {
				stack[len(stack)-1].lastComplete = i
			}
		}
	}

	if rootEnd >= 0 {
		prefix := s[:rootEnd]
		if json.Valid([]byte(prefix)) {
			return prefix, true, false
		}
		return emptyArgumentsJSON, true, true
	}

	if len(stack) == 0 {
		return emptyArgumentsJSON, true, true
	}

	innermost := stack[len(stack)-1]
	kept := strings.TrimRight(s[:innermost.lastComplete], " \t\n\r,:")
	var b strings.Builder
	b.WriteString(kept)
	for i := len(stack) - 1; i >= 0; i-- {
		b.WriteByte(stack[i].closer)
	}
	cut := b.String()
	if !json.Valid([]byte(cut)) {
		return emptyArgumentsJSON, true, true
	}
	return cut, true, true
}

func closingBracket(open byte) byte {
	if open == '[' {
		return ']'
	}
	return '}'
}

func rawArgumentsToJSONString(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var inner string
	if err := json.Unmarshal(raw, &inner); err == nil {
		return inner
	}
	return trimmed
}

func decodeArgumentsMap(argsJSON, name string) map[string]interface{} {
	arguments := make(map[string]interface{})
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed == "" || trimmed == "null" {
		return arguments
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return arguments
	}
	switch v := decoded.(type) {
	case map[string]interface{}:
		if v != nil {
			return v
		}
	case string:
		var nested map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(v)), &nested); err == nil && nested != nil {
			return nested
		}
	}
	return arguments
}

func CanonicalToolCalls(toolCalls []ToolCall) []ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(toolCalls))
	for i := range toolCalls {
		tc := toolCalls[i]
		name := tc.FunctionName()
		if strings.TrimSpace(name) == "" {
			continue
		}
		argsJSON, truncated := canonicalArgumentsJSON(&tc)
		canonical := ToolCall{
			ID:                 tc.ID,
			Type:               "function",
			Name:               name,
			Arguments:          decodeArgumentsMap(argsJSON, name),
			ThoughtSignature:   tc.ThoughtSignature,
			ExtraContent:       tc.ExtraContent,
			ArgumentsTruncated: truncated,
		}
		if tc.Function != nil {
			canonical.Function = &FunctionCall{
				Name:             name,
				Arguments:        argsJSON,
				ThoughtSignature: tc.Function.ThoughtSignature,
			}
		} else {
			canonical.Function = &FunctionCall{Name: name, Arguments: argsJSON}
		}
		out = append(out, canonical)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
