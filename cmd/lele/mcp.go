package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/routing"
)

// The `lele mcp` family is a thin, human-facing front-end over pkg/mcp's data
// layer. It reads the merged inventory (mcp.ReadInventory) and mutates the
// layer files through the byte-splice writers (mcp.UpsertEntry/DeleteEntry) —
// it never re-implements discovery, precedence or validation.
//
// Two invariants carried over from pkg/mcp:
//   - environment references are NEVER expanded here. Whatever the user typed
//     ("${TOKEN}") is written and shown verbatim; expansion is a load-time
//     concern (ParseFile).
//   - env/header VALUES are never printed. The inventory exposes KEY NAMES
//     only, and this file only ever prints those names (see mcpFormatKeys).
//
// Testability: mcpCmd is the only function that reads os.Args or calls
// os.Exit. Every subcommand is a runMCP* function taking (io.Writer, []string)
// and RETURNING an exit code, so tests drive the whole surface directly.

// State glyphs used by list/show, mirroring the house style (✓/✗/⊘).
const (
	mcpGlyphEnabled  = "●"
	mcpGlyphDisabled = "○"
	mcpGlyphInvalid  = "!"
)

// mcpDescriptionMax caps the DESCRIPTION column so a long blurb cannot push
// the other columns off the terminal. A fixed budget (rather than a tty
// probe) keeps the output byte-identical for tests and pipes.
const mcpDescriptionMax = 60

// mcpUsage documents each subcommand's positional shape; it is printed on a
// parse error so the user is never left guessing.
//
// `test` is the only line that names a SECOND command: a probe can end in
// "authentication required" rather than in a transport fault, and the fix is
// always the auth flow (`lele mcp auth <name>`).
var mcpUsage = map[string]string{
	"list":   "lele mcp list [--agent <id>] [--layer global|agent|project]",
	"show":   "lele mcp show <name> [--agent <id>]",
	"add":    "lele mcp add <name> (--command <bin> | --url <u>) [--arg v]… [--type http|sse] [--env K=V]… [--header K=V]… [--description d] [--layer global|agent|project] [--force] [--auth oauth|none] [--scope s]… [--client-id id] [--client-secret secret] [--redirect-uri uri] [--auth-server-metadata-url url] [--protected-resource-metadata-url url]",
	"remove": "lele mcp remove <name> [--agent <id>] [--layer global|agent|project] [--all]",
	"edit":   "lele mcp edit <name> [--agent <id>] [--layer global|agent|project]",
	"test":   "lele mcp test <name> [--agent <id>] [--timeout 30s]  (an OAuth server may need 'lele mcp auth <name>' first)",
}

// mcpAuthUsage holds the usage lines of the subcommands that drive the OAuth
// flow itself. They live in a SEPARATE table on purpose: mcpUsage's keys are
// the surface TestMCPDocs_UsageCoverage ties to docs/mcp.md AND to the
// `lele help` line, and the auth family's documentation is owned by the docs
// surface rather than by this file. Look both tables up through mcpUsageLine.
var mcpAuthUsage = map[string]string{
	"auth":   "lele mcp auth <name> [--agent <id>] [--no-browser] [--manual] [--timeout 5m] [--port <n>]",
	"logout": "lele mcp logout <name> [--agent <id>]",
}

// mcpUsageLine returns the usage line of sub, from either table ("" when the
// subcommand has none — only reachable from a programmer error).
func mcpUsageLine(sub string) string {
	if line, ok := mcpUsage[sub]; ok {
		return line
	}
	return mcpAuthUsage[sub]
}

// mcpCmd is the os.Args dispatcher. It mirrors modelsCmd's shape (bare
// `lele mcp` prints help and exits 0; an unknown subcommand prints help and
// exits 1) while delegating all real work to the testable runMCP* functions.
//
// Console logs are suppressed: loading the config (and the config-layer
// warnings a bare or half-configured install produces) would otherwise bury
// the command's own output. The command communicates exclusively through
// fmt.Fprintf.
func mcpCmd() {
	logger.SetQuiet(true)
	if code := runMCP(os.Stdout, os.Args[2:]); code != 0 {
		os.Exit(code)
	}
}

// runMCP is the testable dispatcher: it returns the exit code instead of
// calling os.Exit.
func runMCP(w io.Writer, args []string) int {
	if len(args) == 0 {
		writeMCPHelp(w)
		return 0
	}
	switch args[0] {
	case "list":
		return runMCPList(w, args[1:])
	case "show":
		return runMCPShow(w, args[1:])
	case "add":
		return runMCPAdd(w, args[1:])
	case "remove":
		return runMCPRemove(w, args[1:])
	case "edit":
		return runMCPEdit(w, args[1:])
	case "test":
		return runMCPTest(w, args[1:])
	case "auth":
		return runMCPAuth(w, args[1:])
	case "logout":
		return runMCPLogout(w, args[1:])
	case "help", "--help", "-h":
		writeMCPHelp(w)
		return 0
	default:
		fmt.Fprintf(w, "Unknown mcp command: %s\n", args[0])
		writeMCPHelp(w)
		return 1
	}
}

// mcpHelp prints the family help to stdout (the house-style no-arg help
// function, so help_test's captureOutput wrapper can drive it).
func mcpHelp() {
	writeMCPHelp(os.Stdout)
}

// writeMCPHelp prints the family help. The --agent line names the effective
// default (the configured default agent, not the literal "main") so the help
// cannot advertise an ID the config does not have. The default is resolved once
// per call and only the resolved ID is interpolated — the rest of the text is a
// constant, and the format string holds no other verbs.
func writeMCPHelp(w io.Writer) {
	fmt.Fprintf(w, mcpHelpText, defaultMCPAgentID())
}

const mcpHelpText = `lele mcp — MCP servers

Usage:
  lele mcp list [--agent <id>] [--layer global|agent|project]
  lele mcp show <name> [--agent <id>]
  lele mcp add <name> (--command <bin> | --url <u>) [flags]
  lele mcp remove <name> [--layer global|agent|project] [--all]
  lele mcp edit <name> [--layer global|agent|project]
  lele mcp test <name> [--timeout 30s]
  lele mcp auth <name> [--agent <id>] [--no-browser] [--manual] [--timeout 5m] [--port <n>]
  lele mcp logout <name> [--agent <id>]

Common flags:
  --agent <id>            Agent whose workspace provides the agent layer (default: %s)
  --                      end of flags (for a server name starting with '-')

Layers (low → high precedence; highest wins, whole-entry replace):
  global                  <config-dir>/mcp.json
  agent                   <agent-workspace>/mcp.json
  project                 <cwd>/.lele/mcp.json

add flags:
  --command <bin>         stdio launcher (mutually exclusive with --url)
  --arg <v>               stdio argument (repeatable, order preserved)
  --url <u>               remote endpoint (mutually exclusive with --command)
  --type http|sse         remote transport (only valid with --url)
  --description <d>       what the LLM sees before the server is loaded
  --env KEY=VALUE         child environment variable (repeatable)
  --header KEY=VALUE      remote HTTP header (repeatable)
  --layer <l>             target layer (default: global)
  --force                 replace an existing entry in the target layer
  --auth oauth|none       opt a remote entry into the OAuth 2.1 flow
  --scope <s>             OAuth scope to request (repeatable)
  --auth-server-metadata-url <u>
                          override authorization-server metadata discovery
  --protected-resource-metadata-url <u>
                          override protected-resource metadata discovery
  --client-id <id>        pre-registered client id (DCR fills it when empty)
  --client-secret <s>     pre-registered client secret (requires --client-id)
  --redirect-uri <uri>    registered redirect uri (pre-registered clients)

auth flags:
  --no-browser            never launch a browser; the URL is printed instead
  --manual                paste the code/redirect URL on stdin (retry on a refused paste)
  --timeout <dur>         how long to wait for the login (default: 5m)
  --port <n>              pin the loopback callback port (0 = ephemeral)

Examples:
  lele mcp list
  lele mcp show filesystem
  lele mcp add filesystem --command npx --arg -y --arg @modelcontextprotocol/server-filesystem --arg /tmp
  lele mcp add api --url https://api.example.com/mcp --type http --header "Authorization=Bearer ${TOKEN}"
  lele mcp add api --url https://api.example.com/mcp --auth oauth
  lele mcp remove filesystem --all
  lele mcp edit api
  lele mcp test api
  lele mcp auth api --no-browser
  lele mcp logout api

edit opens one entry's JSON object in $VISUAL/$EDITOR (fallback: vi), validates
it when the editor exits and writes it back atomically; the file is left
untouched on an invalid entry, so it is also the repair path for a broken server.

test reports a remote server you are not signed in to as "authentication
required — run 'lele mcp auth <name>'"; once authenticated it probes normally.

list appends a trailing [needs auth] to a server that owes a login and
[authenticated] once one is stored; show prints the entry's auth block and the
same verdict as "auth state:". --manual takes one line per attempt: the full
redirect URL, the bare query string, a bare code, or "<code> <state>" — the same
rule the TUI uses (pkg/mcp.ParseAuthPaste). A refused paste is reported and asked
again on the same flow; ":q" gives up.

auth starts the OAuth 2.1 flow of a REMOTE server: it ALWAYS prints the
authorization URL first (the only way to finish on a headless host) and then
waits for the browser login — or, with --manual, reads the pasted
authorization code or full redirect URL from stdin. A refused paste leaves the
flow open: the prompt is shown again on the same flow, until it completes, the
input ends or ':q' is typed. The token is stored in the lele directory; the
gateway picks it up on the next reload.
logout forgets that server's token AND its dynamic registration.

No token, authorization code, verifier, client id or client secret is ever
printed.

Note: ${VAR} references are stored verbatim and expanded only when a server is
loaded; env/header VALUES are never printed.

Writes are guarded: a layer file outside the home directory, /tmp, /var/folders
or the current directory is refused before anything is written.
`

// ---------------------------------------------------------------------------
// Manual flag parsing
// ---------------------------------------------------------------------------

// mcpParseSpec declares the flags one subcommand accepts. Exactly one of the
// three maps holds a given flag name; an unlisted name is a usage error.
type mcpParseSpec struct {
	values  map[string]*string   // single-value flags
	repeats map[string]*[]string // repeatable flags (order preserved)
	bools   map[string]*bool     // boolean flags
}

// parseMCPFlags walks args in order, accepting flags anywhere before or after
// positionals. Both "--flag value" and "--flag=value" are understood. A bare
// "--" ends flag parsing: everything after it is positional (and the marker
// itself is dropped), which is how a server named "-dash" stays addressable —
// validEntryName accepts a leading dash, so the CLI must be able to name it.
// The returned slice holds the positional arguments in order.
func parseMCPFlags(args []string, spec mcpParseSpec) ([]string, error) {
	var pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			pos = append(pos, arg)
			continue
		}
		name, inline, hasInline := splitMCPFlag(arg)
		if dst, ok := spec.bools[name]; ok {
			if hasInline {
				return nil, fmt.Errorf("flag %s does not take a value", name)
			}
			*dst = true
			continue
		}
		if dst, ok := spec.values[name]; ok {
			v, next, ok := nextMCPValue(args, i, inline, hasInline)
			if !ok {
				return nil, fmt.Errorf("flag %s requires a value", name)
			}
			*dst = v
			i = next
			continue
		}
		if dst, ok := spec.repeats[name]; ok {
			v, next, ok := nextMCPValue(args, i, inline, hasInline)
			if !ok {
				return nil, fmt.Errorf("flag %s requires a value", name)
			}
			*dst = append(*dst, v)
			i = next
			continue
		}
		return nil, fmt.Errorf("unknown flag %s", name)
	}
	return pos, nil
}

