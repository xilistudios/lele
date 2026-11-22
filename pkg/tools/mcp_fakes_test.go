package tools

import (
	"context"
	"fmt"
)

type fakeLoader struct {
	servers []MCPServerInfo
	specs   map[string][]LoadedMCPTool
	err     error
	loads   int
	usage   map[string]map[string]uint64
}

var _ MCPLoader = (*fakeLoader)(nil)

func (f *fakeLoader) Servers() []MCPServerInfo { return f.servers }

func (f *fakeLoader) ToolUsage(server string) map[string]uint64 { return f.usage[server] }

func (f *fakeLoader) LoadServer(_ context.Context, server string) ([]LoadedMCPTool, error) {
	f.loads++
	if f.err != nil {
		return nil, f.err
	}
	if specs, ok := f.specs[server]; ok {
		return specs, nil
	}
	return nil, fmt.Errorf("mcp server %q is not configured", server)
}

type fakeTool struct {
	name   string
	desc   string
	schema map[string]interface{}
}

func (f *fakeTool) Name() string               { return f.name }
func (f *fakeTool) Description() string        { return f.desc }
func (f *fakeTool) Parameters() map[string]any { return f.schema }
func (f *fakeTool) Execute(context.Context, map[string]any) *ToolResult {
	return NewToolResult("fake " + f.name)
}

type factoryRecorder struct {
	built []string
}

func (r *factoryRecorder) build(spec LoadedMCPTool) Tool {
	r.built = append(r.built, spec.Name)
	return &fakeTool{name: spec.Name, desc: spec.Description, schema: spec.InputSchema}
}

func specSet() []LoadedMCPTool {
	return []LoadedMCPTool{
		{
			Name:        "mcp_fs_read",
			Server:      "fs",
			RemoteName:  "read",
			Description: "Read a file",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
				},
			},
		},
		{Name: "mcp_fs_write", Server: "fs", RemoteName: "write", Description: "Write a file",
			InputSchema: map[string]interface{}{"type": "object"}},
		{Name: "mcp_fs_stat", Server: "fs", RemoteName: "stat", Description: "File stats",
			InputSchema: map[string]interface{}{"type": "object"}},
	}
}
