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
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
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

// --- inventory: auth report --------------------------------------------------

// mcpAuthFixtureRaw is the auth matrix every report test starts from, written
// in the ONE global layer (= LeleDir, so <LeleDir>/mcp-auth.json shares the
// root): a remote entry opted into OAuth, a remote entry carrying header
// credentials only (with a ${VAR} value, which must stay unexpanded AND
// unread) and a plain stdio entry with no auth story at all.
const mcpAuthFixtureRaw = `{
  "mcpServers": {
    "oauth-srv": {"url": "https://oauth.example/mcp", "auth": {"type": "oauth"}, "description": "oauth"},
    "headers-srv": {"url": "https://headers.example/mcp", "headers": {"Authorization": "Bearer ${HEADER_CANARY}"}},
    "stdio-srv": {"command": "run-stdio"}
  }
}`

// newMCPAuthFixture wires one agent ("auth1") over three temp roots whose
// global layer holds mcpAuthFixtureRaw. LeleDir is that global root, so the
// auth store file (<LeleDir>/mcp-auth.json) lives next to the layer file the
// writer and the handler both read.
func newMCPAuthFixture(t *testing.T) *mcpFixture {
	t.Helper()
	return newMCPAuthFixtureWith(t, mcpAuthFixtureRaw)
}

// newMCPAuthFixtureWith is newMCPAuthFixture for a caller with its own global
// mcp.json (same layout, same single agent id, same LeleDir = global root rule).
func newMCPAuthFixtureWith(t *testing.T, raw string) *mcpFixture {
	t.Helper()

	globalDir := t.TempDir()
	writeMCPJSON(t, filepath.Join(globalDir, "mcp.json"), raw)

	paths := mcp.Paths{LeleDir: globalDir, AgentWorkspace: t.TempDir(), Cwd: t.TempDir()}
	ts := newNativeTestServer(t)
	loop := &mcpTestLoop{
		nativeTestAgentLoop: ts.loop,
		paths:               map[string]mcp.Paths{"auth1": paths},
	}
	ts.channel.agentLoop = loop

	return &mcpFixture{
		ts:        ts,
		loop:      loop,
		globalDir: globalDir,
		paths:     paths,
		globalRaw: raw,
	}
}

// mcpAuthStoreOf opens the SAME store file the handler opens, from a writer
// that is not the handler (a different AuthStore instance — the cross-process
// case the per-request store exists for).
func mcpAuthStoreOf(t *testing.T, f *mcpFixture) *mcp.AuthStore {
	t.Helper()
	return mcp.NewAuthStore(f.paths.LeleDir)
}

// TestMCPInventory_AuthOAuthServer pins the opted-in row: an enabled remote
// entry with "auth": {"type": "oauth"} and no stored record reports
// required/method oauth and NOT authenticated.
func TestMCPInventory_AuthOAuthServer(t *testing.T) {
	f := newMCPAuthFixture(t)

	status, inv, body := mcpInventory(t, f, "auth1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}

	row := mcpRow(t, inv, "oauth-srv")
	if row.Effective != "enabled" {
		t.Fatalf("oauth-srv effective = %q, want enabled", row.Effective)
	}
	auth := row.Server.Auth
	if auth == nil {
		t.Fatalf("oauth-srv has no auth report: %+v", row)
	}
	if !auth.Required || auth.Authenticated || auth.Method != "oauth" {
		t.Fatalf("oauth-srv auth = %+v, want required=true authenticated=false method=oauth", auth)
	}

	// Wire shape: the auth member always carries all three keys — `method` is
	// not omitempty, so it is "" rather than absent when there is no method.
	var wire struct {
		Required      *bool   `json:"required"`
		Authenticated *bool   `json:"authenticated"`
		Method        *string `json:"method"`
	}
	raw := mcpRawJSON(t, body, "oauth-srv", "auth")
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("auth member is not an object: %v (%s)", err, raw)
	}
	if wire.Required == nil || wire.Authenticated == nil || wire.Method == nil {
		t.Fatalf("auth member is missing one of required/authenticated/method: %s", raw)
	}
	if *wire.Method != "oauth" || !*wire.Required || *wire.Authenticated {
		t.Fatalf("auth wire = %s, want required=true authenticated=false method=oauth", raw)
	}
}

