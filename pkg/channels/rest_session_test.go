package channels

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestHandleSessionModel_Get(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/model", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionModelResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.SessionKey == "" {
		t.Fatal("expected non-empty session_key")
	}
	if payload.Model == "" {
		t.Fatal("expected non-empty model")
	}
}

func TestHandleSessionModel_Patch(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	body := mustMarshal(SessionModelUpdateRequest{Model: "openai/gpt-4o-mini"})
	req, _ := http.NewRequest(http.MethodPatch, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/model", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionModelResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Model != "openai/gpt-4o-mini" {
		t.Fatalf("model = %q, want %q", payload.Model, "openai/gpt-4o-mini")
	}
}

func TestHandleSessionModel_PatchEmpty(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	body := mustMarshal(SessionModelUpdateRequest{Model: ""})
	req, _ := http.NewRequest(http.MethodPatch, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/model", strings.NewReader(string(body)))
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

func TestHandleSessionAgent_Get(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/agent", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.SessionKey != sessionKey {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}
	if payload.AgentID == "" {
		t.Fatal("expected non-empty agent_id")
	}
}

func TestHandleSessionAgent_Patch(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	body := mustMarshal(SessionAgentUpdateRequest{AgentID: "main"})
	req, _ := http.NewRequest(http.MethodPatch, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/agent", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.AgentID != "main" {
		t.Fatalf("agent_id = %q, want %q", payload.AgentID, "main")
	}
}

func TestHandleSessionThinking_Get(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/thinking", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionThinkingResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.SessionKey != sessionKey {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}
}

func TestHandleSessionThinking_Patch(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	body := mustMarshal(SessionThinkingUpdateRequest{Level: "high"})
	req, _ := http.NewRequest(http.MethodPatch, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/thinking", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionThinkingResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Level != "high" {
		t.Fatalf("level = %q, want %q", payload.Level, "high")
	}
}

func TestHandleSessionName_Get(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/name", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionNameResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.SessionKey != sessionKey {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}
}

func TestHandleSessionName_Patch(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	body := mustMarshal(SessionNameUpdateRequest{Name: "My Chat"})
	req, _ := http.NewRequest(http.MethodPatch, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/name", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionNameResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Name != "My Chat" {
		t.Fatalf("name = %q, want %q", payload.Name, "My Chat")
	}
}

func TestHandleSessionName_PatchEmpty(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	body := mustMarshal(SessionNameUpdateRequest{Name: ""})
	req, _ := http.NewRequest(http.MethodPatch, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/name", strings.NewReader(string(body)))
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

func TestHandleSessionContext(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/context", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionContextResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.SessionKey == "" {
		t.Fatal("expected non-empty session_key")
	}
	if payload.ContextWindow <= 0 {
		t.Fatal("expected positive context_window")
	}
}

func TestHandleSessionSummary(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/summary", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionSummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.SessionKey == "" {
		t.Fatal("expected non-empty session_key")
	}
}

func TestHandleSessionCompact(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/compact", nil)
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
	if payload["result"] == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestHandleSession_Forbidden(t *testing.T) {
	ts := newNativeTestServer(t)

	// Native sessions are shared across all clients, so accessing
	// another client's session should succeed (200) instead of 403.
	sessionKey := "native:other-client-id"
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/model", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