// splitMCPFlag splits "--name=value" into its three parts; a flag without '='
// reports hasValue=false.
func splitMCPFlag(arg string) (name, value string, hasValue bool) {
	if i := strings.IndexByte(arg, '='); i >= 0 {
		return arg[:i], arg[i+1:], true
	}
	return arg, "", false
}

// nextMCPValue returns a flag's value, preferring the inline "--x=v" form and
// otherwise consuming the next argument verbatim (so "--arg -y" works). The
// returned index is the argument the caller should continue after.
func nextMCPValue(args []string, i int, inline string, hasInline bool) (value string, next int, ok bool) {
	if hasInline {
		return inline, i, true
	}
	if i+1 < len(args) {
		return args[i+1], i + 1, true
	}
	return "", i, false
}

// mcpUsageError prints a parse failure plus the offending subcommand's usage.
func mcpUsageError(w io.Writer, sub string, err error) {
	fmt.Fprintf(w, "✗ %v\n", err)
	fmt.Fprintf(w, "Usage: %s\n", mcpUsageLine(sub))
	fmt.Fprintln(w, "Run 'lele mcp --help' for details.")
}

// validMCPName mirrors pkg/mcp's validEntryName (non-empty, no '/'): the
// writer enforces it too, but checking here yields a usable usage error
// before any layer resolution happens.
func validMCPName(name string) error {
	if name == "" {
		return errors.New("server name must not be empty")
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("server name %q must not contain '/'", name)
	}
	return nil
}

// mcpValidLayer reports whether layer is one of mcp.Layers().
func mcpValidLayer(layer string) bool {
	for _, l := range mcp.Layers() {
		if l == layer {
			return true
		}
	}
	return false
}

// mcpLayerRank returns the precedence rank of a layer (higher wins), or -1.
func mcpLayerRank(layer string) int {
	for i, l := range mcp.Layers() {
		if l == layer {
			return i
		}
	}
	return -1
}

// mcpPairs turns repeated KEY=VALUE flags into a map. A pair without '=' or
// with an empty KEY is rejected; an empty value ("KEY=") is allowed.
//
// Error messages echo the KEY only — never the text after '=' — because that
// half holds the value and values may be secrets (the same rule `list` and
// `show` follow). A pair with no '=' at all is not echoed either: the whole
// token is then a malformed value whose key cannot be identified.
func mcpPairs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			return nil, errors.New("malformed pair: want KEY=VALUE")
		}
		if p[:eq] == "" {
			return nil, fmt.Errorf("malformed %q: KEY must not be empty", p[:eq])
		}
		out[p[:eq]] = p[eq+1:]
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Argument structs + parsers
// ---------------------------------------------------------------------------

type mcpListArgs struct {
	agent string
	layer string
}

func parseMCPListArgs(args []string) (mcpListArgs, error) {
	a := mcpListArgs{agent: defaultMCPAgentID()}
	pos, err := parseMCPFlags(args, mcpParseSpec{values: map[string]*string{
		"--agent": &a.agent,
		"--layer": &a.layer,
	}})
	if err != nil {
		return a, err
	}
	if len(pos) > 0 {
		return a, fmt.Errorf("unexpected argument %q", pos[0])
	}
	if a.layer != "" && !mcpValidLayer(a.layer) {
		return a, fmt.Errorf("unknown layer %q (want global, agent or project)", a.layer)
	}
	return a, nil
}

type mcpShowArgs struct {
	agent string
	name  string
}

func parseMCPShowArgs(args []string) (mcpShowArgs, error) {
	a := mcpShowArgs{agent: defaultMCPAgentID()}
	pos, err := parseMCPFlags(args, mcpParseSpec{values: map[string]*string{
		"--agent": &a.agent,
	}})
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, errors.New("missing server name")
	}
	if len(pos) > 1 {
		return a, fmt.Errorf("unexpected argument %q", pos[1])
	}
	a.name = pos[0]
	return a, validMCPName(a.name)
}

type mcpAddArgs struct {
	agent       string
	name        string
	command     string
	args        []string
	url         string
	typ         string
	description string
	env         []string
	headers     []string
	layer       string
	force       bool

	// Auth block of a REMOTE entry: ONE field per mcp.AuthSpec field, kept
	// exactly as the user spelled them, so the flag surface and the stored
	// block cannot drift apart (a field added to AuthSpec has an obvious home
	// here). authSpec() turns them into the mcp.AuthSpec the writer stores, or
	// nil when NO auth flag was given — which is what keeps today's entries
	// byte-identical (an absent block is omitted from the object; a declared
	// one is written even when it is empty).
	authType                     string
	scopes                       []string
	authServerMetadataURL        string
	protectedResourceMetadataURL string
	clientID                     string
	clientSecret                 string
	redirectURI                  string
}

// authSpec builds the entry's auth block from the flags, or nil when none of
// them was given. A value is never expanded or defaulted here: what the user
// typed ("${MY_TOKEN}") travels to the disk verbatim, exactly like env and
// header values.
func (a mcpAddArgs) authSpec() *mcp.AuthSpec {
	if a.authType == "" && len(a.scopes) == 0 && a.authServerMetadataURL == "" &&
		a.protectedResourceMetadataURL == "" && a.clientID == "" &&
		a.clientSecret == "" && a.redirectURI == "" {
		return nil
	}
	return &mcp.AuthSpec{
		Type:                         a.authType,
		Scopes:                       a.scopes,
		ClientID:                     a.clientID,
		ClientSecret:                 a.clientSecret,
		RedirectURI:                  a.redirectURI,
		AuthServerMetadataURL:        a.authServerMetadataURL,
		ProtectedResourceMetadataURL: a.protectedResourceMetadataURL,
	}
}

// entry builds the ServerConfig exactly as the flags spell it: no expansion,
// no defaulting beyond what mcp.UpsertEntry itself would apply.
func (a mcpAddArgs) entry() (mcp.ServerConfig, error) {
	env, err := mcpPairs(a.env)
	if err != nil {
		return mcp.ServerConfig{}, err
	}
	headers, err := mcpPairs(a.headers)
	if err != nil {
		return mcp.ServerConfig{}, err
	}
	return mcp.ServerConfig{
		Command:     a.command,
		Args:        a.args,
		Env:         env,
		URL:         a.url,
		Type:        a.typ,
		Headers:     headers,
		Auth:        a.authSpec(),
		Description: a.description,
	}, nil
}

func parseMCPAddArgs(args []string) (mcpAddArgs, error) {
	a := mcpAddArgs{agent: defaultMCPAgentID(), layer: mcp.LayerGlobal}
	pos, err := parseMCPFlags(args, mcpParseSpec{
		values: map[string]*string{
			"--agent":                           &a.agent,
			"--command":                         &a.command,
			"--url":                             &a.url,
			"--type":                            &a.typ,
			"--description":                     &a.description,
			"--layer":                           &a.layer,
			"--auth":                            &a.authType,
			"--auth-server-metadata-url":        &a.authServerMetadataURL,
			"--protected-resource-metadata-url": &a.protectedResourceMetadataURL,
			"--client-id":                       &a.clientID,
			"--client-secret":                   &a.clientSecret,
			"--redirect-uri":                    &a.redirectURI,
		},
		repeats: map[string]*[]string{
			"--arg":    &a.args,
			"--env":    &a.env,
			"--header": &a.headers,
			"--scope":  &a.scopes,
		},
		bools: map[string]*bool{"--force": &a.force},
	})
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, errors.New("missing server name")
	}
	if len(pos) > 1 {
		return a, fmt.Errorf("unexpected argument %q", pos[1])
	}
	a.name = pos[0]
	return a, a.validate()
}

// validate mirrors (*mcp.ServerConfig).validate — the rule mcp.UpsertEntry runs
// before it writes anything — so a mistake is reported as a usable usage error
// here instead of being discovered later as a parse failure on a file that was
// already written. The auth block is REMOTE-ONLY and every one of its rules is
// checked: the whole block requires a url, --auth takes oauth|none, and a
// secret needs the id it belongs to.
func (a mcpAddArgs) validate() error {
	if err := validMCPName(a.name); err != nil {
		return err
	}
	if !mcpValidLayer(a.layer) {
		return fmt.Errorf("unknown layer %q (want global, agent or project)", a.layer)
	}
	switch {
	case a.command == "" && a.url == "":
		return errors.New("one of --command or --url is required")
	case a.command != "" && a.url != "":
		return errors.New("--command and --url are mutually exclusive")
	}
	if a.typ != "" {
		if a.url == "" {
			return errors.New("--type is only valid with --url")
		}
		if a.typ != "http" && a.typ != "sse" {
			return fmt.Errorf("unknown type %q (want http or sse)", a.typ)
		}
	}
	switch a.authType {
	case "", "oauth", "none":
	default:
		return fmt.Errorf("unknown --auth %q (want oauth or none)", a.authType)
	}
	if a.authSpec() != nil && a.url == "" {
		return errors.New("the auth flags require --url (an auth block is remote-only)")
	}
	if a.clientSecret != "" && a.clientID == "" {
		return errors.New("--client-secret requires --client-id")
	}
	if _, err := mcpPairs(a.env); err != nil {
		return fmt.Errorf("--env: %w", err)
	}
	if _, err := mcpPairs(a.headers); err != nil {
		return fmt.Errorf("--header: %w", err)
	}
	return nil
}

type mcpRemoveArgs struct {
	agent string
	name  string
	layer string
	all   bool
}

func parseMCPRemoveArgs(args []string) (mcpRemoveArgs, error) {
	a := mcpRemoveArgs{agent: defaultMCPAgentID()}
	pos, err := parseMCPFlags(args, mcpParseSpec{
		values: map[string]*string{
			"--agent": &a.agent,
			"--layer": &a.layer,
		},
		bools: map[string]*bool{"--all": &a.all},
	})
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, errors.New("missing server name")
	}
	if len(pos) > 1 {
		return a, fmt.Errorf("unexpected argument %q", pos[1])
	}
	a.name = pos[0]
	if err := validMCPName(a.name); err != nil {
		return a, err
	}
	if a.layer != "" && !mcpValidLayer(a.layer) {
		return a, fmt.Errorf("unknown layer %q (want global, agent or project)", a.layer)
	}
	if a.layer != "" && a.all {
		return a, errors.New("--layer and --all are mutually exclusive")
	}
	return a, nil
}

type mcpTestArgs struct {
	agent   string
	name    string
	timeout time.Duration
}

func parseMCPTestArgs(args []string) (mcpTestArgs, error) {
	a := mcpTestArgs{agent: defaultMCPAgentID()}
	timeout := "30s"
	pos, err := parseMCPFlags(args, mcpParseSpec{values: map[string]*string{
		"--agent":   &a.agent,
		"--timeout": &timeout,
	}})
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, errors.New("missing server name")
	}
	if len(pos) > 1 {
		return a, fmt.Errorf("unexpected argument %q", pos[1])
	}
	a.name = pos[0]
	if err := validMCPName(a.name); err != nil {
		return a, err
	}
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return a, fmt.Errorf("invalid --timeout %q: %w", timeout, err)
	}
	if d <= 0 {
		return a, fmt.Errorf("invalid --timeout %q: must be positive", timeout)
	}
	a.timeout = d
	return a, nil
}

