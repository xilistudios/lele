// Tests for the MCP management endpoints (rest_mcp.go): the read-only
// inventory/raw views plus the per-layer enable/disable toggle.
//
// The central risk these tests target is secret leakage: every response must
// be built from RAW bytes (ReadInventory/ServerSummary), so an env VALUE like
// CANARY-secret — which mcp.ParseFile WOULD have expanded into the document —
// must never appear in any encoded body. The rest pins routing, auth, merge
// precedence (one row per name + shadow provenance) and the error codes.

package channels

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/mcp"
)

// mcpTestLoop is a fake agent loop implementing the optional mcpPathsSource
// capability: each known agent maps to a fixed mcp.Paths; unknown ids answer
// ok=false (mirroring the real MCPPathsFor contract for unknown agents).
type mcpTestLoop struct {
	*nativeTestAgentLoop
	paths map[string]mcp.Paths
}

func (l *mcpTestLoop) MCPPathsFor(agentID string) (mcp.Paths, bool) {
	p, ok := l.paths[agentID]
	return p, ok
}

// mcpFixture wires a test server whose loop knows agent "agent1" with three
// distinct on-disk roots.
type mcpFixture struct {
	ts         *nativeTestServer
	loop       *mcpTestLoop
	globalDir  string // LeleDir root
	agentDir   string // AgentWorkspace root (NO mcp.json: missing layer)
	projectDir string // Cwd root (project file at <dir>/.lele/mcp.json)
	paths      mcp.Paths
	globalRaw  string // literal bytes of <globalDir>/mcp.json
}

// writeMCPJSON writes raw JSON content at path, creating parent dirs.
func writeMCPJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// newMCPFixture builds three distinct roots and the fixture documents:
//
//	global  mcp.json: shared, only-global, broken (mixed fields), off (disabled), canary (env with ${CANARY})
//	agent   mcp.json: ABSENT (missing layer)
//	project mcp.json: shared (shadows the global copy)
func newMCPFixture(t *testing.T) *mcpFixture {
	t.Helper()

	globalDir := t.TempDir()
	agentDir := t.TempDir()
	projectDir := t.TempDir()

	globalRaw := `{
  "mcpServers": {
    "shared": {"command": "global-cmd", "description": "global copy"},
    "only-global": {"command": "og"},
    "broken": {"command": "run", "url": "http://mix.example"},
    "off": {"command": "off-cmd", "disabled": true},
    "canary": {"command": "run-canary", "env": {"TOKEN": "${CANARY}"}}
  }
}`
	projectRaw := `{
  "mcpServers": {
    "shared": {"command": "project-cmd", "env": {"TOKEN": "${CANARY}"}}
  }
}`
	writeMCPJSON(t, filepath.Join(globalDir, "mcp.json"), globalRaw)
	writeMCPJSON(t, filepath.Join(projectDir, ".lele", "mcp.json"), projectRaw)

	paths := mcp.Paths{LeleDir: globalDir, AgentWorkspace: agentDir, Cwd: projectDir}

	ts := newNativeTestServer(t)
	loop := &mcpTestLoop{
		nativeTestAgentLoop: ts.loop,
		paths:               map[string]mcp.Paths{"agent1": paths},
	}
	ts.channel.agentLoop = loop

	return &mcpFixture{
		ts:         ts,
		loop:       loop,
		globalDir:  globalDir,
		agentDir:   agentDir,
		projectDir: projectDir,
		paths:      paths,
		globalRaw:  globalRaw,
	}
}

// mcpGet issues a GET; authed=false sends no Authorization header.
func mcpGet(t *testing.T, ts *nativeTestServer, url string, authed bool) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest(%s): %v", url, err)
	}
	if authed {
		req.Header.Set("Authorization", "Bearer "+ts.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(%s): %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(%s): %v", url, err)
	}
	return resp.StatusCode, body
}

func mcpInventory(t *testing.T, f *mcpFixture, agentID string) (int, MCPInventoryResponse, []byte) {
	t.Helper()
	status, body := mcpGet(t, f.ts, f.ts.server.URL+"/api/v1/mcp?agent_id="+agentID, true)
	var out MCPInventoryResponse
	if status == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("Decode() error = %v body=%s", err, body)
		}
	}
	return status, out, body
}

