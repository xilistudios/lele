package channels

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/skills"
)

func TestHandleTools(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/tools", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ToolsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}

	// Verify common tools are present
	expectedTools := []string{"read_file", "write_file", "list_dir", "exec", "web_search", "web_fetch", "spawn"}
	toolNames := make(map[string]bool)
	for _, t := range payload.Tools {
		toolNames[t.Name] = true
	}
	for _, name := range expectedTools {
		if !toolNames[name] {
			t.Fatalf("expected tool %q not found", name)
		}
	}
}

func TestHandleModels(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Models) == 0 {
		t.Fatal("expected at least one model")
	}
}

func TestHandleSkills(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/skills", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SkillsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Skills == nil {
		t.Fatal("expected non-nil skills")
	}
}

func TestHandleStatus(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SystemStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Status != "running" {
		t.Fatalf("status = %q, want %q", payload.Status, "running")
	}
	if payload.Uptime == "" {
		t.Fatal("expected non-empty uptime")
	}
}

func TestHandleChannels(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/channels", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ChannelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Channels) == 0 {
		t.Fatal("expected at least one channel")
	}
	if payload.Channels[0].Name == "" {
		t.Fatal("expected non-empty channel name")
	}
}

func TestHandleSkillInstall_NoURL(t *testing.T) {
	ts := newNativeTestServer(t)

	body := mustMarshal(SkillInstallRequest{URL: ""})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills", strings.NewReader(string(body)))
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

func TestHandleSkillInstall_InvalidBody(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills", strings.NewReader("not-json"))
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

func TestHandleSkillRemove_NoName(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.server.URL+"/api/v1/skills/name/ ", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	// No name in path will route to 404
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	// A request without a name will match a different route or 404
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleSkillsAvailable(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/skills/available", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	// This may fail if there's no network, but it must return a valid response
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 200 or 500", resp.StatusCode)
	}
}

func TestHandleSkillScan_MissingRepo(t *testing.T) {
	ts := newNativeTestServer(t)

	body := `{}`
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/scan", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillScan_InvalidBody(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/scan", strings.NewReader("not-json"))
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

func TestHandleSkillInstallBatch_MissingRepo(t *testing.T) {
	ts := newNativeTestServer(t)

	body := `{"skills": ["weather"]}`
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/install-batch", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillInstallBatch_MissingSkills(t *testing.T) {
	ts := newNativeTestServer(t)

	body := `{"repo": "user/repo"}`
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/install-batch", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillInstallBatch_EmptySkills(t *testing.T) {
	ts := newNativeTestServer(t)

	body := `{"repo": "user/repo", "skills": []}`
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/install-batch", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillToggle_NoConfigManager(t *testing.T) {
	ts := newNativeTestServer(t)

	body := `{"enabled": true}`
	req, _ := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/skills/github/toggle", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	// Test server has no config manager initialized, so it returns 500
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
}

func TestHandleSkillToggle_InvalidBody(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/skills/github/toggle", strings.NewReader("not-json"))
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

func TestHandleSkillWorkspaceConfig(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/skills/workspace-config", nil)
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

	// Should have a "skills" key with enabled/disabled
	skills, ok := payload["skills"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'skills' key in response")
	}
	if _, hasEnabled := skills["enabled"]; !hasEnabled {
		t.Fatal("expected 'enabled' in skills config")
	}
	if _, hasDisabled := skills["disabled"]; !hasDisabled {
		t.Fatal("expected 'disabled' in skills config")
	}
}

func TestIsAllowedProviderURL(t *testing.T) {
	tests := []struct {
		url   string
		allow bool
	}{
		{"https://api.openai.com/v1", true},
		{"https://api.groq.com/openai/v1", true},
		{"http://api.openai.com/v1", false}, // no HTTPS
		{"https://localhost:11434/v1", false},
		{"https://127.0.0.1:8080", false},
		{"https://192.168.1.1/v1", false}, // private IP
		{"https://10.0.0.1/v1", false},    // private IP
		{"https://[::1]:11434/v1", false}, // loopback
		{"", false},
		{"not-a-url", false},
	}

	for _, tt := range tests {
		got := isAllowedProviderURL(tt.url)
		if got != tt.allow {
			t.Errorf("isAllowedProviderURL(%q) = %v, want %v", tt.url, got, tt.allow)
		}
	}
}

func TestParsePagination(t *testing.T) {
	tests := []struct {
		offset string
		limit  string
		wantO  int
		wantL  int
	}{
		{"", "", 0, 50},
		{"10", "", 10, 50},
		{"", "5", 0, 5},
		{"-5", "300", 0, 50}, // negative offset → 0, over limit 200 → default 50
		{"0", "100", 0, 100},
	}

	for _, tt := range tests {
		req, _ := http.NewRequest(http.MethodGet, "/api/v1/chat/sessions?offset="+tt.offset+"&limit="+tt.limit, nil)
		offset, limit := parsePagination(req)
		if offset != tt.wantO {
			t.Errorf("parsePagination(offset=%q) = %d, want %d", tt.offset, offset, tt.wantO)
		}
		if limit != tt.wantL {
			t.Errorf("parsePagination(limit=%q) = %d, want %d", tt.limit, limit, tt.wantL)
		}
	}
}

func TestDefaultAPIBaseByTypePublic(t *testing.T) {
	tests := []struct {
		providerType string
		want         string
	}{
		{"openai", "https://api.openai.com/v1"},
		{"gpt", "https://api.openai.com/v1"},
		{"groq", "https://api.groq.com/openai/v1"},
		{"deepseek", "https://api.deepseek.com/v1"},
		{"ollama", "http://localhost:11434/v1"},
		{"unknown", ""},
		// Catalog-backed providers
		{"xai", "https://api.x.ai/v1"},
		{"nous", "https://inference-api.nousresearch.com/v1"},
		{"vercel", "https://ai-gateway.vercel.sh/v1"},
		{"huggingface", "https://router.huggingface.co/v1"},
		{"minimax", "https://api.minimax.io/anthropic/v1"},
		{"kimi_for_coding", "https://api.kimi.com/coding/v1"},
	}

	for _, tt := range tests {
		got := defaultAPIBaseByTypePublic(tt.providerType)
		if got != tt.want {
			t.Errorf("defaultAPIBaseByTypePublic(%q) = %q, want %q", tt.providerType, got, tt.want)
		}
	}
}

// --- scoped install / source-aware remove ----------------------------------
//
// The global skills endpoints (POST /api/v1/skills, POST
// /api/v1/skills/install-batch, DELETE /api/v1/skills/{name}) used to be wired
// to a single installer anchored on the default workspace, which made a global
// install impossible and a global remove a 500. These tests pin the scope
// resolution and the source-aware dispatch added on top of installTarget.
//
// None of them hit the network: an install that finds its target directory
// already populated fails inside InstallFromGitHub before the fetch (F4), and
// one test stubs the github raw host to observe a real install end-to-end.

// scopedSkillsFixture is a native test server whose channel loader sees all
// three skill scopes: the default workspace, the shared global dir and lele's
// built-ins. That is exactly what the global endpoints resolve against.
type scopedSkillsFixture struct {
	ts         *nativeTestServer
	workspace  string // default agent workspace
	globalDir  string // <parent>/skills, shared by every agent
	builtinDir string // <parent>/skills, shipped with lele
}

func newScopedSkillsFixture(t *testing.T) scopedSkillsFixture {
	t.Helper()

	ts := newNativeTestServer(t)
	workspace := t.TempDir()
	// The installer re-appends "skills", so the shared dirs are handed to the
	// loader as <parent>/skills.
	globalDir := filepath.Join(t.TempDir(), "skills")
	builtinDir := filepath.Join(t.TempDir(), "skills")
	ts.channel.skillsLoader = skills.NewSkillsLoader(workspace, globalDir, builtinDir)

	return scopedSkillsFixture{ts: ts, workspace: workspace, globalDir: globalDir, builtinDir: builtinDir}
}

// skillsRoutePayload covers every field these tests assert across the skills
// routes: the install success body (skill_id/message), the batch body
// (scope/installed/count) and the error body (code/message/error).
type skillsRoutePayload struct {
	SkillID   string   `json:"skill_id"`
	Message   string   `json:"message"`
	Code      string   `json:"code"`
	Error     string   `json:"error"`
	Scope     string   `json:"scope"`
	Installed []string `json:"installed"`
	Count     int      `json:"count"`
}

func postSkillsPayload(t *testing.T, ts *nativeTestServer, path, body string) (int, skillsRoutePayload) {
	t.Helper()

	resp, err := http.DefaultClient.Do(authedBodyRequest(t, http.MethodPost, ts.server.URL+path, ts.token, body))
	if err != nil {
		t.Fatalf("Do(POST %s): %v", path, err)
	}
	defer resp.Body.Close()

	var payload skillsRoutePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode(POST %s): %v", path, err)
	}
	return resp.StatusCode, payload
}

// testSkillMarkdown is a minimal SKILL.md with valid frontmatter.
func testSkillMarkdown(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\nbody\n"
}

// githubStubTransport answers raw.githubusercontent.com with a canned SKILL.md
// so the install endpoints can be exercised end-to-end without network egress.
// Every other host is delegated to the real transport, which keeps the test's
// own calls to the httptest server working.
type githubStubTransport struct {
	real   http.RoundTripper
	body   string
	status int
	calls  []string
}

func (t *githubStubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "raw.githubusercontent.com" {
		return t.real.RoundTrip(req)
	}
	t.calls = append(t.calls, req.URL.String())
	return &http.Response{
		StatusCode: t.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Request:    req,
	}, nil
}

// stubGitHubInstall swaps http.DefaultTransport for a canned github raw host
// for the duration of the test. The skills installer builds its own
// http.Client, so the transport is the only seam available. The returned stub
// lets the caller change the served body between installs.
func stubGitHubInstall(t *testing.T) *githubStubTransport {
	t.Helper()

	orig := http.DefaultTransport
	stub := &githubStubTransport{
		real:   orig,
		status: http.StatusOK,
		body:   testSkillMarkdown("skill", "canned"),
	}
	http.DefaultTransport = stub
	t.Cleanup(func() { http.DefaultTransport = orig })

	return stub
}

// fanoutSkillsTestLoop widens skillsTestLoop's agent list so the invalidation
// fan-out of a GLOBAL mutation (which walks ListAvailableAgentIDs) covers more
// than the base fake's single "main" agent.
type fanoutSkillsTestLoop struct {
	*skillsTestLoop
	ids []string
}

func (l *fanoutSkillsTestLoop) ListAvailableAgentIDs() []string {
	return l.ids
}

// TestHandleSkillInstall_ScopeGlobalTargetsSharedDir proves the scope resolves
// to the shared dir without any network call: InstallFromGitHub stats the
// target directory before it builds its request, so a copy pre-seeded in the
// GLOBAL dir surfaces as "already exists" only because the request resolved
// there.
func TestHandleSkillInstall_ScopeGlobalTargetsSharedDir(t *testing.T) {
	fx := newScopedSkillsFixture(t)
	writeTestSkill(t, filepath.Dir(fx.globalDir), "weather", "shared weather")

	status, payload := postSkillsPayload(t, fx.ts, "/api/v1/skills", `{"url":"owner/weather","scope":"global"}`)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if !strings.Contains(payload.Message, "already exists") {
		t.Fatalf("message = %q, want the shared-dir collision", payload.Message)
	}
	if _, err := os.Stat(filepath.Join(fx.workspace, "skills", "weather")); !os.IsNotExist(err) {
		t.Fatalf("default workspace copy stat err = %v, want IsNotExist", err)
	}
}

// TestHandleSkillInstall_NoScopeKeepsDefaultWorkspace pins backward
// compatibility: omitting scope still writes to the default workspace.
func TestHandleSkillInstall_NoScopeKeepsDefaultWorkspace(t *testing.T) {
	fx := newScopedSkillsFixture(t)
	writeTestSkill(t, fx.workspace, "weather", "workspace weather")

	status, payload := postSkillsPayload(t, fx.ts, "/api/v1/skills", `{"url":"owner/weather"}`)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if !strings.Contains(payload.Message, "already exists") {
		t.Fatalf("message = %q, want the workspace collision", payload.Message)
	}
	if _, err := os.Stat(filepath.Join(fx.globalDir, "weather")); !os.IsNotExist(err) {
		t.Fatalf("global copy stat err = %v, want IsNotExist", err)
	}
}

// TestHandleSkillInstall_UnknownScopeIs400 checks the rejection names both
// valid values and writes nothing anywhere.
func TestHandleSkillInstall_UnknownScopeIs400(t *testing.T) {
	fx := newScopedSkillsFixture(t)

	status, payload := postSkillsPayload(t, fx.ts, "/api/v1/skills", `{"url":"owner/weather","scope":"nope"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	if payload.Code != "invalid_scope" {
		t.Fatalf("code = %q, want invalid_scope", payload.Code)
	}
	for _, want := range []string{workspaceSkillScope, globalSkillScope} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("message = %q, want it to name %q", payload.Message, want)
		}
	}
	for _, dir := range []string{filepath.Join(fx.workspace, "skills", "weather"), filepath.Join(fx.globalDir, "weather")} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%s stat err = %v, want IsNotExist", dir, err)
		}
	}
}

// TestHandleSkillInstall_GlobalScopeInvalidatesEveryAgentPrompt drives a real
// (stubbed) install and asserts the shared dir received the file while every
// agent's cached prompt is dropped afterwards. A workspace-scope install must
// not fan out: it changes one agent's view, not everyone's.
func TestHandleSkillInstall_GlobalScopeInvalidatesEveryAgentPrompt(t *testing.T) {
	ts, loop, dirs := newSkillsTestServer(t)
	// Give the CHANNEL loader a shared dir too: this is the loader the global
	// endpoints resolve against.
	globalDir := filepath.Join(t.TempDir(), "skills")
	ts.channel.skillsLoader = skills.NewSkillsLoader(dirs["default"], globalDir, "")

	fanout := &fanoutSkillsTestLoop{skillsTestLoop: loop, ids: []string{"alpha", "beta"}}
	ts.channel.agentLoop = fanout

	stub := stubGitHubInstall(t)

	stub.body = testSkillMarkdown("ws-only", "workspace scoped")
	status, payload := postSkillsPayload(t, ts, "/api/v1/skills", `{"url":"owner/ws-only"}`)
	if status != http.StatusCreated {
		t.Fatalf("workspace install status = %d, want %d", status, http.StatusCreated)
	}
	if payload.SkillID != "ws-only" || !strings.Contains(payload.Message, "("+workspaceSkillScope+")") {
		t.Fatalf("workspace install body = %+v, want skill_id ws-only and a workspace scope", payload)
	}
	if got := fanout.invalidations; len(got) != 0 {
		t.Fatalf("invalidations after a workspace install = %v, want none", got)
	}

	stub.body = testSkillMarkdown("weather", "global scoped")
	status, payload = postSkillsPayload(t, ts, "/api/v1/skills", `{"url":"owner/weather","scope":"global"}`)
	if status != http.StatusCreated {
		t.Fatalf("global install status = %d, want %d", status, http.StatusCreated)
	}
	if payload.SkillID != "weather" || !strings.Contains(payload.Message, "("+globalSkillScope+")") {
		t.Fatalf("global install body = %+v, want skill_id weather and a global scope", payload)
	}
	if _, err := os.Stat(filepath.Join(globalDir, "weather", "SKILL.md")); err != nil {
		t.Fatalf("global skill was not created in the shared dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirs["default"], "skills", "weather")); !os.IsNotExist(err) {
		t.Fatalf("default workspace copy stat err = %v, want IsNotExist", err)
	}
	if got := fanout.invalidations; !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("invalidations after a global install = %v, want [alpha beta]", got)
	}

	// Removing the shared skill must fan out again.
	resp, err := http.DefaultClient.Do(authedBodyRequest(t, http.MethodDelete,
		ts.server.URL+"/api/v1/skills/weather", ts.token, ""))
	if err != nil {
		t.Fatalf("Do(DELETE): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if _, err := os.Stat(filepath.Join(globalDir, "weather")); !os.IsNotExist(err) {
		t.Fatalf("shared skill stat err = %v, want IsNotExist", err)
	}
	if got := fanout.invalidations; !slices.Equal(got, []string{"alpha", "beta", "alpha", "beta"}) {
		t.Fatalf("invalidations after removing a global skill = %v, want [alpha beta alpha beta]", got)
	}
}

// TestHandleSkillInstallBatch_EchoesResolvedScope pins the batch response's
// scope echo. Existing skills are skipped before any fetch (F5), which is what
// keeps both cases network-free.
func TestHandleSkillInstallBatch_EchoesResolvedScope(t *testing.T) {
	t.Run("global scope is echoed and used", func(t *testing.T) {
		fx := newScopedSkillsFixture(t)
		writeTestSkill(t, filepath.Dir(fx.globalDir), "a", "shared a")

		status, payload := postSkillsPayload(t, fx.ts, "/api/v1/skills/install-batch",
			`{"repo":"owner/repo","skills":["a"],"scope":"global"}`)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want %d", status, http.StatusOK)
		}
		if payload.Scope != globalSkillScope {
			t.Fatalf("scope = %q, want %q", payload.Scope, globalSkillScope)
		}
		if payload.Count != 0 || len(payload.Installed) != 0 {
			t.Fatalf("installed = %v (count %d), want the shared skill skipped", payload.Installed, payload.Count)
		}
		if _, err := os.Stat(filepath.Join(fx.workspace, "skills", "a")); !os.IsNotExist(err) {
			t.Fatalf("default workspace copy stat err = %v, want IsNotExist", err)
		}
	})

	t.Run("omitted scope resolves to workspace", func(t *testing.T) {
		fx := newScopedSkillsFixture(t)
		writeTestSkill(t, fx.workspace, "a", "workspace a")

		status, payload := postSkillsPayload(t, fx.ts, "/api/v1/skills/install-batch",
			`{"repo":"owner/repo","skills":["a"]}`)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want %d", status, http.StatusOK)
		}
		if payload.Scope != workspaceSkillScope {
			t.Fatalf("scope = %q, want %q", payload.Scope, workspaceSkillScope)
		}
	})
}

// TestHandleSkillInstallBatch_UnknownScopeIs400 mirrors the single-install
// rejection on the batch route.
func TestHandleSkillInstallBatch_UnknownScopeIs400(t *testing.T) {
	fx := newScopedSkillsFixture(t)

	status, payload := postSkillsPayload(t, fx.ts, "/api/v1/skills/install-batch",
		`{"repo":"owner/repo","skills":["a"],"scope":"nope"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	if payload.Code != "invalid_scope" {
		t.Fatalf("code = %q, want invalid_scope", payload.Code)
	}
	for _, want := range []string{workspaceSkillScope, globalSkillScope} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("message = %q, want it to name %q", payload.Message, want)
		}
	}
	if _, err := os.Stat(filepath.Join(fx.workspace, "skills", "a")); !os.IsNotExist(err) {
		t.Fatalf("default workspace copy stat err = %v, want IsNotExist", err)
	}
}

// TestHandleSkillRemove_BySource is the regression test for the 500 that a
// global skill used to produce: the handler now resolves the name through the
// loader's own view and dispatches to the directory that actually holds it.
func TestHandleSkillRemove_BySource(t *testing.T) {
	cases := []struct {
		name     string
		source   string // which dir gets seeded; "" leaves the name uninstalled
		want     int
		wantCode string
		direct   bool // call the handler with a path value the router would rewrite
	}{
		{"workspace skill is removed from its own dir", "workspace", http.StatusOK, "", false},
		{"global skill is removed from the shared dir", "global", http.StatusOK, "", false},
		{"builtin skill is refused", "builtin", http.StatusForbidden, "builtin_skill", false},
		{"unknown skill is not found", "", http.StatusNotFound, "not_found", false},
		{"path traversal is never resolved", "", http.StatusNotFound, "not_found", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newScopedSkillsFixture(t)
			skillName := "target-skill"
			switch tc.source {
			case "workspace":
				writeTestSkill(t, fx.workspace, skillName, "workspace copy")
			case "global":
				writeTestSkill(t, filepath.Dir(fx.globalDir), skillName, "global copy")
			case "builtin":
				writeTestSkill(t, filepath.Dir(fx.builtinDir), skillName, "builtin copy")
			}
			if tc.direct {
				// A raw "../evil" never reaches the handler through ServeMux
				// (it cleans the path first), so inject the path value the
				// router would have produced for an unescaped traversal.
				skillName = "../evil"
			}

			var status int
			var payload skillsRoutePayload
			if tc.direct {
				req := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/x", nil)
				req.SetPathValue("name", skillName)
				rec := httptest.NewRecorder()
				fx.ts.channel.handleSkillRemove(rec, req)
				status = rec.Code
				if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
					t.Fatalf("Decode: %v", err)
				}
			} else {
				resp, err := http.DefaultClient.Do(authedBodyRequest(t, http.MethodDelete,
					fx.ts.server.URL+"/api/v1/skills/"+skillName, fx.ts.token, ""))
				if err != nil {
					t.Fatalf("Do(DELETE): %v", err)
				}
				defer resp.Body.Close()
				status = resp.StatusCode
				if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
					t.Fatalf("Decode: %v", err)
				}
			}

			if status != tc.want {
				t.Fatalf("status = %d, want %d (body %+v)", status, tc.want, payload)
			}
			if payload.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", payload.Code, tc.wantCode)
			}

			switch tc.source {
			case "workspace":
				if _, err := os.Stat(filepath.Join(fx.workspace, "skills", "target-skill")); !os.IsNotExist(err) {
					t.Fatalf("workspace skill stat err = %v, want IsNotExist", err)
				}
			case "global":
				if _, err := os.Stat(filepath.Join(fx.globalDir, "target-skill")); !os.IsNotExist(err) {
					t.Fatalf("global skill stat err = %v, want IsNotExist", err)
				}
				if _, err := os.Stat(filepath.Join(fx.workspace, "skills", "target-skill")); !os.IsNotExist(err) {
					t.Fatalf("workspace copy stat err = %v, want IsNotExist", err)
				}
			case "builtin":
				if _, err := os.Stat(filepath.Join(fx.builtinDir, "target-skill", "SKILL.md")); err != nil {
					t.Fatalf("builtin skill must survive: %v", err)
				}
			}
		})
	}
}
