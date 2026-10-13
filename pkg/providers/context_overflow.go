// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import (
	"errors"
	"strings"
)

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

	// Bedrock / AWS. "InvalidParameter: Total tokens ..." variants are
	// emitted by Bedrock's Converse API (and its test doubles) for both
	// message-token and image+text token overflow.
	{name: "bedrock_input_too_long", patterns: []string{"input is too long"}},
	{name: "bedrock_too_many_input_tokens", patterns: []string{"too many input tokens"}},
	{name: "bedrock_validationexception_tokens", patterns: []string{"validationexception", "tokens"}, requireAll: true},
	{name: "bedrock_invalidparameter_total_tokens", patterns: []string{"invalidparameter", "tokens"}, requireAll: true},

	// Mistral: "too long" alone is too ambiguous, so it must be combined
	// with "tokens" or "context" to be considered an overflow.
	{name: "mistral_too_long_tokens", patterns: []string{"too long", "tokens"}, requireAll: true},
	{name: "mistral_too_long_context", patterns: []string{"too long", "context"}, requireAll: true},

	// Byte/size rejections from the transport layer (HTTP 413-style body caps,
	// e.g. a gateway refusing to read a request over its limit) or from
	// providers' image handling. They describe the SIZE of the outbound
	// request on the wire, not its token count, but they are terminal in the
	// same way: the identical payload fails everywhere, so the fallback chain
	// must not burn its budget re-sending it. Note "request body too large"
	// and the Anthropic "request too large" are NOT substrings of each other,
	// so each needs its own group; ordering still decides when both could
	// match a message (first match wins).
	{name: "request_body_too_large", patterns: []string{"request body too large"}},
	{name: "payload_too_large", patterns: []string{"payload too large", "request entity too large", "entity too large"}},
	{name: "image_too_large", patterns: []string{"image dimensions exceed max", "image exceeds"}},

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
//
// It is intentionally a SUPERSET of IsRequestSizeError: it covers both
// token/context overflows and byte-size rejections of the outbound request.
// Callers that must distinguish "too many tokens" from "too many bytes" (e.g.
// a shrink-and-retry loop that trims images rather than text) use
// IsRequestSizeError.
func IsContextOverflowError(err error) bool {
	return ContextOverflowSummary(err) != ""
}

// IsRequestSizeError reports whether err is a rejection caused by the SIZE of
// the outbound request (as opposed to its token count): an HTTP 413, or a
// transport/provider message matching the byte-cap pattern groups
// (request_body_too_large, payload_too_large).
//
// IsContextOverflowError is a superset of this predicate: it also covers
// token-count overflows ("prompt is too long", "context_length_exceeded",
// ...), which are NOT size errors.
func IsRequestSizeError(err error) bool {
	if err == nil {
		return false
	}
	// A structured 413 is a size rejection regardless of the body prose.
	var hs httpStatusError
	if errors.As(err, &hs) && hs.HTTPStatus() == 413 {
		return true
	}
	return matchesOverflowGroups(err, "request_body_too_large", "payload_too_large")
}

// matchesOverflowGroups reports whether err's lowercased message matches any
// of the named contextOverflowPatterns groups, using the same first-match
// semantics as ContextOverflowSummary but restricted to a subset of groups
// (so the pattern lists are never duplicated).
func matchesOverflowGroups(err error, names ...string) bool {
	msg := strings.ToLower(err.Error())
	for _, group := range contextOverflowPatterns {
		if !group.matches(msg) {
			continue
		}
		for _, name := range names {
			if group.name == name {
				return true
			}
		}
	}
	return false
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
