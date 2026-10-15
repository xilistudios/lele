package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/xilistudios/lele/pkg/auth"
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
	// The OAuth surface a headless operator reads first: the two markers the
	// list row speaks, the one paste rule (the shared engine helper, not a
	// private copy) and the escape hatch of a prompt that keeps asking.
	for _, want := range []string{
		mcpAuthMarkerRequired,
		mcpAuthMarkerAuthenticated,
		"pkg/mcp.ParseAuthPaste",
		`:q"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mcpHelp should mention %q:\n%s", want, out)
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

// TestMCPList_AuthMarkers pins the C2 (CLI half) rule for `list`: a row's auth
// verdict is a TRAILING marker in the SAME bracket vocabulary as the TUI, and
// it flips from [needs auth] to [authenticated] exactly when a credential is
// stored for (name, url). A server that needs no login gets no marker at all,
// so a plain row's shape is unchanged by this feature.
func TestMCPList_AuthMarkers(t *testing.T) {
	e := newMCPTestEnv(t)
	url := "https://mcp.example.com/mcp"
	writeMCPTestFile(t, e.globalFile(), fmt.Sprintf(
		`{"mcpServers":{`+
			`"alpha":{"url":%q,"type":"http","auth":{"type":"oauth"}},`+
			`"plain":{"command":"/bin/echo"}}}`, url))

	// rowOf returns the ONE `list` row for a server, so the assertions are on
	// the row and not on the whole page (a marker anywhere else would be a bug
	// this must catch).
	rowOf := func(out, name string) string {
		t.Helper()
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimLeft(l, "●○!⊘ "), name+" ") {
				return l
			}
		}
		t.Fatalf("no `list` row for %q:\n%s", name, out)
		return ""
	}

	out, code := runMCPSub(t, runMCPList)
	if code != 0 {
		t.Fatalf("list exit = %d:\n%s", code, out)
	}
	before := rowOf(out, "alpha")
	if !strings.HasSuffix(before, " "+mcpAuthMarkerRequired) {
		t.Errorf("an unauthenticated OAuth server must carry a TRAILING %s marker: %q", mcpAuthMarkerRequired, before)
	}
	if plain := rowOf(out, "plain"); strings.Contains(plain, "[") {
		t.Errorf("a server that needs no auth must carry no marker: %q", plain)
	}

	// Log in on this lele directory: the marker must flip, and NOTHING else
	// about the row may move (the marker is appended, never inserted).
	store := mcp.NewAuthStore(e.leleDir)
	if err := store.TokenStore("alpha", url).SaveToken(context.Background(),
		&transport.Token{AccessToken: "stored-token", TokenType: "Bearer"}); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	out, code = runMCPSub(t, runMCPList)
	if code != 0 {
		t.Fatalf("list exit = %d:\n%s", code, out)
	}
	after := rowOf(out, "alpha")
	if !strings.HasSuffix(after, " "+mcpAuthMarkerAuthenticated) {
		t.Errorf("a stored credential must flip the marker to %s: %q", mcpAuthMarkerAuthenticated, after)
	}
	if want := strings.TrimSuffix(before, " "+mcpAuthMarkerRequired) + " " + mcpAuthMarkerAuthenticated; after != want {
		t.Errorf("logging in changed more than the marker:\n  before %q\n  want   %q\n  after  %q", before, want, after)
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

// TestMCPShow_AuthBlockVerdict pins the C2 (CLI half) rule for `show`: the
// DECLARED auth block is printed the way the other fields are (labels, keys
// side only, verbatim), the LIVE verdict follows it in the same bracket
// vocabulary as `list`, and no credential — client secret, client id value,
// token — ever reaches the page.
func TestMCPShow_AuthBlockVerdict(t *testing.T) {
	e := newMCPTestEnv(t)
	const secretCanary = "SHOW-AUTH-SECRET-CANARY-77b2"
	url := "https://mcp.example.com/mcp"
	writeMCPTestFile(t, e.globalFile(), fmt.Sprintf(
		`{"mcpServers":{"alpha":{"url":%q,"type":"http","auth":{`+
			`"type":"oauth","scopes":["read","write"],"client_id":"cid-123",`+
			`"client_secret":%q,"redirect_uri":"http://127.0.0.1:9000/callback",`+
			`"auth_server_metadata_url":"https://as.example/.well-known/oauth-authorization-server",`+
			`"protected_resource_metadata_url":"https://mcp.example.com/.well-known/oauth-protected-resource"}}}}`,
		url, secretCanary))

	out, code := runMCPSub(t, runMCPShow, "alpha")
	if code != 0 {
		t.Fatalf("show exit = %d, want 0:\n%s", code, out)
	}
	// The labels are padded to 12 cells by mcpShowField, so the expected
	// spacing below is the rendered one.
	for _, want := range []string{
		"auth:        oauth",
		"scopes:      read, write",
		"client id:   (set)",
		"redirect:    http://127.0.0.1:9000/callback",
		"as metadata: https://as.example/.well-known/oauth-authorization-server",
		"pr metadata: https://mcp.example.com/.well-known/oauth-protected-resource",
		"auth state:  " + mcpAuthMarkerRequired,
		"auth method: oauth",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, secretCanary) || strings.Contains(out, "client_secret") {
		t.Fatalf("the client secret reached the show output:\n%s", out)
	}
	if strings.Contains(out, "cid-123") {
		t.Errorf("the client id VALUE must never be printed (set/not set only):\n%s", out)
	}

	// Log in on this lele directory: the LIVE verdict flips, the declared block
	// above is untouched.
	store := mcp.NewAuthStore(e.leleDir)
	if err := store.TokenStore("alpha", url).SaveToken(context.Background(),
		&transport.Token{AccessToken: "stored-token", TokenType: "Bearer"}); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	after, code := runMCPSub(t, runMCPShow, "alpha")
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, after)
	}
	if !strings.Contains(after, "auth state:  "+mcpAuthMarkerAuthenticated) {
		t.Errorf("a stored credential must flip the show verdict to %s:\n%s", mcpAuthMarkerAuthenticated, after)
	}
	if strings.Contains(after, mcpAuthMarkerRequired) {
		t.Errorf("a logged-in server still shows %s:\n%s", mcpAuthMarkerRequired, after)
	}
	if !strings.Contains(after, "auth:        oauth") || !strings.Contains(after, "scopes:      read, write") {
		t.Errorf("the declared auth block must survive a login:\n%s", after)
	}
	if strings.Contains(after, "stored-token") {
		t.Fatalf("the stored token reached the show output:\n%s", after)
	}
}

// TestMCPAuthVerdict_StoredCredentialBeatsNoRequirement pins the ONE render
// rule at BOTH CLI sites — the `list` marker and the `show` verdict — against
// the rule the TUI's suffix and the WebUI's chip apply:
//
//	authenticated ⇒ show authenticated; else required ⇒ show needs-auth;
//	else nothing
//
// Authenticated is tested FIRST because that is what mcp.CheckAuth means by it
// ("a usable credential exists, no sign-in is needed") and because the two
// flags are ORTHOGONAL: pkg/mcp documents a stored credential on an entry whose
// AuthRequired is false, and both other surfaces render that verdict as
// authenticated. Keying on `!Required` first rendered NOTHING in `list` and
// `auth state: (none)` in `show` for a server whose credential the store
// really holds — the CLI answering a different question than the other two
// surfaces for the very same DTO.
func TestMCPAuthVerdict_StoredCredentialBeatsNoRequirement(t *testing.T) {
	e := newMCPTestEnv(t)
	url := "https://mcp.example.com/mcp"
	// A header-carried entry with no auth block and no recorded 401: the store
	// is the ONLY auth fact, so CheckAuth answers Required:false with a token
	// present (Method "headers").
	writeMCPTestFile(t, e.globalFile(), fmt.Sprintf(
		`{"mcpServers":{"alpha":{"url":%q,"type":"http","headers":{"Accept":"application/json"}}}}`, url))

	store := mcp.NewAuthStore(e.leleDir)
	if err := store.TokenStore("alpha", url).SaveToken(context.Background(),
		&transport.Token{AccessToken: "stored-token", TokenType: "Bearer"}); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	// The fixture must actually be the disputed shape, or the test would prove
	// nothing: ask CheckAuth, the one source both sites read.
	disc, err := mcp.Discover(e.paths())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	srv, ok := disc.Servers["alpha"]
	if !ok {
		t.Fatalf("alpha is not discoverable: %+v", disc.Servers)
	}
	st := mcp.CheckAuth(store, "alpha", srv)
	if st.Required || !st.Authenticated {
		t.Fatalf("fixture verdict = %+v, want {Required:false Authenticated:true}", st)
	}

	// The rule itself, both helpers, all three branches.
	rule := []struct {
		name string
		st   mcp.AuthStatus
		mark string
		show string
	}{
		{"stored credential, no requirement", mcp.AuthStatus{Authenticated: true, Method: "headers"}, mcpAuthMarkerAuthenticated, mcpAuthMarkerAuthenticated},
		{"login owed", mcp.AuthStatus{Required: true, Method: "oauth"}, mcpAuthMarkerRequired, mcpAuthMarkerRequired},
		{"nothing to say", mcp.AuthStatus{Method: "headers"}, "", "(none)"},
	}
	for _, tc := range rule {
		if got := mcpAuthMarker(tc.st); got != tc.mark {
			t.Errorf("mcpAuthMarker(%s) = %q, want %q", tc.name, got, tc.mark)
		}
		if got := mcpAuthStateLabel(tc.st); got != tc.show {
			t.Errorf("mcpAuthStateLabel(%s) = %q, want %q", tc.name, got, tc.show)
		}
	}

	// `list`: the row must carry the trailing marker, exactly like the TUI and
	// the WebUI do for this DTO.
	out, code := runMCPSub(t, runMCPList)
	if code != 0 {
		t.Fatalf("list exit = %d:\n%s", code, out)
	}
	var row string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimLeft(l, "●○!⊘ "), "alpha ") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("no `list` row for alpha:\n%s", out)
	}
	if !strings.HasSuffix(row, " "+mcpAuthMarkerAuthenticated) {
		t.Errorf("a stored credential must render %s even with no declared requirement: %q", mcpAuthMarkerAuthenticated, row)
	}
	if strings.Contains(row, mcpAuthMarkerRequired) {
		t.Errorf("a signed-in row still advertises %s: %q", mcpAuthMarkerRequired, row)
	}

	// `show`: the verdict line must say the same thing, not (none).
	page, code := runMCPSub(t, runMCPShow, "alpha")
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, page)
	}
	if !strings.Contains(page, "auth state:  "+mcpAuthMarkerAuthenticated) {
		t.Errorf("`show` must report the stored credential as signed in:\n%s", page)
	}
	if strings.Contains(page, "auth state:  (none)") {
		t.Errorf("`show` reports (none) for a server whose credential is stored:\n%s", page)
	}
	if !strings.Contains(page, "auth method: headers") {
		t.Errorf("the method line must name the carrier:\n%s", page)
	}
	if strings.Contains(page, "stored-token") {
		t.Fatalf("the stored token reached the show output:\n%s", page)
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

	// The merge is whole-entry replace: a surviving COPY ABOVE the definition
	// (a disable stub or a disabled definition) keeps the server OFF even after
	// the winner is removed, so warning "this re-enables" would be a lie.
	t.Run("no warning when the takeover copy keeps it off", func(t *testing.T) {
		tests := []struct {
			name  string
			agent string
		}{
			{name: "disable stub shadows the definition", agent: `{"mcpServers":{"sierra":{"disabled":true}}}`},
			{name: "disabled definition shadows the definition", agent: `{"mcpServers":{"sierra":{"command":"agent-sierra","disabled":true}}}`},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				e := newMCPTestEnv(t)
				writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"sierra":{"command":"npx","args":["-y"]}}}`)
				writeMCPTestFile(t, e.agentFile(), tt.agent)
				writeMCPTestFile(t, e.projectFile(), `{"mcpServers":{"sierra":{"disabled":true}}}`)

				// Fixture premise: the project stub wins and keeps it OFF.
				if inv := mcp.ReadInventory(e.paths()); inv.Effective["sierra"] != "disabled" ||
					inv.Winner["sierra"] != "project" {
					t.Fatalf("fixture premise broken: effective=%q winner=%q",
						inv.Effective["sierra"], inv.Winner["sierra"])
				}

				out, code := runMCPSub(t, runMCPRemove, "sierra")
				if code != 0 {
					t.Fatalf("remove exit = %d, want 0:\n%s", code, out)
				}
				if strings.Contains(out, "re-enables") {
					t.Errorf("the agent copy still keeps the server OFF — no re-enable warning:\n%s", out)
				}
				if inv := mcp.ReadInventory(e.paths()); inv.Effective["sierra"] != "disabled" {
					t.Errorf("after the removal the server must stay disabled, got %q", inv.Effective["sierra"])
				}
			})
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

