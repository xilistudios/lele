// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package protocoltypes

import (
	"encoding/json"
	"strings"
)

// toolCallWire is the JSON shape used on the wire and in persisted sessions.
//
// Arguments is json.RawMessage because the same field has historically carried
// three different shapes: an object (the internal convenience copy), a JSON
// string (the OpenAI wire format) and a double-encoded string. Reading it as
// raw bytes lets UnmarshalJSON accept every one of them without a second
// struct definition.
type toolCallWire struct {
	ID               string          `json:"id"`
	Type             string          `json:"type,omitempty"`
	Function         *FunctionCall   `json:"function,omitempty"`
	Name             string          `json:"name,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	ThoughtSignature string          `json:"thought_signature,omitempty"`
	ExtraContent     *ExtraContent   `json:"extra_content,omitempty"`
}

// MarshalJSON writes the canonical OpenAI tool-call shape:
//
//	{"id":"...","type":"function","function":{"name":"...","arguments":"{...}"}}
//
// The internal convenience fields Name and Arguments (the decoded map) are
// deliberately NOT emitted. They used to be, which caused two distinct
// production failures:
//
//  1. A duplicate top-level "arguments" object next to function.arguments,
//     which strict OpenAI-compatible gateways reject outright with
//     400 invalid_request_error, and which doubled the tool-call token cost of
//     every replayed turn.
//  2. function.arguments:"null" whenever the decoded map was nil - a provider
//     that streamed empty tool-call deltas produced nil Arguments,
//     json.Marshal(nil map) yields the four bytes "null", and the next request
//     failed with 400 'The "function.arguments" parameter ... must be in JSON
//     format'. Because that assistant message was persisted to the session,
//     every later turn replayed it and the agent was stuck permanently.
//
// Emitting exactly one arguments value, always a valid JSON object string,
// makes both states unrepresentable.
//
// The receiver is a value, not a pointer: encoding/json only consults a
// pointer-receiver MarshalJSON when the value is addressable, so
// json.Marshal(someToolCall) would silently fall back to the struct tags and
// re-emit the raw fields. A value receiver makes the canonical shape
// unconditional on every call path.
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

// UnmarshalJSON accepts every historical shape and always leaves the tool call
// internally consistent: Function is non-nil with a valid JSON object string in
// Arguments, and Arguments (the decoded map) is populated from it.
//
// Keeping the decoded map populated matters because tools execute from it
// (ExecuteWithContext takes tc.Arguments) and because providers that do not use
// the OpenAI shape - Anthropic, Bedrock, Codex, Antigravity - read it directly.
// Bedrock even documents the hazard: "tc.Name/tc.Arguments ... may be empty
// when from JSON" (pkg/providers/bedrock/provider_bedrock.go).
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
	argsJSON = normalizeArgumentsJSON(argsJSON)

	tc.Name = name
	// Mirror thought_signature onto the top level only when it was stored there
	// originally; Function keeps the authoritative copy.
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

// FunctionName returns the tool name from whichever field carries it.
func (tc *ToolCall) FunctionName() string {
	if tc == nil {
		return ""
	}
	if tc.Function != nil && strings.TrimSpace(tc.Function.Name) != "" {
		return tc.Function.Name
	}
	return tc.Name
}

// ArgumentJSON returns the tool arguments as a valid JSON object string,
// preferring the canonical function.arguments copy and falling back to the
// decoded map.
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

// normalizeArgumentsJSON coerces any historical arguments representation into a
// valid JSON object string. It never returns "", "null" or malformed JSON, so
// the value it produces can never be rejected as "must be in JSON format".
func normalizeArgumentsJSON(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return emptyArgumentsJSON
	}
	if json.Valid([]byte(trimmed)) {
		switch trimmed[0] {
		case '{':
			// A JSON object is already the canonical form.
			return trimmed
		case '"':
			// A JSON string wrapping the payload: the double-encoded shape some
			// providers (and the CLI tool prompt) emit. Unwrap once and keep the
			// inner object if it is valid, otherwise discard it.
			var inner string
			if err := json.Unmarshal([]byte(trimmed), &inner); err == nil {
				if unwrapped := normalizeArgumentsJSON(inner); unwrapped != emptyArgumentsJSON || strings.TrimSpace(inner) == "" {
					return unwrapped
				}
			}
			return emptyArgumentsJSON
		default:
			// Valid JSON but not an object (null, array, number, bool). Tool
			// arguments must be an object; treat the rest as absent.
			return emptyArgumentsJSON
		}
	}

	// Malformed JSON: recover what we can rather than forwarding a payload the
	// provider will reject. Truncated objects (cut off by max_tokens) are the
	// common case, so close them up.
	if repaired, ok := repairTruncatedObject(trimmed); ok {
		return repaired
	}
	return emptyArgumentsJSON
}

// repairTruncatedObject closes a JSON object that was cut off mid-write,
// keeping every key/value pair that completed before the cut.
//
// Truncation is the common malformed-arguments case: the model hits max_tokens
// while writing a large argument (a file body, a long command) and the stream
// stops inside a value. Dropping the whole payload would throw away arguments
// that did arrive, so the scan records where the last complete member ended at
// each nesting level and rebuilds from there.
//
// It is a single pass over the bytes, which matters because tool arguments can
// be hundreds of kilobytes and the previous "try every prefix" approach was
// quadratic on exactly those payloads.
//
// Returns ok=false when the input is not a truncated object at all (it does not
// start with '{'), leaving the caller to decide the fallback.
func repairTruncatedObject(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "{") {
		return "", false
	}

	// Each frame is a container that is currently open: the character that
	// closes it and where inside it the last complete member ended.
	type frame struct {
		closer       byte
		lastComplete int // exclusive index into s
	}
	var stack []frame

	// rootEnd is set once the opening object has been closed; everything after
	// that point is trailing garbage.
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
				// A closer with nothing open: the structure is not recoverable.
				return emptyArgumentsJSON, true
			}
			top := stack[len(stack)-1]
			if c != top.closer {
				// Mismatched brackets: too corrupted to guess what survived.
				return emptyArgumentsJSON, true
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				rootEnd = i + 1
			} else {
				// The child just closed and is a complete member of its parent.
				stack[len(stack)-1].lastComplete = i + 1
			}
		case ',':
			if len(stack) > 0 {
				stack[len(stack)-1].lastComplete = i
			}
		}
	}

	// The root object closed cleanly; re-encoding it drops any trailing junk.
	if rootEnd >= 0 {
		prefix := s[:rootEnd]
		if json.Valid([]byte(prefix)) {
			return prefix, true
		}
		return emptyArgumentsJSON, true
	}

	if len(stack) == 0 {
		// No frames and no root close: unreachable for input starting with '{',
		// but kept explicit so a future change cannot index stack[0] on empty.
		return emptyArgumentsJSON, true
	}

	// Truncated: cut at the last complete member of the innermost open
	// container - that is where the stream stopped - and close every container
	// still open, innermost first. The member being written at cut time is lost,
	// which is what makes the result valid again.
	innermost := stack[len(stack)-1]
	kept := strings.TrimRight(s[:innermost.lastComplete], " \t\n\r,:")
	var b strings.Builder
	b.WriteString(kept)
	for i := len(stack) - 1; i >= 0; i-- {
		b.WriteByte(stack[i].closer)
	}
	repaired := b.String()
	if !json.Valid([]byte(repaired)) {
		return emptyArgumentsJSON, true
	}
	return repaired, true
}

// closingBracket returns the bracket that closes the given opener.
func closingBracket(open byte) byte {
	if open == '[' {
		return ']'
	}
	return '}'
}

// rawArgumentsToJSONString renders a raw "arguments" value (any historical
// shape) into the string form used by function.arguments.
func rawArgumentsToJSONString(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	// An object was stored directly (the internal convenience shape): use it as
	// is. A string was stored (double-encoded): unquote it.
	var inner string
	if err := json.Unmarshal(raw, &inner); err == nil {
		return inner
	}
	return trimmed
}

// decodeArgumentsMap turns a canonical arguments JSON string back into the
// decoded map that tools and non-OpenAI providers consume.
//
// This is the protocoltypes-local counterpart of common.DecodeToolCallArguments
// (which cannot be called from here: pkg/providers/common imports this package,
// so using it would create an import cycle). It handles the canonical form
// produced by normalizeArgumentsJSON, which is always a valid JSON object, so
// the truncation repair that common performs is not needed on this path.
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
		// Defensive: a double-encoded value that survived normalisation.
		var nested map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(v)), &nested); err == nil && nested != nil {
			return nested
		}
	}
	return arguments
}

// CanonicalToolCalls rebuilds tool calls for persistence and replay: entries
// without a tool name are dropped (they cannot be executed and are provider
// noise), and every surviving entry carries a valid JSON object in
// function.arguments plus the decoded map that tools and non-OpenAI providers
// read.
//
// All assistant-message builders must go through this so a malformed tool call
// cannot reach the session store, where replaying it would fail every
// subsequent turn.
func CanonicalToolCalls(toolCalls []ToolCall) []ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(toolCalls))
	for i := range toolCalls {
		tc := toolCalls[i]
		name := tc.FunctionName()
		if strings.TrimSpace(name) == "" {
			// No tool to call: replaying this would only poison the request.
			continue
		}
		argsJSON := tc.ArgumentJSON()
		canonical := ToolCall{
			ID:               tc.ID,
			Type:             "function",
			Name:             name,
			Arguments:        decodeArgumentsMap(argsJSON, name),
			ThoughtSignature: tc.ThoughtSignature,
			ExtraContent:     tc.ExtraContent,
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
