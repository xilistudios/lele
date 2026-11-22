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

const (
	mcpGlyphEnabled  = "●"
	mcpGlyphDisabled = "○"
	mcpGlyphInvalid  = "!"
)

const mcpDescriptionMax = 60

var mcpUsage = map[string]string{
	"list":   "lele mcp list [--agent <id>] [--layer global|agent|project]",
	"show":   "lele mcp show <name> [--agent <id>]",
	"add":    "lele mcp add <name> (--command <bin> | --url <u>) [--arg v]… [--type http|sse] [--env K=V]… [--header K=V]… [--description d] [--layer global|agent|project] [--force] [--auth oauth|none] [--scope s]… [--client-id id] [--client-secret secret] [--redirect-uri uri] [--auth-server-metadata-url url] [--protected-resource-metadata-url url]",
	"remove": "lele mcp remove <name> [--agent <id>] [--layer global|agent|project] [--all]",
	"edit":   "lele mcp edit <name> [--agent <id>] [--layer global|agent|project]",
	"test":   "lele mcp test <name> [--agent <id>] [--timeout 30s]  (an OAuth server may need 'lele mcp auth <name>' first)",
}

var mcpAuthUsage = map[string]string{
	"auth":   "lele mcp auth <name> [--agent <id>] [--no-browser] [--manual] [--timeout 5m] [--port <n>]",
	"logout": "lele mcp logout <name> [--agent <id>]",
}

func mcpUsageLine(sub string) string {
	if line, ok := mcpUsage[sub]; ok {
		return line
	}
	return mcpAuthUsage[sub]
}

func mcpCmd() {
	logger.SetQuiet(true)
	if code := runMCP(os.Stdout, os.Args[2:]); code != 0 {
		os.Exit(code)
	}
}

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

func mcpHelp() {
	writeMCPHelp(os.Stdout)
}

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

type mcpParseSpec struct {
	values  map[string]*string
	repeats map[string]*[]string
	bools   map[string]*bool
}

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

func splitMCPFlag(arg string) (name, value string, hasValue bool) {
	if i := strings.IndexByte(arg, '='); i >= 0 {
		return arg[:i], arg[i+1:], true
	}
	return arg, "", false
}

func nextMCPValue(args []string, i int, inline string, hasInline bool) (value string, next int, ok bool) {
	if hasInline {
		return inline, i, true
	}
	if i+1 < len(args) {
		return args[i+1], i + 1, true
	}
	return "", i, false
}

func mcpUsageError(w io.Writer, sub string, err error) {
	fmt.Fprintf(w, "✗ %v\n", err)
	fmt.Fprintf(w, "Usage: %s\n", mcpUsageLine(sub))
	fmt.Fprintln(w, "Run 'lele mcp --help' for details.")
}

func validMCPName(name string) error {
	if name == "" {
		return errors.New("server name must not be empty")
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("server name %q must not contain '/'", name)
	}
	return nil
}

func mcpValidLayer(layer string) bool {
	for _, l := range mcp.Layers() {
		if l == layer {
			return true
		}
	}
	return false
}

func mcpLayerRank(layer string) int {
	for i, l := range mcp.Layers() {
		if l == layer {
			return i
		}
	}
	return -1
}

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

	authType                     string
	scopes                       []string
	authServerMetadataURL        string
	protectedResourceMetadataURL string
	clientID                     string
	clientSecret                 string
	redirectURI                  string
}

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

func defaultMCPAgentID() string {
	cfg, err := loadConfig()
	if err != nil || cfg == nil {
		return routing.DefaultAgentID
	}
	return routing.ConfiguredDefaultAgentID(cfg)
}

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
		return mcp.Paths{}, fmt.Errorf("unknown agent %q (known agents: %s; default: %s)",
			agentID, known, routing.ConfiguredDefaultAgentID(cfg))
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	return mcp.Paths{
		LeleDir:        getLeleDir(),
		AgentWorkspace: workspace,
		Cwd:            cwd,
	}, nil
}

func mcpGuardedLayerFile(file string) error {
	if file == "" {
		return errors.New("layer root is not configured for this agent/workspace")
	}
	if filepath.Base(file) != "mcp.json" || !channels.IsAllowedWorkspacePath(filepath.Dir(file)) {
		return fmt.Errorf("refusing to write %q: outside the allowed workspace roots", file)
	}
	return nil
}

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

const (
	mcpAuthMarkerRequired      = "[needs auth]"
	mcpAuthMarkerAuthenticated = "[authenticated]"
)