func mcpRow(t *testing.T, inv MCPInventoryResponse, name string) MCPServerRow {
	t.Helper()
	for _, row := range inv.Servers {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("row %q missing from %+v", name, inv.Servers)
	return MCPServerRow{}
}

// --- routing / auth ---------------------------------------------------------

// TestMCPRoutes_RequireBearerToken proves both routes are registered (a
// registered route rejects the caller with 401; an unregistered one would
// answer 404) and that withAuth guards them.
func TestMCPRoutes_RequireBearerToken(t *testing.T) {
	f := newMCPFixture(t)
	urls := []string{
		f.ts.server.URL + "/api/v1/mcp?agent_id=agent1",
		f.ts.server.URL + "/api/v1/mcp/global/raw?agent_id=agent1",
	}
	for _, url := range urls {
		status, body := mcpGet(t, f.ts, url, false)
		if status != http.StatusUnauthorized {
			t.Fatalf("GET %s without token: status = %d, want 401 (404 would mean the route is not registered); body=%s",
				url, status, body)
		}
	}
}

// --- inventory --------------------------------------------------------------

// TestMCPInventory_Rows pins the row contract: one row per NAME (the winner
// only), sorted by name, each carrying layer/path/effective plus the raw-byte
// summary; layers stay in ReadInventory's low → high order.
func TestMCPInventory_Rows(t *testing.T) {
	f := newMCPFixture(t)

	status, inv, body := mcpInventory(t, f, "agent1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if inv.AgentID != "agent1" {
		t.Fatalf("agent_id = %q, want agent1", inv.AgentID)
	}

	// Layers low → high; agent layer has no file.
	if len(inv.Layers) != 3 {
		t.Fatalf("layers = %+v, want 3 rows", inv.Layers)
	}
	wantLayers := []MCPLayer{
		{Layer: "global", Path: filepath.Join(f.globalDir, "mcp.json"), Exists: true},
		{Layer: "agent", Path: filepath.Join(f.agentDir, "mcp.json"), Exists: false},
		{Layer: "project", Path: filepath.Join(f.projectDir, ".lele", "mcp.json"), Exists: true},
	}
	for i, want := range wantLayers {
		got := inv.Layers[i]
		if got.Layer != want.Layer || got.Path != want.Path || got.Exists != want.Exists {
			t.Errorf("layers[%d] = %+v, want %+v", i, got, want)
		}
	}

	// One row per name, sorted.
	var names []string
	for _, row := range inv.Servers {
		names = append(names, row.Name)
	}
	if got := strings.Join(names, ","); got != "broken,canary,off,only-global,shared" {
		t.Fatalf("server names = %q, want sorted broken,canary,off,only-global,shared", got)
	}

	// Winner fields: global copy shadowed by the project copy → project wins.
	shared := mcpRow(t, inv, "shared")
	if shared.Layer != "project" || shared.Path != filepath.Join(f.projectDir, ".lele", "mcp.json") {
		t.Fatalf("shared winner = %q %q, want project layer file", shared.Layer, shared.Path)
	}
	if shared.Effective != "enabled" || !shared.Defines {
		t.Fatalf("shared = effective %q defines %v, want enabled/true", shared.Effective, shared.Defines)
	}
	if shared.Server.Command != "project-cmd" {
		t.Fatalf("shared command = %q, want project-cmd", shared.Server.Command)
	}
	if len(shared.Server.EnvKeys) != 1 || shared.Server.EnvKeys[0] != "TOKEN" {
		t.Fatalf("shared env_keys = %v, want [TOKEN]", shared.Server.EnvKeys)
	}

	// A plain enabled winner in its own layer.
	og := mcpRow(t, inv, "only-global")
	if og.Layer != "global" || og.Effective != "enabled" {
		t.Fatalf("only-global = %+v, want global/enabled", og)
	}
}

// TestMCPInventory_ShadowedGlobalAppearsOnce covers the merge view: a global
// name that also exists in the project layer must appear ONCE (the winner,
// project) with the inert global copy listed as shadowed — provenance of the
// losing copy, not a second row.
func TestMCPInventory_ShadowedGlobalAppearsOnce(t *testing.T) {
	f := newMCPFixture(t)

	status, inv, body := mcpInventory(t, f, "agent1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}

	count := 0
	for _, row := range inv.Servers {
		if row.Name == "shared" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("shared appears %d times, want exactly 1 (winner row)", count)
	}

	shared := mcpRow(t, inv, "shared")
	if len(shared.Shadowed) != 1 {
		t.Fatalf("shared shadowed = %+v, want exactly 1 entry", shared.Shadowed)
	}
	shadow := shared.Shadowed[0]
	// NOTE (deviation vs the task sketch): inventory.Shadowed holds the copies
	// BELOW the winner, so the entry is the losing GLOBAL copy and the reason
	// names the winning layer.
	if shadow.Layer != "global" || shadow.Path != filepath.Join(f.globalDir, "mcp.json") {
		t.Fatalf("shadowed entry = %+v, want the global copy", shadow)
	}
	if shadow.Reason != "shadowed by project" {
		t.Fatalf("shadowed reason = %q, want %q", shadow.Reason, "shadowed by project")
	}
}

// TestMCPInventory_EffectiveVerdicts pins the three verdict strings: an entry
// Discover refuses (mixed stdio/remote) → invalid; a disabled winner →
// disabled; valid winners → enabled.
func TestMCPInventory_EffectiveVerdicts(t *testing.T) {
	f := newMCPFixture(t)

	status, inv, body := mcpInventory(t, f, "agent1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}

	if got := mcpRow(t, inv, "broken").Effective; got != "invalid" {
		t.Fatalf("broken effective = %q, want invalid", got)
	}
	off := mcpRow(t, inv, "off")
	if off.Effective != "disabled" || !off.Server.Disabled {
		t.Fatalf("off = effective %q server.disabled %v, want disabled/true", off.Effective, off.Server.Disabled)
	}
	if got := mcpRow(t, inv, "canary").Effective; got != "enabled" {
		t.Fatalf("canary effective = %q, want enabled", got)
	}
}

// TestMCPInventory_SecretCanary is the security test: the fixture's env value
// is the literal ${CANARY} and the variable holds CANARY-secret. If any code
// path used mcp.ParseFile (expanding ${VAR}), that value could reach the
// response; the raw-bytes path must make its appearance impossible anywhere
// in the encoded body.
func TestMCPInventory_SecretCanary(t *testing.T) {
	f := newMCPFixture(t)
	t.Setenv("CANARY", "CANARY-secret")

	status, _, body := mcpInventory(t, f, "agent1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if strings.Contains(string(body), "CANARY-secret") {
		t.Fatalf("expanded secret %q leaked into the inventory body: %s", "CANARY-secret", body)
	}
	if !strings.Contains(string(body), "TOKEN") {
		t.Fatalf("expected env key TOKEN to be visible in the body: %s", body)
	}
}

// TestMCPInventory_ErrorCodes pins the resolution failures: missing
// agent_id → 400, unknown agent → 404, loop without the capability → 500.
func TestMCPInventory_ErrorCodes(t *testing.T) {
	f := newMCPFixture(t)

	t.Run("missing agent_id", func(t *testing.T) {
		status, body := mcpGet(t, f.ts, f.ts.server.URL+"/api/v1/mcp", true)
		if status != http.StatusBadRequest || !strings.Contains(string(body), "agent_id_missing") {
			t.Fatalf("status = %d body = %s, want 400 agent_id_missing", status, body)
		}
	})

	t.Run("unknown agent", func(t *testing.T) {
		status, body := mcpGet(t, f.ts, f.ts.server.URL+"/api/v1/mcp?agent_id=ghost", true)
		if status != http.StatusNotFound || !strings.Contains(string(body), "agent_not_found") {
			t.Fatalf("status = %d body = %s, want 404 agent_not_found", status, body)
		}
	})

	t.Run("loop without capability", func(t *testing.T) {
		// A fresh server keeps the default loop, which does not implement
		// mcpPathsSource.
		ts := newNativeTestServer(t)
		status, body := mcpGet(t, ts, ts.server.URL+"/api/v1/mcp?agent_id=main", true)
		if status != http.StatusInternalServerError || !strings.Contains(string(body), "mcp_unavailable") {
			t.Fatalf("status = %d body = %s, want 500 mcp_unavailable", status, body)
		}
	})
}

// --- raw file ----------------------------------------------------------------

// TestMCPRawFile_LiteralBytesUnexpanded serves the global layer and asserts
// the response content equals the file bytes verbatim — ${VAR} stays literal
// and the expanded canary secret appears nowhere.
func TestMCPRawFile_LiteralBytesUnexpanded(t *testing.T) {
	f := newMCPFixture(t)
	t.Setenv("CANARY", "CANARY-secret")

	status, body := mcpGet(t, f.ts, f.ts.server.URL+"/api/v1/mcp/global/raw?agent_id=agent1", true)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var raw MCPRawFileResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("Decode() error = %v body=%s", err, body)
	}
	if !raw.Exists {
		t.Fatal("exists = false, want true")
	}
	if raw.Layer != "global" || raw.Path != filepath.Join(f.globalDir, "mcp.json") {
		t.Fatalf("layer/path = %q %q, want global %q", raw.Layer, raw.Path, filepath.Join(f.globalDir, "mcp.json"))
	}
	if raw.Content != f.globalRaw {
		t.Fatalf("content = %q, want the literal file bytes %q", raw.Content, f.globalRaw)
	}
	if !strings.Contains(raw.Content, "${CANARY}") {
		t.Fatalf("content must keep ${CANARY} literal: %s", raw.Content)
	}
	if strings.Contains(string(body), "CANARY-secret") {
		t.Fatalf("expanded secret leaked into raw body: %s", body)
	}
}

// TestMCPRawFile_MissingFileIsExistsFalse: layers are optional — a layer root
// without an mcp.json answers 200 with exists:false and empty content, not an
// error.
func TestMCPRawFile_MissingFileIsExistsFalse(t *testing.T) {
	f := newMCPFixture(t)

	status, body := mcpGet(t, f.ts, f.ts.server.URL+"/api/v1/mcp/agent/raw?agent_id=agent1", true)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var raw MCPRawFileResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("Decode() error = %v body=%s", err, body)
	}
	if raw.Exists {
		t.Fatal("exists = true, want false")
	}
	if raw.Content != "" {
		t.Fatalf("content = %q, want empty", raw.Content)
	}
	if raw.Path != filepath.Join(f.agentDir, "mcp.json") {
		t.Fatalf("path = %q, want the agent layer path", raw.Path)
	}
}

// TestMCPRawFile_EmptyRootUnavailable: an agent whose roots are all empty
// resolves every layer to "" — the handler must answer 400
// mcp_layer_unavailable WITHOUT reading or creating anything (an empty root
// must never become a process-relative path). The test runs from a scratch
// cwd so a leaked relative write would be visible.
func TestMCPRawFile_EmptyRootUnavailable(t *testing.T) {
	f := newMCPFixture(t)
	f.loop.paths["empty"] = mcp.Paths{}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	scratch := t.TempDir()
	if err := os.Chdir(scratch); err != nil {
		t.Fatalf("Chdir(%s): %v", scratch, err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	for _, layer := range []string{"global", "agent", "project"} {
		status, body := mcpGet(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/"+layer+"/raw?agent_id=empty", true)
		if status != http.StatusBadRequest || !strings.Contains(string(body), "mcp_layer_unavailable") {
			t.Fatalf("layer %s: status = %d body = %s, want 400 mcp_layer_unavailable", layer, status, body)
		}
	}

	// Nothing may exist in the scratch cwd — above all not the relative
	// ".lele/mcp.json" a naive join would have produced.
	for _, rel := range []string{"mcp.json", ".lele/mcp.json"} {
		if _, err := os.Stat(filepath.Join(scratch, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s exists in cwd after the request (err=%v)", rel, err)
		}
	}
}

// --- validation --------------------------------------------------------------

// TestMCPValidation_InvalidLayerAndUnknownAgent: layer ∉ global|agent|project
// → 400 invalid_layer (checked BEFORE any path resolution), and an unknown
// agent on the raw route answers 404 like the inventory route.
func TestMCPValidation_InvalidLayerAndUnknownAgent(t *testing.T) {
	f := newMCPFixture(t)

	t.Run("bogus layer", func(t *testing.T) {
		status, body := mcpGet(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/bogus/raw?agent_id=agent1", true)
		if status != http.StatusBadRequest || !strings.Contains(string(body), "invalid_layer") {
			t.Fatalf("status = %d body = %s, want 400 invalid_layer", status, body)
		}
	})

	t.Run("auto layer is refused too", func(t *testing.T) {
		status, body := mcpGet(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/auto/raw?agent_id=agent1", true)
		if status != http.StatusBadRequest || !strings.Contains(string(body), "invalid_layer") {
			t.Fatalf("status = %d body = %s, want 400 invalid_layer", status, body)
		}
	})

	t.Run("unknown agent", func(t *testing.T) {
		status, body := mcpGet(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/global/raw?agent_id=ghost", true)
		if status != http.StatusNotFound || !strings.Contains(string(body), "agent_not_found") {
			t.Fatalf("status = %d body = %s, want 404 agent_not_found", status, body)
		}
	})

	t.Run("missing agent_id", func(t *testing.T) {
		status, body := mcpGet(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/global/raw", true)
		if status != http.StatusBadRequest || !strings.Contains(string(body), "agent_id_missing") {
			t.Fatalf("status = %d body = %s, want 400 agent_id_missing", status, body)
		}
	})
}

// --- toggle (PUT .../servers/{name}/toggle) --------------------------------

// mcpPut issues an authenticated PUT with a JSON body.
func mcpPut(t *testing.T, ts *nativeTestServer, url, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest(%s): %v", url, err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(%s): %v", url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(%s): %v", url, err)
	}
	return resp.StatusCode, data
}

// mcpToggle wraps mcpPut and decodes the 200 body into MCPToggleResponse.
func mcpToggle(t *testing.T, f *mcpFixture, url, body string) (int, MCPToggleResponse, []byte) {
	t.Helper()
	status, raw := mcpPut(t, f.ts, url, body)
	var out MCPToggleResponse
	if status == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("Decode() error = %v body=%s", err, raw)
		}
	}
	return status, out, raw
}

// mcpCountReloads wires the reload seam to a counter and returns a reader.
func mcpCountReloads(t *testing.T, f *mcpFixture) func() int {
	t.Helper()
	n := 0
	f.ts.channel.SetReloadMCP(func() { n++ })
	return func() int { return n }
}

// mcpReadFile reads a file or fails the test.
func mcpReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return data
}

// newMCPSoloFixture builds a single-server GLOBAL-only fixture (agent and
// project layers empty) for toggle tests that need clean byte assertions.
func newMCPSoloFixture(t *testing.T, globalJSON string) *mcpFixture {
	t.Helper()
	globalDir := t.TempDir()
	agentDir := t.TempDir()
	projectDir := t.TempDir()
	writeMCPJSON(t, filepath.Join(globalDir, "mcp.json"), globalJSON)
	paths := mcp.Paths{LeleDir: globalDir, AgentWorkspace: agentDir, Cwd: projectDir}
	ts := newNativeTestServer(t)
	loop := &mcpTestLoop{
		nativeTestAgentLoop: ts.loop,
		paths:               map[string]mcp.Paths{"agent1": paths},
	}
	ts.channel.agentLoop = loop
	return &mcpFixture{
		ts: ts, loop: loop,
		globalDir: globalDir, agentDir: agentDir, projectDir: projectDir,
		paths: paths, globalRaw: globalJSON,
	}
}

// TestMCPToggle_AutoWritesOwningLayer: auto resolves to the layer owning the
// winning copy — a global-only server is disabled IN THE GLOBAL FILE, and the
// response reports the resolved layer plus the post-write verdict.
func TestMCPToggle_AutoWritesOwningLayer(t *testing.T) {
	f := newMCPSoloFixture(t, `{"mcpServers": {"solo": {"command": "solo-cmd"}}}`)
	reloads := mcpCountReloads(t, f)

	status, resp, body := mcpToggle(t, f,
		f.ts.server.URL+"/api/v1/mcp/auto/servers/solo/toggle?agent_id=agent1", `{"enabled":false}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if resp.Name != "solo" || resp.Enabled {
		t.Fatalf("name/enabled = %q/%v, want solo/false", resp.Name, resp.Enabled)
	}
	if !resp.Changed || resp.Created || resp.Removed {
		t.Fatalf("changed/created/removed = %v/%v/%v, want true/false/false",
			resp.Changed, resp.Created, resp.Removed)
	}
	if resp.Layer != "global" || resp.EffectiveLayer != "global" {
		t.Fatalf("layer/effective_layer = %q/%q, want global/global", resp.Layer, resp.EffectiveLayer)
	}
	if resp.Effective != "disabled" {
		t.Fatalf("effective = %q, want disabled", resp.Effective)
	}
	if resp.Path != filepath.Join(f.globalDir, "mcp.json") {
		t.Fatalf("path = %q, want the global layer file", resp.Path)
	}

	globalBytes := mcpReadFile(t, filepath.Join(f.globalDir, "mcp.json"))
	if !strings.Contains(string(globalBytes), `"disabled": true`) {
		t.Fatalf("global file lacks \"disabled\": true after auto disable: %s", globalBytes)
	}
	if got := reloads(); got != 1 {
		t.Fatalf("reload count = %d, want 1", got)
	}
}

// TestMCPToggle_InversionIsNotFlipped pins the API⇄file inversion: the body
// speaks "enabled", the file stores "disabled". disable→enable must leave the
// document byte-identical to the original — never a flipped flag, never a
// leftover "disabled" key (each successful write reloads exactly once).
func TestMCPToggle_InversionIsNotFlipped(t *testing.T) {
	original := `{"mcpServers": {"solo": {"command": "solo-cmd"}}}`
	f := newMCPSoloFixture(t, original)
	reloads := mcpCountReloads(t, f)
	globalFile := filepath.Join(f.globalDir, "mcp.json")
	url := f.ts.server.URL + "/api/v1/mcp/auto/servers/solo/toggle?agent_id=agent1"

	status, resp, body := mcpToggle(t, f, url, `{"enabled":false}`)
	if status != http.StatusOK {
		t.Fatalf("disable: status = %d, want 200; body=%s", status, body)
	}
	if resp.Effective != "disabled" || !resp.Changed {
		t.Fatalf("disable: effective/changed = %q/%v, want disabled/true", resp.Effective, resp.Changed)
	}
	if disabled := mcpReadFile(t, globalFile); !strings.Contains(string(disabled), `"disabled": true`) {
		t.Fatalf("disable: global file lacks \"disabled\": true: %s", disabled)
	}
	if got := reloads(); got != 1 {
		t.Fatalf("reload count = %d after disable, want 1", got)
	}

	status, resp, body = mcpToggle(t, f, url, `{"enabled":true}`)
	if status != http.StatusOK {
		t.Fatalf("enable: status = %d, want 200; body=%s", status, body)
	}
	if resp.Effective != "enabled" || !resp.Changed {
		t.Fatalf("enable: effective/changed = %q/%v, want enabled/true", resp.Effective, resp.Changed)
	}
	final := mcpReadFile(t, globalFile)
	if strings.Contains(string(final), `"disabled"`) {
		t.Fatalf("enable: \"disabled\" key must be gone (never \"disabled\": false): %s", final)
	}
	if string(final) != original {
		t.Fatalf("enable: final bytes = %s, want the original document %s", final, original)
	}
	if got := reloads(); got != 2 {
		t.Fatalf("reload count = %d after enable, want 2 (one per successful write)", got)
	}
}

// TestMCPToggle_StubDeletion: enabling a pure disable-stub in a higher layer
// DELETES the stub (not "{}") so the lower layer wins again; the lower
// layer's file stays byte-identical.
func TestMCPToggle_StubDeletion(t *testing.T) {
	f := newMCPFixture(t)
	reloads := mcpCountReloads(t, f)

	agentFile := filepath.Join(f.agentDir, "mcp.json")
	// The agent layer holds a pure disable-stub over the global definition —
	// exactly what a cross-layer disable creates.
	writeMCPJSON(t, agentFile, `{"mcpServers": {"only-global": {"disabled": true}}}`)
	globalFile := filepath.Join(f.globalDir, "mcp.json")
	globalBefore := mcpReadFile(t, globalFile)

	status, resp, body := mcpToggle(t, f,
		f.ts.server.URL+"/api/v1/mcp/auto/servers/only-global/toggle?agent_id=agent1", `{"enabled":true}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if !resp.Removed || !resp.Changed {
		t.Fatalf("removed/changed = %v/%v, want true/true", resp.Removed, resp.Changed)
	}
	if resp.Layer != "agent" {
		t.Fatalf("layer = %q, want agent (auto resolves to the stub's layer)", resp.Layer)
	}
	if resp.Effective != "enabled" || resp.EffectiveLayer != "global" {
		t.Fatalf("effective/effective_layer = %q/%q, want enabled/global",
			resp.Effective, resp.EffectiveLayer)
	}

	agentBytes := mcpReadFile(t, agentFile)
	if strings.Contains(string(agentBytes), "only-global") {
		t.Fatalf("agent file still mentions the stubbed name (must be deleted, not %q): %s",
			`"name": {}`, agentBytes)
	}
	globalAfter := mcpReadFile(t, globalFile)
	if !bytes.Equal(globalAfter, globalBefore) {
		t.Fatalf("global file changed:\nbefore: %s\nafter:  %s", globalBefore, globalAfter)
	}
	if got := reloads(); got != 1 {
		t.Fatalf("reload count = %d, want 1", got)
	}
}

// TestMCPToggle_CrossLayerAndGlobalStubRefused:
//
// (a) the name lives ONLY in global: writing the disable into the agent
//
//	layer is an allowed cross-layer create (200 created:true, stub in the
//	agent file, global bytes untouched);
//
// (b) the same call with layer=global on the post-(a) state: the global copy
//
//	is present but shadowed by the agent stub, so the shadow guard (step 7
//	— checked BEFORE the global-stub rule per the handler's ORDER) refuses
//	with 409 mcp_entry_shadowed and no reload;
//
// (c) the literal global-stub refusal: a name NOT held by the global layer
//
//	(project-only) can never be disabled "via global" — a global stub sits
//	at the lowest layer and cannot shadow anything — so the write is
//	refused with 400 mcp_stub_layer_not_allowed and nothing is written.
func TestMCPToggle_CrossLayerAndGlobalStubRefused(t *testing.T) {
	f := newMCPSoloFixture(t, `{"mcpServers": {"solo": {"command": "solo-cmd"}}}`)
	reloads := mcpCountReloads(t, f)
	globalFile := filepath.Join(f.globalDir, "mcp.json")
	agentFile := filepath.Join(f.agentDir, "mcp.json")
	globalBefore := mcpReadFile(t, globalFile)

	// (a) cross-layer create into agent.
	status, resp, body := mcpToggle(t, f,
		f.ts.server.URL+"/api/v1/mcp/agent/servers/solo/toggle?agent_id=agent1", `{"enabled":false}`)
	if status != http.StatusOK {
		t.Fatalf("(a) status = %d, want 200; body=%s", status, body)
	}
	if !resp.Created || !resp.Changed {
		t.Fatalf("(a) created/changed = %v/%v, want true/true", resp.Created, resp.Changed)
	}
	if resp.Layer != "agent" || resp.Effective != "disabled" || resp.EffectiveLayer != "agent" {
		t.Fatalf("(a) layer/effective/effective_layer = %q/%q/%q, want agent/disabled/agent",
			resp.Layer, resp.Effective, resp.EffectiveLayer)
	}
	agentBytes := mcpReadFile(t, agentFile)
	if !strings.Contains(string(agentBytes), "solo") ||
		!strings.Contains(string(agentBytes), `"disabled"`) {
		t.Fatalf("(a) agent file lacks the disable stub: %s", agentBytes)
	}
	if !bytes.Equal(mcpReadFile(t, globalFile), globalBefore) {
		t.Fatalf("(a) global file changed: %s", mcpReadFile(t, globalFile))
	}
	if got := reloads(); got != 1 {
		t.Fatalf("(a) reload count = %d, want 1", got)
	}

	// (b) same call with layer=global → shadow guard fires first.
	status, body = mcpPut(t, f.ts,
		f.ts.server.URL+"/api/v1/mcp/global/servers/solo/toggle?agent_id=agent1", `{"enabled":false}`)
	if status != http.StatusConflict || !strings.Contains(string(body), "mcp_entry_shadowed") {
		t.Fatalf("(b) status = %d body = %s, want 409 mcp_entry_shadowed", status, body)
	}
	if got := reloads(); got != 1 {
		t.Fatalf("(b) reload count = %d, want 1 (a refused write must not reload)", got)
	}

	// (c) disable-stub into global for a name global does not hold → 400.
	projectFile := filepath.Join(f.projectDir, ".lele", "mcp.json")
	writeMCPJSON(t, projectFile, `{"mcpServers": {"proj-only": {"command": "p"}}}`)
	projectBefore := mcpReadFile(t, projectFile)
	status, body = mcpPut(t, f.ts,
		f.ts.server.URL+"/api/v1/mcp/global/servers/proj-only/toggle?agent_id=agent1", `{"enabled":false}`)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "mcp_stub_layer_not_allowed") {
		t.Fatalf("(c) status = %d body = %s, want 400 mcp_stub_layer_not_allowed", status, body)
	}
	if !bytes.Equal(mcpReadFile(t, projectFile), projectBefore) ||
		!bytes.Equal(mcpReadFile(t, globalFile), globalBefore) {
		t.Fatal("(c) a refused global stub must not write any file")
	}
	if got := reloads(); got != 1 {
		t.Fatalf("(c) reload count = %d, want 1", got)
	}
}

// TestMCPToggle_ShadowGuardErrors pins the guard rails: a write into a
// shadowed layer needs force=true (409 carries the owner layer/path in
// details); unknown names, bogus layers and broken bodies are refused with
// their own codes; a layer root outside the allowed workspace roots is a 403
// with NOTHING written. Only the forced success reloads.
func TestMCPToggle_ShadowGuardErrors(t *testing.T) {
	f := newMCPFixture(t)
	reloads := mcpCountReloads(t, f)
	globalFile := filepath.Join(f.globalDir, "mcp.json")
	globalBefore := mcpReadFile(t, globalFile)

	// "shared" lives in global AND project; project wins. Writing the
	// shadowed global copy without force → 409 with the owner in details.
	status, body := mcpPut(t, f.ts,
		f.ts.server.URL+"/api/v1/mcp/global/servers/shared/toggle?agent_id=agent1", `{"enabled":true}`)
	if status != http.StatusConflict || !strings.Contains(string(body), "mcp_entry_shadowed") {
		t.Fatalf("status = %d body = %s, want 409 mcp_entry_shadowed", status, body)
	}
	var apiErr APIError
	if err := json.Unmarshal(body, &apiErr); err != nil {
		t.Fatalf("Decode() error = %v body=%s", err, body)
	}
	details, ok := apiErr.Details.(map[string]any)
	if !ok || details["owner_layer"] != "project" {
		t.Fatalf("details = %+v, want owner_layer project", apiErr.Details)
	}
	if details["owner_path"] != filepath.Join(f.projectDir, ".lele", "mcp.json") {
		t.Fatalf("owner_path = %v, want the project layer file", details["owner_path"])
	}
	if got := reloads(); got != 0 {
		t.Fatalf("reload count = %d after the refused write, want 0", got)
	}

	// Same write with force=true → 200 (the global copy has no "disabled"
	// key, so an enable is an idempotent no-op).
	status, resp, body := mcpToggle(t, f,
		f.ts.server.URL+"/api/v1/mcp/global/servers/shared/toggle?agent_id=agent1&force=true",
		`{"enabled":true}`)
	if status != http.StatusOK {
		t.Fatalf("force retry: status = %d, want 200; body=%s", status, body)
	}
	if resp.Effective != "enabled" || resp.EffectiveLayer != "project" {
		t.Fatalf("force retry: effective/effective_layer = %q/%q, want enabled/project",
			resp.Effective, resp.EffectiveLayer)
	}
	if got := reloads(); got != 1 {
		t.Fatalf("reload count = %d after the forced success, want 1", got)
	}

	// Unknown name → 404.
	status, body = mcpPut(t, f.ts,
		f.ts.server.URL+"/api/v1/mcp/global/servers/ghost/toggle?agent_id=agent1", `{"enabled":false}`)
	if status != http.StatusNotFound || !strings.Contains(string(body), "mcp_server_not_found") {
		t.Fatalf("unknown name: status = %d body = %s, want 404 mcp_server_not_found", status, body)
	}

	// Bogus layer → 400 invalid_layer (checked before anything else).
	status, body = mcpPut(t, f.ts,
		f.ts.server.URL+"/api/v1/mcp/bogus/servers/shared/toggle?agent_id=agent1", `{"enabled":false}`)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "invalid_layer") {
		t.Fatalf("bogus layer: status = %d body = %s, want 400 invalid_layer", status, body)
	}

	// Broken JSON → 400 body_invalid (decoded before touching disk).
	status, body = mcpPut(t, f.ts,
		f.ts.server.URL+"/api/v1/mcp/global/servers/shared/toggle?agent_id=agent1", `{"enabled":`)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "body_invalid") {
		t.Fatalf("bad json: status = %d body = %s, want 400 body_invalid", status, body)
	}

	// A layer root outside the allowed workspace roots → 403, nothing
	// written (the name is known via the global layer, the addressed agent
	// root points outside home/tmp/cwd).
	trap := "/not-allowed-root"
	f.loop.paths["trap"] = mcp.Paths{LeleDir: f.globalDir, AgentWorkspace: trap, Cwd: f.projectDir}
	status, body = mcpPut(t, f.ts,
		f.ts.server.URL+"/api/v1/mcp/agent/servers/only-global/toggle?agent_id=trap", `{"enabled":false}`)
	if status != http.StatusForbidden || !strings.Contains(string(body), "mcp_path_not_allowed") {
		t.Fatalf("trap path: status = %d body = %s, want 403 mcp_path_not_allowed", status, body)
	}
	if _, err := os.Stat(filepath.Join(trap, "mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("file created under %s (err=%v)", trap, err)
	}

	if got := reloads(); got != 1 {
		t.Fatalf("reload count = %d at the end, want 1 (only the forced success reloaded)", got)
	}

	// The forced enable was an idempotent no-op and every other write was
	// refused: the global file must be byte-identical to the start.
	globalAfter := mcpReadFile(t, globalFile)
	if !bytes.Equal(globalAfter, globalBefore) {
		t.Fatalf("global file changed:\nbefore: %s\nafter:  %s", globalBefore, globalAfter)
	}
}

// --- raw PUT (PUT .../raw) + validate (POST .../validate) ------------------

// mcpPost issues an authenticated POST with a JSON body (mcpPut's twin for
// the validate endpoint).
func mcpPost(t *testing.T, ts *nativeTestServer, url, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest(%s): %v", url, err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(%s): %v", url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(%s): %v", url, err)
	}
	return resp.StatusCode, data
}

// mcpValidate wraps mcpPost against /api/v1/mcp/validate and decodes the
// always-200 body into MCPValidateResponse.
func mcpValidate(t *testing.T, f *mcpFixture, body string) (int, MCPValidateResponse, []byte) {
	t.Helper()
	status, raw := mcpPost(t, f.ts,
		f.ts.server.URL+"/api/v1/mcp/validate?agent_id=agent1", body)
	var out MCPValidateResponse
	if status == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("Decode() error = %v body=%s", err, raw)
		}
	}
	return status, out, raw
}

// TestMCPRawPut_WritesVerbatimBytes: the save must NOT reformat — a valid
// but oddly formatted document (extra spaces, duplicate entry name, duplicate
// field) lands on disk BYTE-IDENTICAL to the payload, and the response
// content (re-read from disk) equals the payload. Reload fires exactly once.
func TestMCPRawPut_WritesVerbatimBytes(t *testing.T) {
	f := newMCPSoloFixture(t, `{"mcpServers": {"seed": {"command": "seed-cmd"}}}`)
	reloads := mcpCountReloads(t, f)
	globalFile := filepath.Join(f.globalDir, "mcp.json")

	payload := `{"mcpServers":{   "odd"  :  {"command":"run-1"} ,   "odd" : {"command":"run-1-final", "command":"run-1-final"}   }}`
	status, raw := mcpPut(t, f.ts,
		f.ts.server.URL+"/api/v1/mcp/global/raw?agent_id=agent1",
		`{"content":`+strconv.Quote(payload)+`}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, raw)
	}
	var resp MCPRawFileResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("Decode() error = %v body=%s", err, raw)
	}
	if !resp.Exists || resp.Content != payload {
		t.Fatalf("exists/content = %v/%q, want true/payload", resp.Exists, resp.Content)
	}
	if onDisk := mcpReadFile(t, globalFile); string(onDisk) != payload {
		t.Fatalf("on-disk bytes differ from payload:\non disk: %s\npayload: %s", onDisk, payload)
	}
	if reloads() != 1 {
		t.Fatalf("reload count = %d, want 1", reloads())
	}
}

// TestMCPRawPut_MalformedIs422AndLeavesFileUntouched: an envelope failure
// ({...: 5} — mcpServers not an object) is 422 config_invalid BEFORE the
// write: the pre-existing file keeps its exact bytes and nothing reloads.
func TestMCPRawPut_MalformedIs422AndLeavesFileUntouched(t *testing.T) {
	valid := `{"mcpServers": {"seed": {"command": "seed-cmd"}}}`
	f := newMCPSoloFixture(t, valid)
	reloads := mcpCountReloads(t, f)
	globalFile := filepath.Join(f.globalDir, "mcp.json")
	before := mcpReadFile(t, globalFile)

	status, raw := mcpPut(t, f.ts,
		f.ts.server.URL+"/api/v1/mcp/global/raw?agent_id=agent1",
		`{"content":"{\"mcpServers\": 5}"}`)
	if status != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "config_invalid") {
		t.Fatalf("status = %d, want 422 config_invalid; body=%s", status, raw)
	}
	if after := mcpReadFile(t, globalFile); string(after) != string(before) {
		t.Fatalf("file must be untouched:\nbefore: %s\nafter:  %s", before, after)
	}
	if reloads() != 0 {
		t.Fatalf("reload count = %d, want 0", reloads())
	}
}

// TestMCPRawPut_InvalidEntryIsAcceptedWithWarnings: the escape hatch — an
// entry lacking BOTH command and url is a warning, not a hard error, so the
// editor can save a file it is trying to FIX. 200, warnings non-empty,
// on-disk bytes equal the payload, reload 1.
func TestMCPRawPut_InvalidEntryIsAcceptedWithWarnings(t *testing.T) {
	f := newMCPSoloFixture(t, `{"mcpServers": {"seed": {"command": "seed-cmd"}}}`)
	reloads := mcpCountReloads(t, f)
	globalFile := filepath.Join(f.globalDir, "mcp.json")

	payload := `{"mcpServers": {"orphan": {}}}`
	status, raw := mcpPut(t, f.ts,
		f.ts.server.URL+"/api/v1/mcp/global/raw?agent_id=agent1",
		`{"content":`+strconv.Quote(payload)+`}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, raw)
	}
	var resp MCPRawFileResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("Decode() error = %v body=%s", err, raw)
	}
	if len(resp.Warnings) == 0 {
		t.Fatalf("warnings empty, want non-empty; body=%s", raw)
	}
	if onDisk := mcpReadFile(t, globalFile); string(onDisk) != payload {
		t.Fatalf("on-disk bytes differ from payload:\non disk: %s\npayload: %s", onDisk, payload)
	}
	if reloads() != 1 {
		t.Fatalf("reload count = %d, want 1", reloads())
	}
}

