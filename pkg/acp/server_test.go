package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockBridge struct {
	mu     sync.Mutex
	agents map[string]AgentInfo
	calls  []string
	delay  time.Duration
	fail   bool
}

func newMockBridge() *mockBridge {
	return &mockBridge{
		agents: map[string]AgentInfo{
			"chat":       {ID: "chat", Name: "chat", Description: "Chat agent", Model: "gpt-x"},
			"researcher": {ID: "researcher", Name: "researcher", Description: "Research agent", Model: "gpt-y", SupportsImages: true},
		},
	}
}

func (m *mockBridge) ListAgents() []AgentInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AgentInfo, 0, len(m.agents))
	for _, a := range m.agents {
		out = append(out, a)
	}
	return out
}

func (m *mockBridge) GetAgent(name string) (AgentInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.agents[name]
	return a, ok
}

func (m *mockBridge) Process(ctx context.Context, agentName, sessionKey, content string) (string, error) {
	m.mu.Lock()
	m.calls = append(m.calls, agentName+"|"+sessionKey+"|"+content)
	delay := m.delay
	fail := m.fail
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if fail {
		return "", fmt.Errorf("boom")
	}
	return "echo: " + content, nil
}

func (m *mockBridge) SessionExists(sessionKey string) bool { return true }

func newTestServer(bridge AgentBridge) (*Server, *httptest.Server) {
	mgr := NewRunManager(bridge)
	srv := NewServer(Config{Enabled: true, AgentName: "lele-test"}, mgr)
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	return srv, ts
}

func TestPing(t *testing.T) {
	_, ts := newTestServer(newMockBridge())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body PingResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Protocol != "acp" || body.Version != ProtocolVersion {
		t.Fatalf("unexpected ping body: %+v", body)
	}
}

func TestListAgents(t *testing.T) {
	_, ts := newTestServer(newMockBridge())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body AgentsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(body.Agents))
	}
	for _, a := range body.Agents {
		if !ValidateAgentName(a.Name) {
			t.Fatalf("invalid agent name %q", a.Name)
		}
		if len(a.InputContentTypes) == 0 || len(a.OutputContentTypes) == 0 {
			t.Fatalf("missing content types on %q", a.Name)
		}
	}
}

func TestGetAgent(t *testing.T) {
	_, ts := newTestServer(newMockBridge())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/agents/chat")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	resp2, err := http.Get(ts.URL + "/agents/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("missing agent status = %d", resp2.StatusCode)
	}
}

func TestRunSync(t *testing.T) {
	bridge := newMockBridge()
	_, ts := newTestServer(bridge)
	defer ts.Close()

	payload := `{
		"agent_name": "chat",
		"mode": "sync",
		"input": [{"role":"user","parts":[{"content_type":"text/plain","content":"hello"}]}]
	}`
	resp, err := http.Post(ts.URL+"/runs", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e Error
		_ = json.NewDecoder(resp.Body).Decode(&e)
		t.Fatalf("status = %d err=%+v", resp.StatusCode, e)
	}
	var run Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	if run.Status != RunStatusCompleted {
		t.Fatalf("status = %s", run.Status)
	}
	if len(run.Output) != 1 {
		t.Fatalf("output len = %d", len(run.Output))
	}
	if got := run.Output[0].Text(); !strings.Contains(got, "hello") {
		t.Fatalf("output = %q", got)
	}
	if run.RunID == "" || run.SessionID == "" {
		t.Fatalf("missing ids: %+v", run)
	}
}

