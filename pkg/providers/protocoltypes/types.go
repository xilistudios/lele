// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package protocoltypes

import (
	"encoding/json"
	"strings"
)

// ToolCall represents a tool call from the LLM.
type ToolCall struct {
	ID               string                 `json:"id"`
	Type             string                 `json:"type,omitempty"`
	Function         *FunctionCall          `json:"function,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Arguments        map[string]interface{} `json:"arguments,omitempty"`
	ThoughtSignature string                 `json:"thought_signature,omitempty"`
	ExtraContent     *ExtraContent          `json:"extra_content,omitempty"`
}

// FunctionCall represents a function call within a ToolCall.
type FunctionCall struct {
	Name             string `json:"name"`
	Arguments        string `json:"arguments"`
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// ExtraContent holds provider-specific extra content.
type ExtraContent struct {
	Google *GoogleExtra `json:"google,omitempty"`
}

// GoogleExtra contains Google-specific extra fields.
type GoogleExtra struct {
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// ReasoningDetail contains reasoning information from the model.
type ReasoningDetail struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// LLMResponse represents the response from an LLM provider.
type LLMResponse struct {
	Content          string            `json:"content"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	FinishReason     string            `json:"finish_reason"`
	Usage            *UsageInfo        `json:"usage,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	Reasoning        string            `json:"reasoning,omitempty"`
	ReasoningDetails []ReasoningDetail `json:"reasoning_details,omitempty"`
}

// UsageInfo contains token usage information.
//
// CacheReadInputTokens and CacheCreationInputTokens are reported by providers
// that support prompt caching (Anthropic, Bedrock, OpenAI-compatible endpoints
// exposing prompt_tokens_details). PromptTokens always reflects the *total*
// input tokens billed for the request (cache read + cache write + uncached),
// so cache hit-rate can be derived as CacheReadInputTokens / PromptTokens.
type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	// CacheReadInputTokens counts input tokens served from prompt cache.
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`
	// CacheCreationInputTokens counts input tokens written to prompt cache.
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	// PromptTokensDetails carries the OpenAI-style usage breakdown; some
	// OpenAI-compatible endpoints (OpenRouter, DeepSeek, Gemini compat)
	// report cache hits as prompt_tokens_details.cached_tokens.
	PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

// PromptTokensDetails is the OpenAI Chat Completions usage breakdown.
type PromptTokensDetails struct {
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// NormalizeCacheUsage folds OpenAI-style prompt_tokens_details.cached_tokens
// into CacheReadInputTokens. Call after unmarshaling a provider response.
func (u *UsageInfo) NormalizeCacheUsage() {
	if u == nil || u.PromptTokensDetails == nil {
		return
	}
	if u.CacheReadInputTokens == 0 && u.PromptTokensDetails.CachedTokens > 0 {
		u.CacheReadInputTokens = u.PromptTokensDetails.CachedTokens
	}
}

// CacheHitRate returns the fraction of input tokens served from the prompt
// cache for this request, or 0 when there is no input-token data.
func (u *UsageInfo) CacheHitRate() float64 {
	if u == nil || u.PromptTokens <= 0 {
		return 0
	}
	return float64(u.CacheReadInputTokens) / float64(u.PromptTokens)
}

// ImageURL represents an image URL for multimodal content.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// ContentPart represents a part of multimodal content.
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// MessageAttachment is a file attachment associated with a message. It is the
// persistence/display counterpart of bus.FileAttachment: defined here (and NOT
// imported from pkg/bus) so the providers layer stays free of bus dependencies.
// Path is always a filesystem path the WebUI can serve via
// /api/v1/files/view (staged under the lele dir by the native channel).
//
// IMPORTANT: Attachments are metadata for the UI only. Providers build LLM
// request payloads from explicit fields (Content/ContentParts/ToolCalls/...),
// so this field is never injected into model requests.
type MessageAttachment struct {
	Name     string `json:"name,omitempty"`
	Path     string `json:"path,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Caption  string `json:"caption,omitempty"`
}

// Message represents a message in a conversation.
type Message struct {
	Role               string        `json:"role"`
	Content            string        `json:"content"`
	ContentParts       []ContentPart `json:"content_parts,omitempty"`
	ToolCalls          []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID         string        `json:"tool_call_id,omitempty"`
	Media              []string      `json:"media,omitempty"`
	ReasoningContent   string        `json:"reasoning_content,omitempty"`
	ExcludeFromContext bool          `json:"exclude_from_context,omitempty"`
	Streaming          bool          `json:"streaming,omitempty"`
	// Attachments carries file attachments delivered with this message (e.g.
	// via the send_file tool) so the WebUI can list and download them from
	// chat history. UI metadata only — never sent to LLM providers.
	Attachments []MessageAttachment `json:"attachments,omitempty"`
}

func (m *Message) MarshalJSON() ([]byte, error) {
	type rawMessage struct {
		Role               string              `json:"role"`
		Content            interface{}         `json:"content"`
		ToolCalls          []ToolCall          `json:"tool_calls,omitempty"`
		ToolCallID         string              `json:"tool_call_id,omitempty"`
		ReasoningContent   string              `json:"reasoning_content"`
		ExcludeFromContext bool                `json:"exclude_from_context,omitempty"`
		Streaming          bool                `json:"streaming,omitempty"`
		Attachments        []MessageAttachment `json:"attachments,omitempty"`
	}

	content := interface{}(m.Content)
	if len(m.ContentParts) > 0 {
		content = m.ContentParts
	}

	return json.Marshal(rawMessage{
		Role:               m.Role,
		Content:            content,
		ToolCalls:          m.ToolCalls,
		ToolCallID:         m.ToolCallID,
		ReasoningContent:   m.ReasoningContent,
		ExcludeFromContext: m.ExcludeFromContext,
		Streaming:          m.Streaming,
		Attachments:        m.Attachments,
	})
}

func (m *Message) UnmarshalJSON(data []byte) error {
	type rawMessage struct {
		Role               string              `json:"role"`
		Content            json.RawMessage     `json:"content"`
		ToolCalls          []ToolCall          `json:"tool_calls,omitempty"`
		ToolCallID         string              `json:"tool_call_id,omitempty"`
		ReasoningContent   string              `json:"reasoning_content,omitempty"`
		ExcludeFromContext bool                `json:"exclude_from_context,omitempty"`
		Streaming          bool                `json:"streaming,omitempty"`
		Attachments        []MessageAttachment `json:"attachments,omitempty"`
	}

	var raw rawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	m.Role = raw.Role
	m.ToolCalls = raw.ToolCalls
	m.ToolCallID = raw.ToolCallID
	m.ReasoningContent = raw.ReasoningContent
	m.ExcludeFromContext = raw.ExcludeFromContext
	m.Streaming = raw.Streaming
	m.Attachments = raw.Attachments
	m.Content = ""
	m.ContentParts = nil

	trimmed := strings.TrimSpace(string(raw.Content))
	if trimmed == "" || trimmed == "null" {
		return nil
	}

	var content string
	if err := json.Unmarshal(raw.Content, &content); err == nil {
		m.Content = content
		return nil
	}

	var parts []ContentPart
	if err := json.Unmarshal(raw.Content, &parts); err == nil {
		m.ContentParts = parts
		m.Content = textFromParts(parts)
		return nil
	}

	return nil
}

func (m *Message) TextContent() string {
	if strings.TrimSpace(m.Content) != "" {
		return m.Content
	}
	return textFromParts(m.ContentParts)
}

func (m *Message) HasImageContent() bool {
	for _, part := range m.ContentParts {
		if part.Type == "image_url" && part.ImageURL != nil && strings.TrimSpace(part.ImageURL.URL) != "" {
			return true
		}
	}
	return false
}

// TextOnlyContent returns a guaranteed text-only representation of the message,
// suitable for feeding into a summarization/compaction model that may not
// support vision. Image content parts are rendered as "[image]" and attached
// media entries as "[media]" placeholders, so no base64 payloads or image URLs
// ever reach the model. The result is always plain text.
func (m *Message) TextOnlyContent() string {
	var builder strings.Builder

	// Plain text content takes priority.
	if text := strings.TrimSpace(m.Content); text != "" {
		builder.WriteString(m.Content)
	}

	// Append text from multimodal content parts (images become "[image]").
	for _, part := range m.ContentParts {
		switch part.Type {
		case "text":
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(text)
		case "image_url":
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString("[image]")
		}
	}

	// Append placeholders for channel media attachments.
	for range m.Media {
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString("[media]")
	}

	return builder.String()
}

func textFromParts(parts []ContentPart) string {
	if len(parts) == 0 {
		return ""
	}

	var builder strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "text":
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(text)
		case "image_url":
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString("[image]")
		}
	}

	return builder.String()
}

// ToolDefinition represents a tool definition for the LLM.
type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function"`
}

// ToolFunctionDefinition represents the function definition within a ToolDefinition.
type ToolFunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}
