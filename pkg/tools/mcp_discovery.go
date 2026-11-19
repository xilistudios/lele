package tools

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The lazy MCP discovery pair: list_mcp_tools answers "which tools does this
// server have, most used first?" with NAMES ONLY, and get_mcp_tool loads the
// full spec (description + input schema) of ONE OR MORE of them and registers
// them so they become callable.
//
// Splitting the answer in two is the whole point: a server with 60 tools would
// otherwise burn tens of thousands of tokens of JSON schemas to expose a single
// capability the model actually wants. Listing stays schema-free (cheap enough
// to call repeatedly) and only the chosen tool pays for its schema.
//
// These two tools replace the removed bulk loader: nothing registers a whole
// server's specs any more, and the legacy tool name is only kept as an
// allowlist alias (see LoadMCPToolsName).

// ListMCPToolsName is the registry name of the schema-free listing tool.
const ListMCPToolsName = "list_mcp_tools"

// GetMCPToolName is the registry name of the tool loader.
const GetMCPToolName = "get_mcp_tool"

const (
	// defaultMCPToolPage is the 1-based page list_mcp_tools starts on.
	defaultMCPToolPage = 1
	// defaultMCPToolsPageSize is how many names one page shows when the
	// caller does not ask for a size: small enough to stay cheap, large
	// enough to cover most servers in one call.
	defaultMCPToolsPageSize = 20
	// maxMCPToolsPageSize caps page_size so a single call can never dump a
	// huge server into the context by accident.
	maxMCPToolsPageSize = 100
	// maxMCPToolDescriptionRunes bounds the one-line description shown per
	// listed tool; the full text is available via get_mcp_tool.
	maxMCPToolDescriptionRunes = 120
)

// ListMCPToolsTool lists the tool NAMES of one MCP server, paginated and
// ordered by recorded usage. It carries no registry and no factory on purpose:
// listing must never register anything, so discovering a server (or paging
// through its tools) costs nothing but text.
type ListMCPToolsTool struct {
	loader MCPLoader
}

var _ Tool = (*ListMCPToolsTool)(nil)

// NewListMCPToolsTool wires the listing tool to a loader. A nil loader is
// tolerated: Execute then fails with a "not configured" error.
func NewListMCPToolsTool(loader MCPLoader) *ListMCPToolsTool {
	return &ListMCPToolsTool{loader: loader}
}

// Name returns the registry name of this tool.
func (t *ListMCPToolsTool) Name() string { return ListMCPToolsName }

// Description tells the model what a page contains (names only), how it is
// ordered and where to go next for a full specification.
func (t *ListMCPToolsTool) Description() string {
	return "List the tool names of one MCP server, paginated and ordered by usage frequency (most used first). " +
		"Returns names and short descriptions only — no input schemas. " +
		"Use get_mcp_tool to load one tool's full specification and make it callable."
}

// Parameters returns the JSON schema for this tool: the required "server" plus
// the optional page and page_size of the listing.
func (t *ListMCPToolsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"server": map[string]interface{}{
				"type":        "string",
				"description": "MCP server name (see the '## MCP Servers' section of the context)",
			},
			"page": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"description": "1-based page number (default 1)",
			},
			"page_size": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     maxMCPToolsPageSize,
				"description": "tools per page (default 20, max 100)",
			},
		},
		"required": []string{"server"},
	}
}

// Execute lists one page of the server's tool names. The tools are ordered by
// recorded usage (most used first, ties broken by registry name) so the tools
// the user actually works with land on page 1.
//
// Out-of-range paging is forgiving rather than an error: page beyond the last
// page is clamped to the last page and page_size above the maximum is clamped
// to the maximum, because a model that guesses "page 5" of a 2-page server
// wants the tail, not a failure.
func (t *ListMCPToolsTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.loader == nil {
		return ErrorResult("list_mcp_tools is not configured (missing loader)")
	}
	server, _ := args["server"].(string)
	server = strings.TrimSpace(server)
	if server == "" {
		return ErrorResult(fmt.Sprintf(`missing required argument "server"; known servers: %s`, knownServers(t.loader)))
	}
	specs, err := t.loader.LoadServer(ctx, server)
	if err != nil {
		return ErrorResult(loadError(t.loader, server, err))
	}
	if len(specs) == 0 {
		return NewToolResult(fmt.Sprintf("MCP server %q has no tools.", server))
	}

	pageSize := positiveIntArg(args, "page_size", defaultMCPToolsPageSize, maxMCPToolsPageSize)
	// LoadServer hands back a shared cached slice: orderByUsage copies it so a
	// listing never reorders the loader's cache (another session may be
	// iterating it right now).
	counts := t.loader.ToolUsage(server)
	ordered := orderByUsage(specs, counts)
	totalPages := (len(ordered) + pageSize - 1) / pageSize
	page := positiveIntArg(args, "page", defaultMCPToolPage, 0)
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if end > len(ordered) {
		end = len(ordered)
	}
	return NewToolResult(renderToolList(server, ordered[start:end], counts, page, pageSize, totalPages, len(ordered)))
}

