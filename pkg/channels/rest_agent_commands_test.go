// Tests for the per-agent command endpoints (rest_agent_commands.go).
//
// The risk these tests target is the one the feature exists for: a command
// request for agent X must never be answered from — or written into — the
// DEFAULT workspace, and a write must never touch a level the agent does not
// run. So the tests build two agents with two real on-disk workspaces plus an
// isolated ~/.lele (LELE_CONFIG_DIR), and assert which file each call created,
// changed or removed.

package channels

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// commandsTestLoop widens the base fake (which only knows "main") with a set of
// agents, each pointing at its own workspace directory.
type commandsWorkspaceLoop struct {
	*nativeTestAgentLoop
	workspaces map[string]string
}

// commandsWorkspaceLoop knows every agent but deliberately implements NO
// InvalidateHarnessWorkspace, which is how a test simulates an agent loop
// without the optional capability.

func (l *commandsWorkspaceLoop) GetAgentInfo(agentID string) (AgentBasicInfo, bool) {
	if ws, ok := l.workspaces[agentID]; ok {
		return AgentBasicInfo{ID: agentID, Name: agentID, Workspace: ws}, true
	}
	return l.nativeTestAgentLoop.GetAgentInfo(agentID)
}

func (l *commandsWorkspaceLoop) ListAvailableAgentIDs() []string {
	ids := make([]string, 0, len(l.workspaces))
	for id := range l.workspaces {
		ids = append(ids, id)
	}
	return append(ids, "main")
}

type commandsTestLoop struct {
	*commandsWorkspaceLoop

	// invalidations records the workspace keys passed to
	// InvalidateHarnessWorkspace, so the tests can assert the runtime cache is
	// dropped after every write. mu guards it because the channel may call the
	// hook from any handler.
	invalidations []string
	mu            sync.Mutex
}

// InvalidateHarnessWorkspace implements the optional harnessCommandInvalidator
// capability of the real agent loop.
func (l *commandsTestLoop) InvalidateHarnessWorkspace(workspace string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.invalidations = append(l.invalidations, workspace)
}

// takeInvalidations returns and clears the recorded calls.
func (l *commandsTestLoop) takeInvalidations() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.invalidations
	l.invalidations = nil
	return out
}

// newCommandsTestServer isolates the global level in a temp dir and gives
// agents "alpha" and "beta" their own workspaces. The returned map holds the
// workspace of each agent plus "lele" for the fake ~/.lele.
func newCommandsTestServer(t *testing.T) (*nativeTestServer, map[string]string) {
	t.Helper()

	leleDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", leleDir)

	alpha := t.TempDir()
	beta := t.TempDir()

	ts := newNativeTestServer(t)
	loop := &commandsTestLoop{
		commandsWorkspaceLoop: &commandsWorkspaceLoop{
			nativeTestAgentLoop: ts.loop,
			workspaces:          map[string]string{"alpha": alpha, "beta": beta},
		},
	}
	ts.channel.agentLoop = loop
	ts.loop = loop.nativeTestAgentLoop

	return ts, map[string]string{"alpha": alpha, "beta": beta, "lele": leleDir, "default": ts.loop.workspace}
}

// writeCommandFile puts a command markdown file at one of the discovery levels.
func writeCommandFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return path
}

// commandMarkdown builds the front-matter form the UI sends.
func commandMarkdown(description, template string) string {
	return "---\ndescription: \"" + description + "\"\n---\n" + template + "\n"
}

func doCommandRequest(t *testing.T, ts *nativeTestServer, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("NewRequest(%s %s): %v", method, url, err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(%s %s): %v", method, url, err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	return resp, payload
}

func getAgentCommands(t *testing.T, ts *nativeTestServer, agentID string) (int, AgentCommandsResponse) {
	t.Helper()
	resp, body := doCommandRequest(t, ts, http.MethodGet, ts.server.URL+"/api/v1/agents/"+agentID+"/commands", "")
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, AgentCommandsResponse{}
	}
	var payload AgentCommandsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode AgentCommandsResponse: %v body=%s", err, body)
	}
	return resp.StatusCode, payload
}

func findCommandRow(rows []AgentCommandInfo, name, source string) (AgentCommandInfo, bool) {
	for _, row := range rows {
		if row.Name == name && row.Source == source {
			return row, true
		}
	}
	return AgentCommandInfo{}, false
}

// --- list -------------------------------------------------------------------

