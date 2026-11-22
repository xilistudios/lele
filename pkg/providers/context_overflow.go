package providers

import (
	"errors"
	"strings"
)

type overflowPatternGroup struct {
	name       string
	patterns   []string
	requireAll bool
}

var contextOverflowPatterns = []overflowPatternGroup{
	{name: "openai_context_length_exceeded", patterns: []string{"context_length_exceeded"}},
	{name: "openai_maximum_context_length", patterns: []string{"maximum context length"}},
	{name: "openai_reduce_length_of_messages", patterns: []string{"reduce the length of the messages"}},

	{name: "anthropic_prompt_too_long", patterns: []string{"prompt is too long"}},
	{name: "anthropic_request_too_large", patterns: []string{"request too large"}},

	{name: "gemini_input_token_count", patterns: []string{"input token count"}},
	{name: "gemini_exceeds_max_number_of_tokens", patterns: []string{"exceeds the maximum number of tokens"}},

	{name: "bedrock_input_too_long", patterns: []string{"input is too long"}},
	{name: "bedrock_too_many_input_tokens", patterns: []string{"too many input tokens"}},
	{name: "bedrock_validationexception_tokens", patterns: []string{"validationexception", "tokens"}, requireAll: true},
	{name: "bedrock_invalidparameter_total_tokens", patterns: []string{"invalidparameter", "tokens"}, requireAll: true},

	{name: "mistral_too_long_tokens", patterns: []string{"too long", "tokens"}, requireAll: true},
	{name: "mistral_too_long_context", patterns: []string{"too long", "context"}, requireAll: true},

	{name: "request_body_too_large", patterns: []string{"request body too large"}},
	{name: "payload_too_large", patterns: []string{"payload too large", "request entity too large", "entity too large"}},
	{name: "image_too_large", patterns: []string{"image dimensions exceed max", "image exceeds"}},

	{name: "generic_context_window", patterns: []string{"context window"}},
	{name: "generic_context_length", patterns: []string{"context length"}},
	{name: "generic_too_many_tokens", patterns: []string{"too many tokens"}},
	{name: "generic_token_limit", patterns: []string{"token limit"}},
}

func IsContextOverflowError(err error) bool {
	return ContextOverflowSummary(err) != ""
}

func IsRequestSizeError(err error) bool {
	if err == nil {
		return false
	}
	var hs httpStatusError
	if errors.As(err, &hs) && hs.HTTPStatus() == 413 {
		return true
	}
	return matchesOverflowGroups(err, "request_body_too_large", "payload_too_large")
}

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

func ContextOverflowSummary(err error) string {
	if err == nil {
		return ""
	}

	msg := strings.ToLower(err.Error())

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

func (g overflowPatternGroup) matches(msg string) bool {
	if g.requireAll {
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
	for _, p := range g.patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}