// GetMCPToolTool loads the full specification of one or more MCP tools and
// registers them in the tool registry, which makes them callable as regular
// tools. Loading several in one call costs one tool call instead of N.
type GetMCPToolTool struct {
	loader   MCPLoader
	registry *ToolRegistry
	// factory builds the concrete Tool for a loaded spec, injected so
	// pkg/tools never references pkg/mcp (same seam as MCPLoader and
	// LoadedMCPTool in mcp_seam.go).
	factory func(LoadedMCPTool) Tool
}

var _ Tool = (*GetMCPToolTool)(nil)

// NewGetMCPToolTool wires the tool loader. registry is normally the session's
// scoped layer, so a load stays visible only to the session that performed it.
func NewGetMCPToolTool(loader MCPLoader, registry *ToolRegistry, factory func(LoadedMCPTool) Tool) *GetMCPToolTool {
	return &GetMCPToolTool{
		loader:   loader,
		registry: registry,
		factory:  factory,
	}
}

// Name returns the registry name of this tool.
func (t *GetMCPToolTool) Name() string { return GetMCPToolName }

// Description explains that this tool both loads specs into the context and
// registers the tools, and that list_mcp_tools is the way to find names.
func (t *GetMCPToolTool) Description() string {
	return "Load the full specification (description and input schema) of one or more MCP tools into " +
		"the context and register them so they become callable as regular tools. " +
		"Pass tool for a single name or tools for several. " +
		"Use list_mcp_tools first to discover tool names, and load only the tools you need."
}

// Parameters returns the JSON schema for this tool: the required "server" plus
// the optional "tool" (a single name) and "tools" (several names), both
// accepting either naming form.
//
// JSON-Schema cannot express "tool OR tools" portably, so neither is marked
// required and Execute enforces the at-least-one rule itself.
func (t *GetMCPToolTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"server": map[string]interface{}{
				"type":        "string",
				"description": "MCP server name (see the '## MCP Servers' section of the context)",
			},
			"tool": map[string]interface{}{
				"type": "string",
				"description": "Single tool name — either the namespaced name from list_mcp_tools " +
					"(e.g. mcp_fs_read) or the original remote name (e.g. read). " +
					"Use tools to load several at once.",
			},
			"tools": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
				"description": "Tool names to load in one call (same naming rules as tool). " +
					"Duplicates are ignored.",
			},
		},
		"required": []string{"server"},
	}
}

// Execute loads the requested tools' specs and registers them. Registration is
// skipped per tool when the name is already registered (a built-in, a parent
// layer's tool or an earlier load): the existing instance always wins, so
// repeated calls are safe and a base-registry tool can never be shadowed by a
// remote one. When a tool is skipped, the result renders the RETAINED entry's
// description and schema — the model must see what will actually run (see
// renderRetainedTool).
//
// Names may come from "tool" (one) and/or "tools" (several); the union is
// deduplicated twice — first on the requested string, then on the resolved
// registry name, so asking for both "read" and "mcp_fs_read" registers and
// renders the tool once.
//
// Partial success is a success: the tools that were found are registered and
// rendered, and a trailing note names the ones that were not. Unknown names
// are always answered with a pointer to list_mcp_tools instead of the server's
// full name list: dumping every name here would re-spend the context the
// listing step was introduced to save.
func (t *GetMCPToolTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.loader == nil || t.registry == nil || t.factory == nil {
		return ErrorResult("get_mcp_tool is not configured (missing loader, registry or tool factory)")
	}
	server, _ := args["server"].(string)
	server = strings.TrimSpace(server)
	if server == "" {
		return ErrorResult(fmt.Sprintf(`missing required argument "server"; known servers: %s`, knownServers(t.loader)))
	}
	names := requestedToolNames(args)
	if len(names) == 0 {
		return ErrorResult(fmt.Sprintf(
			`missing required argument "tool" (or "tools"); use list_mcp_tools with {"server": %q} to see the available tools`,
			server))
	}
	specs, err := t.loader.LoadServer(ctx, server)
	if err != nil {
		return ErrorResult(loadError(t.loader, server, err))
	}

	// Resolve every requested name before registering anything, so a call
	// either reports its full outcome or fails without touching the registry.
	// Both lists preserve the caller's order.
	found := make([]LoadedMCPTool, 0, len(names))
	var missing []string
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		spec, ok := findToolSpec(specs, name)
		if !ok {
			missing = append(missing, name)
			continue
		}
		if _, duplicate := seen[spec.Name]; duplicate {
			continue
		}
		seen[spec.Name] = struct{}{}
		found = append(found, spec)
	}
	if len(found) == 0 {
		return ErrorResult(fmt.Sprintf(
			`tool(s) %s not found on MCP server %q; call list_mcp_tools with {"server": %q} to see the available tools`,
			quoteNames(missing), server, server))
	}

	blocks := make([]string, 0, len(found))
	for _, spec := range found {
		// A name that is already taken is never overwritten (skip-if-present),
		// so the description and schema this call reports must come from the
		// entry that will actually run — see renderRetainedTool.
		if existing, exists := t.registry.Get(spec.Name); exists {
			blocks = append(blocks, renderRetainedTool(server, spec, existing))
			continue
		}
		t.registry.Register(t.factory(spec))
		blocks = append(blocks, renderToolSpec(server, spec))
	}
	return NewToolResult(renderToolSpecs(server, blocks, missing))
}

