package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/xilistudios/lele/pkg/tools"
)

// mgrWithConn returns a manager over the basic fixture whose "srv" connection
// is already installed (no dialing involved).
func mgrWithConn(t *testing.T, conn ClientConn) *Manager {
	t.Helper()
	mgr := NewManager(basicFixture(t), &fakeDialer{})
	mgr.conns["srv"] = conn
	return mgr
}

// --- T6: naming -------------------------------------------------------------

// TestNamespacedName covers clean passthrough, sanitization and trimming.
func TestNamespacedName(t *testing.T) {
	cases := []struct {
		server, tool, want string
	}{
		{"fs", "read", "mcp_fs_read"},
		{"already-clean", "do-thing", "mcp_already-clean_do-thing"},
		{"my.server", "read_file.txt", "mcp_my_server_read_file_txt"},
		{"a/b", "c d", "mcp_a_b_c_d"},
		{"sërvér", "über", "mcp_s_rv_r_ber"},
		{"srv", "read/../../etc", "mcp_srv_read_etc"},
		{"_x", "t", "mcp_x_t"},
		{"srv", "tool.", "mcp_srv_tool"},
		{"-lead", "t", "mcp_-lead_t"},
	}
	for _, tc := range cases {
		t.Run(tc.server+"/"+tc.tool, func(t *testing.T) {
			got := NamespacedName(tc.server, tc.tool)
			if got != tc.want {
				t.Errorf("NamespacedName(%q, %q) = %q, want %q", tc.server, tc.tool, got, tc.want)
			}
			if len(got) > maxToolNameLen {
				t.Errorf("name %q is %d chars, cap is %d", got, len(got), maxToolNameLen)
			}
		})
	}
}

// TestNamespacedNameLongTruncatesWithHash: >64 chars ⇒ cut + "_" + 6-hex
// FNV-1a, deterministic and distinguishing tools that differ only past the cut.
func TestNamespacedNameLongTruncatesWithHash(t *testing.T) {
	server := strings.Repeat("a", 60)

	first := NamespacedName(server, "doThingA")
	second := NamespacedName(server, "doThingB")

	for _, name := range []string{first, second} {
		if len(name) > maxToolNameLen {
			t.Errorf("long name %q is %d chars, cap is %d", name, len(name), maxToolNameLen)
		}
	}
	if again := NamespacedName(server, "doThingA"); again != first {
		t.Errorf("not deterministic: %q vs %q", first, again)
	}
	if first == second {
		t.Errorf("tools differing after the truncation point collided: %q", first)
	}
	if !strings.HasPrefix(first, "mcp_aaaa") {
		t.Errorf("truncated name should keep its prefix, got %q", first)
	}
	// A second truncation-style server with separators still fits.
	noisy := NamespacedName(strings.Repeat("x.y", 30), "tool")
	if len(noisy) > maxToolNameLen {
		t.Errorf("noisy long name is %d chars, cap is %d", len(noisy), maxToolNameLen)
	}
}

// --- T6: schema extraction --------------------------------------------------

// TestSchemaFromMCPTool covers structured, raw and empty schemas.
func TestSchemaFromMCPTool(t *testing.T) {
	t.Run("structured", func(t *testing.T) {
		got := SchemaFromMCPTool(remoteTool("read"))
		if got["type"] != "object" {
			t.Errorf("type = %v, want object", got["type"])
		}
		props, ok := got["properties"].(map[string]interface{})
		if !ok || len(props) != 1 {
			t.Errorf("properties = %v, want one entry", got["properties"])
		}
	})

	t.Run("raw json schema", func(t *testing.T) {
		raw := json.RawMessage(`{"type":"object","properties":{"n":{"type":"number"}},"required":["n"]}`)
		got := SchemaFromMCPTool(mcp.Tool{Name: "t", RawInputSchema: raw})
		props, ok := got["properties"].(map[string]interface{})
		if !ok || len(props) != 1 {
			t.Fatalf("properties = %v, want raw schema props", got["properties"])
		}
		if _, ok := got["required"]; !ok {
			t.Errorf("raw schema should keep required, got %v", got)
		}
	})

	t.Run("zero schema falls back", func(t *testing.T) {
		got := SchemaFromMCPTool(mcp.Tool{Name: "t"})
		if got["type"] != "object" {
			t.Errorf("type = %v, want fallback object", got["type"])
		}
		props, ok := got["properties"].(map[string]interface{})
		if !ok || len(props) != 0 {
			t.Errorf("properties = %v, want empty object", got["properties"])
		}
	})

	t.Run("marshal conflict falls back", func(t *testing.T) {
		got := SchemaFromMCPTool(mcp.Tool{
			Name:           "t",
			InputSchema:    mcp.ToolInputSchema{Type: "object"},
			RawInputSchema: json.RawMessage(`{"type":"object"}`),
		})
		if got["type"] != "object" {
			t.Errorf("type = %v, want fallback object", got["type"])
		}
	})
}

