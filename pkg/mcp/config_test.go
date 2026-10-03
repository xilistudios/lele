package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// writeFileAt creates (including parent dirs) a fixture file and returns path.
func writeFileAt(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// writeConfig writes content to <tempdir>/mcp.json and returns the path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	return writeFileAt(t, filepath.Join(t.TempDir(), "mcp.json"), content)
}

// staticExpand returns a pure expander over a fixed set of variables.
func staticExpand(vars map[string]string) ExpandFunc {
	return newExpand(func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	})
}

func TestParseFileValidEntries(t *testing.T) {
	tests := []struct {
		name       string
		json       string
		want       map[string]ServerConfig
		wantActive []string
	}{
		{
			name: "stdio entry keeps command args env and description",
			json: `{"mcpServers": {"fs": {"command": "npx", "args": ["-y", "server"],
				"env": {"TOKEN": "abc"}, "description": "file tools"}}}`,
			want: map[string]ServerConfig{
				"fs": {
					Command:     "npx",
					Args:        []string{"-y", "server"},
					Env:         map[string]string{"TOKEN": "abc"},
					Description: "file tools",
				},
			},
			wantActive: []string{"fs"},
		},
		{
			name:       "remote http with explicit type",
			json:       `{"mcpServers": {"api": {"url": "https://example.com/mcp", "type": "http"}}}`,
			want:       map[string]ServerConfig{"api": {URL: "https://example.com/mcp", Type: "http"}},
			wantActive: []string{"api"},
		},
		{
			name:       "remote http is the default when type is omitted",
			json:       `{"mcpServers": {"api": {"url": "https://example.com/mcp"}}}`,
			want:       map[string]ServerConfig{"api": {URL: "https://example.com/mcp", Type: "http"}},
			wantActive: []string{"api"},
		},
		{
			name:       "remote sse",
			json:       `{"mcpServers": {"api": {"url": "https://example.com/sse", "type": "sse", "headers": {"Authorization": "Bearer x"}}}}`,
			want:       map[string]ServerConfig{"api": {URL: "https://example.com/sse", Type: "sse", Headers: map[string]string{"Authorization": "Bearer x"}}},
			wantActive: []string{"api"},
		},
		{
			name:       "disabled entry is parsed but not active",
			json:       `{"mcpServers": {"old": {"command": "cmd", "disabled": true}}}`,
			want:       map[string]ServerConfig{"old": {Command: "cmd", Disabled: true}},
			wantActive: []string{},
		},
		{
			name:       "null args becomes a nil slice without error",
			json:       `{"mcpServers": {"x": {"command": "c", "args": null}}}`,
			want:       map[string]ServerConfig{"x": {Command: "c"}},
			wantActive: []string{"x"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.json)
			f, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile(%s) error: %v", path, err)
			}
			if len(f.EntryErrors) != 0 {
				t.Errorf("unexpected entry errors: %v", f.EntryErrors)
			}
			if !reflect.DeepEqual(f.MCPServers, tt.want) {
				t.Errorf("MCPServers = %#v, want %#v", f.MCPServers, tt.want)
			}
			// The active set is Discover's job now (File.Active was removed as
			// a dead API): same file, read through the agent layer.
			res, _ := Discover(Paths{AgentWorkspace: filepath.Dir(path)})
			if len(res.Names) != len(tt.wantActive) {
				t.Fatalf("active set = %v, want %v", res.Names, tt.wantActive)
			}
			for _, name := range tt.wantActive {
				if !slices.Contains(res.Names, name) {
					t.Errorf("active set %v missing %q", res.Names, name)
				}
			}
		})
	}
}

func TestParseFileMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no", "such", "mcp.json")
	f, err := ParseFile(missing)
	if f != nil {
		t.Errorf("File = %#v, want nil for missing path", f)
	}
	if err != nil {
		t.Errorf("error = %v, want nil (missing layers are optional)", err)
	}
}