// TestMCPRawPut_GuardsAndLimits: every guard refuses BEFORE any write and
// reload stays 0 throughout — auto layer (400 invalid_layer), a Paths root
// outside the allowed workspace roots (403 mcp_path_not_allowed, NO file
// created), a body > 1 MiB (400 body_invalid: applyBodyLimit's
// MaxBytesReader trips inside the decode — verified by running the test),
// an empty root (400 mcp_layer_unavailable), a non-JSON body (400
// body_invalid). THEN the reachable boundary: a `content` just under the
// 1 MiB body cap is accepted (200) and written byte-identically (reload 1).
func TestMCPRawPut_GuardsAndLimits(t *testing.T) {
	seed := `{"mcpServers": {"seed": {"command": "seed-cmd"}}}`
	f := newMCPSoloFixture(t, seed)
	reloads := mcpCountReloads(t, f)
	globalFile := filepath.Join(f.globalDir, "mcp.json")
	base := f.ts.server.URL + "/api/v1/mcp"

	// 1. auto is refused: the raw PUT writes a PHYSICAL file.
	status, raw := mcpPut(t, f.ts, base+"/auto/raw?agent_id=agent1", `{"content":"{}"}`)
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "invalid_layer") {
		t.Fatalf("auto: status = %d, want 400 invalid_layer; body=%s", status, raw)
	}

	// 2. a root outside the allowed workspace roots → 403, NOTHING created.
	//    /etc is under none of home, /tmp, /var/folders or the cwd.
	f.loop.paths["agent-outside"] = mcp.Paths{
		LeleDir: "/etc/lele-mcp-rawput-test", AgentWorkspace: "", Cwd: "",
	}
	status, raw = mcpPut(t, f.ts, base+"/global/raw?agent_id=agent-outside",
		`{"content":"{\"mcpServers\":{}}"}`)
	if status != http.StatusForbidden || !strings.Contains(string(raw), "mcp_path_not_allowed") {
		t.Fatalf("outside root: status = %d, want 403 mcp_path_not_allowed; body=%s", status, raw)
	}
	if _, err := os.Stat("/etc/lele-mcp-rawput-test/mcp.json"); !os.IsNotExist(err) {
		t.Fatalf("outside root: file must not exist, stat err = %v", err)
	}

	// 3. a body > 1 MiB → the route's applyBodyLimit (http.MaxBytesReader,
	//    1 MiB) trips inside the decode → 400 body_invalid, message
	//    "http: request body too large". Verified by running the test.
	//    (The handler's own 413 mcp_file_too_large is NOT reachable
	//    through this route: any body carrying content > mcpRawFileMaxBytes
	//    is itself > 1 MiB and dies here first.)
	huge := strings.Repeat("a", mcpRawFileMaxBytes+1)
	status, raw = mcpPut(t, f.ts, base+"/global/raw?agent_id=agent1",
		`{"content":"`+huge+`"}`)
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "body_invalid") {
		t.Fatalf("oversize: status = %d, want 400 body_invalid; body=%s", status, raw)
	}

	// 4. an empty layer root resolves to "" → 400 mcp_layer_unavailable.
	f.loop.paths["agent-empty"] = mcp.Paths{
		LeleDir: f.globalDir, AgentWorkspace: "", Cwd: f.projectDir,
	}
	status, raw = mcpPut(t, f.ts, base+"/agent/raw?agent_id=agent-empty", `{"content":"{}"}`)
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "mcp_layer_unavailable") {
		t.Fatalf("empty root: status = %d, want 400 mcp_layer_unavailable; body=%s", status, raw)
	}

	// 5. a non-JSON body → 400 body_invalid.
	status, raw = mcpPut(t, f.ts, base+"/global/raw?agent_id=agent1", `{not json`)
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "body_invalid") {
		t.Fatalf("bad body: status = %d, want 400 body_invalid; body=%s", status, raw)
	}

	// None of the guard paths wrote or reloaded; the seed file is intact.
	if onDisk := mcpReadFile(t, globalFile); string(onDisk) != seed {
		t.Fatalf("seed file mutated: %s", onDisk)
	}
	if reloads() != 0 {
		t.Fatalf("reload count = %d, want 0", reloads())
	}

	// 6. field-level boundary (the case that IS reachable): the largest
	//    `content` that still fits under the 1 MiB body cap is accepted
	//    (200) and written to disk byte-identically. The envelope is
	//    {"content":<json-string>} = 12 wrapper bytes + the quoted string,
	//    so pad the content until the whole body sits strictly below
	//    mcpRawFileMaxBytes.
	makeContent := func(pad int) string {
		return `{"mcpServers":{"big":{"command":"x","env":{"PAD":"` +
			strings.Repeat("x", pad) + `"}}}}`
	}
	quoted0, err := json.Marshal(makeContent(0))
	if err != nil {
		t.Fatalf("Marshal(content): %v", err)
	}
	pad := mcpRawFileMaxBytes - 12 - len(quoted0) - 1 // 1 byte of headroom
	if pad < 0 {
		t.Fatalf("pad = %d: fixture already exceeds the body cap", pad)
	}
	bigContent := makeContent(pad)
	quoted, err := json.Marshal(bigContent)
	if err != nil {
		t.Fatalf("Marshal(content): %v", err)
	}
	envelope := `{"content":` + string(quoted) + `}`
	if len(envelope) >= mcpRawFileMaxBytes {
		t.Fatalf("envelope = %d bytes, want < %d", len(envelope), mcpRawFileMaxBytes)
	}
	status, raw = mcpPut(t, f.ts, base+"/global/raw?agent_id=agent1", envelope)
	if status != http.StatusOK {
		t.Fatalf("boundary: status = %d, want 200; body=%s", status, raw)
	}
	if onDisk := mcpReadFile(t, globalFile); string(onDisk) != bigContent {
		t.Fatalf("boundary: on-disk bytes differ from content (disk=%d bytes, want %d)",
			len(onDisk), len(bigContent))
	}
	if reloads() != 1 {
		t.Fatalf("boundary: reload count = %d, want 1", reloads())
	}
}

