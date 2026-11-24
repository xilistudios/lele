package protocoltypes

import (
	"encoding/json"
	"strings"
)

type ToolCall struct {
	ID               string                 `json:"id"`
	Type             string                 `json:"type,omitempty"`
	Function         *FunctionCall          `json:"function,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Arguments        map[string]interface{} `json:"arguments,omitempty"`
	ThoughtSignature string                 `json:"thought_signature,omitempty"`
	ExtraContent     *ExtraContent          `json:"extra_content,omitempty"`

	ArgumentsTruncated bool `json:"-"`
}

type FunctionCall struct {
	Name             string `json:"name"`
	Arguments        string `json:"arguments"`
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

type ExtraContent struct {
	Google *GoogleExtra `json:"google,omitempty"`
}

type GoogleExtra struct {
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

type ReasoningDetail struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type LLMResponse struct {
	Content          string            `json:"content"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	FinishReason     string            `json:"finish_reason"`
	Usage            *UsageInfo        `json:"usage,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	Reasoning        string            `json:"reasoning,omitempty"`
	ReasoningDetails []ReasoningDetail `json:"reasoning_details,omitempty"`
}

type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	CacheReadInputTokens     int                  `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int                  `json:"cache_creation_input_tokens,omitempty"`
	PromptTokensDetails      *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

type PromptTokensDetails struct {
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

func (u *UsageInfo) NormalizeCacheUsage() {
	if u == nil || u.PromptTokensDetails == nil {
		return
	}
	if u.CacheReadInputTokens == 0 && u.PromptTokensDetails.CachedTokens > 0 {
		u.CacheReadInputTokens = u.PromptTokensDetails.CachedTokens
	}
}

func (u *UsageInfo) CacheHitRate() float64 {
	if u == nil || u.PromptTokens <= 0 {
		return 0
	}
	return float64(u.CacheReadInputTokens) / float64(u.PromptTokens)
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type VideoURL struct {
	URL string  `json:"url"`
	FPS float64 `json:"fps,omitempty"`
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
	VideoURL *VideoURL `json:"video_url,omitempty"`
}

type MessageAttachment struct {
	Name     string `json:"name,omitempty"`
	Path     string `json:"path,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Caption  string `json:"caption,omitempty"`
}

type Message struct {
	Role               string              `json:"role"`
	Content            string              `json:"content"`
	ContentParts       []ContentPart       `json:"content_parts,omitempty"`
	ToolCalls          []ToolCall          `json:"tool_calls,omitempty"`
	ToolCallID         string              `json:"tool_call_id,omitempty"`
	Media              []string            `json:"media,omitempty"`
	ReasoningContent   string              `json:"reasoning_content,omitempty"`
	ExcludeFromContext bool                `json:"exclude_from_context,omitempty"`
	Streaming          bool                `json:"streaming,omitempty"`
	Attachments        []MessageAttachment `json:"attachments,omitempty"`
	DisplayContent     string              `json:"display_content,omitempty"`
	Command            *CommandApplied     `json:"harness_command,omitempty"`
	ErrorDetail        string              `json:"error_detail,omitempty"`
}

type CommandApplied struct {
	Name        string `json:"command"`
	Description string `json:"description,omitempty"`
	Args        string `json:"args,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Model       string `json:"model,omitempty"`
	Source      string `json:"source,omitempty"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	type messageFields Message

	return json.Marshal(messageFields(m))
}

func (m *Message) UnmarshalJSON(data []byte) error {
	type messageFields Message

	var wire struct {
		messageFields
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	decoded := Message(wire.messageFields)
	decoded.Content = ""

	text, parts, kind := decodeStoredContent(wire.Content)
	switch kind {
	case storedContentText:
		decoded.Content = text
	case storedContentParts:
		if len(decoded.ContentParts) == 0 {
			decoded.ContentParts = parts
		}
		decoded.Content = textFromParts(decoded.ContentParts)
	}

	*m = decoded

	return nil
}

type storedContentKind int

const (
	storedContentEmpty storedContentKind = iota
	storedContentText
	storedContentParts
)

func decodeStoredContent(raw json.RawMessage) (string, []ContentPart, storedContentKind) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil, storedContentEmpty
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil, storedContentText
	}

	var parts []ContentPart
	if err := json.Unmarshal(raw, &parts); err == nil && parts != nil {
		return "", parts, storedContentParts
	}

	return "", nil, storedContentEmpty
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

func (m *Message) HasVideoContent() bool {
	for _, part := range m.ContentParts {
		if part.Type == "video_url" && part.VideoURL != nil && strings.TrimSpace(part.VideoURL.URL) != "" {
			return true
		}
	}
	return false
}

func (m *Message) TextOnlyContent() string {
	var builder strings.Builder

	if text := strings.TrimSpace(m.Content); text != "" {
		builder.WriteString(m.Content)
	}

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
		case "video_url":
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString("[video]")
		}
	}

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
		case "video_url":
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString("[video]")
		}
	}

	return builder.String()
}

type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function"`
}

type ToolFunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}