func TestParseFileMalformedJSON(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"truncated object", `{"mcpServers": {"a": {"command": "c"`},
		{"not json at all", "definitely not json"},
		{"empty file", ""},
		{"wrong top-level type", `["not", "an", "object"]`},
		{"wrong field type", `{"mcpServers": {"a": {"command": 42}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.content)
			f, err := ParseFile(path)
			if err == nil {
				t.Fatalf("ParseFile(%q) = (%v, nil), want error", tt.content, f)
			}
			if f != nil {
				t.Errorf("File = %#v, want nil on malformed JSON", f)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not mention path %q", err, path)
			}
		})
	}
}

func TestParseFileInvalidEntriesDoNotKillFile(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		badServer   string
		errContains string
		wantActive  []string
	}{
		{
			name:        "mixed stdio and url is invalid",
			json:        `{"mcpServers": {"x": {"command": "npx", "url": "https://e.com"}}}`,
			badServer:   "x",
			errContains: "stdio and remote",
			wantActive:  nil,
		},
		{
			name:        "unknown remote type is invalid",
			json:        `{"mcpServers": {"x": {"url": "https://e.com", "type": "gRPC"}}}`,
			badServer:   "x",
			errContains: `unknown type "gRPC"`,
			wantActive:  nil,
		},
		{
			name:        "entry with neither command nor url is invalid",
			json:        `{"mcpServers": {"x": {}}}`,
			badServer:   "x",
			errContains: "command (stdio) or url (remote)",
			wantActive:  nil,
		},
		{
			name:        "one bad entry keeps the valid siblings",
			json:        `{"mcpServers": {"bad": {"url": "https://e.com", "type": "ftp"}, "good": {"command": "c"}}}`,
			badServer:   "bad",
			errContains: `unknown type "ftp"`,
			wantActive:  []string{"good"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.json)
			f, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile returned file-level error: %v", err)
			}
			raw, ok := f.EntryErrors[tt.badServer]
			if !ok {
				t.Fatalf("EntryErrors = %v, want an error for %q", keysOfErrors(f.EntryErrors), tt.badServer)
			}
			var srvErr *ServerError
			if !errors.As(raw, &srvErr) {
				t.Fatalf("entry error %T is not *ServerError", raw)
			}
			if srvErr.Server != tt.badServer {
				t.Errorf("ServerError.Server = %q, want %q", srvErr.Server, tt.badServer)
			}
			if !strings.Contains(raw.Error(), tt.errContains) {
				t.Errorf("error %q does not contain %q", raw, tt.errContains)
			}
			if _, stillParsed := f.MCPServers[tt.badServer]; !stillParsed {
				t.Errorf("invalid entry %q was dropped from MCPServers", tt.badServer)
			}
			// Active-set check through Discover (File.Active was removed as a
			// dead API): invalid entries must be excluded from the active set.
			res, _ := Discover(Paths{AgentWorkspace: filepath.Dir(path)})
			if !slices.Equal(res.Names, tt.wantActive) {
				t.Fatalf("active set = %v, want %v", res.Names, tt.wantActive)
			}
		})
	}
}

func TestParseFileExpansion(t *testing.T) {
	expand := staticExpand(map[string]string{
		"BIN":   "/usr/local/bin/mcp-server",
		"FLAG":  "--verbose",
		"URL":   "https://api.example.com/mcp",
		"TOKEN": "s3cr3t",
	})
	content := `{"mcpServers": {
		"stdio": {
			"command": "${BIN}",
			"args": ["${FLAG}", "run", "${UNSET}"],
			"env": {"API_TOKEN": "${TOKEN}"},
			"description": "server run by ${BIN}"
		},
		"remote": {
			"url": "${URL}/v1",
			"headers": {"Authorization": "Bearer ${TOKEN}"},
			"description": "money costs $$5"
		}
	}}`
	path := writeConfig(t, content)
	f, err := parseFile(path, expand)
	if err != nil {
		t.Fatalf("parseFile error: %v", err)
	}
	if len(f.EntryErrors) != 0 {
		t.Fatalf("unexpected entry errors: %v", f.EntryErrors)
	}

	stdio := f.MCPServers["stdio"]
	if stdio.Command != "/usr/local/bin/mcp-server" {
		t.Errorf("command = %q, want expanded ${BIN}", stdio.Command)
	}
	wantArgs := []string{"--verbose", "run", ""}
	if !reflect.DeepEqual(stdio.Args, wantArgs) {
		t.Errorf("args = %#v, want %#v (${UNSET} expands to empty)", stdio.Args, wantArgs)
	}
	if got := stdio.Env["API_TOKEN"]; got != "s3cr3t" {
		t.Errorf("env value = %q, want expanded ${TOKEN}", got)
	}
	if want := "server run by /usr/local/bin/mcp-server"; stdio.Description != want {
		t.Errorf("description = %q, want %q", stdio.Description, want)
	}

	remote := f.MCPServers["remote"]
	if remote.URL != "https://api.example.com/mcp/v1" {
		t.Errorf("url = %q, want ${URL} expanded with suffix", remote.URL)
	}
	if got := remote.Headers["Authorization"]; got != "Bearer s3cr3t" {
		t.Errorf("header value = %q, want expanded ${TOKEN}", got)
	}
	if remote.Description != "money costs $5" {
		t.Errorf("description = %q, want $$ to unescape to a literal $", remote.Description)
	}
}

func TestParseFileExpansionUsesProcessEnv(t *testing.T) {
	t.Setenv("LELE_MCP_TEST_CMD", "lele-mcp-cmd")
	t.Setenv("LELE_MCP_TEST_OTHER", "lele-mcp-other")
	path := writeConfig(t, `{"mcpServers": {"x": {"command": "${LELE_MCP_TEST_CMD}", "description": "${LELE_MCP_TEST_OTHER} ${LELE_MCP_NEVER_SET}"}}}`)

	f, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile error: %v", err)
	}
	srv := f.MCPServers["x"]
	if srv.Command != "lele-mcp-cmd" {
		t.Errorf("command = %q, want os.LookupEnv expansion of LELE_MCP_TEST_CMD", srv.Command)
	}
	if want := "lele-mcp-other "; srv.Description != want {
		t.Errorf("description = %q, want %q (unset var expands to empty)", srv.Description, want)
	}
}

func TestExpandString(t *testing.T) {
	expand := staticExpand(map[string]string{
		"A":      "alpha",
		"B":      "beta",
		"RECURS": "${A}",
	})
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple reference", "${A}", "alpha"},
		{"nested concatenation", "${A}${B}", "alphabeta"},
		{"mixed literal and concat", "x${A}-y${B}-z", "xalpha-ybeta-z"},
		{"unset variable expands to empty", "[${UNSET}]", "[]"},
		{"adjacent same variable twice", "${A}:${A}", "alpha:alpha"},
		{"empty variable name expands to empty", "${}", ""},
		{"dollar-dollar escapes to a literal dollar", "100$$", "100$"},
		{"dollar-dollar before a reference keeps it literal", "$${A}", "${A}"},
		{"lone dollar without brace passes through", "costs 5$ and $x", "costs 5$ and $x"},
		{"unterminated reference passes through", "${A stays", "${A stays"},
		{"backslash is not an escape character", `\${A}`, `\alpha`},
		{"values are not re-scanned (single pass)", "${RECURS}", "${A}"},
		{"plain text without dollars", "nothing to expand", "nothing to expand"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expand(tt.in); got != tt.want {
				t.Errorf("expand(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestDiscoverActiveFiltering: the active set excludes invalid and disabled
// entries — the semantics File.Active used to own, now asserted through the
// production path (Discover), which also records the disabled name.
func TestDiscoverActiveFiltering(t *testing.T) {
	content := `{"mcpServers": {
		"good": {"command": "c"},
		"off": {"command": "c", "disabled": true},
		"broken": {"url": "https://e.com", "type": "ftp"}
	}}`
	path := writeConfig(t, content)
	f, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile error: %v", err)
	}
	if len(f.MCPServers) != 3 {
		t.Fatalf("MCPServers has %d entries, want all 3 parsed", len(f.MCPServers))
	}

	res, _ := Discover(Paths{AgentWorkspace: filepath.Dir(path)})
	if !slices.Equal(res.Names, []string{"good"}) {
		t.Fatalf("active set = %v, want exactly [good] (invalid and disabled excluded)", res.Names)
	}
	if layer := res.Disabled["off"]; layer != LayerAgent {
		t.Errorf("Disabled[off] = %q, want the %q layer", layer, LayerAgent)
	}
	if _, broken := res.Servers["broken"]; broken {
		t.Error("invalid entry must not reach Servers")
	}
}

func keysOfErrors(m map[string]error) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