func TestRunAsync(t *testing.T) {
	bridge := newMockBridge()
	bridge.delay = 50 * time.Millisecond
	_, ts := newTestServer(bridge)
	defer ts.Close()

	payload := `{
		"agent_name": "chat",
		"mode": "async",
		"input": [{"role":"user","parts":[{"content_type":"text/plain","content":"hi"}]}]
	}`
	resp, err := http.Post(ts.URL+"/runs", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var run Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}

	// Poll until completed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r2, err := http.Get(ts.URL + "/runs/" + run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		var cur Run
		_ = json.NewDecoder(r2.Body).Decode(&cur)
		r2.Body.Close()
		if cur.Status == RunStatusCompleted {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("async run did not complete")
}

func TestRunUnknownAgent(t *testing.T) {
	_, ts := newTestServer(newMockBridge())
	defer ts.Close()

	payload := `{"agent_name":"nope","input":[{"role":"user","parts":[{"content_type":"text/plain","content":"x"}]}]}`
	resp, err := http.Post(ts.URL+"/runs", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestRunInvalidMode(t *testing.T) {
	_, ts := newTestServer(newMockBridge())
	defer ts.Close()

	payload := `{"agent_name":"chat","mode":"wat","input":[{"role":"user","parts":[{"content_type":"text/plain","content":"x"}]}]}`
	resp, err := http.Post(ts.URL+"/runs", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestRunFailed(t *testing.T) {
	bridge := newMockBridge()
	bridge.fail = true
	_, ts := newTestServer(bridge)
	defer ts.Close()

	payload := `{"agent_name":"chat","mode":"sync","input":[{"role":"user","parts":[{"content_type":"text/plain","content":"x"}]}]}`
	resp, err := http.Post(ts.URL+"/runs", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var run Run
	_ = json.NewDecoder(resp.Body).Decode(&run)
	if run.Status != RunStatusFailed {
		t.Fatalf("status = %s", run.Status)
	}
	if run.Error == nil {
		t.Fatal("expected error payload")
	}
}

func TestRunStream(t *testing.T) {
	_, ts := newTestServer(newMockBridge())
	defer ts.Close()

	payload := `{"agent_name":"chat","mode":"stream","input":[{"role":"user","parts":[{"content_type":"text/plain","content":"stream me"}]}]}`
	resp, err := http.Post(ts.URL+"/runs", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}

	body := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
			if strings.Contains(string(body), "run.completed") {
				break
			}
		}
		if err != nil {
			break
		}
	}
	s := string(body)
	for _, want := range []string{"run.created", "run.in-progress", "message.completed", "run.completed"} {
		if !strings.Contains(s, want) {
			t.Fatalf("stream missing %q in:\n%s", want, s)
		}
	}
}

func TestListEvents(t *testing.T) {
	_, ts := newTestServer(newMockBridge())
	defer ts.Close()

	payload := `{"agent_name":"chat","mode":"sync","input":[{"role":"user","parts":[{"content_type":"text/plain","content":"ev"}]}]}`
	resp, err := http.Post(ts.URL+"/runs", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	var run Run
	_ = json.NewDecoder(resp.Body).Decode(&run)
	resp.Body.Close()

	er, err := http.Get(ts.URL + "/runs/" + run.RunID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer er.Body.Close()
	var list RunEventsListResponse
	if err := json.NewDecoder(er.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Events) == 0 {
		t.Fatal("expected events")
	}
	if list.Events[0].Type != EventRunCreated {
		t.Fatalf("first event = %s", list.Events[0].Type)
	}
}

func TestCancelRun(t *testing.T) {
	bridge := newMockBridge()
	bridge.delay = 2 * time.Second
	srv, ts := newTestServer(bridge)
	defer ts.Close()

	payload := `{"agent_name":"chat","mode":"async","input":[{"role":"user","parts":[{"content_type":"text/plain","content":"slow"}]}]}`
	resp, err := http.Post(ts.URL+"/runs", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	var run Run
	_ = json.NewDecoder(resp.Body).Decode(&run)
	resp.Body.Close()

	cr, err := http.Post(ts.URL+"/runs/"+run.RunID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Body.Close()
	if cr.StatusCode != 202 {
		t.Fatalf("cancel status = %d", cr.StatusCode)
	}
	var cancelled Run
	_ = json.NewDecoder(cr.Body).Decode(&cancelled)
	if cancelled.Status != RunStatusCancelled && cancelled.Status != RunStatusCancelling {
		t.Fatalf("status after cancel = %s", cancelled.Status)
	}

	// Eventually cancelled.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, apiErr := srv.Manager().GetRun(run.RunID)
		if apiErr == nil && got.Status == RunStatusCancelled {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	final, _ := srv.Manager().GetRun(run.RunID)
	t.Fatalf("run not cancelled, status=%s", final.Status)
}

func TestAuthToken(t *testing.T) {
	bridge := newMockBridge()
	mgr := NewRunManager(bridge)
	srv := NewServer(Config{Enabled: true, Token: "secret"}, mgr)
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unauth status = %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/ping", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("auth status = %d", resp2.StatusCode)
	}
}

func TestSessionReuse(t *testing.T) {
	bridge := newMockBridge()
	_, ts := newTestServer(bridge)
	defer ts.Close()

	// First run creates a session.
	p1 := `{"agent_name":"chat","mode":"sync","input":[{"role":"user","parts":[{"content_type":"text/plain","content":"one"}]}]}`
	resp, err := http.Post(ts.URL+"/runs", "application/json", strings.NewReader(p1))
	if err != nil {
		t.Fatal(err)
	}
	var run1 Run
	_ = json.NewDecoder(resp.Body).Decode(&run1)
	resp.Body.Close()

	// Second run reuses it.
	p2 := fmt.Sprintf(`{"agent_name":"chat","session_id":%q,"mode":"sync","input":[{"role":"user","parts":[{"content_type":"text/plain","content":"two"}]}]}`, run1.SessionID)
	resp2, err := http.Post(ts.URL+"/runs", "application/json", strings.NewReader(p2))
	if err != nil {
		t.Fatal(err)
	}
	var run2 Run
	_ = json.NewDecoder(resp2.Body).Decode(&run2)
	resp2.Body.Close()

	if run2.SessionID != run1.SessionID {
		t.Fatalf("session ids differ: %s vs %s", run1.SessionID, run2.SessionID)
	}

	// Session endpoint.
	sr, err := http.Get(ts.URL + "/session/" + run1.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Body.Close()
	if sr.StatusCode != 200 {
		t.Fatalf("session status = %d", sr.StatusCode)
	}
}

func TestSanitizeAgentName(t *testing.T) {
	cases := map[string]string{
		"chat":          "chat",
		"My Agent":      "my-agent",
		"default_agent": "default-agent",
		"OK":            "ok",
		"":              "",
		"---":           "",
		"a":             "a",
	}
	for in, want := range cases {
		if got := SanitizeAgentName(in); got != want {
			t.Errorf("SanitizeAgentName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBridgeSanitizesIDs(t *testing.T) {
	src := &fakeSource{
		ids: []string{"main_agent", "Research Bot"},
		infos: map[string]AgentInfo{
			"main_agent":   {ID: "main_agent", Name: "Main", Description: "main"},
			"Research Bot": {ID: "Research Bot", Name: "Research Bot", Description: "research"},
		},
	}
	b := NewBridge(src)
	agents := b.ListAgents()
	if len(agents) != 2 {
		t.Fatalf("agents = %d", len(agents))
	}
	names := map[string]bool{}
	for _, a := range agents {
		if !ValidateAgentName(a.Name) {
			t.Errorf("illegal name %q", a.Name)
		}
		names[a.Name] = true
	}
	if !names["main-agent"] || !names["research-bot"] {
		t.Fatalf("names = %v", names)
	}

	info, ok := b.GetAgent("main-agent")
	if !ok || info.ID != "main_agent" {
		t.Fatalf("GetAgent main-agent -> %+v ok=%v", info, ok)
	}
}

type fakeSource struct {
	ids   []string
	infos map[string]AgentInfo
	mu    sync.Mutex
	last  struct{ agent, session, content string }
}

func (f *fakeSource) ListAvailableAgentIDs() []string { return f.ids }
func (f *fakeSource) GetAgentInfo(id string) (AgentInfo, bool) {
	a, ok := f.infos[id]
	return a, ok
}
func (f *fakeSource) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	f.mu.Lock()
	f.last.agent = channel
	f.last.session = sessionKey
	f.last.content = content
	f.mu.Unlock()
	return "ok:" + content, nil
}
func (f *fakeSource) SetSessionAgent(sessionKey, agentID string) {}
func (f *fakeSource) HasMessages(sessionKey string) bool         { return false }
