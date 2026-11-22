package channels

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	agentcommands "github.com/xilistudios/lele/pkg/agent/commands"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/harness"
)

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

	if len(payload.Commands) != 3 {
		t.Fatalf("got %d commands, want 3: %s", len(payload.Commands), body)
	}

	wantNames := []string{"/clear", "/compact", "/goal"}
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

	const want = `{"commands":[{"name":"/clear","description":"Clear the conversation history for this session.","usage":"/clear"},{"name":"/compact","description":"Summarize and compact the conversation history (needs 5+ messages).","usage":"/compact"},{"name":"/goal","description":"Set a persistent goal the agent works toward autonomously, or check/pause/resume/clear it (/goal status|pause|resume|clear).","usage":"/goal \u003ctext\u003e"}]}`
	if got := strings.TrimSuffix(string(body), "\n"); got != want {
		t.Errorf("payload mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestChatCommandsEndpoint_RejectsOtherMethods(t *testing.T) {
	ts := newNativeTestServer(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		status, body := commandsGet(t, ts, method)
		if status != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want %d (body=%s)", method, status, http.StatusMethodNotAllowed, body)
		}
	}
}

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
	if len(payload.Commands) != 3 {
		t.Fatalf("got %d commands, want 3", len(payload.Commands))
	}
}

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

type harnessLoopFake struct {
	*nativeTestAgentLoop
	calls int
	cmds  []*harness.Command
}

func (h *harnessLoopFake) HarnessCommands() []*harness.Command {
	h.calls++
	return h.cmds
}

func newHarnessCommandsServer(t *testing.T, cmds ...*harness.Command) (*nativeTestServer, *harnessLoopFake) {
	t.Helper()

	ts := newNativeTestServer(t)
	provider := &harnessLoopFake{nativeTestAgentLoop: ts.loop, cmds: cmds}
	ts.channel.agentLoop = provider
	return ts, provider
}

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

func TestChatCommandsEndpoint_LoopWithoutProviderServesBuiltinsOnly(t *testing.T) {
	ts := newNativeTestServer(t)

	if _, ok := interface{}(ts.loop).(customCommandProvider); ok {
		t.Fatal("nativeTestAgentLoop unexpectedly implements customCommandProvider; this test would be vacuous")
	}

	status, body := commandsGet(t, ts, http.MethodGet)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", status, http.StatusOK, body)
	}
	const want = `{"commands":[{"name":"/clear","description":"Clear the conversation history for this session.","usage":"/clear"},{"name":"/compact","description":"Summarize and compact the conversation history (needs 5+ messages).","usage":"/compact"},{"name":"/goal","description":"Set a persistent goal the agent works toward autonomously, or check/pause/resume/clear it (/goal status|pause|resume|clear).","usage":"/goal \u003ctext\u003e"}]}`
	if got := strings.TrimSuffix(string(body), "\n"); got != want {
		t.Errorf("payload mismatch:\n got %s\nwant %s", got, want)
	}
}

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

	if len(got) != 4 {
		t.Fatalf("got %d commands, want 4 (built-ins + /review): %+v", len(got), got)
	}

	wantNames := []string{"/clear", "/compact", "/goal", "/review"}
	for i, cmd := range got {
		if cmd.Name != wantNames[i] {
			t.Errorf("commands[%d].name = %q, want %q", i, cmd.Name, wantNames[i])
		}
	}

	if got[0].Description != "Clear the conversation history for this session." || got[0].Source != "" {
		t.Errorf("/clear = %+v, want the built-in entry with empty source", got[0])
	}
	if got[1].Source != "" || got[2].Source != "" {
		t.Errorf("/compact and /goal sources = %q/%q, want empty for built-ins", got[1].Source, got[2].Source)
	}

	custom := got[3]
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

func TestChatCommandsEndpoint_CustomNameIsNormalized(t *testing.T) {
	ts, _ := newHarnessCommandsServer(t,
		&harness.Command{Name: "Review", Template: "t", Source: harness.SourceConfig},
		&harness.Command{Name: "CLEAR", Template: "t", Source: harness.SourceGlobal},
	)

	got := chatCommandsGet(t, ts)
	if len(got) != 4 {
		t.Fatalf("got %d commands, want 4: %+v", len(got), got)
	}
	if got[3].Name != "/review" {
		t.Errorf("custom name = %q, want %q", got[3].Name, "/review")
	}
	if got[3].Usage != "/review [args]" {
		t.Errorf("custom usage = %q, want %q", got[3].Usage, "/review [args]")
	}
	if got[0].Name != "/clear" || got[0].Source != "" {
		t.Errorf("/clear = %+v, want the built-in entry", got[0])
	}
}