// ---------------------------------------------------------------------------
// Runtime resolution (config + layer roots)
// ---------------------------------------------------------------------------

// defaultMCPAgentID returns the agent ID an unscoped `lele mcp …` command acts
// on: the configured default agent (agents.list entry with default: true, else
// the first entry), or "main" for a single-agent install.
//
// Every subcommand's --agent flag starts from this value. Using the literal
// routing.DefaultAgentID ("main") instead broke the CLI on any multi-agent
// config — the asking user's case — with `✗ unknown agent "main" (known
// agents: coder, explorer, …)`, because "main" only exists as an implicit agent
// when agents.list is empty. The rule itself lives in
// routing.ConfiguredDefaultAgentID, which the runtime route resolver uses too,
// so the CLI can never disagree with the agent a plain message would reach.
//
// A config that cannot be loaded is not fatal here: the flag keeps its
// fallback and the subcommand reports the load failure with its own context,
// exactly as before this helper existed.
func defaultMCPAgentID() string {
	cfg, err := loadConfig()
	if err != nil || cfg == nil {
		return routing.DefaultAgentID
	}
	return routing.ConfiguredDefaultAgentID(cfg)
}

// mcpPathsFor resolves the three mcp.json roots for one agent ID exactly as
// the running gateway does (pkg/agent's resolveAgentWorkspace, reached through
// agent.AgentWorkspacePath): the agent layer is the agent's own workspace,
// resolved from an explicit workspace, else the shared default.
//
// The resolution is PURE — the registry is deliberately NOT built here. A
// registry instantiation initializes every agent workspace on disk
// (NewAgentInstance → InitializeWorkspace), so using it made even `lele mcp
// list` create <cwd>/memory; agent.AgentWorkspacePath reuses the very same
// resolveAgentWorkspace rule without touching the filesystem. The unknown-agent
// error is unchanged: an ID no configured agent normalizes to is refused and
// the known IDs are listed.
func mcpPathsFor(agentID string) (mcp.Paths, error) {
	cfg, err := loadConfig()
	if err != nil {
		return mcp.Paths{}, fmt.Errorf("loading config: %w", err)
	}
	workspace, ok := agent.AgentWorkspacePath(cfg, agentID)
	if !ok {
		ids := agent.AgentIDs(cfg)
		known := "none"
		if len(ids) > 0 {
			known = strings.Join(ids, ", ")
		}
		// Name the agent the command would have used on its own: the most
		// common cause is an explicit --agent (or a stale default) whose ID is
		// not in agents.list, and the suggestion makes the fix obvious.
		return mcp.Paths{}, fmt.Errorf("unknown agent %q (known agents: %s; default: %s)",
			agentID, known, routing.ConfiguredDefaultAgentID(cfg))
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "" // no project layer — the other two still work
	}
	return mcp.Paths{
		LeleDir:        getLeleDir(),
		AgentWorkspace: workspace,
		Cwd:            cwd,
	}, nil
}