// TestMCPValidate_ValidAndFatal: validate is a dry run — valid doc ⇒ 200
// valid:true with no error; envelope garbage ⇒ 200 valid:false + error text.
// In BOTH cases the addressed file's bytes are unchanged and reload is 0.
// (The fatal case addresses layer auto, proving auto is accepted here.)
func TestMCPValidate_ValidAndFatal(t *testing.T) {
	seed := `{"mcpServers": {"seed": {"command": "seed-cmd"}}}`
	f := newMCPSoloFixture(t, seed)
	reloads := mcpCountReloads(t, f)
	globalFile := filepath.Join(f.globalDir, "mcp.json")

	status, resp, raw := mcpValidate(t, f,
		`{"layer":"global","content":"{\"mcpServers\":{\"ok\":{\"command\":\"c\"}}}"}`)
	if status != http.StatusOK || !resp.Valid || resp.Error != "" {
		t.Fatalf("valid doc: status = %d valid = %v error = %q; body=%s",
			status, resp.Valid, resp.Error, raw)
	}

	status, resp, raw = mcpValidate(t, f,
		`{"layer":"auto","content":"{\"mcpServers\": 5}"}`)
	if status != http.StatusOK || resp.Valid {
		t.Fatalf("garbage: status = %d valid = %v, want 200/false; body=%s", status, resp.Valid, raw)
	}
	if resp.Error == "" {
		t.Fatalf("garbage: error empty, want the fatal message; body=%s", raw)
	}

	if onDisk := mcpReadFile(t, globalFile); string(onDisk) != seed {
		t.Fatalf("file must be untouched: %s", onDisk)
	}
	if reloads() != 0 {
		t.Fatalf("reload count = %d, want 0", reloads())
	}
}

