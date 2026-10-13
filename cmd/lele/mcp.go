package main

import (
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
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/tools"
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
var mcpUsage = map[string]string{
	"list":   "lele mcp list [--agent <id>] [--layer global|agent|project]",
	"show":   "lele mcp show <name> [--agent <id>]",
	"add":    "lele mcp add <name> (--command <bin> | --url <u>) [--arg v]… [--type http|sse] [--env K=V]… [--header K=V]… [--description d] [--layer global|agent|project] [--force]",
	"remove": "lele mcp remove <name> [--agent <id>] [--layer global|agent|project] [--all]",
	"edit":   "lele mcp edit <name> [--agent <id>] [--layer global|agent|project]",
	"test":   "lele mcp test <name> [--agent <id>] [--timeout 30s]",
}

// mcpLoadServer loads one server's tool specs through the production MCP
// manager. It is a package variable — the ONLY seam this file carves — so
// tests can exercise the happy/error branches of `lele mcp test` without
// spawning a child process or hitting the network. Production code never
// replaces it: mcp.NewManager(paths, nil) uses the real dialer.
var mcpLoadServer = func(ctx context.Context, paths mcp.Paths, name string) ([]tools.LoadedMCPTool, error) {
	mgr := mcp.NewManager(paths, nil)
	defer mgr.Close()
	return mgr.LoadServer(ctx, name)
}

// mcpCmd is the os.Args dispatcher. It mirrors modelsCmd's shape (bare
// `lele mcp` prints help and exits 0; an unknown subcommand prints help and
// exits 1) while delegating all real work to the testable runMCP* functions.
//
// Console logs are suppressed: resolving the agent layer instantiates the
// agent registry, which logs a provider warning when a config has no API key
// yet — noise that would otherwise bury the command's own output. The command
// communicates exclusively through fmt.Fprintf.
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

func writeMCPHelp(w io.Writer) {
	fmt.Fprint(w, `lele mcp — MCP servers

Usage:
  lele mcp list [--agent <id>] [--layer global|agent|project]
  lele mcp show <name> [--agent <id>]
  lele mcp add <name> (--command <bin> | --url <u>) [flags]
  lele mcp remove <name> [--layer global|agent|project] [--all]
  lele mcp edit <name> [--layer global|agent|project]
  lele mcp test <name> [--timeout 30s]

Common flags:
  --agent <id>            Agent whose workspace provides the agent layer (default: main)

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

Examples:
  lele mcp list
  lele mcp show filesystem
  lele mcp add filesystem --command npx --arg -y --arg @modelcontextprotocol/server-filesystem --arg /tmp
  lele mcp add api --url https://api.example.com/mcp --type http --header "Authorization=Bearer ${TOKEN}"
  lele mcp remove filesystem --all
  lele mcp edit api
  lele mcp test api

edit opens one entry's JSON object in $VISUAL/$EDITOR (fallback: vi), validates
it when the editor exits and writes it back atomically; the file is left
untouched on an invalid entry, so it is also the repair path for a broken server.

Note: ${VAR} references are stored verbatim and expanded only when a server is
loaded; env/header VALUES are never printed.
`)
}

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
// positionals. Both "--flag value" and "--flag=value" are understood. The
// returned slice holds the positional arguments in order.
func parseMCPFlags(args []string, spec mcpParseSpec) ([]string, error) {
	var pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
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
	fmt.Fprintf(w, "Usage: %s\n", mcpUsage[sub])
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
func mcpPairs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			return nil, fmt.Errorf("malformed %q: want KEY=VALUE", p)
		}
		if p[:eq] == "" {
			return nil, fmt.Errorf("malformed %q: KEY must not be empty", p)
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
	a := mcpListArgs{agent: routing.DefaultAgentID}
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
	a := mcpShowArgs{agent: routing.DefaultAgentID}
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
		Description: a.description,
	}, nil
}

