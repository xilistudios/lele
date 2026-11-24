package protocoltypes

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const marshalTestImageURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg=="

func marshalTestMessage() Message {
	return Message{
		Role:    "user",
		Content: "look at this",
		ContentParts: []ContentPart{
			{Type: "text", Text: "look at this"},
			{Type: "image_url", ImageURL: &ImageURL{URL: marshalTestImageURL, Detail: "auto"}},
			{Type: "video_url", VideoURL: &VideoURL{URL: "data:video/mp4;base64,AAAA", FPS: 2.0}},
		},
		ToolCalls: []ToolCall{{
			ID:        "call_1",
			Type:      "function",
			Name:      "read_image",
			Arguments: map[string]interface{}{"path": "/tmp/cat.png"},
			Function:  &FunctionCall{Name: "read_image", Arguments: `{"path":"/tmp/cat.png"}`},
		}},
		ToolCallID:         "call_1",
		Media:              []string{"data:image/png;base64,AAAA"},
		ReasoningContent:   "thinking",
		ExcludeFromContext: true,
		Streaming:          true,
		Attachments: []MessageAttachment{{
			Name: "report.pdf", Path: "/tmp/report.pdf", MIMEType: "application/pdf", Kind: "file", Caption: "final",
		}},
		DisplayContent: "/review src",
		Command: &CommandApplied{
			Name: "review", Description: "Review code", Args: "src", Agent: "coder", Model: "fast", Source: "workspace",
		},
		ErrorDetail: "boom",
	}
}

func TestMessageMarshalJSON_ValueAndPointerEmitTheSamePayload(t *testing.T) {
	msg := marshalTestMessage()

	byValue, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	byPointer, err := json.Marshal(&msg)
	if err != nil {
		t.Fatalf("marshal pointer: %v", err)
	}

	if string(byValue) != string(byPointer) {
		t.Fatalf("value and pointer marshal disagree:\nvalue:   %s\npointer: %s", byValue, byPointer)
	}
	if !strings.Contains(string(byValue), `"content_parts"`) {
		t.Fatalf("payload drops content_parts: %s", byValue)
	}
	if !strings.Contains(string(byValue), `"media"`) {
		t.Fatalf("payload drops media: %s", byValue)
	}
	if !strings.Contains(string(byValue), `"content":"look at this"`) {
		t.Fatalf("payload does not keep the text content: %s", byValue)
	}
}

func TestMessageJSON_RoundTripsEveryField(t *testing.T) {
	msg := marshalTestMessage()

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantCalls := msg.ToolCalls
	wantWithoutCalls := msg
	wantWithoutCalls.ToolCalls = nil
	gotWithoutCalls := got
	gotWithoutCalls.ToolCalls = nil
	if !reflect.DeepEqual(gotWithoutCalls, wantWithoutCalls) {
		t.Fatalf("round-trip lost fields:\ngot:  %+v\nwant: %+v", gotWithoutCalls, wantWithoutCalls)
	}

	if len(got.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v, want the single stored call", got.ToolCalls)
	}
	if got.ToolCalls[0].FunctionName() != wantCalls[0].FunctionName() {
		t.Errorf("tool call name = %q, want %q", got.ToolCalls[0].FunctionName(), wantCalls[0].FunctionName())
	}
	if got.ToolCalls[0].ArgumentJSON() != wantCalls[0].ArgumentJSON() {
		t.Errorf("tool call arguments = %q, want %q", got.ToolCalls[0].ArgumentJSON(), wantCalls[0].ArgumentJSON())
	}

	again, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if string(again) != string(data) {
		t.Errorf("second marshalling is not stable:\nfirst:  %s\nsecond: %s", data, again)
	}
}

func TestMessageUnmarshalJSON_KeepsContentAndContentParts(t *testing.T) {
	data := `{"role":"user","content":"look at this","content_parts":[` +
		`{"type":"text","text":"look at this"},` +
		`{"type":"image_url","image_url":{"url":"` + marshalTestImageURL + `","detail":"auto"}}],` +
		`"media":["data:image/png;base64,AAAA"],"tool_call_id":"call_1"}`

	var got Message
	if err := json.Unmarshal([]byte(data), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Content != "look at this" {
		t.Errorf("Content = %q, want %q", got.Content, "look at this")
	}
	if len(got.ContentParts) != 2 {
		t.Fatalf("ContentParts = %+v, want 2 parts", got.ContentParts)
	}
	if got.ContentParts[1].ImageURL == nil || got.ContentParts[1].ImageURL.URL != marshalTestImageURL {
		t.Errorf("image part = %+v, want the stored data URL", got.ContentParts[1])
	}
	if len(got.Media) != 1 || got.Media[0] != "data:image/png;base64,AAAA" {
		t.Errorf("Media = %+v, want the stored entry", got.Media)
	}
	if got.ToolCallID != "call_1" {
		t.Errorf("ToolCallID = %q, want call_1", got.ToolCallID)
	}
}

func TestMessageUnmarshalJSON_FoldsContentBlocks(t *testing.T) {
	data := `{"role":"user","content":[` +
		`{"type":"text","text":"Analyze the image"},` +
		`{"type":"image_url","image_url":{"url":"` + marshalTestImageURL + `"}}]}`

	var got Message
	if err := json.Unmarshal([]byte(data), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.ContentParts) != 2 {
		t.Fatalf("ContentParts = %+v, want 2 parts", got.ContentParts)
	}
	if want := "Analyze the image\n[image]"; got.Content != want {
		t.Errorf("Content = %q, want %q", got.Content, want)
	}
}

func TestMessageUnmarshalJSON_EmptyContent(t *testing.T) {
	cases := map[string]string{
		"absent":   `{"role":"assistant"}`,
		"null":     `{"role":"assistant","content":null}`,
		"empty":    `{"role":"assistant","content":""}`,
		"explicit": `{"role":"user","content":"","content_parts":[]}`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			var got Message
			if err := json.Unmarshal([]byte(data), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Content != "" {
				t.Errorf("Content = %q, want empty", got.Content)
			}
			if len(got.ContentParts) != 0 {
				t.Errorf("ContentParts = %+v, want none", got.ContentParts)
			}
		})
	}
}