func TestAgentCommandsList_MergesLevelsPerAgent(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "alpha-only.md",
		commandMarkdown("alpha private command", "hello $ARGUMENTS"))
	writeCommandFile(t, filepath.Join(dirs["beta"], "commands"), "beta-only.md",
		commandMarkdown("beta private command", "hi"))
	writeCommandFile(t, filepath.Join(dirs["lele"], "commands"), "shared.md",
		commandMarkdown("global command", "shared body"))

	status, alpha := getAgentCommands(t, ts, "alpha")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if alpha.AgentID != "alpha" || alpha.Workspace != dirs["alpha"] {
		t.Fatalf("agent identity wrong: %+v", alpha)
	}
	if want := filepath.Join(dirs["alpha"], "commands"); alpha.CommandsDir != want {
		t.Fatalf("commands_dir = %q, want %q", alpha.CommandsDir, want)
	}
	if !alpha.CommandsDirExists {
		t.Fatal("commands_dir_exists = false, want true (alpha has a command)")
	}
	if alpha.SharedBy != 1 {
		t.Fatalf("shared_by = %d, want 1 (alpha has its own workspace)", alpha.SharedBy)
	}

	if _, ok := findCommandRow(alpha.Commands, "alpha-only", "workspace"); !ok {
		t.Fatalf("alpha-only missing from alpha's list: %+v", alpha.Commands)
	}
	if _, ok := findCommandRow(alpha.Commands, "shared", "global"); !ok {
		t.Fatalf("global command missing from alpha's list: %+v", alpha.Commands)
	}
	// The central guarantee: beta's command must not leak into alpha's page.
	for _, row := range alpha.Commands {
		if row.Name == "beta-only" {
			t.Fatalf("beta's workspace command leaked into agent alpha: %+v", alpha.Commands)
		}
	}

	_, beta := getAgentCommands(t, ts, "beta")
	if _, ok := findCommandRow(beta.Commands, "beta-only", "workspace"); !ok {
		t.Fatalf("beta-only missing from beta's list: %+v", beta.Commands)
	}
	for _, row := range beta.Commands {
		if row.Name == "alpha-only" {
			t.Fatalf("alpha's workspace command leaked into agent beta: %+v", beta.Commands)
		}
	}
}

func TestAgentCommandsList_FlagsDeletableOnlyOnWritableLevels(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "mine.md",
		commandMarkdown("workspace level", "body"))
	writeCommandFile(t, filepath.Join(dirs["lele"], "commands"), "theirs.md",
		commandMarkdown("global level", "body"))
	ts.loop.config.Commands = map[string]config.CommandDefinition{
		"from-config": {Description: "config level", Template: "inline"},
	}

	_, payload := getAgentCommands(t, ts, "alpha")

	mine, ok := findCommandRow(payload.Commands, "mine", "workspace")
	if !ok {
		t.Fatalf("workspace command missing: %+v", payload.Commands)
	}
	if !mine.Deletable {
		t.Fatal("workspace command must be deletable")
	}
	if mine.ShadowedBy != "" {
		t.Fatalf("winner row must carry an empty shadowed_by, got %q", mine.ShadowedBy)
	}

	global, ok := findCommandRow(payload.Commands, "theirs", "global")
	if !ok {
		t.Fatalf("global command missing: %+v", payload.Commands)
	}
	if !global.Deletable {
		t.Fatal("global command is a file the API can remove; deletable must be true")
	}

	cfg, ok := findCommandRow(payload.Commands, "from-config", "config")
	if !ok {
		t.Fatalf("config command missing: %+v", payload.Commands)
	}
	if cfg.Deletable {
		t.Fatal("config-defined command must never be deletable")
	}
	if cfg.Path != "" {
		t.Fatalf("config command has no file, path = %q", cfg.Path)
	}
}

func TestAgentCommandsList_TagsShadowedRows(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	// Same name at global and workspace: workspace wins (higher precedence).
	writeCommandFile(t, filepath.Join(dirs["lele"], "commands"), "dup.md",
		commandMarkdown("global version", "global body"))
	writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "dup.md",
		commandMarkdown("workspace version", "workspace body"))

	_, payload := getAgentCommands(t, ts, "alpha")

	winner, ok := findCommandRow(payload.Commands, "dup", "workspace")
	if !ok {
		t.Fatalf("workspace row missing: %+v", payload.Commands)
	}
	if winner.ShadowedBy != "" {
		t.Fatalf("winner must not be shadowed, got %q", winner.ShadowedBy)
	}
	if winner.Description != "workspace version" {
		t.Fatalf("winner description = %q, want the workspace file's", winner.Description)
	}

	loser, ok := findCommandRow(payload.Commands, "dup", "global")
	if !ok {
		t.Fatalf("shadowed global row missing: %+v", payload.Commands)
	}
	if loser.ShadowedBy != "workspace" {
		t.Fatalf("shadowed_by = %q, want \"workspace\"", loser.ShadowedBy)
	}
}

func TestAgentCommandsList_ReportsAgentPermissions(t *testing.T) {
	ts, _ := newCommandsTestServer(t)
	ts.loop.config.Harness.AllowShell = true
	ts.loop.config.Harness.AllowAbsoluteFiles = true

	_, payload := getAgentCommands(t, ts, "alpha")
	if !payload.Harness.AllowShell || !payload.Harness.AllowAbsoluteFiles {
		t.Fatalf("harness defaults not echoed: %+v", payload.Harness)
	}
	if len(payload.Builtin) == 0 {
		t.Fatal("builtin commands must be listed so the UI can explain name collisions")
	}
	for _, b := range payload.Builtin {
		if strings.HasPrefix(b.Name, "/") {
			t.Fatalf("builtin name must be slash-free for this API: %q", b.Name)
		}
	}
}