// --- T6: result mapping -----------------------------------------------------

// TestMapCallResult pins the MCP result → tools.ToolResult translation.
func TestMapCallResult(t *testing.T) {
	t.Run("single text", func(t *testing.T) {
		res := &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "hello"}},
		}
		out := mapCallResult(res)
		if out.IsError || out.ForLLM != "hello" {
			t.Errorf("out = %+v, want ForLLM hello, no error", out)
		}
	})

	t.Run("multiple text parts join with newline", func(t *testing.T) {
		res := &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "a"},
				&mcp.TextContent{Type: "text", Text: "b"},
			},
		}
		if got := mapCallResult(res).ForLLM; got != "a\nb" {
			t.Errorf("ForLLM = %q, want %q", got, "a\nb")
		}
	})

	t.Run("is error flag", func(t *testing.T) {
		res := &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "bad args"}},
		}
		out := mapCallResult(res)
		if !out.IsError || out.ForLLM != "bad args" {
			t.Errorf("out = %+v, want IsError with bad args", out)
		}
	})

	t.Run("structured content appended as json fence", func(t *testing.T) {
		res := &mcp.CallToolResult{
			Content:           []mcp.Content{mcp.TextContent{Type: "text", Text: "ok"}},
			StructuredContent: map[string]interface{}{"k": "v"},
		}
		want := "ok\n```json\n{\n  \"k\": \"v\"\n}\n```"
		if got := mapCallResult(res).ForLLM; got != want {
			t.Errorf("ForLLM = %q, want %q", got, want)
		}
	})

	t.Run("raw structured content preferred", func(t *testing.T) {
		res := &mcp.CallToolResult{
			RawStructuredContent: json.RawMessage(`{"a":1}`),
		}
		want := "```json\n{\n  \"a\": 1\n}\n```"
		if got := mapCallResult(res).ForLLM; got != want {
			t.Errorf("ForLLM = %q, want %q", got, want)
		}
	})

	t.Run("non-text content ignored", func(t *testing.T) {
		res := &mcp.CallToolResult{
			Content: []mcp.Content{mcp.ImageContent{Type: "image", Data: "Zg==", MIMEType: "image/png"}},
		}
		out := mapCallResult(res)
		if out.ForLLM != noOutputText {
			t.Errorf("ForLLM = %q, want %q", out.ForLLM, noOutputText)
		}
	})

	t.Run("empty and nil", func(t *testing.T) {
		if got := mapCallResult(&mcp.CallToolResult{}).ForLLM; got != noOutputText {
			t.Errorf("empty result ForLLM = %q", got)
		}
		if got := mapCallResult(nil).ForLLM; got != noOutputText {
			t.Errorf("nil result ForLLM = %q", got)
		}
	})
}

// --- T6: RemoteTool ---------------------------------------------------------

// TestRemoteToolContract: identity comes straight from the spec.
func TestRemoteToolContract(t *testing.T) {
	spec := tools.LoadedMCPTool{
		Name:        "mcp_srv_read",
		RemoteName:  "read",
		Description: "reads files",
		InputSchema: map[string]interface{}{"type": "object"},
	}
	rt := NewRemoteTool(nil, "srv", spec)

	if rt.Name() != spec.Name {
		t.Errorf("Name = %q, want %q", rt.Name(), spec.Name)
	}
	if rt.Description() != spec.Description {
		t.Errorf("Description = %q, want %q", rt.Description(), spec.Description)
	}
	if !reflectSameSchema(rt.Parameters(), spec.InputSchema) {
		t.Errorf("Parameters = %v, want the spec schema", rt.Parameters())
	}

	// A spec without schema still yields a valid object schema.
	rtNoSchema := NewRemoteTool(nil, "srv", tools.LoadedMCPTool{Name: "x"})
	if rtNoSchema.Parameters()["type"] != "object" {
		t.Errorf("nil schema fallback = %v, want type object", rtNoSchema.Parameters())
	}
}