// The happy paths are exercised at the REAL seam: `lele mcp test` resolves the
// entry's expanded config and dials it through mcp.ProbeServer, so these tests
// point a url entry at a real in-process MCP server (streamable HTTP) — the
// world is mocked, never the code under test.
//
// The auth branches are the ONE exception, and they need it: a real server
// cannot be made to answer "authentication required" on demand. probeServerForCLI
// lets a test hand runMCPTest that OUTCOME, while everything up to the dial
// (config resolution, OAuth attachment) still runs for real — and the
// assertions are on the config the CLI built, so the wiring under test is
// verified rather than bypassed.

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

// withProbeSeam swaps the CLI's probe for the duration of one test. The seam
// exists for OUTCOME branches (see probeServerForCLI): the substituted probe
// still receives the config runMCPTest resolved and attached.
func withProbeSeam(t *testing.T, probe func(context.Context, mcp.ServerConfig) (mcp.ProbeResult, error)) {
	t.Helper()
	restore := probeServerForCLI
	probeServerForCLI = probe
	t.Cleanup(func() { probeServerForCLI = restore })
}

// requireProductionProbe fails when a seam substitution leaked out of another
// test: a test that means to exercise the REAL dial is meaningless if it
// inherits a fake probe.
func requireProductionProbe(t *testing.T) {
	t.Helper()
	if reflect.ValueOf(probeServerForCLI).Pointer() != reflect.ValueOf(mcp.ProbeServer).Pointer() {
		t.Fatal("probeServerForCLI is substituted: this test must run against the production probe")
	}
}

// TestMCPTest_AuthRequiredSentinelBranch: an authorization challenge is not
// "the transport broke". The probe's failure carries mcp.ErrAuthRequired
// (wrapped exactly as ProbeServer wraps it) and the command must say what to
// do about it instead of collapsing it into the generic ✗ failed line — while
// keeping the exit code of every other failure (1).
func TestMCPTest_AuthRequiredSentinelBranch(t *testing.T) {
	e := newMCPTestEnv(t)
	url := closedMCPURL(t)
	writeMCPTestFile(t, e.globalFile(),
		fmt.Sprintf(`{"mcpServers":{"alpha":{"url":%q,"type":"http"}}}`, url))

	var gotCfg mcp.ServerConfig
	withProbeSeam(t, func(_ context.Context, cfg mcp.ServerConfig) (mcp.ProbeResult, error) {
		gotCfg = cfg
		// The production shape, twice wrapped: ProbeServer wraps the dial
		// error, which wraps the sentinel.
		return mcp.ProbeResult{}, fmt.Errorf("mcp probe: %w",
			fmt.Errorf("mcp dial: http transport: %w", mcp.ErrAuthRequired))
	})

	out, code := runMCPSub(t, runMCPTest, "alpha")
	if code != 1 {
		t.Fatalf("auth-required test exit = %d, want 1:\n%s", code, out)
	}
	if want := `✗ "alpha" authentication required — run 'lele mcp auth alpha'`; !strings.Contains(out, want) {
		t.Errorf("missing the auth-required instruction %q:\n%s", want, out)
	}
	// The entry is remote, so the instruction is repeated for a headless user.
	if want := `you must sign in first: run 'lele mcp auth alpha' where lele runs`; !strings.Contains(out, want) {
		t.Errorf("missing the headless instruction %q:\n%s", want, out)
	}
	if strings.Contains(out, "failed:") {
		t.Errorf("the auth challenge took the generic failure branch:\n%s", out)
	}
	// The probe really was called with the resolved config: the branch must
	// not have been reached some other way.
	if gotCfg.URL != url {
		t.Errorf("probe received url %q, want %q", gotCfg.URL, url)
	}
}

// TestMCPTest_OAuthEntryWithoutTokenIsAuthRequired drives the SAME branch with
// the PRODUCTION probe and no token stored: an entry that declares
// auth.type == "oauth" is attached by the CLI, and mcp-go's attached handler
// refuses to send anything ("no valid token available, authorization
// required") before the transport is dialed at all.
//
// The url is one nothing listens on, so this is also the attach regression: a
// CLI that probed the BARE config would get a plain "connection refused" and
// print the generic ✗ failed line instead.
func TestMCPTest_OAuthEntryWithoutTokenIsAuthRequired(t *testing.T) {
	requireProductionProbe(t)
	e := newMCPTestEnv(t)
	url := closedMCPURL(t)
	writeMCPTestFile(t, e.globalFile(),
		fmt.Sprintf(`{"mcpServers":{"alpha":{"url":%q,"type":"http","auth":{"type":"oauth"}}}}`, url))

	// Premise: nothing is stored for this server.
	if st := mcp.CheckAuth(mcp.NewAuthStore(e.leleDir), "alpha", mustParseServer(t, e.globalFile(), "alpha")); st.Required && st.Authenticated {
		t.Fatalf("fixture premise broken: store already holds a usable token (%+v)", st)
	}

	out, code := runMCPSub(t, runMCPTest, "alpha")
	if code != 1 {
		t.Fatalf("auth-required test exit = %d, want 1:\n%s", code, out)
	}
	if want := `✗ "alpha" authentication required — run 'lele mcp auth alpha'`; !strings.Contains(out, want) {
		t.Errorf("missing the auth-required instruction %q:\n%s", want, out)
	}
	if strings.Contains(out, "failed:") {
		t.Errorf("the OAuth challenge took the generic failure branch:\n%s", out)
	}
}

// startBearerGatedMCPHTTPServer serves the same in-process MCP server as
// startMCPHTTPServer but answers 401 (the RFC 9728 challenge) unless the
// request carries bearerToken. It is the WORLD's half of "this server needs a
// login": an OAuth-attached client with no token gets the challenge, one that
// presents the stored token gets the real handshake.
func startBearerGatedMCPHTTPServer(t *testing.T, bearerToken string, toolNames ...string) string {
	t.Helper()
	srv := mcpserver.NewMCPServer("lele-cli-test", "9.9.9", mcpserver.WithToolCapabilities(false))
	for _, name := range toolNames {
		srv.AddTool(mcpgo.NewTool(name), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			return mcpgo.NewToolResultText(name), nil
		})
	}
	inner := mcpserver.NewStreamableHTTPServer(srv)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+bearerToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp"
}

// TestMCPTest_AuthenticatedServerIsProbedForReal is part 2 proven on the wire,
// with nothing substituted: a REAL in-process MCP server that demands a bearer
// token, a token stored in a temp-dir store, and the production probe. It can
// only succeed if the CLI both attached the OAuth runtime AND handed it the
// token from this lele directory — probing the bare config leaves the request
// unauthenticated, the server answers 401 and the run fails with
// "authentication required".
func TestMCPTest_AuthenticatedServerIsProbedForReal(t *testing.T) {
	requireProductionProbe(t)
	e := newMCPTestEnv(t)
	url := startBearerGatedMCPHTTPServer(t, "stored-token", "echo")
	writeMCPTestFile(t, e.globalFile(),
		fmt.Sprintf(`{"mcpServers":{"alpha":{"url":%q,"type":"http","auth":{"type":"oauth"}}}}`, url))

	store := mcp.NewAuthStore(e.leleDir)
	if err := store.TokenStore("alpha", url).SaveToken(context.Background(),
		&transport.Token{AccessToken: "stored-token", TokenType: "Bearer"}); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	out, code := runMCPSub(t, runMCPTest, "alpha")
	if code != 0 {
		t.Fatalf("test exit = %d, want 0 (the server accepts the stored token):\n%s", code, out)
	}
	if strings.Contains(out, "authentication required") {
		t.Errorf("a logged-in server was reported as needing a login:\n%s", out)
	}
	if !strings.Contains(out, `✓ "alpha" connected — 1 tools: echo`) {
		t.Errorf("unexpected probe output:\n%s", out)
	}
}