func TestAgentCommandsList_UnknownAgent(t *testing.T) {
	ts, _ := newCommandsTestServer(t)

	resp, body := doCommandRequest(t, ts, http.MethodGet, ts.server.URL+"/api/v1/agents/ghost/commands", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 body=%s", resp.StatusCode, body)
	}
}

// --- detail -----------------------------------------------------------------

func TestAgentCommandDetail_ReturnsRawMarkdown(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	content := commandMarkdown("raw round trip", "line one\nline two")
	writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "review.md", content)

	url := ts.server.URL + "/api/v1/agents/alpha/commands/review"
	resp, body := doCommandRequest(t, ts, http.MethodGet, url, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", resp.StatusCode, body)
	}

	var detail AgentCommandDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Content != content {
		t.Fatalf("content must be byte-identical to the file:\n got %q\nwant %q", detail.Content, content)
	}
	if detail.Source != "workspace" || !detail.Deletable {
		t.Fatalf("unexpected detail flags: %+v", detail)
	}
	if detail.Path != filepath.Join(dirs["alpha"], "commands", "review.md") {
		t.Fatalf("path = %q", detail.Path)
	}
}

func TestAgentCommandDetail_ConfigCommandIsForbidden(t *testing.T) {
	ts, _ := newCommandsTestServer(t)
	ts.loop.config.Commands = map[string]config.CommandDefinition{
		"from-config": {Description: "config level", Template: "inline"},
	}

	resp, body := doCommandRequest(t, ts, http.MethodGet, ts.server.URL+"/api/v1/agents/alpha/commands/from-config", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "not_editable") {
		t.Fatalf("body = %s, want code not_editable", body)
	}
}

func TestAgentCommandDetail_UnknownIsNotFound(t *testing.T) {
	ts, _ := newCommandsTestServer(t)

	resp, _ := doCommandRequest(t, ts, http.MethodGet, ts.server.URL+"/api/v1/agents/alpha/commands/nope", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestAgentCommandDetail_RejectsTraversal(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	// A name that would escape the commands dir must be rejected by the grammar
	// before any path is built.
	for _, tc := range []struct{ raw, label string }{
		// ".." must be percent-encoded: Go's ServeMux cleans the literal form
		// out of the path before the handler ever runs, so it cannot test the
		// grammar. %2e%2e reaches PathValue as "..".
		{"%2e%2e", ".."},
		{"%2e%2e%2f%2e%2e%2fetc%2fpasswd", "../../etc/passwd"},
		{"a%20b", "a b"},
		{"UPPER", "UPPER"},
		{"name.md", "name.md"},
	} {
		resp, body := doCommandRequest(t, ts, http.MethodGet,
			ts.server.URL+"/api/v1/agents/alpha/commands/"+tc.raw, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("name %q: status = %d body=%s, want 400 (must never reach disk)", tc.label, resp.StatusCode, body)
		}
	}
	// Nothing was created outside the workspace while probing.
	if _, err := os.Stat(filepath.Join(dirs["alpha"], "commands")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(dirs["alpha"], "commands"))
		if len(entries) != 0 {
			t.Fatalf("probing created files: %v", entries)
		}
	}
}

// --- create -----------------------------------------------------------------

func TestAgentCommandCreate_WritesIntoAgentWorkspace(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	body := `{"name":"deploy","content":"---\ndescription: \"ship it\"\n---\nrun $ARGUMENTS","scope":"workspace"}`
	resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%s", resp.StatusCode, payload)
	}

	path := filepath.Join(dirs["alpha"], "commands", "deploy.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("created file missing at %s: %v", path, err)
	}
	if string(raw) != "---\ndescription: \"ship it\"\n---\nrun $ARGUMENTS" {
		t.Fatalf("file content = %q", raw)
	}

	var created AgentCommandMutationResponse
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatalf("decode mutation: %v", err)
	}
	if !created.OK || created.Command == nil || created.Command.Name != "deploy" {
		t.Fatalf("mutation payload = %s", payload)
	}
	if created.Command.Source != "workspace" || !created.Command.Deletable {
		t.Fatalf("created row flags = %+v", created.Command)
	}
	if created.Command.Description != "ship it" {
		t.Fatalf("description = %q, want it parsed back from the stored frontmatter", created.Command.Description)
	}

	// The list must show it immediately: no cache sits in front of the file.
	if _, listed := getAgentCommands(t, ts, "alpha"); !listed.has("deploy", "workspace") {
		t.Fatalf("created command not listed: %+v", listed.Commands)
	}

	// The other agent's workspace and the shared global dir must be untouched —
	// the whole point of a per-agent endpoint.
	if entries, _ := os.ReadDir(filepath.Join(dirs["beta"], "commands")); len(entries) != 0 {
		t.Fatalf("beta's workspace was written: %v", entries)
	}
	if entries, _ := os.ReadDir(filepath.Join(dirs["lele"], "commands")); len(entries) != 0 {
		t.Fatalf("the global level was written: %v", entries)
	}
}

