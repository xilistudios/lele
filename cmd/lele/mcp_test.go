package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tools"
)

// ---------------------------------------------------------------------------
// Test harness
// ---------------------------------------------------------------------------

// mcpTestEnv isolates the three layer roots for one test: a temp LELE_CONFIG_DIR
// (global), a temp agent workspace (agent, wired through config.json) and a temp
// cwd (project). Every path lives under the temp dir so it passes the workspace
// guard.
type mcpTestEnv struct {
	root    string
	leleDir string
	agentWS string
	cwd     string
}

func newMCPTestEnv(t *testing.T) *mcpTestEnv {
	t.Helper()
	root := t.TempDir()
	e := &mcpTestEnv{
		root:    root,
		leleDir: filepath.Join(root, "lele"),
		agentWS: filepath.Join(root, "agentws"),
		cwd:     filepath.Join(root, "project"),
	}
	for _, d := range []string{e.leleDir, e.agentWS, e.cwd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	t.Setenv("LELE_CONFIG_DIR", e.leleDir)
	t.Chdir(e.cwd)
	writeMCPConfigFile(t, e.leleDir, e.agentWS)
	return e
}

// writeMCPConfigFile writes a minimal config.json whose default workspace is
// agentWS: the registry resolves the implicit "main" agent to it, so the agent
// layer lands inside the temp tree.
func writeMCPConfigFile(t *testing.T, leleDir, agentWS string) {
	t.Helper()
	writeMCPTestFile(t, filepath.Join(leleDir, "config.json"),
		fmt.Sprintf(`{"agents":{"defaults":{"workspace":%q}}}`, agentWS))
}

func writeMCPTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readMCPTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func (e *mcpTestEnv) globalFile() string  { return filepath.Join(e.leleDir, "mcp.json") }
func (e *mcpTestEnv) agentFile() string   { return filepath.Join(e.agentWS, "mcp.json") }
func (e *mcpTestEnv) projectFile() string { return filepath.Join(e.cwd, ".lele", "mcp.json") }

// seedThreeLayers writes the canonical fixture: global defines alpha, agent
// shadows alpha and defines bravo (disabled), project defines charlie (invalid).
func (e *mcpTestEnv) seedThreeLayers(t *testing.T) {
	t.Helper()
	writeMCPTestFile(t, e.globalFile(),
		`{"mcpServers":{"alpha":{"command":"global-alpha","args":["--from","global"],"description":"global alpha entry"}}}`)
	writeMCPTestFile(t, e.agentFile(),
		`{"mcpServers":{"alpha":{"command":"agent-alpha","args":["-y","x"],"description":"agent alpha entry"},`+
			`"bravo":{"command":"bravo-bin","disabled":true,"description":"a disabled server"}}}`)
	writeMCPTestFile(t, e.projectFile(),
		`{"mcpServers":{"charlie":{"command":"charlie","url":"http://charlie","description":"see: invalid"}}}`)
}

// runMCPSub drives one runMCP* function and captures its output.
func runMCPSub(t *testing.T, fn func(io.Writer, []string) int, args ...string) (string, int) {
	t.Helper()
	var buf bytes.Buffer
	code := fn(&buf, args)
	return buf.String(), code
}

// stubMCPLoadServer replaces the probe seam for the duration of one test and
// records the name it was asked to load.
func stubMCPLoadServer(t *testing.T, specs []tools.LoadedMCPTool, err error) *string {
	t.Helper()
	var got string
	old := mcpLoadServer
	mcpLoadServer = func(_ context.Context, _ mcp.Paths, name string) ([]tools.LoadedMCPTool, error) {
		got = name
		return specs, err
	}
	t.Cleanup(func() { mcpLoadServer = old })
	return &got
}

// ---------------------------------------------------------------------------
// Dispatcher + help
// ---------------------------------------------------------------------------

func TestRunMCP_BarePrintsHelp(t *testing.T) {
	out, code := runMCPSub(t, runMCP)
	if code != 0 {
		t.Fatalf("bare `lele mcp` exit = %d, want 0 (mirrors modelsCmd)", code)
	}
	if !strings.Contains(out, "lele mcp — MCP servers") {
		t.Errorf("bare help missing title:\n%s", out)
	}
}

func TestRunMCP_UnknownSubcommand(t *testing.T) {
	out, code := runMCPSub(t, runMCP, "frobnicate")
	if code != 1 {
		t.Fatalf("unknown subcommand exit = %d, want 1", code)
	}
	if !strings.Contains(out, "Unknown mcp command: frobnicate") {
		t.Errorf("missing unknown-command line:\n%s", out)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("unknown subcommand must print help:\n%s", out)
	}
}

func TestMCPHelp_ContainsCommands(t *testing.T) {
	out := captureOutput(mcpHelp)
	for _, sub := range []string{"list", "show", "add", "remove", "test"} {
		if !strings.Contains(out, "lele mcp "+sub) {
			t.Errorf("mcpHelp should document %q:\n%s", sub, out)
		}
	}
	// edit is a later task: it must NOT be advertised as a working command.
	if strings.Contains(out, "lele mcp edit") {
		t.Errorf("mcpHelp must not advertise the unimplemented edit command:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Flag parsing
// ---------------------------------------------------------------------------

func TestParseMCPAddArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
		check   func(t *testing.T, a mcpAddArgs)
	}{
		{
			name: "command with repeated args and env",
			args: []string{"fs", "--command", "npx", "--arg", "-y", "--arg", "pkg", "--env", "A=1", "--env", "B=2", "--description", "d", "--layer", "agent", "--force"},
			check: func(t *testing.T, a mcpAddArgs) {
				if a.name != "fs" || a.command != "npx" || a.layer != "agent" || !a.force {
					t.Fatalf("unexpected parse: %+v", a)
				}
				if got := strings.Join(a.args, ","); got != "-y,pkg" {
					t.Fatalf("args order = %q, want -y,pkg", got)
				}
				if len(a.env) != 2 {
					t.Fatalf("env = %v, want 2 pairs", a.env)
				}
			},
		},
		{
			name: "inline equals form",
			args: []string{"x", "--command=/bin/echo", "--env=KEY=a=b"},
			check: func(t *testing.T, a mcpAddArgs) {
				if a.command != "/bin/echo" {
					t.Fatalf("command = %q", a.command)
				}
				if a.env[0] != "KEY=a=b" {
					t.Fatalf("env = %v", a.env)
				}
			},
		},
		{
			name: "url with type",
			args: []string{"api", "--url", "https://x/mcp", "--type", "sse"},
			check: func(t *testing.T, a mcpAddArgs) {
				if a.url != "https://x/mcp" || a.typ != "sse" {
					t.Fatalf("unexpected parse: %+v", a)
				}
			},
		},
		{name: "missing name", args: []string{"--command", "x"}, wantErr: "missing server name"},
		{name: "extra positional", args: []string{"a", "b", "--command", "x"}, wantErr: "unexpected argument"},
		{name: "no command or url", args: []string{"a"}, wantErr: "one of --command or --url is required"},
		{name: "command and url", args: []string{"a", "--command", "x", "--url", "y"}, wantErr: "mutually exclusive"},
		{name: "type without url", args: []string{"a", "--command", "x", "--type", "http"}, wantErr: "--type is only valid with --url"},
		{name: "bad type", args: []string{"a", "--url", "u", "--type", "ws"}, wantErr: "unknown type"},
		{name: "bad env", args: []string{"a", "--command", "x", "--env", "NOEQUALS"}, wantErr: "want KEY=VALUE"},
		{name: "empty env key", args: []string{"a", "--command", "x", "--env", "=v"}, wantErr: "KEY must not be empty"},
		{name: "bad header", args: []string{"a", "--command", "x", "--header", "nope"}, wantErr: "--header: malformed"},
		{name: "unknown flag", args: []string{"a", "--command", "x", "--bogus"}, wantErr: "unknown flag --bogus"},
		{name: "flag without value", args: []string{"a", "--command"}, wantErr: "flag --command requires a value"},
		{name: "force with value", args: []string{"a", "--command", "x", "--force=yes"}, wantErr: "does not take a value"},
		{name: "bad layer", args: []string{"a", "--command", "x", "--layer", "nope"}, wantErr: "unknown layer"},
		{name: "slash name", args: []string{"a/b", "--command", "x"}, wantErr: "must not contain '/'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := parseMCPAddArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, a)
			}
		})
	}
}