func TestChatCommandsEndpoint_EmptyAndJunkCustomCommands(t *testing.T) {
	t.Run("nil slice", func(t *testing.T) {
		ts, _ := newHarnessCommandsServer(t)
		if got := chatCommandsGet(t, ts); len(got) != 3 {
			t.Fatalf("got %d commands, want the 3 built-ins: %+v", len(got), got)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		ts, _ := newHarnessCommandsServer(t, []*harness.Command{}...)
		if got := chatCommandsGet(t, ts); len(got) != 3 {
			t.Fatalf("got %d commands, want the 3 built-ins: %+v", len(got), got)
		}
	})

	t.Run("nameless entries skipped", func(t *testing.T) {
		ts, _ := newHarnessCommandsServer(t,
			&harness.Command{Name: "", Template: "t", Source: harness.SourceConfig},
			nil,
			&harness.Command{Name: "ok", Template: "t", Source: harness.SourceConfig},
		)
		got := chatCommandsGet(t, ts)
		if len(got) != 4 {
			t.Fatalf("got %d commands, want 4: %+v", len(got), got)
		}
		if got[3].Name != "/ok" {
			t.Errorf("last command = %q, want %q", got[3].Name, "/ok")
		}
	})
}

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

func TestNativeChannelCommandAppliedNeedsNoAgentLoop(t *testing.T) {
	n := &NativeChannel{wsClients: make(map[string]*WSClient)}

	if err := n.Send(context.Background(), bus.OutboundMessage{
		Channel:  ChannelName,
		ChatID:   "native:orphan",
		Event:    "command.applied",
		Metadata: map[string]string{"command": "review"},
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}

func TestConsumesEventIncludesCommandApplied(t *testing.T) {
	n := &NativeChannel{}
	if !n.ConsumesEvent("command.applied") {
		t.Error("NativeChannel must declare command.applied as consumed (dispatchOutboundMessage branches on it)")
	}
}

type harnessCommandsLoopFake struct {
	*commandsWorkspaceLoop
	defaultCmds []*harness.Command
}

func (h *harnessCommandsLoopFake) HarnessCommands() []*harness.Command {
	return h.defaultCmds
}

func newScopedCommandsServer(t *testing.T) (*nativeTestServer, *harnessCommandsLoopFake) {
	t.Helper()

	ts, dirs := newCommandsTestServer(t)

	writeCommandFile(t, filepath.Join(dirs["alpha"], agentCommandsSubdir), "alpha-only.md",
		commandMarkdown("Alpha command", "do the alpha thing"))
	writeCommandFile(t, filepath.Join(dirs["beta"], agentCommandsSubdir), "beta-only.md",
		commandMarkdown("Beta command", "do the beta thing"))

	provider := &harnessCommandsLoopFake{
		commandsWorkspaceLoop: &commandsWorkspaceLoop{
			nativeTestAgentLoop: ts.loop,
			workspaces:          map[string]string{"alpha": dirs["alpha"], "beta": dirs["beta"]},
		},
		defaultCmds: []*harness.Command{{
			Name: "legacy", Description: "from the default view", Template: "t",
			Source: harness.SourceWorkspace,
		}},
	}
	ts.channel.agentLoop = provider
	return ts, provider
}

func chatCommandsURL(t *testing.T, ts *nativeTestServer, rawQuery string) []agentcommands.CommandInfo {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/commands?"+rawQuery, nil)
	if err != nil {
		t.Fatalf("NewRequest(): %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(): %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(): %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", resp.StatusCode, http.StatusOK, body)
	}
	var payload ChatCommandsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode payload: %v (body=%s)", err, body)
	}
	return payload.Commands
}

func findCommand(cmds []agentcommands.CommandInfo, name string) (agentcommands.CommandInfo, bool) {
	i := slices.IndexFunc(cmds, func(c agentcommands.CommandInfo) bool { return c.Name == name })
	if i < 0 {
		return agentcommands.CommandInfo{}, false
	}
	return cmds[i], true
}

func TestChatCommandsEndpoint_AgentIDScopesCustomCommands(t *testing.T) {
	ts, _ := newScopedCommandsServer(t)

	t.Run("alpha sees only its own custom command", func(t *testing.T) {
		got := chatCommandsURL(t, ts, "agent_id=alpha")
		if _, ok := findCommand(got, "/alpha-only"); !ok {
			t.Fatalf("alpha palette missing /alpha-only: %+v", got)
		}
		for _, forbidden := range []string{"/beta-only", "/legacy"} {
			if _, ok := findCommand(got, forbidden); ok {
				t.Errorf("alpha palette leaked %q (beta or default-workspace command): %+v", forbidden, got)
			}
		}
		if _, ok := findCommand(got, "/clear"); !ok {
			t.Error("alpha palette lost the built-ins")
		}
		if cmd, _ := findCommand(got, "/alpha-only"); cmd.Source != string(harness.SourceWorkspace) {
			t.Errorf("/alpha-only source = %q, want %q", cmd.Source, harness.SourceWorkspace)
		}
	})

	t.Run("beta sees a different palette", func(t *testing.T) {
		got := chatCommandsURL(t, ts, "agent_id=beta")
		if _, ok := findCommand(got, "/beta-only"); !ok {
			t.Fatalf("beta palette missing /beta-only: %+v", got)
		}
		if _, ok := findCommand(got, "/alpha-only"); ok {
			t.Errorf("beta palette leaked /alpha-only: %+v", got)
		}
	})

	t.Run("agent_id is URL-decoded", func(t *testing.T) {
		got := chatCommandsURL(t, ts, "agent_id=al%70ha")
		if _, ok := findCommand(got, "/alpha-only"); !ok {
			t.Errorf("escaped agent_id did not resolve to alpha's palette: %+v", got)
		}
	})
}

func TestChatCommandsEndpoint_AgentIDFallbacks(t *testing.T) {
	ts, provider := newScopedCommandsServer(t)

	for _, tc := range []struct {
		name  string
		query string
	}{
		{"no agent_id", ""},
		{"empty agent_id", "agent_id="},
		{"unknown agent_id", "agent_id=ghost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := chatCommandsURL(t, ts, tc.query)
			if _, ok := findCommand(got, "/legacy"); !ok {
				t.Errorf("palette = %+v, want the default-workspace view (/legacy)", got)
			}
			for _, leak := range []string{"/alpha-only", "/beta-only"} {
				if _, ok := findCommand(got, leak); ok {
					t.Errorf("fallback palette leaked %q: %+v", leak, got)
				}
			}
		})
	}

	if len(provider.defaultCmds) == 0 {
		t.Fatal("test server has no default-view commands; fallback assertions would be vacuous")
	}
}
