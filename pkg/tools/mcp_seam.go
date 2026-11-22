package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type LoadedMCPTool struct {
	Name        string
	Server      string
	RemoteName  string
	Description string
	InputSchema map[string]interface{}
}

type MCPServerInfo struct {
	Name        string
	Description string
	Layer       string
}

type MCPLoader interface {
	Servers() []MCPServerInfo
	LoadServer(ctx context.Context, server string) ([]LoadedMCPTool, error)
	ToolUsage(server string) map[string]uint64
}

const LoadMCPToolsName = "load_mcp_tools"

func loadError(loader MCPLoader, server string, err error) string {
	return fmt.Sprintf("failed to load MCP server %q: %v\nknown servers: %s",
		server, err, knownServers(loader))
}

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
