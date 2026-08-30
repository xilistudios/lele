// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

// Package common provides shared utilities used by multiple LLM provider
// implementations (openai_compat, azure, etc.).
package common

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

// Re-export protocol types used across providers.
type (
	ToolCall               = protocoltypes.ToolCall
	FunctionCall           = protocoltypes.FunctionCall
	LLMResponse            = protocoltypes.LLMResponse
	UsageInfo              = protocoltypes.UsageInfo
	Message                = protocoltypes.Message
	ToolDefinition         = protocoltypes.ToolDefinition
	ToolFunctionDefinition = protocoltypes.ToolFunctionDefinition
	ExtraContent           = protocoltypes.ExtraContent
	GoogleExtra            = protocoltypes.GoogleExtra
	ReasoningDetail        = protocoltypes.ReasoningDetail
)

const DefaultRequestTimeout = 120 * time.Second

// DefaultResponseHeaderTimeout is how long to wait for the server to send
// response headers before giving up (detects a hung/dead server).
const DefaultResponseHeaderTimeout = DefaultRequestTimeout

// DefaultStreamIdleTimeout is the maximum time allowed between streamed
// bytes before the connection is considered stalled. This is the guard used
// for long-reasoning models: the request may run for many minutes as long as
// data keeps flowing.
//
// It must stay well above DefaultRequestTimeout: some gateways buffer a
// reasoning model's thinking tokens and emit nothing until the answer starts,
// so a tight idle window would reintroduce the very failure this replaces.
const DefaultStreamIdleTimeout = 5 * time.Minute

// MaxRequestLifetime is an absolute upper bound on a single LLM request,
// applied on top of the idle timeout. An idle timeout alone cannot detect a
// misbehaving upstream that dribbles a byte every few seconds forever, so this
// acts as defence in depth. It is deliberately far above any legitimate
// reasoning time.
const MaxRequestLifetime = 30 * time.Minute

// newTransport builds an *http.Transport cloning http.DefaultTransport and
// applying an optional proxy. headerTimeout is the ResponseHeaderTimeout; pass
// 0 to leave it unset.
func newTransport(proxy string, headerTimeout time.Duration) *http.Transport {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		tr := base.Clone()
		if headerTimeout > 0 {
			tr.ResponseHeaderTimeout = headerTimeout
		}
		if proxy != "" {
			parsed, err := url.Parse(proxy)
			if err == nil {
				tr.Proxy = http.ProxyURL(parsed)
			} else {
				log.Printf("common: invalid proxy URL %q: %v", proxy, err)
			}
		}
		return tr
	}
	// Fallback: minimal transport if DefaultTransport is not *http.Transport.
	tr := &http.Transport{}
	if headerTimeout > 0 {
		tr.ResponseHeaderTimeout = headerTimeout
	}
	if proxy != "" {
		parsed, err := url.Parse(proxy)
		if err == nil {
			tr.Proxy = http.ProxyURL(parsed)
		}
	}
	return tr
}

// NewHTTPClient creates an *http.Client with an optional proxy and the default timeout.
func NewHTTPClient(proxy string) *http.Client {
	client := &http.Client{
		Timeout: DefaultRequestTimeout,
	}
	if proxy != "" {
		client.Transport = newTransport(proxy, 0)
	}
	return client
}

// NewStreamingHTTPClient creates an *http.Client tuned for long-lived
// streaming responses: no total timeout (a reasoning model may stream for
// minutes), but a ResponseHeaderTimeout so a hung/dead server is still
// detected quickly.
func NewStreamingHTTPClient(proxy string) *http.Client {
	return &http.Client{
		Timeout:   0,
		Transport: newTransport(proxy, DefaultResponseHeaderTimeout),
	}
}

// IdleTimeoutReader enforces a maximum idle time between reads. If no data
// arrives within `timeout`, the underlying reader is closed and Read returns a
// descriptive error. Long-lived streams can run indefinitely as long as data
// keeps flowing, while a stalled connection is still detected.
type IdleTimeoutReader struct {
	r       io.ReadCloser
	timeout time.Duration
	mu      sync.Mutex
	timer   *time.Timer
	err     error
}

// NewIdleTimeoutReader wraps r with an idle timeout. If timeout <= 0, r is
// returned unchanged (no idle timeout).
func NewIdleTimeoutReader(r io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if timeout <= 0 {
		return r
	}
	return &IdleTimeoutReader{r: r, timeout: timeout}
}