// mcpRawJSON extracts servers[i].server[field] verbatim so a test can assert
// the wire shape, not the decoded struct.
func mcpRawJSON(t *testing.T, body []byte, serverName, field string) json.RawMessage {
	t.Helper()
	var doc struct {
		Servers []struct {
			Name   string                     `json:"name"`
			Server map[string]json.RawMessage `json:"server"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal inventory body: %v\nbody=%s", err, body)
	}
	for _, row := range doc.Servers {
		if row.Name == serverName {
			return row.Server[field]
		}
	}
	t.Fatalf("server %q missing from body=%s", serverName, body)
	return nil
}

// TestMCPInventory_AuthAbsentForStdio pins the omitempty rule: a stdio entry
// (no url, no headers, no stored state) must not carry an "auth" member at
// all — the response struct and the raw JSON agree on that.
func TestMCPInventory_AuthAbsentForStdio(t *testing.T) {
	f := newMCPAuthFixture(t)

	status, _, body := mcpInventory(t, f, "auth1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}

	if raw := mcpRawJSON(t, body, "stdio-srv", "auth"); raw != nil {
		t.Fatalf("stdio-srv carries an auth member: %s", raw)
	}
	// Non-vacuous: the OAuth row DOES carry one in the same body.
	if raw := mcpRawJSON(t, body, "oauth-srv", "auth"); raw == nil {
		t.Fatalf("oauth-srv lost its auth member (the absence check above proves nothing): %s", body)
	}
}

// TestMCPInventory_AuthHeadersMethod pins the header-carrying row. The row is
// described by mcp.CheckAuth, whose rule this test borrows instead of guessing:
// header keys make the CARRIER "headers", and `required` follows the RECORDED
// challenge — headers are frequently non-credential (Accept, User-Agent) and
// their values are never read, so presence alone proves nothing and
// `authenticated` stays false either way.
//
// Both halves are asserted, in the order the client experiences them: a fresh
// entry with no challenge reports method=headers required=false (the engine's
// own case "headers only, no challenge", pkg/mcp/auth_test.go), and the same
// row flips to required=true once a 401 has been recorded — without which the
// first assertion would also pass on an implementation that hardcoded false.
func TestMCPInventory_AuthHeadersMethod(t *testing.T) {
	f := newMCPAuthFixture(t)

	status, inv, body := mcpInventory(t, f, "auth1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}

	auth := mcpRow(t, inv, "headers-srv").Server.Auth
	if auth == nil {
		t.Fatalf("headers-srv has no auth report: %+v", mcpRow(t, inv, "headers-srv"))
	}
	if auth.Method != "headers" || auth.Required || auth.Authenticated {
		t.Fatalf("headers-srv auth = %+v, want method=headers required=false authenticated=false", auth)
	}

	// A real 401 recorded by another process (the TUI, a probe) is what makes
	// the same row required.
	if err := mcpAuthStoreOf(t, f).Save("headers-srv", mcp.AuthRecord{
		URL:          "https://headers.example/mcp",
		AuthRequired: true,
	}); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	status, inv, body = mcpInventory(t, f, "auth1")
	if status != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", status, body)
	}
	auth = mcpRow(t, inv, "headers-srv").Server.Auth
	if auth == nil {
		t.Fatalf("headers-srv lost its auth report after the challenge: %s", body)
	}
	if auth.Method != "headers" || !auth.Required || auth.Authenticated {
		t.Fatalf("headers-srv after the recorded 401 = %+v, want method=headers required=true authenticated=false", auth)
	}
}

// mcpAuthUnresolvedRaw is the fixture of the branch-agreement test: every
// declared shape exists TWICE, once enabled (the copy mcp.Discover RESOLVES)
// and once disabled or invalid (a copy the merge excludes, so the auth report
// used to take the store-only fallback). Same shape, one variable.
//
// Two url shapes are covered on purpose: a literal one, and a ${VAR}-shaped one
// — the auth store binds a record to the EXPANDED url, so the fallback could
// only see a stored token or a recorded 401 by expanding what it looks up.
const mcpAuthUnresolvedRaw = `{
  "mcpServers": {
    "plain-headers":   {"url": "https://plain.example/mcp", "headers": {"Authorization": "Bearer ${HEADER_CANARY}"}},
    "plain-headers-off": {"url": "https://plain.example/mcp", "headers": {"Authorization": "Bearer ${HEADER_CANARY}"}, "disabled": true},
    "var-bare":        {"url": "${UNRESOLVED_URL}"},
    "var-bare-off":    {"url": "${UNRESOLVED_URL}", "disabled": true},
    "var-oauth":       {"url": "${UNRESOLVED_URL}", "auth": {"type": "oauth"}},
    "var-oauth-off":   {"url": "${UNRESOLVED_URL}", "auth": {"type": "oauth"}, "disabled": true},
    "var-invalid":     {"url": "${UNRESOLVED_URL}", "command": "run"}
  }
}`

// TestMCPInventory_AuthUnresolvedRowMatchesResolved is the branch-agreement
// guard: a row whose winner the merge did NOT resolve (disabled entry, invalid
// entry) must report the SAME auth block as the resolved row it is a copy of —
// the report is mcp.CheckAuth's answer in both cases, never a second rule.
//
// Three properties are pinned, each of which the store-only fallback failed:
//
//  1. the method precedence does not depend on resolution: a declared OAuth
//     block is "oauth", a header carrier is "headers" (and WINS over a bare
//     recorded 401, which names no carrier and stays ""), in both branches;
//  2. the store lookup uses the ${VAR}-EXPANDED url — a record written for the
//     expanded resource must be seen by the unresolved copy too;
//  3. Required is driven by the DECLARATION or the RECORDED challenge, never by
//     the presence of header keys (the rule of AuthStatus.Required).
//
// Every pair also has its expected block spelled out, so the equality can never
// be satisfied by two nils.
func TestMCPInventory_AuthUnresolvedRowMatchesResolved(t *testing.T) {
	t.Setenv("UNRESOLVED_URL", "https://unresolved.example/mcp")
	t.Setenv("HEADER_CANARY", "HEADER-VALUE-CANARY")

	f := newMCPAuthFixtureWith(t, mcpAuthUnresolvedRaw)

	// The 401 of each pair is recorded for the url the row RESOLVES to — the
	// expanded one for the ${VAR} rows, since that is the url a token would be
	// bound to and the only one the store can match. Writing the raw
	// "${UNRESOLVED_URL}" text would be the bug this test is about.
	writer := mcpAuthStoreOf(t, f)
	for name, url := range map[string]string{
		"plain-headers":     "https://plain.example/mcp",
		"plain-headers-off": "https://plain.example/mcp",
		"var-bare":          "https://unresolved.example/mcp",
		"var-bare-off":      "https://unresolved.example/mcp",
		"var-oauth":         "https://unresolved.example/mcp",
		"var-oauth-off":     "https://unresolved.example/mcp",
		"var-invalid":       "https://unresolved.example/mcp",
	} {
		if err := writer.Save(name, mcp.AuthRecord{URL: url, AuthRequired: true}); err != nil {
			t.Fatalf("Save(%q): %v", name, err)
		}
	}

	status, inv, body := mcpInventory(t, f, "auth1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}

	// (resolved copy, unresolved copy) pairs, each with the block BOTH must
	// report. The unresolved half is what the fallback used to get wrong.
	pairs := []struct {
		resolved, unresolved, why string
		want                      MCPServerAuth
	}{
		{
			resolved: "plain-headers", unresolved: "plain-headers-off",
			why:  "a header carrier wins over the recorded 401 (the fallback said oauth)",
			want: MCPServerAuth{Required: true, Authenticated: false, Method: "headers"},
		},
		{
			resolved: "var-bare", unresolved: "var-bare-off",
			why:  "a bare recorded 401 names no carrier: method stays empty (the fallback said oauth)",
			want: MCPServerAuth{Required: true, Authenticated: false, Method: ""},
		},
		{
			resolved: "var-oauth", unresolved: "var-oauth-off",
			why:  "a declared OAuth block is required with or without a resolution, and without a record",
			want: MCPServerAuth{Required: true, Authenticated: false, Method: "oauth"},
		},
		{
			resolved: "var-bare", unresolved: "var-invalid",
			why:  "an INVALID winner takes the same branch as a disabled one",
			want: MCPServerAuth{Required: true, Authenticated: false, Method: ""},
		},
	}
	for _, tc := range pairs {
		want := mcpRow(t, inv, tc.resolved).Server.Auth
		if want == nil || *want != tc.want {
			t.Fatalf("%s auth = %+v, want %+v (the resolved side of the pair)", tc.resolved, want, tc.want)
		}
		got := mcpRow(t, inv, tc.unresolved).Server.Auth
		if got == nil {
			t.Fatalf("%s has no auth report; %s — want %+v, the same block as %s",
				tc.unresolved, tc.why, tc.want, tc.resolved)
		}
		if *got != tc.want {
			t.Fatalf("%s auth = %+v, want %+v (%s)", tc.unresolved, got, tc.want, tc.why)
		}
		if *got != *want {
			t.Fatalf("%s auth = %+v, want the resolved copy's block %+v", tc.unresolved, got, want)
		}
	}

	// Non-vacuity of the ${VAR} half: the pair really is unresolved (so it went
	// through the fallback at all) and really carries the raw url.
	if eff := mcpRow(t, inv, "var-bare-off").Effective; eff != "disabled" {
		t.Fatalf("var-bare-off effective = %q, want disabled (the pair would not exercise the fallback)", eff)
	}
	if eff := mcpRow(t, inv, "var-invalid").Effective; eff != "invalid" {
		t.Fatalf("var-invalid effective = %q, want invalid", eff)
	}
	// The url and the header VALUE stay literal/absent: expanding them is an
	// internal step of the lookup, never something the API echoes.
	if !strings.Contains(string(body), "${UNRESOLVED_URL}") {
		t.Fatalf("the raw ${VAR} url disappeared from the body: %s", body)
	}
	for _, secret := range []string{"https://unresolved.example/mcp", "HEADER-VALUE-CANARY"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("the expanded value %q leaked into the inventory body: %s", secret, body)
		}
	}
}

// TestMCPInventory_AuthSeesTokenStoredElsewhere is the freshness guarantee:
// the handler builds its store PER REQUEST, and the store re-validates
// mcp-auth.json on every read, so a token written by another process (here a
// different AuthStore instance pointed at the same lele dir) is visible to the
// very next request without a restart.
func TestMCPInventory_AuthSeesTokenStoredElsewhere(t *testing.T) {
	f := newMCPAuthFixture(t)

	status, inv, body := mcpInventory(t, f, "auth1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if auth := mcpRow(t, inv, "oauth-srv").Server.Auth; auth == nil || auth.Authenticated {
		t.Fatalf("oauth-srv before the write = %+v, want authenticated=false", auth)
	}

	// A DIFFERENT store instance writes, exactly like the TUI process would.
	writer := mcpAuthStoreOf(t, f)
	if err := writer.Save("oauth-srv", mcp.AuthRecord{
		URL:          "https://oauth.example/mcp",
		AuthRequired: true,
		Token: &transport.Token{
			AccessToken:  "access-token-value",
			RefreshToken: "refresh-token-value",
			TokenType:    "Bearer",
			ExpiresAt:    time.Now().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	status, inv, body = mcpInventory(t, f, "auth1")
	if status != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", status, body)
	}
	auth := mcpRow(t, inv, "oauth-srv").Server.Auth
	if auth == nil {
		t.Fatalf("oauth-srv lost its auth report after the write: %s", body)
	}
	if !auth.Authenticated || !auth.Required || auth.Method != "oauth" {
		t.Fatalf("oauth-srv after the write = %+v, want required=true authenticated=true method=oauth", auth)
	}
	// The token VALUE must still be nowhere in the body.
	for _, secret := range []string{"access-token-value", "refresh-token-value"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("token value %q leaked into the inventory body: %s", secret, body)
		}
	}
}

// TestMCPInventory_AuthNeverLeaksSecrets is the canary of this feature: a
// record whose every value is canary-shaped (client_id, client_secret, token,
// redirect_uri) plus a header VALUE expanded from ${VAR} must never appear in
// the encoded body — the report is booleans and a fixed method string only.
func TestMCPInventory_AuthNeverLeaksSecrets(t *testing.T) {
	f := newMCPAuthFixture(t)
	t.Setenv("HEADER_CANARY", "HEADER-VALUE-CANARY")

	const canary = "SUPER-SECRET-CANARY"
	writer := mcpAuthStoreOf(t, f)
	if err := writer.Save("oauth-srv", mcp.AuthRecord{
		URL:          "https://oauth.example/mcp",
		ClientID:     canary + "-client-id",
		ClientSecret: canary + "-client-secret",
		RedirectURI:  "http://127.0.0.1:9/callback",
		AuthRequired: true,
		Token: &transport.Token{
			AccessToken:  canary + "-access",
			RefreshToken: canary + "-refresh",
			TokenType:    "Bearer",
			ExpiresAt:    time.Now().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	status, inv, body := mcpInventory(t, f, "auth1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	// Non-vacuous: the record WAS read (the row reports authenticated), so the
	// assertions below are about a store that really produced an answer.
	if auth := mcpRow(t, inv, "oauth-srv").Server.Auth; auth == nil || !auth.Authenticated {
		t.Fatalf("oauth-srv = %+v, want authenticated=true (the store was not read)", auth)
	}
	if strings.Contains(string(body), canary) {
		t.Fatalf("canary %q leaked into the inventory body: %s", canary, body)
	}
	if strings.Contains(string(body), "HEADER-VALUE-CANARY") {
		t.Fatalf("expanded header value leaked into the inventory body: %s", body)
	}
	// The key NAMES stay visible (the UI shows which headers a server sends).
	if !strings.Contains(string(body), "Authorization") {
		t.Fatalf("expected header key Authorization to stay visible: %s", body)
	}
}

// TestMCPAuthRoute_NotShadowedByRawFileHandler is the route-collision guard
// for GET /api/v1/mcp/auth/{flow_id} (contract route 3): the path must never be
// served by the raw-file READ, where "auth" would be read as a layer name.
//
// Once this task registered the flow routes, the naive patterns overlapped:
// "GET /api/v1/mcp/{layer}/raw" and "GET /api/v1/mcp/auth/{flow_id}" both match
// GET /api/v1/mcp/auth/raw and neither is more specific, so ServeMux PANICS at
// registration time ("conflicts with pattern"). The raw read is therefore
// registered as "GET /api/v1/mcp/{layer}/{doc}" behind handleMCPRawDoc (which
// serves only doc="raw"), and this test is the guard on BOTH halves of that
// decision: the raw route still works, and the two overlapping paths answer
// with the auth handler.
//
// Non-vacuity: the raw handler IS reachable for a real layer in the same
// function, and both auth answers are asserted by the auth layer's own error
// codes (never by "it did not 200").
func TestMCPAuthRoute_NotShadowedByRawFileHandler(t *testing.T) {
	f := newMCPFixture(t)
	// A WELL-FORMED flow id (32 hex chars): the shape check passes, so the
	// answer proves the lookup path, not the validator.
	const unknownFlowID = "abababababababababababababababab"

	// Baseline: the raw read still works through the {doc} shim.
	status, body := mcpGet(t, f.ts, f.ts.server.URL+"/api/v1/mcp/global/raw?agent_id=agent1", true)
	if status != http.StatusOK || !strings.Contains(string(body), `"content"`) {
		t.Fatalf("GET .../global/raw: status = %d body = %s, want the raw file body (the shim must not disable the route)", status, body)
	}

	// The literal-segment overlap: "/mcp/auth/raw" matches both patterns; the
	// literal "auth" must win, so the answer is the auth route refusing a bad
	// flow id shape (400 invalid_request) — never invalid_layer (the raw handler
	// reading "auth" as a layer) and never a file body.
	status, body = mcpGet(t, f.ts, f.ts.server.URL+"/api/v1/mcp/auth/raw?agent_id=agent1", true)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "invalid_request") {
		t.Fatalf("GET /api/v1/mcp/auth/raw: status = %d body = %s, want 400 invalid_request from the auth status route", status, body)
	}
	if strings.Contains(string(body), "invalid_layer") || strings.Contains(string(body), `"content"`) {
		t.Fatalf("the raw-file handler answered the auth path: %s", body)
	}

	// A well-formed but unknown flow id: still the auth route (404
	// mcp_auth_flow_not_found), which is the response the WebUI's poll sees.
	status, body = mcpGet(t, f.ts, f.ts.server.URL+"/api/v1/mcp/auth/"+unknownFlowID+"?agent_id=agent1", true)
	if status != http.StatusNotFound || !strings.Contains(string(body), "mcp_auth_flow_not_found") {
		t.Fatalf("GET /api/v1/mcp/auth/%s: status = %d body = %s, want 404 mcp_auth_flow_not_found", unknownFlowID, status, body)
	}
	if strings.Contains(string(body), "invalid_layer") || strings.Contains(string(body), `"content"`) {
		t.Fatalf("the raw-file handler answered the auth path: %s", body)
	}
}

// TestMCPRawDoc_RejectsAnyDocOtherThanRaw is the GATE test for the {doc} shim
// (finding 2 of the adversarial review). Widening the raw-file READ from the
// literal "GET /api/v1/mcp/{layer}/raw" to "GET /api/v1/mcp/{layer}/{doc}" (see
// handleMCPRawDoc in rest_mcp.go) means the ONLY thing standing between that
// wildcard and handleMCPRawFile — which serves a layer's mcp.json BYTES, header
// and env values included, to any authenticated caller — is the
// `PathValue("doc") != "raw"` check. Deleting that check leaves the rest of the
// MCP suite green AND turns GET /api/v1/mcp/global/<anything> into a 200 file
// read, so this test observes the observable consequence.
//
// Contract routes pinned: the raw-file READ (route `GET /api/v1/mcp/{layer}/raw`)
// must keep answering for doc == "raw", and every other doc value must keep
// answering the 404 the ServeMux would produce for an unregistered path.
//
// Non-vacuity: the REAL doc segment is served in the same function, so a 404
// below cannot be the shim having disabled the route altogether.
func TestMCPRawDoc_RejectsAnyDocOtherThanRaw(t *testing.T) {
	f := newMCPFixture(t)

	// Baseline: doc == "raw" IS the route; the file still comes back.
	status, body := mcpGet(t, f.ts, f.ts.server.URL+"/api/v1/mcp/global/raw?agent_id=agent1", true)
	if status != http.StatusOK || !strings.Contains(string(body), `"content"`) {
		t.Fatalf("GET .../global/raw: status = %d body = %s, want 200 carrying the file (the gate must not disable the route)", status, body)
	}

	// Every other doc value is a plain 404 — never a file read. "r%2Faw" is the
	// escaping caller's shape: Go's mux DECODES the segment before the handler
	// sees it, so PathValue("doc") is the literal "r/aw" there and the gate has
	// to refuse it on value, not on spelling (verified against a standalone
	// ServeMux).
	for _, doc := range []string{"nonsense", "r%2Faw", "%2Fraw", "raw%20", "raw2"} {
		t.Run(doc, func(t *testing.T) {
			status, body := mcpGet(t, f.ts,
				f.ts.server.URL+"/api/v1/mcp/global/"+doc+"?agent_id=agent1", true)
			if status != http.StatusNotFound {
				t.Fatalf("GET .../global/%s: status = %d, want 404 (removing the doc gate in handleMCPRawDoc serves the file here); body=%s",
					doc, status, body)
			}
			// The layer file was not read, let alone echoed: no content member
			// and none of the file's bytes.
			if strings.Contains(string(body), `"content"`) || bytes.Contains(body, []byte(f.globalRaw)) {
				t.Fatalf("GET .../global/%s served the layer file through the {doc} wildcard: %s", doc, body)
			}
		})
	}
}

// TestMCPAuthRoutes_AgentIDMissingOnEveryRoute pins the contract rule that
// `agent_id` is a REQUIRED query param on EVERY route: routes 3, 4, 5 and 6
// here (route 2 has its own cell in the error matrix of
// rest_mcp_auth_test.go) plus the AUTH-CARRYING inventory of route 1, whose
// rows make room for the auth report.
//
// Both other refusals are made unreachable on purpose, so a 400 can only be the
// agent_id one: the flow id is SHAPE-VALID (32 lowercase hex), the server name
// exists and route 4 carries a well-formed body — which is also what pins the
// validation ORDER behind finding F7 (agent_id is checked BEFORE the flow id,
// the body and the lookup).
//
// It lives here rather than next to the other route tests because it needs only
// the auth fixture of the inventory tests and the shared mcpAuthDo helper.
func TestMCPAuthRoutes_AgentIDMissingOnEveryRoute(t *testing.T) {
	f := newMCPAuthFixture(t)
	flowID := strings.Repeat("a", mcpAuthFlowIDLen)

	requests := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/v1/mcp", nil},
		{http.MethodGet, "/api/v1/mcp/auth/" + flowID, nil},
		{http.MethodPost, "/api/v1/mcp/auth/" + flowID + "/complete",
			MCPAuthCompleteRequest{Code: "a-code", State: "a-state"}},
		{http.MethodDelete, "/api/v1/mcp/auth/" + flowID, nil},
		{http.MethodDelete, "/api/v1/mcp/servers/oauth-srv/auth", nil},
	}
	for _, r := range requests {
		status, body := mcpAuthDo(t, f.ts, r.method, f.ts.server.URL+r.path, r.body, true)
		if status != http.StatusBadRequest || !strings.Contains(string(body), "agent_id_missing") {
			t.Fatalf("%s %s without agent_id: status = %d body = %s, want 400 agent_id_missing",
				r.method, r.path, status, body)
		}
	}
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

// TestMCPRawFile_PathGuardMatchesWrite (MENOR-3): for the SAME layer file
// the read endpoint and the write endpoints must apply the SAME path guard —
// a root outside the allowed workspace roots answers 403
// mcp_path_not_allowed on GET raw, PUT raw and PUT toggle alike, with
// nothing read, decoded or written under the disallowed root. The guard
// order is preserved: before any read (GET) and before decode/write (PUTs).
func TestMCPRawFile_PathGuardMatchesWrite(t *testing.T) {
	f := newMCPFixture(t)
	trap := "/not-allowed-root"
	f.loop.paths["trap"] = mcp.Paths{LeleDir: f.globalDir, AgentWorkspace: trap, Cwd: f.projectDir}
	base := f.ts.server.URL + "/api/v1/mcp/agent"

	// Read path: GET .../agent/raw must refuse exactly like the writes do.
	status, body := mcpGet(t, f.ts, base+"/raw?agent_id=trap", true)
	if status != http.StatusForbidden || !strings.Contains(string(body), "mcp_path_not_allowed") {
		t.Fatalf("GET raw: status = %d body = %s, want 403 mcp_path_not_allowed (read/write guards must agree)",
			status, body)
	}

	// Write path 1: PUT .../agent/raw — same verdict (guard before decode).
	status, body = mcpPut(t, f.ts, base+"/raw?agent_id=trap", `{"content":"{\"mcpServers\":{}}"}`)
	if status != http.StatusForbidden || !strings.Contains(string(body), "mcp_path_not_allowed") {
		t.Fatalf("PUT raw: status = %d body = %s, want 403 mcp_path_not_allowed", status, body)
	}

	// Write path 2: PUT .../toggle — same verdict (guard before write).
	status, body = mcpPut(t, f.ts, base+"/servers/only-global/toggle?agent_id=trap", `{"enabled":false}`)
	if status != http.StatusForbidden || !strings.Contains(string(body), "mcp_path_not_allowed") {
		t.Fatalf("toggle: status = %d body = %s, want 403 mcp_path_not_allowed", status, body)
	}

	// Nothing may have been created under the disallowed root.
	if _, err := os.Stat(filepath.Join(trap, "mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("file created under %s (err=%v)", trap, err)
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
//	is present but shadowed by the agent stub, so the shadow guard (step 8
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

// TestMCPToggle_StubBelowWinnerRefused (MENOR-2): a DISABLE that would
// create a stub BELOW the winning layer is inert — the winner keeps serving
// — so it is refused for EVERY layer below the winner, not only global (the
// old guard was exactly this rule spelled for the lowest layer only). Same
// error code; the refusal message names the winning layer. Writes AT or
// ABOVE the winner stay allowed.
func TestMCPToggle_StubBelowWinnerRefused(t *testing.T) {
	// projectOnly builds a fixture where "solo" is defined ONLY in the
	// project layer (global file present but empty).
	projectOnly := func(t *testing.T) *mcpFixture {
		t.Helper()
		f := newMCPSoloFixture(t, `{"mcpServers": {}}`)
		writeMCPJSON(t, filepath.Join(f.projectDir, ".lele", "mcp.json"),
			`{"mcpServers": {"solo": {"command": "solo-cmd"}}}`)
		return f
	}

	t.Run("project winner + agent disable is refused", func(t *testing.T) {
		f := projectOnly(t)
		reloads := mcpCountReloads(t, f)
		projectFile := filepath.Join(f.projectDir, ".lele", "mcp.json")
		projectBefore := mcpReadFile(t, projectFile)

		status, body := mcpPut(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/agent/servers/solo/toggle?agent_id=agent1", `{"enabled":false}`)
		if status != http.StatusBadRequest || !strings.Contains(string(body), "mcp_stub_layer_not_allowed") {
			t.Fatalf("status = %d body = %s, want 400 mcp_stub_layer_not_allowed (an inert write must not land)",
				status, body)
		}
		if !strings.Contains(string(body), "project") {
			t.Fatalf("refusal must name the winning layer (project): %s", body)
		}
		if _, err := os.Stat(filepath.Join(f.agentDir, "mcp.json")); !os.IsNotExist(err) {
			t.Fatalf("stub written below the winner (err=%v)", err)
		}
		if !bytes.Equal(mcpReadFile(t, projectFile), projectBefore) {
			t.Fatal("winner file changed by a refused write")
		}
		if reloads() != 0 {
			t.Fatalf("reload count = %d, want 0", reloads())
		}
	})

	t.Run("agent winner + agent disable is allowed", func(t *testing.T) {
		f := newMCPSoloFixture(t, `{"mcpServers": {}}`)
		writeMCPJSON(t, filepath.Join(f.agentDir, "mcp.json"),
			`{"mcpServers": {"solo": {"command": "solo-cmd"}}}`)
		reloads := mcpCountReloads(t, f)

		status, resp, body := mcpToggle(t, f,
			f.ts.server.URL+"/api/v1/mcp/agent/servers/solo/toggle?agent_id=agent1", `{"enabled":false}`)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", status, body)
		}
		if !resp.Changed || resp.Created || resp.Effective != "disabled" || resp.EffectiveLayer != "agent" {
			t.Fatalf("changed/created/effective/effective_layer = %v/%v/%q/%q, want true/false/disabled/agent",
				resp.Changed, resp.Created, resp.Effective, resp.EffectiveLayer)
		}
		if reloads() != 1 {
			t.Fatalf("reload count = %d, want 1", reloads())
		}
	})

	t.Run("global winner + agent disable is allowed", func(t *testing.T) {
		f := newMCPSoloFixture(t, `{"mcpServers": {"solo": {"command": "solo-cmd"}}}`)
		reloads := mcpCountReloads(t, f)

		status, resp, body := mcpToggle(t, f,
			f.ts.server.URL+"/api/v1/mcp/agent/servers/solo/toggle?agent_id=agent1", `{"enabled":false}`)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", status, body)
		}
		if !resp.Created || !resp.Changed || resp.Effective != "disabled" || resp.EffectiveLayer != "agent" {
			t.Fatalf("created/changed/effective/effective_layer = %v/%v/%q/%q, want true/true/disabled/agent",
				resp.Created, resp.Changed, resp.Effective, resp.EffectiveLayer)
		}
		if reloads() != 1 {
			t.Fatalf("reload count = %d, want 1", reloads())
		}
	})

	t.Run("global disable below a project winner is still refused", func(t *testing.T) {
		f := projectOnly(t)
		reloads := mcpCountReloads(t, f)
		globalFile := filepath.Join(f.globalDir, "mcp.json")
		projectFile := filepath.Join(f.projectDir, ".lele", "mcp.json")
		globalBefore := mcpReadFile(t, globalFile)
		projectBefore := mcpReadFile(t, projectFile)

		status, body := mcpPut(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/global/servers/solo/toggle?agent_id=agent1", `{"enabled":false}`)
		if status != http.StatusBadRequest || !strings.Contains(string(body), "mcp_stub_layer_not_allowed") {
			t.Fatalf("status = %d body = %s, want 400 mcp_stub_layer_not_allowed", status, body)
		}
		if !strings.Contains(string(body), "project") {
			t.Fatalf("refusal must name the winning layer (project): %s", body)
		}
		if !bytes.Equal(mcpReadFile(t, globalFile), globalBefore) ||
			!bytes.Equal(mcpReadFile(t, projectFile), projectBefore) {
			t.Fatal("a refused stub write must not touch any file")
		}
		if reloads() != 0 {
			t.Fatalf("reload count = %d, want 0", reloads())
		}
	})
}

// TestMCPToggle_InvalidEntryRefused (MENOR-N1): a name whose EFFECTIVE
// verdict is invalid is not toggleable in either direction, on ANY layer —
// the rule both clients already enforce, now invariant at the API. A write
// into an invalid entry would mask the invalid verdict behind a
// legit-looking enabled/disabled state (the verdict derivation prefers
// Disabled over Invalid), and the only repair is the raw editor. The guard
// keys off inv.Effective[name] — the same map the response row uses — and
// fires before auto resolution, the shadow/force logic and any write, so
// file bytes and the reload counter never move.
func TestMCPToggle_InvalidEntryRefused(t *testing.T) {
	// invalidSolo: "broken" has neither command nor url nor a disabled
	// flag — Discover refuses it, so the derived effective verdict is
	// invalid (the reviewer's N1 repro fixture).
	const invalidSolo = `{"mcpServers": {"broken": {"description": "no transport configured"}}}`
	invalidFixture := func(t *testing.T) (*mcpFixture, string) {
		t.Helper()
		f := newMCPSoloFixture(t, invalidSolo)
		return f, filepath.Join(f.globalDir, "mcp.json")
	}
	// assertRefused pins the whole refusal contract: 400 mcp_entry_invalid
	// naming the server, its verdict and the raw endpoint; file bytes
	// untouched; zero reloads.
	assertRefused := func(t *testing.T, f *mcpFixture, globalFile string, reloads func() int, raw []byte) {
		t.Helper()
		if !bytes.Equal(mcpReadFile(t, globalFile), []byte(invalidSolo)) {
			t.Fatalf("file changed by a refused toggle: %s", mcpReadFile(t, globalFile))
		}
		if !strings.Contains(string(raw), "mcp_entry_invalid") {
			t.Fatalf("body lacks mcp_entry_invalid: %s", raw)
		}
		if !strings.Contains(string(raw), "broken") ||
			!strings.Contains(string(raw), "invalid") ||
			!strings.Contains(string(raw), "/raw") {
			t.Fatalf("refusal must name the server, its verdict and the raw endpoint: %s", raw)
		}
		if got := reloads(); got != 0 {
			t.Fatalf("reload count = %d, want 0 (a refused toggle must not reload)", got)
		}
	}

	t.Run("auto_disable_is_refused", func(t *testing.T) {
		f, globalFile := invalidFixture(t)
		reloads := mcpCountReloads(t, f)

		status, raw := mcpPut(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/auto/servers/broken/toggle?agent_id=agent1", `{"enabled":false}`)
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d body = %s, want 400", status, raw)
		}
		assertRefused(t, f, globalFile, reloads, raw)
	})

	// The direction the clients never send: enable is refused too, so the
	// masking hazard is closed in BOTH directions by the API alone.
	t.Run("auto_enable_is_refused", func(t *testing.T) {
		f, globalFile := invalidFixture(t)
		reloads := mcpCountReloads(t, f)

		status, raw := mcpPut(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/auto/servers/broken/toggle?agent_id=agent1", `{"enabled":true}`)
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d body = %s, want 400", status, raw)
		}
		assertRefused(t, f, globalFile, reloads, raw)
	})

	t.Run("concrete_layer_is_refused", func(t *testing.T) {
		f, globalFile := invalidFixture(t)
		reloads := mcpCountReloads(t, f)

		status, raw := mcpPut(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/global/servers/broken/toggle?agent_id=agent1", `{"enabled":false}`)
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d body = %s, want 400", status, raw)
		}
		assertRefused(t, f, globalFile, reloads, raw)
	})

	// The guard fires BEFORE the stub guard: a lower layer addressing an
	// invalid winner would get mcp_stub_layer_not_allowed from the stub
	// rule — the invalid verdict must win the ordering.
	t.Run("guard_fires_before_the_stub_guard", func(t *testing.T) {
		f := newMCPSoloFixture(t, `{"mcpServers": {}}`)
		writeMCPJSON(t, filepath.Join(f.projectDir, ".lele", "mcp.json"), invalidSolo)
		reloads := mcpCountReloads(t, f)
		globalBefore := mcpReadFile(t, filepath.Join(f.globalDir, "mcp.json"))
		projectFile := filepath.Join(f.projectDir, ".lele", "mcp.json")
		projectBefore := mcpReadFile(t, projectFile)

		status, raw := mcpPut(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/global/servers/broken/toggle?agent_id=agent1", `{"enabled":false}`)
		if status != http.StatusBadRequest || !strings.Contains(string(raw), "mcp_entry_invalid") {
			t.Fatalf("status = %d body = %s, want 400 mcp_entry_invalid (before the stub guard)", status, raw)
		}
		if strings.Contains(string(raw), "mcp_stub_layer_not_allowed") {
			t.Fatalf("stub guard fired before the invalid guard: %s", raw)
		}
		if !bytes.Equal(mcpReadFile(t, projectFile), projectBefore) ||
			!bytes.Equal(mcpReadFile(t, filepath.Join(f.globalDir, "mcp.json")), globalBefore) {
			t.Fatal("a refused toggle must not touch any file")
		}
		if reloads() != 0 {
			t.Fatalf("reload count = %d, want 0", reloads())
		}
	})

	// The guard keys off the EFFECTIVE verdict, not off a per-layer
	// Defines entry: the agent layer defines nothing for "broken", so
	// pre-guard this was a legal cross-layer create (a stub ABOVE the
	// invalid global winner) that would have masked the invalid verdict.
	t.Run("stub_over_invalid_via_higher_layer_is_refused", func(t *testing.T) {
		f, globalFile := invalidFixture(t)
		reloads := mcpCountReloads(t, f)

		status, raw := mcpPut(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/agent/servers/broken/toggle?agent_id=agent1", `{"enabled":false}`)
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d body = %s, want 400", status, raw)
		}
		assertRefused(t, f, globalFile, reloads, raw)
		if _, err := os.Stat(filepath.Join(f.agentDir, "mcp.json")); !os.IsNotExist(err) {
			t.Fatalf("stub written over an invalid entry (err=%v)", err)
		}
	})

	// CONTROL: a valid enabled row still disables with 200 changed:true —
	// the guard is not a blanket refusal.
	t.Run("control_valid_enabled_row_still_disables", func(t *testing.T) {
		f := newMCPSoloFixture(t, `{"mcpServers": {"ok": {"command": "ok-cmd"}}}`)
		reloads := mcpCountReloads(t, f)

		status, resp, raw := mcpToggle(t, f,
			f.ts.server.URL+"/api/v1/mcp/auto/servers/ok/toggle?agent_id=agent1", `{"enabled":false}`)
		if status != http.StatusOK {
			t.Fatalf("status = %d body = %s, want 200", status, raw)
		}
		if !resp.Changed || resp.Effective != "disabled" {
			t.Fatalf("changed/effective = %v/%q, want true/disabled", resp.Changed, resp.Effective)
		}
		if reloads() != 1 {
			t.Fatalf("reload count = %d, want 1", reloads())
		}
	})

	// CONTROL: a valid disabled row still enables with 200.
	t.Run("control_valid_disabled_row_still_enables", func(t *testing.T) {
		f := newMCPSoloFixture(t, `{"mcpServers": {"ok": {"command": "ok-cmd", "disabled": true}}}`)
		reloads := mcpCountReloads(t, f)

		status, resp, raw := mcpToggle(t, f,
			f.ts.server.URL+"/api/v1/mcp/auto/servers/ok/toggle?agent_id=agent1", `{"enabled":true}`)
		if status != http.StatusOK {
			t.Fatalf("status = %d body = %s, want 200", status, raw)
		}
		if !resp.Changed || resp.Effective != "enabled" {
			t.Fatalf("changed/effective = %v/%q, want true/enabled", resp.Changed, resp.Effective)
		}
		if reloads() != 1 {
			t.Fatalf("reload count = %d, want 1", reloads())
		}
	})
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
// content echoes the request bytes byte-for-byte. Reload fires exactly once.
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

// TestMCPRawPut_ResponseIsRequestAfterSuccessfulSave (MAYOR-1): once
// SaveRawFile has succeeded the response is 200 echoing EXACTLY the bytes
// the client sent — byte-for-byte, whitespace included. No post-write read
// may turn a landed, reloaded write into a 500, and a second writer winning
// the race must not have its bytes echoed as the result of THIS save. The
// race window is driven through the only seam that fires between the atomic
// rename and where the old read-back used to run: the reload hook plays the
// concurrent writer (an editor, the TUI, another WebUI tab).
func TestMCPRawPut_ResponseIsRequestAfterSuccessfulSave(t *testing.T) {
	// Whitespace/formatting proves byte-for-byte echoing, not re-serialization.
	payload := "{\n  \"mcpServers\": {\"odd\": {\"command\": \"run-1\"}}\n}\n"

	put := func(t *testing.T, f *mcpFixture) (int, MCPRawFileResponse, []byte) {
		t.Helper()
		status, raw := mcpPut(t, f.ts,
			f.ts.server.URL+"/api/v1/mcp/global/raw?agent_id=agent1",
			`{"content":`+strconv.Quote(payload)+`}`)
		var resp MCPRawFileResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("Decode() error = %v body=%s", err, raw)
		}
		return status, resp, raw
	}

	t.Run("read-back fails after a landed write", func(t *testing.T) {
		f := newMCPSoloFixture(t, `{"mcpServers": {"seed": {"command": "seed-cmd"}}}`)
		globalFile := filepath.Join(f.globalDir, "mcp.json")
		reloads := 0
		f.ts.channel.SetReloadMCP(func() {
			reloads++
			// A concurrent deleter unlinks the file inside the race window.
			_ = os.Remove(globalFile)
		})

		status, resp, raw := put(t, f)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200: a successful SaveRawFile must never answer non-2xx; body=%s",
				status, raw)
		}
		if strings.Contains(string(raw), "mcp_read_failed") || strings.Contains(string(raw), "mcp_write_failed") {
			t.Fatalf("landed write reported as a failure: %s", raw)
		}
		if !resp.Exists {
			t.Fatal("exists = false, want true (the write just landed)")
		}
		if resp.Content != payload {
			t.Fatalf("content = %q, want the request bytes %q", resp.Content, payload)
		}
		if reloads != 1 {
			t.Fatalf("reload count = %d, want 1", reloads)
		}
	})

	t.Run("second writer wins the rename/read race", func(t *testing.T) {
		f := newMCPSoloFixture(t, `{"mcpServers": {"seed": {"command": "seed-cmd"}}}`)
		globalFile := filepath.Join(f.globalDir, "mcp.json")
		other := `{"mcpServers": {"other-writer": {"command": "other"}}}`
		reloads := 0
		f.ts.channel.SetReloadMCP(func() {
			reloads++
			// A second writer replaces the file inside the race window.
			_ = os.WriteFile(globalFile, []byte(other), 0o644)
		})

		status, resp, raw := put(t, f)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", status, raw)
		}
		if resp.Content != payload {
			t.Fatalf("content = %q, want the bytes THIS client sent (%q) — never another writer's bytes",
				resp.Content, payload)
		}
		if reloads != 1 {
			t.Fatalf("reload count = %d, want 1", reloads)
		}
	})
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
