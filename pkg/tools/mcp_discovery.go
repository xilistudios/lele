package tools

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const ListMCPToolsName = "list_mcp_tools"

const GetMCPToolName = "get_mcp_tool"

const (
	defaultMCPToolPage         = 1
	defaultMCPToolsPageSize    = 20
	maxMCPToolsPageSize        = 100
	maxMCPToolDescriptionRunes = 120
)

type ListMCPToolsTool struct {
	loader MCPLoader
}

var _ Tool = (*ListMCPToolsTool)(nil)

func NewListMCPToolsTool(loader MCPLoader) *ListMCPToolsTool {
	return &ListMCPToolsTool{loader: loader}
}

func (t *ListMCPToolsTool) Name() string { return ListMCPToolsName }

func (t *ListMCPToolsTool) Description() string {
	return "List the tool names of one MCP server, paginated and ordered by usage frequency (most used first). " +
		"Returns names and short descriptions only — no input schemas. " +
		"Use get_mcp_tool to load one tool's full specification and make it callable."
}

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

type GetMCPToolTool struct {
	loader   MCPLoader
	registry *ToolRegistry
	factory  func(LoadedMCPTool) Tool
}

var _ Tool = (*GetMCPToolTool)(nil)

func NewGetMCPToolTool(loader MCPLoader, registry *ToolRegistry, factory func(LoadedMCPTool) Tool) *GetMCPToolTool {
	return &GetMCPToolTool{
		loader:   loader,
		registry: registry,
		factory:  factory,
	}
}

func (t *GetMCPToolTool) Name() string { return GetMCPToolName }

func (t *GetMCPToolTool) Description() string {
	return "Load the full specification (description and input schema) of one or more MCP tools into " +
		"the context and register them so they become callable as regular tools. " +
		"Pass tool for a single name or tools for several. " +
		"Use list_mcp_tools first to discover tool names, and load only the tools you need."
}

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
		if existing, exists := t.registry.Get(spec.Name); exists {
			blocks = append(blocks, renderRetainedTool(server, spec, existing))
			continue
		}
		t.registry.Register(t.factory(spec))
		blocks = append(blocks, renderToolSpec(server, spec))
	}
	return NewToolResult(renderToolSpecs(server, blocks, missing))
}

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

func quoteNames(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = strconv.Quote(name)
	}
	return strings.Join(quoted, ", ")
}

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

func renderToolSpec(server string, spec LoadedMCPTool) string {
	return fmt.Sprintf("Tool %s from MCP server %q is now callable.\n", spec.Name, server) +
		renderSpecBody(spec.Description, spec.InputSchema)
}

func renderRetainedTool(server string, spec LoadedMCPTool, existing Tool) string {
	if sameRemoteTool(spec, existing) {
		return fmt.Sprintf("Tool %s from MCP server %q is already registered and callable.\n", spec.Name, server) +
			renderSpecBody(spec.Description, spec.InputSchema)
	}
	return fmt.Sprintf("Tool %s is already registered and callable; the existing registration wins over MCP server %q.\n",
		spec.Name, server) +
		renderSpecBody(existing.Description(), existing.Parameters())
}

func sameRemoteTool(spec LoadedMCPTool, existing Tool) bool {
	if existing == nil {
		return false
	}
	if strings.TrimSpace(existing.Description()) != strings.TrimSpace(spec.Description) {
		return false
	}
	return schemasEqual(existing.Parameters(), spec.InputSchema)
}

func schemasEqual(a, b map[string]interface{}) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return prettySchema(a) == prettySchema(b)
}

func renderSpecBody(description string, schema map[string]interface{}) string {
	var b strings.Builder
	if desc := strings.TrimSpace(description); desc != "" {
		fmt.Fprintf(&b, "\n%s\n", desc)
	}
	fmt.Fprintf(&b, "\ninputSchema:\n```json\n%s\n```\n", prettySchema(schema))
	return b.String()
}

func renderToolSpecs(server string, blocks []string, missing []string) string {
	var b strings.Builder
	if len(blocks) > 1 {
		fmt.Fprintf(&b, "Loaded %d tool(s) from MCP server %q.\n\n", len(blocks), server)
	}
	b.WriteString(strings.Join(blocks, "\n"))
	if len(missing) > 0 {
		fmt.Fprintf(&b, "\nNot found on MCP server %q: %s. Call list_mcp_tools with {\"server\": %q} to see the available tools.\n",
			server, quoteNames(missing), server)
	}
	return b.String()
}

func shortDescription(description string) string {
	line, _, _ := strings.Cut(description, "\n")
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) <= maxMCPToolDescriptionRunes {
		return line
	}
	return string(runes[:maxMCPToolDescriptionRunes]) + "…"
}

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