func (itr *IdleTimeoutReader) Read(p []byte) (int, error) {
	itr.mu.Lock()
	if itr.err != nil {
		err := itr.err
		itr.mu.Unlock()
		return 0, err
	}
	if itr.timer == nil {
		itr.timer = time.AfterFunc(itr.timeout, func() {
			itr.mu.Lock()
			if itr.err == nil {
				itr.err = fmt.Errorf("stream read idle timeout: no data for %s", itr.timeout)
			}
			itr.mu.Unlock()
			_ = itr.r.Close()
		})
	}
	itr.timer.Reset(itr.timeout)
	itr.mu.Unlock()

	n, err := itr.r.Read(p)

	itr.mu.Lock()
	if itr.timer != nil {
		itr.timer.Stop()
	}
	if err != nil && itr.err != nil {
		err = itr.err
	}
	itr.mu.Unlock()

	return n, err
}

// Close closes the underlying reader, stopping the idle timeout timer.
func (itr *IdleTimeoutReader) Close() error {
	itr.mu.Lock()
	if itr.timer != nil {
		itr.timer.Stop()
		itr.timer = nil
	}
	itr.mu.Unlock()
	err := itr.r.Close()
	itr.mu.Lock()
	if itr.err == nil {
		itr.err = err
	}
	itr.mu.Unlock()
	return err
}

// --- Message serialization ---

// openaiMessage is the wire-format message for OpenAI-compatible APIs.
// It mirrors protocoltypes.Message but omits SystemParts, which is an
// internal field that would be unknown to third-party endpoints.
type openaiMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

// SerializeMessages converts internal Message structs to the OpenAI wire format.
//   - Strips SystemParts (unknown to third-party endpoints)
//   - Converts messages with ContentParts to multipart content (text + image_url)
//   - Converts messages with Media to multipart content format (text + image_url parts)
//   - If both ContentParts and Media are present, merges them (ContentParts first)
//   - Preserves ToolCallID, ToolCalls, and ReasoningContent for all messages
func SerializeMessages(messages []Message) []any {
	out := make([]any, 0, len(messages))
	for _, m := range messages {
		// Determine if we need multipart content
		hasContentParts := len(m.ContentParts) > 0
		hasMedia := len(m.Media) > 0

		if !hasContentParts && !hasMedia {
			out = append(out, openaiMessage{
				Role:             m.Role,
				Content:          m.Content,
				ReasoningContent: m.ReasoningContent,
				ToolCalls:        m.ToolCalls,
				ToolCallID:       m.ToolCallID,
			})
			continue
		}

		// Multipart content format for messages with images/media
		parts := make([]map[string]any, 0, 1+len(m.ContentParts)+len(m.Media))

		// First, add ContentParts if present (from read_image tool etc.)
		for _, part := range m.ContentParts {
			switch part.Type {
			case "text":
				if strings.TrimSpace(part.Text) != "" {
					parts = append(parts, map[string]any{
						"type": "text",
						"text": part.Text,
					})
				}
			case "image_url":
				if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
					continue
				}
				imageURL := map[string]any{"url": part.ImageURL.URL}
				if part.ImageURL.Detail != "" {
					imageURL["detail"] = part.ImageURL.Detail
				}
				parts = append(parts, map[string]any{
					"type":      "image_url",
					"image_url": imageURL,
				})
			case "input_audio":
				// Input audio is only available via Media path
			}
		}

		// Then, add text content if present (and not already added via ContentParts)
		if m.Content != "" {
			parts = append(parts, map[string]any{
				"type": "text",
				"text": m.Content,
			})
		}

		// Then, add Media if present (from channel attachments)
		for _, mediaURL := range m.Media {
			if strings.HasPrefix(mediaURL, "data:image/") {
				parts = append(parts, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": mediaURL,
					},
				})
				continue
			}

			if format, data, ok := parseDataAudioURL(mediaURL); ok {
				parts = append(parts, map[string]any{
					"type": "input_audio",
					"input_audio": map[string]any{
						"data":   data,
						"format": format,
					},
				})
			}
		}

		msg := map[string]any{
			"role":    m.Role,
			"content": parts,
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}
		if m.Role == "assistant" {
			// Always include reasoning_content for assistant messages.
			// Some providers (e.g. Moonshot AI) require it when reasoning/thinking is
			// enabled, even if the content is empty. Missing the field causes 400 errors.
			msg["reasoning_content"] = m.ReasoningContent
		}
		out = append(out, msg)
	}
	return out
}