// TestMCPTest_ProbeConfigCarriesAuthRuntime is THE regression test for the
// whole task: a server this lele directory IS logged into must not be reported
// as "authentication required". The token lives in a temp-dir store and the
// probe seam records the config it was handed, so the assertion is on what the
// CLI built.
//
// The load-bearing assertion is `reflect.DeepEqual(got, bare) == false`.
// pkg/mcp keeps the OAuth runtime in an unexported, unserialized field, so
// AttachAuthRuntime's ONLY effect on the value is to set that field: equal
// configs therefore mean the CLI probed the bare `ServerConfig` — the exact
// bug this test exists for. It fails on the pre-fix code, and the sibling
// assertions (Required/Authenticated from a freshly opened store) pin that the
// store the CLI used is this lele directory and not, say, the agent workspace.
func TestMCPTest_ProbeConfigCarriesAuthRuntime(t *testing.T) {
	e := newMCPTestEnv(t)
	url := "https://mcp.example.com/mcp" // never dialed: the seam probe replaces the dial
	writeMCPTestFile(t, e.globalFile(),
		fmt.Sprintf(`{"mcpServers":{"alpha":{"url":%q,"type":"http","auth":{"type":"oauth"}}}}`, url))

	// A login already performed on this machine.
	store := mcp.NewAuthStore(e.leleDir)
	if err := store.TokenStore("alpha", url).SaveToken(context.Background(),
		&transport.Token{AccessToken: "test-token", TokenType: "Bearer"}); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	bare := mustParseServer(t, e.globalFile(), "alpha")
	fresh := mcp.NewAuthStore(e.leleDir)
	if st := mcp.CheckAuth(fresh, "alpha", bare); !st.Required || !st.Authenticated {
		t.Fatalf("fixture premise broken: CheckAuth(store, alpha, bare) = %+v, want required+authenticated", st)
	}

	var got mcp.ServerConfig
	withProbeSeam(t, func(_ context.Context, cfg mcp.ServerConfig) (mcp.ProbeResult, error) {
		got = cfg
		return mcp.ProbeResult{Tools: []string{"echo"}}, nil
	})

	out, code := runMCPSub(t, runMCPTest, "alpha")
	if code != 0 {
		t.Fatalf("authenticated test exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, `✓ "alpha" connected — 1 tools: echo`) {
		t.Errorf("unexpected probe output:\n%s", out)
	}
	if reflect.DeepEqual(got, bare) {
		t.Error("the probe received the BARE ServerConfig: the OAuth runtime was not attached, " +
			"so a server this lele directory is logged into would be reported as 'authentication required'")
	}
	if st := mcp.CheckAuth(mcp.NewAuthStore(e.leleDir), "alpha", got); !st.Required || !st.Authenticated {
		t.Errorf("CheckAuth(store, alpha, probed config) = %+v, want required+authenticated", st)
	}
}

// TestMCPAuthSeam_DefaultsToTheEngine pins the seam the `lele mcp auth`
// subcommand drives: it must START as the real engine call, so a test can only
// ever substitute it explicitly (a nil seam would make the argv tests pass
// against nothing). The check goes past a nil comparison and ACTUALLY calls the
// default: mcp.StartAuth refuses a disabled store as its very first act, so
// this can only return the engine's own sentinel — and it needs no network.
func TestMCPAuthSeam_DefaultsToTheEngine(t *testing.T) {
	if startMCPAuthFlow == nil {
		t.Fatal("startMCPAuthFlow must default to mcp.StartAuth")
	}
	_, err := startMCPAuthFlow(context.Background(), "api",
		mcp.ServerConfig{URL: "https://mcp.example.com/mcp"}, mcp.NewAuthStore(""), mcp.AuthOptions{})
	if !errors.Is(err, mcp.ErrAuthStoreDisabled) {
		t.Fatalf("the default seam returned %v, want mcp.ErrAuthStoreDisabled from the real engine", err)
	}
}

// mustParseServer returns the UNEXPANDED entry as written in file — the bare
// config, i.e. exactly what mcpProbeConfig would return without the OAuth
// runtime attached.
func mustParseServer(t *testing.T, file, name string) mcp.ServerConfig {
	t.Helper()
	f, err := mcp.ParseFile(file)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	cfg, ok := f.MCPServers[name]
	if !ok {
		t.Fatalf("%s has no server %q", file, name)
	}
	return cfg
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
// auth / logout
// ---------------------------------------------------------------------------

// The whole `auth` argv surface is driven at its seams: startMCPAuthFlow is
// substituted (the engine would dial, register and listen for real) and
// mcpAuthInput feeds the --manual paste. Nothing else is faked — the entry is
// resolved by the production path, so the tests assert on the config and the
// options the CLI actually built.

// seedMCPRemoteAuthServer writes the remote, OAuth-opted-in entry into the
// global layer — the shape `lele mcp add --url … --auth oauth` produces.
func seedMCPRemoteAuthServer(t *testing.T, e *mcpTestEnv, name, url string) {
	t.Helper()
	writeMCPTestFile(t, e.globalFile(),
		fmt.Sprintf(`{"mcpServers":{%q:{"url":%q,"type":"http","auth":{"type":"oauth"}}}}`, name, url))
}

// fakeMCPAuthFlow stands in for the engine's *mcp.AuthFlow. The interface seam
// exists for exactly this reason: the engine's flow carries unexported fields,
// so nothing outside pkg/mcp can build one, and this test must never touch the
// network.
//
// It models the engine's retryable-failure contract: a Submit that FAILS does
// not make the flow Done() (so the --manual loop re-prompts the same flow), a
// Submit that SUCCEEDS does, and Cancel is terminal. submitErrs, when non-empty,
// answers one error per Submit call (index 0 for the first call, then the last
// entry repeats); submitErr is the shorthand for "every Submit fails".
type fakeMCPAuthFlow struct {
	url        string
	waitErr    error
	submitErr  error
	submitErrs []error
	done       bool

	submitted [][2]string // every (code, state) pair handed to Submit
	cancels   int
}

func (f *fakeMCPAuthFlow) URL() string                { return f.url }
func (f *fakeMCPAuthFlow) Wait(context.Context) error { return f.waitErr }
func (f *fakeMCPAuthFlow) Done() bool                 { return f.done }
func (f *fakeMCPAuthFlow) Cancel() error {
	f.cancels++
	f.done = true
	return nil
}

func (f *fakeMCPAuthFlow) Submit(_ context.Context, code, state string) error {
	f.submitted = append(f.submitted, [2]string{code, state})
	err := f.submitErr
	if i := len(f.submitted) - 1; i < len(f.submitErrs) {
		err = f.submitErrs[i]
	}
	if err != nil {
		// A failed exchange leaves the flow OPEN and retryable, like the
		// engine's.
		return err
	}
	f.done = true
	return nil
}

// mcpAuthSeamCall records one startMCPAuthFlow call, so a test can assert the
// entry, the store and the options the CLI resolved.
type mcpAuthSeamCall struct {
	name  string
	srv   mcp.ServerConfig
	store *mcp.AuthStore
	opts  mcp.AuthOptions
}

// mcpAuthSeam is a substituted startMCPAuthFlow plus the calls it saw.
type mcpAuthSeam struct {
	flow  mcpAuthFlow
	err   error
	calls []mcpAuthSeamCall
}

func (s *mcpAuthSeam) start(_ context.Context, name string, srv mcp.ServerConfig, store *mcp.AuthStore, opts mcp.AuthOptions) (mcpAuthFlow, error) {
	s.calls = append(s.calls, mcpAuthSeamCall{name: name, srv: srv, store: store, opts: opts})
	if s.err != nil {
		return nil, s.err
	}
	return s.flow, nil
}

// installMCPAuthSeam substitutes startMCPAuthFlow for one test (and restores it
// afterwards, so a substituted seam can never leak into a sibling test).
func installMCPAuthSeam(t *testing.T, flow mcpAuthFlow, err error) *mcpAuthSeam {
	t.Helper()
	s := &mcpAuthSeam{flow: flow, err: err}
	restore := startMCPAuthFlow
	startMCPAuthFlow = s.start
	t.Cleanup(func() { startMCPAuthFlow = restore })
	return s
}

// withMCPAuthInput replaces the --manual stdin reader for one test.
func withMCPAuthInput(t *testing.T, in string) {
	t.Helper()
	restore := mcpAuthInput
	mcpAuthInput = strings.NewReader(in)
	t.Cleanup(func() { mcpAuthInput = restore })
}

// requireMCPOwnLine asserts want appears as a WHOLE line of out — the contract
// for the authorization URL, which must be printable/copyable on a headless
// host with no decoration around it.
func requireMCPOwnLine(t *testing.T, out, want string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if line == want {
			return
		}
	}
	t.Errorf("output has no line exactly %q:\n%s", want, out)
}

// TestMCPAuth_PrintsURLAndSucceeds is the happy path: the URL goes out (on its
// own line) before the CLI blocks, the flow is waited for, and the seam
// received the resolved remote entry, this lele directory's store and the
// default options.
func TestMCPAuth_PrintsURLAndSucceeds(t *testing.T) {
	e := newMCPTestEnv(t)
	const srvURL = "https://mcp.example.com/mcp"
	seedMCPRemoteAuthServer(t, e, "api", srvURL)
	// No --manual: stdin must not be read — a reader that fails loudly proves it.
	withMCPAuthInput(t, "")

	flow := &fakeMCPAuthFlow{url: "https://as.example.com/authorize?client_id=lele&state=st-1&code_challenge=xyz"}
	seam := installMCPAuthSeam(t, flow, nil)

	out, code := runMCPSub(t, runMCPAuth, "api")
	if code != 0 {
		t.Fatalf("auth exit = %d, want 0:\n%s", code, out)
	}
	requireMCPOwnLine(t, out, flow.url)
	if !strings.Contains(out, "Authorization URL") {
		t.Errorf("the printed URL is not labelled:\n%s", out)
	}
	if !strings.Contains(out, `✓ "api" authenticated; the gateway picks it up on the next reload`) {
		t.Errorf("missing the success line:\n%s", out)
	}
	if len(flow.submitted) != 0 {
		t.Errorf("nothing was pasted, yet Submit saw %v", flow.submitted)
	}

	if len(seam.calls) != 1 {
		t.Fatalf("start seam called %d times, want 1", len(seam.calls))
	}
	call := seam.calls[0]
	if call.name != "api" || call.srv.URL != srvURL {
		t.Errorf("seam got name=%q url=%q, want the resolved entry", call.name, call.srv.URL)
	}
	if call.srv.Auth == nil || call.srv.Auth.Type != "oauth" {
		t.Errorf("the resolved config lost its auth block: %+v", call.srv.Auth)
	}
	if call.opts.Timeout != mcpAuthDefaultTimeout {
		t.Errorf("default timeout = %v, want %v (the engine's 5m)", call.opts.Timeout, mcpAuthDefaultTimeout)
	}
	if call.opts.Port != 0 {
		t.Errorf("default port = %d, want 0 (ephemeral)", call.opts.Port)
	}
	if call.opts.OpenURL == nil {
		t.Error("without --no-browser the engine must be given a browser opener")
	}
	// The store is THIS lele directory's: a token written through the store the
	// seam received is visible to a freshly opened store on e.leleDir.
	if err := call.store.TokenStore("api", srvURL).SaveToken(context.Background(),
		&transport.Token{AccessToken: "t", TokenType: "Bearer"}); err != nil {
		t.Fatalf("save through the flow's store: %v", err)
	}
	if !mcp.NewAuthStore(e.leleDir).Authenticated("api", srvURL) {
		t.Error("the flow's store is not this lele directory's mcp-auth.json")
	}
}

// TestMCPAuth_BrowserOpenerWiring pins the two halves of the browser contract:
// without --no-browser the engine is handed pkg/auth's OpenBrowser (the one
// platform switch), with it the option is nil — and the URL is printed either
// way, because --no-browser only suppresses the launch, never the URL.
func TestMCPAuth_BrowserOpenerWiring(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantOpener bool
	}{
		{name: "default opens a browser", args: []string{"api"}, wantOpener: true},
		{name: "--no-browser suppresses it", args: []string{"api", "--no-browser"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMCPTestEnv(t)
			seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
			withMCPAuthInput(t, "")
			flow := &fakeMCPAuthFlow{url: "https://as.example.com/authorize?state=st-1"}
			seam := installMCPAuthSeam(t, flow, nil)

			out, code := runMCPSub(t, runMCPAuth, tt.args...)
			if code != 0 {
				t.Fatalf("auth exit = %d, want 0:\n%s", code, out)
			}
			requireMCPOwnLine(t, out, flow.url)

			got := seam.calls[0].opts.OpenURL
			if !tt.wantOpener {
				if got != nil {
					t.Error("--no-browser must pass a nil OpenURL: the engine then never launches a browser")
				}
				return
			}
			if got == nil {
				t.Fatal("the engine was not given a browser opener")
			}
			if reflect.ValueOf(got).Pointer() != reflect.ValueOf(auth.OpenBrowser).Pointer() {
				t.Error("the opener must be pkg/auth's OpenBrowser (the shared platform switch)")
			}
		})
	}
}

