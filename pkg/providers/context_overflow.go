// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import "strings"

// overflowPatternGroup defines a named group of lowercase substrings used to
// detect context-window overflow errors reported by different LLM providers.
type overflowPatternGroup struct {
	// name is the identifier returned by ContextOverflowSummary.
	name string
	// patterns are lowercase substrings looked up in the error message.
	patterns []string
	// requireAll, when true, means every pattern in the group must be present
	// for a match (used for combined cases like "validationexception" + "tokens").
	// When false, a single matching pattern is enough.
	requireAll bool
}

// contextOverflowPatterns lists known context-window overflow error patterns,
// ordered from most specific (provider-specific) to most generic. Order
// matters: the first matching group wins, so provider-specific groups must
// precede the generic ones to yield precise summaries.
var contextOverflowPatterns = []overflowPatternGroup{
	// OpenAI (also emitted by many OpenAI-compatible providers).
	{name: "openai_context_length_exceeded", patterns: []string{"context_length_exceeded"}},
	{name: "openai_maximum_context_length", patterns: []string{"maximum context length"}},
	{name: "openai_reduce_length_of_messages", patterns: []string{"reduce the length of the messages"}},

	// Anthropic.
	{name: "anthropic_prompt_too_long", patterns: []string{"prompt is too long"}},
	{name: "anthropic_request_too_large", patterns: []string{"request too large"}},

	// Gemini.
	{name: "gemini_input_token_count", patterns: []string{"input token count"}},
	{name: "gemini_exceeds_max_number_of_tokens", patterns: []string{"exceeds the maximum number of tokens"}},

	// Bedrock / AWS.
	{name: "bedrock_input_too_long", patterns: []string{"input is too long"}},
	{name: "bedrock_too_many_input_tokens", patterns: []string{"too many input tokens"}},
	{name: "bedrock_validationexception_tokens", patterns: []string{"validationexception", "tokens"}, requireAll: true},

	// Mistral: "too long" alone is too ambiguous, so it must be combined
	// with "tokens" or "context" to be considered an overflow.
	{name: "mistral_too_long_tokens", patterns: []string{"too long", "tokens"}, requireAll: true},
	{name: "mistral_too_long_context", patterns: []string{"too long", "context"}, requireAll: true},

	// Generic fallbacks (keep last: they are the least specific).
	{name: "generic_context_window", patterns: []string{"context window"}},
	{name: "generic_context_length", patterns: []string{"context length"}},
	{name: "generic_too_many_tokens", patterns: []string{"too many tokens"}},
	{name: "generic_token_limit", patterns: []string{"token limit"}},
}

// IsContextOverflowError reports whether err indicates that the request
// exceeded the model's context window (prompt too large), as opposed to
// timeouts, auth failures or other unrelated errors.
//
// It intentionally does NOT match the bare substring "length", which appears
// in unrelated errors such as "invalid length", and it explicitly rejects
// timeout messages ("deadline", "timed out") that merely mention "context".
func IsContextOverflowError(err error) bool {
	return ContextOverflowSummary(err) != ""
}

// ContextOverflowSummary returns a short lowercase identifier of the first
// matching overflow pattern group (e.g. "anthropic_prompt_too_long"), or an
// empty string if the error does not indicate a context overflow.
func ContextOverflowSummary(err error) string {
	if err == nil {
		return ""
	}

	msg := strings.ToLower(err.Error())

	// False-positive guard: Go's context.DeadlineExceeded ("context deadline
	// exceeded") and client timeouts mention "context" but are timeouts, not
	// overflows. Reject them before checking any pattern.
	if strings.Contains(msg, "deadline") || strings.Contains(msg, "timed out") {
		return ""
	}

	for _, group := range contextOverflowPatterns {
		if group.matches(msg) {
			return group.name
		}
	}
	return ""
}

// matches reports whether msg satisfies this pattern group according to its
// requireAll semantics.
func (g overflowPatternGroup) matches(msg string) bool {
	if g.requireAll {
		// Every pattern in the group must be present. The length guard keeps
		// empty groups from vacuously matching everything.
		if len(g.patterns) == 0 {
			return false
		}
		for _, p := range g.patterns {
			if !strings.Contains(msg, p) {
				return false
			}
		}
		return true
	}
	// Any single pattern is enough.
	for _, p := range g.patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}