func parseDataAudioURL(mediaURL string) (format, data string, ok bool) {
	if !strings.HasPrefix(mediaURL, "data:audio/") {
		return "", "", false
	}

	payload := strings.TrimPrefix(mediaURL, "data:audio/")
	meta, data, found := strings.Cut(payload, ",")
	if !found {
		return "", "", false
	}

	format, _, _ = strings.Cut(meta, ";")
	format = strings.TrimSpace(format)
	data = strings.TrimSpace(data)
	if format == "" || data == "" {
		return "", "", false
	}
	return format, data, true
}

// --- Response parsing ---

// ParseResponse parses a JSON chat completion response body into an LLMResponse.
func ParseResponse(body io.Reader) (*LLMResponse, error) {
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content          string            `json:"content"`
				ReasoningContent string            `json:"reasoning_content"`
				Reasoning        string            `json:"reasoning"`
				ReasoningDetails []ReasoningDetail `json:"reasoning_details"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function *struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
					ExtraContent *struct {
						Google *struct {
							ThoughtSignature string `json:"thought_signature"`
						} `json:"google"`
					} `json:"extra_content"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *UsageInfo `json:"usage"`
	}

	if err := json.NewDecoder(body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return &LLMResponse{
			Content:      "",
			FinishReason: "stop",
		}, nil
	}

	choice := apiResponse.Choices[0]
	toolCalls := make([]ToolCall, 0, len(choice.Message.ToolCalls))
	for _, tc := range choice.Message.ToolCalls {
		arguments := make(map[string]any)
		name := ""

		// Extract thought_signature from Gemini/Google-specific extra content
		thoughtSignature := ""
		if tc.ExtraContent != nil && tc.ExtraContent.Google != nil {
			thoughtSignature = tc.ExtraContent.Google.ThoughtSignature
		}

		if tc.Function != nil {
			name = tc.Function.Name
			arguments = DecodeToolCallArguments(tc.Function.Arguments, name)
		}

		toolCall := ToolCall{
			ID:               tc.ID,
			Name:             name,
			Arguments:        arguments,
			ThoughtSignature: thoughtSignature,
		}

		if thoughtSignature != "" {
			toolCall.ExtraContent = &ExtraContent{
				Google: &GoogleExtra{
					ThoughtSignature: thoughtSignature,
				},
			}
		}

		toolCalls = append(toolCalls, toolCall)
	}

	return &LLMResponse{
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
		Reasoning:        choice.Message.Reasoning,
		ReasoningDetails: choice.Message.ReasoningDetails,
		ToolCalls:        toolCalls,
		FinishReason:     normalizeFinishReason(choice.FinishReason),
		Usage:            apiResponse.Usage,
	}, nil
}

// normalizeFinishReason normalizes finish_reason values across providers.
// Converts "length" to "truncated" for consistent handling.
func normalizeFinishReason(reason string) string {
	if reason == "length" {
		return "truncated"
	}
	return reason
}

// DecodeToolCallArguments decodes a tool call's arguments from raw JSON.
func DecodeToolCallArguments(raw json.RawMessage, name string) map[string]any {
	arguments := make(map[string]any)
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return arguments
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		if repaired, ok := repairTruncatedJSONObject(raw); ok {
			return repaired
		}
		log.Printf("common: failed to decode tool call arguments payload for %q: %v", name, err)
		return arguments
	}

	switch v := decoded.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return arguments
		}
		if decodedArguments, ok := decodeJSONObject([]byte(v)); ok {
			return decodedArguments
		}
		log.Printf("common: failed to decode tool call arguments for %q", name)
		return arguments
	case map[string]any:
		return v
	default:
		log.Printf("common: unsupported tool call arguments type for %q: %T", name, decoded)
		return arguments
	}
}

func decodeJSONObject(raw []byte) (map[string]any, bool) {
	var arguments map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &arguments); err == nil && arguments != nil {
		return arguments, true
	}
	return repairTruncatedJSONObject(raw)
}