// TestMCPAuth_StdioServerNotSupported: a local (stdio) entry has no url to
// authorize against. The refusal names the shape and the engine is never called
// — a stdio server must not even start a flow.
func TestMCPAuth_StdioServerNotSupported(t *testing.T) {
	e := newMCPTestEnv(t)
	writeMCPTestFile(t, e.globalFile(), `{"mcpServers":{"fs":{"command":"npx","args":["-y","fs"]}}}`)
	withMCPAuthInput(t, "")
	seam := installMCPAuthSeam(t, &fakeMCPAuthFlow{url: "https://as.example/authorize"}, nil)

	out, code := runMCPSub(t, runMCPAuth, "fs")
	if code != mcpAuthExitFailure {
		t.Fatalf("stdio auth exit = %d, want %d:\n%s", code, mcpAuthExitFailure, out)
	}
	if !strings.Contains(out, `✗ "fs" is a local (stdio) server`) || !strings.Contains(out, "not supported") {
		t.Errorf("missing the stdio refusal:\n%s", out)
	}
	if len(seam.calls) != 0 {
		t.Errorf("a local server reached the engine %d time(s)", len(seam.calls))
	}
}

// TestMCPAuth_StartFailureSentinels is the sentinel table: every failure the
// engine can report when STARTING a flow gets its own sentence and the family's
// exit code, and none of them is collapsed into another. The transport sentinel
// in particular must never read as "does not require authentication".
func TestMCPAuth_StartFailureSentinels(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    string
		notWant string
	}{
		{
			name: "not required",
			err:  mcp.ErrAuthNotRequired,
			want: `✗ "api" does not require authentication`,
		},
		{
			name: "not required, wrapped",
			err:  fmt.Errorf("mcp auth %q: %w", "api", mcp.ErrAuthNotRequired),
			want: `✗ "api" does not require authentication`,
		},
		{
			name: "not supported",
			err:  mcp.ErrAuthNotSupported,
			want: `✗ "api" authentication is not supported`,
		},
		{
			name:    "probe failed is a transport failure",
			err:     mcp.ErrAuthProbeFailed,
			want:    `✗ "api" transport failure`,
			notWant: "does not require authentication",
		},
		{
			name:    "probe failed, engine wrapping",
			err:     fmt.Errorf("mcp auth %q: probe: %w", "api", mcp.ErrAuthProbeFailed),
			want:    `✗ "api" transport failure`,
			notWant: "does not require authentication",
		},
		{
			name: "store disabled",
			err:  mcp.ErrAuthStoreDisabled,
			want: "the authorization store is disabled",
		},
		{
			name: "canceled",
			err:  mcp.ErrAuthCanceled,
			want: "authorization canceled",
		},
		{
			name: "timed out",
			err:  mcp.ErrAuthTimedOut,
			want: "authorization timed out",
		},
		{
			name: "engine detail",
			err:  errors.New("mcp auth \"api\": authorization was rejected by the authorization server"),
			want: `✗ "api" authorization failed: mcp auth "api": authorization was rejected`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMCPTestEnv(t)
			seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
			withMCPAuthInput(t, "")
			seam := installMCPAuthSeam(t, &fakeMCPAuthFlow{url: "https://as.example/authorize"}, tt.err)

			out, code := runMCPSub(t, runMCPAuth, "api")
			if code != mcpAuthExitFailure {
				t.Fatalf("start failure exit = %d, want %d (the family's convention):\n%s", code, mcpAuthExitFailure, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("want %q in:\n%s", tt.want, out)
			}
			if tt.notWant != "" && strings.Contains(out, tt.notWant) {
				t.Errorf("output must not contain %q:\n%s", tt.notWant, out)
			}
			if strings.Contains(out, "Authorization URL") {
				t.Errorf("no URL exists when the flow failed to start:\n%s", out)
			}
			if strings.Contains(out, "✓") {
				t.Errorf("a failed start reported success:\n%s", out)
			}
			if len(seam.calls) != 1 {
				t.Errorf("start seam called %d times, want 1", len(seam.calls))
			}
		})
	}
}

// TestMCPAuth_WaitFailureSentinels: the flow started (so the URL WAS printed,
// on its own line, before the wait) but never completed. Cancellation and the
// timeout keep their own sentences and the family's exit code.
func TestMCPAuth_WaitFailureSentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "canceled", err: mcp.ErrAuthCanceled, want: "authorization canceled"},
		{name: "timed out", err: mcp.ErrAuthTimedOut, want: "authorization timed out"},
		{name: "engine detail", err: errors.New("authorization failed: invalid_grant"), want: "authorization failed: invalid_grant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMCPTestEnv(t)
			seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
			withMCPAuthInput(t, "")
			flow := &fakeMCPAuthFlow{url: "https://as.example/authorize?state=st-1", waitErr: tt.err}
			installMCPAuthSeam(t, flow, nil)

			out, code := runMCPSub(t, runMCPAuth, "api")
			if code != mcpAuthExitFailure {
				t.Fatalf("wait failure exit = %d, want %d:\n%s", code, mcpAuthExitFailure, out)
			}
			// The URL is the headless path's only way to finish: it must be out
			// even when the flow then fails.
			requireMCPOwnLine(t, out, flow.url)
			if !strings.Contains(out, tt.want) {
				t.Errorf("want %q in:\n%s", tt.want, out)
			}
			if strings.Contains(out, "✓") {
				t.Errorf("a failed wait reported success:\n%s", out)
			}
			if flow.cancels != 1 {
				t.Errorf("Cancel calls = %d, want 1 (the listener must be released on every exit path)", flow.cancels)
			}
		})
	}
}

// TestMCPAuth_ManualSubmitsPastedInput drives --manual with every paste shape a
// real redirect produces, and pins the (code, state) pair Submit receives.
func TestMCPAuth_ManualSubmitsPastedInput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want [2]string
	}{
		{name: "full redirect URL", in: "http://127.0.0.1:8080/callback?code=abc123&state=st-9", want: [2]string{"abc123", "st-9"}},
		{name: "full redirect URL, URL-encoded", in: "https://as.example/cb?iss=https%3A%2F%2Fas.example&code=a-b.c_d~e&state=st-9", want: [2]string{"a-b.c_d~e", "st-9"}},
		{name: "bare query string", in: "code=abc123&state=st-9", want: [2]string{"abc123", "st-9"}},
		{name: "bare code", in: "SplxlOBeZQQYbYS6WxSbIA", want: [2]string{"SplxlOBeZQQYbYS6WxSbIA", ""}},
		{name: "two tokens on one line", in: "abc123 st-9", want: [2]string{"abc123", "st-9"}},
		{name: "URL without state", in: "http://127.0.0.1:8080/callback?code=abc123", want: [2]string{"abc123", ""}},
		{name: "surrounding whitespace", in: "  abc123\r\n", want: [2]string{"abc123", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMCPTestEnv(t)
			seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
			withMCPAuthInput(t, tt.in)
			flow := &fakeMCPAuthFlow{url: "https://as.example/authorize?state=st-9"}
			installMCPAuthSeam(t, flow, nil)

			out, code := runMCPSub(t, runMCPAuth, "api", "--manual")
			if code != 0 {
				t.Fatalf("manual auth exit = %d, want 0:\n%s", code, out)
			}
			requireMCPOwnLine(t, out, flow.url)
			if len(flow.submitted) != 1 {
				t.Fatalf("Submit called %d times, want 1 (%v)", len(flow.submitted), flow.submitted)
			}
			if flow.submitted[0] != tt.want {
				t.Errorf("Submit got %v, want %v", flow.submitted[0], tt.want)
			}
			if !strings.Contains(out, `✓ "api" authenticated; the gateway picks it up on the next reload`) {
				t.Errorf("missing the success line:\n%s", out)
			}
		})
	}
}

// TestMCPAuth_ManualRefusedRedirectFails: a redirect carrying error= is the
// authorization server REFUSING the request. It must be reported as a failure
// and never submitted as if it were a code.
func TestMCPAuth_ManualRefusedRedirectFails(t *testing.T) {
	for _, in := range []string{
		"http://127.0.0.1:8080/callback?error=access_denied&state=st-1",
		"error=access_denied&state=st-1",
	} {
		t.Run(in, func(t *testing.T) {
			e := newMCPTestEnv(t)
			seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
			withMCPAuthInput(t, in)
			flow := &fakeMCPAuthFlow{url: "https://as.example/authorize?state=st-1"}
			installMCPAuthSeam(t, flow, nil)

			out, code := runMCPSub(t, runMCPAuth, "api", "--manual")
			if code != mcpAuthExitFailure {
				t.Fatalf("refused redirect exit = %d, want %d:\n%s", code, mcpAuthExitFailure, out)
			}
			if !strings.Contains(out, "the authorization server refused the authorization (access_denied)") {
				t.Errorf("missing the refusal, with the server's own error code:\n%s", out)
			}
			if len(flow.submitted) != 0 {
				t.Errorf("a refusal must not be submitted: %v", flow.submitted)
			}
			if strings.Contains(out, "✓") {
				t.Errorf("a refusal reported success:\n%s", out)
			}
		})
	}
}

// TestMCPAuth_ManualGarbageIsRejected: input the frozen grammar refuses never
// reaches Submit. The wording is the CLI's own; the RULE is the engine's
// (mcp.ParseAuthPaste), so the expectations follow the engine's sentinels:
// `unrecognized` (nothing matched an accepted form) and `no-code` (a URL/query
// without a code) share the CLI's one "unrecognized input" sentence — the
// grammar explicitly allows it and the TUI renders them the same way.
func TestMCPAuth_ManualGarbageIsRejected(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty stdin", in: "", want: "no input on stdin"},
		{name: "blank line", in: "\n", want: "empty input"},
		{name: "several tokens", in: "not an authorization code", want: "unrecognized input"},
		{name: "punctuation", in: "abc!def", want: "unrecognized input"},
		{name: "spaced code in a query", in: "code=abc def", want: "unrecognized input"},
		{name: "unrelated parameters", in: "foo=bar", want: "unrecognized input"},
		{name: "query without a code", in: "state=st-1&scope=read", want: "unrecognized input"},
		{name: "URL without a code", in: "http://127.0.0.1:8080/callback?state=st-1", want: "unrecognized input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMCPTestEnv(t)
			seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
			withMCPAuthInput(t, tt.in)
			flow := &fakeMCPAuthFlow{url: "https://as.example/authorize?state=st-1"}
			installMCPAuthSeam(t, flow, nil)

			out, code := runMCPSub(t, runMCPAuth, "api", "--manual")
			if code != mcpAuthExitFailure {
				t.Fatalf("garbage exit = %d, want %d:\n%s", code, mcpAuthExitFailure, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("want %q in:\n%s", tt.want, out)
			}
			if len(flow.submitted) != 0 {
				t.Errorf("garbage was submitted: %v", flow.submitted)
			}
		})
	}
}