// requestedToolNames collects the tool names of one call, in caller order and
// deduplicated: "tool" (a single string) first, then "tools" (a JSON array,
// which arrives as []interface{}, or a []string from in-process callers).
// Blank and non-string entries are skipped, so an empty or malformed list ends
// up as "no name requested" rather than as a bogus lookup.
func requestedToolNames(args map[string]interface{}) []string {
	names := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, duplicate := seen[name]; duplicate {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	if name, ok := args["tool"].(string); ok {
		add(name)
	}
	switch list := args["tools"].(type) {
	case []string:
		for _, name := range list {
			add(name)
		}
	case []interface{}:
		for _, item := range list {
			if name, ok := item.(string); ok {
				add(name)
			}
		}
	}
	return names
}

// quoteNames renders a name list as the quoted, comma-joined enumeration the
// error and the partial-success note show. Quoting matches %q, so a single
// name reads the same as it did when only one tool could be requested.
func quoteNames(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = strconv.Quote(name)
	}
	return strings.Join(quoted, ", ")
}

// findToolSpec resolves a caller-supplied name against the server's specs,
// accepting either the namespaced registry name ("mcp_fs_read") or the
// original remote name ("read"). Matching is exact and case-sensitive: MCP
// tool names are, and a fuzzy match would register a tool the model did not
// ask for.
//
// Registry names win over remote names, and the two are searched in SEPARATE
// passes. A server may legitimately expose a tool called "mcp_fs_read" next to
// "read": comparing both fields in one pass would resolve the listed name
// "mcp_fs_read" through spec.RemoteName of the OTHER tool, and asking for both
// listed names would collapse them into a single tool.
func findToolSpec(specs []LoadedMCPTool, name string) (LoadedMCPTool, bool) {
	for _, spec := range specs {
		if spec.Name == name {
			return spec, true
		}
	}
	for _, spec := range specs {
		if spec.RemoteName == name {
			return spec, true
		}
	}
	return LoadedMCPTool{}, false
}