func reflectSameSchema(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// TestRemoteToolExecuteSuccess forwards to the manager, returns the mapped
// result and passes the original remote name.
func TestRemoteToolExecuteSuccess(t *testing.T) {
	conn := &fakeConn{
		callResult: &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "file contents"}},
		},
	}
	mgr := mgrWithConn(t, conn)
	rt := NewRemoteTool(mgr, "srv", tools.LoadedMCPTool{
		Name: "mcp_srv_read", RemoteName: "read", Description: "d",
	})

	out := rt.Execute(context.Background(), map[string]interface{}{"path": "/etc/hosts"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if out.ForLLM != "file contents" {
		t.Errorf("ForLLM = %q", out.ForLLM)
	}
	if conn.lastName != "read" {
		t.Errorf("forwarded remote name = %q, want read", conn.lastName)
	}
	if conn.lastArgs["path"] != "/etc/hosts" {
		t.Errorf("forwarded args = %v", conn.lastArgs)
	}
}

// TestRemoteToolExecuteMCPError: application errors surface as ErrorResult
// with the tool name and cause (no reconnect: this is not a transport error).
func TestRemoteToolExecuteMCPError(t *testing.T) {
	conn := &fakeConn{callErrs: []error{errors.New("invalid arguments")}}
	mgr := mgrWithConn(t, conn)
	rt := NewRemoteTool(mgr, "srv", tools.LoadedMCPTool{
		Name: "mcp_srv_read", RemoteName: "read",
	})

	out := rt.Execute(context.Background(), nil)
	if !out.IsError {
		t.Fatal("expected IsError")
	}
	if !strings.Contains(out.ForLLM, "mcp tool srv.read failed") {
		t.Errorf("error should name server.tool, got: %q", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "invalid arguments") {
		t.Errorf("error should keep the cause, got: %q", out.ForLLM)
	}
	if out.Err == nil {
		t.Error("ToolResult.Err should be set")
	}
	if conn.callCalls != 1 {
		t.Errorf("CallTool calls = %d, want 1 (no reconnect for MCP errors)", conn.callCalls)
	}
}

// TestRemoteToolExecuteGuards: missing manager / unresolved server.
func TestRemoteToolExecuteGuards(t *testing.T) {
	rtNoMgr := NewRemoteTool(nil, "srv", tools.LoadedMCPTool{Name: "mcp_srv_read", RemoteName: "read"})
	if out := rtNoMgr.Execute(context.Background(), nil); !out.IsError || !strings.Contains(out.ForLLM, "no manager") {
		t.Errorf("no-manager guard: %+v", out)
	}

	mgr := mgrWithConn(t, &fakeConn{})
	rtNoServer := NewRemoteTool(mgr, "", tools.LoadedMCPTool{Name: "mcp_srv_read", RemoteName: "read"})
	out := rtNoServer.Execute(context.Background(), nil)
	if !out.IsError || !strings.Contains(out.ForLLM, tools.LoadMCPToolsName) {
		t.Errorf("unresolved-server guard should hint to reload: %+v", out)
	}
}

// TestToolFactoryBindsServer covers the DEFENSIVE fallback of the factory: a
// foreign spec with no Server of its own (built by a non-pkg/mcp loader) is
// bound through the manager's loaded set, and a miss there leaves the server
// unresolved so Execute asks for a reload. Specs stamped by buildSpecs bind
// via spec.Server instead — see TestToolFactoryPrefersSpecServerOverLoadedCache.
func TestToolFactoryBindsServer(t *testing.T) {
	conn := &fakeConn{
		callResult: &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "ok"}},
		},
	}
	mgr := mgrWithConn(t, conn)
	spec := tools.LoadedMCPTool{
		Name: "mcp_srv_ping", RemoteName: "ping", Description: "d",
		InputSchema: map[string]interface{}{"type": "object"},
	}
	mgr.loaded["srv"] = []tools.LoadedMCPTool{spec}

	factory := mgr.ToolFactory()
	tool := factory(spec)

	rt, ok := tool.(*RemoteTool)
	if !ok {
		t.Fatalf("factory returned %T, want *RemoteTool", tool)
	}
	if rt.server != "srv" {
		t.Errorf("bound server = %q, want srv", rt.server)
	}
	if out := tool.Execute(context.Background(), nil); out.IsError || out.ForLLM != "ok" {
		t.Errorf("execute through factory: %+v", out)
	}

	// Unknown spec: the server cannot be recovered, Execute asks for a reload.
	unknown := factory(tools.LoadedMCPTool{Name: "mcp_other_ping", RemoteName: "ping"})
	if out := unknown.Execute(context.Background(), nil); !out.IsError || !strings.Contains(out.ForLLM, tools.LoadMCPToolsName) {
		t.Errorf("unknown-spec guard: %+v", out)
	}
}