// repairTruncatedJSONObject recovers the fields from an object cut off by a
// provider at the end of a response. It deliberately returns an empty map
// when recovery is not safe instead of passing a synthetic "raw" argument to
// the tool, since raw is not part of any tool schema.
func repairTruncatedJSONObject(raw []byte) (map[string]any, bool) {
	truncated := bytes.TrimSpace(raw)
	if len(truncated) == 0 || truncated[0] != '{' {
		return nil, false
	}

	stack := make([]byte, 0, 4)
	inString := false
	escaped := false
	for _, b := range truncated {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch b {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, b)
		case '}', ']':
			if len(stack) == 0 || (b == '}' && stack[len(stack)-1] != '{') || (b == ']' && stack[len(stack)-1] != '[') {
				return nil, false
			}
			stack = stack[:len(stack)-1]
		}
	}

	if !inString && len(stack) == 0 {
		return nil, false
	}

	if escaped {
		truncated = append(truncated, '\\')
	}
	if inString {
		truncated = append(truncated, '"')
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			truncated = append(truncated, '}')
		} else {
			truncated = append(truncated, ']')
		}
	}

	var repaired map[string]any
	if err := json.Unmarshal(truncated, &repaired); err != nil || repaired == nil {
		return nil, false
	}
	return repaired, true
}

// --- HTTP response helpers ---

// HandleErrorResponse reads a non-200 response body and returns an appropriate error.
func HandleErrorResponse(resp *http.Response, apiBase string) error {
	contentType := resp.Header.Get("Content-Type")
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256))
	if readErr != nil {
		return fmt.Errorf("failed to read response: %w", readErr)
	}
	if LooksLikeHTML(body, contentType) {
		return WrapHTMLResponseError(resp.StatusCode, body, contentType, apiBase)
	}
	return fmt.Errorf(
		"API request failed:\n  Status: %d\n  Body:   %s",
		resp.StatusCode,
		ResponsePreview(body, 128),
	)
}

// ReadAndParseResponse peeks at the response body to detect HTML errors,
// then parses the JSON response into an LLMResponse.
func ReadAndParseResponse(resp *http.Response, apiBase string) (*LLMResponse, error) {
	contentType := resp.Header.Get("Content-Type")
	reader := bufio.NewReader(resp.Body)
	prefix, err := reader.Peek(256)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return nil, fmt.Errorf("failed to inspect response: %w", err)
	}
	if LooksLikeHTML(prefix, contentType) {
		return nil, WrapHTMLResponseError(resp.StatusCode, prefix, contentType, apiBase)
	}
	out, err := ParseResponse(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}
	return out, nil
}

// LooksLikeHTML checks if the response body appears to be HTML.
func LooksLikeHTML(body []byte, contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml") {
		return true
	}
	prefix := bytes.ToLower(leadingTrimmedPrefix(body, 128))
	return bytes.HasPrefix(prefix, []byte("<!doctype html")) ||
		bytes.HasPrefix(prefix, []byte("<html")) ||
		bytes.HasPrefix(prefix, []byte("<head")) ||
		bytes.HasPrefix(prefix, []byte("<body"))
}

// WrapHTMLResponseError creates a descriptive error for HTML responses.
func WrapHTMLResponseError(statusCode int, body []byte, contentType, apiBase string) error {
	respPreview := ResponsePreview(body, 128)
	return fmt.Errorf(
		"API request failed: %s returned HTML instead of JSON (content-type: %s); check api_base or proxy configuration.\n  Status: %d\n  Body:   %s",
		apiBase,
		contentType,
		statusCode,
		respPreview,
	)
}

// ResponsePreview returns a truncated preview of response body for error messages.
func ResponsePreview(body []byte, maxLen int) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "<empty>"
	}
	if len(trimmed) <= maxLen {
		return string(trimmed)
	}
	return string(trimmed[:maxLen]) + "..."
}

// SanitizeToolCallID ensures a tool call ID is valid for all providers.
// Anthropic requires IDs to match ^[a-zA-Z0-9_-]+$. Any character outside
// this set is replaced with '_'. If the result is empty, a fallback ID is returned.
func SanitizeToolCallID(id string) string {
	if id == "" {
		return "tool_call_0"
	}
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	result := b.String()
	if result == "" {
		return "tool_call_0"
	}
	return result
}

func leadingTrimmedPrefix(body []byte, maxLen int) []byte {
	i := 0
	for i < len(body) {
		switch body[i] {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			i++
		default:
			end := i + maxLen
			if end > len(body) {
				end = len(body)
			}
			return body[i:end]
		}
	}
	return nil
}

// --- Numeric helpers ---

// AsInt converts various numeric types to int.
func AsInt(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int64:
		return int(val), true
	case float64:
		return int(val), true
	case float32:
		return int(val), true
	default:
		return 0, false
	}
}

// AsFloat converts various numeric types to float64.
func AsFloat(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}
