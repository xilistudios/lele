package openai_compat

// read_video produces messages carrying a video_url content part (either an
// http(s) URL or a data:video/...;base64 payload). The provider serializes
// messages through common.SerializeMessages, so these tests pin that video
// parts actually reach the wire in the OpenAI-compatible shape — and that
// the whole payload survives both Chat and ChatStream requests untouched.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

// videoWireMessage builds a user message carrying a video content part the
// same way the read_video tool does.
func videoWireMessage(dataURL string, fps float64) []Message {
	return []Message{{
		Role: "user",
		ContentParts: []protocoltypes.ContentPart{
			{Type: "text", Text: "analyze this video"},
			{Type: "video_url", VideoURL: &protocoltypes.VideoURL{URL: dataURL, FPS: fps}},
		},
	}}
}

// videoRequest is the decoded shape we assert against: messages[].content[]
// entries with an optional video_url object.
type videoRequest struct {
	Messages []struct {
		Content []struct {
			Type     string `json:"type"`
			VideoURL *struct {
				URL string  `json:"url"`
				FPS float64 `json:"fps"`
			} `json:"video_url"`
		} `json:"content"`
	} `json:"messages"`
}

func TestVideoContentPartSentToProvider_Chat(t *testing.T) {
	dataURL := "data:video/mp4;base64,AAAA"
	body := captureChatRequestBody(t, videoWireMessage(dataURL, 2.0))

	if !strings.Contains(body, `"type":"video_url"`) {
		t.Fatalf("request body missing video_url part: %s", body)
	}
	if !strings.Contains(body, dataURL) {
		t.Fatalf("request body missing the video data URL: %s", body)
	}

	var parsed videoRequest
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(parsed.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(parsed.Messages))
	}
	content := parsed.Messages[0].Content
	if len(content) != 2 {
		t.Fatalf("content parts = %d, want 2 (%s)", len(content), body)
	}

	textPart := content[0]
	if textPart.Type != "text" {
		t.Errorf("content[0].type = %q, want text", textPart.Type)
	}

	videoPart := content[1]
	if videoPart.Type != "video_url" {
		t.Fatalf("content[1].type = %q, want video_url", videoPart.Type)
	}
	if videoPart.VideoURL == nil {
		t.Fatal("content[1] lost its video_url payload")
	}
	if videoPart.VideoURL.URL != dataURL {
		t.Errorf("video url = %q, want %q", videoPart.VideoURL.URL, dataURL)
	}
	if videoPart.VideoURL.FPS != 2.0 {
		t.Errorf("video fps = %v, want 2.0", videoPart.VideoURL.FPS)
	}
}

func TestVideoContentPartSentToProvider_ChatStream(t *testing.T) {
	dataURL := "data:video/mp4;base64,AAAA"
	var captured string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		captured = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	p := NewProvider("k", server.URL, "")
	_, _ = p.ChatStream(context.Background(), videoWireMessage(dataURL, 1.0), nil, "gpt-4o", nil, func(string, bool) {}, nil)

	if !strings.Contains(captured, `"type":"video_url"`) {
		t.Errorf("stream request body missing video_url part: %s", captured)
	}
	if !strings.Contains(captured, dataURL) {
		t.Errorf("stream request body missing the video data URL: %s", captured)
	}
}
