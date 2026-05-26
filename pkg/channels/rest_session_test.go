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

func TestHandleSessionSubagents_Empty(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/subagents", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionSubagentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.SessionKey != sessionKey {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}
	if len(payload.Subagents) != 0 {
		t.Fatalf("expected empty subagents, got %d", len(payload.Subagents))
	}
}

func TestHandleSessionSubagents_WithData(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID

	// Seed the mock with subagent tasks
	ts.loop.sessionSubagents[sessionKey] = []SubagentTaskInfo{
		{
			TaskID:     "subagent-1",
			SessionKey: sessionKey + ":subagent-1",
			Label:      "Research Go logging",
			AgentID:    "",
			Status:     "running",
			Summary:    "",
			Created:    1716748800000,
			Updated:    1716748810000,
			Iterations: 0,
		},
		{
			TaskID:     "subagent-2",
			SessionKey: sessionKey + ":subagent-2",
			Label:      "Analyze pkg/agent",
			AgentID:    "coder",
			Status:     "completed",
			Summary:    "Found 3 undocumented exported functions",
			Created:    1716748700000,
			Updated:    1716748750000,
			Iterations: 5,
		},
		{
			TaskID:     "subagent-3",
			SessionKey: sessionKey + ":subagent-3",
			Label:      "",
			AgentID:    "",
			Status:     "failed",
			Summary:    "Subagent execution failed",
			Created:    1716748600000,
			Updated:    1716748610000,
			Iterations: 0,
		},
	}

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/subagents", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SessionSubagentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.SessionKey != sessionKey {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}
	if len(payload.Subagents) != 3 {
		t.Fatalf("expected 3 subagents, got %d", len(payload.Subagents))
	}

	// Should be sorted by Created descending (newest first)
	if payload.Subagents[0].TaskID != "subagent-1" {
		t.Fatalf("first subagent = %q, want %q", payload.Subagents[0].TaskID, "subagent-1")
	}
	if payload.Subagents[1].TaskID != "subagent-2" {
		t.Fatalf("second subagent = %q, want %q", payload.Subagents[1].TaskID, "subagent-2")
	}
	if payload.Subagents[2].TaskID != "subagent-3" {
		t.Fatalf("third subagent = %q, want %q", payload.Subagents[2].TaskID, "subagent-3")
	}

	// Verify fields
	first := payload.Subagents[0]
	if first.Label != "Research Go logging" {
		t.Fatalf("label = %q, want %q", first.Label, "Research Go logging")
	}
	if first.Status != "running" {
		t.Fatalf("status = %q, want %q", first.Status, "running")
	}
	if first.Iterations != 0 {
		t.Fatalf("iterations = %d, want %d", first.Iterations, 0)
	}

	second := payload.Subagents[1]
	if second.AgentID != "coder" {
		t.Fatalf("agent_id = %q, want %q", second.AgentID, "coder")
	}
	if second.Summary != "Found 3 undocumented exported functions" {
		t.Fatalf("summary = %q, want %q", second.Summary, "Found 3 undocumented exported functions")
	}
	if second.Iterations != 5 {
		t.Fatalf("iterations = %d, want %d", second.Iterations, 5)
	}

	third := payload.Subagents[2]
	if third.Status != "failed" {
		t.Fatalf("status = %q, want %q", third.Status, "failed")
	}
	if third.Label != "" {
		t.Fatalf("label = %q, want empty", third.Label)
	}
}

func TestHandleSessionSubagents_SortedByCreated(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID

	// Insert tasks out of order
	ts.loop.sessionSubagents[sessionKey] = []SubagentTaskInfo{
		{TaskID: "oldest", SessionKey: sessionKey + ":oldest", Status: "completed", Created: 1000},
		{TaskID: "newest", SessionKey: sessionKey + ":newest", Status: "running", Created: 3000},
		{TaskID: "middle", SessionKey: sessionKey + ":middle", Status: "completed", Created: 2000},
	}

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/subagents", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	var payload SessionSubagentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}

	if len(payload.Subagents) != 3 {
		t.Fatalf("expected 3 subagents, got %d", len(payload.Subagents))
	}

	expected := []string{"newest", "middle", "oldest"}
	for i, id := range expected {
		if payload.Subagents[i].TaskID != id {
			t.Fatalf("subagent[%d].task_id = %q, want %q", i, payload.Subagents[i].TaskID, id)
		}
	}
}

func TestHandleSessionSubagents_AllStatuses(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID

	statuses := []string{"running", "completed", "not_done", "needs_context", "failed", "cancelled"}
	var tasks []SubagentTaskInfo
	for i, status := range statuses {
		tasks = append(tasks, SubagentTaskInfo{
			TaskID:     "task-" + status,
			SessionKey: sessionKey + ":task-" + status,
			Status:     status,
			Created:    int64(1000 + i),
		})
	}
	ts.loop.sessionSubagents[sessionKey] = tasks

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/subagents", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	var payload SessionSubagentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}

	if len(payload.Subagents) != len(statuses) {
		t.Fatalf("expected %d subagents, got %d", len(statuses), len(payload.Subagents))
	}

	// Verify all statuses are present (sorted descending by Created, so reversed order)
	gotStatuses := make(map[string]bool)
	for _, s := range payload.Subagents {
		gotStatuses[s.Status] = true
	}
	for _, status := range statuses {
		if !gotStatuses[status] {
			t.Fatalf("missing status %q in response", status)
		}
	}
}
