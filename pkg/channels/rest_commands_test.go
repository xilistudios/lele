// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package channels

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// commandsGet performs GET /api/v1/chat/commands and returns status + raw body
// so callers can decode either the success payload or an APIError.
func commandsGet(t *testing.T, ts *nativeTestServer, method string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequest(method, ts.server.URL+"/api/v1/chat/commands", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	return resp.StatusCode, body
}

// TestChatCommandsEndpoint_GetReturnsRegistry drives the real mux and asserts
// the palette payload: 200, decodes, both commands present with every field
// populated, in stable (sorted) order.
func TestChatCommandsEndpoint_GetReturnsRegistry(t *testing.T) {
	ts := newNativeTestServer(t)

	status, body := commandsGet(t, ts, http.MethodGet)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", status, http.StatusOK, body)
	}

	var payload ChatCommandsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode payload: %v (body=%s)", err, body)
	}

	if len(payload.Commands) != 2 {
		t.Fatalf("got %d commands, want 2: %s", len(payload.Commands), body)
	}

	wantNames := []string{"/clear", "/compact"}
	for i, cmd := range payload.Commands {
		if cmd.Name != wantNames[i] {
			t.Errorf("commands[%d].name = %q, want %q", i, cmd.Name, wantNames[i])
		}
		if cmd.Description == "" {
			t.Errorf("commands[%d].description is empty", i)
		}
		if cmd.Usage == "" {
			t.Errorf("commands[%d].usage is empty", i)
		}
	}

	// The exact wire format the WebUI consumes (writeJSON terminates with a
	// newline, so trim it before comparing).
	const want = `{"commands":[{"name":"/clear","description":"Clear the conversation history for this session.","usage":"/clear"},{"name":"/compact","description":"Summarize and compact the conversation history (needs 5+ messages).","usage":"/compact"}]}`
	if got := strings.TrimSuffix(string(body), "\n"); got != want {
		t.Errorf("payload mismatch:\n got %s\nwant %s", got, want)
	}
}

// TestChatCommandsEndpoint_RejectsOtherMethods covers the route registration:
// the mux itself answers 405 for a method with no handler on that path.
func TestChatCommandsEndpoint_RejectsOtherMethods(t *testing.T) {
	ts := newNativeTestServer(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		status, body := commandsGet(t, ts, method)
		if status != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want %d (body=%s)", method, status, http.StatusMethodNotAllowed, body)
		}
	}
}

// TestChatCommandsEndpoint_RequiresAuth guards the withAuth wrapper at route
// registration: the command list is not public.
func TestChatCommandsEndpoint_RequiresAuth(t *testing.T) {
	ts := newNativeTestServer(t)

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/commands", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestHandleChatCommands_WorksWithoutAgentLoop pins the property that makes the
// handler trivially safe: the registry is package data, so the handler must
// never dereference n.agentLoop. A NativeChannel with no agent loop, no config
// and no auth manager must still serve the list.
func TestHandleChatCommands_WorksWithoutAgentLoop(t *testing.T) {
	n := &NativeChannel{}

	rec := httptest.NewRecorder()
	n.handleChatCommands(rec, httptest.NewRequest(http.MethodGet, "/api/v1/chat/commands", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var payload ChatCommandsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v (body=%s)", err, rec.Body.String())
	}
	if len(payload.Commands) != 2 {
		t.Fatalf("got %d commands, want 2", len(payload.Commands))
	}
}

// TestHandleChatCommands_NonGetIsMethodNotAllowed exercises the in-handler guard
// directly. Through the mux the router already answers 405, so this is what
// actually covers the defensive branch.
func TestHandleChatCommands_NonGetIsMethodNotAllowed(t *testing.T) {
	n := &NativeChannel{}

	rec := httptest.NewRecorder()
	n.handleChatCommands(rec, httptest.NewRequest(http.MethodPost, "/api/v1/chat/commands", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}

	var apiErr APIError
	if err := json.Unmarshal(rec.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("decode APIError: %v (body=%s)", err, rec.Body.String())
	}
	if apiErr.Code != "method_invalid" {
		t.Errorf("error code = %q, want %q", apiErr.Code, "method_invalid")
	}
}
