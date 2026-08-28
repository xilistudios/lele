// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import (
	"errors"
	"fmt"
	"testing"
)

// TestIsContextOverflowError_Matches covers every pattern group: each must
// match its representative real-world error message.
func TestIsContextOverflowError_Matches(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		// OpenAI.
		{"openai context_length_exceeded", errors.New(`Error code: 400 - {'error': {'code': 'context_length_exceeded', 'message': "This model's maximum context length is 8192 tokens"}}`), true},
		{"openai maximum context length", errors.New("This model's maximum context length is 4097 tokens"), true},
		{"openai reduce the length of the messages", errors.New("Please reduce the length of the messages"), true},

		// Anthropic.
		{"anthropic prompt is too long", errors.New("Your prompt is too long: 250000 tokens > 200000 token maximum"), true},
		{"anthropic request too large", errors.New("request too large: 210000 tokens"), true},

		// Gemini.
		{"gemini input token count", errors.New("The input token count (1200000) exceeds the maximum number of tokens"), true},
		{"gemini exceeds the maximum number of tokens", errors.New("Request exceeds the maximum number of tokens allowed"), true},

		// Bedrock / AWS.
		{"bedrock input is too long", errors.New("Input is too long for requested model"), true},
		{"bedrock too many input tokens", errors.New("Too many input tokens: 300000"), true},
		{"bedrock validationexception + tokens", errors.New("An error occurred (ValidationException) when calling the Converse operation: Input tokens exceed the model limit"), true},

		// Mistral (requireAll: "too long" + "tokens"/"context").
		{"mistral too long + tokens", errors.New("Conversation is too long: 40000 tokens"), true},
		{"mistral too long + context", errors.New("Request is too long for the model context"), true},

		// Generic.
		{"generic context window", errors.New("Prompt exceeds the model's context window"), true},
		{"generic context length", errors.New("Prompt exceeds the context length of the model"), true},
		{"generic too many tokens", errors.New("Too many tokens in the request"), true},
		{"generic token limit", errors.New("Request hit the token limit"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContextOverflowError(tt.err); got != tt.want {
				t.Errorf("IsContextOverflowError(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestContextOverflowSummary_Groups verifies that each group returns its
// expected summary name, and that provider-specific groups win over generic
// ones when both would match.
func TestContextOverflowSummary_Groups(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"openai code", errors.New("context_length_exceeded"), "openai_context_length_exceeded"},
		{"openai message", errors.New("maximum context length is 8192 tokens"), "openai_maximum_context_length"},
		{"openai reduce", errors.New("Please reduce the length of the messages"), "openai_reduce_length_of_messages"},
		{"anthropic", errors.New("Your prompt is too long"), "anthropic_prompt_too_long"},
		{"anthropic large", errors.New("request too large"), "anthropic_request_too_large"},
		{"gemini", errors.New("input token count exceeds limit"), "gemini_input_token_count"},
		{"gemini max", errors.New("exceeds the maximum number of tokens"), "gemini_exceeds_max_number_of_tokens"},
		{"bedrock long", errors.New("Input is too long"), "bedrock_input_too_long"},
		{"bedrock tokens", errors.New("too many input tokens"), "bedrock_too_many_input_tokens"},
		{"bedrock validation", errors.New("ValidationException: too many tokens"), "bedrock_validationexception_tokens"},
		{"bedrock invalidparameter total tokens", errors.New("InvalidParameter: Total tokens of image and text exceed max message tokens"), "bedrock_invalidparameter_total_tokens"},
		{"bedrock invalidparameter message tokens", errors.New("InvalidParameter: Total tokens exceed max message tokens"), "bedrock_invalidparameter_total_tokens"},
		{"mistral tokens", errors.New("too long: 50000 tokens"), "mistral_too_long_tokens"},
		{"mistral context", errors.New("too long for the context"), "mistral_too_long_context"},
		{"generic window", errors.New("context window exceeded"), "generic_context_window"},
		{"generic length", errors.New("context length exceeded"), "generic_context_length"},
		{"generic many", errors.New("too many tokens"), "generic_too_many_tokens"},
		{"generic limit", errors.New("token limit reached"), "generic_token_limit"},
		// Provider-specific groups must win over generic ones.
		{"specific beats generic", errors.New("prompt is too long for this context window"), "anthropic_prompt_too_long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContextOverflowSummary(tt.err); got != tt.want {
				t.Errorf("ContextOverflowSummary(%q) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// TestIsContextOverflowError_Negative covers errors that must NOT be
// classified as context overflow: nil, timeouts, and unrelated failures.
func TestIsContextOverflowError_Negative(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"nil error", nil},
		{"go context deadline exceeded", errors.New("context deadline exceeded")},
		{"wrapped deadline", fmt.Errorf("call failed: %w", errors.New("context deadline exceeded"))},
		{"timed out", errors.New("request timed out after 30s")},
		{"connection refused", errors.New("dial tcp 127.0.0.1:8080: connect: connection refused")},
		{"invalid api key", errors.New("invalid api key provided")},
		{"invalid length", errors.New("invalid length: expected 3, got 5")},
		{"rate limit", errors.New("429 too many requests")},
		{"empty message", errors.New("")},
		{"bare length", errors.New("length")},
		{"bare too long", errors.New("string is too long")}, // "too long" without tokens/context
		{"validationexception alone", errors.New("ValidationException: invalid parameter")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContextOverflowError(tt.err); got {
				t.Errorf("IsContextOverflowError(%q) = true, want false", tt.err)
			}
			if got := ContextOverflowSummary(tt.err); got != "" {
				t.Errorf("ContextOverflowSummary(%q) = %q, want empty", tt.err, got)
			}
		})
	}
}

// TestIsContextOverflowError_RequireAllSemantics exercises the requireAll
// flag: combined groups need every substring, single substrings are not enough.
func TestIsContextOverflowError_RequireAllSemantics(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		// Bedrock: validationexception requires "tokens" to be present too.
		{"validationexception without tokens", errors.New("ValidationException: malformed request body"), false},
		{"validationexception with tokens", errors.New("ValidationException: input has too many tokens"), true},
		// Mistral: "too long" requires "tokens" or "context".
		{"too long alone", errors.New("the string is too long"), false},
		{"too long with tokens", errors.New("the string is too long: 90000 tokens"), true},
		{"too long with context", errors.New("too long for the model context"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContextOverflowError(tt.err); got != tt.want {
				t.Errorf("IsContextOverflowError(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestIsContextOverflowError_WrappedErrors ensures errors wrapped with %w are
// still classified, since toolloop callers typically receive wrapped errors.
func TestIsContextOverflowError_WrappedErrors(t *testing.T) {
	base := errors.New("prompt is too long: 250000 tokens > 200000 maximum")
	wrapped := fmt.Errorf("provider call failed: %w", base)

	if !IsContextOverflowError(wrapped) {
		t.Errorf("IsContextOverflowError(wrapped) = false, want true")
	}
	if got := ContextOverflowSummary(wrapped); got != "anthropic_prompt_too_long" {
		t.Errorf("ContextOverflowSummary(wrapped) = %q, want %q", got, "anthropic_prompt_too_long")
	}
}
