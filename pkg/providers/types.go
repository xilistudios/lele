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
type CommandApplied = protocoltypes.CommandApplied
type ContentPart = protocoltypes.ContentPart
type ImageURL = protocoltypes.ImageURL
type VideoURL = protocoltypes.VideoURL
type ToolDefinition = protocoltypes.ToolDefinition
type ToolFunctionDefinition = protocoltypes.ToolFunctionDefinition

const (
	OptPromptCache    = protocoltypes.OptPromptCache
	OptPromptCacheTTL = protocoltypes.OptPromptCacheTTL
)

var (
	CanonicalToolCalls = protocoltypes.CanonicalToolCalls
)

type LLMProvider interface {
	Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error)
	GetDefaultModel() string
}

type StreamingLLMProvider interface {
	ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}, onChunk func(chunk string, done bool), onReasoning func(reasoningChunk string)) (*LLMResponse, error)
}

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

type FailoverError struct {
	Reason     FailoverReason
	Provider   string
	Model      string
	Status     int
	RetryAfter time.Duration
	Wrapped    error
}

func (e *FailoverError) Error() string {
	base := fmt.Sprintf("failover(%s): provider=%s model=%s status=%d",
		e.Reason, e.Provider, e.Model, e.Status)
	if e.RetryAfter > 0 {
		base += fmt.Sprintf(" retry_after=%gs", e.RetryAfter.Seconds())
	}
	return fmt.Sprintf("%s: %v", base, e.Wrapped)
}

func (e *FailoverError) Unwrap() error {
	return e.Wrapped
}

func (e *FailoverError) IsRetriable() bool {
	return !e.IsTerminal() || e.Reason == FailoverAuth || e.Reason == FailoverBilling
}

func (e *FailoverError) IsTerminal() bool {
	switch e.Reason {
	case FailoverAuth, FailoverBilling, FailoverFormat:
		return true
	}
	return false
}

func isTerminalReason(r FailoverReason) bool {
	return (&FailoverError{Reason: r}).IsTerminal()
}

func (e *FailoverError) ShouldBackoff() bool {
	switch e.Reason {
	case FailoverRateLimit, FailoverOverloaded, FailoverTimeout, FailoverUnknown:
		return true
	}
	return false
}

type ModelConfig struct {
	Primary   string
	Fallbacks []string
}
