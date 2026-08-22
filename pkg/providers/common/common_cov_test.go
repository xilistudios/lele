package common

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

// TestNewTransport_ResponseHeaderTimeoutSet covers headerTimeout>0 path in newTransport.
func TestNewTransport_ResponseHeaderTimeoutSet(t *testing.T) {
	transport := newTransport("", 5*time.Second)
	if transport.ResponseHeaderTimeout != 5*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 5s", transport.ResponseHeaderTimeout)
	}
}

// TestNewTransport_InvalidProxyGraceful verifies invalid proxy does not panic and returns DefaultTransport clone.
func TestNewTransport_InvalidProxyGraceful(t *testing.T) {
	transport := newTransport("://not-a-good-url", 0)
	if transport == nil {
		t.Fatal("newTransport returned nil")
	}
	if transport.ResponseHeaderTimeout != 0 {
		t.Errorf("ResponseHeaderTimeout = %v, want 0", transport.ResponseHeaderTimeout)
	}
}

// TestNewTransport_FallbackDefaultTransport covers the fallback branch when
// http.DefaultTransport is not a *http.Transport.
func TestNewTransport_FallbackDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()

	http.DefaultTransport = &notATransport{}

	tr := newTransport("", 0)
	if tr == nil {
		t.Fatal("newTransport returned nil in fallback branch")
	}

	transport2 := newTransport("http://127.0.0.1:9999", 7*time.Second)
	if transport2.ResponseHeaderTimeout != 7*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 7s", transport2.ResponseHeaderTimeout)
	}
	req := &http.Request{URL: mustParseURL(t, "https://api.example.com")}
	gotProxy, err := transport2.Proxy(req)
	if err != nil || gotProxy == nil || gotProxy.String() != "http://127.0.0.1:9999" {
		t.Errorf("proxy = %v, err = %v", gotProxy, err)
	}
}

type notATransport struct{}

func (notATransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, errors.New("not used")
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// IdleTimeoutReader coverage.
func TestIdleTimeoutReader_ReadAfterError(t *testing.T) {
	pr, pw := io.Pipe()
	r := NewIdleTimeoutReader(pr, 1*time.Hour) // no real timeout
	_ = pw.Close()
	_, err := r.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("expected error reading from closed pipe")
	}
	// Second read returns the stored err (err != nil branch).
	_, err2 := r.Read(make([]byte, 1))
	if err2 == nil {
		t.Fatal("expected stored error on second read")
	}
	_ = r.Close()
}

func TestIdleTimeoutReader_CloseStopsTimer(t *testing.T) {
	pr, pw := io.Pipe()
	r := NewIdleTimeoutReader(pr, 50*time.Millisecond)
	if err := r.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}
	// Reading after close returns an error because pipe is closed.
	_, err := r.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("expected read error after close")
	}
	_ = pw.Close()
}

// SerializeMessages coverage for remaining branches.
func TestSerializeMessages_ToolMessagesAndReasoning(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "tool result", ToolCallID: "call-1"},
		{Role: "assistant", Content: "", ReasoningContent: "thinking", ToolCalls: []ToolCall{{ID: "call-2"}}},
	}
	out := SerializeMessages(messages)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	// openaiMessage is the concrete element type.
	elem, ok := out[0].(openaiMessage)
	if !ok {
		t.Fatalf("expected openaiMessage, got %T", out[0])
	}
	if elem.Role != "system" {
		t.Errorf("first role = %v", elem.Role)
	}
	toolMsg, ok := out[1].(openaiMessage)
	if !ok {
		t.Fatal("expected openaiMessage for tool msg")
	}
	if toolMsg.ToolCallID != "call-1" {
		t.Errorf("tool_call_id = %v", toolMsg.ToolCallID)
	}
	asst, ok := out[2].(openaiMessage)
	if !ok {
		t.Fatal("expected openaiMessage for assistant msg")
	}
	if asst.ReasoningContent != "thinking" {
		t.Errorf("reasoning_content = %v, want thinking", asst.ReasoningContent)
	}
	if len(asst.ToolCalls) != 1 {
		t.Errorf("tool_calls len = %d, want 1", len(asst.ToolCalls))
	}
}

func TestSerializeMessages_ContentPartsImageDetail(t *testing.T) {
	detail := "high"
	messages := []Message{
		{
			Role: "user",
			ContentParts: []protocoltypes.ContentPart{
				{Type: "text", Text: "  "}, // blank -> skipped
				{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "https://img/1.png", Detail: detail}},
				{Type: "image_url", ImageURL: nil}, // nil -> skipped
				{Type: "input_audio"},              // ignored in ContentParts path
				{Type: "text", Text: "hello"},      // text with content
			},
		},
	}
	result := SerializeMessages(messages)
	data, _ := json.Marshal(result)
	var msgs []map[string]any
	json.Unmarshal(data, &msgs)
	if len(msgs) != 1 {
		t.Fatalf("len = %d, want 1", len(msgs))
	}
	contentPart, ok := msgs[0]["content"].([]any)
	if !ok || len(contentPart) != 2 {
		t.Fatalf("expected 2 content parts, got %#v", msgs[0]["content"])
	}
	imgPart := contentPart[0].(map[string]any)
	if imgPart["type"] != "image_url" {
		t.Errorf("part0 type = %v", imgPart["type"])
	}
	imgURL := imgPart["image_url"].(map[string]any)
	if imgURL["detail"] != "high" {
		t.Errorf("detail = %v, want high", imgURL["detail"])
	}
}