func TestAgentCommandCreate_DefaultsToWorkspaceScope(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	body := `{"name":"no-scope","content":"---\ndescription: \"x\"\n---\nbody"}`
	resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%s", resp.StatusCode, payload)
	}
	if _, err := os.Stat(filepath.Join(dirs["alpha"], "commands", "no-scope.md")); err != nil {
		t.Fatalf("an omitted scope must mean workspace: %v", err)
	}
}

func TestAgentCommandCreate_RejectsGlobalScope(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	body := `{"name":"everybody","content":"---\ndescription: \"x\"\n---\nbody","scope":"global"}`
	resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), "invalid_scope") {
		t.Fatalf("body = %s, want code invalid_scope", payload)
	}
	if entries, _ := os.ReadDir(filepath.Join(dirs["lele"], "commands")); len(entries) != 0 {
		t.Fatalf("a rejected global create still wrote to the shared dir: %v", entries)
	}
}

func TestAgentCommandCreate_RejectsInvalidContent(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	// allow_shell must parse: a typo there is a hard error in the loader, and
	// storing it would leave a file the agent silently skips.
	body := `{"name":"broken","content":"---\ndescription: \"x\"\nallow_shell: maybe\n---\nbody"}`
	resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), "command_invalid") {
		t.Fatalf("body = %s, want code command_invalid", payload)
	}
	if _, err := os.Stat(filepath.Join(dirs["alpha"], "commands", "broken.md")); !os.IsNotExist(err) {
		t.Fatalf("invalid content was stored anyway: %v", err)
	}
}

func TestAgentCommandCreate_RejectsInvalidNames(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	for _, name := range []string{"", "UPPER", "with space", "../escape", "a/b", "review.md", "_leading", strings.Repeat("x", 100)} {
		body := `{"name":` + jsonQuote(name) + `,"content":"---\ndescription: \"x\"\n---\nbody"}`
		resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands", body)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(payload), "invalid_name") {
			t.Fatalf("name %q: status = %d body=%s, want 400 invalid_name", name, resp.StatusCode, payload)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(dirs["alpha"], "commands")); len(entries) != 0 {
		t.Fatalf("rejected creates left files behind: %v", entries)
	}
}

func TestAgentCommandCreate_Conflicts(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "exists.md", commandMarkdown("here", "b"))
	// Real newlines: the front-matter regex anchors on "\n", so a literal
	// backslash-n (what a raw Go string yields) never parses as front matter.
	content := "---\ndescription: \"x\"\n---\nbody"

	resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands",
		`{"name":"exists","content":`+jsonQuote(content)+`}`)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(payload), `"exists"`) {
		t.Fatalf("existing name: status = %d body=%s, want 409 exists", resp.StatusCode, payload)
	}

	// A built-in name would be shadowed by the dispatcher, so the file would be
	// dead the moment it is written.
	// "clear" is dispatched by handleCommand; "help" is answered by Telegram
	// before the message ever reaches the agent loop. Both are reserved by
	// commands.DispatcherReserved(), which pkg/agent keeps synced to both sources.
	for _, name := range []string{"clear", "help", "status", "model", "start"} {
		resp, payload = doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands",
			`{"name":`+jsonQuote(name)+`,"content":`+jsonQuote(content)+`}`)
		if resp.StatusCode != http.StatusConflict || !strings.Contains(string(payload), "reserved_name") {
			t.Fatalf("built-in name %q: status = %d body=%s, want 409 reserved_name", name, resp.StatusCode, payload)
		}
		if _, err := os.Stat(filepath.Join(dirs["alpha"], "commands", strings.ToLower(name)+".md")); !os.IsNotExist(err) {
			t.Fatalf("a built-in collision must not create a file: %v", err)
		}
	}

	// A name nothing dispatches must work, even when it *looks* reserved: /review
	// is the canonical custom command and no built-in owns it.
	resp, payload = doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands",
		`{"name":"review","content":`+jsonQuote(content)+`}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("free name: status = %d body=%s, want 201", resp.StatusCode, payload)
	}
}

func TestAgentCommandCreate_UnknownAgent(t *testing.T) {
	ts, _ := newCommandsTestServer(t)

	resp, _ := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/ghost/commands",
		`{"name":"x","content":"---\ndescription: \"x\"\n---\nbody"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// --- update -----------------------------------------------------------------

func TestAgentCommandUpdate_ReplacesFileContent(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	path := writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "edit.md",
		commandMarkdown("before", "old body"))

	body := `{"content":` + jsonQuote(commandMarkdown("after", "new body")) + `}`
	resp, payload := doCommandRequest(t, ts, http.MethodPut, ts.server.URL+"/api/v1/agents/alpha/commands/edit", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", resp.StatusCode, payload)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "new body") || strings.Contains(string(raw), "old body") {
		t.Fatalf("file not replaced: %q", raw)
	}

	var updated AgentCommandMutationResponse
	if err := json.Unmarshal(payload, &updated); err != nil {
		t.Fatalf("decode mutation: %v", err)
	}
	if updated.Command == nil || updated.Command.Description != "after" {
		t.Fatalf("response row = %+v, want the freshly parsed description", updated.Command)
	}
}