// mcpGuardedLayerFile refuses to write to a path that is not an mcp.json under
// an allowed workspace root — the same predicate the REST handlers gate every
// path-bearing request on (pkg/channels.IsAllowedWorkspacePath).
func mcpGuardedLayerFile(file string) error {
	if file == "" {
		return errors.New("layer root is not configured for this agent/workspace")
	}
	if filepath.Base(file) != "mcp.json" || !channels.IsAllowedWorkspacePath(filepath.Dir(file)) {
		return fmt.Errorf("refusing to write %q: outside the allowed workspace roots", file)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shared rendering helpers
// ---------------------------------------------------------------------------

// mcpServerNames returns the effective server names sorted case-insensitively,
// with a case-sensitive tie-break so the order is fully deterministic.
func mcpServerNames(inv mcp.Inventory) []string {
	names := make([]string, 0, len(inv.Winner))
	for name := range inv.Winner {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		li, lj := strings.ToLower(names[i]), strings.ToLower(names[j])
		if li != lj {
			return li < lj
		}
		return names[i] < names[j]
	})
	return names
}

// mcpStateGlyph maps an effective state to its list/show glyph.
func mcpStateGlyph(state string) string {
	switch state {
	case "enabled":
		return mcpGlyphEnabled
	case "disabled":
		return mcpGlyphDisabled
	default:
		return mcpGlyphInvalid
	}
}

// ── Auth verdict ──────────────────────────────────────────────────────
//
// The CLI must be able to answer the two questions the TUI and the WebUI
// already answer — "does this server need a login?" and "am I signed in?" — with
// the same vocabulary, and without ever touching a credential. Both answers come
// from pkg/mcp's own verbs (CheckAuth), read through the SAME in-process store
// the flow uses (mcp.NewAuthStore(paths.LeleDir)): no REST round-trip, no second
// way of finding the lele directory, and never a token or any other record field
// in a rendered line.

// The two auth markers, byte-identical to the TUI's localized
// tui.mcpAuthRequired / tui.mcpAuthOK (pkg/tui/i18n/locales/en.json): the
// surfaces must speak the same vocabulary. The CLI is deliberately NOT
// localized, so these are English literals rather than i18n lookups.
const (
	mcpAuthMarkerRequired      = "[needs auth]"
	mcpAuthMarkerAuthenticated = "[authenticated]"
)

// mcpAuthVerdicts resolves the auth verdict of every name of an inventory.
//
// This mirrors the TUI's helper of the same name (pkg/tui/mcp_auth.go): the
// config comes from Discover — the same layer precedence and ${VAR} expansion
// the manager dials, and the only view whose entries carry the auth block — and
// NOT from the inventory row (EntryState.Summary is a display slice that never
// carries it). Disabled and invalid names are excluded by Discover and therefore
// get no verdict at all: an inert row must not advertise an auth state.
//
// A store that is not Enabled() (an empty LeleDir) yields an EMPTY map: there
// is no credential to report and no place to record one, so no surface may
// claim a server "needs auth" on its behalf ("no store ⇒ Required false").
func mcpAuthVerdicts(paths mcp.Paths, inv mcp.Inventory) map[string]mcp.AuthStatus {
	verdicts := make(map[string]mcp.AuthStatus)
	if len(inv.Winner) == 0 {
		return verdicts
	}
	store := mcp.NewAuthStore(paths.LeleDir)
	if !store.Enabled() {
		return verdicts
	}
	// Discover's warning error is ignored here, exactly like the TUI ignores
	// it: warnings belong to the loaders and the logs, never to a listing — its
	// RESULT is what is needed, and every entry it could read still gets a
	// verdict.
	disc, _ := mcp.Discover(paths)
	for name := range inv.Winner {
		srv, ok := disc.Servers[name]
		if !ok {
			continue
		}
		verdicts[name] = mcp.CheckAuth(store, name, srv)
	}
	return verdicts
}

// mcpAuthMarker is the marker appended to a `list` row, "" when there is
// nothing to say. Pure, and the SAME rule as the TUI's mcpAuthSuffix and the
// WebUI's chip:
//
//	authenticated ⇒ [authenticated]; else required ⇒ [needs auth];
//	else nothing
//
// Authenticated is tested FIRST because that is what CheckAuth means by it ("a
// usable credential exists, no sign-in is needed") and because it is
// ORTHOGONAL to Required — pkg/mcp documents a stored credential on an entry
// whose AuthRequired is false, and both other surfaces render that verdict as
// authenticated. Keying on Required first made the CLI the odd one out: this
// row said nothing while the TUI and the WebUI said "authenticated" for the
// very same DTO.
func mcpAuthMarker(st mcp.AuthStatus) string {
	switch {
	case st.Authenticated:
		return mcpAuthMarkerAuthenticated
	case st.Required:
		return mcpAuthMarkerRequired
	default:
		return ""
	}
}

// mcpAuthStateLabel renders the `show` verdict through the SAME rule and the
// SAME vocabulary as the list marker (mcpAuthMarker): [authenticated] for a
// stored credential, [needs auth] for a login owed and none stored, and
// (none) only when neither is true — nothing stored and nothing owed. Sharing
// the marker helper is deliberate: it makes the two sites structurally
// incapable of drifting apart.
func mcpAuthStateLabel(st mcp.AuthStatus) string {
	if marker := mcpAuthMarker(st); marker != "" {
		return marker
	}
	return "(none)"
}

// mcpAuthMethodLabel names the live carrier CheckAuth reports ("oauth",
// "headers"), or (none) when there is no carrier — the same spelling the TUI
// uses for its "Auth" line.
func mcpAuthMethodLabel(method string) string {
	if method == "" {
		return "(none)"
	}
	return method
}

// mcpAuthTypeLabel renders the DECLARED auth block's type. An empty type inside
// a present block (e.g. `"auth": {}`) is shown as unset rather than as an empty
// value.
func mcpAuthTypeLabel(t string) string {
	if t == "" {
		return "(unset)"
	}
	return t
}

// mcpShowField prints one labelled field of `lele mcp show`. The label is
// padded to a fixed width (12 cells) so the value always starts at the same
// column, matching the hard-coded field lines elsewhere in this command.
func mcpShowField(w io.Writer, label, value string) {
	fmt.Fprintf(w, "  %-12s %s\n", label, value)
}

// mcpDerivedInvalidReason explains an entry the runtime rejected WITHOUT a raw
// verdict: ReadInventory derives "invalid" for a winning entry Discover
// refused, and Discover's verdict is computed on the EXPANDED config, so the
// raw file can look perfectly fine (an unexpanded ${VAR} URL that becomes
// empty). Printing an empty reason ("invalid ()") is never acceptable, so
// every "invalid" surface uses this text when the raw verdict is empty.
const mcpDerivedInvalidReason = "the runtime rejects it — its ${VAR} references may expand to empty"

// mcpInvalidReason renders why a copy fails: the raw verdict when the reader
// produced one, else the derived-invalid explanation above.
func mcpInvalidReason(row mcp.EntryState) string {
	if row.Invalid != "" {
		return row.Invalid
	}
	return mcpDerivedInvalidReason
}

// mcpTruncate clips s to max runes (ellipsis included) and collapses newlines
// so a multi-line description cannot break the table layout.
func mcpTruncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// mcpFormatKeys renders an env/header KEY list. Values never reach here.
func mcpFormatKeys(keys []string) string {
	if len(keys) == 0 {
		return "(none)"
	}
	return strings.Join(keys, ", ")
}

// mcpQuoteArgs renders a verbatim args slice for `show`.
func mcpQuoteArgs(args []string) string {
	if len(args) == 0 {
		return "[]"
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprintf("%q", a)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// mcpYesNo renders a bool for the show breakdown.
func mcpYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// mcpSetLabel reports whether a config value is set WITHOUT printing it, for
// fields that must never be rendered (a client id). It matches the "(none)"
// spelling the key-list helpers use for an empty value.
func mcpSetLabel(set bool) string {
	if set {
		return "(set)"
	}
	return "(none)"
}

// mcpFindRow returns the inventory row owned by the given layer.
func mcpFindRow(rows []mcp.EntryState, layer string) (mcp.EntryState, bool) {
	for _, row := range rows {
		if row.Layer == layer {
			return row, true
		}
	}
	return mcp.EntryState{}, false
}

// mcpContains reports whether s holds want.
func mcpContains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

// mcpSuggestions returns up to five known names closest to name: first those
// sharing a case-insensitive prefix, then those merely containing it.
func mcpSuggestions(name string, names []string) []string {
	lower := strings.ToLower(name)
	var prefix, contains []string
	for _, n := range names {
		ln := strings.ToLower(n)
		if ln == lower {
			continue
		}
		switch {
		case strings.HasPrefix(ln, lower):
			prefix = append(prefix, n)
		case strings.Contains(ln, lower):
			contains = append(contains, n)
		}
	}
	out := append(prefix, contains...)
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// mcpPrintLayerFooter lists every layer file so users learn where config
// lives. Folded/aliased layers are reported once.
func mcpPrintLayerFooter(w io.Writer, inv mcp.Inventory) {
	fmt.Fprintln(w, "Layers (low → high):")
	for _, li := range inv.Layers {
		state := "(missing)"
		switch {
		case li.Path == "":
			state = "(not configured)"
		case li.Exists:
			state = "(exists)"
		}
		name := li.Layer
		if len(li.AliasedWith) > 0 {
			name = li.Layer + "=" + strings.Join(li.AliasedWith, "+")
		}
		fmt.Fprintf(w, "  %-16s %-40s %s\n", name, li.Path, state)
	}
}

// mcpPrintWarnings prints the inventory's non-fatal warnings.
func mcpPrintWarnings(w io.Writer, inv mcp.Inventory) {
	for _, warn := range inv.Warnings {
		fmt.Fprintf(w, "⊘ warning: %v\n", warn)
	}
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

func runMCPList(w io.Writer, args []string) int {
	a, err := parseMCPListArgs(args)
	if err != nil {
		mcpUsageError(w, "list", err)
		return 1
	}
	paths, err := mcpPathsFor(a.agent)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	inv := mcp.ReadInventory(paths)

	names := mcpServerNames(inv)
	if a.layer != "" {
		filtered := names[:0]
		for _, n := range names {
			if inv.Winner[n] == a.layer {
				filtered = append(filtered, n)
			}
		}
		names = filtered
	}

	if len(names) == 0 {
		if a.layer != "" {
			fmt.Fprintf(w, "No MCP servers configured in the %s layer.\n", a.layer)
		} else {
			fmt.Fprintln(w, "No MCP servers configured.")
		}
		fmt.Fprintln(w, "Create one with: lele mcp add <name> --command <bin> --layer global")
		fmt.Fprintf(w, "Global config file: %s\n", mcp.LayerFile(paths, mcp.LayerGlobal))
		fmt.Fprintln(w)
		mcpPrintLayerFooter(w, inv)
		mcpPrintWarnings(w, inv)
		return 0
	}

	// Resolve every row's auth verdict once, from the same inventory the rows
	// come from, so a row's marker and the `show` verdict cannot disagree.
	verdicts := mcpAuthVerdicts(paths, inv)

	for _, name := range names {
		state := inv.Effective[name]
		row, _ := mcpFindRow(inv.ByName[name], inv.Winner[name])
		desc := mcpTruncate(row.Summary.Description, mcpDescriptionMax)
		line := fmt.Sprintf("%s  %-18s %-7s %-7s", mcpStateGlyph(state), name, row.Summary.Kind, inv.Winner[name])
		line = strings.TrimRight(line, " ")
		if desc != "" {
			line += "  " + desc
		}
		// The auth marker goes LAST so nothing before it shifts: the row stays
		// a stable, machine-readable shape (glyph, name, kind, layer,
		// description) with the verdict appended. It is empty when the verdict
		// has nothing to say (no credential stored and none owed), and for a
		// row Discover does not report (disabled, invalid) — see
		// mcpAuthMarker and mcpAuthVerdicts.
		if marker := mcpAuthMarker(verdicts[name]); marker != "" {
			line += " " + marker
		}
		fmt.Fprintln(w, line)
	}

	enabled, disabled, invalid := 0, 0, 0
	for _, name := range names {
		switch inv.Effective[name] {
		case "enabled":
			enabled++
		case "disabled":
			disabled++
		default:
			invalid++
		}
	}
	fmt.Fprintf(w, "\n%d server(s): %d enabled, %d disabled, %d invalid\n\n", len(names), enabled, disabled, invalid)
	mcpPrintLayerFooter(w, inv)
	mcpPrintWarnings(w, inv)
	return 0
}

// ---------------------------------------------------------------------------
// show
// ---------------------------------------------------------------------------

// mcpRawEntry re-reads one layer file to recover an entry VERBATIM — the
// source of `show`'s args and auth block. The inventory deliberately exposes
// neither the args values (only their count) nor the auth block's own fields
// (only a display-safe projection), and mcp.ParseFile would expand ${VAR};
// neither is what `show` needs. This is a read-only, no-expansion decode of the
// file the inventory already named as the winner; it never re-implements
// discovery or validation. A missing/unreadable/unparseable file, or an absent
// entry, yields nil.
func mcpRawEntry(path, name string) *mcp.ServerConfig {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		MCPServers map[string]mcp.ServerConfig `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	entry, ok := doc.MCPServers[name]
	if !ok {
		return nil
	}
	return &entry
}

func runMCPShow(w io.Writer, args []string) int {
	a, err := parseMCPShowArgs(args)
	if err != nil {
		mcpUsageError(w, "show", err)
		return 1
	}
	paths, err := mcpPathsFor(a.agent)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	inv := mcp.ReadInventory(paths)

	rows := inv.ByName[a.name]
	state, ok := inv.Effective[a.name]
	if !ok || len(rows) == 0 {
		fmt.Fprintf(w, "✗ unknown MCP server %q\n", a.name)
		if sug := mcpSuggestions(a.name, mcpServerNames(inv)); len(sug) > 0 {
			fmt.Fprintf(w, "  did you mean: %s?\n", strings.Join(sug, ", "))
		}
		return 1
	}

	winnerLayer := inv.Winner[a.name]
	winner, _ := mcpFindRow(rows, winnerLayer)
	s := winner.Summary
	// The raw (unexpanded) entry: the source of the args and the auth block.
	raw := mcpRawEntry(winner.Path, a.name)
	// The live verdict, resolved through the SAME path the list uses. A row
	// Discover does not report (disabled, invalid) has no verdict here; that is
	// deliberate, so an inert copy never advertises an auth state.
	verdicts := mcpAuthVerdicts(paths, inv)
	st, hasVerdict := verdicts[a.name]

	fmt.Fprintf(w, "MCP server %q\n", a.name)
	// ONE verdict source: the STATE line follows Effective (the runtime's
	// verdict, computed after ${VAR} expansion) and never the raw per-file
	// verdict. The raw text is still visible, labelled, in the breakdown.
	switch state {
	case "invalid":
		fmt.Fprintf(w, "  state:       invalid (%s)\n", mcpInvalidReason(winner))
	default:
		fmt.Fprintf(w, "  state:       %s\n", state)
	}
	fmt.Fprintf(w, "  layer:       %s\n", winnerLayer)
	if winner.Path != "" {
		fmt.Fprintf(w, "  file:        %s\n", winner.Path)
	}
	fmt.Fprintf(w, "  kind:        %s\n", s.Kind)
	if s.Command != "" {
		fmt.Fprintf(w, "  command:     %s\n", s.Command)
	}
	if raw != nil && len(raw.Args) > 0 {
		fmt.Fprintf(w, "  args:        %s\n", mcpQuoteArgs(raw.Args))
	} else if s.Args > 0 {
		fmt.Fprintf(w, "  args:        (%d args)\n", s.Args)
	}
	if s.URL != "" {
		fmt.Fprintf(w, "  url:         %s\n", s.URL)
	}
	if s.Type != "" {
		fmt.Fprintf(w, "  type:        %s\n", s.Type)
	}
	if s.Description != "" {
		fmt.Fprintf(w, "  description: %s\n", s.Description)
	}
	fmt.Fprintf(w, "  env keys:    %s\n", mcpFormatKeys(s.EnvKeys))
	fmt.Fprintf(w, "  header keys: %s\n", mcpFormatKeys(s.HeaderKeys))

	// The declared auth block of the winning entry, printed the way the other
	// fields are: VERBATIM (a ${VAR} stays literal, never expanded) and
	// KEYS-side only. A client id is reported as set/not set and a client
	// SECRET is never printed at all — the same "no value leaves the config
	// reader" rule pkg/mcp's ServerSummary documents. An absent block prints
	// nothing, so a plain entry's output is unchanged by this feature.
	if raw != nil && raw.Auth != nil {
		mcpShowField(w, "auth:", mcpAuthTypeLabel(raw.Auth.Type))
		if len(raw.Auth.Scopes) > 0 {
			mcpShowField(w, "scopes:", strings.Join(raw.Auth.Scopes, ", "))
		}
		mcpShowField(w, "client id:", mcpSetLabel(raw.Auth.ClientID != ""))
		if raw.Auth.RedirectURI != "" {
			mcpShowField(w, "redirect:", raw.Auth.RedirectURI)
		}
		if raw.Auth.AuthServerMetadataURL != "" {
			mcpShowField(w, "as metadata:", raw.Auth.AuthServerMetadataURL)
		}
		if raw.Auth.ProtectedResourceMetadataURL != "" {
			mcpShowField(w, "pr metadata:", raw.Auth.ProtectedResourceMetadataURL)
		}
	}
	// The LIVE verdict (mcp.CheckAuth), a boolean report: required yes/no is
	// the bracket vocabulary, the method names the carrier. It is shown for any
	// auth-relevant entry — a declared block, a recorded 401, or a stored
	// credential — and ONLY when Discover reports the entry, so a disabled or
	// invalid copy gets the block above but no verdict.
	if hasVerdict && (raw != nil && raw.Auth != nil || st.Required || st.Authenticated) {
		mcpShowField(w, "auth state:", mcpAuthStateLabel(st))
		mcpShowField(w, "auth method:", mcpAuthMethodLabel(st.Method))
	}

	fmt.Fprintln(w, "\nPer-layer breakdown (low → high):")
	for _, layer := range mcp.Layers() {
		path := mcp.LayerFile(paths, layer)
		row, found := mcpFindRow(rows, layer)
		if !found {
			if path == "" {
				fmt.Fprintf(w, "  %-8s present=no (layer root not configured)\n", layer)
			} else {
				fmt.Fprintf(w, "  %-8s present=no  %s\n", layer, path)
			}
			continue
		}
		line := fmt.Sprintf("  %-8s present=%s defines=%s disabled=%s", layer,
			mcpYesNo(row.Present), mcpYesNo(row.Defines), mcpYesNo(row.Disabled))
		if row.Invalid != "" {
			// This copy's own verdict, read from the file's RAW bytes (no
			// ${VAR} expansion). Informational only: the STATE above is the
			// runtime's, and the two legitimately disagree when expansion is
			// what makes an entry valid or not.
			line += fmt.Sprintf("  raw verdict: %q", row.Invalid)
		}
		if row.Path != "" {
			line += "  " + row.Path
		}
		if layer == winnerLayer {
			line += "  ← winner"
		}
		if mcpContains(inv.Shadowed[a.name], layer) {
			line += "  (shadowed)"
		}
		fmt.Fprintln(w, line)
	}
	return 0
}

// ---------------------------------------------------------------------------
// add
// ---------------------------------------------------------------------------

func runMCPAdd(w io.Writer, args []string) int {
	a, err := parseMCPAddArgs(args)
	if err != nil {
		mcpUsageError(w, "add", err)
		return 1
	}
	entry, err := a.entry()
	if err != nil {
		mcpUsageError(w, "add", err)
		return 1
	}
	paths, err := mcpPathsFor(a.agent)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	file := mcp.LayerFile(paths, a.layer)
	if err := mcpGuardedLayerFile(file); err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}

	inv := mcp.ReadInventory(paths)
	targetExists := false
	shadowLayer, shadowPath := "", ""
	for _, row := range inv.ByName[a.name] {
		if row.Path == file {
			targetExists = true
			continue
		}
		if mcpLayerRank(row.Layer) > mcpLayerRank(a.layer) {
			shadowLayer, shadowPath = row.Layer, row.Path
		}
	}

	if targetExists && !a.force {
		fmt.Fprintf(w, "✗ server %q already exists in %s (%s)\n", a.name, a.layer, file)
		fmt.Fprintf(w, "  use --force to replace it\n")
		return 1
	}
	if shadowLayer != "" {
		fmt.Fprintf(w, "⊘ note: %q in %s shadows this entry (defined in %s)\n", a.name, shadowLayer, shadowPath)
	}

	res, err := mcp.UpsertEntry(file, a.name, entry)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	switch {
	case res.Created:
		fmt.Fprintf(w, "✓ added %q to %s (%s)\n", a.name, a.layer, file)
	case res.Replaced:
		fmt.Fprintf(w, "✓ replaced %q in %s (%s)\n", a.name, a.layer, file)
	default:
		fmt.Fprintf(w, "✓ %q unchanged in %s (%s)\n", a.name, a.layer, file)
	}
	return 0
}

// ---------------------------------------------------------------------------
// remove
// ---------------------------------------------------------------------------

func runMCPRemove(w io.Writer, args []string) int {
	a, err := parseMCPRemoveArgs(args)
	if err != nil {
		mcpUsageError(w, "remove", err)
		return 1
	}
	paths, err := mcpPathsFor(a.agent)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	inv := mcp.ReadInventory(paths)
	rows := inv.ByName[a.name]
	if len(rows) == 0 {
		fmt.Fprintf(w, "✗ unknown MCP server %q\n", a.name)
		if sug := mcpSuggestions(a.name, mcpServerNames(inv)); len(sug) > 0 {
			fmt.Fprintf(w, "  did you mean: %s?\n", strings.Join(sug, ", "))
		}
		return 1
	}

	switch {
	case a.all:
		for _, row := range rows {
			if err := mcpDeleteRow(w, a.name, row); err != nil {
				fmt.Fprintf(w, "✗ %v\n", err)
				return 1
			}
		}
		return 0
	case a.layer != "":
		row, found := mcpFindRow(rows, a.layer)
		if !found {
			fmt.Fprintf(w, "✗ %q is not defined in %s\n", a.name, a.layer)
			return 1
		}
		if err := mcpDeleteRow(w, a.name, row); err != nil {
			fmt.Fprintf(w, "✗ %v\n", err)
			return 1
		}
		return 0
	default:
		// ByName rows are low → high and ReadInventory always derives Winner
		// from the last row, so looking it up by name IS rows[len(rows)-1] —
		// one spelling of "the winner" across show/edit/remove/test.
		winnerLayer := inv.Winner[a.name]
		winner, found := mcpFindRow(rows, winnerLayer)
		if !found {
			// Unreachable guard: ReadInventory sets Winner for every name it
			// enumerates, and rows is non-empty here.
			fmt.Fprintf(w, "✗ %q has no copy in the %s layer\n", a.name, winnerLayer)
			return 1
		}
		if err := mcpDeleteRow(w, a.name, winner); err != nil {
			fmt.Fprintf(w, "✗ %v\n", err)
			return 1
		}
		remaining := make([]mcp.EntryState, 0, len(rows)-1)
		for _, row := range rows {
			if row.Layer != winnerLayer {
				remaining = append(remaining, row)
			}
		}
		// Removing a copy that kept the server OFF (a disable stub, or a
		// disabled definition) can switch a lower layer's definition ON — but
		// only when the copy that takes over is itself an enabled definition:
		// the merge is whole-entry replace, so a surviving stub higher up keeps
		// the server OFF.
		if reenabled, ok := mcpReenabledBy(winner, remaining); ok {
			fmt.Fprintf(w, "⚠ this re-enables %q from %s (%s)\n", a.name, reenabled.Layer, reenabled.Path)
		}
		for _, row := range remaining {
			fmt.Fprintf(w, "⊘ note: %q is still defined in %s (%s)\n", a.name, row.Layer, row.Path)
		}
		if len(remaining) > 0 {
			fmt.Fprintf(w, "  run: lele mcp remove %s --all\n", a.name)
		}
		return 0
	}
}

// mcpReenabledBy reports the copy that takes over once deleted is removed from
// the merge, when that means the server comes back ON. deleted must have been
// the reason it was off (a disable stub, i.e. a copy that defines nothing, or a
// definition carrying "disabled": true). The takeover copy is the HIGHEST
// remaining one — layers replace whole entries, so even a disable stub shadows
// everything below it — and the server only comes back ON when that winner is
// an enabled definition (it defines command/url and is not disabled).
func mcpReenabledBy(deleted mcp.EntryState, remaining []mcp.EntryState) (mcp.EntryState, bool) {
	if deleted.Defines && !deleted.Disabled {
		return mcp.EntryState{}, false // the deleted copy was the live one
	}
	if len(remaining) == 0 {
		return mcp.EntryState{}, false // nothing takes over
	}
	// remaining keeps ReadInventory's low → high order, so the last row is the
	// new winner of the merge.
	takeover := remaining[len(remaining)-1]
	if !takeover.Defines || takeover.Disabled {
		return mcp.EntryState{}, false // a stub or disabled copy keeps it OFF
	}
	return takeover, true
}

// mcpDeleteRow removes one copy through the byte-splice writer and reports it.
// A no-op result (nothing found) is an error: the caller only ever deletes
// rows the inventory proved present, so a no-op means the file changed under
// us.
//
// The path guard runs BEFORE the write, exactly as in `add` and `edit` (and as
// the REST/TUI surfaces do): all three subcommands must agree on which layer
// files may be written.
func mcpDeleteRow(w io.Writer, name string, row mcp.EntryState) error {
	if err := mcpGuardedLayerFile(row.Path); err != nil {
		return err
	}
	res, err := mcp.DeleteEntry(row.Path, name)
	if err != nil {
		return err
	}
	if !res.Deleted {
		return fmt.Errorf("%q is not defined in %s (%s)", name, row.Layer, row.Path)
	}
	fmt.Fprintf(w, "✓ removed %q from %s (%s)\n", name, row.Layer, row.Path)
	return nil
}

// ---------------------------------------------------------------------------
// edit
// ---------------------------------------------------------------------------

type mcpEditArgs struct {
	agent string
	name  string
	layer string
}

func parseMCPEditArgs(args []string) (mcpEditArgs, error) {
	a := mcpEditArgs{agent: defaultMCPAgentID()}
	pos, err := parseMCPFlags(args, mcpParseSpec{values: map[string]*string{
		"--agent": &a.agent,
		"--layer": &a.layer,
	}})
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, errors.New("missing server name")
	}
	if len(pos) > 1 {
		return a, fmt.Errorf("unexpected argument %q", pos[1])
	}
	a.name = pos[0]
	if err := validMCPName(a.name); err != nil {
		return a, err
	}
	if a.layer != "" && !mcpValidLayer(a.layer) {
		return a, fmt.Errorf("unknown layer %q (want global, agent or project)", a.layer)
	}
	return a, nil
}

// mcpEditTarget resolves which layer's copy an edit must open.
//
// The default targets the copy that WINS the merge (Inventory.Winner, i.e. the
// one a reader actually loads — even when that copy is a bare
// {"disabled":true} stub). Invalid entries need no special case: they are
// excluded from Discover's verdicts but ReadInventory still enumerates them in
// ByName, and it derives Winner from the winning copy — so the offender a user
// must repair is already the target.
//
// An explicit layer targets THAT layer's copy (mirroring `remove --layer`),
// even when a higher layer shadows it.
func mcpEditTarget(inv mcp.Inventory, name, layer string) (mcp.EntryState, bool) {
	rows := inv.ByName[name]
	if len(rows) == 0 {
		return mcp.EntryState{}, false
	}
	if layer != "" {
		row, found := mcpFindRow(rows, layer)
		return row, found
	}
	// ByName rows are low → high and ReadInventory sets Winner for every name
	// in ByName, so this branch always resolves and the fallback below is
	// unreachable in practice (defensive guard, kept for a bare Inventory
	// built by hand in a future caller).
	if winner, ok := inv.Winner[name]; ok {
		if row, found := mcpFindRow(rows, winner); found {
			return row, true
		}
	}
	row := rows[len(rows)-1] // rows are low → high
	for _, r := range rows {
		if r.Defines {
			row = r
		}
	}
	return row, true
}

// mcpEditorCommand returns the editor to launch, in the conventional order:
// $VISUAL, then $EDITOR, then vi.
func mcpEditorCommand() string {
	if v := os.Getenv("VISUAL"); v != "" {
		return v
	}
	if v := os.Getenv("EDITOR"); v != "" {
		return v
	}
	return "vi"
}

// mcpRunEditor runs editor on path with stdio attached, so interactive
// full-screen editors (vi, emacs -nw) work.
//
// The editor string may carry arguments ("code -w", "emacs -nw"), so it is
// run as `sh -c '<editor> "$1"' sh <path>`: sh word-splits the whole string
// for us (no hand-rolled word splitter) and the temp path travels as a
// positional parameter, so it never needs quoting. The shell indirection is
// confined to this helper and targets POSIX shells; the CLI runs on
// linux/macOS, where sh is always present.
func mcpRunEditor(editor, path string) error {
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// runMCPEdit implements `lele mcp edit <name> [--layer L]`: it opens the
// entry's raw JSON object in the user's editor, validates what comes back and
// saves it through the byte-splice writer.
//
// It is also the documented REPAIR path (`lele mcp test` prints it for an
// invalid entry): the object handed to the editor is exactly what is on disk
// (no ${VAR} expansion) and it is a bare entry object, with no name wrapper
// and no comment header, so the temp file stays valid JSON.
func runMCPEdit(w io.Writer, args []string) int {
	a, err := parseMCPEditArgs(args)
	if err != nil {
		mcpUsageError(w, "edit", err)
		return 1
	}
	paths, err := mcpPathsFor(a.agent)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	inv := mcp.ReadInventory(paths)

	target, ok := mcpEditTarget(inv, a.name, a.layer)
	if !ok {
		if len(inv.ByName[a.name]) == 0 {
			fmt.Fprintf(w, "✗ unknown MCP server %q\n", a.name)
			if sug := mcpSuggestions(a.name, mcpServerNames(inv)); len(sug) > 0 {
				fmt.Fprintf(w, "  did you mean: %s?\n", strings.Join(sug, ", "))
			}
			return 1
		}
		fmt.Fprintf(w, "✗ %q is not defined in %s\n", a.name, a.layer)
		return 1
	}

	// The path guard runs BEFORE the editor is launched: a write we already
	// know we will refuse must not cost the user an editing session.
	if err := mcpGuardedLayerFile(target.Path); err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}

	raw, found, err := mcp.EntryRaw(target.Path, a.name)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	if !found {
		fmt.Fprintf(w, "✗ %q is not defined in %s (%s)\n", a.name, target.Layer, target.Path)
		return 1
	}
	// The confirmation below is keyed to the EFFECTIVE verdict, not to the
	// raw one: an entry can be invalid purely because its ${VAR} expanded to
	// nothing (the raw text looks fine), and those repairs must be announced
	// too.
	wasInvalid := inv.Effective[a.name] == "invalid"

	tmp, err := os.CreateTemp("", "lele-mcp-edit-*.json")
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	// 0600 like SaveRawFile: the object can hold API keys and headers.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	if err := tmp.Close(); err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}

	fmt.Fprintf(w, "Editing %q (%s %s) — JSON object for this server. Values are stored raw; ${VAR} is expanded at connect time.\n",
		a.name, target.Layer, target.Path)

	if err := mcpRunEditor(mcpEditorCommand(), tmpPath); err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.As(err, &exitErr) && exitErr.ExitCode() == 127:
			// POSIX sh reports "command not found" as 127: the editor
			// binary itself is missing.
			fmt.Fprintf(w, "✗ edit aborted (editor not found) — set $EDITOR or $VISUAL\n")
		case errors.As(err, &exitErr):
			fmt.Fprintf(w, "✗ edit aborted (editor exited with status %d)\n", exitErr.ExitCode())
		default:
			fmt.Fprintf(w, "✗ edit aborted (%v)\n", err)
		}
		return 1
	}

	next, err := os.ReadFile(tmpPath)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	if bytes.Equal(next, raw) {
		fmt.Fprintln(w, "✓ no changes")
		return 0
	}

	// The editor's bytes are parsed WITHOUT expansion (paths/headers keep
	// their "${VAR}" text until connect time) and validated by the writer
	// itself, so there is exactly one validation rule. A refused entry writes
	// nothing and does not re-prompt: the user re-runs the command.
	var entry mcp.ServerConfig
	if err := json.Unmarshal(next, &entry); err != nil {
		fmt.Fprintf(w, "✗ invalid entry: %v — file left unchanged\n", err)
		fmt.Fprintf(w, "  hint: re-run: lele mcp edit %s\n", a.name)
		return 1
	}
	res, err := mcp.UpsertEntry(target.Path, a.name, entry)
	if err != nil {
		fmt.Fprintf(w, "✗ invalid entry: %v — file left unchanged\n", err)
		fmt.Fprintf(w, "  hint: re-run: lele mcp edit %s\n", a.name)
		return 1
	}
	if !res.Changed {
		// A purely cosmetic edit canonicalizes back to the bytes already on
		// disk (UpsertEntry's own no-op rule): nothing changed.
		fmt.Fprintln(w, "✓ no changes")
		return 0
	}
	fmt.Fprintf(w, "✓ updated %q (%s %s)\n", a.name, target.Layer, target.Path)
	if wasInvalid {
		// The entry validates now — report it only when the fresh read's
		// verdict agrees (Effective != "invalid"), so a shadowing layer can
		// never make the claim untrue.
		if after := mcp.ReadInventory(paths); after.Effective[a.name] != "invalid" {
			fmt.Fprintf(w, "✓ %q is now valid\n", a.name)
		}
	}
	// Same shadow warning as `add`: a higher layer's copy makes this one inert.
	for _, row := range inv.ByName[a.name] {
		if row.Layer == target.Layer {
			continue
		}
		if mcpLayerRank(row.Layer) > mcpLayerRank(target.Layer) {
			fmt.Fprintf(w, "⊘ note: %q in %s shadows this entry (defined in %s)\n", a.name, row.Layer, row.Path)
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// test
// ---------------------------------------------------------------------------

// probeServerForCLI is the probe `lele mcp test` dials through. It is a
// package-level var (not a direct call) so a test can drive the command's
// OUTCOME branches — a probe that fails with mcp.ErrAuthRequired, above all —
// without standing up a server: mcp.ProbeServer is not injectable from here,
// and those branches are about how this file RENDERS a probe's outcome.
//
// The dial itself is never faked to hide a wiring bug: everything the config
// resolution does (expansion, OAuth attachment) is still exercised, and the
// tests that must prove the real dial happens keep the default.
var probeServerForCLI = mcp.ProbeServer

// mcpAuthFlow is the subset of *mcp.AuthFlow `lele mcp auth` drives. It is an
// interface (rather than the concrete type) for the same reason the TUI's twin
// is: *mcp.AuthFlow has unexported fields, so only the engine can build one and
// an argv test could never drive the command without a network. *mcp.AuthFlow
// satisfies it as-is — no adapter.
//
// RedirectURI() is deliberately NOT part of it: the redirect URI the flow
// registered is the engine's business, and the URL it prints is URL().
type mcpAuthFlow interface {
	// URL is the authorization URL the user must visit (public client id,
	// CSRF state and PKCE challenge — never a secret).
	URL() string
	// Wait blocks until the flow reaches a terminal outcome.
	Wait(ctx context.Context) error
	// Submit completes the flow with a code obtained out of band.
	Submit(ctx context.Context, code, state string) error
	// Done reports whether the flow reached a TERMINAL outcome. A failed token
	// exchange is explicitly NOT terminal: the engine leaves the flow OPEN and
	// retryable (Submit stays valid while !Done()), which is the distinction
	// the --manual loop below is built on.
	Done() bool
	// Cancel releases the flow (a no-op once it finished).
	Cancel() error
}

// startMCPAuthFlow is the seam the `lele mcp auth` subcommand drives, so its
// argv surface is testable without a network (the engine probes and registers
// against the real server before it returns). It lives next to
// probeServerForCLI because it is the same kind of seam, and it defaults to the
// real engine call — mcp.StartAuth — wrapped in the interface above.
var startMCPAuthFlow = func(ctx context.Context, name string, srv mcp.ServerConfig, store *mcp.AuthStore, opts mcp.AuthOptions) (mcpAuthFlow, error) {
	return mcp.StartAuth(ctx, name, srv, store, opts)
}

// mcpAuthInput is where --manual reads its pasted input from. It is
// a package-level var (not os.Stdin inline) so a test can drive the paste
// without touching the process's own stdin: one line per attempt, on the single
// scanner mcpAuthManual owns.
var mcpAuthInput io.Reader = os.Stdin

// mcpTestDisabledNote and mcpAuthDisabledNote word the shared DISABLED line for
// each caller's verb: `test` probes such a server directly, `auth` authorizes
// the definition underneath a disable stub. Both explain the same thing — the
// runtime will not load the entry, the command acts on it anyway.
const (
	mcpTestDisabledNote = "probing it directly (the runtime will not load it)"
	mcpAuthDisabledNote = "authorizing it directly (the runtime will not load it)"
)

// mcpResolveTarget resolves the server config a probing or authorizing command
// must act on and prints the refusals BOTH commands share: an unknown name
// (with suggestions), an entry the runtime rejects, and a name that exists only
// as a disable stub.
//
// It exists so `lele mcp test` and `lele mcp auth` can never disagree about
// WHICH entry they are talking about: one inventory read, one winner lookup,
// one definition resolution (mcpProbeConfig owns the disable-stub descent) and
// one set of messages. The shared helper must never re-implement discovery.
//
// disabledNote is the clause the informational DISABLED line ends with, so each
// caller can word it for its own verb ("" suppresses the line). ok=false means
// the refusal has already been printed: the caller returns its failure code.
func mcpResolveTarget(w io.Writer, inv mcp.Inventory, name, disabledNote string) (mcp.ServerConfig, mcp.EntryState, bool) {
	rows := inv.ByName[name]
	state, known := inv.Effective[name]
	if !known || len(rows) == 0 {
		fmt.Fprintf(w, "✗ unknown MCP server %q\n", name)
		if sug := mcpSuggestions(name, mcpServerNames(inv)); len(sug) > 0 {
			fmt.Fprintf(w, "  did you mean: %s?\n", strings.Join(sug, ", "))
		}
		return mcp.ServerConfig{}, mcp.EntryState{}, false
	}
	// ByName rows are low → high and ReadInventory always sets Winner (the
	// last row) — the same spelling of "the winner" as show/edit/remove.
	winnerLayer := inv.Winner[name]
	winner, found := mcpFindRow(rows, winnerLayer)
	if !found {
		// Unreachable guard: Winner always names one of the enumerated rows.
		fmt.Fprintf(w, "✗ %q has no copy in the %s layer\n", name, winnerLayer)
		return mcp.ServerConfig{}, mcp.EntryState{}, false
	}
	if state == "invalid" {
		// The runtime's verdict, with the reason the reader has (or the
		// derived-invalid explanation when the raw text carries none).
		fmt.Fprintf(w, "✗ %q is invalid: %s (fix with: lele mcp edit %s)\n",
			name, mcpInvalidReason(winner), name)
		return mcp.ServerConfig{}, winner, false
	}
	if state == "disabled" && disabledNote != "" {
		fmt.Fprintf(w, "⊘ %q is disabled in the %s layer — %s\n", name, winnerLayer, disabledNote)
	}
	// The config is resolved OUTSIDE the seam on purpose: ProbeServer (and
	// StartAuth) take the config as given and never expand it — see
	// mcp.ProbeServer's contract — so this side must hand over the EXPANDED
	// entry, exactly the object the runtime's dialer would receive.
	cfg, row, err := mcpProbeConfig(rows, name)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return mcp.ServerConfig{}, row, false
	}
	return cfg, row, true
}

// runMCPTest implements `lele mcp test <name> [--timeout d]`.
//
// It dials the server's winning DEFINITION through mcp.ProbeServer — the
// production dial path (transport selection, stdio spawn or HTTP/SSE, the
// initialize handshake, tools/list) — and never through Manager.LoadServer:
// the manager deliberately refuses a disabled entry, which is exactly the
// entry `test` is asked about ("does it answer if I switch it on?").
//
// The entry resolution (unknown / invalid / disable-stub / disabled note) is
// SHARED with `lele mcp auth` through mcpResolveTarget, so the two commands can
// never act on different copies of the same name.
//
// The config is handed over WITH its OAuth runtime attached (mcp.
// AttachAuthRuntime, built from this CLI's own lele directory): pkg/mcp keeps
// the runtime private and ProbeServer dials the config it is given, so a bare
// config would report "authentication required" for a server this lele
// directory already holds a token for.
//
// Verdicts follow Inventory.Effective, never the raw per-file text: an entry
// whose raw bytes look fine can still be rejected by the runtime once ${VAR}
// references have been expanded, and vice versa.
func runMCPTest(w io.Writer, args []string) int {
	a, err := parseMCPTestArgs(args)
	if err != nil {
		mcpUsageError(w, "test", err)
		return 1
	}
	paths, err := mcpPathsFor(a.agent)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	inv := mcp.ReadInventory(paths)
	cfg, _, ok := mcpResolveTarget(w, inv, a.name, mcpTestDisabledNote)
	if !ok {
		return 1
	}

	// The OAuth runtime is PRIVATE to pkg/mcp and ProbeServer dials the config
	// it receives, so this side must attach it — exactly as the manager
	// resolves it before a dial (pkg/mcp/manager.go). The store is the CLI's
	// own lele directory (mcpPathsFor), the same file the gateway and the TUI
	// read, so a login performed anywhere is visible here.
	store := mcp.NewAuthStore(paths.LeleDir)
	cfg = mcp.AttachAuthRuntime(a.name, cfg, store)

	ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
	defer cancel()

	res, err := probeServerForCLI(ctx, cfg)
	if err != nil {
		// Keyed on the SENTINEL, never on the message: a transport failure
		// whose text happens to mention authentication must still take the
		// generic branch below.
		if errors.Is(err, mcp.ErrAuthRequired) {
			fmt.Fprintf(w, "✗ %q authentication required — run 'lele mcp auth %s'\n", a.name, a.name)
			if cfg.URL != "" {
				// A remote entry is the one case where the browser may live on
				// another machine, so the instruction is repeated for a user on
				// a headless host (the auth flow prints the URL to open).
				fmt.Fprintf(w, "  you must sign in first: run 'lele mcp auth %s' where lele runs — it prints the authorization URL for any browser\n", a.name)
			}
			return 1
		}
		fmt.Fprintf(w, "✗ %q failed: %v\n", a.name, err)
		return 1
	}
	fmt.Fprintf(w, "✓ %q connected — %d tools", a.name, len(res.Tools))
	if names := mcpToolNames(res.Tools); len(names) > 0 {
		fmt.Fprintf(w, ": %s", strings.Join(names, ", "))
	}
	// The handshake identity is diagnostic: only a server that reported a
	// name is announced (a substituted dialer reports none).
	if res.Server.Name != "" {
		fmt.Fprintf(w, " (server: %s)", strings.TrimSpace(res.Server.Name+" "+res.Server.Version))
	}
	fmt.Fprintln(w)
	return 0
}

// mcpProbeConfig resolves the ServerConfig `lele mcp test` must dial for name:
// the highest copy whose raw entry is a real DEFINITION. Pure
// {"disabled":true} stubs are skipped on the way down, because layers replace
// whole entries — if the stub were deleted (or switched on) the runtime would
// load the definition underneath it, so that is the config the user is asking
// about. For an enabled server the first candidate is the winner itself.
//
// The returned config is EXPANDED (ParseFile, exactly as Discover expands it
// for the runtime); ProbeServer never expands. The rows must be low → high.
//
// The error case is "every copy is a disable stub": there is nothing to dial.
func mcpProbeConfig(rows []mcp.EntryState, name string) (mcp.ServerConfig, mcp.EntryState, error) {
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		// EntryRaw reads the copy's raw JSON object without expansion (only
		// the writer's own accessor knows both the file and the name) — the
		// clean way to tell a stub from a definition.
		raw, found, err := mcp.EntryRaw(row.Path, name)
		if err != nil {
			return mcp.ServerConfig{}, row, err
		}
		if !found {
			continue // an inventory row whose bytes vanished: nothing to dial
		}
		var shape struct {
			Command string `json:"command"`
			URL     string `json:"url"`
		}
		if err := json.Unmarshal(raw, &shape); err != nil {
			return mcp.ServerConfig{}, row, fmt.Errorf("%q in %s (%s) is not a JSON object: %w",
				name, row.Layer, row.Path, err)
		}
		if shape.Command == "" && shape.URL == "" {
			continue // a pure disable stub shadows, it does not define
		}
		file, err := mcp.ParseFile(row.Path)
		if err != nil {
			return mcp.ServerConfig{}, row, err
		}
		cfg, ok := file.MCPServers[name]
		if !ok {
			continue
		}
		return cfg, row, nil
	}
	last := rows[len(rows)-1]
	return mcp.ServerConfig{}, last, fmt.Errorf("%q is only a disable stub (%s %s) — no definition to probe",
		name, last.Layer, last.Path)
}

// mcpToolListLimit caps how many tool names a successful probe prints.
const mcpToolListLimit = 10

// mcpToolNames renders the probe's tool names, truncating the tail with
// "+N more" so a chatty server cannot flood the terminal. The names are the
// server's own (ProbeServer deliberately does not namespace them the way the
// runtime's loaded tools are).
func mcpToolNames(reported []string) []string {
	if len(reported) == 0 {
		return nil
	}
	shown := reported
	if len(shown) > mcpToolListLimit {
		shown = shown[:mcpToolListLimit]
	}
	names := make([]string, 0, len(shown)+1)
	names = append(names, shown...)
	if extra := len(reported) - len(shown); extra > 0 {
		names = append(names, fmt.Sprintf("+%d more", extra))
	}
	return names
}

// ---------------------------------------------------------------------------
// auth
// ---------------------------------------------------------------------------

// mcpAuthDefaultTimeout is the flow budget when --timeout is not given. It
// mirrors the engine's own default (pkg/mcp's unexported defaultAuthTimeout):
// long enough for a browser login, short enough that a forgotten flow does not
// pin the loopback listener forever.
const mcpAuthDefaultTimeout = 5 * time.Minute

// mcpAuthExitFailure is the exit code of EVERY `lele mcp auth` failure — the
// family's convention (`lele mcp test` exits 1 for each of its own failure
// kinds, including the "authentication required" branch). It is named once so
// the sentinel table and the tests cannot drift apart.
const mcpAuthExitFailure = 1

type mcpAuthArgs struct {
	agent     string
	name      string
	noBrowser bool
	manual    bool
	timeout   time.Duration
	port      int
}

func parseMCPAuthArgs(args []string) (mcpAuthArgs, error) {
	a := mcpAuthArgs{agent: defaultMCPAgentID()}
	timeout := "5m"
	port := ""
	pos, err := parseMCPFlags(args, mcpParseSpec{
		values: map[string]*string{
			"--agent":   &a.agent,
			"--timeout": &timeout,
			"--port":    &port,
		},
		bools: map[string]*bool{
			"--no-browser": &a.noBrowser,
			"--manual":     &a.manual,
		},
	})
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, errors.New("missing server name")
	}
	if len(pos) > 1 {
		return a, fmt.Errorf("unexpected argument %q", pos[1])
	}
	a.name = pos[0]
	if err := validMCPName(a.name); err != nil {
		return a, err
	}
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return a, fmt.Errorf("invalid --timeout %q: %w", timeout, err)
	}
	if d <= 0 {
		return a, fmt.Errorf("invalid --timeout %q: must be positive", timeout)
	}
	a.timeout = d
	if port != "" {
		// 0 means "an ephemeral loopback port", which is the engine's own
		// default (AuthOptions.Port) — everything outside a TCP port range is
		// refused here rather than by the engine's private redirect-URI
		// sentinel.
		n, err := strconv.Atoi(port)
		if err != nil {
			return a, fmt.Errorf("invalid --port %q: want a number", port)
		}
		if n < 0 || n > 65535 {
			return a, fmt.Errorf("invalid --port %q: must be between 0 and 65535", port)
		}
		a.port = n
	}
	return a, nil
}

// runMCPAuth implements `lele mcp auth <name> [flags]`: it starts the engine's
// OAuth 2.1 flow for a REMOTE server and drives it to completion.
//
// The entry is resolved through mcpResolveTarget, the very helper `lele mcp
// test` uses, so the two commands can never disagree about WHICH copy of the
// name they are acting on; the store is the CLI's own lele directory, the same
// file the gateway and the TUI read.
//
// The authorization URL is printed BEFORE anything blocks, unconditionally: on
// a headless host the URL is the only way to finish, and the CLI is the
// headless path. Nothing that is a secret is ever printed — not the URL's
// parts beyond what the engine put there (public client id, state, PKCE
// challenge), and never a token, a code, a verifier, a client id or a client
// secret.
func runMCPAuth(w io.Writer, args []string) int {
	a, err := parseMCPAuthArgs(args)
	if err != nil {
		mcpUsageError(w, "auth", err)
		return 1
	}
	paths, err := mcpPathsFor(a.agent)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	inv := mcp.ReadInventory(paths)
	cfg, _, ok := mcpResolveTarget(w, inv, a.name, mcpAuthDisabledNote)
	if !ok {
		return 1
	}
	if cfg.URL == "" {
		// A local (stdio) entry: OAuth needs a url to talk to. The engine
		// refuses this shape too, but it keeps that sentinel private, so the
		// CLI reports what it can see. The wording matches the engine's
		// "not supported" branch below: to the user both mean "there is no
		// flow to run here".
		fmt.Fprintf(w, "✗ %q is a local (stdio) server — authentication is not supported; only a remote (url) server can run the OAuth flow\n", a.name)
		return mcpAuthExitFailure
	}
	// Everything that is a value stays in the engine: this side only decides
	// WHERE the flow may persist (this lele directory) and whether a browser
	// may be launched.
	store := mcp.NewAuthStore(paths.LeleDir)
	opts := mcp.AuthOptions{Timeout: a.timeout, Port: a.port}
	if !a.noBrowser {
		// The engine never spawns a browser itself — the opener is injected
		// (AuthOptions.OpenURL) — which is exactly what makes --no-browser a
		// single nil here, with the URL still printed above.
		opts.OpenURL = auth.OpenBrowser
	}
	// No CLI-side deadline: the ENGINE owns the flow budget (AuthOptions.
	// Timeout) and reports its expiry as mcp.ErrAuthTimedOut. A second,
	// identical deadline here would race it and turn that sentinel into a
	// bare context.DeadlineExceeded, losing the distinction the user needs.
	flow, err := startMCPAuthFlow(context.Background(), a.name, cfg, store, opts)
	if err != nil {
		return mcpAuthFail(w, a.name, err)
	}
	// Release the loopback listener on every exit path (a no-op once the flow
	// reached a terminal outcome), so an abandoned --manual prompt does not
	// leave the port bound until the budget runs out.
	defer func() { _ = flow.Cancel() }()

	fmt.Fprintf(w, "Authorization URL — open it in any browser (printed here for headless hosts):\n%s\n", flow.URL())

	if a.manual {
		return mcpAuthManual(w, a.name, flow)
	}
	if err := flow.Wait(context.Background()); err != nil {
		return mcpAuthFail(w, a.name, err)
	}
	mcpAuthSuccess(w, a.name)
	return 0
}

// mcpAuthManualMaxAttempts bounds the --manual read/submit loop. The engine
// deliberately keeps a FAILED token exchange OPEN and retryable, so a user who
// mistyped — or whose authorization server answered an error — can paste again
// WITHOUT starting a whole new flow (a new URL, a new PKCE verifier). The loop
// therefore re-prompts instead of exiting on the first failure, exactly like the
// TUI's and the WebUI's still-armed paste box.
//
// The bound exists because stdin can be a pipe with no EOF: `yes "" | lele mcp
// auth <name> --manual` would otherwise spin forever printing the same
// rejection. Ten attempts is far above the two or three a human needs and keeps
// the worst case finite; on reaching it the loop leaves the flow (the deferred
// Cancel releases the listener) and tells the user to start over.
const mcpAuthManualMaxAttempts = 10

// mcpAuthManualPrompt is printed before every read, and REPEATED after a
// failure so the user can see the box is still live: the same flow (same
// authorization URL, same state and verifier) is still accepting a paste.
const mcpAuthManualPrompt = "Paste the authorization code or the full redirect URL, then press ENTER:"

// mcpAuthQuitInputs are the explicit "give up" inputs. They are deliberately
// values the FROZEN paste grammar itself REJECTS — `:` is outside the
// authorization-code alphabet — so a quit token can never shadow a real paste:
// if one could parse as a code, quitting here would refuse or submit it
// differently from the TUI and the WebUI. They are matched on the trimmed
// paste, before mcp.ParseAuthPaste.
var mcpAuthQuitInputs = map[string]bool{":q": true, ":quit": true}

// mcpAuthManual completes the flow out of band: it reads pasted authorization
// results from mcpAuthInput and submits the (code, state) pair each carries,
// until the flow completes or the user gives up.
//
// The loop honours the engine's design (see mcpAuthFlow.Done): a failed token
// exchange leaves the flow open and retryable, so the value-free failure is
// printed and the prompt shown again — the SAME flow (same URL, same PKCE
// state and verifier) keeps accepting a code. It stops on success, when the
// flow becomes Done() on its own (the loopback callback landed while the user
// was at the prompt, or the budget expired), on EOF, on an explicit quit input,
// and after mcpAuthManualMaxAttempts attempts.
//
// The loopback callback stays available throughout — the engine owns that
// decision — but nothing here waits for it: a headless host has no browser to
// deliver one, and whichever half arrives first wins (the engine's Submit is
// idempotent per flow).
func mcpAuthManual(w io.Writer, name string, flow mcpAuthFlow) int {
	// ONE scanner for the whole loop: a fresh bufio.Scanner per read would
	// buffer ahead and swallow every paste after the first.
	sc := bufio.NewScanner(mcpAuthInput)
	for attempt := 1; ; attempt++ {
		// A flow that reached a terminal outcome on its own — the loopback
		// callback landed while we were at the prompt, or the budget expired —
		// ends the loop: Wait then returns immediately with that outcome.
		if flow.Done() {
			if err := flow.Wait(context.Background()); err != nil {
				return mcpAuthFail(w, name, err)
			}
			mcpAuthSuccess(w, name)
			return 0
		}
		if attempt > mcpAuthManualMaxAttempts {
			fmt.Fprintf(w, "✗ too many refused pastes (%d) — leaving the flow; run 'lele mcp auth %s' to start over\n",
				mcpAuthManualMaxAttempts, name)
			return mcpAuthExitFailure
		}
		fmt.Fprintln(w, mcpAuthManualPrompt)
		line, err := mcpAuthReadLine(sc)
		if err != nil {
			fmt.Fprintf(w, "✗ %v\n", err)
			return mcpAuthExitFailure
		}
		if mcpAuthQuitInputs[strings.TrimSpace(line)] {
			// The deferred flow.Cancel() releases the loopback listener on
			// every exit path, so the flow is NOT left open by a quit: say what
			// actually happens, and name the command that starts a new one.
			fmt.Fprintf(w, "✗ authorization canceled — nothing was submitted; the flow is abandoned, and 'lele mcp auth %s' starts a new one\n", name)
			return mcpAuthExitFailure
		}
		code, state, perr := mcp.ParseAuthPaste(line)
		if perr != nil {
			// Nothing was submitted (the engine answers ("", "") with every
			// error), so the box stays armed: print the outcome and re-prompt.
			fmt.Fprintf(w, "✗ %v\n", mcpAuthPasteError(perr))
			continue
		}
		if serr := flow.Submit(context.Background(), code, state); serr != nil {
			// NOT terminal by itself: the engine keeps the flow open after a
			// failed exchange, so this is a retry, not the end of the flow.
			fmt.Fprintf(w, "✗ %s\n", mcpAuthReason(name, serr))
			if flow.Done() {
				return mcpAuthExitFailure
			}
			continue
		}
		mcpAuthSuccess(w, name)
		return 0
	}
}

// mcpAuthReadLine reads ONE line of pasted input for the --manual loop. The
// caller passes the SINGLE scanner the loop owns: a scanner created per call
// would read ahead into the pipe and throw away every paste after the first.
//
// A reader that hits EOF without a line (a closed stdin, a piped input that ran
// out) is an error, not an empty code: there is nothing to submit, and EOF is
// the loop's terminator.
func mcpAuthReadLine(sc *bufio.Scanner) (string, error) {
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", fmt.Errorf("reading the pasted input: %w", err)
		}
		return "", errors.New("no input on stdin — paste the authorization code or the full redirect URL")
	}
	return sc.Text(), nil
}

// mcpAuthPasteError renders a mcp.ParseAuthPaste outcome with the CLI's own
// sentences. The WORDING is the only thing this side owns: the rule itself is
// the engine's — pkg/mcp.ParseAuthPaste is the ONE canonical implementation of
// the frozen grammar (.lele/tasks/mcp-oauth-paste-grammar.md), and the CLI no
// longer keeps a private copy (a review proved three hand-written copies
// DISAGREED: the same paste was submitted, refused, or submitted as a different
// code depending on the surface).
//
// `unrecognized` and `no-code` share ONE sentence on purpose: the grammar
// explicitly allows it, the TUI renders them the same way, and both mean "there
// is no authorization code in what you pasted".
func mcpAuthPasteError(err error) error {
	switch {
	case errors.Is(err, mcp.ErrAuthPasteEmpty):
		return errMCPAuthEmptyInput
	case errors.Is(err, mcp.ErrAuthPasteUnrecognized), errors.Is(err, mcp.ErrAuthPasteNoCode):
		return errMCPAuthBadInput
	}
	var denied *mcp.AuthPasteDeniedError
	if errors.As(err, &denied) {
		return mcpAuthRefusal(denied.Code)
	}
	return errMCPAuthBadInput
}

// errMCPAuthEmptyInput is the fixed sentence for a paste with nothing in it
// (mcp.ErrAuthPasteEmpty). Nothing was submitted and nothing is wrong.
var errMCPAuthEmptyInput = errors.New("empty input — paste the authorization code or the full redirect URL")

// errMCPAuthBadInput is the fixed sentence for a paste that carries no usable
// authorization code: a value none of the accepted forms matches
// (mcp.ErrAuthPasteUnrecognized) or a URL/query that arrived without one
// (mcp.ErrAuthPasteNoCode).
var errMCPAuthBadInput = errors.New("unrecognized input — paste the authorization code or the full redirect URL")

// mcpAuthRefusal renders the authorization server's own refusal (the `error`
// parameter of a redirect it sent back). The code is echoed ONLY when the
// engine's sanitizer keeps it; anything else becomes a fixed message, so a
// hostile redirect cannot push arbitrary bytes onto the terminal.
//
// The policy is pkg/mcp.SanitizeOAuthCode — the engine's ONE implementation of
// the rule, which delegates to the engine's own sanitizeOAuthToken. This file
// used to carry a private copy of that rule (mcpAuthOAuthToken); the point of
// the finding is that no surface keeps one. The engine has already sanitized
// the code it hands over, and the sanitizer is idempotent, so applying it again
// here is a belt-and-braces re-check, never a second rule.
func mcpAuthRefusal(code string) error {
	safe := mcp.SanitizeOAuthCode(code)
	if safe == "" {
		return errors.New("the authorization server refused the authorization (error=…); nothing was submitted")
	}
	return fmt.Errorf("the authorization server refused the authorization (%s); nothing was submitted", safe)
}

// mcpAuthFail renders one failure of the auth flow and returns the family's
// failure exit code.
//
// It maps the engine's SENTINELS, never its message text: "this server needs no
// login", "this server is local", "the store is off", "the transport is
// broken", "you cancelled" and "the login timed out" are different problems
// with different fixes, and collapsing them into one line is exactly what the
// sentinels exist to prevent. In particular a probe/transport failure
// (ErrAuthProbeFailed) must NOT be reported as "does not require
// authentication" — that would send the operator looking in the wrong place.
//
// The generic fallback prints the engine's own string, which pkg/mcp guarantees
// is value-free: no token, code, verifier, client secret or URL ever reaches
// this line.
func mcpAuthFail(w io.Writer, name string, err error) int {
	fmt.Fprintf(w, "✗ %s\n", mcpAuthReason(name, err))
	return mcpAuthExitFailure
}

// mcpAuthReason is the sentinel→message table behind mcpAuthFail.
func mcpAuthReason(name string, err error) string {
	switch {
	case errors.Is(err, mcp.ErrAuthNotRequired):
		return fmt.Sprintf("%q does not require authentication — the plain handshake succeeded, so there is nothing to authorize", name)
	case errors.Is(err, mcp.ErrAuthNotSupported):
		return fmt.Sprintf("%q authentication is not supported: the server advertises no dynamic client registration endpoint (add a pre-registered client with --client-id/--client-secret)", name)
	case errors.Is(err, mcp.ErrAuthProbeFailed):
		// The transport/probe sentinel: the server never completed a
		// handshake, so it is dead, unreachable or misaddressed — NOT a server
		// that needs no login.
		return fmt.Sprintf("%q transport failure — the server did not complete the handshake, so this is not an authorization challenge (run 'lele mcp test %s' to inspect the connection)", name, name)
	case errors.Is(err, mcp.ErrAuthStoreDisabled):
		return "the authorization store is disabled — there is no lele directory to persist the login in"
	case errors.Is(err, mcp.ErrAuthCanceled):
		return "authorization canceled"
	case errors.Is(err, mcp.ErrAuthTimedOut):
		return fmt.Sprintf("authorization timed out — run 'lele mcp auth %s' to try again", name)
	}
	return fmt.Sprintf("%q authorization failed: %v", name, err)
}

// mcpAuthSuccess is the ONE success line, shared by the loopback and the
// --manual completion paths so both report the same outcome.
func mcpAuthSuccess(w io.Writer, name string) {
	fmt.Fprintf(w, "✓ %q authenticated; the gateway picks it up on the next reload\n", name)
}

// ---------------------------------------------------------------------------
// logout
// ---------------------------------------------------------------------------

type mcpLogoutArgs struct {
	agent string
	name  string
}

func parseMCPLogoutArgs(args []string) (mcpLogoutArgs, error) {
	a := mcpLogoutArgs{agent: defaultMCPAgentID()}
	pos, err := parseMCPFlags(args, mcpParseSpec{values: map[string]*string{
		"--agent": &a.agent,
	}})
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, errors.New("missing server name")
	}
	if len(pos) > 1 {
		return a, fmt.Errorf("unexpected argument %q", pos[1])
	}
	a.name = pos[0]
	return a, validMCPName(a.name)
}

// runMCPLogout implements `lele mcp logout <name>`: it forgets everything the
// lele directory stores about one server — the token AND the dynamic client
// registration — through mcp.Logout, the same primitive the other surfaces use.
//
// It reports WHETHER anything was forgotten but never what it was: the store's
// only exported questions are yes/no, and nothing here ever touches a token.
//
// The server does not have to exist in the inventory. The name is the only key
// the store has, so a server already removed from mcp.json can (and should)
// still be forgotten here.
//
// An empty store is a SUCCESS: "logout" names a desired state, and that state
// ("nothing is stored for this server") already holds — the user's intent is
// satisfied, so the exit code says so instead of pretending a failure. Only a
// real store failure is non-zero.
func runMCPLogout(w io.Writer, args []string) int {
	a, err := parseMCPLogoutArgs(args)
	if err != nil {
		mcpUsageError(w, "logout", err)
		return 1
	}
	paths, err := mcpPathsFor(a.agent)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	store := mcp.NewAuthStore(paths.LeleDir)
	// The verdict is read BEFORE the clear: Clear removes the record, so
	// afterwards there is no way to tell "forgotten" from "never stored".
	_, had := store.Record(a.name)
	if err := mcp.Logout(store, a.name); err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return 1
	}
	if had {
		fmt.Fprintf(w, "✓ forgot the stored credentials and registration for %q (the gateway drops the session on the next reload)\n", a.name)
		return 0
	}
	fmt.Fprintf(w, "✓ nothing was stored for %q — nothing to forget (a logout that finds nothing is still a success)\n", a.name)
	return 0
}