// TestMCPAuth_ManualSubmitFailure: the engine rejects the code (bad state, a
// failed exchange) — the failure is reported with the engine's own value-free
// reason, and the URL stays printed.
func TestMCPAuth_ManualSubmitFailure(t *testing.T) {
	e := newMCPTestEnv(t)
	seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
	withMCPAuthInput(t, "abc123")
	flow := &fakeMCPAuthFlow{
		url:       "https://as.example/authorize?state=st-1",
		submitErr: errors.New("authorization failed: invalid_grant"),
	}
	installMCPAuthSeam(t, flow, nil)

	out, code := runMCPSub(t, runMCPAuth, "api", "--manual")
	if code != mcpAuthExitFailure {
		t.Fatalf("submit failure exit = %d, want %d:\n%s", code, mcpAuthExitFailure, out)
	}
	requireMCPOwnLine(t, out, flow.url)
	if !strings.Contains(out, `✗ "api" authorization failed: authorization failed: invalid_grant`) {
		t.Errorf("missing the engine's value-free reason:\n%s", out)
	}
}

// TestMCPAuth_ManualRetriesAfterFailure pins the C9 fix: a FAILED token
// exchange leaves the engine's flow OPEN and retryable, so the CLI must not
// burn it — the failure is printed, the prompt is shown AGAIN and the next
// paste is submitted on the SAME flow (same URL, same PKCE state).
func TestMCPAuth_ManualRetriesAfterFailure(t *testing.T) {
	e := newMCPTestEnv(t)
	seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
	withMCPAuthInput(t, "first-bad\nsecond-good\n")
	flow := &fakeMCPAuthFlow{
		url:        "https://as.example/authorize?state=st-1",
		submitErrs: []error{errors.New("authorization failed: invalid_grant"), nil},
	}
	installMCPAuthSeam(t, flow, nil)

	out, code := runMCPSub(t, runMCPAuth, "api", "--manual")
	if code != 0 {
		t.Fatalf("manual retry exit = %d, want 0:\n%s", code, out)
	}
	if got := strings.Count(out, mcpAuthManualPrompt); got != 2 {
		t.Errorf("the prompt was printed %d time(s), want 2 (the box stays live after a refused paste):\n%s", got, out)
	}
	want := [][2]string{{"first-bad", ""}, {"second-good", ""}}
	if !reflect.DeepEqual(flow.submitted, want) {
		t.Errorf("Submit saw %v, want %v (both pastes on the SAME flow)", flow.submitted, want)
	}
	if !strings.Contains(out, "invalid_grant") {
		t.Errorf("the value-free failure of the first attempt is not reported:\n%s", out)
	}
	if !strings.Contains(out, `✓ "api" authenticated; the gateway picks it up on the next reload`) {
		t.Errorf("the retry's success is not reported:\n%s", out)
	}
	if flow.cancels != 1 {
		t.Errorf("Cancel calls = %d, want 1 (no path may leak a flow)", flow.cancels)
	}
}

// TestMCPAuth_ManualEOFTerminates: the loop is bounded by stdin, too. One
// refused paste then EOF ends it with the family's exit code and releases the
// flow — EOF is not an empty code and never a third attempt.
func TestMCPAuth_ManualEOFTerminates(t *testing.T) {
	e := newMCPTestEnv(t)
	seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
	withMCPAuthInput(t, "abc123\n") // one line, then EOF
	flow := &fakeMCPAuthFlow{
		url:       "https://as.example/authorize?state=st-1",
		submitErr: errors.New("authorization failed: invalid_grant"),
	}
	installMCPAuthSeam(t, flow, nil)

	out, code := runMCPSub(t, runMCPAuth, "api", "--manual")
	if code != mcpAuthExitFailure {
		t.Fatalf("EOF exit = %d, want %d:\n%s", code, mcpAuthExitFailure, out)
	}
	if !strings.Contains(out, "no input on stdin") {
		t.Errorf("EOF must be reported as such, not as an empty code:\n%s", out)
	}
	if len(flow.submitted) != 1 {
		t.Errorf("Submit called %d time(s), want 1 (the paste was consumed once):\n%s", len(flow.submitted), out)
	}
	if flow.cancels != 1 {
		t.Errorf("Cancel calls = %d, want 1 (no path may leak a flow)", flow.cancels)
	}
	if strings.Contains(out, "✓") {
		t.Errorf("a run that never authenticated reported success:\n%s", out)
	}
}

// TestMCPAuth_ManualQuitTerminates: an explicit quit input ends the loop
// without submitting anything, and still releases the flow.
func TestMCPAuth_ManualQuitTerminates(t *testing.T) {
	e := newMCPTestEnv(t)
	seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
	withMCPAuthInput(t, ":q\n")
	flow := &fakeMCPAuthFlow{url: "https://as.example/authorize?state=st-1"}
	installMCPAuthSeam(t, flow, nil)

	out, code := runMCPSub(t, runMCPAuth, "api", "--manual")
	if code != mcpAuthExitFailure {
		t.Fatalf("quit exit = %d, want %d:\n%s", code, mcpAuthExitFailure, out)
	}
	// The exact sentence, not just the word: the flow is released by the
	// deferred Cancel (asserted below), so the message must NOT claim it was
	// left open, and it must name the command that starts a new flow.
	want := "✗ authorization canceled — nothing was submitted; the flow is abandoned, and 'lele mcp auth api' starts a new one"
	if !strings.Contains(out, want) {
		t.Errorf("the quit line must read %q:\n%s", want, out)
	}
	if strings.Contains(out, "left open") {
		t.Errorf("a quit releases the flow (Cancel ran), so it must not claim it was left open:\n%s", out)
	}
	if len(flow.submitted) != 0 {
		t.Errorf("a quit input was submitted: %v", flow.submitted)
	}
	if flow.cancels != 1 {
		t.Errorf("Cancel calls = %d, want 1 (no path may leak a flow)", flow.cancels)
	}
}

// TestMCPAuth_ManualQuitTokensAreNotPastes proves the quit inputs can never
// shadow a real paste: each is a value the frozen grammar itself REFUSES, so a
// quit never changes how an otherwise-valid paste would be handled.
func TestMCPAuth_ManualQuitTokensAreNotPastes(t *testing.T) {
	if len(mcpAuthQuitInputs) == 0 {
		t.Fatal("there are no quit inputs")
	}
	for tok := range mcpAuthQuitInputs {
		if _, _, err := mcp.ParseAuthPaste(tok); !errors.Is(err, mcp.ErrAuthPasteUnrecognized) {
			t.Errorf("quit token %q is a paste form (%v): it would shadow a code and diverge from the TUI/WebUI", tok, err)
		}
	}
}

// TestMCPAuth_ManualIsBounded: stdin can be a pipe with no EOF. The loop must
// stop after a fixed number of refused pastes instead of spinning forever.
func TestMCPAuth_ManualIsBounded(t *testing.T) {
	e := newMCPTestEnv(t)
	seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
	withMCPAuthInput(t, strings.Repeat("!!!\n", mcpAuthManualMaxAttempts+5))
	flow := &fakeMCPAuthFlow{url: "https://as.example/authorize?state=st-1"}
	installMCPAuthSeam(t, flow, nil)

	out, code := runMCPSub(t, runMCPAuth, "api", "--manual")
	if code != mcpAuthExitFailure {
		t.Fatalf("bounded exit = %d, want %d:\n%s", code, mcpAuthExitFailure, out)
	}
	if !strings.Contains(out, "too many refused pastes") {
		t.Errorf("the bound is not reported:\n%s", out)
	}
	if got := strings.Count(out, mcpAuthManualPrompt); got != mcpAuthManualMaxAttempts {
		t.Errorf("prompt printed %d time(s), want exactly %d attempts:\n%s", got, mcpAuthManualMaxAttempts, out)
	}
	if len(flow.submitted) != 0 {
		t.Errorf("garbage was submitted: %v", flow.submitted)
	}
	if flow.cancels != 1 {
		t.Errorf("Cancel calls = %d, want 1", flow.cancels)
	}
}

// TestMCPAuth_ManualMatchesEnginePasteGrammar is the C1 re-point: the CLI no
// longer owns a copy of the paste rule, so for every input of the frozen
// example table (.lele/tasks/mcp-oauth-paste-grammar.md) the pair the CLI
// submits — or its refusal — must be EXACTLY what mcp.ParseAuthPaste answers.
// It fails the moment a private parser (or a divergent mapping) reappears.
func TestMCPAuth_ManualMatchesEnginePasteGrammar(t *testing.T) {
	const canary = "GRAMMAR-CANARY-33f1"
	// The engine's input bound is 8192 bytes (authPasteMaxInputLen, unexported);
	// one byte over must be refused, on every surface.
	const inputBound = 8192
	inputs := []string{
		"",
		"   ",
		"abc123",
		"abc-12_3.x~y",
		strings.Repeat("a", 300), // a long, opaque real code
		strings.Repeat("a", inputBound+1),
		"  abc123  ",
		"!!!",
		"abc!def",
		"a b c",
		"abc xyz",
		"abc   xyz",
		"abc! xyz",
		"code=abc",
		"code=abc&state=xyz",
		"?code=abc&state=xyz",
		"http://127.0.0.1:37401/cb?code=abc&state=xyz",
		"http://127.0.0.1:37401/cb?state=xyz&code=abc",
		"http://127.0.0.1:37401/cb?code=a%2Bb&state=s",
		"http://127.0.0.1:37401/cb?code=abc#frag",
		"http://127.0.0.1:37401/cb?code=",
		"http://127.0.0.1:37401/cb?state=xyz",
		"http://127.0.0.1:37401/cb?error=access_denied",
		"http://127.0.0.1:37401/cb?error=access_denied&error_description=" + canary,
		"http://127.0.0.1:37401/cb?error=%1B%5B31mhax",
		"foo=bar",
		"code=abc def",
		"http://127.0.0.1:37401/cb#code=abc&state=s",
		"http://127.0.0.1:37401/cb?state=xyz#code=abc",
		"http://127.0.0.1:37401/cb?error=access_denied&code=abc",
	}
	for i, in := range inputs {
		t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
			wantCode, wantState, wantErr := mcp.ParseAuthPaste(in)

			e := newMCPTestEnv(t)
			seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
			withMCPAuthInput(t, in)
			flow := &fakeMCPAuthFlow{url: "https://as.example/authorize?state=st-1"}
			installMCPAuthSeam(t, flow, nil)

			out, _ := runMCPSub(t, runMCPAuth, "api", "--manual")

			if strings.Contains(out, canary) {
				t.Fatalf("a canary from the paste reached the CLI output:\n%s", out)
			}
			if wantErr != nil {
				if len(flow.submitted) != 0 {
					t.Fatalf("the engine refuses %q (%v) but the CLI submitted %v", in, wantErr, flow.submitted)
				}
				return
			}
			if len(flow.submitted) != 1 || flow.submitted[0] != [2]string{wantCode, wantState} {
				t.Fatalf("engine says (%q, %q) for %q, CLI submitted %v", wantCode, wantState, in, flow.submitted)
			}
		})
	}
}