func TestAgentCommandUpdate_RespectsSourceOfTheWinner(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	global := writeCommandFile(t, filepath.Join(dirs["lele"], "commands"), "shared.md",
		commandMarkdown("global v1", "global body"))
	workspace := writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "shared.md",
		commandMarkdown("workspace v1", "workspace body"))

	body := `{"content":` + jsonQuote(commandMarkdown("workspace v2", "edited")) + `}`
	resp, payload := doCommandRequest(t, ts, http.MethodPut, ts.server.URL+"/api/v1/agents/alpha/commands/shared", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", resp.StatusCode, payload)
	}

	// The agent's own copy wins precedence, so it is the one that gets written.
	if raw, _ := os.ReadFile(workspace); !strings.Contains(string(raw), "workspace v2") {
		t.Fatalf("workspace file untouched: %q", raw)
	}
	if raw, _ := os.ReadFile(global); strings.Contains(string(raw), "workspace v2") {
		t.Fatalf("the shared global file was rewritten through an agent page: %q", raw)
	}
}

func TestAgentCommandUpdate_GlobalFileIsWritable(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	global := writeCommandFile(t, filepath.Join(dirs["lele"], "commands"), "shared.md",
		commandMarkdown("global v1", "global body"))

	body := `{"content":` + jsonQuote(commandMarkdown("global v2", "edited")) + `}`
	resp, payload := doCommandRequest(t, ts, http.MethodPut, ts.server.URL+"/api/v1/agents/alpha/commands/shared", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", resp.StatusCode, payload)
	}
	if raw, _ := os.ReadFile(global); !strings.Contains(string(raw), "global v2") {
		t.Fatalf("global file not updated: %q", raw)
	}
}

func TestAgentCommandUpdate_ReadOnlyAndMissing(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)
	ts.loop.config.Commands = map[string]config.CommandDefinition{
		"from-config": {Description: "config level", Template: "inline"},
	}
	body := `{"content":` + jsonQuote(commandMarkdown("nope", "nope")) + `}`

	resp, payload := doCommandRequest(t, ts, http.MethodPut, ts.server.URL+"/api/v1/agents/alpha/commands/from-config", body)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(payload), "not_editable") {
		t.Fatalf("config command: status = %d body=%s, want 403 not_editable", resp.StatusCode, payload)
	}

	resp, payload = doCommandRequest(t, ts, http.MethodPut, ts.server.URL+"/api/v1/agents/alpha/commands/ghost", body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing command: status = %d body=%s, want 404", resp.StatusCode, payload)
	}
	if _, err := os.Stat(filepath.Join(dirs["alpha"], "commands", "ghost.md")); !os.IsNotExist(err) {
		t.Fatal("a 404 update must not create the file")
	}
}