// TestMCPValidate_WarningsAreNotErrors: a doc with one invalid entry (no
// command, no url) is still valid:true with non-empty warnings — per-entry
// problems NEVER fail the validate request. No write, no reload.
func TestMCPValidate_WarningsAreNotErrors(t *testing.T) {
	seed := `{"mcpServers": {"seed": {"command": "seed-cmd"}}}`
	f := newMCPSoloFixture(t, seed)
	reloads := mcpCountReloads(t, f)
	globalFile := filepath.Join(f.globalDir, "mcp.json")

	status, resp, raw := mcpValidate(t, f,
		`{"layer":"project","content":"{\"mcpServers\":{\"orphan\":{}}}"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, raw)
	}
	if !resp.Valid {
		t.Fatalf("valid = false, want true (per-entry problems are warnings); body=%s", raw)
	}
	if len(resp.Warnings) == 0 {
		t.Fatalf("warnings empty, want non-empty; body=%s", raw)
	}
	if onDisk := mcpReadFile(t, globalFile); string(onDisk) != seed {
		t.Fatalf("file must be untouched: %s", onDisk)
	}
	if reloads() != 0 {
		t.Fatalf("reload count = %d, want 0", reloads())
	}
}

// TestMCPValidate_BodyLimit: POST .../validate is registered behind
// applyBodyLimit (http.MaxBytesReader, 1 MiB — the same cap as the raw
// PUT), so a body > 1 MiB trips INSIDE the decode: 400 body_invalid with
// "request body too large" (never 413 — the handler's own checks are
// unreachable past the reader) and nothing reaches disk: the addressed
// file keeps its exact bytes and reload stays 0.
func TestMCPValidate_BodyLimit(t *testing.T) {
	seed := `{"mcpServers": {"seed": {"command": "seed-cmd"}}}`
	f := newMCPSoloFixture(t, seed)
	reloads := mcpCountReloads(t, f)
	globalFile := filepath.Join(f.globalDir, "mcp.json")

	huge := strings.Repeat("a", mcpRawFileMaxBytes+1)
	status, _, raw := mcpValidate(t, f,
		`{"layer":"global","content":"`+huge+`"}`)
	if status != http.StatusBadRequest ||
		!strings.Contains(string(raw), "body_invalid") ||
		!strings.Contains(string(raw), "request body too large") {
		t.Fatalf("oversize: status = %d, want 400 body_invalid (request body too large); body=%s",
			status, raw)
	}
	if onDisk := mcpReadFile(t, globalFile); string(onDisk) != seed {
		t.Fatalf("file must be untouched: %s", onDisk)
	}
	if reloads() != 0 {
		t.Fatalf("reload count = %d, want 0", reloads())
	}
}