func TestSerializeMessages_MediaDataImageAndAudio(t *testing.T) {
	messages := []Message{
		{
			Role: "user",
			Media: []string{
				"data:image/png;base64,AAAA",
				"data:audio/wav;base64,BBBB",
				"https://plain.example/x.png", // not data URL -> ignored
			},
		},
	}
	out := SerializeMessages(messages)
	data, _ := json.Marshal(out)
	var msgs []map[string]any
	json.Unmarshal(data, &msgs)
	contentAny, ok := msgs[0]["content"].([]any)
	if !ok || len(contentAny) != 2 {
		t.Fatalf("expected 2 content parts, got %#v", msgs[0]["content"])
	}
	if contentAny[0].(map[string]any)["type"] != "image_url" {
		t.Errorf("part0 type = %v", contentAny[0])
	}
	if contentAny[1].(map[string]any)["type"] != "input_audio" {
		t.Errorf("part1 type = %v", contentAny[1])
	}
}

// parseDataAudioURL edge cases.
func TestParseDataAudioURL_EdgeCases(t *testing.T) {
	if _, _, ok := parseDataAudioURL("https://x.mp3"); ok {
		t.Error("expected false for non-audio data URL")
	}
	if _, _, ok := parseDataAudioURL("data:audio/wav"); ok {
		t.Error("expected false when no comma (no data)")
	}
	if _, _, ok := parseDataAudioURL("data:audio/wav,   "); ok {
		t.Error("expected false when data is blank")
	}
	if _, _, ok := parseDataAudioURL("data:audio/;base64,AAA"); ok {
		t.Error("expected false when format is blank")
	}
	format, data, ok := parseDataAudioURL("data:audio/mpeg;base64,  MYYQ  ")
	if !ok {
		t.Fatal("expected ok for valid audio URL")
	}
	if format != "mpeg" || data != "MYYQ" {
		t.Errorf("got format=%q data=%q", format, data)
	}
}

// normalizeFinishReason.
func TestNormalizeFinishReason(t *testing.T) {
	if got := normalizeFinishReason("length"); got != "truncated" {
		t.Errorf("length -> %q, want truncated", got)
	}
	if got := normalizeFinishReason("stop"); got != "stop" {
		t.Errorf("stop -> %q", got)
	}
	if got := normalizeFinishReason(""); got != "" {
		t.Errorf("empty -> %q", got)
	}
}

// decodeJSONObject and repairTruncatedJSONObject.
func TestDecodeJSONObject_IncompleteObject(t *testing.T) {
	got, ok := decodeJSONObject([]byte(`{"a": 1, "b": "unterminated`))
	if !ok || got["a"] != float64(1) {
		t.Errorf("decodeJSONObject incomplete = %v, ok=%v", got, ok)
	}
}

func TestRepairTruncatedJSONObject_NotObjectStart(t *testing.T) {
	if _, ok := repairTruncatedJSONObject([]byte(`"hello`)); ok {
		t.Error("expected false for non-object start")
	}
	if _, ok := repairTruncatedJSONObject(nil); ok {
		t.Error("expected false for empty")
	}
}

func TestRepairTruncatedJSONObject_ValidJSON(t *testing.T) {
	// No repair needed (stack empties and not inString) -> returns false.
	if _, ok := repairTruncatedJSONObject([]byte(`{"a":1}`)); ok {
		t.Error("expected false when no repair is needed")
	}
	// A fully balanced object with an inner string needs no repair.
	if _, ok := repairTruncatedJSONObject([]byte(`{"a": "hi"}`)); ok {
		t.Error("expected false for balanced JSON")
	}
}

// HandleErrorResponse read error path.
func TestHandleErrorResponse_ReadError(t *testing.T) {
	resp := &http.Response{
		StatusCode: 500,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(errReader{}),
	}
	err := HandleErrorResponse(resp, "https://api")
	if err == nil || !strings.Contains(err.Error(), "failed to read response") {
		t.Fatalf("expected read error, got %v", err)
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, errors.New("boom") }

// HandleErrorResponse plain (non-HTML) body.
func TestHandleErrorResponse_PlainBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: 400,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(strings.NewReader("bad request")),
	}
	err := HandleErrorResponse(resp, "https://api")
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected 400 error, got %v", err)
	}
}

// SerializeMessages preserves reasoning content via the openaiMessage struct.
func TestSerializeMessages_NoReasoningOnUser(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "hi", ReasoningContent: "kept-as-field"},
	}
	out := SerializeMessages(messages)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if _, ok := out[0].(openaiMessage); !ok {
		t.Fatalf("expected openaiMessage, got %T", out[0])
	}
}

// decodeJSONObject returns repaired map for valid object.
func TestDecodeJSONObject_ValidObject(t *testing.T) {
	got, ok := decodeJSONObject([]byte(`{"x": 2}`))
	if !ok || got["x"] != float64(2) {
		t.Errorf("decodeJSONObject = %v, ok=%v", got, ok)
	}
}

func TestParseDataAudioURL_Utf8Plain(t *testing.T) {
	if _, _, ok := parseDataAudioURL("data:audio/wav;base64,MDAw"); !ok {
		t.Error("plain valid audio should be ok")
	}
}

func TestRepairTruncatedJSONObject_ArrayNesting(t *testing.T) {
	// Truncated object with array nesting.
	got, ok := repairTruncatedJSONObject([]byte(`{"a": [1, 2`))
	if !ok {
		t.Fatal("expected repair to succeed")
	}
	arr, ok := got["a"].([]any)
	if !ok || len(arr) != 2 {
		t.Errorf("array = %v", got["a"])
	}
}

var _ = json.Marshal
