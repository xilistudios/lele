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
	// namePrefix namespaces every remote tool ("mcp_" + server + "_" +
	// tool) so a server can never shadow a built-in lele tool.
	namePrefix = "mcp_"
	// maxToolNameLen is the provider cap for tool names (OpenAI, Gemini).
	maxToolNameLen = 64
	// hashSuffixLen is the number of hex chars appended when a name must be
	// truncated to stay under maxToolNameLen.
	hashSuffixLen = 6
)

// noOutputText is rendered when a tool result carries no displayable part.
const noOutputText = "(no output)"

// maxCallResultChars caps the total ForLLM text of one remote tool result
// (text parts + structured block), mirroring the 50000-char convention of
// pkg/tools/web.go: a runaway server must not blow up the model context.
const maxCallResultChars = 50000

// RemoteTool adapts one remote MCP tool to the tools.Tool interface. It holds
// no connection of its own: Execute goes through the shared Manager, which
// owns reconnects.
type RemoteTool struct {
	server     string // MCP server name (may be empty if not resolved: see Manager.ToolFactory)
	remote     string // original tool name on the server
	registered string // registry name (already namespaced)
	desc       string
	schema     map[string]interface{} // read-only after construction
	mgr        *Manager
}

var _ tools.Tool = (*RemoteTool)(nil)

// NewRemoteTool builds the adapter for one spec of one server.
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

// ToolFactory returns a factory for LoadMCPToolsTool: given a loaded spec it
// builds the RemoteTool bound to this manager. The server comes from the
// spec itself (spec.Server, stamped by buildSpecs), so a spec that is NOT in
// the loaded cache — a mid-flight config change deliberately does not
// re-store it (N1) — still binds to its live server instead of executing
// dead ("not linked to a loaded server"), which skip-if-present registration
// would then keep forever. Only a foreign spec without a Server (built by a
// non-pkg/mcp loader) falls back to the loaded-cache reverse lookup; a miss
// there leaves the server empty and Execute reports the reload hint. This is
// the pkg/mcp half of the pkg/tools ↔ pkg/mcp wiring; pkg/tools only sees
// func(LoadedMCPTool) Tool.
func (m *Manager) ToolFactory() func(tools.LoadedMCPTool) tools.Tool {
	return func(spec tools.LoadedMCPTool) tools.Tool {
		return NewRemoteTool(m, m.toolServer(spec), spec)
	}
}

// toolServer picks the server a spec belongs to: the spec's own Server field
// when set (always the case for buildSpecs output — the loaded cache is
// never consulted), otherwise the reverse lookup over the loaded set for
// foreign specs, "" when neither knows it.
func (m *Manager) toolServer(spec tools.LoadedMCPTool) string {
	if spec.Server != "" {
		return spec.Server
	}
	return m.serverOf(spec.Name)
}

// Name returns the registry name of this tool.
func (r *RemoteTool) Name() string { return r.registered }

// Description returns the server-declared description.
func (r *RemoteTool) Description() string { return r.desc }

// Parameters returns the server-declared input schema. The map is SHARED
// across registrations of this tool and must be treated as read-only —
// callers (providers) must never mutate it, so no defensive copy is made
// per call.
func (r *RemoteTool) Parameters() map[string]interface{} {
	if r.schema == nil {
		return defaultInputSchema()
	}
	return r.schema
}

// Execute forwards the call to the MCP server through the manager, mapping
// the MCP result onto a tools.ToolResult (text + structured content,
// IsError ⇒ ErrorResult).
func (r *RemoteTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	if r.mgr == nil {
		return tools.ErrorResult(fmt.Sprintf("mcp tool %q: no manager configured", r.registered))
	}
	if r.server == "" {
		return tools.ErrorResult(fmt.Sprintf("mcp tool %q is not linked to a loaded server; reload it with %s",
			r.registered, tools.LoadMCPToolsName))
	}
	res, err := r.mgr.CallRemote(ctx, r.server, r.remote, args)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("mcp tool %s.%s failed: %v", r.server, r.remote, err)).WithError(err)
	}
	return mapCallResult(res)
}

