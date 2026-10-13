package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/xilistudios/lele/pkg/mcp"
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

// startMCPHTTPServer starts a REAL MCP server behind an httptest HTTP server
// and returns the url to put in mcp.json. The streamable HTTP transport is the
// one ProbeServer builds for a url entry, so `lele mcp test` performs a real
// dial over TCP, a real initialize handshake and a real tools/list — nothing
// about the probe is mocked.
func startMCPHTTPServer(t *testing.T, toolNames ...string) string {
	t.Helper()
	srv := mcpserver.NewMCPServer("lele-cli-test", "9.9.9", mcpserver.WithToolCapabilities(false))
	for _, name := range toolNames {
		srv.AddTool(mcpgo.NewTool(name), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			return mcpgo.NewToolResultText(name), nil
		})
	}
	ts := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv))
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp"
}

// startCountingHTTPServer serves a plain handler that only counts requests. It
// exists to prove a NEGATIVE: a subcommand that must not dial (an invalid entry)
// leaves the counter at zero, which a mocked probe could never show.
func startCountingHTTPServer(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp", &hits
}

// closedMCPURL returns a url nothing listens on: an httptest server is started
// to reserve the port, then closed, which is how the dial-failure test gets a
// real "connection refused" from the production dialer.
func closedMCPURL(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := ts.URL + "/mcp"
	ts.Close()
	return url
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
	for _, sub := range []string{"list", "show", "add", "remove", "edit", "test"} {
		if !strings.Contains(out, "lele mcp "+sub) {
			t.Errorf("mcpHelp should document %q:\n%s", sub, out)
		}
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

// TestParseMCPFlags_EndOfFlags pins the "--" marker: everything after it is
// positional and the marker itself disappears, so a server whose name starts
// with a dash (validEntryName accepts it, the runtime honours it) is
// addressable.
func TestParseMCPFlags_EndOfFlags(t *testing.T) {
	pos, err := parseMCPFlags([]string{"plain", "--", "--not-a-flag", "-x"}, mcpParseSpec{})
	if err != nil {
		t.Fatalf("parseMCPFlags after --: %v", err)
	}
	if got := strings.Join(pos, "|"); got != "plain|--not-a-flag|-x" {
		t.Fatalf("positionals after -- = %q, want %q", got, "plain|--not-a-flag|-x")
	}

	a, err := parseMCPShowArgs([]string{"--", "-dash"})
	if err != nil {
		t.Fatalf("show -- -dash: %v", err)
	}
	if a.name != "-dash" {
		t.Fatalf("show name = %q, want -dash", a.name)
	}

	add, err := parseMCPAddArgs([]string{"--command", "/bin/echo", "--", "-dash"})
	if err != nil {
		t.Fatalf("add --command c -- -dash: %v", err)
	}
	if add.name != "-dash" || add.command != "/bin/echo" {
		t.Fatalf("add after -- = %+v, want name -dash and command /bin/echo", add)
	}

	// Addressable end to end: `show -- -dash` finds the server named "-dash".
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"-dash":{"command":"/bin/echo"}}}`)
	out, code := runMCPSub(t, runMCPShow, "--", "-dash")
	if code != 0 || !strings.Contains(out, `MCP server "-dash"`) {
		t.Fatalf("show -- -dash exit=%d out=%s", code, out)
	}
}

// TestMCPPairs_NeverEchoesValues pins the secrecy rule for malformed pairs: the
// KEY may be echoed, the text after '=' never is (it is the value), and a pair
// with no '=' is not echoed at all — its whole token could be a value
// ("--header 'Authorization Bearer hunter2'").
func TestMCPPairs_NeverEchoesValues(t *testing.T) {
	const secret = "hunter2"
	for _, pair := range []string{"=" + secret, secret, "Bearer " + secret} {
		_, err := mcpPairs([]string{pair})
		if err == nil {
			t.Fatalf("mcpPairs(%q) accepted, want an error", pair)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("mcpPairs(%q) error echoes the value: %v", pair, err)
		}
	}
	// The key of a malformed pair may still be named (it is not a secret).
	_, err := mcpPairs([]string{"KEY=" + secret + "=x"}) // valid: value is everything after the first '='
	if err != nil {
		t.Fatalf("mcpPairs with an '=' in the value: %v", err)
	}

	// Same rule through the command surface, which wraps the error.
	newMCPTestEnv(t)
	out, code := runMCPSub(t, runMCPAdd, "leaky", "--command", "/bin/echo", "--env", "="+secret)
	if code != 1 {
		t.Fatalf("add with an empty env key exit = %d, want 1:\n%s", code, out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("add echoed the env value:\n%s", out)
	}
	if !strings.Contains(out, "KEY must not be empty") {
		t.Errorf("missing the malformed-pair error:\n%s", out)
	}

	out, code = runMCPSub(t, runMCPAdd, "leaky", "--command", "/bin/echo", "--header", "Authorization Bearer "+secret)
	if code != 1 {
		t.Fatalf("add with a malformed header exit = %d, want 1:\n%s", code, out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("add echoed the header value:\n%s", out)
	}
	if !strings.Contains(out, "want KEY=VALUE") {
		t.Errorf("missing the malformed-pair error:\n%s", out)
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

// TestMCPList_NoWorkspaceSideEffect is the LOW-4 regression: the CLI must not
// initialize an agent workspace just to answer a read-only question. With no
// agents.defaults.workspace configured the workspace resolves to "" and the
// registry's InitializeWorkspace used to create <cwd>/memory (and the context
// files) for a mere `lele mcp list`.
func TestMCPList_NoWorkspaceSideEffect(t *testing.T) {
	root := t.TempDir()
	leleDir := filepath.Join(root, "lele")
	cwd := filepath.Join(root, "proj")
	for _, d := range []string{leleDir, cwd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	t.Chdir(cwd)
	// No workspace anywhere: the agent layer root is unconfigured.
	writeMCPTestFile(t, filepath.Join(leleDir, "config.json"), `{"agents":{"defaults":{}}}`)
	writeMCPTestFile(t, filepath.Join(leleDir, "mcp.json"),
		`{"mcpServers":{"filesystem":{"command":"npx","description":"d"}}}`)

	out, code := runMCPSub(t, runMCPList)
	if code != 0 {
		t.Fatalf("list exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "●  filesystem") {
		t.Errorf("list did not report the global server:\n%s", out)
	}
	if !strings.Contains(out, "(not configured)") {
		t.Errorf("the unconfigured agent layer should be reported as such:\n%s", out)
	}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatalf("read cwd: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("a read-only list created %v in the cwd", names)
	}
}

// TestMCPList_DisabledStubKind covers the documented kind column for a copy
// that defines nothing: a disable stub prints "unknown" (it has neither
// command nor url).
func TestMCPList_DisabledStubKind(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"filesystem":{"command":"npx","disabled":true}}}`)
	writeMCPTestFile(t, e.agentFile(), `{"mcpServers":{"filesystem":{"disabled":true}}}`)

	out, code := runMCPSub(t, runMCPList)
	if code != 0 {
		t.Fatalf("list exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "○  filesystem") || !strings.Contains(out, "unknown") {
		t.Errorf("a disable stub must print state ○ and kind unknown:\n%s", out)
	}
	if !strings.Contains(out, "1 server(s): 0 enabled, 1 disabled, 0 invalid") {
		t.Errorf("stub count wrong:\n%s", out)
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
		"kind:        stdio",
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

// TestMCPShow_OneVerdictSource pins the MED-3 rule: the STATE line follows the
// runtime's verdict (Effective, computed after ${VAR} expansion) while the
// per-layer breakdown still shows each copy's RAW verdict — clearly labelled,
// so an entry that is valid to the runtime is never announced as invalid (and
// vice versa).
func TestMCPShow_OneVerdictSource(t *testing.T) {
	e := newMCPTestEnv(t)
	t.Setenv("MCP_CLI_TEST_UNSET_URL", "") // expands to "", i.e. no url at all
	writeMCPTestFile(t, e.globalFile(),
		`{"mcpServers":{`+
			`"bothcmd":{"command":"/bin/echo","url":"${MCP_CLI_TEST_UNSET_URL}","description":"command plus an unset-var url"},`+
			`"urlonly":{"url":"${MCP_CLI_TEST_UNSET_URL}","description":"url built from an unset var"}}}`)

	// The raw text says "mixes stdio and remote fields", the runtime sees a
	// plain stdio entry: the state must follow the runtime.
	out, code := runMCPSub(t, runMCPShow, "bothcmd")
	if code != 0 {
		t.Fatalf("show bothcmd exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "state:       enabled") {
		t.Errorf("bothcmd state must follow Effective (enabled):\n%s", out)
	}
	if strings.Contains(out, "state:       invalid") {
		t.Errorf("bothcmd must not be reported invalid:\n%s", out)
	}
	if !strings.Contains(out, `raw verdict: "mixes stdio and remote fields`) {
		t.Errorf("the raw per-layer verdict must be labelled as such:\n%s", out)
	}

	// urlonly is the derived-invalid case: the raw bytes carry NO verdict, so
	// the reason must be the explicit fallback — never "invalid ()".
	out, code = runMCPSub(t, runMCPShow, "urlonly")
	if code != 0 {
		t.Fatalf("show urlonly exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "state:       invalid ("+mcpDerivedInvalidReason+")") {
		t.Errorf("urlonly needs the derived-invalid reason:\n%s", out)
	}
	if strings.Contains(out, "invalid ()") {
		t.Errorf("an empty invalid reason leaked into show:\n%s", out)
	}
	if strings.Contains(out, `raw verdict:`) {
		t.Errorf("urlonly has no raw verdict to show:\n%s", out)
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

// TestMCPRemove_ReenableWarning pins the MED side effect of the default remove:
// deleting the copy that kept a server OFF (a disable stub, or a disabled
// definition) hands the merge to a lower layer's definition, i.e. the server
// comes back ON — the user must be told.
func TestMCPRemove_ReenableWarning(t *testing.T) {
	tests := []struct {
		name  string
		agent string
	}{
		{name: "disable stub", agent: `{"mcpServers":{"filesystem":{"disabled":true}}}`},
		{name: "disabled definition", agent: `{"mcpServers":{"filesystem":{"command":"agent-npx","disabled":true}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMCPTestEnv(t)
			global := `{"mcpServers":{"filesystem":{"command":"npx","args":["-y"]}}}`
			writeMCPTestFile(t, e.globalFile(), global)
			writeMCPTestFile(t, e.agentFile(), tt.agent)

			// Fixture premise: the server is off and the agent copy is the
			// reason.
			if inv := mcp.ReadInventory(e.paths()); inv.Effective["filesystem"] != "disabled" ||
				inv.Winner["filesystem"] != "agent" {
				t.Fatalf("fixture premise broken: effective=%q winner=%q",
					inv.Effective["filesystem"], inv.Winner["filesystem"])
			}

			out, code := runMCPSub(t, runMCPRemove, "filesystem")
			if code != 0 {
				t.Fatalf("remove exit = %d, want 0:\n%s", code, out)
			}
			want := fmt.Sprintf("⚠ this re-enables %q from global (%s)", "filesystem", e.globalFile())
			if !strings.Contains(out, want) {
				t.Errorf("missing re-enable warning %q:\n%s", want, out)
			}
			// The existing note and hint stay.
			if !strings.Contains(out, fmt.Sprintf("⊘ note: %q is still defined in global", "filesystem")) {
				t.Errorf("missing shadow note:\n%s", out)
			}
			if !strings.Contains(out, "lele mcp remove filesystem --all") {
				t.Errorf("missing --all hint:\n%s", out)
			}
			if got := readMCPTestFile(t, e.globalFile()); got != global {
				t.Errorf("the lower definition must not be touched:\n got: %s\nwant: %s", got, global)
			}
			if inv := mcp.ReadInventory(e.paths()); inv.Effective["filesystem"] != "enabled" {
				t.Errorf("after the removal the server should be enabled, got %q", inv.Effective["filesystem"])
			}
		})
	}

	// The warning must NOT fire when the deleted copy was the live definition
	// (nothing is switched on by its removal), nor when every lower copy is a
	// stub too.
	t.Run("no warning without a lower definition", func(t *testing.T) {
		e := newMCPTestEnv(t)
		writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"bravo":{"command":"b","disabled":true}}}`)

		out, code := runMCPSub(t, runMCPRemove, "bravo")
		if code != 0 {
			t.Fatalf("remove exit = %d, want 0:\n%s", code, out)
		}
		if strings.Contains(out, "re-enables") {
			t.Errorf("removing the only copy must not warn about re-enabling:\n%s", out)
		}
	})

	t.Run("no warning for a live winner", func(t *testing.T) {
		e := newMCPTestEnv(t)
		writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"bravo":{"command":"b"}}}`)
		writeMCPTestFile(t, e.agentFile(), `{"mcpServers":{"bravo":{"command":"a"}}}`)

		out, code := runMCPSub(t, runMCPRemove, "bravo")
		if code != 0 {
			t.Fatalf("remove exit = %d, want 0:\n%s", code, out)
		}
		if strings.Contains(out, "re-enables") {
			t.Errorf("removing an enabled winner must not warn about re-enabling:\n%s", out)
		}
	})
}

// TestMCPRemove_PathGuardRefuses mirrors TestMCPAdd_PathGuardRefuses: the same
// predicate `add` and `edit` enforce (and the REST/TUI surfaces answer with 403
// mcp_path_not_allowed) must gate `remove` too, per row and including --all.
//
// The fixture root lives under /var/tmp: writable, but outside home, /tmp and
// the process cwd — exactly the shape the guard exists for.
func TestMCPRemove_PathGuardRefuses(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "lele-mcp-remove-guard-")
	if err != nil {
		t.Skipf("cannot create a workspace outside the allowed roots: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	leleDir := filepath.Join(root, "lele")
	agentWS := filepath.Join(root, "ws") // not under home, /tmp or cwd
	cwd := filepath.Join(root, "proj")
	for _, d := range []string{leleDir, agentWS, cwd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	t.Chdir(cwd)
	writeMCPConfigFile(t, leleDir, agentWS)
	agentFile := filepath.Join(agentWS, "mcp.json")
	content := `{"mcpServers":{"victim":{"command":"v"}}}`
	writeMCPTestFile(t, agentFile, content)

	for _, args := range [][]string{
		{"victim"},
		{"victim", "--layer", "agent"},
		{"victim", "--all"},
	} {
		out, code := runMCPSub(t, runMCPRemove, args...)
		if code != 1 {
			t.Fatalf("guarded %v exit = %d, want 1:\n%s", args, code, out)
		}
		if !strings.Contains(out, "refusing to write") {
			t.Errorf("guarded %v missing the guard message:\n%s", args, out)
		}
		if got := readMCPTestFile(t, agentFile); got != content {
			t.Errorf("guarded %v changed the file:\n got: %s\nwant: %s", args, got, content)
		}
	}
}

// ---------------------------------------------------------------------------
// test
// ---------------------------------------------------------------------------

// The probe is exercised at the REAL seam: `lele mcp test` resolves the entry's
// expanded config and dials it through mcp.ProbeServer, so these tests point a
// url entry at a real in-process MCP server (streamable HTTP). No probe seam
// exists any more: the only thing tests may mock is the world, never the code
// under test.

func TestMCPTest_HappyPath(t *testing.T) {
	e := newMCPTestEnv(t)
	url := startMCPHTTPServer(t, "mcp_alpha_one", "mcp_alpha_two", "mcp_alpha_three")
	writeMCPTestFile(t, e.globalFile(),
		fmt.Sprintf(`{"mcpServers":{"alpha":{"url":%q,"type":"http","description":"d"}}}`, url))

	out, code := runMCPSub(t, runMCPTest, "alpha")
	if code != 0 {
		t.Fatalf("test exit = %d, want 0:\n%s", code, out)
	}
	// Tool names keep the SERVER's order (ProbeServer never sorts them), so
	// the assertion is on the shape plus the set.
	line := firstLineWith(out, "connected")
	if !strings.Contains(line, `✓ "alpha" connected — 3 tools: `) {
		t.Fatalf("unexpected probe output: %q", line)
	}
	for _, name := range []string{"mcp_alpha_one", "mcp_alpha_two", "mcp_alpha_three"} {
		if !strings.Contains(line, name) {
			t.Errorf("probe output misses %q: %q", name, line)
		}
	}
	// The handshake identity is reported when the server sent one.
	if !strings.Contains(line, "(server: lele-cli-test 9.9.9)") {
		t.Errorf("missing the server identity from the handshake: %q", line)
	}
}

// firstLineWith returns the first line of out containing want ("" when there
// is none), so output assertions cannot be broken by unrelated lines.
func firstLineWith(out, want string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

func TestMCPTest_TruncatesToolList(t *testing.T) {
	e := newMCPTestEnv(t)
	names := make([]string, 12)
	for i := range names {
		names[i] = fmt.Sprintf("tool%02d", i)
	}
	url := startMCPHTTPServer(t, names...)
	writeMCPTestFile(t, e.globalFile(), fmt.Sprintf(`{"mcpServers":{"many":{"url":%q,"type":"http"}}}`, url))

	out, code := runMCPSub(t, runMCPTest, "many")
	if code != 0 {
		t.Fatalf("test exit = %d:\n%s", code, out)
	}
	line := firstLineWith(out, "connected")
	if !strings.Contains(line, "12 tools: ") {
		t.Fatalf("unexpected probe output: %q", line)
	}
	list := line[strings.Index(line, "12 tools: ")+len("12 tools: "):]
	list = strings.TrimSuffix(list, " (server: lele-cli-test 9.9.9)")
	parts := strings.Split(list, ", ")
	if len(parts) != mcpToolListLimit+1 { // 10 names + the tail
		t.Fatalf("printed %d entries (%v), want %d names plus the +N more tail",
			len(parts), parts, mcpToolListLimit)
	}
	if parts[len(parts)-1] != "+2 more" {
		t.Errorf("tail = %q, want %q", parts[len(parts)-1], "+2 more")
	}
	for i, name := range parts[:mcpToolListLimit] {
		if !mcpContains(names, name) {
			t.Errorf("entry %d = %q, not a reported tool name", i, name)
		}
	}
}

// TestMCPTest_DisabledProbesDefinition is THE regression test for HIGH-1: the
// note must be followed by a REAL probe, and the probed config is the highest
// copy that is a real definition — a {"disabled": true} stub in a higher layer
// shadows the merge but defines nothing, so the server that would run if the
// stub were removed is the global one. Before the fix the command sent this to
// Manager.LoadServer, which refuses disabled servers: the note lied and the
// probe could never succeed.
func TestMCPTest_DisabledProbesDefinition(t *testing.T) {
	e := newMCPTestEnv(t)
	url := startMCPHTTPServer(t, "echo")
	writeMCPTestFile(t, e.globalFile(),
		fmt.Sprintf(`{"mcpServers":{"off":{"url":%q,"type":"http","description":"d"}}}`, url))
	writeMCPTestFile(t, e.agentFile(), `{"mcpServers":{"off":{"disabled":true}}}`)

	inv := mcp.ReadInventory(e.paths())
	if inv.Effective["off"] != "disabled" || inv.Winner["off"] != "agent" {
		t.Fatalf("fixture premise broken: effective=%q winner=%q",
			inv.Effective["off"], inv.Winner["off"])
	}

	out, code := runMCPSub(t, runMCPTest, "off")
	if code != 0 {
		t.Fatalf("disabled test exit = %d, want 0:\n%s", code, out)
	}
	want := fmt.Sprintf("⊘ %q is disabled in the %s layer — probing it directly (the runtime will not load it)",
		"off", "agent")
	if !strings.Contains(out, want) {
		t.Errorf("missing disabled note %q:\n%s", want, out)
	}
	if !strings.Contains(out, `✓ "off" connected — 1 tools: echo`) {
		t.Errorf("the disabled server was not actually probed:\n%s", out)
	}
}

// TestMCPTest_DisabledWinnerDefines: the disabled copy is itself the definition
// (nothing is shadowed) — the probe must still happen.
func TestMCPTest_DisabledWinnerDefines(t *testing.T) {
	e := newMCPTestEnv(t)
	url := startMCPHTTPServer(t, "echo")
	writeMCPTestFile(t, e.globalFile(),
		fmt.Sprintf(`{"mcpServers":{"off":{"url":%q,"type":"http","disabled":true}}}`, url))

	out, code := runMCPSub(t, runMCPTest, "off")
	if code != 0 {
		t.Fatalf("disabled test exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, `⊘ "off" is disabled in the global layer`) {
		t.Errorf("missing disabled note:\n%s", out)
	}
	if !strings.Contains(out, `✓ "off" connected — 1 tools: echo`) {
		t.Errorf("the disabled server was not actually probed:\n%s", out)
	}
}

// TestMCPTest_OnlyDisableStub: every copy is a stub, so there is no definition
// to dial — the command must say exactly that instead of pretending to probe.
func TestMCPTest_OnlyDisableStub(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.agentFile(), `{"mcpServers":{"stub":{"disabled":true}}}`)

	out, code := runMCPSub(t, runMCPTest, "stub")
	if code != 1 {
		t.Fatalf("stub-only test exit = %d, want 1:\n%s", code, out)
	}
	want := fmt.Sprintf(`✗ "stub" is only a disable stub (agent %s) — no definition to probe`, e.agentFile())
	if !strings.Contains(out, want) {
		t.Errorf("missing stub-only refusal %q:\n%s", want, out)
	}
}

// TestMCPTest_InvalidRefusedWithoutDialing: the refusal is the runtime's
// verdict (Effective), the reason is never empty, and nothing is dialed — the
// counting HTTP server behind the entry proves the negative.
func TestMCPTest_InvalidRefusedWithoutDialing(t *testing.T) {
	e := newMCPTestEnv(t)
	e.seedThreeLayers(t)
	url, hits := startCountingHTTPServer(t)
	writeMCPTestFile(t, e.projectFile(),
		fmt.Sprintf(`{"mcpServers":{"charlie":{"command":"charlie","url":%q,"description":"see: invalid"}}}`, url))

	out, code := runMCPSub(t, runMCPTest, "charlie")
	if code != 1 {
		t.Fatalf("invalid test exit = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, `✗ "charlie" is invalid:`) || !strings.Contains(out, "lele mcp edit charlie") {
		t.Errorf("missing invalid refusal / edit hint:\n%s", out)
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("the invalid entry was dialed %d time(s), want 0", got)
	}
}

// TestMCPTest_DerivedInvalidReason: an entry that is invalid only because its
// ${VAR} URL expanded to nothing has no raw verdict, so the refusal must carry
// the fallback reason — never an empty "invalid ()".
func TestMCPTest_DerivedInvalidReason(t *testing.T) {
	e := newMCPTestEnv(t)
	t.Setenv("MCP_CLI_TEST_UNSET_URL", "")
	writeMCPTestFile(t, e.globalFile(),
		`{"mcpServers":{"urlonly":{"url":"${MCP_CLI_TEST_UNSET_URL}","description":"d"}}}`)

	out, code := runMCPSub(t, runMCPTest, "urlonly")
	if code != 1 {
		t.Fatalf("derived-invalid test exit = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "is invalid: "+mcpDerivedInvalidReason) {
		t.Errorf("missing the derived-invalid reason:\n%s", out)
	}
	if strings.Contains(out, "invalid ()") {
		t.Errorf("an empty invalid reason leaked into test:\n%s", out)
	}
}

func TestMCPTest_UnknownAndProbeFailure(t *testing.T) {
	e := newMCPTestEnv(t)

	out, code := runMCPSub(t, runMCPTest, "nosuch")
	if code != 1 || !strings.Contains(out, `✗ unknown MCP server "nosuch"`) {
		t.Fatalf("unknown test exit=%d out=%s", code, out)
	}

	// A real dial failure: the production dialer gets a connection refused
	// from a port nothing listens on.
	url := closedMCPURL(t)
	writeMCPTestFile(t, e.globalFile(), fmt.Sprintf(`{"mcpServers":{"alpha":{"url":%q,"type":"http"}}}`, url))
	out, code = runMCPSub(t, runMCPTest, "alpha")
	if code != 1 {
		t.Fatalf("probe failure exit = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, `✗ "alpha" failed: mcp probe:`) {
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
		t.Errorf("empty tools = %v, want nil", got)
	}
	if got := strings.Join(mcpToolNames([]string{"a", "b"}), ","); got != "a,b" {
		t.Errorf("names = %q", got)
	}
	if got := mcpToolNames([]string{"a", "b", "c", "d"})[3]; got != "d" {
		t.Errorf("short lists must not be truncated: %q", got)
	}
}

// ---------------------------------------------------------------------------
// edit
// ---------------------------------------------------------------------------

// setMCPEditor points the editor lookup at cmd for one test. $VISUAL is
// cleared because it takes precedence over $EDITOR in mcpEditorCommand.
func setMCPEditor(t *testing.T, cmd string) {
	t.Helper()
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", cmd)
}

// writeMCPEditorScript writes a fake editor to dir and returns its path. The
// file to edit is the script's LAST argument (mirroring a real editor such as
// `EDITOR="code -w"`, whose own flags come first); body sees it as "$target".
func writeMCPEditorScript(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-editor.sh")
	script := "#!/bin/sh\n" +
		"target=\"\"\n" +
		"for a in \"$@\"; do target=\"$a\"; done\n" +
		body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}
	return path
}

// writeMCPEditorFixture stores the bytes a fake editor should install and
// returns a script that copies them over the file it was handed.
func writeMCPEditorFixture(t *testing.T, dir, content string) string {
	t.Helper()
	fixture := filepath.Join(dir, "edited.json")
	if err := os.WriteFile(fixture, []byte(content), 0o600); err != nil {
		t.Fatalf("write edited fixture: %v", err)
	}
	return writeMCPEditorScript(t, dir, "cp '"+fixture+"' \"$target\"")
}

// mcpTestPaths returns the layer roots the test env wired, without going
// through the config/registry machinery runMCPEdit uses.
func (e *mcpTestEnv) paths() mcp.Paths {
	return mcp.Paths{LeleDir: e.leleDir, AgentWorkspace: e.agentWS, Cwd: e.cwd}
}

func TestParseMCPEditArgs(t *testing.T) {
	a, err := parseMCPEditArgs([]string{"name"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.name != "name" || a.agent != "main" || a.layer != "" {
		t.Fatalf("defaults wrong: %+v", a)
	}
	a, err = parseMCPEditArgs([]string{"--layer", "project", "--agent", "worker", "name"})
	if err != nil || a.layer != "project" || a.agent != "worker" {
		t.Fatalf("parse: %+v %v", a, err)
	}
	if _, err := parseMCPEditArgs(nil); err == nil || !strings.Contains(err.Error(), "missing server name") {
		t.Fatalf("missing name err = %v", err)
	}
	if _, err := parseMCPEditArgs([]string{"a", "b"}); err == nil {
		t.Fatal("extra positional accepted")
	}
	if _, err := parseMCPEditArgs([]string{"--layer", "nope", "a"}); err == nil {
		t.Fatal("bad layer accepted")
	}
	if _, err := parseMCPEditArgs([]string{"a/b"}); err == nil {
		t.Fatal("slash name accepted")
	}
	if _, err := parseMCPEditArgs([]string{"--bogus", "a"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

// TestMCPEdit_NoChanges pins the "editor touched nothing" path: the note is
// printed, the exit code is 0, and not one byte of the layer file moves.
func TestMCPEdit_NoChanges(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"alpha":{"command":"global-alpha"}}}`)
	before := readMCPTestFile(t, e.globalFile())

	setMCPEditor(t, writeMCPEditorScript(t, t.TempDir(), "exit 0"))

	out, code := runMCPSub(t, runMCPEdit, "alpha")
	if code != 0 {
		t.Fatalf("edit exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "✓ no changes") {
		t.Errorf("missing no-changes line:\n%s", out)
	}
	want := fmt.Sprintf("Editing %q (global %s)", "alpha", e.globalFile())
	if !strings.Contains(out, want) {
		t.Errorf("missing pre-edit note %q:\n%s", want, out)
	}
	if !strings.Contains(out, "JSON object for this server") || !strings.Contains(out, "${VAR} is expanded at connect time") {
		t.Errorf("pre-edit note must explain the object shape and raw values:\n%s", out)
	}
	if got := readMCPTestFile(t, e.globalFile()); got != before {
		t.Errorf("file changed by a no-op edit:\n got: %s\nwant: %s", got, before)
	}
}

// TestMCPEdit_SavesCanonicalizedEntry pins the happy path: the edited object
// is stored through UpsertEntry's canonical form, only that member's value
// span changes, and the report names the layer and file.
func TestMCPEdit_SavesCanonicalizedEntry(t *testing.T) {
	e := newMCPTestEnv(t)
	in := "{\"mcpServers\":{\n  \"alpha\": {\"command\":\"old\",\"description\":\"d\"}\n}}"
	writeMCPTestFile(t, e.globalFile(), in)

	dir := t.TempDir()
	setMCPEditor(t, writeMCPEditorFixture(t, dir, `{"command":"new-bin","args":["-x"]}`))

	out, code := runMCPSub(t, runMCPEdit, "alpha")
	if code != 0 {
		t.Fatalf("edit exit = %d, want 0:\n%s", code, out)
	}
	want := fmt.Sprintf("✓ updated %q (global %s)", "alpha", e.globalFile())
	if !strings.Contains(out, want) {
		t.Errorf("missing update line %q:\n%s", want, out)
	}
	// Only the entry's value bytes are rewritten (the odd whitespace around
	// them survives: the splice writer's contract).
	wantFile := strings.Replace(in, `{"command":"old","description":"d"}`, `{"command":"new-bin","args":["-x"]}`, 1)
	if got := readMCPTestFile(t, e.globalFile()); got != wantFile {
		t.Errorf("file bytes:\n got: %s\nwant: %s", got, wantFile)
	}
	f, err := mcp.ParseFile(e.globalFile())
	if err != nil {
		t.Fatalf("ParseFile(result) error = %v", err)
	}
	if got := f.MCPServers["alpha"].Command; got != "new-bin" {
		t.Errorf("saved command = %q, want new-bin", got)
	}
}

// TestMCPEdit_InvalidEditLeavesFileUntouched: a rejected edit must print a
// repair hint and not move a byte.
func TestMCPEdit_InvalidEditLeavesFileUntouched(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "command and url",
			content: `{"command":"x","url":"http://y"}`,
			wantErr: "mixes stdio and remote",
		},
		{
			name:    "neither command nor url",
			content: `{"description":"d"}`,
			wantErr: "needs command",
		},
		{
			name:    "not a JSON object",
			content: `"nope"`,
			wantErr: "cannot unmarshal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMCPTestEnv(t)
			base := `{"mcpServers":{"alpha":{"command":"global-alpha"}}}`
			writeMCPTestFile(t, e.globalFile(), base)

			setMCPEditor(t, writeMCPEditorFixture(t, t.TempDir(), tt.content))

			out, code := runMCPSub(t, runMCPEdit, "alpha")
			if code != 1 {
				t.Fatalf("invalid edit exit = %d, want 1:\n%s", code, out)
			}
			if !strings.Contains(out, "✗ invalid entry:") || !strings.Contains(out, tt.wantErr) {
				t.Errorf("error text = %q, want it to mention %q", out, tt.wantErr)
			}
			if !strings.Contains(out, "file left unchanged") {
				t.Errorf("missing 'file left unchanged':\n%s", out)
			}
			if !strings.Contains(out, "re-run: lele mcp edit alpha") {
				t.Errorf("missing re-run hint:\n%s", out)
			}
			if got := readMCPTestFile(t, e.globalFile()); got != base {
				t.Errorf("a rejected edit touched the file:\n got: %s\nwant: %s", got, base)
			}
		})
	}
}

// TestMCPEdit_RepairsInvalidEntry is the documented flow: `lele mcp test`
// sends the user here, the invalid copy is the edit target, and a valid
// replacement flips the entry back to enabled.
func TestMCPEdit_RepairsInvalidEntry(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.projectFile(), `{"mcpServers":{"charlie":{"command":"charlie","url":"http://charlie"}}}`)

	before := mcp.ReadInventory(e.paths())
	if got := before.Effective["charlie"]; got != "invalid" {
		t.Fatalf("fixture premise broken: Effective[charlie] = %q, want invalid", got)
	}
	if got := before.Winner["charlie"]; got != "project" {
		t.Fatalf("fixture premise broken: Winner[charlie] = %q, want project", got)
	}

	setMCPEditor(t, writeMCPEditorFixture(t, t.TempDir(), `{"command":"charlie-fixed"}`))

	out, code := runMCPSub(t, runMCPEdit, "charlie")
	if code != 0 {
		t.Fatalf("repair exit = %d, want 0:\n%s", code, out)
	}
	for _, want := range []string{
		fmt.Sprintf("✓ updated %q (project %s)", "charlie", e.projectFile()),
		`✓ "charlie" is now valid`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("repair output missing %q:\n%s", want, out)
		}
	}
	after := mcp.ReadInventory(e.paths())
	if got := after.Effective["charlie"]; got != "enabled" {
		t.Errorf("Effective[charlie] = %q, want enabled", got)
	}
	if row, ok := mcpProjectRow(after, "charlie"); !ok || row.Invalid != "" {
		t.Errorf("project row after repair = %+v (ok=%v), want a valid copy", row, ok)
	}
}