// orderByUsage returns a copy of specs ordered by recorded usage (most used
// first) with the registry name as a deterministic tie-break, so equal-usage
// tools always list in the same order across calls.
func orderByUsage(specs []LoadedMCPTool, counts map[string]uint64) []LoadedMCPTool {
	ordered := make([]LoadedMCPTool, len(specs))
	copy(ordered, specs)
	slices.SortStableFunc(ordered, func(a, b LoadedMCPTool) int {
		if diff := cmp.Compare(counts[b.RemoteName], counts[a.RemoteName]); diff != 0 {
			return diff
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return ordered
}

// renderToolList builds the schema-free listing page: a header with the
// totals and the page position, one line per tool (name, one-line description
// and usage count when it has been used) and a pointer to get_mcp_tool.
func renderToolList(server string, window []LoadedMCPTool, counts map[string]uint64, page, pageSize, totalPages, total int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "MCP server %q: %d tool(s), page %d/%d (page size %d). Ordered by usage frequency.\n",
		server, total, page, totalPages, pageSize)
	for _, spec := range window {
		fmt.Fprintf(&b, "\n- %s", spec.Name)
		if desc := shortDescription(spec.Description); desc != "" {
			fmt.Fprintf(&b, " — %s", desc)
		}
		b.WriteString(usageSuffix(counts[spec.RemoteName]))
	}
	fmt.Fprintf(&b, "\n\nCall get_mcp_tool with {\"server\": %q, \"tool\": \"<name>\"} to load a tool's full schema and make it callable.\n",
		server)
	return b.String()
}

// renderToolSpec builds the result of a freshly registered tool: it is now
// callable, and here is its full description and pretty-printed input schema.
func renderToolSpec(server string, spec LoadedMCPTool) string {
	return fmt.Sprintf("Tool %s from MCP server %q is now callable.\n", spec.Name, server) +
		renderSpecBody(spec.Description, spec.InputSchema)
}

// renderRetainedTool builds the result of a name that was ALREADY registered
// and therefore not overwritten (skip-if-present — the base registry is never
// shadowed).
//
// The rendered description and schema always come from the retained entry, not
// from the remote spec: the remote spec describes a tool the registry does not
// run, so advertising it would make the model call the retained tool with the
// wrong arguments (a built-in that happens to share the name has its own
// description and its own schema). The entry is only attributed to the
// requested server when it IS that server's tool — which is exactly the
// repeated-load case, where description and schema match the remote spec.
func renderRetainedTool(server string, spec LoadedMCPTool, existing Tool) string {
	if sameRemoteTool(spec, existing) {
		return fmt.Sprintf("Tool %s from MCP server %q is already registered and callable.\n", spec.Name, server) +
			renderSpecBody(spec.Description, spec.InputSchema)
	}
	return fmt.Sprintf("Tool %s is already registered and callable; the existing registration wins over MCP server %q.\n",
		spec.Name, server) +
		renderSpecBody(existing.Description(), existing.Parameters())
}

// sameRemoteTool reports whether the retained registry entry is the tool the
// remote spec describes, i.e. the name belongs to this server's tool and the
// call is a repeated (idempotent) load. Description and input schema are
// compared because they are what the model reads: a name match alone says
// nothing about which tool runs.
func sameRemoteTool(spec LoadedMCPTool, existing Tool) bool {
	if existing == nil {
		return false
	}
	if strings.TrimSpace(existing.Description()) != strings.TrimSpace(spec.Description) {
		return false
	}
	return schemasEqual(existing.Parameters(), spec.InputSchema)
}

// schemasEqual compares two input schemas by their rendered JSON. Absent and
// empty schemas are equal (pkg/mcp substitutes a default schema for both, and
// prettySchema renders either as "{}"), and json.Marshal sorts map keys, so the
// comparison is stable.
func schemasEqual(a, b map[string]interface{}) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return prettySchema(a) == prettySchema(b)
}

// renderSpecBody renders the description (when there is one) plus the
// pretty-printed input schema, in the shape every load result uses.
func renderSpecBody(description string, schema map[string]interface{}) string {
	var b strings.Builder
	if desc := strings.TrimSpace(description); desc != "" {
		fmt.Fprintf(&b, "\n%s\n", desc)
	}
	fmt.Fprintf(&b, "\ninputSchema:\n```json\n%s\n```\n", prettySchema(schema))
	return b.String()
}

// renderToolSpecs joins the rendered spec blocks of one call. A single block
// with nothing missing is returned verbatim, so a one-tool load reads exactly
// as it did before batches existed; two or more blocks get a one-line summary
// header. Names that were not found are reported in a trailing note, which
// keeps a partial success usable instead of failing the whole call.
func renderToolSpecs(server string, blocks []string, missing []string) string {
	var b strings.Builder
	if len(blocks) > 1 {
		fmt.Fprintf(&b, "Loaded %d tool(s) from MCP server %q.\n\n", len(blocks), server)
	}
	// Each block already ends with a newline, so a single "\n" between them
	// renders one blank line of separation.
	b.WriteString(strings.Join(blocks, "\n"))
	if len(missing) > 0 {
		fmt.Fprintf(&b, "\nNot found on MCP server %q: %s. Call list_mcp_tools with {\"server\": %q} to see the available tools.\n",
			server, quoteNames(missing), server)
	}
	return b.String()
}

// shortDescription reduces a tool description to the single trimmed line the
// listing shows: everything after the first newline is dropped and the rest is
// truncated to maxMCPToolDescriptionRunes runes with an ellipsis.
func shortDescription(description string) string {
	line, _, _ := strings.Cut(description, "\n")
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) <= maxMCPToolDescriptionRunes {
		return line
	}
	return string(runes[:maxMCPToolDescriptionRunes]) + "…"
}

// usageSuffix renders the "(used N times)" marker of one listed tool, or ""
// when the tool has never been called.
func usageSuffix(count uint64) string {
	switch count {
	case 0:
		return ""
	case 1:
		return " (used 1 time)"
	default:
		return fmt.Sprintf(" (used %d times)", count)
	}
}

// positiveIntArg reads an optional positive integer argument. JSON numbers
// arrive as float64 but int/int64 are accepted too (in-process callers);
// anything missing, non-numeric or smaller than 1 falls back to def. A value
// above limit is clamped to limit; a non-positive limit means "no ceiling".
func positiveIntArg(args map[string]interface{}, key string, def, limit int) int {
	value, ok := toInt(args[key])
	if !ok || value < 1 {
		return def
	}
	if limit > 0 && value > limit {
		return limit
	}
	return value
}
