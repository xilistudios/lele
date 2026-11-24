package anthropicmessages

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

func jsonRoundTripMessage(t *testing.T, msg Message) Message {
	t.Helper()
	encoded, err := json.Marshal(&msg)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	var reloaded Message
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	return reloaded
}

func marshalBody(t *testing.T, body map[string]any) string {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return string(data)
}

func TestBuildRequestBodyVisionImageOnWire(t *testing.T) {
	body, err := buildRequestBody([]Message{{
		Role: "user",
		ContentParts: []protocoltypes.ContentPart{
			{Type: "text", Text: "what is in this image?"},
			{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "data:image/png;base64,abcd"}},
		},
	}}, nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("buildRequestBody() error: %v", err)
	}

	wire := marshalBody(t, body)
	for _, want := range []string{`"type":"image"`, `"media_type":"image/png"`, `"data":"abcd"`, `"type":"base64"`} {
		if !strings.Contains(wire, want) {
			t.Fatalf("wire body missing %s: %s", want, wire)
		}
	}
	if got := strings.Count(wire, "what is in this image?"); got != 1 {
		t.Fatalf("prompt text appears %d times, want 1: %s", got, wire)
	}
}

func TestBuildRequestBodyMediaOnlyNotEmpty(t *testing.T) {
	body, err := buildRequestBody([]Message{{
		Role:  "user",
		Media: []string{"data:image/png;base64,zzz"},
	}}, nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("buildRequestBody() error: %v", err)
	}

	wire := marshalBody(t, body)
	if !strings.Contains(wire, `"media_type":"image/png"`) || !strings.Contains(wire, `"data":"zzz"`) {
		t.Fatalf("media-only turn lost its image block: %s", wire)
	}
	if strings.Contains(wire, `"content":""`) {
		t.Fatalf("media-only turn produced empty string content: %s", wire)
	}
}

func TestBuildRequestBodyReloadedNoDuplication(t *testing.T) {
	live := Message{
		Role: "user",
		ContentParts: []protocoltypes.ContentPart{
			{Type: "text", Text: "Analyze shot.png"},
			{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "data:image/png;base64,abcd"}},
		},
	}

	reloaded := jsonRoundTripMessage(t, live)
	if reloaded.Content == "" {
		t.Fatal("expected reloaded message to carry synthesized Content")
	}

	liveBody, err := buildRequestBody([]Message{live}, nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("buildRequestBody(live) error: %v", err)
	}
	reloadedBody, err := buildRequestBody([]Message{reloaded}, nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("buildRequestBody(reloaded) error: %v", err)
	}

	liveWire := marshalBody(t, liveBody)
	reloadedWire := marshalBody(t, reloadedBody)
	if liveWire != reloadedWire {
		t.Fatalf("wire body differs after reload:\nlive:     %s\nreloaded: %s", liveWire, reloadedWire)
	}
	if got := strings.Count(reloadedWire, "Analyze shot.png"); got != 1 {
		t.Fatalf("prompt text appears %d times, want 1: %s", got, reloadedWire)
	}
	if strings.Contains(reloadedWire, "[image]") {
		t.Fatalf("reloaded wire body contains the [image] placeholder: %s", reloadedWire)
	}
}

func TestBuildRequestBodyEmptyAssistantSkipped(t *testing.T) {
	body, err := buildRequestBody([]Message{
		{Role: "user", Content: "hi"},
		{
			Role:      "assistant",
			ToolCalls: []ToolCall{{ID: "call_1", Name: "  ", Arguments: map[string]any{"x": 1}}},
		},
		{Role: "user", Content: "again"},
	}, nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("buildRequestBody() error: %v", err)
	}

	messages, _ := body["messages"].([]any)
	if len(messages) != 2 {
		got, _ := json.Marshal(messages)
		t.Fatalf("expected the empty assistant turn to be dropped, got %d messages: %s", len(messages), got)
	}
	wire := marshalBody(t, body)
	if strings.Contains(wire, `"content":[]`) {
		t.Fatalf("wire body contains an empty content array: %s", wire)
	}
}

func TestBuildRequestBodyPartsFallbacks(t *testing.T) {
	tests := []struct {
		name     string
		message  Message
		wantText string
	}{
		{
			name: "remote image url becomes text",
			message: Message{
				Role: "user",
				ContentParts: []protocoltypes.ContentPart{
					{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "https://example.com/cat.png"}},
				},
			},
			wantText: "https://example.com/cat.png",
		},
		{
			name: "video part becomes text",
			message: Message{
				Role: "user",
				ContentParts: []protocoltypes.ContentPart{
					{Type: "video_url", VideoURL: &protocoltypes.VideoURL{URL: "https://example.com/clip.mp4"}},
				},
			},
			wantText: "https://example.com/clip.mp4",
		},
		{
			name: "video media becomes text",
			message: Message{
				Role:  "user",
				Media: []string{"data:video/mp4;base64,zzz"},
			},
			wantText: "video",
		},
		{
			name:     "empty plain message repaired",
			message:  Message{Role: "user"},
			wantText: "[no content]",
		},
		{
			name: "blank parts repaired with placeholder",
			message: Message{
				Role: "user",
				ContentParts: []protocoltypes.ContentPart{
					{Type: "text", Text: "   "},
				},
			},
			wantText: "[no content]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := buildRequestBody([]Message{tt.message}, nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 1024})
			if err != nil {
				t.Fatalf("buildRequestBody() error: %v", err)
			}
			wire := marshalBody(t, body)
			if !strings.Contains(wire, tt.wantText) {
				t.Fatalf("wire body missing %q: %s", tt.wantText, wire)
			}
			if strings.Contains(wire, `"content":""`) {
				t.Fatalf("wire body contains empty string content: %s", wire)
			}
		})
	}
}
