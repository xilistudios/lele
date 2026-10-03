package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// LoadedMCPTool is one tool specification fetched from an MCP server.
// It is produced by pkg/mcp (which implements MCPLoader) and consumed here by
// LoadMCPToolsTool; pkg/tools never imports pkg/mcp or mcp-go.
//
// The spec carries its origin (Server) end to end: the loader registers it
// through the injected factory without needing any manager-side lookup, so a
// spec that is deliberately NOT in the manager's loaded cache (a mid-flight
// config change) still binds to a live server instead of a dead instance.
type LoadedMCPTool struct {
	// Name is the name actually registered in the ToolRegistry. It is
	// already namespaced and sanitized by pkg/mcp (e.g. "mcp_fs_read") so
	// this package only has to look it up with registry.Get(spec.Name) —
	// no dependency on the naming code.
	Name string
	// Server is the name of the MCP server that produced this spec (set by
	// pkg/mcp's buildSpecs). The factory binds the concrete tool to it
	// directly — never through a cache that a config edit can clear. Empty
	// only for specs built by a foreign (non-pkg/mcp) loader.
	Server string
	// RemoteName is the original MCP tool name on the server, used when
	// forwarding the call.
	RemoteName string
	// Description is the human-readable tool description shown to the LLM.
	Description string
	// InputSchema is the JSON-Schema object for the tool arguments.
	InputSchema map[string]interface{}
}

// MCPServerInfo is the summary of one configured MCP server, enough for the
// system prompt ("## MCP Servers") without exposing any tool schema.
type MCPServerInfo struct {
	Name        string
	Description string
	Layer       string // "project" | "agent" | "global"
}

// MCPLoader is the seam between pkg/tools and the MCP client manager. It is
// implemented by *mcp.Manager; defining it here keeps the dependency
// direction pkg/mcp → pkg/tools one-way (a reverse import would cycle).
type MCPLoader interface {
	// Servers returns the currently configured servers, sorted, excluding
	// disabled and invalid ones (they never reach the prompt).
	Servers() []MCPServerInfo
	// LoadServer returns the tool specs of one server. It is idempotent:
	// repeated calls return the same specs without re-dialing.
	LoadServer(ctx context.Context, server string) ([]LoadedMCPTool, error)
}

// LoadMCPToolsName is the registry name of the built-in loader tool.
const LoadMCPToolsName = "load_mcp_tools"

// LoadMCPToolsTool lets the LLM load the tool specs of one MCP server on
// demand. Before loading, the LLM only sees server names + descriptions (in
// the system prompt); after loading, the server's tools are registered in the
// ToolRegistry and become ordinary tool calls.
type LoadMCPToolsTool struct {
	loader   MCPLoader
	registry *ToolRegistry
	// factory builds the concrete Tool for a loaded spec. It is injected so
	// pkg/tools never references pkg/mcp: the caller passes a closure over
	// the MCP manager (see mcp.Manager.ToolFactory).
	factory func(LoadedMCPTool) Tool
}

var _ Tool = (*LoadMCPToolsTool)(nil)

// NewLoadMCPToolsTool wires the loader tool. factory must never return nil
// for a spec (the MCP-side factory is total).
func NewLoadMCPToolsTool(loader MCPLoader, registry *ToolRegistry, factory func(LoadedMCPTool) Tool) *LoadMCPToolsTool {
	return &LoadMCPToolsTool{
		loader:   loader,
		registry: registry,
		factory:  factory,
	}
}

// Name returns the registry name of this tool.
func (t *LoadMCPToolsTool) Name() string { return LoadMCPToolsName }

// Description tells the model where server names come from and what loading
// does: specs are fetched on demand and the tools become callable normally.
func (t *LoadMCPToolsTool) Description() string {
	return "Load the tool specifications of one MCP server on demand. " +
		"MCP servers are listed in the '## MCP Servers' section of the context; " +
		`call with {"server": "<name>"} to load that server's tool specs ` +
		"(name, description and input schema). The loaded tools then become " +
		"callable as regular tools."
}

// Parameters returns the JSON schema for this tool: a single required
// "server" string.
func (t *LoadMCPToolsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"server": map[string]interface{}{
				"type":        "string",
				"description": "MCP server name (see the '## MCP Servers' section of the context)",
			},
		},
		"required": []string{"server"},
	}
}

// Execute loads the requested server and registers any tool that is not in
// the registry yet. Existing names are never overwritten (a name may belong
// to a built-in or to an earlier load), which makes repeated calls safe.
// The result lists every spec — newly registered or already present — so the
// LLM knows what it can call now.
func (t *LoadMCPToolsTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.loader == nil || t.registry == nil || t.factory == nil {
		return ErrorResult("load_mcp_tools is not configured (missing loader, registry or tool factory)")
	}
	server, _ := args["server"].(string)
	if strings.TrimSpace(server) == "" {
		return ErrorResult(fmt.Sprintf(`missing required argument "server"; known servers: %s`, t.knownServers()))
	}
	specs, err := t.loader.LoadServer(ctx, server)
	if err != nil {
		return ErrorResult(t.loadError(server, err))
	}
	registered, already := t.register(specs)
	return NewToolResult(t.render(server, specs, registered, already))
}

// register adds the specs that are missing from the registry and reports how
// many were newly registered versus already present.
func (t *LoadMCPToolsTool) register(specs []LoadedMCPTool) (registered, already int) {
	for _, spec := range specs {
		if _, exists := t.registry.Get(spec.Name); exists {
			already++
			continue
		}
		t.registry.Register(t.factory(spec))
		registered++
	}
	return registered, already
}

// loadError renders a LoadServer failure as an ErrorResult message. The
// known-servers list is ALWAYS appended and built from loader.Servers() (the
// authoritative, sorted set): helpful errors never depend on the wording of
// the wrapped error, so no string coupling to any particular loader's
// message is needed.
func (t *LoadMCPToolsTool) loadError(server string, err error) string {
	return fmt.Sprintf("failed to load MCP server %q: %v\nknown servers: %s",
		server, err, t.knownServers())
}

// knownServers returns the sorted server names for error messages.
func (t *LoadMCPToolsTool) knownServers() string {
	infos := t.loader.Servers()
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Name)
	}
	slices.Sort(names)
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// render builds the ForLLM listing: a header with the server, the counts and
// the registration outcome, then name + description + pretty JSON schema for
// every spec.
func (t *LoadMCPToolsTool) render(server string, specs []LoadedMCPTool, registered, already int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Loaded %d tool(s) from MCP server %q: %d newly registered, %d already present.\n",
		len(specs), server, registered, already)
	for _, spec := range specs {
		fmt.Fprintf(&b, "\n## %s\n%s\ninputSchema:\n```json\n%s\n```\n",
			spec.Name, spec.Description, prettySchema(spec.InputSchema))
	}
	return b.String()
}

// prettySchema renders an input schema as indented JSON; empty schemas render
// as "{}" instead of "null".
func prettySchema(schema map[string]interface{}) string {
	if len(schema) == 0 {
		return "{}"
	}
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}