func parseMCPAddArgs(args []string) (mcpAddArgs, error) {
	a := mcpAddArgs{agent: routing.DefaultAgentID, layer: mcp.LayerGlobal}
	pos, err := parseMCPFlags(args, mcpParseSpec{
		values: map[string]*string{
			"--agent":       &a.agent,
			"--command":     &a.command,
			"--url":         &a.url,
			"--type":        &a.typ,
			"--description": &a.description,
			"--layer":       &a.layer,
		},
		repeats: map[string]*[]string{
			"--arg":    &a.args,
			"--env":    &a.env,
			"--header": &a.headers,
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
	a := mcpRemoveArgs{agent: routing.DefaultAgentID}
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
	a := mcpTestArgs{agent: routing.DefaultAgentID}
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

// mcpPathsFor resolves the three mcp.json roots for one agent ID exactly as
// the running gateway does (pkg/agent's resolveAgentWorkspace, reached through
// the same registry the runtime builds): the agent layer is the agent's own
// workspace, resolved from an explicit workspace, else the shared default.
// Instantiating the registry also initializes the agent workspace on disk,
// which is the runtime's own behaviour on first start.
func mcpPathsFor(agentID string) (mcp.Paths, error) {
	cfg, err := loadConfig()
	if err != nil {
		return mcp.Paths{}, fmt.Errorf("loading config: %w", err)
	}
	registry := agent.NewAgentRegistry(cfg)
	inst, ok := registry.GetAgent(agentID)
	if !ok {
		ids := registry.ListAgentIDs()
		sort.Strings(ids)
		known := "none"
		if len(ids) > 0 {
			known = strings.Join(ids, ", ")
		}
		return mcp.Paths{}, fmt.Errorf("unknown agent %q (known agents: %s)", agentID, known)
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "" // no project layer — the other two still work
	}
	return mcp.Paths{
		LeleDir:        getLeleDir(),
		AgentWorkspace: inst.Workspace,
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

	for _, name := range names {
		state := inv.Effective[name]
		row, _ := mcpFindRow(inv.ByName[name], inv.Winner[name])
		desc := mcpTruncate(row.Summary.Description, mcpDescriptionMax)
		line := fmt.Sprintf("%s  %-18s %-7s %-7s", mcpStateGlyph(state), name, row.Summary.Kind, inv.Winner[name])
		line = strings.TrimRight(line, " ")
		if desc != "" {
			line += "  " + desc
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

// mcpRawEntryArgs re-reads one layer file to recover an entry's args slice
// VERBATIM. The inventory deliberately exposes only the arg COUNT
// (ServerSummary.Args is an int), and mcp.ParseFile would expand ${VAR} —
// neither is what `show` needs. This is a read-only, no-expansion decode of
// the file the inventory already named as the winner; it never re-implements
// discovery or validation. A missing/unreadable/unparseable file yields nil.
func mcpRawEntryArgs(path, name string) []string {
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
	return doc.MCPServers[name].Args
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

	fmt.Fprintf(w, "MCP server %q\n", a.name)
	if state == "invalid" {
		fmt.Fprintf(w, "  state:       invalid (%s)\n", winner.Invalid)
	} else {
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
	if rawArgs := mcpRawEntryArgs(winner.Path, a.name); len(rawArgs) > 0 {
		fmt.Fprintf(w, "  args:        %s\n", mcpQuoteArgs(rawArgs))
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
			line += fmt.Sprintf(" invalid=%q", row.Invalid)
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
		deleted := 0
		for _, row := range rows {
			if err := mcpDeleteRow(w, a.name, row); err != nil {
				fmt.Fprintf(w, "✗ %v\n", err)
				return 1
			}
			deleted++
		}
		if deleted == 0 {
			fmt.Fprintf(w, "✗ %q is not defined in any layer\n", a.name)
			return 1
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
		winner := rows[len(rows)-1]
		if err := mcpDeleteRow(w, a.name, winner); err != nil {
			fmt.Fprintf(w, "✗ %v\n", err)
			return 1
		}
		remaining := rows[:len(rows)-1]
		for _, row := range remaining {
			fmt.Fprintf(w, "⊘ note: %q is still defined in %s (%s)\n", a.name, row.Layer, row.Path)
		}
		if len(remaining) > 0 {
			fmt.Fprintf(w, "  run: lele mcp remove %s --all\n", a.name)
		}
		return 0
	}
}

// mcpDeleteRow removes one copy through the byte-splice writer and reports it.
// A no-op result (nothing found) is an error: the caller only ever deletes
// rows the inventory proved present, so a no-op means the file changed under
// us.
func mcpDeleteRow(w io.Writer, name string, row mcp.EntryState) error {
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
	a := mcpEditArgs{agent: routing.DefaultAgentID}
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
// The default targets the copy that WINS the merge (Inventory.Winner), i.e.
// the one a reader actually loads. Invalid entries need no special case: they
// are excluded from Discover's verdicts but ReadInventory still enumerates
// them in ByName, and it derives Winner from the winning copy — so the
// offender a user must repair is already the target. The defining-layer
// fallback covers the corner where a name survives in ByName with no derived
// winner at all: editing must work precisely for entries nobody can use, so
// the highest-rank DEFINING copy is picked (and, failing that, the
// highest-rank copy — a disable stub is editable too).
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

// mcpEntryValid reports whether the layer's copy of name passes validation in
// a fresh inventory read.
func mcpEntryValid(inv mcp.Inventory, name, layer string) bool {
	row, ok := mcpFindRow(inv.ByName[name], layer)
	return ok && row.Invalid == ""
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
	wasInvalid := target.Invalid != ""

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
		// The edited copy validates now — report it only when the fresh read
		// agrees, so a shadowing layer can never make the claim untrue.
		if after := mcp.ReadInventory(paths); mcpEntryValid(after, a.name, target.Layer) {
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
	state, ok := inv.Effective[a.name]
	rows := inv.ByName[a.name]
	if !ok || len(rows) == 0 {
		fmt.Fprintf(w, "✗ unknown MCP server %q\n", a.name)
		if sug := mcpSuggestions(a.name, mcpServerNames(inv)); len(sug) > 0 {
			fmt.Fprintf(w, "  did you mean: %s?\n", strings.Join(sug, ", "))
		}
		return 1
	}
	winner := rows[len(rows)-1]
	if winner.Invalid != "" {
		fmt.Fprintf(w, "✗ %q is invalid: %s (fix with: lele mcp edit %s)\n", a.name, winner.Invalid, a.name)
		return 1
	}
	if state == "disabled" {
		fmt.Fprintf(w, "⊘ %q is disabled (runtime will not load it); testing anyway...\n", a.name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
	defer cancel()

	specs, err := mcpLoadServer(ctx, paths, a.name)
	if err != nil {
		fmt.Fprintf(w, "✗ %q failed: %v\n", a.name, err)
		return 1
	}
	fmt.Fprintf(w, "✓ %q connected — %d tools", a.name, len(specs))
	if names := mcpToolNames(specs); len(names) > 0 {
		fmt.Fprintf(w, ": %s", strings.Join(names, ", "))
	}
	fmt.Fprintln(w)
	return 0
}

// mcpToolListLimit caps how many tool names a successful probe prints.
const mcpToolListLimit = 10

// mcpToolNames renders the probe's tool names, truncating the tail with
// "+N more" so a chatty server cannot flood the terminal.
func mcpToolNames(specs []tools.LoadedMCPTool) []string {
	if len(specs) == 0 {
		return nil
	}
	shown := specs
	if len(shown) > mcpToolListLimit {
		shown = shown[:mcpToolListLimit]
	}
	names := make([]string, 0, len(shown)+1)
	for _, s := range shown {
		names = append(names, s.Name)
	}
	if extra := len(specs) - len(shown); extra > 0 {
		names = append(names, fmt.Sprintf("+%d more", extra))
	}
	return names
}
