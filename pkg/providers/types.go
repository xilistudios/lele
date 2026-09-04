package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

type ToolCall = protocoltypes.ToolCall
type FunctionCall = protocoltypes.FunctionCall
type LLMResponse = protocoltypes.LLMResponse
type UsageInfo = protocoltypes.UsageInfo
type Message = protocoltypes.Message
type MessageAttachment = protocoltypes.MessageAttachment
type ContentPart = protocoltypes.ContentPart
type ImageURL = protocoltypes.ImageURL
type ToolDefinition = protocoltypes.ToolDefinition
type ToolFunctionDefinition = protocoltypes.ToolFunctionDefinition

// Prompt-cache option keys (see protocoltypes for semantics).
const (
	OptPromptCache    = protocoltypes.OptPromptCache
	OptPromptCacheTTL = protocoltypes.OptPromptCacheTTL
)

type LLMProvider interface {
	Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error)
	GetDefaultModel() string
}

type StreamingLLMProvider interface {
	ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}, onChunk func(chunk string, done bool), onReasoning func(reasoningChunk string)) (*LLMResponse, error)
}

// FailoverReason classifies why an LLM request failed for fallback decisions.
type FailoverReason string

const (
	FailoverAuth       FailoverReason = "auth"
	FailoverRateLimit  FailoverReason = "rate_limit"
	FailoverBilling    FailoverReason = "billing"
	FailoverTimeout    FailoverReason = "timeout"
	FailoverFormat     FailoverReason = "format"
	FailoverOverloaded FailoverReason = "overloaded"
	FailoverUnknown    FailoverReason = "unknown"
)

// FailoverError wraps an LLM provider error with classification metadata.
type FailoverError struct {
	Reason   FailoverReason
	Provider string
	Model    string
	Status   int
	// RetryAfter is the wait the server asked for (Retry-After and friends),
	// propagated verbatim from the provider response. 0 means no usable hint,
	// which is the overwhelmingly common case; consumers must treat it as a
	// floor for backoff, not as a schedule (see common.ParseRetryAfter).
	RetryAfter time.Duration
	Wrapped    error
}

func (e *FailoverError) Error() string {
	base := fmt.Sprintf("failover(%s): provider=%s model=%s status=%d",
		e.Reason, e.Provider, e.Model, e.Status)
	// The hint is appended only when present so the historical message format
	// is unchanged for every error without a server-side wait hint.
	if e.RetryAfter > 0 {
		base += fmt.Sprintf(" retry_after=%gs", e.RetryAfter.Seconds())
	}
	return fmt.Sprintf("%s: %v", base, e.Wrapped)
}

func (e *FailoverError) Unwrap() error {
	return e.Wrapped
}

// IsRetriable answers: "may the fallback chain try a DIFFERENT candidate?"
//
// Only format errors (bad payload, unsupported image dimensions/size) are
// candidate-independent, so they abort the chain. Auth and billing are NOT
// excluded here on purpose: a bad key or an empty wallet belongs to one
// provider, so the next candidate may well succeed. Do not confuse this with
// IsTerminal below, which asks a different question.
func (e *FailoverError) IsRetriable() bool {
	// Equivalent to the historical `Reason != FailoverFormat`, but expressed
	// through IsTerminal so terminality is declared in exactly one place.
	// auth/billing are the exception: terminal for this provider, still
	// candidate-swappable for the chain.
	return !e.IsTerminal() || e.Reason == FailoverAuth || e.Reason == FailoverBilling
}

// IsTerminal answers: "is retrying THIS SAME request useless?"
//
// This is deliberately narrower than !IsRetriable(): auth and billing are
// terminal for the current provider (retrying the identical request repeats
// the failure) yet still allow a failover to another candidate. Terminal
// reasons are the blacklist used by IsRetriableError and, downstream, by the
// agent loop to decide when to stop retrying.
func (e *FailoverError) IsTerminal() bool {
	switch e.Reason {
	case FailoverAuth, FailoverBilling, FailoverFormat:
		return true
	}
	return false
}

// isTerminalReason is the package-level form of (*FailoverError).IsTerminal,
// for code that only has a FailoverReason (e.g. fallback attempts).
func isTerminalReason(r FailoverReason) bool {
	return (&FailoverError{Reason: r}).IsTerminal()
}

// ShouldBackoff returns true if this error should trigger exponential backoff
// retry within the same provider before falling back.
//
// Everything transient backs off, including timeouts, transport failures and
// unknown errors: retrying them immediately hammers a provider that is already
// down or saturated, which is exactly what turns a 2s blip into an outage.
// Terminal reasons never back off here because they should not be retried at
// all (see IsTerminal).
func (e *FailoverError) ShouldBackoff() bool {
	switch e.Reason {
	case FailoverRateLimit, FailoverOverloaded, FailoverTimeout, FailoverUnknown:
		return true
	}
	return false
}

// ModelConfig holds primary model and fallback list.
type ModelConfig struct {
	Primary   string
	Fallbacks []string
}
