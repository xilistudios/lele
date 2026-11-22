package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/xilistudios/lele/pkg/tools"
)

const (
	namePrefix     = "mcp_"
	maxToolNameLen = 64
	hashSuffixLen  = 6
)

const noOutputText = "(no output)"

const maxCallResultChars = 50000

type RemoteTool struct {
	server     string
	remote     string
	registered string
	desc       string
	schema     map[string]interface{}
	mgr        *Manager
}

var _ tools.Tool = (*RemoteTool)(nil)

func NewRemoteTool(mgr *Manager, server string, spec tools.LoadedMCPTool) *RemoteTool {
	return &RemoteTool{
		server:     server,
		remote:     spec.RemoteName,
		registered: spec.Name,
		desc:       spec.Description,
		schema:     spec.InputSchema,
		mgr:        mgr,
	}
}

func (m *Manager) ToolFactory() func(tools.LoadedMCPTool) tools.Tool {
	return func(spec tools.LoadedMCPTool) tools.Tool {
		return NewRemoteTool(m, m.toolServer(spec), spec)
	}
}

func (m *Manager) toolServer(spec tools.LoadedMCPTool) string {
	if spec.Server != "" {
		return spec.Server
	}
	return m.serverOf(spec.Name)
}

func (r *RemoteTool) Name() string { return r.registered }

func (r *RemoteTool) Description() string { return r.desc }

func (r *RemoteTool) Parameters() map[string]interface{} {
	if r.schema == nil {
		return defaultInputSchema()
	}
	return r.schema
}

func (r *RemoteTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	if r.mgr == nil {
		return tools.ErrorResult(fmt.Sprintf("mcp tool %q: no manager configured", r.registered))
	}
	if r.server == "" {
		return tools.ErrorResult(fmt.Sprintf("mcp tool %q is not linked to a loaded server; reload it with %s",
			r.registered, tools.GetMCPToolName))
	}
	res, err := r.mgr.CallRemote(ctx, r.server, r.remote, args)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("mcp tool %s.%s failed: %v", r.server, r.remote, err)).WithError(err)
	}
	return mapCallResult(res)
}

func NamespacedName(server, tool string) string {
	full := namePrefix + server + "_" + tool
	sanitized := sanitizeName(full)
	if len(sanitized) <= maxToolNameLen {
		return sanitized
	}
	keep := maxToolNameLen - hashSuffixLen - 1
	prefix := strings.TrimRight(sanitized[:keep], "_-")
	return prefix + "_" + fnv1aHex(full)
}

func sanitizeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSep := false
	for _, r := range s {
		valid := r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			prevSep = false
			continue
		}
		if !prevSep {
			b.WriteByte('_')
		}
		prevSep = true
	}
	return strings.Trim(b.String(), "_-")
}

func fnv1aHex(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%06x", h.Sum32()&0xffffff)
}

func defaultInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func SchemaFromMCPTool(t mcp.Tool) map[string]interface{} {
	data, err := json.Marshal(t)
	if err != nil {
		return defaultInputSchema()
	}
	var wire struct {
		InputSchema map[string]interface{} `json:"inputSchema"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return defaultInputSchema()
	}
	if len(wire.InputSchema) == 0 {
		return defaultInputSchema()
	}
	props, _ := wire.InputSchema["properties"].(map[string]interface{})
	typeVal, _ := wire.InputSchema["type"].(string)
	if typeVal == "" && len(props) == 0 {
		return defaultInputSchema()
	}
	return wire.InputSchema
}

func mapCallResult(res *mcp.CallToolResult) *tools.ToolResult {
	if res == nil {
		return tools.NewToolResult(noOutputText)
	}
	var texts []string
	for _, part := range res.Content {
		switch v := part.(type) {
		case mcp.TextContent:
			texts = append(texts, v.Text)
		case *mcp.TextContent:
			if v != nil {
				texts = append(texts, v.Text)
			}
		}
	}
	body := strings.Join(texts, "\n")
	if block := structuredBlock(res); block != "" {
		if body != "" {
			body += "\n"
		}
		body += block
	}
	if strings.TrimSpace(body) == "" {
		body = noOutputText
	}
	body = capCallResult(body)
	if res.IsError {
		return tools.ErrorResult(body)
	}
	return tools.NewToolResult(body)
}

func capCallResult(body string) string {
	if len(body) <= maxCallResultChars {
		return body
	}
	cut := maxCallResultChars
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	remaining := len(body) - cut
	return body[:cut] + fmt.Sprintf("\n…[truncated, %d more bytes]…", remaining)
}

func structuredBlock(res *mcp.CallToolResult) string {
	var payload interface{}
	switch {
	case len(res.RawStructuredContent) > 0:
		payload = res.RawStructuredContent
	case res.StructuredContent != nil:
		payload = res.StructuredContent
	default:
		return ""
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ""
	}
	return "```json\n" + string(data) + "\n```"
}