func TestAgentCommandUpdate_RejectsInvalidContent(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	path := writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "keep.md",
		commandMarkdown("original", "original body"))

	resp, payload := doCommandRequest(t, ts, http.MethodPut, ts.server.URL+"/api/v1/agents/alpha/commands/keep",
		`{"content":"---\ndescription: \"x\"\nallow_shell: perhaps\n---\nbody"}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(payload), "command_invalid") {
		t.Fatalf("status = %d body=%s, want 400 command_invalid", resp.StatusCode, payload)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "original body") {
		t.Fatalf("a rejected update clobbered the file: %q", raw)
	}
}

// --- delete -----------------------------------------------------------------

func TestAgentCommandDelete_RemovesOnlyTheWinningFile(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	global := writeCommandFile(t, filepath.Join(dirs["lele"], "commands"), "shared.md",
		commandMarkdown("global", "global body"))
	workspace := writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "shared.md",
		commandMarkdown("workspace", "workspace body"))

	resp, payload := doCommandRequest(t, ts, http.MethodDelete, ts.server.URL+"/api/v1/agents/alpha/commands/shared", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", resp.StatusCode, payload)
	}
	var deleted AgentCommandDeleteResponse
	if err := json.Unmarshal(payload, &deleted); err != nil {
		t.Fatalf("decode delete: %v", err)
	}
	if !deleted.OK {
		t.Fatalf("body = %s, want ok=true", payload)
	}

	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("the winning workspace file survived: %v", err)
	}
	// Deleting the override reveals the global command underneath — the correct
	// meaning of "delete this level", and the reason beta still sees it.
	if _, err := os.Stat(global); err != nil {
		t.Fatalf("the shadowed global file must be left alone: %v", err)
	}
	if _, listed := getAgentCommands(t, ts, "alpha"); !listed.has("shared", "global") {
		t.Fatalf("after deleting the override the global command must reappear: %+v", listed.Commands)
	}
}

func TestAgentCommandDelete_ReadOnlyAndMissing(t *testing.T) {
	ts, _ := newCommandsTestServer(t)
	ts.loop.config.Commands = map[string]config.CommandDefinition{
		"from-config": {Description: "config level", Template: "inline"},
	}

	// DELETE reports not_deletable (not not_editable): the UI keeps the delete
	// button disabled from `deletable`, so this code only shows up on a stale
	// view, and it must be distinguishable from "cannot open this file".
	resp, payload := doCommandRequest(t, ts, http.MethodDelete, ts.server.URL+"/api/v1/agents/alpha/commands/from-config", "")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(payload), "not_deletable") {
		t.Fatalf("status = %d body=%s, want 403 not_deletable", resp.StatusCode, payload)
	}

	resp, _ = doCommandRequest(t, ts, http.MethodDelete, ts.server.URL+"/api/v1/agents/alpha/commands/ghost", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// --- helpers used by the tests above ----------------------------------------

// has reports whether the payload lists name at source.
func (r AgentCommandsResponse) has(name, source string) bool {
	_, ok := findCommandRow(r.Commands, name, source)
	return ok
}

// jsonQuote encodes a string as a JSON literal so test bodies can embed
// markdown without hand-escaping it.
func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// --- runtime cache invalidation ---------------------------------------------

// The agent loops cache one command manager per workspace behind a refresh TTL
// (pkg/agent harnessRefreshTTL). A REST write that only touched the disk would
// therefore leave the just-created command untypeable in chat for up to 30
// seconds, so every mutation must also drop the cached managers it affects.
func TestAgentCommandWrites_InvalidateWorkspaceCache(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)
	loop := ts.channel.agentLoop.(*commandsTestLoop)
	content := "---\ndescription: \"x\"\n---\nbody"

	// create (workspace scope) -> only that agent's workspace.
	resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands",
		`{"name":"fresh","content":`+jsonQuote(content)+`}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d body=%s", resp.StatusCode, payload)
	}
	if got := loop.takeInvalidations(); !slices.Equal(got, []string{dirs["alpha"]}) {
		t.Fatalf("create invalidated %v, want exactly alpha's workspace", got)
	}

	// update of that workspace file -> same single workspace.
	resp, payload = doCommandRequest(t, ts, http.MethodPut, ts.server.URL+"/api/v1/agents/alpha/commands/fresh",
		`{"content":`+jsonQuote(commandMarkdown("x", "edited"))+`}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: status = %d body=%s", resp.StatusCode, payload)
	}
	if got := loop.takeInvalidations(); !slices.Equal(got, []string{dirs["alpha"]}) {
		t.Fatalf("update invalidated %v, want exactly alpha's workspace", got)
	}

	// delete -> same single workspace.
	resp, payload = doCommandRequest(t, ts, http.MethodDelete, ts.server.URL+"/api/v1/agents/alpha/commands/fresh", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: status = %d body=%s", resp.StatusCode, payload)
	}
	if got := loop.takeInvalidations(); !slices.Equal(got, []string{dirs["alpha"]}) {
		t.Fatalf("delete invalidated %v, want exactly alpha's workspace", got)
	}
}

func TestAgentCommandGlobalWrite_InvalidatesEveryWorkspace(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)
	loop := ts.channel.agentLoop.(*commandsTestLoop)

	writeCommandFile(t, filepath.Join(dirs["lele"], "commands"), "shared.md", commandMarkdown("g", "old"))

	resp, payload := doCommandRequest(t, ts, http.MethodPut, ts.server.URL+"/api/v1/agents/alpha/commands/shared",
		`{"content":`+jsonQuote(commandMarkdown("g", "new"))+`}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, payload)
	}

	got := loop.takeInvalidations()
	// "" is the loop's key for the defaults workspace, which also reads the
	// global level, plus every agent's resolved workspace — beta included,
	// because a global command changes what beta can run even though beta made
	// no request.
	for _, want := range []string{"", dirs["alpha"], dirs["beta"], dirs["default"]} {
		if !slices.Contains(got, want) {
			t.Fatalf("global write invalidated %v, missing %q", got, want)
		}
	}
	if len(got) != len(dedupeAgentCommandPaths(got)) {
		t.Fatalf("global write invalidated duplicates: %v", got)
	}
}

