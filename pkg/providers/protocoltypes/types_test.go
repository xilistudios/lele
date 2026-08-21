package protocoltypes

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTextOnlyContent_PlainText(t *testing.T) {
	m := Message{Role: "user", Content: "hello world"}
	if got := m.TextOnlyContent(); got != "hello world" {
		t.Errorf("TextOnlyContent() = %q, want %q", got, "hello world")
	}
}

func TestTextOnlyContent_ContentPartsWithImage(t *testing.T) {
	m := Message{
		Role: "user",
		ContentParts: []ContentPart{
			{Type: "text", Text: "Analyze the image at /tmp/cat.png."},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==", Detail: "auto"}},
		},
	}
	got := m.TextOnlyContent()
	want := "Analyze the image at /tmp/cat.png.\n[image]"
	if got != want {
		t.Errorf("TextOnlyContent() = %q, want %q", got, want)
	}
	// Must never contain base64 payload data.
	if strings.Contains(got, "base64") || strings.Contains(got, "iVBORw0KGgo") {
		t.Errorf("TextOnlyContent() leaked image data: %q", got)
	}
}

func TestTextOnlyContent_Media(t *testing.T) {
	m := Message{
		Role:    "user",
		Content: "look at this",
		Media:   []string{"/tmp/photo1.jpg", "/tmp/photo2.jpg"},
	}
	got := m.TextOnlyContent()
	want := "look at this\n[media]\n[media]"
	if got != want {
		t.Errorf("TextOnlyContent() = %q, want %q", got, want)
	}
}

func TestTextOnlyContent_EmptyMessage(t *testing.T) {
	m := Message{Role: "assistant"}
	if got := m.TextOnlyContent(); got != "" {
		t.Errorf("TextOnlyContent() = %q, want empty", got)
	}
}

func TestTextOnlyContent_ContentAndPartsCombined(t *testing.T) {
	m := Message{
		Role:    "user",
		Content: "main text",
		ContentParts: []ContentPart{
			{Type: "text", Text: "extra text"},
		},
	}
	got := m.TextOnlyContent()
	want := "main text\nextra text"
	if got != want {
		t.Errorf("TextOnlyContent() = %q, want %q", got, want)
	}
}
func TestMessageTextContent(t *testing.T) {
	tests := []struct {
		name    string
		m       Message
		wantStr string
	}{
		{"plain content", Message{Role: "user", Content: "hello"}, "hello"},
		{"blank content returns parts text", Message{Role: "user", Content: "   ", ContentParts: []ContentPart{{Type: "text", Text: "from parts"}}}, "from parts"},
		{"empty message", Message{Role: "assistant"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.TextContent(); got != tt.wantStr {
				t.Errorf("TextContent() = %q, want %q", got, tt.wantStr)
			}
		})
	}
}

