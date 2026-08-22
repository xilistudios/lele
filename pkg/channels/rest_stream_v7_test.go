package channels

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// overrideLoop wraps a nativeTestAgentLoop and overrides select methods so
// tests can exercise branches the default stub never hits (e.g. returning a
// non-nil in-progress assistant message).
type overrideLoop struct {
	*nativeTestAgentLoop
	inProgress func(sessionKey string) *providers.Message
	thinkLevel func(sessionKey, level string) bool
	contextUse func(sessionKey string) (current, window int)
}

// GetInProgressAssistant uses the override func if provided.
func (o *overrideLoop) GetInProgressAssistant(sessionKey string) *providers.Message {
	if o.inProgress != nil {
		return o.inProgress(sessionKey)
	}
	return o.nativeTestAgentLoop.GetInProgressAssistant(sessionKey)
}

// SetThinkLevel uses the override func if provided.
func (o *overrideLoop) SetThinkLevel(sessionKey, level string) bool {
	if o.thinkLevel != nil {
		return o.thinkLevel(sessionKey, level)
	}
	return o.nativeTestAgentLoop.SetThinkLevel(sessionKey, level)
}

// GetCurrentContextUsage uses the override func if provided.
func (o *overrideLoop) GetCurrentContextUsage(sessionKey string) (int, int) {
	if o.contextUse != nil {
		return o.contextUse(sessionKey)
	}
	return o.nativeTestAgentLoop.GetCurrentContextUsage(sessionKey)
}

func TestHandleStreamStatus_MissingSessionKey(t *testing.T) {
	ts := newNativeTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/streams/", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.SetPathValue("sessionKey", "")
	rec := httptest.NewRecorder()
	ts.channel.handleStreamStatus(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleStreamStatus_AgentLoopNil(t *testing.T) {
	ts := newNativeTestServer(t)
	loop := ts.channel.agentLoop
	ts.channel.agentLoop = nil
	defer func() { ts.channel.agentLoop = loop }()

	req := authenticatedRequest(t, ts, "/api/v1/chat/streams/my-session")
	req.SetPathValue("sessionKey", "my-session")
	rec := httptest.NewRecorder()
	ts.channel.handleStreamStatus(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleStreamStatus_WithInProgress(t *testing.T) {
	ts := newNativeTestServer(t)
	loop := &overrideLoop{nativeTestAgentLoop: ts.loop, inProgress: func(string) *providers.Message {
		return &providers.Message{Content: "partial", ReasoningContent: "reason"}
	}}
	ts.channel.agentLoop = loop

	req := authenticatedRequest(t, ts, "/api/v1/chat/streams/my-session")
	req.SetPathValue("sessionKey", "my-session")
	rec := httptest.NewRecorder()
	ts.channel.handleStreamStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "partial") {
		t.Fatalf("body does not contain stream content: %s", body)
	}
	if !strings.Contains(body, "\"streams\"") {
		t.Fatalf("body does not contain streams array: %s", body)
	}
}

func TestHandleStreamState_MissingParams(t *testing.T) {
	ts := newNativeTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/streams/x/y", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.SetPathValue("sessionKey", "x")
	req.SetPathValue("messageID", "")
	rec := httptest.NewRecorder()
	ts.channel.handleStreamState(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleStreamState_AgentLoopNil(t *testing.T) {
	ts := newNativeTestServer(t)
	loop := ts.channel.agentLoop
	ts.channel.agentLoop = nil
	defer func() { ts.channel.agentLoop = loop }()

	req := authenticatedRequest(t, ts, "/api/v1/chat/streams/my-session/msg1")
	req.SetPathValue("sessionKey", "my-session")
	req.SetPathValue("messageID", "msg1")
	rec := httptest.NewRecorder()
	ts.channel.handleStreamState(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleStreamState_NotFound(t *testing.T) {
	ts := newNativeTestServer(t)
	req := authenticatedRequest(t, ts, "/api/v1/chat/streams/my-session/msg1")
	req.SetPathValue("sessionKey", "my-session")
	req.SetPathValue("messageID", "msg1")
	rec := httptest.NewRecorder()
	ts.channel.handleStreamState(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleStreamState_WithInProgress(t *testing.T) {
	ts := newNativeTestServer(t)
	loop := &overrideLoop{nativeTestAgentLoop: ts.loop, inProgress: func(string) *providers.Message {
		return &providers.Message{Content: "accumulated", ReasoningContent: ""}
	}}
	ts.channel.agentLoop = loop

	req := authenticatedRequest(t, ts, "/api/v1/chat/streams/my-session/msg1")
	req.SetPathValue("sessionKey", "my-session")
	req.SetPathValue("messageID", "msg1")
	rec := httptest.NewRecorder()
	ts.channel.handleStreamState(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "accumulated") {
		t.Fatalf("body does not contain content: %s", rec.Body.String())
	}
}

// TestHandleChatSendStream_InvalidBody exercises the JSON decode failure path.
func TestHandleChatSendStream_InvalidBody(t *testing.T) {
	ts := newNativeTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/send/stream", strings.NewReader("{invalid"))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	rec := httptest.NewRecorder()
	ts.channel.handleChatSendStream(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestHandleChatSendStream_EmptyContent exercises content-missing.
func TestHandleChatSendStream_EmptyContent(t *testing.T) {
	ts := newNativeTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/send/stream", strings.NewReader(`{"content":""}`))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	rec := httptest.NewRecorder()
	ts.channel.handleChatSendStream(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestHandleChatSendStream_NoFlusher exercises the streaming-unsupported path
// using a writer that does not implement http.Flusher.
func TestHandleChatSendStream_NoFlusher(t *testing.T) {
	ts := newNativeTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/send/stream", strings.NewReader(`{"content":"hi"}`))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("X-Client-Id", ts.clientID)
	w := &nonFlushingWriter{header: make(http.Header), body: &bytes.Buffer{}}
	ts.channel.handleChatSendStream(w, req)
	if w.status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.status, http.StatusInternalServerError)
	}
}

// nonFlushingWriter implements http.ResponseWriter but NOT http.Flusher.
type nonFlushingWriter struct {
	header http.Header
	body   *bytes.Buffer
	status int
}

func (w *nonFlushingWriter) Header() http.Header         { return w.header }
func (w *nonFlushingWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *nonFlushingWriter) WriteHeader(status int)      { w.status = status }

// TestHandleChatSendStream_Forbidden exercises ownership denial via a
// subagent-style key with no parent session.
func TestHandleChatSendStream_Forbidden(t *testing.T) {
	ts := newNativeTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/send/stream",
		strings.NewReader(`{"content":"hi","session_key":"subagent:nonexistent"}`))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("X-Client-Id", ts.clientID)
	rec := httptest.NewRecorder()
	ts.channel.handleChatSendStream(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// TestHandleChatSendStream_ClientDisconnect exercises the r.Context().Done()
// path by cancelling the request context before the handler blocks.
func TestHandleChatSendStream_ClientDisconnect(t *testing.T) {
	ts := newNativeTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/chat/send/stream",
		strings.NewReader(`{"content":"hi"}`))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("X-Client-Id", ts.clientID)
	rec := httptest.NewRecorder()
	cancel() // cancel before handler runs so the context is already Done

	done := make(chan struct{})
	go func() {
		ts.channel.handleChatSendStream(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after client disconnect")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (headers written before streaming loop)", rec.Code, http.StatusOK)
	}
}
