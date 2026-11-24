package common

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
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

const DefaultResponseHeaderTimeout = DefaultRequestTimeout

const DefaultStreamIdleTimeout = 5 * time.Minute

const MaxRequestLifetime = 30 * time.Minute

const MaxResponseBodyBytes = 32 << 20

var ErrStreamTruncated = errors.New("provider stream truncated")

func StreamScanError(err error) error {
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("%w: provider sent a single SSE line larger than 1 MB", ErrStreamTruncated)
	}
	return fmt.Errorf("%w: %w", ErrStreamTruncated, err)
}

func ReadLimitedBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, MaxResponseBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxResponseBodyBytes {
		return nil, fmt.Errorf("response body exceeds the %d MB limit", MaxResponseBodyBytes>>20)
	}
	return body, nil
}

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

func NewHTTPClient(proxy string) *http.Client {
	client := &http.Client{
		Timeout: DefaultRequestTimeout,
	}
	if proxy != "" {
		client.Transport = newTransport(proxy, 0)
	}
	return client
}

func NewStreamingHTTPClient(proxy string) *http.Client {
	return &http.Client{
		Timeout:   0,
		Transport: newTransport(proxy, DefaultResponseHeaderTimeout),
	}
}

type IdleTimeoutReader struct {
	r       io.ReadCloser
	timeout time.Duration
	mu      sync.Mutex
	timer   *time.Timer
	err     error
}

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

type openaiMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

func SerializeMessages(messages []Message) []any {
	out := make([]any, 0, len(messages))
	for _, m := range messages {
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

		parts := make([]map[string]any, 0, 1+len(m.ContentParts)+len(m.Media))

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
			case "video_url":
				if part.VideoURL == nil || strings.TrimSpace(part.VideoURL.URL) == "" {
					continue
				}
				videoURL := map[string]any{"url": part.VideoURL.URL}
				if part.VideoURL.FPS > 0 {
					videoURL["fps"] = part.VideoURL.FPS
				}
				parts = append(parts, map[string]any{
					"type":      "video_url",
					"video_url": videoURL,
				})
			case "input_audio":
			}
		}

		if !hasContentParts && m.Content != "" {
			parts = append(parts, map[string]any{
				"type": "text",
				"text": m.Content,
			})
		}

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

			if strings.HasPrefix(mediaURL, "data:video/") {
				parts = append(parts, map[string]any{
					"type": "video_url",
					"video_url": map[string]any{
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

		thoughtSignature := ""
		if tc.ExtraContent != nil && tc.ExtraContent.Google != nil {
			thoughtSignature = tc.ExtraContent.Google.ThoughtSignature
		}

		argsTruncated := false
		if tc.Function != nil {
			name = tc.Function.Name
			arguments, argsTruncated = DecodeToolCallArgumentsTruncated(tc.Function.Arguments, name)
		}

		toolCall := ToolCall{
			ID:                 tc.ID,
			Name:               name,
			Arguments:          arguments,
			ThoughtSignature:   thoughtSignature,
			ArgumentsTruncated: argsTruncated,
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

func normalizeFinishReason(reason string) string {
	if reason == "length" {
		return "truncated"
	}
	return reason
}

func DecodeToolCallArguments(raw json.RawMessage, name string) map[string]any {
	arguments, _ := DecodeToolCallArgumentsTruncated(raw, name)
	return arguments
}

func DecodeToolCallArgumentsTruncated(raw json.RawMessage, name string) (map[string]any, bool) {
	arguments := make(map[string]any)
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return arguments, false
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		if repaired, ok := repairTruncatedJSONObject(raw); ok {
			return repaired, true
		}
		log.Printf("common: failed to decode tool call arguments payload for %q: %v", name, err)
		return arguments, raw[0] == '{'
	}

	switch v := decoded.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return arguments, false
		}
		inner := strings.TrimSpace(v)
		if decodedArguments, repaired := decodeJSONObjectTruncated([]byte(inner)); decodedArguments != nil {
			return decodedArguments, repaired
		}
		log.Printf("common: failed to decode tool call arguments for %q", name)
		return arguments, strings.HasPrefix(inner, "{")
	case map[string]any:
		return v, false
	default:
		log.Printf("common: unsupported tool call arguments type for %q: %T", name, decoded)
		return arguments, false
	}
}

func decodeJSONObjectTruncated(raw []byte) (map[string]any, bool) {
	var arguments map[string]any
	trimmed := bytes.TrimSpace(raw)
	if err := json.Unmarshal(trimmed, &arguments); err == nil && arguments != nil {
		return arguments, false
	}
	repaired, ok := repairTruncatedJSONObject(trimmed)
	if !ok || repaired == nil {
		return nil, false
	}
	return repaired, true
}

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

func HandleErrorResponse(resp *http.Response, apiBase string) error {
	contentType := resp.Header.Get("Content-Type")
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256))
	if readErr != nil {
		return fmt.Errorf("failed to read response: %w", readErr)
	}
	if LooksLikeHTML(body, contentType) {
		return WrapHTMLResponseError(resp.StatusCode, body, contentType, apiBase)
	}
	return NewAPIError(resp, []byte(ResponsePreview(body, 128)), apiBase)
}

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