func TestMessageHasImageContent(t *testing.T) {
	tests := []struct {
		name string
		m    Message
		want bool
	}{
		{"no parts", Message{Role: "user", Content: "hi"}, false},
		{"text part only", Message{ContentParts: []ContentPart{{Type: "text", Text: "x"}}}, false},
		{"image with url", Message{ContentParts: []ContentPart{{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}}}}, true},
		{"image with empty url", Message{ContentParts: []ContentPart{{Type: "image_url", ImageURL: &ImageURL{URL: "  "}}}}, false},
		{"nil image url", Message{ContentParts: []ContentPart{{Type: "image_url", ImageURL: nil}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.HasImageContent(); got != tt.want {
				t.Errorf("HasImageContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMessageMarshalJSON(t *testing.T) {
	t.Run("plain content", func(t *testing.T) {
		m := Message{Role: "user", Content: "hi", ReasoningContent: "r", ExcludeFromContext: true, Streaming: true}
		data, err := m.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON() error: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if decoded["content"] != "hi" {
			t.Errorf("content = %v, want hi", decoded["content"])
		}
		if decoded["role"] != "user" {
			t.Errorf("role = %v, want user", decoded["role"])
		}
		if decoded["reasoning_content"] != "r" {
			t.Errorf("reasoning_content = %v, want r", decoded["reasoning_content"])
		}
		if decoded["exclude_from_context"] != true {
			t.Errorf("exclude_from_context = %v, want true", decoded["exclude_from_context"])
		}
		if decoded["streaming"] != true {
			t.Errorf("streaming = %v, want true", decoded["streaming"])
		}
	})

	t.Run("content parts override content", func(t *testing.T) {
		m := Message{
			Role:         "user",
			Content:      "plain",
			ContentParts: []ContentPart{{Type: "text", Text: "part text"}},
		}
		data, err := m.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON() error: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		parts, ok := decoded["content"].([]any)
		if !ok {
			t.Fatalf("content type = %T, want []any", decoded["content"])
		}
		if len(parts) != 1 {
			t.Fatalf("parts length = %d, want 1", len(parts))
		}
		first, ok := parts[0].(map[string]any)
		if !ok {
			t.Fatalf("part type = %T, want map", parts[0])
		}
		if first["text"] != "part text" {
			t.Errorf("part text = %v, want part text", first["text"])
		}
	})

	t.Run("tool calls and tool call id", func(t *testing.T) {
		m := Message{
			Role:       "assistant",
			ToolCallID: "call_1",
			ToolCalls: []ToolCall{{
				ID:   "call_1",
				Name: "get_weather",
				Function: &FunctionCall{
					Name:      "get_weather",
					Arguments: "{}",
				},
			}},
		}
		data, err := m.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON() error: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if decoded["tool_call_id"] != "call_1" {
			t.Errorf("tool_call_id = %v, want call_1", decoded["tool_call_id"])
		}
		tcs := decoded["tool_calls"].([]any)
		if len(tcs) != 1 {
			t.Fatalf("tool_calls length = %d, want 1", len(tcs))
		}
	})

	t.Run("zero message", func(t *testing.T) {
		var m Message
		data, err := m.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON() error: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if decoded["content"] == nil {
			t.Error("content should not be nil")
		}
	})
}

func TestMessageUnmarshalJSON(t *testing.T) {
	t.Run("plain content string", func(t *testing.T) {
		var m Message
		err := m.UnmarshalJSON([]byte(`{"role":"user","content":"hello world","tool_call_id":"tc1","reasoning_content":"rc"}`))
		if err != nil {
			t.Fatalf("UnmarshalJSON() error: %v", err)
		}
		if m.Content != "hello world" {
			t.Errorf("Content = %q, want hello world", m.Content)
		}
		if m.Role != "user" {
			t.Errorf("Role = %q, want user", m.Role)
		}
		if m.ToolCallID != "tc1" {
			t.Errorf("ToolCallID = %q, want tc1", m.ToolCallID)
		}
		if m.ReasoningContent != "rc" {
			t.Errorf("ReasoningContent = %q, want rc", m.ReasoningContent)
		}
	})

	t.Run("content parts array", func(t *testing.T) {
		var m Message
		err := m.UnmarshalJSON([]byte(`{"role":"user","content":[{"type":"text","text":"a"},{"type":"image_url","image_url":{"url":"data:image/png;base64,xx"}}]}`))
		if err != nil {
			t.Fatalf("UnmarshalJSON() error: %v", err)
		}
		if len(m.ContentParts) != 2 {
			t.Fatalf("ContentParts length = %d, want 2", len(m.ContentParts))
		}
		// Content should be populated from textFromParts
		if m.Content != "a\n[image]" {
			t.Errorf("Content = %q, want %q", m.Content, "a\n[image]")
		}
	})

	t.Run("null content", func(t *testing.T) {
		var m Message
		err := m.UnmarshalJSON([]byte(`{"role":"assistant","content":null}`))
		if err != nil {
			t.Fatalf("UnmarshalJSON() error: %v", err)
		}
		if m.Content != "" {
			t.Errorf("Content = %q, want empty", m.Content)
		}
	})

	t.Run("empty content", func(t *testing.T) {
		var m Message
		err := m.UnmarshalJSON([]byte(`{"role":"assistant","content":""}`))
		if err != nil {
			t.Fatalf("UnmarshalJSON() error: %v", err)
		}
		if m.Content != "" {
			t.Errorf("Content = %q, want empty", m.Content)
		}
	})

	t.Run("content parts with omitted content field", func(t *testing.T) {
		var m Message
		err := m.UnmarshalJSON([]byte(`{"role":"user"}`))
		if err != nil {
			t.Fatalf("UnmarshalJSON() error: %v", err)
		}
		if m.Content != "" {
			t.Errorf("Content = %q, want empty", m.Content)
		}
		if m.ContentParts != nil {
			t.Errorf("ContentParts = %v, want nil", m.ContentParts)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		var m Message
		err := m.UnmarshalJSON([]byte(`{invalid`))
		if err == nil {
			t.Fatal("expected error for invalid json")
		}
	})

	t.Run("content is number falls through silently", func(t *testing.T) {
		var m Message
		err := m.UnmarshalJSON([]byte(`{"role":"user","content":123}`))
		if err != nil {
			t.Fatalf("UnmarshalJSON() error: %v", err)
		}
		if m.Content != "" {
			t.Errorf("Content = %q, want empty", m.Content)
		}
		if m.ContentParts != nil {
			t.Errorf("ContentParts = %v, want nil", m.ContentParts)
		}
	})

	t.Run("roundtrip via encoding/json custom marshaler", func(t *testing.T) {
		// protocoltypes.Message implements both MarshalJSON and UnmarshalJSON,
		// so encoding/json should route through the custom ones automatically.
		orig := Message{Role: "user", Content: "round trip", Streaming: true}
		data, err := json.Marshal(&orig)
		if err != nil {
			t.Fatalf("json.Marshal error: %v", err)
		}
		var back Message
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("json.Unmarshal error: %v", err)
		}
		if back.Content != "round trip" {
			t.Errorf("roundtrip Content = %q, want round trip", back.Content)
		}
		if !back.Streaming {
			t.Errorf("roundtrip Streaming = false, want true")
		}
	})
}

func TestTextFromParts(t *testing.T) {
	t.Run("empty parts", func(t *testing.T) {
		if got := textFromParts(nil); got != "" {
			t.Errorf("textFromParts(nil) = %q, want empty", got)
		}
	})
	t.Run("text and image", func(t *testing.T) {
		parts := []ContentPart{
			{Type: "text", Text: "first"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "http://x"}},
			{Type: "text", Text: "second"},
		}
		want := "first\n[image]\nsecond"
		if got := textFromParts(parts); got != want {
			t.Errorf("textFromParts() = %q, want %q", got, want)
		}
	})
	t.Run("blank text skipped, no leading separator", func(t *testing.T) {
		parts := []ContentPart{{Type: "text", Text: "   "}, {Type: "text", Text: "real"}}
		if got := textFromParts(parts); got != "real" {
			t.Errorf("textFromParts() = %q, want %q", got, "real")
		}
	})
}