func mcpAuthVerdicts(paths mcp.Paths, inv mcp.Inventory) map[string]mcp.AuthStatus {
	verdicts := make(map[string]mcp.AuthStatus)
	if len(inv.Winner) == 0 {
		return verdicts
	}
	store := mcp.NewAuthStore(paths.LeleDir)
	if !store.Enabled() {
		return verdicts
	}
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

func mcpAuthStateLabel(st mcp.AuthStatus) string {
	if marker := mcpAuthMarker(st); marker != "" {
		return marker
	}
	return "(none)"
}

func mcpAuthMethodLabel(method string) string {
	if method == "" {
		return "(none)"
	}
	return method
}

func mcpAuthTypeLabel(t string) string {
	if t == "" {
		return "(unset)"
	}
	return t
}

func mcpShowField(w io.Writer, label, value string) {
	fmt.Fprintf(w, "  %-12s %s\n", label, value)
}

const mcpDerivedInvalidReason = "the runtime rejects it — its ${VAR} references may expand to empty"

func mcpInvalidReason(row mcp.EntryState) string {
	if row.Invalid != "" {
		return row.Invalid
	}
	return mcpDerivedInvalidReason
}

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

func mcpFormatKeys(keys []string) string {
	if len(keys) == 0 {
		return "(none)"
	}
	return strings.Join(keys, ", ")
}

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

func mcpYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func mcpSetLabel(set bool) string {
	if set {
		return "(set)"
	}
	return "(none)"
}

func mcpFindRow(rows []mcp.EntryState, layer string) (mcp.EntryState, bool) {
	for _, row := range rows {
		if row.Layer == layer {
			return row, true
		}
	}
	return mcp.EntryState{}, false
}

func mcpContains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

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

func mcpPrintWarnings(w io.Writer, inv mcp.Inventory) {
	for _, warn := range inv.Warnings {
		fmt.Fprintf(w, "⊘ warning: %v\n", warn)
	}
}

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
	raw := mcpRawEntry(winner.Path, a.name)
	verdicts := mcpAuthVerdicts(paths, inv)
	st, hasVerdict := verdicts[a.name]

	fmt.Fprintf(w, "MCP server %q\n", a.name)
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
		winnerLayer := inv.Winner[a.name]
		winner, found := mcpFindRow(rows, winnerLayer)
		if !found {
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

func mcpReenabledBy(deleted mcp.EntryState, remaining []mcp.EntryState) (mcp.EntryState, bool) {
	if deleted.Defines && !deleted.Disabled {
		return mcp.EntryState{}, false
	}
	if len(remaining) == 0 {
		return mcp.EntryState{}, false
	}
	takeover := remaining[len(remaining)-1]
	if !takeover.Defines || takeover.Disabled {
		return mcp.EntryState{}, false
	}
	return takeover, true
}

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

func mcpEditTarget(inv mcp.Inventory, name, layer string) (mcp.EntryState, bool) {
	rows := inv.ByName[name]
	if len(rows) == 0 {
		return mcp.EntryState{}, false
	}
	if layer != "" {
		row, found := mcpFindRow(rows, layer)
		return row, found
	}
	if winner, ok := inv.Winner[name]; ok {
		if row, found := mcpFindRow(rows, winner); found {
			return row, true
		}
	}
	row := rows[len(rows)-1]
	for _, r := range rows {
		if r.Defines {
			row = r
		}
	}
	return row, true
}

func mcpEditorCommand() string {
	if v := os.Getenv("VISUAL"); v != "" {
		return v
	}
	if v := os.Getenv("EDITOR"); v != "" {
		return v
	}
	return "vi"
}

func mcpRunEditor(editor, path string) error {
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

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
		fmt.Fprintln(w, "✓ no changes")
		return 0
	}
	fmt.Fprintf(w, "✓ updated %q (%s %s)\n", a.name, target.Layer, target.Path)
	if wasInvalid {
		if after := mcp.ReadInventory(paths); after.Effective[a.name] != "invalid" {
			fmt.Fprintf(w, "✓ %q is now valid\n", a.name)
		}
	}
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

var probeServerForCLI = mcp.ProbeServer

type mcpAuthFlow interface {
	URL() string
	Wait(ctx context.Context) error
	Submit(ctx context.Context, code, state string) error
	Done() bool
	Cancel() error
}

var startMCPAuthFlow = func(ctx context.Context, name string, srv mcp.ServerConfig, store *mcp.AuthStore, opts mcp.AuthOptions) (mcpAuthFlow, error) {
	return mcp.StartAuth(ctx, name, srv, store, opts)
}

var mcpAuthInput io.Reader = os.Stdin

const (
	mcpTestDisabledNote = "probing it directly (the runtime will not load it)"
	mcpAuthDisabledNote = "authorizing it directly (the runtime will not load it)"
)

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
	winnerLayer := inv.Winner[name]
	winner, found := mcpFindRow(rows, winnerLayer)
	if !found {
		fmt.Fprintf(w, "✗ %q has no copy in the %s layer\n", name, winnerLayer)
		return mcp.ServerConfig{}, mcp.EntryState{}, false
	}
	if state == "invalid" {
		fmt.Fprintf(w, "✗ %q is invalid: %s (fix with: lele mcp edit %s)\n",
			name, mcpInvalidReason(winner), name)
		return mcp.ServerConfig{}, winner, false
	}
	if state == "disabled" && disabledNote != "" {
		fmt.Fprintf(w, "⊘ %q is disabled in the %s layer — %s\n", name, winnerLayer, disabledNote)
	}
	cfg, row, err := mcpProbeConfig(rows, name)
	if err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		return mcp.ServerConfig{}, row, false
	}
	return cfg, row, true
}

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

	store := mcp.NewAuthStore(paths.LeleDir)
	cfg = mcp.AttachAuthRuntime(a.name, cfg, store)

	ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
	defer cancel()

	res, err := probeServerForCLI(ctx, cfg)
	if err != nil {
		if errors.Is(err, mcp.ErrAuthRequired) {
			fmt.Fprintf(w, "✗ %q authentication required — run 'lele mcp auth %s'\n", a.name, a.name)
			if cfg.URL != "" {
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
	if res.Server.Name != "" {
		fmt.Fprintf(w, " (server: %s)", strings.TrimSpace(res.Server.Name+" "+res.Server.Version))
	}
	fmt.Fprintln(w)
	return 0
}

func mcpProbeConfig(rows []mcp.EntryState, name string) (mcp.ServerConfig, mcp.EntryState, error) {
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		raw, found, err := mcp.EntryRaw(row.Path, name)
		if err != nil {
			return mcp.ServerConfig{}, row, err
		}
		if !found {
			continue
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
			continue
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

const mcpToolListLimit = 10

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

const mcpAuthDefaultTimeout = 5 * time.Minute

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
		fmt.Fprintf(w, "✗ %q is a local (stdio) server — authentication is not supported; only a remote (url) server can run the OAuth flow\n", a.name)
		return mcpAuthExitFailure
	}
	store := mcp.NewAuthStore(paths.LeleDir)
	opts := mcp.AuthOptions{Timeout: a.timeout, Port: a.port}
	if !a.noBrowser {
		opts.OpenURL = auth.OpenBrowser
	}
	flow, err := startMCPAuthFlow(context.Background(), a.name, cfg, store, opts)
	if err != nil {
		return mcpAuthFail(w, a.name, err)
	}
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

const mcpAuthManualMaxAttempts = 10

const mcpAuthManualPrompt = "Paste the authorization code or the full redirect URL, then press ENTER:"

var mcpAuthQuitInputs = map[string]bool{":q": true, ":quit": true}

func mcpAuthManual(w io.Writer, name string, flow mcpAuthFlow) int {
	sc := bufio.NewScanner(mcpAuthInput)
	for attempt := 1; ; attempt++ {
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
			fmt.Fprintf(w, "✗ authorization canceled — nothing was submitted; the flow is abandoned, and 'lele mcp auth %s' starts a new one\n", name)
			return mcpAuthExitFailure
		}
		code, state, perr := mcp.ParseAuthPaste(line)
		if perr != nil {
			fmt.Fprintf(w, "✗ %v\n", mcpAuthPasteError(perr))
			continue
		}
		if serr := flow.Submit(context.Background(), code, state); serr != nil {
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

func mcpAuthReadLine(sc *bufio.Scanner) (string, error) {
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", fmt.Errorf("reading the pasted input: %w", err)
		}
		return "", errors.New("no input on stdin — paste the authorization code or the full redirect URL")
	}
	return sc.Text(), nil
}

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

var errMCPAuthEmptyInput = errors.New("empty input — paste the authorization code or the full redirect URL")

var errMCPAuthBadInput = errors.New("unrecognized input — paste the authorization code or the full redirect URL")

func mcpAuthRefusal(code string) error {
	safe := mcp.SanitizeOAuthCode(code)
	if safe == "" {
		return errors.New("the authorization server refused the authorization (error=…); nothing was submitted")
	}
	return fmt.Errorf("the authorization server refused the authorization (%s); nothing was submitted", safe)
}

func mcpAuthFail(w io.Writer, name string, err error) int {
	fmt.Fprintf(w, "✗ %s\n", mcpAuthReason(name, err))
	return mcpAuthExitFailure
}

func mcpAuthReason(name string, err error) string {
	switch {
	case errors.Is(err, mcp.ErrAuthNotRequired):
		return fmt.Sprintf("%q does not require authentication — the plain handshake succeeded, so there is nothing to authorize", name)
	case errors.Is(err, mcp.ErrAuthNotSupported):
		return fmt.Sprintf("%q authentication is not supported: the server advertises no dynamic client registration endpoint (add a pre-registered client with --client-id/--client-secret)", name)
	case errors.Is(err, mcp.ErrAuthProbeFailed):
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

func mcpAuthSuccess(w io.Writer, name string) {
	fmt.Fprintf(w, "✓ %q authenticated; the gateway picks it up on the next reload\n", name)
}

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
