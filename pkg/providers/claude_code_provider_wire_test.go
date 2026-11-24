package providers

import (
	"encoding/json"
	"strings"
	"testing"
)

const bridgeContentPartsImageURL = "data:image/png;base64,abcd"

func TestClaudeBridgeChatRequest_CarriesContentParts(t *testing.T) {
	imageTurn := Message{
		Role: "user",
		ContentParts: []ContentPart{
			{Type: "text", Text: "what is in this image?"},
			{Type: "image_url", ImageURL: &ImageURL{URL: bridgeContentPartsImageURL, Detail: "auto"}},
		},
	}

	request := buildClaudeBridgeChatRequest([]Message{imageTurn}, nil, "claude-code", true)

	wire, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal bridge request: %v", err)
	}
	if !strings.Contains(string(wire), `"content_parts"`) {
		t.Fatalf("bridge request drops content_parts: %s", wire)
	}
	if !strings.Contains(string(wire), bridgeContentPartsImageURL) {
		t.Fatalf("bridge request drops the image payload: %s", wire)
	}

	var decoded struct {
		Messages []struct {
			Content      json.RawMessage `json:"content"`
			ContentParts []ContentPart   `json:"content_parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("decode bridge request: %v", err)
	}
	if len(decoded.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(decoded.Messages))
	}

	var text string
	if err := json.Unmarshal(decoded.Messages[0].Content, &text); err != nil {
		t.Fatalf("bridge message content is not a JSON string: %s", decoded.Messages[0].Content)
	}
	if text != "" {
		t.Errorf("bridge message content = %q, want empty (the parts carry the turn)", text)
	}
	if len(decoded.Messages[0].ContentParts) != 2 {
		t.Fatalf("bridge message parts = %+v, want the 2 stored parts", decoded.Messages[0].ContentParts)
	}
	if decoded.Messages[0].ContentParts[1].ImageURL == nil ||
		decoded.Messages[0].ContentParts[1].ImageURL.URL != bridgeContentPartsImageURL {
		t.Errorf("bridge message image part = %+v, want the stored data URL", decoded.Messages[0].ContentParts[1])
	}
}