// TestMCPAuth_ManualPasteGrammarLiteralRows is the INDEPENDENT half of the
// paste-grammar pair kept in this file. Its sibling,
// TestMCPAuth_ManualMatchesEnginePasteGrammar, derives its expectations from
// mcp.ParseAuthPaste itself, so it proves the CLI submits and refuses exactly
// what the engine answered — but it stays GREEN through a regression INSIDE the
// engine's paste rules, because both sides of that assertion move together. A
// reviewer's mutation test proved the gap: narrowing the engine's query
// pre-check to "the chosen substring only" fails the pkg/mcp and pkg/tui tables
// and left this package green.
//
// This test closes it. Every row below carries a HARD-CODED verdict taken from
// the frozen grammar (.lele/tasks/mcp-oauth-paste-grammar.md) — the expected
// pair is never computed by calling mcp.ParseAuthPaste — so a mutation anywhere
// on the path (a rule inside ParseAuthPaste, or the CLI's mapping of an outcome
// to a sentence) turns it red.
//
// It goes through the SAME path as the sibling and as the neighbouring manual
// tests: --manual over the fake flow (fakeMCPAuthFlow), through the substituted
// startMCPAuthFlow seam, with a strings.Reader for stdin. No network, no
// terminal (the fake flow never dials anything).
//
// The sentences are the CLI's own, and two of them are REUSED rather than
// retyped: errMCPAuthBadInput is the ONE sentence the CLI prints for both
// engine outcomes that carry no usable code (`unrecognized` and `no-code` share
// it on purpose — see mcpAuthPasteError), and the success line is shared with
// the loopback path.
func TestMCPAuth_ManualPasteGrammarLiteralRows(t *testing.T) {
	// The verdicts a row may carry: submitted, or one of the two engine
	// refusals the CLI maps to its single "no usable code" sentence.
	const (
		pasteSubmitted    = "submitted"    // ok(code, state) ⇒ Submit
		pasteUnrecognized = "unrecognized" // ErrAuthPasteUnrecognized ⇒ refused
		pasteNoCode       = "no-code"      // ErrAuthPasteNoCode ⇒ refused
	)
	// The engine's input bound is 8192 bytes (authPasteMaxInputLen, unexported):
	// exactly 8192 is still a paste, 8193 is refused. It is a BYTE bound.
	const inputBound = 8192
	longCode := strings.Repeat("a", inputBound)

	tests := []struct {
		name string
		in   string
		kind string
		// want is the (code, state) pair Submit must receive. It is the zero
		// value on every refusal, because the engine answers ("", "") with every
		// error and nothing may be submitted.
		want [2]string
	}{
		{
			name: "01 query string",
			in:   "?code=abc&state=xyz",
			kind: pasteSubmitted,
			want: [2]string{"abc", "xyz"},
		},
		{
			// The query carries no code/error, so the FRAGMENT is the chosen
			// substring: the answer is code=abc and NO state — the query's own
			// `state=xyz` is consulted only to decide the fallback, and is then
			// discarded with the rest of the unused query.
			name: "02 fragment chosen, state comes from the fragment only",
			in:   "http://127.0.0.1:37401/cb?state=xyz#code=abc",
			kind: pasteSubmitted,
			want: [2]string{"abc", ""},
		},
		{
			// THE regression row: the query is scanned ALWAYS, even though the
			// fragment is what ends up being used. A malformed escape in the
			// unused query is still a refusal — deciding otherwise would be a
			// different answer per decoder language.
			name: "03 malformed escape in the query, fragment would be chosen",
			in:   "http://127.0.0.1:37401/cb?state=%zz#code=abc",
			kind: pasteUnrecognized,
		},
		{
			name: "04 trailing percent in the query",
			in:   "http://127.0.0.1:37401/cb?code=abc%#code=xyz",
			kind: pasteUnrecognized,
		},
		{
			// The CHOSEN fragment is scanned too.
			name: "05 malformed escape in the chosen fragment",
			in:   "http://127.0.0.1:37401/cb?state=x#code=%zz",
			kind: pasteUnrecognized,
		},
		{
			name: "06 percent-decoded code",
			in:   "http://127.0.0.1:37401/cb?code=a%2Bb",
			kind: pasteSubmitted,
			want: [2]string{"a+b", ""},
		},
		{
			name: "07 plus decodes to a space",
			in:   "http://127.0.0.1:37401/cb?code=a+b",
			kind: pasteSubmitted,
			want: [2]string{"a b", ""},
		},
		{
			// A URL that ARRIVED without a code: the engine's `no-code`, which
			// the CLI deliberately words exactly like `unrecognized`.
			name: "08 empty code parameter",
			in:   "http://127.0.0.1:37401/cb?code=",
			kind: pasteNoCode,
		},
		{
			name: "09 bound: exactly 8192 bytes is still a paste",
			in:   longCode,
			kind: pasteSubmitted,
			want: [2]string{longCode, ""},
		},
		{
			name: "10 bound: one byte over is refused",
			in:   strings.Repeat("a", inputBound+1),
			kind: pasteUnrecognized,
		},
		{
			// The CHOSEN fragment is scanned for malformed escapes — the same
			// rule row 05 states, pinned here in a form a mutation can see. The
			// fragment below carries a VALID code, so an engine that dropped its
			// fragment pre-check would not refuse it: Go's ParseQuery drops only
			// the bad `state` pair and keeps `code=abc`, and the paste would be
			// ACCEPTED and submitted. Row 05 cannot see that regression — its
			// fragment carries no usable code either way, so the engine answers
			// `unrecognized` (with the check) or the CLI-equivalent `no-code`
			// (without it), and this CLI renders and exits identically for both.
			// Here the difference is visible: refused (unrecognized) versus
			// submitted `abc`.
			name: "11 malformed escape in the chosen fragment, valid code",
			in:   "http://127.0.0.1:37401/cb?state=x#code=abc&state=%zz",
			kind: pasteUnrecognized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMCPTestEnv(t)
			seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")
			withMCPAuthInput(t, tt.in)
			flow := &fakeMCPAuthFlow{url: "https://as.example/authorize?state=st-1"}
			installMCPAuthSeam(t, flow, nil)

			out, code := runMCPSub(t, runMCPAuth, "api", "--manual")

			// Every --manual exit path releases the flow (the deferred Cancel),
			// refusals included: the loop re-prompts after a refusal and then
			// hits EOF, which is still an exit through the same defer.
			if flow.cancels != 1 {
				t.Errorf("Cancel calls = %d, want 1 (no path may leak a flow):\n%s", flow.cancels, out)
			}

			if tt.kind == pasteSubmitted {
				if code != 0 {
					t.Fatalf("exit = %d, want 0 for %q:\n%s", code, tt.in, out)
				}
				if len(flow.submitted) != 1 {
					t.Fatalf("Submit called %d time(s), want 1 (%v)", len(flow.submitted), flow.submitted)
				}
				if flow.submitted[0] != tt.want {
					t.Errorf("Submit got %v, want %v for %q", flow.submitted[0], tt.want, tt.in)
				}
				if !strings.Contains(out, `✓ "api" authenticated; the gateway picks it up on the next reload`) {
					t.Errorf("missing the success line:\n%s", out)
				}
				return
			}

			// A refused row submits NOTHING and says so with the CLI's shared
			// sentence: `unrecognized` and `no-code` are distinct outcomes in
			// the engine but one sentence here, by design.
			if code != mcpAuthExitFailure {
				t.Fatalf("refusal exit = %d, want %d for %q:\n%s", code, mcpAuthExitFailure, tt.in, out)
			}
			if len(flow.submitted) != 0 {
				t.Fatalf("the CLI submitted %v for %q, want nothing (verdict %s)", flow.submitted, tt.in, tt.kind)
			}
			if !strings.Contains(out, errMCPAuthBadInput.Error()) {
				t.Errorf("want the shared refusal sentence %q in:\n%s", errMCPAuthBadInput.Error(), out)
			}
			if strings.Contains(out, "✓") {
				t.Errorf("a refusal reported success:\n%s", out)
			}
		})
	}
}