// NamespacedName builds the registry name for a remote tool:
// namePrefix + server + "_" + tool, sanitized to [a-zA-Z0-9_-] (any other
// rune becomes "_", runs of "_" collapse, leading/trailing separators are
// trimmed). Hyphens are preserved as-is.
//
// Names longer than 64 chars are cut to 57 and suffixed with "_" plus a
// 6-hex-char FNV-1a hash of the pre-sanitization name, so distinct long names
// stay distinct (truncate+hash instead of blind truncate).
//
// Collision note: the mapping is not injective — server "a" + tool "b_c" and
// server "a_b" + tool "c" both sanitize to "mcp_a_b_c". The registry never
// overwrites an existing name (see LoadMCPToolsTool.register), so the worst
// case is that the second tool reports as "already present".
func NamespacedName(server, tool string) string {
	full := namePrefix + server + "_" + tool
	sanitized := sanitizeName(full)
	if len(sanitized) <= maxToolNameLen {
		return sanitized
	}
	// 57 + "_" + 6 hex chars = 64.
	keep := maxToolNameLen - hashSuffixLen - 1
	prefix := strings.TrimRight(sanitized[:keep], "_-")
	return prefix + "_" + fnv1aHex(full)
}

// sanitizeName maps s onto the safe charset: alphanumerics, "_" and "-" are
// kept; every other rune becomes "_"; runs of "_" collapse into one; the
// result is trimmed of leading/trailing "_" and "-". (The input always starts
// with namePrefix, so the result is never empty.)
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
		// "_" and every invalid rune map to "_" with run collapsing.
		if !prevSep {
			b.WriteByte('_')
		}
		prevSep = true
	}
	return strings.Trim(b.String(), "_-")
}

// fnv1aHex returns the low 24 bits of the FNV-1a hash of s as exactly 6 hex
// chars (used to disambiguate truncated names).
func fnv1aHex(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%06x", h.Sum32()&0xffffff)
}

// defaultInputSchema is the permissive fallback for servers that declare no
// usable schema (always a valid JSON-Schema object for the provider).
func defaultInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

// SchemaFromMCPTool extracts the tool's inputSchema into a generic map,
// whether it was declared structurally (mcp.Tool.InputSchema) or as raw JSON
// (mcp.Tool.RawInputSchema): both end up as "inputSchema" when the tool is
// marshaled. Absent or empty schemas degrade to defaultInputSchema so the
// provider always receives a valid schema.
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
		// mcp-go's zero schema marshals as {"type":"","properties":{}} —
		// treat it as undeclared.
		return defaultInputSchema()
	}
	return wire.InputSchema
}

// mapCallResult converts an MCP tool result into a tools.ToolResult:
// text parts are joined with "\n"; StructuredContent (or its raw bytes) is
// appended as a fenced ```json block; IsError produces an ErrorResult;
// empty or nil content renders as noOutputText.
//
// v1 limitation (plan §2.1): only text parts are surfaced — image, audio and
// embedded-resource parts are ignored.
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

// capCallResult truncates body at maxCallResultChars bytes, marking how many
// bytes were dropped so the model knows the output is incomplete. The cut
// never lands inside a multi-byte rune: the index backs off to the nearest
// rune start, so the kept prefix is always valid UTF-8 (a split rune would
// render as a replacement glyph and fail strict UTF-8 validators).
func capCallResult(body string) string {
	if len(body) <= maxCallResultChars {
		return body
	}
	cut := maxCallResultChars
	// body[cut] is the first byte AFTER the kept prefix: while it is a UTF-8
	// continuation byte, the rune at the boundary straddles the cut.
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	remaining := len(body) - cut
	return body[:cut] + fmt.Sprintf("\n…[truncated, %d more bytes]…", remaining)
}

// structuredBlock renders StructuredContent as a fenced JSON block, preferring
// the raw bytes when present (they are the original wire payload).
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
