package openai_compat

// send_file attachments are UI metadata persisted on session messages. The
// openai_compat provider passes the messages slice straight into the request
// body, which is marshalled through protocoltypes.Message.MarshalJSON — so a
// leak here would send unknown fields to every OpenAI-compatible endpoint.
// These tests pin that the wire format never carries attachments.

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

func captureChatRequestBody(t *testing.T, messages []Message) string {
	t.Helper()
	var captured string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		captured = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(server.Close)

	p := NewProvider("k", server.URL, "")
	if _, err := p.Chat(context.Background(), messages, nil, "gpt-4o", nil); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	return captured
}

func TestAttachmentsNotSentToProvider_Chat(t *testing.T) {
	msgs := []Message{{
		Role:    "assistant",
		Content: "here is your file",
		Attachments: []protocoltypes.MessageAttachment{
			{Name: "report.pdf", Path: "/home/u/.lele/tmp/attachments/aa_report.pdf", MIMEType: "application/pdf", Kind: "file", Caption: "c"},
		},
	}}
	body := captureChatRequestBody(t, msgs)

	if strings.Contains(body, "attachments") {
		t.Errorf("request body leaks attachments: %s", body)
	}
	if strings.Contains(body, "aa_report.pdf") {
		t.Errorf("request body leaks attachment path: %s", body)
	}
	// Sanity: the message itself is there.
	if !strings.Contains(body, "here is your file") {
		t.Errorf("request body lost message content: %s", body)
	}
	// And the body must be valid JSON with our message serialized as an
	// object with content/tool_calls fields only.
	var parsed struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(parsed.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(parsed.Messages))
	}
	for _, key := range []string{"attachments", "media"} {
		if _, ok := parsed.Messages[0][key]; ok {
			t.Errorf("wire message has %q field", key)
		}
	}
}

func TestAttachmentsNotSentToProvider_ChatStream(t *testing.T) {
	var captured string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		captured = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	p := NewProvider("k", server.URL, "")
	msgs := []Message{{
		Role:        "user",
		Content:     "hi",
		Attachments: []protocoltypes.MessageAttachment{{Name: "f.txt", Path: "/p/f.txt"}},
	}}
	_, _ = p.ChatStream(context.Background(), msgs, nil, "gpt-4o", nil, func(string, bool) {}, nil)

	if strings.Contains(captured, "attachments") || strings.Contains(captured, "/p/f.txt") {
		t.Errorf("stream request body leaks attachments: %s", captured)
	}
}
