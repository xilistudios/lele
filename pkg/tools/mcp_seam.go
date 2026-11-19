package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// This file is the seam between pkg/tools and pkg/mcp: the data shapes and the
// loader interface the MCP discovery tools (list_mcp_tools, get_mcp_tool — see
// mcp_discovery.go) work against, plus the few rendering/error helpers they
// share. pkg/tools never imports pkg/mcp or mcp-go, and pkg/mcp implements
// MCPLoader; keeping the interface here is what makes that dependency one-way.

// LoadedMCPTool is one tool specification fetched from an MCP server.
// It is produced by pkg/mcp (which implements MCPLoader) and consumed here by
// the discovery tools — GetMCPToolTool loads and registers its full spec,
// ListMCPToolsTool shows only its name and a one-line description. pkg/tools
// never imports pkg/mcp or mcp-go.
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
	// repeated calls return the same specs without re-dialing. The returned
	// slice is a SHARED cached slice — callers must copy it before sorting.
	LoadServer(ctx context.Context, server string) ([]LoadedMCPTool, error)
	// ToolUsage returns recorded call counts for one server's tools, keyed by
	// RemoteName. May return nil/empty when nothing was recorded yet.
	ToolUsage(server string) map[string]uint64
}

// LoadMCPToolsName is the LEGACY name of the removed bulk loader. It is kept
// only as an allowlist alias: configs that still list load_mcp_tools keep BOTH
// discovery tools enabled (see agent.mcpDiscoveryAllowed). No tool registers
// under this name any more.
const LoadMCPToolsName = "load_mcp_tools"

// loadError renders a LoadServer failure as an ErrorResult message. The
// known-servers list is ALWAYS appended and built from loader.Servers() (the
// authoritative, sorted set): helpful errors never depend on the wording of
// the wrapped error, so no string coupling to any particular loader's
// message is needed.
//
// Shared by every MCP tool in this package (list/get) so all of them answer a
// failed load in exactly the same way.
func loadError(loader MCPLoader, server string, err error) string {
	return fmt.Sprintf("failed to load MCP server %q: %v\nknown servers: %s",
		server, err, knownServers(loader))
}

// knownServers returns the sorted server names for error messages, or
// "(none)" when no server is configured.
func knownServers(loader MCPLoader) string {
	infos := loader.Servers()
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