// mcpProjectRow returns the project layer's copy of name from an inventory.
func mcpProjectRow(inv mcp.Inventory, name string) (mcp.EntryState, bool) {
	for _, row := range inv.ByName[name] {
		if row.Layer == "project" {
			return row, true
		}
	}
	return mcp.EntryState{}, false
}

// TestMCPEdit_TargetsWinningStubCopy pins the default edit target against
// mutation M8 (drop the Winner preference): with a {"disabled": true} stub in
// the agent layer shadowing a real global definition, the WINNER is the stub —
// edit must hand the stub's raw bytes to the editor and report the agent layer,
// NOT fall back to the "highest defining copy".
func TestMCPEdit_TargetsWinningStubCopy(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"alpha":{"command":"global-alpha"}}}`)
	writeMCPTestFile(t, e.agentFile(), `{"mcpServers":{"alpha":{"disabled":true}}}`)

	inv := mcp.ReadInventory(e.paths())
	if inv.Winner["alpha"] != "agent" {
		t.Fatalf("fixture premise broken: Winner[alpha] = %q, want agent", inv.Winner["alpha"])
	}

	dir := t.TempDir()
	captured := filepath.Join(dir, "captured.json")
	setMCPEditor(t, writeMCPEditorScript(t, dir, `cp "$target" '`+captured+`'`))

	out, code := runMCPSub(t, runMCPEdit, "alpha")
	if code != 0 {
		t.Fatalf("edit exit = %d, want 0:\n%s", code, out)
	}
	want := fmt.Sprintf("Editing %q (agent %s)", "alpha", e.agentFile())
	if !strings.Contains(out, want) {
		t.Errorf("edit must report the winning (agent) copy %q:\n%s", want, out)
	}
	if !strings.Contains(out, "✓ no changes") {
		t.Errorf("an untouched stub must report no changes:\n%s", out)
	}
	// The object handed to the editor is the STUB, byte for byte.
	if got := readMCPTestFile(t, captured); got != `{"disabled":true}` {
		t.Errorf("editor received %q, want the stub's raw bytes", got)
	}
	if got := readMCPTestFile(t, e.globalFile()); !strings.Contains(got, `"command":"global-alpha"`) {
		t.Errorf("the shadowed global copy must not be touched:\n%s", got)
	}
}

// TestMCPEdit_RepairsDerivedInvalidEntry is the LOW-1 regression: an entry that
// is invalid only because its ${VAR} expanded to nothing has NO raw verdict, so
// the repair confirmation must key on the EFFECTIVE state (before and after)
// instead of the raw text.
func TestMCPEdit_RepairsDerivedInvalidEntry(t *testing.T) {
	e := newMCPTestEnv(t)
	t.Setenv("MCP_CLI_TEST_UNSET_URL", "")
	writeMCPTestFile(t, e.globalFile(),
		`{"mcpServers":{"urlonly":{"url":"${MCP_CLI_TEST_UNSET_URL}","description":"d"}}}`)

	before := mcp.ReadInventory(e.paths())
	if got := before.Effective["urlonly"]; got != "invalid" {
		t.Fatalf("fixture premise broken: Effective[urlonly] = %q, want invalid", got)
	}
	if row, ok := mcpFindRow(before.ByName["urlonly"], before.Winner["urlonly"]); !ok || row.Invalid != "" {
		t.Fatalf("fixture premise broken: the raw verdict must be empty (row=%+v ok=%v)", row, ok)
	}

	setMCPEditor(t, writeMCPEditorFixture(t, t.TempDir(), `{"command":"urlonly-fixed"}`))

	out, code := runMCPSub(t, runMCPEdit, "urlonly")
	if code != 0 {
		t.Fatalf("repair exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, `✓ "urlonly" is now valid`) {
		t.Errorf("a derived-invalid repair must be reported:\n%s", out)
	}
	if after := mcp.ReadInventory(e.paths()); after.Effective["urlonly"] == "invalid" {
		t.Errorf("Effective[urlonly] = %q after the repair, want a valid state", after.Effective["urlonly"])
	}
}

// TestMCPEdit_UnknownName: an unknown name lists suggestions and never
// invokes the editor.
func TestMCPEdit_UnknownName(t *testing.T) {
	e := newMCPTestEnv(t)
	e.seedThreeLayers(t)

	marker := filepath.Join(t.TempDir(), "editor-ran")
	setMCPEditor(t, writeMCPEditorScript(t, t.TempDir(), "touch '"+marker+"'"))

	out, code := runMCPSub(t, runMCPEdit, "alp")
	if code != 1 {
		t.Fatalf("unknown edit exit = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, `✗ unknown MCP server "alp"`) {
		t.Errorf("missing unknown line:\n%s", out)
	}
	if !strings.Contains(out, "did you mean: alpha?") {
		t.Errorf("missing suggestion:\n%s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the editor ran for an unknown server")
	}
}

// TestMCPEdit_LayerTargetsShadowedCopy: --layer edits THAT file, leaving the
// winner alone, and the shadow note warns why the edit is inert.
func TestMCPEdit_LayerTargetsShadowedCopy(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"alpha":{"command":"global-alpha"}}}`)
	writeMCPTestFile(t, e.agentFile(), `{"mcpServers":{"alpha":{"command":"agent-alpha"}}}`)
	agentBefore := readMCPTestFile(t, e.agentFile())

	setMCPEditor(t, writeMCPEditorFixture(t, t.TempDir(), `{"command":"global-edited"}`))

	out, code := runMCPSub(t, runMCPEdit, "alpha", "--layer", "global")
	if code != 0 {
		t.Fatalf("edit --layer exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, fmt.Sprintf("✓ updated %q (global %s)", "alpha", e.globalFile())) {
		t.Errorf("edit --layer did not report the global file:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("⊘ note: %q in agent shadows this entry", "alpha")) {
		t.Errorf("missing shadow note:\n%s", out)
	}
	if !strings.Contains(readMCPTestFile(t, e.globalFile()), `"command":"global-edited"`) {
		t.Errorf("global copy not edited:\n%s", readMCPTestFile(t, e.globalFile()))
	}
	if got := readMCPTestFile(t, e.agentFile()); got != agentBefore {
		t.Errorf("the winner (agent) must not be touched by --layer global:\n got: %s\nwant: %s", got, agentBefore)
	}

	// A layer that does not hold a copy is refused before the editor runs.
	out, code = runMCPSub(t, runMCPEdit, "alpha", "--layer", "project")
	if code != 1 || !strings.Contains(out, `✗ "alpha" is not defined in project`) {
		t.Fatalf("--layer project exit=%d out=%s", code, out)
	}
}

// TestMCPEdit_EditorFailureAborts: a non-zero editor (or a missing binary)
// aborts the edit and leaves the file untouched.
func TestMCPEdit_EditorFailureAborts(t *testing.T) {
	tests := []struct {
		name string
		cmd  func(t *testing.T) string
		want string
	}{
		{
			name: "non-zero exit",
			cmd:  func(t *testing.T) string { return writeMCPEditorScript(t, t.TempDir(), "exit 3") },
			want: "✗ edit aborted (editor exited with status 3)",
		},
		{
			name: "missing binary",
			cmd:  func(t *testing.T) string { return "lele-no-such-editor-2f9c" },
			want: "✗ edit aborted (editor not found)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMCPTestEnv(t)
			base := `{"mcpServers":{"alpha":{"command":"global-alpha"}}}`
			writeMCPTestFile(t, e.globalFile(), base)
			setMCPEditor(t, tt.cmd(t))

			out, code := runMCPSub(t, runMCPEdit, "alpha")
			if code != 1 {
				t.Fatalf("aborted edit exit = %d, want 1:\n%s", code, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("want %q in:\n%s", tt.want, out)
			}
			if got := readMCPTestFile(t, e.globalFile()); got != base {
				t.Errorf("an aborted edit touched the file:\n got: %s\nwant: %s", got, base)
			}
		})
	}
}

// TestMCPEdit_DisabledRoundTrips pins that ServerConfig.Disabled survives (or
// not) exactly as the editor wrote it: keep it → the copy stays off; drop it
// → the copy comes back on.
func TestMCPEdit_DisabledRoundTrips(t *testing.T) {
	tests := []struct {
		name      string
		edited    string
		wantRaw   string
		wantState string
	}{
		{
			name:      "kept disabled",
			edited:    `{"command":"y","disabled":true}`,
			wantRaw:   `{"command":"y","disabled":true}`,
			wantState: "disabled",
		},
		{
			name:      "dropped disabled re-enables",
			edited:    `{"command":"y"}`,
			wantRaw:   `{"command":"y"}`,
			wantState: "enabled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMCPTestEnv(t)
			writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"alpha":{"command":"x","disabled":true}}}`)

			setMCPEditor(t, writeMCPEditorFixture(t, t.TempDir(), tt.edited))

			out, code := runMCPSub(t, runMCPEdit, "alpha")
			if code != 0 {
				t.Fatalf("edit exit = %d, want 0:\n%s", code, out)
			}
			got := readMCPTestFile(t, e.globalFile())
			want := `{"mcpServers":{"alpha":` + tt.wantRaw + `}}`
			if got != want {
				t.Errorf("file bytes:\n got: %s\nwant: %s", got, want)
			}
			if inv := mcp.ReadInventory(e.paths()); inv.Effective["alpha"] != tt.wantState {
				t.Errorf("Effective[alpha] = %q, want %q", inv.Effective["alpha"], tt.wantState)
			}
			// A disabled copy is valid: the repair line must not appear.
			if strings.Contains(out, "is now valid") {
				t.Errorf("a disabled entry must not be reported as repaired:\n%s", out)
			}
		})
	}
}

// TestMCPEdit_EditorWithArgs pins the sh -c indirection: an editor command
// carrying its own flags ("code -w"-style) still receives the temp file.
func TestMCPEdit_EditorWithArgs(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"alpha":{"command":"global-alpha"}}}`)

	dir := t.TempDir()
	script := writeMCPEditorFixture(t, dir, `{"command":"from-args-editor"}`)
	setMCPEditor(t, script+" -w")

	out, code := runMCPSub(t, runMCPEdit, "alpha")
	if code != 0 {
		t.Fatalf("edit with editor args exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(readMCPTestFile(t, e.globalFile()), `"command":"from-args-editor"`) {
		t.Errorf("editor with flags did not edit the file:\n%s", readMCPTestFile(t, e.globalFile()))
	}
}

// TestMCPEdit_PathGuardRefusesBeforeEditor pins requirement 7: a layer file
// outside the allowed workspace roots is refused BEFORE the editor is
// launched — the fake editor never gets a chance to run.
//
// The fixture root lives under /var/tmp (writable, but outside home, /tmp and
// the process cwd), which is exactly the shape the guard exists for.
func TestMCPEdit_PathGuardRefusesBeforeEditor(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "lele-mcp-edit-guard-")
	if err != nil {
		t.Skipf("cannot create a workspace outside the allowed roots: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	leleDir := filepath.Join(root, "lele")
	agentWS := filepath.Join(root, "ws") // not under home, /tmp or cwd
	cwd := filepath.Join(root, "proj")
	for _, d := range []string{leleDir, agentWS, cwd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	t.Chdir(cwd)
	writeMCPConfigFile(t, leleDir, agentWS)
	writeMCPTestFile(t, filepath.Join(agentWS, "mcp.json"), `{"mcpServers":{"alpha":{"command":"x"}}}`)

	marker := filepath.Join(root, "editor-ran")
	setMCPEditor(t, writeMCPEditorScript(t, root, "touch '"+marker+"'"))

	out, code := runMCPSub(t, runMCPEdit, "alpha")
	if code != 1 {
		t.Fatalf("guarded edit exit = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "refusing to write") {
		t.Errorf("missing guard message:\n%s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the editor ran despite the path guard")
	}
	if !strings.Contains(readMCPTestFile(t, filepath.Join(agentWS, "mcp.json")), `"command":"x"`) {
		t.Error("the guarded file was modified")
	}
}

// ---------------------------------------------------------------------------
// docs guard
// ---------------------------------------------------------------------------

// TestMCPDocs_UsageCoverage is the guard the review found missing (N1/LOW-5):
// nothing tied the documentation to the command surface, so `edit` was missing
// from the parent help and the samples were hand-written. It checks coverage,
// not formatting: every subcommand the dispatcher knows must be shown as
// `lele mcp <sub>` in docs/mcp.md and named in both the CLI help and the
// parent help, and no line of the docs may advertise an unknown subcommand.
func TestMCPDocs_UsageCoverage(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "mcp.md"))
	if err != nil {
		t.Fatalf("read docs/mcp.md: %v", err)
	}

	documented := map[string]bool{}
	for _, line := range strings.Split(string(doc), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "lele mcp ")
		if !ok {
			continue
		}
		sub, _, _ := strings.Cut(rest, " ")
		documented[sub] = true
	}
	for sub := range mcpUsage {
		if !documented[sub] {
			t.Errorf("docs/mcp.md never shows `lele mcp %s`", sub)
		}
	}
	for sub := range documented {
		if _, ok := mcpUsage[sub]; !ok {
			t.Errorf("docs/mcp.md advertises `lele mcp %s`, which the dispatcher does not know", sub)
		}
	}

	help, _ := runMCPSub(t, func(w io.Writer, _ []string) int { writeMCPHelp(w); return 0 })
	for sub := range mcpUsage {
		if !strings.Contains(help, "lele mcp "+sub+" ") {
			t.Errorf("writeMCPHelp does not document `lele mcp %s`", sub)
		}
	}
	if !strings.Contains(help, "lele mcp test <name>") {
		t.Errorf("writeMCPHelp must show the test usage line:\n%s", help)
	}

	// The parent `lele help` line must name every subcommand (LOW-5: it used to
	// omit `edit`).
	parent := captureOutput(printHelp)
	line := ""
	for _, l := range strings.Split(parent, "\n") {
		if strings.Contains(l, "Manage MCP servers") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("printHelp has no MCP line:\n%s", parent)
	}
	for sub := range mcpUsage {
		if !strings.Contains(line, sub) {
			t.Errorf("the parent help line omits %q: %q", sub, line)
		}
	}
}
