// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package channels

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	agentcommands "github.com/xilistudios/lele/pkg/agent/commands"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/harness"
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

// --- custom (harness) command merging --------------------------------------

// harnessLoopFake wraps the shared agent-loop fake and adds the OPTIONAL
// customCommandProvider capability, i.e. exactly what *agent.Loop implements in
// production. Embedding keeps it a full AgentProvidable without duplicating the
// ~40 methods of nativeTestAgentLoop.
type harnessLoopFake struct {
	*nativeTestAgentLoop
	calls int
	cmds  []*harness.Command
}

func (h *harnessLoopFake) HarnessCommands() []*harness.Command {
	h.calls++
	return h.cmds
}

// newHarnessCommandsServer returns a test server whose agent loop advertises the
// given harness commands through the optional provider interface.
func newHarnessCommandsServer(t *testing.T, cmds ...*harness.Command) (*nativeTestServer, *harnessLoopFake) {
	t.Helper()

	ts := newNativeTestServer(t)
	provider := &harnessLoopFake{nativeTestAgentLoop: ts.loop, cmds: cmds}
	ts.channel.agentLoop = provider
	return ts, provider
}

// chatCommandsGet decodes the palette payload for a test server.
func chatCommandsGet(t *testing.T, ts *nativeTestServer) []agentcommands.CommandInfo {
	t.Helper()

	status, body := commandsGet(t, ts, http.MethodGet)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", status, http.StatusOK, body)
	}
	var payload ChatCommandsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode payload: %v (body=%s)", err, body)
	}
	return payload.Commands
}

// TestChatCommandsEndpoint_LoopWithoutProviderServesBuiltinsOnly pins the safe
// degradation path: the default test loop does NOT implement HarnessCommands, so
// the type assertion fails and the response must be exactly the built-in list
// (no "source" key on the wire, byte-identical to the pre-harness payload).
func TestChatCommandsEndpoint_LoopWithoutProviderServesBuiltinsOnly(t *testing.T) {
	ts := newNativeTestServer(t)

	// Sanity: the fake really lacks the capability, otherwise this test proves
	// nothing about the assertion.
	if _, ok := interface{}(ts.loop).(customCommandProvider); ok {
		t.Fatal("nativeTestAgentLoop unexpectedly implements customCommandProvider; this test would be vacuous")
	}

	status, body := commandsGet(t, ts, http.MethodGet)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", status, http.StatusOK, body)
	}
	const want = `{"commands":[{"name":"/clear","description":"Clear the conversation history for this session.","usage":"/clear"},{"name":"/compact","description":"Summarize and compact the conversation history (needs 5+ messages).","usage":"/compact"}]}`
	if got := strings.TrimSuffix(string(body), "\n"); got != want {
		t.Errorf("payload mismatch:\n got %s\nwant %s", got, want)
	}
}

// TestChatCommandsEndpoint_MergesHarnessCommands covers the merged palette: a
// custom command colliding with a built-in ("/clear") must be dropped, a new one
// ("/review") must appear with its source and synthesised usage, and the list
// must stay sorted by name.
func TestChatCommandsEndpoint_MergesHarnessCommands(t *testing.T) {
	ts, provider := newHarnessCommandsServer(t,
		&harness.Command{
			Name: "clear", Description: "custom clear", Template: "wipe $ARGUMENTS",
			Source: harness.SourceDirectory, Path: "/proj/.lele/commands/clear.md",
		},
		&harness.Command{
			Name: "review", Description: "Review the current diff", Template: "review $ARGUMENTS",
			Agent: "coder", Model: "fast", Source: harness.SourceWorkspace,
		},
	)

	got := chatCommandsGet(t, ts)

	if provider.calls != 1 {
		t.Errorf("HarnessCommands called %d times, want exactly 1", provider.calls)
	}

	if len(got) != 3 {
		t.Fatalf("got %d commands, want 3 (built-ins + /review): %+v", len(got), got)
	}

	// Sorted, built-in collision dropped.
	wantNames := []string{"/clear", "/compact", "/review"}
	for i, cmd := range got {
		if cmd.Name != wantNames[i] {
			t.Errorf("commands[%d].name = %q, want %q", i, cmd.Name, wantNames[i])
		}
	}

	// The surviving /clear is the BUILT-IN: same description, no source.
	if got[0].Description != "Clear the conversation history for this session." || got[0].Source != "" {
		t.Errorf("/clear = %+v, want the built-in entry with empty source", got[0])
	}
	if got[1].Source != "" {
		t.Errorf("/compact source = %q, want empty for built-ins", got[1].Source)
	}

	custom := got[2]
	if custom.Description != "Review the current diff" {
		t.Errorf("/review description = %q", custom.Description)
	}
	if custom.Usage != "/review [args]" {
		t.Errorf("/review usage = %q, want %q", custom.Usage, "/review [args]")
	}
	if custom.Source != string(harness.SourceWorkspace) {
		t.Errorf("/review source = %q, want %q", custom.Source, harness.SourceWorkspace)
	}
}

