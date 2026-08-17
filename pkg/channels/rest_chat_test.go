package channels

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestHandleChatSend_Success(t *testing.T) {
	ts := newNativeTestServer(t)

	body := mustMarshal(ChatSendRequest{Content: "Hello"})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/send", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var payload ChatSendResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.MessageID == "" {
		t.Fatal("expected non-empty message_id")
	}
	if payload.SessionKey == "" {
		t.Fatal("expected non-empty session_key")
	}
}

func TestHandleChatSend_EmptyContent(t *testing.T) {
	ts := newNativeTestServer(t)

	body := mustMarshal(ChatSendRequest{Content: ""})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/send", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleChatSend_WithSessionKey(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "test-session-" + ts.clientID
	body := mustMarshal(ChatSendRequest{Content: "Hello", SessionKey: sessionKey})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/send", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var payload ChatSendResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.SessionKey != sessionKey {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}
}

func TestHandleChatHistory_ReturnsMessages(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "system", Content: "hidden"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi!"},
		{Role: "tool", Content: "result", ToolCallID: "call-1"},
	}

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/history", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ChatHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.SessionKey == "" {
		t.Fatal("expected non-empty session_key")
	}
	// system message should be filtered out
	if len(payload.Messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(payload.Messages))
	}
	if payload.Messages[0].Role != "user" {
		t.Fatalf("first message role = %q, want %q", payload.Messages[0].Role, "user")
	}
	if payload.Messages[1].Role != "assistant" {
		t.Fatalf("second message role = %q, want %q", payload.Messages[1].Role, "assistant")
	}
}

func TestHandleChatHistory_Pagination(t *testing.T) {
	ts := newNativeTestServer(t)

	// Create enough history to paginate
	sessionKey := "native:" + ts.clientID
	msgs := make([]providers.Message, 60)
	for i := 0; i < 60; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = providers.Message{Role: role, Content: "msg-" + string(rune('A'+i))}
	}
	ts.loop.histories[sessionKey] = msgs

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/history?limit=10", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ChatHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Messages) > 10 {
		t.Fatalf("got %d messages, want at most 10", len(payload.Messages))
	}
	if !payload.HasMore {
		t.Fatal("expected has_more=true with 60 messages and limit=10")
	}
}

func TestHandleChatHistory_HidesStreamingMessageWhileProcessing(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "partial...", Streaming: true},
	}
	ts.loop.processing[sessionKey] = true

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/history", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ChatHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Messages) != 1 {
		t.Fatalf("got %d messages, want 1 (streaming assistant hidden while processing)", len(payload.Messages))
	}
	if payload.Messages[0].Role != "user" {
		t.Fatalf("first message role = %q, want %q", payload.Messages[0].Role, "user")
	}
	if !payload.Processing {
		t.Fatal("expected processing=true while the session is processing")
	}
}

func TestHandleChatHistory_ShowsStreamingMessageWhenIdle(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "partial...", Streaming: true},
	}

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/history", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ChatHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Messages) != 2 {
		t.Fatalf("got %d messages, want 2 (orphaned streaming assistant kept when idle)", len(payload.Messages))
	}
	if payload.Processing {
		t.Fatal("expected processing=false while the session is idle")
	}
}

func TestHandleChatSessions_Empty(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ChatSessionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Sessions == nil {
		t.Fatal("expected non-nil sessions")
	}
	if len(payload.Sessions) != 0 {
		t.Fatalf("got %d sessions, want 0", len(payload.Sessions))
	}
}

func TestHandleCreateSession(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "new-session-" + ts.clientID
	body := mustMarshal(CreateSessionRequest{SessionKey: sessionKey})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var payload CreateSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.SessionKey != sessionKey {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}
}

func TestHandleCreateSession_NoKey(t *testing.T) {
	ts := newNativeTestServer(t)

	body := mustMarshal(CreateSessionRequest{SessionKey: ""})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleChatSessionGet(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload["session_key"] != sessionKey {
		t.Fatalf("session_key = %q, want %q", payload["session_key"], sessionKey)
	}
}

func TestHandleChatSessionDelete(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)
	req, _ := http.NewRequest(http.MethodDelete, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload["status"] != "deleted" {
		t.Fatalf("status = %q, want %q", payload["status"], "deleted")
	}
}

func TestHandleChatClear(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "user", Content: "Hello"},
	}
	beforeClear, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/history", nil)
	beforeClear.Header.Set("Authorization", "Bearer "+ts.token)

	// Clear the session
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/clear", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload["status"] != "cleared" {
		t.Fatalf("status = %q, want %q", payload["status"], "cleared")
	}
}

func TestHandleChatSend_Unauthorized(t *testing.T) {
	ts := newNativeTestServer(t)

	body := mustMarshal(ChatSendRequest{Content: "Hello"})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/send", strings.NewReader(string(body)))
	// No Authorization header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestHandleChatHistory_Unauthorized(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/history", nil)
	// No Authorization header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}