func TestParseMCPRemoveArgs(t *testing.T) {
	a, err := parseMCPRemoveArgs([]string{"x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.name != "x" || a.all || a.layer != "" || a.agent != "main" {
		t.Fatalf("defaults wrong: %+v", a)
	}
	if _, err := parseMCPRemoveArgs([]string{"x", "--all"}); err != nil {
		t.Fatalf("--all rejected: %v", err)
	}
	if _, err := parseMCPRemoveArgs([]string{"x", "--layer", "agent"}); err != nil {
		t.Fatalf("--layer rejected: %v", err)
	}
	if _, err := parseMCPRemoveArgs([]string{"x", "--layer", "agent", "--all"}); err == nil ||
		!strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("layer+all err = %v, want mutual-exclusion", err)
	}
	if _, err := parseMCPRemoveArgs([]string{"x", "--layer", "nope"}); err == nil {
		t.Fatal("bad layer accepted")
	}
	if _, err := parseMCPRemoveArgs(nil); err == nil {
		t.Fatal("missing name accepted")
	}
}

func TestParseMCPTestArgs(t *testing.T) {
	a, err := parseMCPTestArgs([]string{"x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.timeout.String() != "30s" {
		t.Fatalf("default timeout = %v, want 30s", a.timeout)
	}
	a, err = parseMCPTestArgs([]string{"x", "--timeout", "2m"})
	if err != nil || a.timeout.String() != "2m0s" {
		t.Fatalf("timeout parse: %v, %v", a.timeout, err)
	}
	for _, bad := range []string{"bogus", "0s", "-5s"} {
		if _, err := parseMCPTestArgs([]string{"x", "--timeout", bad}); err == nil {
			t.Errorf("timeout %q accepted, want error", bad)
		}
	}
	if _, err := parseMCPTestArgs([]string{"x", "--nope"}); err == nil {
		t.Error("unknown flag accepted")
	}
}

func TestParseMCPListShowArgs(t *testing.T) {
	if _, err := parseMCPListArgs([]string{"--layer", "project"}); err != nil {
		t.Fatalf("valid layer rejected: %v", err)
	}
	if _, err := parseMCPListArgs([]string{"--layer", "nope"}); err == nil {
		t.Error("bad layer accepted")
	}
	if _, err := parseMCPListArgs([]string{"extra"}); err == nil {
		t.Error("positional accepted by list")
	}
	if _, err := parseMCPListArgs([]string{"--bogus"}); err == nil {
		t.Error("unknown flag accepted by list")
	}
	if _, err := parseMCPShowArgs(nil); err == nil {
		t.Error("show without name accepted")
	}
	a, err := parseMCPShowArgs([]string{"--agent", "worker", "name"})
	if err != nil || a.name != "name" || a.agent != "worker" {
		t.Fatalf("show parse: %+v %v", a, err)
	}
}

func TestParseMCPFlags_MixedOrder(t *testing.T) {
	a, err := parseMCPAddArgs([]string{"--layer", "project", "srv", "--command", "c", "--arg", "a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.name != "srv" || a.layer != "project" || a.command != "c" {
		t.Fatalf("flags around positional mis-parsed: %+v", a)
	}
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

func TestMCPList_ThreeLayerFixture(t *testing.T) {
	e := newMCPTestEnv(t)
	e.seedThreeLayers(t)

	out, code := runMCPSub(t, runMCPList)
	if code != 0 {
		t.Fatalf("list exit = %d, want 0:\n%s", code, out)
	}

	// One row per effective server, sorted case-insensitively: alpha, bravo, charlie.
	if !strings.Contains(out, "●  alpha") {
		t.Errorf("alpha should be enabled (●):\n%s", out)
	}
	if !strings.Contains(out, "○  bravo") {
		t.Errorf("bravo should be disabled (○):\n%s", out)
	}
	if !strings.Contains(out, "!  charlie") {
		t.Errorf("charlie should be invalid (!):\n%s", out)
	}

	// Winner layers.
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		switch {
		case strings.Contains(l, "alpha") && !strings.Contains(l, "mcp.json"):
			if !strings.Contains(l, "agent") {
				t.Errorf("alpha winner should be the agent layer: %q", l)
			}
		case strings.Contains(l, "charlie"):
			if !strings.Contains(l, "project") {
				t.Errorf("charlie winner should be the project layer: %q", l)
			}
		}
	}

	// Deterministic order: a second run is byte-identical.
	again, code2 := runMCPSub(t, runMCPList)
	if code2 != 0 || again != out {
		t.Errorf("list output is not deterministic:\n--- first ---\n%s\n--- second ---\n%s", out, again)
	}

	// Ordering of the three rows.
	ia, ib, ic := strings.Index(out, "alpha"), strings.Index(out, "bravo"), strings.Index(out, "charlie")
	if !(ia < ib && ib < ic) {
		t.Errorf("rows not sorted alpha<bravo<charlie: %d %d %d", ia, ib, ic)
	}

	// Counts, footer paths and the invalid-entry warning.
	for _, want := range []string{
		"3 server(s): 1 enabled, 1 disabled, 1 invalid",
		e.globalFile(),
		e.agentFile(),
		e.projectFile(),
		"⊘ warning:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
}

func TestMCPList_EmptyAndLayerFilter(t *testing.T) {
	e := newMCPTestEnv(t)

	out, code := runMCPSub(t, runMCPList)
	if code != 0 {
		t.Fatalf("empty list exit = %d, want 0", code)
	}
	if !strings.Contains(out, "No MCP servers configured.") {
		t.Errorf("empty list should say so:\n%s", out)
	}
	if !strings.Contains(out, e.globalFile()) {
		t.Errorf("empty list should point at the global file:\n%s", out)
	}

	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"alpha":{"command":"a"}}}`)
	writeMCPTestFile(t, e.projectFile(), `{"mcpServers":{"charlie":{"command":"c"}}}`)

	out, code = runMCPSub(t, runMCPList, "--layer", "project")
	if code != 0 {
		t.Fatalf("filtered list exit = %d", code)
	}
	if !strings.Contains(out, "charlie") {
		t.Errorf("project filter dropped charlie:\n%s", out)
	}
	if strings.Contains(out, "alpha") {
		t.Errorf("project filter kept global alpha:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// show
// ---------------------------------------------------------------------------

func TestMCPShow_WinnerShadowAndArgs(t *testing.T) {
	e := newMCPTestEnv(t)
	e.seedThreeLayers(t)

	out, code := runMCPSub(t, runMCPShow, "alpha")
	if code != 0 {
		t.Fatalf("show exit = %d, want 0:\n%s", code, out)
	}
	for _, want := range []string{
		`MCP server "alpha"`,
		"state:       enabled",
		"layer:       agent",
		"file:        " + e.agentFile(),
		"command:     agent-alpha",
		`args:        ["-y", "x"]`,
		"Per-layer breakdown",
		"global",
		"present=yes defines=yes",
		"← winner",
		"(shadowed)",
		"project",
		"present=no",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
}

func TestMCPShow_SecretCanary(t *testing.T) {
	e := newMCPTestEnv(t)
	const canary = "LEAKME-9f2c-canary"
	t.Setenv("CANARY", canary)

	writeMCPTestFile(t, e.globalFile(),
		`{"mcpServers":{"leaky":{"command":"cmd ${CANARY}","env":{"TOKEN":"${CANARY}","OTHER":"plain"},`+
			`"headers":{"Authorization":"Bearer ${CANARY}"},"description":"d"}}}`)

	out, code := runMCPSub(t, runMCPShow, "leaky")
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, out)
	}
	if strings.Contains(out, canary) {
		t.Fatalf("expanded secret value leaked into show output:\n%s", out)
	}
	if !strings.Contains(out, "env keys:    OTHER, TOKEN") {
		t.Errorf("env keys (sorted, names only) missing:\n%s", out)
	}
	if !strings.Contains(out, "header keys: Authorization") {
		t.Errorf("header keys missing:\n%s", out)
	}
	// The command is raw config: ${CANARY} stays literal, unexpanded.
	if !strings.Contains(out, "cmd ${CANARY}") {
		t.Errorf("command must be shown verbatim (unexpanded):\n%s", out)
	}
}

func TestMCPShow_UnknownSuggests(t *testing.T) {
	e := newMCPTestEnv(t)
	e.seedThreeLayers(t)

	out, code := runMCPSub(t, runMCPShow, "alp")
	if code != 1 {
		t.Fatalf("unknown show exit = %d, want 1", code)
	}
	if !strings.Contains(out, `✗ unknown MCP server "alp"`) {
		t.Errorf("missing unknown-server line:\n%s", out)
	}
	if !strings.Contains(out, "did you mean: alpha?") {
		t.Errorf("missing prefix suggestion:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// add
// ---------------------------------------------------------------------------

func TestMCPAdd_DefaultGlobal(t *testing.T) {
	e := newMCPTestEnv(t)

	out, code := runMCPSub(t, runMCPAdd, "fileserver", "--command", "npx",
		"--arg", "-y", "--arg", "pkg", "--env", "TOKEN=${SECRET}", "--description", "fs")
	if code != 0 {
		t.Fatalf("add exit = %d, want 0:\n%s", code, out)
	}
	want := fmt.Sprintf("✓ added %q to global (%s)", "fileserver", e.globalFile())
	if !strings.Contains(out, want) {
		t.Errorf("add output = %q, want containing %q", out, want)
	}
	got := readMCPTestFile(t, e.globalFile())
	for _, frag := range []string{
		`"command":"npx"`,
		`"args":["-y","pkg"]`,
		`"env":{"TOKEN":"${SECRET}"}`,
		`"description":"fs"`,
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("global file missing %s:\n%s", frag, got)
		}
	}
}

func TestMCPAdd_VerbatimNoExpansion(t *testing.T) {
	e := newMCPTestEnv(t)
	t.Setenv("SECRET", "LEAKME")

	_, code := runMCPSub(t, runMCPAdd, "claude-ext", "--command", "/bin/echo",
		"--env", "TOKEN=${SECRET}", "--header", "Authorization=Bearer ${SECRET}")
	if code != 0 {
		t.Fatalf("add exit = %d", code)
	}
	got := readMCPTestFile(t, e.globalFile())
	if strings.Contains(got, "LEAKME") {
		t.Fatalf("expansion ran at write time:\n%s", got)
	}
	if !strings.Contains(got, `"TOKEN":"${SECRET}"`) || !strings.Contains(got, `"Authorization":"Bearer ${SECRET}"`) {
		t.Fatalf("placeholders not stored verbatim:\n%s", got)
	}
}

func TestMCPAdd_AgentLayer(t *testing.T) {
	e := newMCPTestEnv(t)

	out, code := runMCPSub(t, runMCPAdd, "agentonly", "--command", "/bin/echo", "--layer", "agent")
	if code != 0 {
		t.Fatalf("add --layer agent exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, e.agentFile()) {
		t.Errorf("add should report the agent file:\n%s", out)
	}
	if !strings.Contains(readMCPTestFile(t, e.agentFile()), `"agentonly"`) {
		t.Errorf("agent file does not hold the entry:\n%s", readMCPTestFile(t, e.agentFile()))
	}
	if _, err := os.Stat(e.globalFile()); err == nil {
		t.Errorf("global file should not have been created for an agent-layer add")
	}
}

func TestMCPAdd_RefusesExistingWithoutForce(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"delta":{"command":"old"}}}`)
	before := readMCPTestFile(t, e.globalFile())

	out, code := runMCPSub(t, runMCPAdd, "delta", "--command", "new")
	if code != 1 {
		t.Fatalf("existing add exit = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, `✗ server "delta" already exists in global`) {
		t.Errorf("missing refusal message:\n%s", out)
	}
	if readMCPTestFile(t, e.globalFile()) != before {
		t.Errorf("refused add must not touch the file:\n%s", readMCPTestFile(t, e.globalFile()))
	}

	out, code = runMCPSub(t, runMCPAdd, "delta", "--command", "new", "--force")
	if code != 0 {
		t.Fatalf("forced add exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, `✓ replaced "delta"`) {
		t.Errorf("forced add should report a replace:\n%s", out)
	}
	if !strings.Contains(readMCPTestFile(t, e.globalFile()), `"command":"new"`) {
		t.Errorf("forced add did not replace the command:\n%s", readMCPTestFile(t, e.globalFile()))
	}
}

func TestMCPAdd_ShadowNote(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.projectFile(), `{"mcpServers":{"gamma":{"command":"project-gamma"}}}`)

	out, code := runMCPSub(t, runMCPAdd, "gamma", "--command", "global-gamma")
	if code != 0 {
		t.Fatalf("add exit = %d, want 0:\n%s", code, out)
	}
	want := fmt.Sprintf("⊘ note: %q in project shadows this entry (defined in %s)", "gamma", e.projectFile())
	if !strings.Contains(out, want) {
		t.Errorf("missing shadow note %q:\n%s", want, out)
	}
}

func TestMCPAdd_PathGuardRefuses(t *testing.T) {
	root := t.TempDir()
	leleDir := filepath.Join(root, "lele")
	if err := os.MkdirAll(leleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	cwd := filepath.Join(root, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	// A workspace outside every allowed root: /dev/null cannot be created even
	// by root, and the path is not under home, /tmp, /var/folders or cwd.
	badWS := filepath.Join(os.DevNull, "lele-mcp-guard")
	writeMCPConfigFile(t, leleDir, badWS)

	out, code := runMCPSub(t, runMCPAdd, "guarded", "--command", "/bin/echo", "--layer", "agent")
	if code != 1 {
		t.Fatalf("guard exit = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "refusing to write") {
		t.Errorf("missing guard message:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(badWS, "mcp.json")); err == nil {
		t.Errorf("guarded write created a file outside the allowed roots")
	}
}

func TestMCPAdd_UnknownFlagLeavesFileUntouched(t *testing.T) {
	e := newMCPTestEnv(t)

	out, code := runMCPSub(t, runMCPAdd, "fileserver", "--command", "x", "--bogus")
	if code != 1 {
		t.Fatalf("unknown flag exit = %d, want 1", code)
	}
	if !strings.Contains(out, "✗ unknown flag --bogus") || !strings.Contains(out, "Usage:") {
		t.Errorf("unknown flag should print a usage error:\n%s", out)
	}
	if _, err := os.Stat(e.globalFile()); err == nil {
		t.Errorf("parse failure must not create the layer file")
	}
}

// ---------------------------------------------------------------------------
// remove
// ---------------------------------------------------------------------------

func TestMCPRemove_WinnerOnlyThenAll(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"romeo":{"command":"global"}}}`)
	writeMCPTestFile(t, e.agentFile(), `{"mcpServers":{"romeo":{"command":"agent"}}}`)

	out, code := runMCPSub(t, runMCPRemove, "romeo")
	if code != 0 {
		t.Fatalf("remove exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, fmt.Sprintf("✓ removed %q from agent (%s)", "romeo", e.agentFile())) {
		t.Errorf("winner removal not reported:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("⊘ note: %q is still defined in global (%s)", "romeo", e.globalFile())) {
		t.Errorf("missing shadow note:\n%s", out)
	}
	if !strings.Contains(out, "lele mcp remove romeo --all") {
		t.Errorf("missing --all hint:\n%s", out)
	}
	if strings.Contains(readMCPTestFile(t, e.agentFile()), "romeo") {
		t.Errorf("agent copy should be gone:\n%s", readMCPTestFile(t, e.agentFile()))
	}
	if !strings.Contains(readMCPTestFile(t, e.globalFile()), "romeo") {
		t.Errorf("global copy should remain after winner-only removal:\n%s", readMCPTestFile(t, e.globalFile()))
	}

	// --all removes what is left.
	out, code = runMCPSub(t, runMCPRemove, "romeo", "--all")
	if code != 0 {
		t.Fatalf("remove --all exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "✓ removed") {
		t.Errorf("--all removal not reported:\n%s", out)
	}
	if strings.Contains(readMCPTestFile(t, e.globalFile()), "romeo") {
		t.Errorf("--all should have emptied the global file:\n%s", readMCPTestFile(t, e.globalFile()))
	}
}

func TestMCPRemove_AllRemovesEveryCopyIncludingStubs(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"sierra":{"command":"global"}}}`)
	writeMCPTestFile(t, e.projectFile(), `{"mcpServers":{"sierra":{"disabled":true}}}`)

	out, code := runMCPSub(t, runMCPRemove, "sierra", "--all")
	if code != 0 {
		t.Fatalf("remove --all exit = %d:\n%s", code, out)
	}
	if strings.Contains(readMCPTestFile(t, e.globalFile()), "sierra") {
		t.Errorf("global copy remains:\n%s", readMCPTestFile(t, e.globalFile()))
	}
	if strings.Contains(readMCPTestFile(t, e.projectFile()), "sierra") {
		t.Errorf("project stub remains:\n%s", readMCPTestFile(t, e.projectFile()))
	}
}

func TestMCPRemove_LayerAndUnknown(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.agentFile(), `{"mcpServers":{"tango":{"command":"agent"}}}`)

	out, code := runMCPSub(t, runMCPRemove, "tango", "--layer", "global")
	if code != 1 {
		t.Fatalf("--layer on absent entry exit = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, `✗ "tango" is not defined in global`) {
		t.Errorf("missing not-defined message:\n%s", out)
	}

	out, code = runMCPSub(t, runMCPRemove, "nope")
	if code != 1 {
		t.Fatalf("unknown remove exit = %d, want 1", code)
	}
	if !strings.Contains(out, `✗ unknown MCP server "nope"`) {
		t.Errorf("missing unknown message:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// test
// ---------------------------------------------------------------------------

func TestMCPTest_HappyPath(t *testing.T) {
	e := newMCPTestEnv(t)
	e.seedThreeLayers(t)
	stubMCPLoadServer(t, []tools.LoadedMCPTool{
		{Name: "mcp_alpha_one"}, {Name: "mcp_alpha_two"}, {Name: "mcp_alpha_three"},
	}, nil)

	out, code := runMCPSub(t, runMCPTest, "alpha")
	if code != 0 {
		t.Fatalf("test exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, `✓ "alpha" connected — 3 tools: mcp_alpha_one, mcp_alpha_two, mcp_alpha_three`) {
		t.Errorf("unexpected probe output:\n%s", out)
	}
}

func TestMCPTest_TruncatesToolList(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"many":{"command":"x"}}}`)
	specs := make([]tools.LoadedMCPTool, 12)
	for i := range specs {
		specs[i] = tools.LoadedMCPTool{Name: fmt.Sprintf("tool%02d", i)}
	}
	stubMCPLoadServer(t, specs, nil)

	out, code := runMCPSub(t, runMCPTest, "many")
	if code != 0 {
		t.Fatalf("test exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "12 tools") || !strings.Contains(out, "+2 more") {
		t.Errorf("tool list not truncated with a +N more tail:\n%s", out)
	}
	if strings.Contains(out, "tool11") {
		t.Errorf("11th tool should have been truncated:\n%s", out)
	}
}

func TestMCPTest_DisabledStillProbes(t *testing.T) {
	e := newMCPTestEnv(t)
	e.seedThreeLayers(t)
	got := stubMCPLoadServer(t, nil, nil)

	out, code := runMCPSub(t, runMCPTest, "bravo")
	if code != 0 {
		t.Fatalf("disabled test exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, `⊘ "bravo" is disabled (runtime will not load it); testing anyway...`) {
		t.Errorf("missing disabled warning:\n%s", out)
	}
	if *got != "bravo" {
		t.Errorf("probe called with %q, want bravo", *got)
	}
}

func TestMCPTest_InvalidRefusedWithoutDialing(t *testing.T) {
	e := newMCPTestEnv(t)
	e.seedThreeLayers(t)
	called := false
	old := mcpLoadServer
	mcpLoadServer = func(_ context.Context, _ mcp.Paths, _ string) ([]tools.LoadedMCPTool, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() { mcpLoadServer = old })

	out, code := runMCPSub(t, runMCPTest, "charlie")
	if code != 1 {
		t.Fatalf("invalid test exit = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, `✗ "charlie" is invalid:`) || !strings.Contains(out, "lele mcp edit charlie") {
		t.Errorf("missing invalid refusal / edit hint:\n%s", out)
	}
	if called {
		t.Errorf("invalid entry must not be dialed")
	}
}

func TestMCPTest_UnknownAndProbeFailure(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"alpha":{"command":"x"}}}`)

	out, code := runMCPSub(t, runMCPTest, "nosuch")
	if code != 1 || !strings.Contains(out, `✗ unknown MCP server "nosuch"`) {
		t.Fatalf("unknown test exit=%d out=%s", code, out)
	}

	stubMCPLoadServer(t, nil, fmt.Errorf("dial tcp: connection refused"))
	out, code = runMCPSub(t, runMCPTest, "alpha")
	if code != 1 {
		t.Fatalf("probe failure exit = %d, want 1", code)
	}
	if !strings.Contains(out, `✗ "alpha" failed: dial tcp: connection refused`) {
		t.Errorf("missing failure message:\n%s", out)
	}
}

func TestMCPTest_TimeoutParseError(t *testing.T) {
	newMCPTestEnv(t)

	out, code := runMCPSub(t, runMCPTest, "alpha", "--timeout", "not-a-duration")
	if code != 1 {
		t.Fatalf("bad timeout exit = %d, want 1", code)
	}
	if !strings.Contains(out, "invalid --timeout") || !strings.Contains(out, "Usage:") {
		t.Errorf("bad timeout should print a usage error:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// mcpToolNames unit checks
// ---------------------------------------------------------------------------

func TestMCPToolNames(t *testing.T) {
	if got := mcpToolNames(nil); got != nil {
		t.Errorf("empty specs = %v, want nil", got)
	}
	specs := []tools.LoadedMCPTool{{Name: "a"}, {Name: "b"}}
	if got := strings.Join(mcpToolNames(specs), ","); got != "a,b" {
		t.Errorf("names = %q", got)
	}
}
