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
type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
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

// Message represents a message in a conversation.
type Message struct {
	Role             string          `json:"-"`
	Content          string          `json:"-"`
	ContentParts     []ContentPart   `json:"-"`
	ToolCalls        []ToolCall      `json:"-"`
	ToolCallID       string          `json:"-"`
	Media            []string        `json:"-"`
	ReasoningContent string          `json:"-"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	type rawMessage struct {
		Role             string      `json:"role"`
		Content          interface{} `json:"content"`
		ToolCalls        []ToolCall  `json:"tool_calls,omitempty"`
		ToolCallID       string      `json:"tool_call_id,omitempty"`
		ReasoningContent string      `json:"reasoning_content,omitempty"`
	}

	content := interface{}(m.Content)
	if len(m.ContentParts) > 0 {
		content = m.ContentParts
	}

	return json.Marshal(rawMessage{
		Role:             m.Role,
		Content:          content,
		ToolCalls:        m.ToolCalls,
		ToolCallID:       m.ToolCallID,
		ReasoningContent: m.ReasoningContent,
	})
}

func (m *Message) UnmarshalJSON(data []byte) error {
	type rawMessage struct {
		Role             string          `json:"role"`
		Content          json.RawMessage `json:"content"`
		ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
		ToolCallID       string          `json:"tool_call_id,omitempty"`
		ReasoningContent string          `json:"reasoning_content,omitempty"`
	}

	var raw rawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	m.Role = raw.Role
	m.ToolCalls = raw.ToolCalls
	m.ToolCallID = raw.ToolCallID
	m.ReasoningContent = raw.ReasoningContent
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

func (m Message) TextContent() string {
	if strings.TrimSpace(m.Content) != "" {
		return m.Content
	}
	return textFromParts(m.ContentParts)
}

func (m Message) HasImageContent() bool {
	for _, part := range m.ContentParts {
		if part.Type == "image_url" && part.ImageURL != nil && strings.TrimSpace(part.ImageURL.URL) != "" {
			return true
		}
	}
	return false
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