// TestParseMCPAuthArgs covers the flag surface: defaults, both value flags, both
// booleans, and every rejection.
func TestParseMCPAuthArgs(t *testing.T) {
	a, err := parseMCPAuthArgs([]string{"api"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.name != "api" || a.agent != "main" || a.noBrowser || a.manual || a.port != 0 {
		t.Fatalf("defaults wrong: %+v", a)
	}
	if a.timeout != mcpAuthDefaultTimeout {
		t.Fatalf("default timeout = %v, want %v", a.timeout, mcpAuthDefaultTimeout)
	}

	a, err = parseMCPAuthArgs([]string{"--manual", "--no-browser", "--agent", "worker", "--timeout", "90s", "--port", "8080", "api"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !a.manual || !a.noBrowser || a.agent != "worker" || a.port != 8080 || a.timeout != 90*time.Second {
		t.Fatalf("parse: %+v", a)
	}

	if a, err := parseMCPAuthArgs([]string{"api", "--port", "0"}); err != nil || a.port != 0 {
		t.Fatalf("--port 0 (ephemeral) rejected: %+v %v", a, err)
	}

	for _, tt := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing name", args: nil, wantErr: "missing server name"},
		{name: "extra positional", args: []string{"a", "b"}, wantErr: "unexpected argument"},
		{name: "slash name", args: []string{"a/b"}, wantErr: "must not contain '/'"},
		{name: "unknown flag", args: []string{"api", "--bogus"}, wantErr: "unknown flag --bogus"},
		{name: "no-browser with a value", args: []string{"api", "--no-browser=yes"}, wantErr: "does not take a value"},
		{name: "bad timeout", args: []string{"api", "--timeout", "bogus"}, wantErr: "invalid --timeout"},
		{name: "zero timeout", args: []string{"api", "--timeout", "0s"}, wantErr: "must be positive"},
		{name: "negative timeout", args: []string{"api", "--timeout", "-5s"}, wantErr: "must be positive"},
		{name: "bad port", args: []string{"api", "--port", "http"}, wantErr: "invalid --port"},
		{name: "port too large", args: []string{"api", "--port", "70000"}, wantErr: "between 0 and 65535"},
		{name: "negative port", args: []string{"api", "--port", "-1"}, wantErr: "between 0 and 65535"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseMCPAuthArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestMCPAuth_UsageErrorPrintsTheUsageLine pins the parse-error surface: the
// auth usage line is printed with the failure (it lives in mcpAuthUsage, since
// mcpUsage is the table the docs guard ties to docs/mcp.md).
func TestMCPAuth_UsageErrorPrintsTheUsageLine(t *testing.T) {
	newMCPTestEnv(t)
	out, code := runMCPSub(t, runMCPAuth, "api", "--bogus")
	if code != 1 {
		t.Fatalf("usage error exit = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "Usage: "+mcpAuthUsage["auth"]) {
		t.Errorf("missing the auth usage line:\n%s", out)
	}
}

// TestMCPAuth_UsesTheSharedResolution: `auth` must refuse exactly what `test`
// refuses (unknown name, invalid entry, disable stub) with the same messages,
// and must resolve the DEFINITION underneath a disable stub — the shared
// mcpResolveTarget exists for that.
func TestMCPAuth_UsesTheSharedResolution(t *testing.T) {
	e := newMCPTestEnv(t)
	e.seedThreeLayers(t)
	withMCPAuthInput(t, "")
	seam := installMCPAuthSeam(t, &fakeMCPAuthFlow{url: "https://as.example/authorize"}, nil)

	out, code := runMCPSub(t, runMCPAuth, "nosuch")
	if code != mcpAuthExitFailure || !strings.Contains(out, `✗ unknown MCP server "nosuch"`) {
		t.Fatalf("unknown auth exit=%d out=%s", code, out)
	}

	out, code = runMCPSub(t, runMCPAuth, "charlie")
	if code != mcpAuthExitFailure {
		t.Fatalf("invalid auth exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, `✗ "charlie" is invalid:`) || !strings.Contains(out, "fix with: lele mcp edit charlie") {
		t.Errorf("auth must explain an invalid entry the way test does:\n%s", out)
	}

	writeMCPTestFile(t, e.agentFile(), `{"mcpServers":{"stub":{"disabled":true}}}`)
	out, code = runMCPSub(t, runMCPAuth, "stub")
	if code != mcpAuthExitFailure || !strings.Contains(out, `"stub" is only a disable stub`) {
		t.Fatalf("stub auth exit=%d out=%s", code, out)
	}
	if len(seam.calls) != 0 {
		t.Errorf("a refused entry reached the engine %d time(s)", len(seam.calls))
	}

	// A disabled server with a real definition under it: the note is printed for
	// auth's own verb and the DEFINITION is what gets authorized.
	seedMCPRemoteAuthServer(t, e, "off", "https://mcp.example.com/mcp")
	writeMCPTestFile(t, e.agentFile(), `{"mcpServers":{"off":{"disabled":true}}}`)
	out, code = runMCPSub(t, runMCPAuth, "off")
	if code != 0 {
		t.Fatalf("disabled auth exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, `⊘ "off" is disabled in the agent layer — authorizing it directly (the runtime will not load it)`) {
		t.Errorf("missing the disabled note:\n%s", out)
	}
	if len(seam.calls) != 1 || seam.calls[0].srv.URL != "https://mcp.example.com/mcp" {
		t.Errorf("the definition under the stub must be authorized: %+v", seam.calls)
	}
}

// ---------------------------------------------------------------------------
// logout
// ---------------------------------------------------------------------------

func TestParseMCPLogoutArgs(t *testing.T) {
	a, err := parseMCPLogoutArgs([]string{"api"})
	if err != nil || a.name != "api" || a.agent != "main" {
		t.Fatalf("parse: %+v %v", a, err)
	}
	if a, err := parseMCPLogoutArgs([]string{"--agent", "worker", "api"}); err != nil || a.agent != "worker" {
		t.Fatalf("--agent parse: %+v %v", a, err)
	}
	for _, tt := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing name", args: nil, wantErr: "missing server name"},
		{name: "extra positional", args: []string{"a", "b"}, wantErr: "unexpected argument"},
		{name: "slash name", args: []string{"a/b"}, wantErr: "must not contain '/'"},
		{name: "unknown flag", args: []string{"api", "--layer", "global"}, wantErr: "unknown flag --layer"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseMCPLogoutArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestMCPLogout_ClearsTheStore: the login is gone, the file with it (mcp-auth
// removes the file once it is empty), and the report names the server.
func TestMCPLogout_ClearsTheStore(t *testing.T) {
	e := newMCPTestEnv(t)
	const srvURL = "https://mcp.example.com/mcp"
	seedMCPRemoteAuthServer(t, e, "api", srvURL)
	store := mcp.NewAuthStore(e.leleDir)
	if err := store.TokenStore("api", srvURL).SaveToken(context.Background(),
		&transport.Token{AccessToken: "secret-token", TokenType: "Bearer", RefreshToken: "r"}); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	out, code := runMCPSub(t, runMCPLogout, "api")
	if code != 0 {
		t.Fatalf("logout exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, `✓ forgot the stored credentials and registration for "api"`) {
		t.Errorf("missing the forgotten-credentials line:\n%s", out)
	}
	if _, ok := mcp.NewAuthStore(e.leleDir).Record("api"); ok {
		t.Error("the record survived logout")
	}
	if strings.Contains(out, "secret-token") || strings.Contains(out, "Bearer") {
		t.Errorf("logout printed a secret:\n%s", out)
	}
}

// TestMCPLogout_EmptyStoreIsSuccess: nothing was stored, so the requested state
// already holds — exit 0, and the message says so (the choice is deliberate).
func TestMCPLogout_EmptyStoreIsSuccess(t *testing.T) {
	e := newMCPTestEnv(t)
	seedMCPRemoteAuthServer(t, e, "api", "https://mcp.example.com/mcp")

	out, code := runMCPSub(t, runMCPLogout, "api")
	if code != 0 {
		t.Fatalf("empty logout exit = %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, `✓ nothing was stored for "api" — nothing to forget`) {
		t.Errorf("the no-op must be stated explicitly:\n%s", out)
	}
	if !strings.Contains(out, "still a success") {
		t.Errorf("the no-op must state the exit-code choice:\n%s", out)
	}

	// Idempotent: a second logout reports the same thing, still exit 0.
	out2, code2 := runMCPSub(t, runMCPLogout, "api")
	if code2 != 0 || !strings.Contains(out2, "nothing was stored") {
		t.Fatalf("second logout exit=%d out=%s", code2, out2)
	}
}

// TestMCPLogout_UnknownServerIsStillLogoutable: the store is keyed by NAME, so
// a server already removed from mcp.json must still be forgettable.
func TestMCPLogout_UnknownServerIsStillLogoutable(t *testing.T) {
	e := newMCPTestEnv(t)
	store := mcp.NewAuthStore(e.leleDir)
	if err := store.TokenStore("gone", "https://mcp.example.com/mcp").SaveToken(context.Background(),
		&transport.Token{AccessToken: "t", TokenType: "Bearer"}); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	out, code := runMCPSub(t, runMCPLogout, "gone")
	if code != 0 || !strings.Contains(out, "forgot the stored credentials") {
		t.Fatalf("logout of a removed server exit=%d out=%s", code, out)
	}
	if _, ok := mcp.NewAuthStore(e.leleDir).Record("gone"); ok {
		t.Error("the record survived logout")
	}
}

// ---------------------------------------------------------------------------
// add: the auth flags
// ---------------------------------------------------------------------------

// TestParseMCPAddArgs_AuthFlags: the flag matrix of the auth block, including
// every validation failure pkg/mcp's own (*ServerConfig).validate would raise —
// refused here, with a clear message, instead of surfacing later as a parse
// error on a file that was already written.
func TestParseMCPAddArgs_AuthFlags(t *testing.T) {
	a, err := parseMCPAddArgs([]string{"api", "--url", "https://x/mcp", "--auth", "oauth",
		"--scope", "read", "--scope", "write", "--client-id", "cid", "--client-secret", "${MY_TOKEN}",
		"--redirect-uri", "http://127.0.0.1:9000/cb", "--auth-server-metadata-url", "https://as.example/.well-known/oauth-authorization-server",
		"--protected-resource-metadata-url", "https://mcp.example.com/.well-known/oauth-protected-resource"})
	if err != nil {
		t.Fatalf("valid oauth block rejected: %v", err)
	}
	spec := a.authSpec()
	if spec == nil {
		t.Fatal("authSpec() = nil for a full auth block")
	}
	if spec.Type != "oauth" || spec.ClientID != "cid" || spec.ClientSecret != "${MY_TOKEN}" ||
		spec.RedirectURI != "http://127.0.0.1:9000/cb" ||
		spec.AuthServerMetadataURL != "https://as.example/.well-known/oauth-authorization-server" ||
		spec.ProtectedResourceMetadataURL != "https://mcp.example.com/.well-known/oauth-protected-resource" {
		t.Fatalf("auth spec = %+v", spec)
	}
	if strings.Join(spec.Scopes, ",") != "read,write" {
		t.Fatalf("scopes = %v, want read,write (order preserved)", spec.Scopes)
	}

	// No auth flag at all ⇒ no block, which is what keeps plain entries
	// byte-identical to what they were before this feature.
	plain, err := parseMCPAddArgs([]string{"fs", "--command", "npx"})
	if err != nil {
		t.Fatalf("plain entry rejected: %v", err)
	}
	if plain.authSpec() != nil {
		t.Errorf("authSpec() = %+v for an entry with no auth flag, want nil", plain.authSpec())
	}

	// An explicitly declared `none` IS a block: the reader distinguishes
	// "declared but unconfigured" from "nil".
	none, err := parseMCPAddArgs([]string{"api", "--url", "https://x/mcp", "--auth", "none"})
	if err != nil {
		t.Fatalf("--auth none rejected: %v", err)
	}
	if spec := none.authSpec(); spec == nil || spec.Type != "none" {
		t.Fatalf("--auth none spec = %+v, want a declared block with type none", spec)
	}

	for _, tt := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "unknown auth type",
			args:    []string{"api", "--url", "https://x/mcp", "--auth", "basic"},
			wantErr: `unknown --auth "basic"`,
		},
		{
			name:    "oauth without a url",
			args:    []string{"fs", "--command", "npx", "--auth", "oauth"},
			wantErr: "auth flags require --url",
		},
		{
			name:    "scopes without a url",
			args:    []string{"fs", "--command", "npx", "--scope", "read"},
			wantErr: "auth flags require --url",
		},
		{
			name:    "client id without a url",
			args:    []string{"fs", "--command", "npx", "--client-id", "cid"},
			wantErr: "auth flags require --url",
		},
		{
			name:    "secret without an id",
			args:    []string{"api", "--url", "https://x/mcp", "--auth", "oauth", "--client-secret", "s"},
			wantErr: "--client-secret requires --client-id",
		},
		{
			name:    "secret alone",
			args:    []string{"api", "--url", "https://x/mcp", "--client-secret", "s"},
			wantErr: "--client-secret requires --client-id",
		},
		{
			name:    "flag without a value",
			args:    []string{"api", "--url", "https://x/mcp", "--client-id"},
			wantErr: "flag --client-id requires a value",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseMCPAddArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestMCPAdd_AuthFlagsCoverTheSpec is the drift guard for the flag surface:
// every field mcp.AuthSpec can carry must be reachable from `lele mcp add`
// (through the real parser AND into the spec), so a field added to the spec
// cannot quietly be unsettable from the CLI. The mapping is written out field
// by field — `scopes` is the repeatable `--scope`, and `client_secret` needs
// the `--client-id` it belongs to — because it is the whole surface, not a
// sample of it.
func TestMCPAdd_AuthFlagsCoverTheSpec(t *testing.T) {
	tests := []struct {
		field  string // mcp.AuthSpec json tag
		flag   string
		value  string
		extras []string // flags the entry needs for validate() to accept it
		check  func(*mcp.AuthSpec) bool
	}{
		{"type", "--auth", "oauth", nil, func(s *mcp.AuthSpec) bool { return s.Type == "oauth" }},
		{"scopes", "--scope", "read", nil, func(s *mcp.AuthSpec) bool { return reflect.DeepEqual(s.Scopes, []string{"read"}) }},
		{"client_id", "--client-id", "cid", nil, func(s *mcp.AuthSpec) bool { return s.ClientID == "cid" }},
		{"client_secret", "--client-secret", "sec", []string{"--client-id", "cid"}, func(s *mcp.AuthSpec) bool { return s.ClientSecret == "sec" }},
		{"redirect_uri", "--redirect-uri", "http://127.0.0.1:1/cb", nil, func(s *mcp.AuthSpec) bool { return s.RedirectURI == "http://127.0.0.1:1/cb" }},
		{"auth_server_metadata_url", "--auth-server-metadata-url", "https://as.example/meta", nil, func(s *mcp.AuthSpec) bool { return s.AuthServerMetadataURL == "https://as.example/meta" }},
		{"protected_resource_metadata_url", "--protected-resource-metadata-url", "https://pr.example/meta", nil, func(s *mcp.AuthSpec) bool { return s.ProtectedResourceMetadataURL == "https://pr.example/meta" }},
	}
	if len(tests) != 7 {
		t.Fatalf("the AuthSpec surface has 7 fields, the table covers %d", len(tests))
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			if !strings.Contains(mcpUsage["add"], tt.flag) {
				t.Errorf("the add usage line omits %s", tt.flag)
			}
			for _, form := range [][]string{{tt.flag, tt.value}, {tt.flag + "=" + tt.value}} {
				args := append([]string{"api", "--url", "https://x/mcp"}, tt.extras...)
				args = append(args, form...)
				a, err := parseMCPAddArgs(args)
				if err != nil {
					t.Fatalf("parseMCPAddArgs(%v): %v", args, err)
				}
				spec := a.authSpec()
				if spec == nil || !tt.check(spec) {
					t.Errorf("parseMCPAddArgs(%v) produced %+v, which does not carry the flag's value", args, spec)
				}
			}
		})
	}
}

// TestMCPAdd_WritesTheAuthBlock pins the effective JSON the writer produces:
// canonical field order, only non-zero fields, and the entry still validates
// when the reader parses it back (the writer runs the reader's own validate).
func TestMCPAdd_WritesTheAuthBlock(t *testing.T) {
	e := newMCPTestEnv(t)
	out, code := runMCPSub(t, runMCPAdd, "api", "--url", "https://mcp.example.com/mcp",
		"--auth", "oauth", "--scope", "read", "--scope", "write",
		"--client-id", "cid", "--redirect-uri", "http://127.0.0.1:9000/cb")
	if code != 0 {
		t.Fatalf("add exit = %d, want 0:\n%s", code, out)
	}
	// A fresh file: the writer emits no trailing newline of its own, and with
	// no previous bytes to preserve there is none.
	want := `{"mcpServers":{"api":{"url":"https://mcp.example.com/mcp","auth":{` +
		`"type":"oauth","scopes":["read","write"],"client_id":"cid","redirect_uri":"http://127.0.0.1:9000/cb"}}}}`
	if got := readMCPTestFile(t, e.globalFile()); got != want {
		t.Errorf("written bytes:\n got: %s\nwant: %s", got, want)
	}
	f, err := mcp.ParseFile(e.globalFile())
	if err != nil {
		t.Fatalf("ParseFile(result): %v", err)
	}
	if err := f.EntryErrors["api"]; err != nil {
		t.Errorf("the written entry does not validate: %v", err)
	}
}

// TestMCPAdd_ClientSecretStaysVerbatim is the CLI half of the writer's
// no-expansion contract: a literal "${VAR}" in a flag value must be written
// BYTE-FOR-BYTE, never resolved from the environment — even when the variable
// is set in this process.
func TestMCPAdd_ClientSecretStaysVerbatim(t *testing.T) {
	e := newMCPTestEnv(t)
	t.Setenv("MY_TOKEN", "resolved-secret-value")

	out, code := runMCPSub(t, runMCPAdd, "api", "--url", "https://mcp.example.com/mcp",
		"--auth", "oauth", "--client-id", "cid", "--client-secret", "${MY_TOKEN}")
	if code != 0 {
		t.Fatalf("add exit = %d, want 0:\n%s", code, out)
	}
	got := readMCPTestFile(t, e.globalFile())
	if !strings.Contains(got, `"client_secret":"${MY_TOKEN}"`) {
		t.Errorf("the literal ${MY_TOKEN} was not written verbatim:\n%s", got)
	}
	if strings.Contains(got, "resolved-secret-value") {
		t.Errorf("the flag value was EXPANDED before it reached the disk:\n%s", got)
	}
	// The reader still expands it at load time — the reference is live config,
	// not dead text.
	f, err := mcp.ParseFile(e.globalFile())
	if err != nil {
		t.Fatalf("ParseFile(result): %v", err)
	}
	if spec := f.MCPServers["api"].Auth; spec == nil || spec.ClientSecret != "resolved-secret-value" {
		t.Errorf("ParseFile did not expand the reference: %+v", spec)
	}
}

// TestMCPAdd_StdioRefusesOAuthAuth pins the CLI-side refusal for the shape the
// reader would reject anyway ("auth block requires a url"): the command must
// say so before writing anything at all.
func TestMCPAdd_StdioRefusesOAuthAuth(t *testing.T) {
	e := newMCPTestEnv(t)
	out, code := runMCPSub(t, runMCPAdd, "fs", "--command", "npx", "--auth", "oauth")
	if code != 1 {
		t.Fatalf("stdio+oauth exit = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "auth flags require --url") || !strings.Contains(out, "Usage:") {
		t.Errorf("missing the refusal / usage:\n%s", out)
	}
	if _, err := os.Stat(e.globalFile()); err == nil {
		t.Errorf("a refused add wrote the file:\n%s", readMCPTestFile(t, e.globalFile()))
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
//
// The auth family (mcpAuthUsage) is covered by the SAME guard: it is a second
// table only because its documentation is owned by the docs surface, not
// because it is a lesser part of the surface. Its help lines are asserted
// against mcpUsageLine, the one lookup both tables go through.
// TestMCPDocs_AuthSurface pins the C1/C2/C9 doc half: docs/mcp.md must describe
// the ONE paste grammar the CLI now delegates to (the shared engine helper, not
// a private copy), the retry/quit/bound behaviour of --manual, and the two
// bracket markers the `list` row and the `show` verdict speak — the same words
// the TUI and the WebUI render. A doc that drifts here would send an operator
// (or a future re-implementation) back to a per-surface rule.
func TestMCPDocs_AuthSurface(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "mcp.md"))
	if err != nil {
		t.Fatalf("read docs/mcp.md: %v", err)
	}
	text := string(doc)
	for _, want := range []string{
		// The shared rule, named.
		"pkg/mcp.ParseAuthPaste",
		// The markers/list vocabulary, byte-identical to the code.
		mcpAuthMarkerRequired,
		mcpAuthMarkerAuthenticated,
		"auth state:",
		"auth method:",
		// The ONE render rule's precedence (V4), stated at BOTH doc sites: a
		// stored credential is reported as authenticated even when the entry
		// declares no requirement, whose verdict is what the store holds.
		"even when the entry declares no requirement",
		// The declared auth block, keys-side only.
		"auth:",
		"scopes:",
		"client id:",
		"never printed",
		// The four accepted paste forms, including the two-token convenience.
		"bare authorization code",
		"two whitespace-separated tokens",
		"#fragment",
		// The discovery path the probe documents (C2's mitigation).
		"authentication required",
		// The --manual loop's exit paths.
		":q",
		"no input on stdin",
		"too many refused pastes",
		"same PKCE verifier",
		// ... and the loop SHAPE (V8): one line PER ATTEMPT, the prompt that
		// is repeated for every attempt (pinned by value, so it stays
		// byte-identical to what the CLI prints), and the attempt bound.
		"read **one pasted line per attempt** from stdin",
		mcpAuthManualPrompt,
		"bound of 10 attempts",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/mcp.md no longer documents %q", want)
		}
	}

	// The precedence must be stated at BOTH sites the rule governs — the
	// `list` marker paragraph and the `show` verdict one — so neither reader
	// can come away with the retired `!Required`-first order.
	const precedence = "even when the entry declares no requirement"
	if got := strings.Count(text, precedence); got < 2 {
		t.Errorf("docs/mcp.md states %q %d time(s), want 2: the `list` paragraph and the `show` paragraph must both carry the rule", precedence, got)
	}
}

func TestMCPDocs_UsageCoverage(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "mcp.md"))
	if err != nil {
		t.Fatalf("read docs/mcp.md: %v", err)
	}

	// Every subcommand the dispatcher knows, from both usage tables.
	subs := map[string]bool{}
	for sub := range mcpUsage {
		subs[sub] = true
	}
	for sub := range mcpAuthUsage {
		subs[sub] = true
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
	for sub := range subs {
		if !documented[sub] {
			t.Errorf("docs/mcp.md never shows `lele mcp %s`", sub)
		}
	}
	for sub := range documented {
		if !subs[sub] {
			t.Errorf("docs/mcp.md advertises `lele mcp %s`, which the dispatcher does not know", sub)
		}
	}

	help, _ := runMCPSub(t, func(w io.Writer, _ []string) int { writeMCPHelp(w); return 0 })
	for sub := range subs {
		if !strings.Contains(help, "lele mcp "+sub+" ") {
			t.Errorf("writeMCPHelp does not document `lele mcp %s`", sub)
		}
		// The usage line is what a parse error prints; the help itself may
		// abbreviate. mcpUsageLine is the ONE lookup both tables go through, so
		// asserting on it covers the auth family and the original table alike.
		line := mcpUsageLine(sub)
		if line == "" {
			t.Errorf("mcpUsageLine(%q) is empty", sub)
			continue
		}
		var buf bytes.Buffer
		mcpUsageError(&buf, sub, errors.New("boom"))
		if !strings.Contains(buf.String(), "Usage: "+line) {
			t.Errorf("a %s usage error does not print the usage line %q:\n%s", sub, line, buf.String())
		}
	}
	if !strings.Contains(help, "lele mcp test <name>") {
		t.Errorf("writeMCPHelp must show the test usage line:\n%s", help)
	}
	// The auth usage line is the one that must survive a copy/paste into a
	// headless shell: assert the flags the command actually parses are on it.
	for _, flag := range []string{"--no-browser", "--manual", "--timeout", "--port"} {
		if !strings.Contains(mcpAuthUsage["auth"], flag) {
			t.Errorf("the auth usage line dropped %s: %q", flag, mcpAuthUsage["auth"])
		}
		if !strings.Contains(help, flag) {
			t.Errorf("writeMCPHelp does not mention the auth flag %s", flag)
		}
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
	for sub := range subs {
		if !strings.Contains(line, sub) {
			t.Errorf("the parent help line omits %q: %q", sub, line)
		}
	}
}