// A loop without the optional capability must keep working: the write is a
// success and the runtime simply reloads on its own TTL. This is what protects
// every other channel fake in the package.
func TestAgentCommandWrites_WorkWithoutInvalidationHook(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)
	// Same agents and workspaces, but no InvalidateHarnessWorkspace method.
	ts.channel.agentLoop = ts.channel.agentLoop.(*commandsTestLoop).commandsWorkspaceLoop

	resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands",
		`{"name":"nohook","content":`+jsonQuote(commandMarkdown("x", "body"))+`}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%s", resp.StatusCode, payload)
	}
	if _, err := os.Stat(filepath.Join(dirs["alpha"], "commands", "nohook.md")); err != nil {
		t.Fatalf("file was not written: %v", err)
	}
}

// --- content rules the parser does not enforce (brief §6) --------------------

func TestAgentCommandCreate_RejectsEmptyTemplate(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	// The loader accepts a body-less file (the config level skips it, the file
	// levels store it), so without this check the API would happily create a
	// command that expands to nothing.
	body := `{"name":"hollow","content":"---\ndescription: \"no body\"\n---\n"}`
	resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands", body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(payload), "command_invalid") {
		t.Fatalf("status = %d body=%s, want 400 command_invalid", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), "template") {
		t.Fatalf("body = %s, want the message to name the offending field", payload)
	}
	if _, err := os.Stat(filepath.Join(dirs["alpha"], "commands", "hollow.md")); !os.IsNotExist(err) {
		t.Fatalf("an empty template was stored anyway: %v", err)
	}
}

func TestAgentCommandCreate_RejectsEmptyDescription(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	body := `{"name":"nodesc","content":"---\nagent: coder\n---\nreal body"}`
	resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands", body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(payload), "command_invalid") {
		t.Fatalf("status = %d body=%s, want 400 command_invalid", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), "description") {
		t.Fatalf("body = %s, want the message to name the offending field", payload)
	}
	if _, err := os.Stat(filepath.Join(dirs["alpha"], "commands", "nodesc.md")); !os.IsNotExist(err) {
		t.Fatalf("an empty description was stored anyway: %v", err)
	}
}

func TestAgentCommandCreate_RejectsOversizedContent(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	// Just above the 256 KB cap and below the gateway's 1 MB body limit, so the
	// handler — not the transport — is what answers.
	oversized := "---\ndescription: \"big\"\n---\n" + strings.Repeat("x", maxAgentCommandSize)
	resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands",
		`{"name":"big","content":`+jsonQuote(oversized)+`}`)
	if resp.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(string(payload), "too_large") {
		t.Fatalf("status = %d body=%s, want 413 too_large", resp.StatusCode, payload)
	}
	if _, err := os.Stat(filepath.Join(dirs["alpha"], "commands", "big.md")); !os.IsNotExist(err) {
		t.Fatalf("oversized content was stored anyway: %v", err)
	}
}

// PUT runs the same validator: bypassing it through the update path would let a
// client store a file the agent refuses to load.
func TestAgentCommandUpdate_RejectsEmptyTemplate(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	path := writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "hollow.md",
		commandMarkdown("original", "original body"))

	resp, payload := doCommandRequest(t, ts, http.MethodPut, ts.server.URL+"/api/v1/agents/alpha/commands/hollow",
		`{"content":"---\ndescription: \"original\"\n---\n"}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(payload), "command_invalid") {
		t.Fatalf("status = %d body=%s, want 400 command_invalid", resp.StatusCode, payload)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "original body") {
		t.Fatalf("the rejected update clobbered the file: %q", raw)
	}
}

// --- workspace policy -------------------------------------------------------

// An agent whose workspace sits outside the allowed roots is a policy refusal,
// not a missing agent: answering 404 would tell a caller the id is unknown when
// the real reason is that the configured path is off-limits. Every /agents/{id}
// endpoint already splits the two (handleAgentFiles); the command endpoints must
// not be the exception, and all five of them share the resolution helper.
func TestAgentCommands_ForeignWorkspaceIsForbidden(t *testing.T) {
	ts, _ := newCommandsTestServer(t)
	loop := ts.channel.agentLoop.(*commandsTestLoop)
	// /etc is outside home, /tmp and the cwd — the three roots
	// isAllowedWorkspacePath accepts. The path is never created: the grammar
	// check rejects it before any filesystem call.
	loop.workspaces["jail"] = "/etc/lele-outside-roots"

	calls := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/agents/jail/commands"},
		{http.MethodGet, "/api/v1/agents/jail/commands/anything"},
		{http.MethodPost, "/api/v1/agents/jail/commands"},
		{http.MethodPut, "/api/v1/agents/jail/commands/anything"},
		{http.MethodDelete, "/api/v1/agents/jail/commands/anything"},
	}
	for _, c := range calls {
		resp, payload := doCommandRequest(t, ts, c.method, ts.server.URL+c.path, `{"name":"x","content":"y"}`)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s = %d body=%s, want 403", c.method, c.path, resp.StatusCode, payload)
		}
		if !strings.Contains(string(payload), "workspace_forbidden") {
			t.Errorf("%s %s body = %s, want code workspace_forbidden", c.method, c.path, payload)
		}
	}
}