// --- minor m2: ForLLM result cap --------------------------------------------

// TestMapCallResultTruncation: the total ForLLM text of a remote result is
// capped at maxCallResultChars (the pkg/tools/web.go convention); anything
// under the cap is untouched, anything over it carries the explicit
// truncation marker with the number of dropped bytes.
func TestMapCallResultTruncation(t *testing.T) {
	textResult := func(body string) *mcp.CallToolResult {
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: body}},
		}
	}

	t.Run("under the cap is untouched", func(t *testing.T) {
		body := strings.Repeat("a", maxCallResultChars-1)
		got := mapCallResult(textResult(body)).ForLLM
		if got != body {
			t.Errorf("under-cap result modified (len %d, want %d)", len(got), len(body))
		}
		if strings.Contains(got, "[truncated") {
			t.Error("under-cap result must not carry the truncation marker")
		}
	})

	t.Run("exactly at the cap is untouched", func(t *testing.T) {
		body := strings.Repeat("b", maxCallResultChars)
		got := mapCallResult(textResult(body)).ForLLM
		if got != body {
			t.Errorf("at-cap result modified (len %d, want %d)", len(got), len(body))
		}
	})

	t.Run("over the cap is truncated with the marker", func(t *testing.T) {
		over := maxCallResultChars + 1234
		got := mapCallResult(textResult(strings.Repeat("c", over))).ForLLM
		marker := "\n…[truncated, 1234 more bytes]…"
		if !strings.HasSuffix(got, marker) {
			t.Errorf("result must end with %q, got tail %q", marker, got[len(got)-40:])
		}
		if !strings.HasPrefix(got, strings.Repeat("c", maxCallResultChars)) {
			t.Error("kept prefix must be the first maxCallResultChars bytes")
		}
		if want := maxCallResultChars + len(marker); len(got) != want {
			t.Errorf("truncated length = %d, want %d", len(got), want)
		}
	})

	t.Run("structured content counts toward the cap", func(t *testing.T) {
		res := &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: strings.Repeat("d", maxCallResultChars)}},
			StructuredContent: map[string]interface{}{
				"blob": strings.Repeat("e", 64),
			},
		}
		got := mapCallResult(res).ForLLM
		if !strings.Contains(got, "[truncated, ") {
			t.Errorf("text + structured block over the cap must be truncated, got len %d", len(got))
		}
	})
}

// --- N5: UTF-8-safe result cap ----------------------------------------------

// TestCapCallResultNeverSplitsRune is the N5 regression: a 3-byte rune
// straddling the byte boundary must not be cut in half. The kept prefix backs
// off to the rune start, so the truncated output stays valid UTF-8 — pre-fix
// the raw byte slice left a dangling continuation byte at the end (and one
// byte fewer was counted as dropped).
func TestCapCallResultNeverSplitsRune(t *testing.T) {
	// 49999 ASCII bytes + three 3-byte runes (CJK ideographs, built from
	// code points so the source stays ASCII — see gosmopolitan): the byte at
	// index 49999 starts a rune that straddles the maxCallResultChars
	// boundary (50000).
	prefix := strings.Repeat("a", maxCallResultChars-1)
	cjk := string(rune(0x4E2D)) + string(rune(0x6587)) + string(rune(0x5B57))
	body := prefix + cjk
	if len(body) <= maxCallResultChars {
		t.Fatalf("test body must exceed the cap: len(body) = %d", len(body))
	}
	// Sanity: the boundary really lands inside the first rune.
	if utf8.RuneStart(body[maxCallResultChars]) {
		t.Fatal("test setup: the cut does not straddle a rune (fix the body)")
	}

	got := capCallResult(body)

	if !utf8.ValidString(got) {
		t.Fatalf("truncated output is not valid UTF-8: the cut split a multi-byte rune")
	}
	marker := "\n…[truncated, 9 more bytes]…" // all 9 bytes of the three runes are dropped
	kept, found := strings.CutSuffix(got, marker)
	if !found {
		t.Fatalf("result must end with %q, got tail %q", marker, got[len(got)-40:])
	}
	if kept != prefix {
		t.Errorf("kept prefix = %d bytes, want the %d ASCII bytes before the split rune",
			len(kept), len(prefix))
	}
}