// TestChatCommandsEndpoint_CustomNameIsNormalized proves the slash-less harness
// names reach the wire as slashed names (what the palette inserts into the
// composer) and that uppercase input still collides with the built-in.
func TestChatCommandsEndpoint_CustomNameIsNormalized(t *testing.T) {
	ts, _ := newHarnessCommandsServer(t,
		&harness.Command{Name: "Review", Template: "t", Source: harness.SourceConfig},
		&harness.Command{Name: "CLEAR", Template: "t", Source: harness.SourceGlobal},
	)

	got := chatCommandsGet(t, ts)
	if len(got) != 3 {
		t.Fatalf("got %d commands, want 3: %+v", len(got), got)
	}
	if got[2].Name != "/review" {
		t.Errorf("custom name = %q, want %q", got[2].Name, "/review")
	}
	if got[2].Usage != "/review [args]" {
		t.Errorf("custom usage = %q, want %q", got[2].Usage, "/review [args]")
	}
	// "/CLEAR" (custom) must not displace the built-in "/clear".
	if got[0].Name != "/clear" || got[0].Source != "" {
		t.Errorf("/clear = %+v, want the built-in entry", got[0])
	}
}

// TestChatCommandsEndpoint_EmptyAndJunkCustomCommands covers a provider that is
// present but has nothing useful to say: nil/empty results must fall back to the
// built-ins, and nameless entries must be skipped instead of leaking a bare "/".
func TestChatCommandsEndpoint_EmptyAndJunkCustomCommands(t *testing.T) {
	t.Run("nil slice", func(t *testing.T) {
		ts, _ := newHarnessCommandsServer(t)
		if got := chatCommandsGet(t, ts); len(got) != 2 {
			t.Fatalf("got %d commands, want the 2 built-ins: %+v", len(got), got)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		ts, _ := newHarnessCommandsServer(t, []*harness.Command{}...)
		if got := chatCommandsGet(t, ts); len(got) != 2 {
			t.Fatalf("got %d commands, want the 2 built-ins: %+v", len(got), got)
		}
	})

	t.Run("nameless entries skipped", func(t *testing.T) {
		ts, _ := newHarnessCommandsServer(t,
			&harness.Command{Name: "", Template: "t", Source: harness.SourceConfig},
			nil,
			&harness.Command{Name: "ok", Template: "t", Source: harness.SourceConfig},
		)
		got := chatCommandsGet(t, ts)
		if len(got) != 3 {
			t.Fatalf("got %d commands, want 3: %+v", len(got), got)
		}
		if got[2].Name != "/ok" {
			t.Errorf("last command = %q, want %q", got[2].Name, "/ok")
		}
	})
}

// TestChatCommandsEndpoint_ResponseIsFreshCopy guards the encoder against a
// mutated package registry: two consecutive requests must be independent slices.
func TestChatCommandsEndpoint_ResponseIsFreshCopy(t *testing.T) {
	ts, _ := newHarnessCommandsServer(t, &harness.Command{
		Name: "review", Description: "d", Template: "t", Source: harness.SourceWorkspace,
	})

	first := chatCommandsGet(t, ts)
	first[len(first)-1].Name = "/tampered"

	second := chatCommandsGet(t, ts)
	if second[len(second)-1].Name != "/review" {
		t.Fatalf("second request saw %q; response slices must not be shared", second[len(second)-1].Name)
	}
}

// --- WS command.applied ------------------------------------------------------

// TestNativeChannelWebSocketCommandAppliedEvent mirrors the tool.executing WS
// test for the new event: an outbound "command.applied" message must reach the
// subscribed session as a command.applied frame whose payload carries every
// metadata field plus the resolved session key.
func TestNativeChannelWebSocketCommandAppliedEvent(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := ts.clientID

	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(ts.token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	welcome := readWSMessage(t, conn)
	if welcome.Event != "welcome" {
		t.Fatalf("first event = %q, want welcome", welcome.Event)
	}

	// This is exactly the shape pkg/agent publishes from publishCommandApplied:
	// command WITHOUT the leading slash.
	ts.channel.Send(context.Background(), bus.OutboundMessage{
		Channel: ChannelName,
		ChatID:  sessionKey,
		Event:   "command.applied",
		Metadata: map[string]string{
			"command":     "review",
			"description": "Review the current diff",
			"args":        "pkg/foo.go",
			"agent":       "coder",
			"model":       "fast",
			"source":      "workspace",
		},
	})

	frame := readWSMessage(t, conn)
	if frame.Event != "command.applied" {
		t.Fatalf("event = %q, want command.applied", frame.Event)
	}

	var payload WSCommandAppliedPayload
	decodeWSData(t, frame.Data, &payload)
	if payload.SessionKey != sessionKey {
		t.Errorf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}
	if payload.Command != "review" {
		t.Errorf("command = %q, want %q (no leading slash)", payload.Command, "review")
	}
	if payload.Description != "Review the current diff" {
		t.Errorf("description = %q", payload.Description)
	}
	if payload.Args != "pkg/foo.go" {
		t.Errorf("args = %q, want %q", payload.Args, "pkg/foo.go")
	}
	if payload.Agent != "coder" || payload.Model != "fast" {
		t.Errorf("agent/model = %q/%q, want coder/fast", payload.Agent, payload.Model)
	}
	if payload.Source != string(harness.SourceWorkspace) {
		t.Errorf("source = %q, want %q", payload.Source, harness.SourceWorkspace)
	}
}

// TestNativeChannelWebSocketCommandAppliedMinimalOverrides pins the omitempty
// contract for a command that overrides neither agent nor model and takes no
// arguments: the frame must still arrive (command + session_key only) so the UI
// renders the chip without null-checking every field.
func TestNativeChannelWebSocketCommandAppliedMinimalOverrides(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := ts.clientID

	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(ts.token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	if welcome := readWSMessage(t, conn); welcome.Event != "welcome" {
		t.Fatalf("first event = %q, want welcome", welcome.Event)
	}

	ts.channel.Send(context.Background(), bus.OutboundMessage{
		Channel:  ChannelName,
		ChatID:   sessionKey,
		Event:    "command.applied",
		Metadata: map[string]string{"command": "hola", "source": "directory"},
	})

	frame := readWSMessage(t, conn)
	if frame.Event != "command.applied" {
		t.Fatalf("event = %q, want command.applied", frame.Event)
	}

	// The omitted fields must be absent from the JSON, not null/empty.
	var raw map[string]interface{}
	decodeWSData(t, frame.Data, &raw)
	for _, key := range []string{"description", "args", "agent", "model"} {
		if _, ok := raw[key]; ok {
			t.Errorf("payload has %q = %v, want it omitted", key, raw[key])
		}
	}

	var payload WSCommandAppliedPayload
	decodeWSData(t, frame.Data, &payload)
	if payload.SessionKey != sessionKey || payload.Command != "hola" || payload.Source != "directory" {
		t.Fatalf("payload = %#v, want %q/hola/directory", payload, sessionKey)
	}
}

// TestNativeChannelCommandAppliedNeedsNoAgentLoop pins the design constraint
// behind the event branch: the payload comes entirely from Metadata, so a
// NativeChannel with no agent loop must still translate the event (Send derives
// sessionKey straight from ChatID when agentLoop is nil).
func TestNativeChannelCommandAppliedNeedsNoAgentLoop(t *testing.T) {
	n := &NativeChannel{wsClients: make(map[string]*WSClient)}

	// No client connected: the emit is a no-op broadcast that must not panic.
	if err := n.Send(context.Background(), bus.OutboundMessage{
		Channel:  ChannelName,
		ChatID:   "native:orphan",
		Event:    "command.applied",
		Metadata: map[string]string{"command": "review"},
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}

// TestConsumesEventIncludesCommandApplied keeps the dispatcher guard honest for
// the new contentless signal: native declares it consumes every protocol event,
// and command.applied is now one of them.
func TestConsumesEventIncludesCommandApplied(t *testing.T) {
	n := &NativeChannel{}
	if !n.ConsumesEvent("command.applied") {
		t.Error("NativeChannel must declare command.applied as consumed (dispatchOutboundMessage branches on it)")
	}
}