// T-B12: the tri-state of allow_absolute_files must survive the wire in both
// directions. nil means "inherit the harness default", so collapsing it to false
// would silently turn off a capability an agent configured globally — and the
// editor's third option ("heredar") has nothing to select if the API cannot say
// "absent".
func TestAgentCommands_TriStateRoundTrip(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	cases := []struct {
		name    string
		front   string // the front-matter line, "" = key absent
		want    *bool
		wantPtr bool
	}{
		{name: "inherit", front: "", want: nil, wantPtr: false},
		{name: "on", front: "allow_absolute_files: true\n", want: boolPtr(true), wantPtr: true},
		{name: "off", front: "allow_absolute_files: false\n", want: boolPtr(false), wantPtr: true},
	}

	for _, tc := range cases {
		content := "---\ndescription: \"" + tc.name + "\"\n" + tc.front + "---\nbody\n"
		writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), tc.name+".md", content)
	}

	resp, body := doCommandRequest(t, ts, http.MethodGet, ts.server.URL+"/api/v1/agents/alpha/commands", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d body=%s", resp.StatusCode, body)
	}
	var payload AgentCommandsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, tc := range cases {
		row, ok := findCommandRow(payload.Commands, tc.name, "workspace")
		if !ok {
			t.Fatalf("%s: row missing from %+v", tc.name, payload.Commands)
		}
		if (row.AllowAbsoluteFiles != nil) != tc.wantPtr {
			t.Errorf("%s: pointer present = %v, want %v (value %v)", tc.name,
				row.AllowAbsoluteFiles != nil, tc.wantPtr, row.AllowAbsoluteFiles)
		}
		if tc.wantPtr && *row.AllowAbsoluteFiles != *tc.want {
			t.Errorf("%s: value = %v, want %v", tc.name, *row.AllowAbsoluteFiles, *tc.want)
		}
	}

	// And on the wire: the JSON itself must carry null / true / false, not omit
	// the key — the UI distinguishes the three from the parsed value.
	var rows []map[string]any
	if err := json.Unmarshal(structFieldJSON(t, body, "commands"), &rows); err != nil {
		t.Fatalf("unmarshal rows: %v", err)
	}
	seen := map[string]any{}
	for _, row := range rows {
		seen[row["name"].(string)] = row["allow_absolute_files"]
	}
	if v, ok := seen["inherit"]; !ok || v != nil {
		t.Errorf("inherit: JSON value = %#v present=%v, want null", v, ok)
	}
	if v := seen["on"]; v != true {
		t.Errorf("on: JSON value = %#v, want true", v)
	}
	if v := seen["off"]; v != false {
		t.Errorf("off: JSON value = %#v, want false", v)
	}
}

// T-B13: a GET right after a PUT must show the new content. The catalog is built
// from disk on demand (never from the loop's TTL cache), so this holds even for a
// loop that cannot invalidate — what it guards is that no response-level caching
// crept in between the two endpoints.
func TestAgentCommandUpdate_GetReflectsChangeImmediately(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "live.md",
		commandMarkdown("first version", "v1 body"))

	resp, payload := doCommandRequest(t, ts, http.MethodPut, ts.server.URL+"/api/v1/agents/alpha/commands/live",
		`{"content":`+jsonQuote(commandMarkdown("second version", "v2 body"))+`}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: status = %d body=%s", resp.StatusCode, payload)
	}

	// Detail endpoint (raw bytes of the file).
	resp, payload = doCommandRequest(t, ts, http.MethodGet, ts.server.URL+"/api/v1/agents/alpha/commands/live", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get detail: status = %d body=%s", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), "v2 body") {
		t.Fatalf("detail still serves the old content: %s", payload)
	}

	// List endpoint (parsed row).
	_, listed := getAgentCommands(t, ts, "alpha")
	row, ok := findCommandRow(listed.Commands, "live", "workspace")
	if !ok {
		t.Fatalf("live missing from catalog: %+v", listed.Commands)
	}
	if row.Description != "second version" {
		t.Fatalf("catalog description = %q, want the value just written", row.Description)
	}
}

// structFieldJSON pulls one top-level field out of a JSON object as raw bytes,
// so a test can look at what the wire actually carried instead of at what the
// typed struct chose to represent.
func structFieldJSON(t *testing.T, body []byte, field string) []byte {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	raw, ok := envelope[field]
	if !ok {
		t.Fatalf("response has no %q field: %s", field, body)
	}
	return raw
}

// The loader discovers .markdown as well as .md, so an existence check limited
// to "<name>.md" would let a create drop a second file on a command the agent
// already has — and which body runs would then depend on directory read order.
func TestAgentCommandCreate_ExistingMarkdownExtensionIsConflict(t *testing.T) {
	ts, dirs := newCommandsTestServer(t)

	long := writeCommandFile(t, filepath.Join(dirs["alpha"], "commands"), "review.markdown",
		commandMarkdown("the real one", "long extension body"))

	resp, payload := doCommandRequest(t, ts, http.MethodPost, ts.server.URL+"/api/v1/agents/alpha/commands",
		`{"name":"review","content":`+jsonQuote(commandMarkdown("impostor", "second file"))+`}`)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(payload), `"exists"`) {
		t.Fatalf("status = %d body=%s, want 409 exists", resp.StatusCode, payload)
	}
	if _, err := os.Stat(filepath.Join(dirs["alpha"], "commands", "review.md")); !os.IsNotExist(err) {
		t.Fatalf("a duplicate file was created next to review.markdown: %v", err)
	}
	if raw, _ := os.ReadFile(long); !strings.Contains(string(raw), "long extension body") {
		t.Fatalf("the existing file was overwritten: %q", raw)
	}
}
