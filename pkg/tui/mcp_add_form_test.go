package tui

// Tests for the MCP-add wizard's pure layer (T2) and modal renderer (T3):
// the slot constants, the visible-slot sets, the per-slot parsers, the
// entry builder, the masking helper, the review warnings and
// renderMCPAddModal. Everything is called directly on a bare &Model{…} with
// the form state set by hand — no ModalMCPAdd registration, no key wiring
// (T5/T6), no write path (T4) — so this file compiles and passes on its
// own.
//
// The i18n keys referenced here are added to the locale packs by T1 (in
// parallel). Assertions therefore never pin the VALUE of a key: they compare
// against i18n.T(...) exactly the way production code does, so a missing key
// degrades to its fallback instead of failing, and the T7 completeness test
// is what pins the translations.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// mcpAddFormModel builds a Model whose generic form state looks like a
// mid-wizard MCP-add: formValues sized to mcpAddStepCount, the given slot
// values, the given current slot and no error. The textInput is the zero
// textinput.Model — renderMCPAddModal only reads Value()/renders View().
func mcpAddFormModel(stepIndex int, values map[int]string) *Model {
	m := &Model{}
	m.formValues = make([]string, mcpAddStepCount)
	for slot, v := range values {
		m.formValues[slot] = v
	}
	m.formStepIndex = stepIndex
	return m
}

// ── 1. slots & visible-slot sets ──────────────────────────────────────

func TestMCPAddStepCount(t *testing.T) {
	if mcpAddStepCount != 10 {
		t.Errorf("mcpAddStepCount = %d, want 10", mcpAddStepCount)
	}
	// The slot constants ARE the formValues indexes: a rename that reorders
	// them silently corrupts every parser's lookups.
	want := []int{
		mcpAddStepName, mcpAddStepKind, mcpAddStepEndpoint, mcpAddStepArgs,
		mcpAddStepType, mcpAddStepEnv, mcpAddStepHeaders,
		mcpAddStepDescription, mcpAddStepLayer, mcpAddStepReview,
	}
	for i, got := range want {
		if got != i {
			t.Errorf("slot constant #%d = %d, want %d (slots must be 0..9 in declaration order)", i, got, i)
		}
	}
}

// TestMCPAddVisibleSlots pins the three exact ordered slot sets of the plan:
// "" → [0 1], stdio → [0 1 2 3 5 7 8 9], remote → [0 1 2 4 5 6 7 8 9].
func TestMCPAddVisibleSlots(t *testing.T) {
	cases := []struct {
		kind string
		want []int
	}{
		{"", []int{mcpAddStepName, mcpAddStepKind}},
		{"stdio", []int{mcpAddStepName, mcpAddStepKind, mcpAddStepEndpoint,
			mcpAddStepArgs, mcpAddStepEnv, mcpAddStepDescription,
			mcpAddStepLayer, mcpAddStepReview}},
		{"remote", []int{mcpAddStepName, mcpAddStepKind, mcpAddStepEndpoint,
			mcpAddStepType, mcpAddStepEnv, mcpAddStepHeaders,
			mcpAddStepDescription, mcpAddStepLayer, mcpAddStepReview}},
		// Anything else falls back to the kind-not-chosen set.
		{"bogus", []int{mcpAddStepName, mcpAddStepKind}},
		{"STDIO", []int{mcpAddStepName, mcpAddStepKind}},
	}
	for _, tc := range cases {
		got := mcpAddVisibleSlots(tc.kind)
		if len(got) != len(tc.want) {
			t.Fatalf("mcpAddVisibleSlots(%q) = %v, want %v", tc.kind, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("mcpAddVisibleSlots(%q)[%d] = %d, want %d",
					tc.kind, i, got[i], tc.want[i])
			}
		}
	}
}

// ── 2. transport ──────────────────────────────────────────────────────

func TestMCPAddTransport(t *testing.T) {
	cases := []struct {
		name   string
		values map[int]string
		want   string
	}{
		{"unset", nil, ""},
		{"too short", map[int]string{}, ""},
		{"canonical stdio", map[int]string{mcpAddStepKind: "stdio"}, "stdio"},
		{"canonical remote", map[int]string{mcpAddStepKind: "remote"}, "remote"},
		{"shortcut not canonical", map[int]string{mcpAddStepKind: "s"}, ""},
		{"unparsed raw", map[int]string{mcpAddStepKind: "anything"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mcpAddFormModel(0, tc.values)
			if got := m.mcpAddTransport(); got != tc.want {
				t.Errorf("mcpAddTransport() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ── 3. name ───────────────────────────────────────────────────────────

func TestMCPAddValidateName(t *testing.T) {
	t.Run("positive", func(t *testing.T) {
		cases := []struct{ in, want string }{
			{"filesystem", "filesystem"},
			{"  padded  ", "padded"},
			{"a b", "a b"}, // spaces are legal: the name is a map key
		}
		for _, tc := range cases {
			got, err := mcpAddValidateName(tc.in)
			if err != nil {
				t.Errorf("mcpAddValidateName(%q) err = %v, want nil", tc.in, err)
				continue
			}
			if got != tc.want {
				t.Errorf("mcpAddValidateName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		cases := []struct{ in, wantErr string }{
			{"", i18n.T("tui.mcpAddErrNameRequired")},
			{"   ", i18n.T("tui.mcpAddErrNameRequired")},
			{"a/b", i18n.T("tui.mcpAddErrNameSlash")},
			{"trailing/", i18n.T("tui.mcpAddErrNameSlash")},
		}
		for _, tc := range cases {
			got, err := mcpAddValidateName(tc.in)
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("mcpAddValidateName(%q) err = %v, want %q", tc.in, err, tc.wantErr)
			}
			if got != "" {
				t.Errorf("mcpAddValidateName(%q) = %q, want \"\" on error", tc.in, got)
			}
		}
	})
}

// ── 4. transport parser ───────────────────────────────────────────────

func TestMCPAddParseTransport(t *testing.T) {
	t.Run("positive", func(t *testing.T) {
		cases := []struct{ in, want string }{
			{"stdio", "stdio"}, {"stdio ", "stdio"}, {"STDIO", "stdio"},
			{"s", "stdio"}, {"S", "stdio"}, {"1", "stdio"},
			{"remote", "remote"}, {"remote ", "remote"}, {"REMOTE", "remote"},
			{"r", "remote"}, {"R", "remote"}, {"2", "remote"},
		}
		for _, tc := range cases {
			got, err := mcpAddParseTransport(tc.in)
			if err != nil {
				t.Errorf("mcpAddParseTransport(%q) err = %v, want nil", tc.in, err)
				continue
			}
			if got != tc.want {
				t.Errorf("mcpAddParseTransport(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		for _, in := range []string{"", "  ", "stdio,remote", "http", "3", "none"} {
			got, err := mcpAddParseTransport(in)
			if err == nil || err.Error() != i18n.T("tui.mcpAddErrKind") {
				t.Errorf("mcpAddParseTransport(%q) err = %v, want %q", in, err, i18n.T("tui.mcpAddErrKind"))
			}
			if got != "" {
				t.Errorf("mcpAddParseTransport(%q) = %q, want \"\" on error", in, got)
			}
		}
	})
}

// ── 5. endpoint (command/url) ─────────────────────────────────────────

func TestMCPAddParseEndpoint(t *testing.T) {
	t.Run("positive", func(t *testing.T) {
		cases := []struct{ kind, in, want string }{
			{"stdio", "npx", "npx"},
			{"stdio", "  npx  ", "npx"},
			{"stdio", "node /opt/my server/index.js", "node /opt/my server/index.js"}, // spaces kept
			{"stdio", "npx ${PKG}", "npx ${PKG}"},                                     // ${VAR} never expanded
			{"remote", "https://example.com/mcp", "https://example.com/mcp"},
			{"remote", "  https://example.com  ", "https://example.com"},
			{"remote", "https://x.dev/${TOKEN}", "https://x.dev/${TOKEN}"},
		}
		for _, tc := range cases {
			got, err := mcpAddParseEndpoint(tc.kind, tc.in)
			if err != nil {
				t.Errorf("mcpAddParseEndpoint(%q,%q) err = %v, want nil", tc.kind, tc.in, err)
				continue
			}
			if got != tc.want {
				t.Errorf("mcpAddParseEndpoint(%q,%q) = %q, want %q", tc.kind, tc.in, got, tc.want)
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		cases := []struct{ kind, wantErr string }{
			{"stdio", i18n.T("tui.mcpAddErrCommand")},
			{"remote", i18n.T("tui.mcpAddErrURL")},
		}
		for _, tc := range cases {
			for _, in := range []string{"", "   "} {
				got, err := mcpAddParseEndpoint(tc.kind, in)
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("mcpAddParseEndpoint(%q,%q) err = %v, want %q", tc.kind, in, err, tc.wantErr)
				}
				if got != "" {
					t.Errorf("mcpAddParseEndpoint(%q,%q) = %q, want \"\" on error", tc.kind, in, got)
				}
			}
		}
	})
}

// ── 6. args ───────────────────────────────────────────────────────────

// TestMCPAddParseArgs pins the comma-only split: whitespace is NEVER a
// separator (paths/globs may contain spaces), empty input yields nil, empty
// segments are dropped, and the parser has no error case.
func TestMCPAddParseArgs(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"empty", "", nil},
		{"whitespace only", "   ", nil},
		{"single", "-y", []string{"-y"}},
		{"csv", "-y,@modelcontextprotocol/server-filesystem,/home/me/notes",
			[]string{"-y", "@modelcontextprotocol/server-filesystem", "/home/me/notes"}},
		{"padded csv", " -y , pkg , ", []string{"-y", "pkg"}},
		{"empty segments dropped", ",-y,,pkg,", []string{"-y", "pkg"}},
		// The whole reason for comma-only: an arg with a space must survive
		// as ONE element.
		{"spaces preserved", "--dir=/home/my user/notes",
			[]string{"--dir=/home/my user/notes"}},
		{"spaces preserved in csv", "-y,--dir=/home/my user/notes",
			[]string{"-y", "--dir=/home/my user/notes"}},
		{"literal token", "-y,${PKG}", []string{"-y", "${PKG}"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mcpAddParseArgs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("mcpAddParseArgs(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("mcpAddParseArgs(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// ── 7. type ───────────────────────────────────────────────────────────

func TestMCPAddParseType(t *testing.T) {
	t.Run("positive", func(t *testing.T) {
		cases := []struct{ in, want string }{
			{"", ""},           // untouched: the READER defaults, the writer omits
			{"  ", ""},         //
			{"http", "http"},   //
			{"HTTP", "http"},   // case-insensitive → lowercase
			{" Http ", "http"}, //
			{"sse", "sse"},     //
			{"SSE", "sse"},     //
			{"Sse", "sse"},     //
		}
		for _, tc := range cases {
			got, err := mcpAddParseType(tc.in)
			if err != nil {
				t.Errorf("mcpAddParseType(%q) err = %v, want nil", tc.in, err)
				continue
			}
			if got != tc.want {
				t.Errorf("mcpAddParseType(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		for _, in := range []string{"httpx", "sse2", "websocket", "default", "1"} {
			got, err := mcpAddParseType(in)
			if err == nil || err.Error() != i18n.T("tui.mcpAddErrType") {
				t.Errorf("mcpAddParseType(%q) err = %v, want %q", in, err, i18n.T("tui.mcpAddErrType"))
			}
			if got != "" {
				t.Errorf("mcpAddParseType(%q) = %q, want \"\" on error", in, got)
			}
		}
	})
}

// ── 8. pairs ──────────────────────────────────────────────────────────

func TestMCPAddParsePairs(t *testing.T) {
	t.Run("positive", func(t *testing.T) {
		cases := []struct {
			in   string
			want map[string]string
		}{
			{"", nil},
			{" , ", nil},
			{"A=1", map[string]string{"A": "1"}},
			{"A=1,B=2", map[string]string{"A": "1", "B": "2"}},
			{" A=1 , B=2 ", map[string]string{"A": "1", "B": "2"}},
			// Blind-typed pairs may carry padding around the key and value:
			// both halves are trimmed so the written env/header names match
			// what the process expects (review #2 MINOR 5).
			{" TOKEN = abc ", map[string]string{"TOKEN": "abc"}},
			{"A = 1,B=2", map[string]string{"A": "1", "B": "2"}},
			{"K1=v, K1 = w ", map[string]string{"K1": "w"}},
			{"KEY=", map[string]string{"KEY": ""}},   // empty value allowed
			{"A=b=c", map[string]string{"A": "b=c"}}, // split on the FIRST '='
			{"Authorization=Bearer ${TOKEN}", map[string]string{"Authorization": "Bearer ${TOKEN}"}},
			{"A=1,,B=2,", map[string]string{"A": "1", "B": "2"}}, // empty segments dropped
		}
		for _, tc := range cases {
			got, err := mcpAddParsePairs(tc.in)
			if err != nil {
				t.Errorf("mcpAddParsePairs(%q) err = %v, want nil", tc.in, err)
				continue
			}
			if len(got) != len(tc.want) {
				t.Fatalf("mcpAddParsePairs(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("mcpAddParsePairs(%q)[%q] = %q, want %q", tc.in, k, got[k], v)
				}
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		const secret = "s3cr3t-value"
		cases := []struct{ in, wantErr string }{
			// No '=' at all: the whole token is a malformed value whose key
			// cannot be identified, so it is never echoed.
			{"A=1,JUSTTEXT", i18n.T("tui.mcpAddErrPairNoEquals")},
			{secret, i18n.T("tui.mcpAddErrPairNoEquals")},
			// Empty key before '='.
			{"A=1,=v", i18n.T("tui.mcpAddErrPairNoKey")},
			{"=secret", i18n.T("tui.mcpAddErrPairNoKey")},
		}
		for _, tc := range cases {
			got, err := mcpAddParsePairs(tc.in)
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("mcpAddParsePairs(%q) err = %v, want %q", tc.in, err, tc.wantErr)
			}
			if got != nil {
				t.Errorf("mcpAddParsePairs(%q) = %v, want nil on error", tc.in, got)
			}
			// Secrecy: no error message may carry the text after '='.
			if err != nil && strings.Contains(err.Error(), secret) {
				t.Errorf("mcpAddParsePairs(%q) error %q leaks the value %q", tc.in, err, secret)
			}
		}
	})
}

// ── 9. masking ────────────────────────────────────────────────────────

// TestMCPMaskPairs pins the display mask: "A=s3cr3t,B=" → "A=•••••,B=",
// segments without '=' pass verbatim, and the value never survives.
func TestMCPMaskPairs(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"single pair", "A=s3cr3t", "A=•••••"},
		{"empty value kept", "A=s3cr3t,B=", "A=•••••,B="},
		{"value with equals", "Authorization=Bearer x=y", "Authorization=•••••"},
		{"bare segment verbatim", "novalue,A=1", "novalue,A=•••••"},
		{"whitespace preserved", " A=1 , B=2 ", " A=••••• , B=••••• "},
		{"${} value", "TOKEN=${TOKEN}", "TOKEN=•••••"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mcpMaskPairs(tc.in)
			if got != tc.want {
				t.Errorf("mcpMaskPairs(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, "s3cr3t") || strings.Contains(got, "Bearer") ||
				strings.Contains(got, "${TOKEN}") {
				t.Errorf("mcpMaskPairs(%q) = %q leaks a value", tc.in, got)
			}
		})
	}
}

// ── 10. layer ─────────────────────────────────────────────────────────

func TestMCPAddParseLayer(t *testing.T) {
	t.Run("positive", func(t *testing.T) {
		// Empty means the CLI default (global); the canonical names and the
		// localized /mcp filter labels are accepted case-insensitively.
		cases := []struct{ in, want string }{
			{"", mcp.LayerGlobal},
			{"  ", mcp.LayerGlobal},
			{"global", mcp.LayerGlobal},
			{"GLOBAL", mcp.LayerGlobal},
			{" Global ", mcp.LayerGlobal},
			{"agent", mcp.LayerAgent},
			{"AGENT", mcp.LayerAgent},
			{"project", mcp.LayerProject},
			{"PROJECT", mcp.LayerProject},
			{i18n.T("tui.mcpFilterGlobal"), mcp.LayerGlobal},
			{strings.ToUpper(i18n.T("tui.mcpFilterAgent")), mcp.LayerAgent},
			{i18n.T("tui.mcpFilterProject"), mcp.LayerProject},
			{i18n.T("tui.mcpFilterProject") + " ", mcp.LayerProject},
		}
		for _, tc := range cases {
			got, err := mcpAddParseLayer(tc.in)
			if err != nil {
				t.Errorf("mcpAddParseLayer(%q) err = %v, want nil", tc.in, err)
				continue
			}
			if got != tc.want {
				t.Errorf("mcpAddParseLayer(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})
	t.Run("negative", func(t *testing.T) {
		for _, in := range []string{"all", "Global!", "proj", "layers"} {
			got, err := mcpAddParseLayer(in)
			if err == nil || err.Error() != i18n.T("tui.mcpAddErrLayer") {
				t.Errorf("mcpAddParseLayer(%q) err = %v, want %q", in, err, i18n.T("tui.mcpAddErrLayer"))
			}
			if got != "" {
				t.Errorf("mcpAddParseLayer(%q) = %q, want \"\" on error", in, got)
			}
		}
	})
}

// TestMCPAddLayerRank pins the rank helper to mcp.Layers() itself: the
// merge order is the single source of truth and unknown names rank -1.
func TestMCPAddLayerRank(t *testing.T) {
	layers := mcp.Layers()
	for i, layer := range layers {
		if got := mcpAddLayerRank(layer); got != i {
			t.Errorf("mcpAddLayerRank(%q) = %d, want %d (index in mcp.Layers())", layer, got, i)
		}
	}
	if got := mcpAddLayerRank("bogus"); got != -1 {
		t.Errorf("mcpAddLayerRank(\"bogus\") = %d, want -1", got)
	}
	if got := mcpAddLayerRank(""); got != -1 {
		t.Errorf("mcpAddLayerRank(\"\") = %d, want -1", got)
	}
}

// ── 11. entry building ────────────────────────────────────────────────

// TestMCPAddEntry_StdioFieldIsolation: a stdio wizard run stores command,
// args and env — and NOTHING remote: URL empty, Type empty, Headers nil,
// Auth nil, Disabled false.
func TestMCPAddEntry_StdioFieldIsolation(t *testing.T) {
	m := mcpAddFormModel(mcpAddStepReview, map[int]string{
		mcpAddStepName:        "fs",
		mcpAddStepKind:        "stdio",
		mcpAddStepEndpoint:    "npx",
		mcpAddStepArgs:        "-y,@modelcontextprotocol/server-filesystem,/home/me/notes",
		mcpAddStepEnv:         "LOG_LEVEL=info,TOKEN=${TOKEN}",
		mcpAddStepType:        "sse",      // typed before switching kind: must be IGNORED
		mcpAddStepHeaders:     "X-Nope=1", // stdio run: must be IGNORED
		mcpAddStepDescription: "  read/write files  ",
		mcpAddStepLayer:       "project",
	})
	cfg, err := m.mcpAddEntry()
	if err != nil {
		t.Fatalf("mcpAddEntry() err = %v, want nil", err)
	}
	if cfg.Command != "npx" {
		t.Errorf("Command = %q, want %q", cfg.Command, "npx")
	}
	if want := []string{"-y", "@modelcontextprotocol/server-filesystem", "/home/me/notes"}; len(cfg.Args) != len(want) {
		t.Errorf("Args = %v, want %v", cfg.Args, want)
	} else {
		for i := range want {
			if cfg.Args[i] != want[i] {
				t.Errorf("Args[%d] = %q, want %q", i, cfg.Args[i], want[i])
			}
		}
	}
	if cfg.Env["LOG_LEVEL"] != "info" {
		t.Errorf("Env[LOG_LEVEL] = %q, want %q", cfg.Env["LOG_LEVEL"], "info")
	}
	// ${VAR} is stored literally: the WRITER never expands.
	if cfg.Env["TOKEN"] != "${TOKEN}" {
		t.Errorf("Env[TOKEN] = %q, want the literal ${TOKEN}", cfg.Env["TOKEN"])
	}
	// Remote-only fields stay at their zero values.
	if cfg.URL != "" {
		t.Errorf("URL = %q, want \"\" for a stdio entry", cfg.URL)
	}
	if cfg.Type != "" {
		t.Errorf("Type = %q, want \"\" for a stdio entry", cfg.Type)
	}
	if cfg.Headers != nil {
		t.Errorf("Headers = %v, want nil for a stdio entry", cfg.Headers)
	}
	if cfg.Auth != nil {
		t.Errorf("Auth = %v, want nil (OAuth is out of the wizard's scope)", cfg.Auth)
	}
	if cfg.Disabled {
		t.Error("Disabled = true, want false")
	}
	if cfg.Description != "read/write files" {
		t.Errorf("Description = %q, want the trimmed %q", cfg.Description, "read/write files")
	}
}

// TestMCPAddEntry_RemoteFieldIsolation: a remote wizard run stores url,
// type, headers and env — and NOTHING stdio: Command empty, Args nil.
func TestMCPAddEntry_RemoteFieldIsolation(t *testing.T) {
	m := mcpAddFormModel(mcpAddStepReview, map[int]string{
		mcpAddStepName:        "tracker",
		mcpAddStepKind:        "remote",
		mcpAddStepEndpoint:    "https://example.com/mcp",
		mcpAddStepType:        "SSE",
		mcpAddStepArgs:        "-y,pkg", // typed before switching kind: must be IGNORED
		mcpAddStepEnv:         "E=1",    // both kinds
		mcpAddStepHeaders:     "Authorization=Bearer ${TOKEN}",
		mcpAddStepDescription: "issue tracker",
		mcpAddStepLayer:       "global",
	})
	cfg, err := m.mcpAddEntry()
	if err != nil {
		t.Fatalf("mcpAddEntry() err = %v, want nil", err)
	}
	if cfg.URL != "https://example.com/mcp" {
		t.Errorf("URL = %q, want the typed URL", cfg.URL)
	}
	if cfg.Type != "sse" {
		t.Errorf("Type = %q, want the lowercased %q", cfg.Type, "sse")
	}
	if cfg.Headers["Authorization"] != "Bearer ${TOKEN}" {
		t.Errorf("Headers[Authorization] = %q, want the literal %q",
			cfg.Headers["Authorization"], "Bearer ${TOKEN}")
	}
	if cfg.Env["E"] != "1" {
		t.Errorf("Env[E] = %q, want %q", cfg.Env["E"], "1")
	}
	// Stdio-only fields stay at their zero values.
	if cfg.Command != "" {
		t.Errorf("Command = %q, want \"\" for a remote entry", cfg.Command)
	}
	if cfg.Args != nil {
		t.Errorf("Args = %v, want nil for a remote entry", cfg.Args)
	}
	if cfg.Disabled {
		t.Error("Disabled = true, want false")
	}

	t.Run("empty type stays empty", func(t *testing.T) {
		m2 := mcpAddFormModel(mcpAddStepReview, map[int]string{
			mcpAddStepKind:     "remote",
			mcpAddStepEndpoint: "https://example.com/mcp",
		})
		cfg2, err := m2.mcpAddEntry()
		if err != nil {
			t.Fatalf("mcpAddEntry() err = %v, want nil", err)
		}
		if cfg2.Type != "" {
			t.Errorf("Type = %q, want \"\" (never defaulted: the reader defaults)", cfg2.Type)
		}
	})
}

// TestMCPAddEntry_FirstErrorIsLocalized: whichever slot fails first, the
// entry is refused with THAT slot's message and a zero config.
func TestMCPAddEntry_FirstErrorIsLocalized(t *testing.T) {
	cases := []struct {
		name    string
		values  map[int]string
		wantErr string
	}{
		{"stdio without command", map[int]string{
			mcpAddStepKind: "stdio",
		}, i18n.T("tui.mcpAddErrCommand")},
		{"remote without url", map[int]string{
			mcpAddStepKind: "remote",
		}, i18n.T("tui.mcpAddErrURL")},
		{"bad type", map[int]string{
			mcpAddStepKind:     "remote",
			mcpAddStepEndpoint: "https://x.dev",
			mcpAddStepType:     "ftp",
		}, i18n.T("tui.mcpAddErrType")},
		{"env pair without '='", map[int]string{
			mcpAddStepKind:     "stdio",
			mcpAddStepEndpoint: "npx",
			mcpAddStepEnv:      "JUSTTEXT",
		}, i18n.T("tui.mcpAddErrPairNoEquals")},
		{"env pair without key", map[int]string{
			mcpAddStepKind:     "stdio",
			mcpAddStepEndpoint: "npx",
			mcpAddStepEnv:      "=v",
		}, i18n.T("tui.mcpAddErrPairNoKey")},
		{"headers pair without '='", map[int]string{
			mcpAddStepKind:     "remote",
			mcpAddStepEndpoint: "https://x.dev",
			mcpAddStepHeaders:  "JUSTTEXT",
		}, i18n.T("tui.mcpAddErrPairNoEquals")},
		// An unparsable kind never leaves the kind slot through the walk,
		// but mcpAddEntry is also called directly: it must REFUSE rather
		// than silently guess a transport (NIT review #2).
		{"unparsable kind is refused, never guessed", map[int]string{
			mcpAddStepKind: "bogus",
		}, i18n.T("tui.mcpAddErrKind")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mcpAddFormModel(mcpAddStepReview, tc.values)
			cfg, err := m.mcpAddEntry()
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("mcpAddEntry() err = %v, want %q", err, tc.wantErr)
			}
			if cfg.Command != "" || cfg.URL != "" || cfg.Env != nil || cfg.Headers != nil {
				t.Errorf("mcpAddEntry() = %+v, want the zero config on error", cfg)
			}
		})
	}
}

// ── 12. step names ────────────────────────────────────────────────────

// TestMCPAddStepNames checks the label list is exactly the visible slots'
// labels, in order, and localized (non-empty). Values are compared against
// i18n.T, never pinned: T1 may not have landed yet.
func TestMCPAddStepNames(t *testing.T) {
	cases := []struct {
		kind       string
		wantLabels []string
	}{
		{"", []string{i18n.T("tui.mcpAddStepName"), i18n.T("tui.mcpAddStepKind")}},
		{"stdio", []string{
			i18n.T("tui.mcpAddStepName"), i18n.T("tui.mcpAddStepKind"),
			i18n.T("tui.mcpAddStepCommand"), i18n.T("tui.mcpAddStepArgs"),
			i18n.T("tui.mcpAddStepEnv"), i18n.T("tui.mcpAddStepDescription"),
			i18n.T("tui.mcpAddStepLayer"), i18n.T("tui.mcpAddStepReview")}},
		{"remote", []string{
			i18n.T("tui.mcpAddStepName"), i18n.T("tui.mcpAddStepKind"),
			i18n.T("tui.mcpAddStepURL"), i18n.T("tui.mcpAddStepType"),
			i18n.T("tui.mcpAddStepEnv"), i18n.T("tui.mcpAddStepHeaders"),
			i18n.T("tui.mcpAddStepDescription"), i18n.T("tui.mcpAddStepLayer"),
			i18n.T("tui.mcpAddStepReview")}},
	}
	for _, tc := range cases {
		t.Run("kind="+tc.kind, func(t *testing.T) {
			m := mcpAddFormModel(0, map[int]string{mcpAddStepKind: tc.kind})
			got := m.mcpAddStepNames()
			// Length must match the visible slot set exactly: a label per
			// visible slot, no more, no fewer.
			if len(got) != len(mcpAddVisibleSlots(tc.kind)) {
				t.Fatalf("mcpAddStepNames() = %v (%d), want %d labels (one per visible slot)",
					got, len(got), len(mcpAddVisibleSlots(tc.kind)))
			}
			for i, want := range tc.wantLabels {
				if got[i] != want {
					t.Errorf("mcpAddStepNames()[%d] = %q, want %q", i, got[i], want)
				}
				if strings.TrimSpace(want) == "" {
					t.Errorf("label %d is empty — the locale key must resolve", i)
				}
			}
		})
	}
}

// ── 13. review warnings ───────────────────────────────────────────────

// mcpAddFormInventory builds an inventory snapshot the way loadMCPListWith
// stores it: one EntryState row per layer that holds a copy of name.
func mcpAddFormInventory(name string, copies ...string) mcp.Inventory {
	inv := mcp.Inventory{
		ByName:    make(map[string][]mcp.EntryState),
		Winner:    make(map[string]string),
		Effective: make(map[string]string),
	}
	for _, layer := range copies {
		inv.ByName[name] = append(inv.ByName[name], mcp.EntryState{
			Name: name, Layer: layer, Present: true, Defines: true,
		})
		inv.Winner[name] = layer // rows are low → high: last write wins
	}
	inv.Effective[name] = "enabled"
	return inv
}

// TestMCPAddReviewWarnings pins the four cases of the review advisory:
// no usable snapshot → silent; a copy in the TARGET layer → replace
// warning; a copy in a HIGHER layer → shadowed warning; a copy in a LOWER
// layer → "shadows" note. Ranking follows mcp.Layers().
func TestMCPAddReviewWarnings(t *testing.T) {
	build := func(kind, layer string, inv mcp.Inventory, valid bool) *Model {
		return &Model{
			formValues: func() []string {
				v := make([]string, mcpAddStepCount)
				v[mcpAddStepName] = "svc"
				v[mcpAddStepKind] = kind
				v[mcpAddStepLayer] = layer
				return v
			}(),
			formStepIndex:     mcpAddStepReview,
			mcpInventory:      inv,
			mcpInventoryValid: valid,
		}
	}

	t.Run("invalid snapshot is silent", func(t *testing.T) {
		m := build("stdio", mcp.LayerGlobal,
			mcpAddFormInventory("svc", mcp.LayerGlobal), false)
		if got := m.mcpAddReviewWarnings(); len(got) != 0 {
			t.Errorf("warnings = %v, want none with !mcpInventoryValid", got)
		}
	})
	t.Run("name not in any layer is silent", func(t *testing.T) {
		m := build("stdio", mcp.LayerGlobal,
			mcpAddFormInventory("other", mcp.LayerGlobal), true)
		if got := m.mcpAddReviewWarnings(); len(got) != 0 {
			t.Errorf("warnings = %v, want none for a new name", got)
		}
	})
	t.Run("copy in the target layer yields the replace warning", func(t *testing.T) {
		m := build("stdio", mcp.LayerGlobal,
			mcpAddFormInventory("svc", mcp.LayerGlobal), true)
		got := m.mcpAddReviewWarnings()
		want := fmt.Sprintf(i18n.T("tui.mcpAddReviewWarnReplace"), "svc", mcp.LayerGlobal)
		if len(got) != 1 || got[0] != want {
			t.Errorf("warnings = %v, want exactly [%q]", got, want)
		}
	})
	t.Run("copy in a higher layer yields the shadowed warning", func(t *testing.T) {
		// Target agent; a project copy ranks ABOVE it (mcp.Layers order).
		m := build("stdio", mcp.LayerAgent,
			mcpAddFormInventory("svc", mcp.LayerAgent, mcp.LayerProject), true)
		got := m.mcpAddReviewWarnings()
		wantShadowed := fmt.Sprintf(i18n.T("tui.mcpAddReviewWarnShadowed"), mcp.LayerProject)
		wantReplace := fmt.Sprintf(i18n.T("tui.mcpAddReviewWarnReplace"), "svc", mcp.LayerAgent)
		if len(got) != 2 || got[0] != wantReplace || got[1] != wantShadowed {
			t.Errorf("warnings = %v, want [%q %q]", got, wantReplace, wantShadowed)
		}
	})
	t.Run("copy in a lower layer yields the shadows note", func(t *testing.T) {
		m := build("stdio", mcp.LayerProject,
			mcpAddFormInventory("svc", mcp.LayerGlobal, mcp.LayerProject), true)
		got := m.mcpAddReviewWarnings()
		wantNote := fmt.Sprintf(i18n.T("tui.mcpAddReviewNoteShadows"), mcp.LayerGlobal)
		wantReplace := fmt.Sprintf(i18n.T("tui.mcpAddReviewWarnReplace"), "svc", mcp.LayerProject)
		// The inventory lists copies low → high, so the note (global, the
		// lower copy) comes first and the replace warning (the target layer)
		// second.
		if len(got) != 2 || got[0] != wantNote || got[1] != wantReplace {
			t.Errorf("warnings = %v, want [%q %q]", got, wantNote, wantReplace)
		}
	})
	t.Run("layer parsed from the localized filter label", func(t *testing.T) {
		m := build("stdio", i18n.T("tui.mcpFilterProject"),
			mcpAddFormInventory("svc", mcp.LayerGlobal), true)
		got := m.mcpAddReviewWarnings()
		wantNote := fmt.Sprintf(i18n.T("tui.mcpAddReviewNoteShadows"), mcp.LayerGlobal)
		if len(got) != 1 || got[0] != wantNote {
			t.Errorf("warnings = %v, want exactly [%q] (target resolved via the filter label)", got, wantNote)
		}
	})
}

// ── 14. renderer ──────────────────────────────────────────────────────

// mcpAddStripANSI removes ANSI sequences (SGR, box-drawing borders added by
// lipgloss containers, padding) so assertions see the visible text of a
// rendered frame.
func mcpAddStripANSI(s string) string {
	s = ansi.Strip(s)
	s = strings.ReplaceAll(s, "│", "")
	return s
}

// visibleLines returns the non-empty, trimmed lines of a rendered frame.
func visibleLines(t *testing.T, s string) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(mcpAddStripANSI(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// mcpAssertLabelsInOrder asserts that every want label appears, in order,
// exactly once as a "  ▶/✓/○ label" step line (or a review "label:" line),
// and that none of the forbidden labels appears anywhere.
// mcpAssertLabelsInOrderCount is mcpAssertLabelsInOrder with an explicit
// expected hit-count per label. The review step legitimately repeats each
// field (step line + review summary line), so it asserts exactly 2 hits.
func mcpAssertLabelsInOrderCount(t *testing.T, out string, wantHits int, want, forbidden []string) {
	t.Helper()
	lines := visibleLines(t, out)
	labelLines := func(label string) []string {
		var hits []string
		for _, l := range lines {
			if strings.HasPrefix(l, "✓ "+label) || strings.HasPrefix(l, "▶ "+label) ||
				strings.HasPrefix(l, "○ "+label) || strings.HasPrefix(l, label+":") {
				hits = append(hits, l)
			}
		}
		return hits
	}
	lastIdx := -1
	for _, label := range want {
		if strings.TrimSpace(label) == "" {
			t.Fatalf("want-label #%d is empty; the locale key must resolve (T1)", lastIdx+2)
		}
		hits := labelLines(label)
		if len(hits) != wantHits {
			t.Errorf("label %q appears in %d step lines, want %d: %v", label, len(hits), wantHits, hits)
			continue
		}
		idx := strings.Index(out, hits[0])
		if idx < lastIdx {
			t.Errorf("label %q appears out of order in:\n%s", label, out)
		}
		lastIdx = idx
	}
	for _, label := range forbidden {
		if strings.Contains(out, label) {
			t.Errorf("forbidden label %q appears in:\n%s", label, out)
		}
	}
}

func mcpAssertLabelsInOrder(t *testing.T, out string, want, forbidden []string) {
	t.Helper()
	lines := visibleLines(t, out)
	labelLines := func(label string) []string {
		var hits []string
		for _, l := range lines {
			// Step lines ("✓ label:", "▶ label:", "○ label") and review
			// lines ("label:") all start with the label right after the
			// glyph/indent. Using a line-prefix test keeps the assertion
			// independent of the styling.
			if strings.HasPrefix(l, "✓ "+label) || strings.HasPrefix(l, "▶ "+label) ||
				strings.HasPrefix(l, "○ "+label) || strings.HasPrefix(l, label+":") {
				hits = append(hits, l)
			}
		}
		return hits
	}
	lastIdx := -1
	for _, label := range want {
		if strings.TrimSpace(label) == "" {
			t.Fatalf("want-label #%d is empty; the locale key must resolve (T1)", lastIdx+2)
		}
		hits := labelLines(label)
		if len(hits) == 0 {
			t.Errorf("no step line for the %q label in:\n%s", label, out)
			continue
		}
		if len(hits) > 1 {
			t.Errorf("label %q appears in %d step lines, want 1: %v", label, len(hits), hits)
		}
		idx := strings.Index(out, hits[0])
		if idx <= lastIdx {
			t.Errorf("label %q (line %q) is out of order", label, hits[0])
		}
		lastIdx = idx
	}
	for _, label := range forbidden {
		for _, l := range lines {
			if strings.Contains(l, label) {
				t.Errorf("inapplicable label %q leaked into the output line %q:\n%s", label, l, out)
			}
		}
	}
}

// TestMCPAddRender_StdioStepsShowsOnlyApplicableLabels: at a mid-wizard
// stdio step the output carries the stdio labels in order and NONE of the
// remote-only ones.
func TestMCPAddRender_StdioStepsShowsOnlyApplicableLabels(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := mcpAddFormModel(mcpAddStepEnv, map[int]string{
		mcpAddStepName:     "fs",
		mcpAddStepKind:     "stdio",
		mcpAddStepEndpoint: "npx",
		mcpAddStepArgs:     "-y,pkg",
	})
	out := m.renderMCPAddModal()
	mcpAssertLabelsInOrder(t, out, []string{
		i18n.T("tui.mcpAddStepName"), i18n.T("tui.mcpAddStepKind"),
		i18n.T("tui.mcpAddStepCommand"), i18n.T("tui.mcpAddStepArgs"),
		i18n.T("tui.mcpAddStepEnv"), i18n.T("tui.mcpAddStepDescription"),
		i18n.T("tui.mcpAddStepLayer"), i18n.T("tui.mcpAddStepReview"),
	}, []string{
		i18n.T("tui.mcpAddStepURL"), i18n.T("tui.mcpAddStepType"),
		i18n.T("tui.mcpAddStepHeaders"),
	})
}

// TestMCPAddRender_RemoteStepsShowsOnlyApplicableLabels: the remote run
// shows type/headers and never args/command.
func TestMCPAddRender_RemoteStepsShowsOnlyApplicableLabels(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := mcpAddFormModel(mcpAddStepHeaders, map[int]string{
		mcpAddStepName:     "tracker",
		mcpAddStepKind:     "remote",
		mcpAddStepEndpoint: "https://example.com/mcp",
		mcpAddStepType:     "http",
	})
	out := m.renderMCPAddModal()
	mcpAssertLabelsInOrder(t, out, []string{
		i18n.T("tui.mcpAddStepName"), i18n.T("tui.mcpAddStepKind"),
		i18n.T("tui.mcpAddStepURL"), i18n.T("tui.mcpAddStepType"),
		i18n.T("tui.mcpAddStepEnv"), i18n.T("tui.mcpAddStepHeaders"),
		i18n.T("tui.mcpAddStepDescription"), i18n.T("tui.mcpAddStepLayer"),
		i18n.T("tui.mcpAddStepReview"),
	}, []string{
		i18n.T("tui.mcpAddStepCommand"), i18n.T("tui.mcpAddStepArgs"),
	})
}

// TestMCPAddRender_KindNotChosen: with no kind yet only name and kind are
// on screen.
func TestMCPAddRender_KindNotChosen(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := mcpAddFormModel(mcpAddStepKind, nil)
	out := m.renderMCPAddModal()
	mcpAssertLabelsInOrder(t, out, []string{
		i18n.T("tui.mcpAddStepName"), i18n.T("tui.mcpAddStepKind"),
	}, []string{
		i18n.T("tui.mcpAddStepEnv"), i18n.T("tui.mcpAddStepLayer"),
		i18n.T("tui.mcpAddStepReview"),
	})
}

// TestMCPAddRender_MasksEnvValuesEverywhere (the secrecy acceptance): with
// formValues[5] = "TOKEN=s3cr3t" completed, the rendering at ANY later slot
// and at the review slot contains the KEY and NEVER the value.
func TestMCPAddRender_MasksEnvValuesEverywhere(t *testing.T) {
	i18n.InitWithLanguage("en")
	const secret = "s3cr3t"
	base := map[int]string{
		mcpAddStepName:        "fs",
		mcpAddStepKind:        "stdio",
		mcpAddStepEndpoint:    "npx",
		mcpAddStepArgs:        "-y,pkg",
		mcpAddStepEnv:         "TOKEN=" + secret,
		mcpAddStepDescription: "files",
		mcpAddStepLayer:       "global",
	}

	// Later steps: description (7) and layer (8).
	for _, step := range []int{mcpAddStepDescription, mcpAddStepLayer} {
		t.Run(fmt.Sprintf("at step %d", step), func(t *testing.T) {
			m := mcpAddFormModel(step, base)
			out := mcpAddStripANSI(m.renderMCPAddModal())
			if !strings.Contains(out, "TOKEN") {
				t.Errorf("rendering misses the KEY:\n%s", out)
			}
			if strings.Contains(out, secret) {
				t.Errorf("rendering leaks the value %q:\n%s", secret, out)
			}
			// The completed env line shows the mask, not the value.
			if !strings.Contains(out, "TOKEN=•••••") {
				t.Errorf("rendering misses the masked pair:\n%s", out)
			}
		})
	}

	// The review block.
	t.Run("at review", func(t *testing.T) {
		m := mcpAddFormModel(mcpAddStepReview, base)
		out := mcpAddStripANSI(m.renderMCPAddModal())
		if !strings.Contains(out, "TOKEN") {
			t.Errorf("review misses the KEY:\n%s", out)
		}
		if strings.Contains(out, secret) {
			t.Errorf("review leaks the value %q:\n%s", secret, out)
		}
		if !strings.Contains(out, "TOKEN=•••••") {
			t.Errorf("review misses the masked pair:\n%s", out)
		}
	})

	// The headers step masks the IN-FLIGHT input too: the env/headers steps
	// use password echo (plan D9 flip, see isSecretInputStep), so the widget
	// shows bullets and only the KEY the user is typing reads back in the
	// step list's `▶` line via mcpMaskPairs. What this subtest pins is the
	// only PERSISTED surface: the completed-step summary, which must mask
	// through mcpMaskPairs even though the value is no longer on screen.
	t.Run("completed headers line masks the value", func(t *testing.T) {
		m := mcpAddFormModel(mcpAddStepDescription, map[int]string{
			mcpAddStepName:     "tracker",
			mcpAddStepKind:     "remote",
			mcpAddStepEndpoint: "https://example.com/mcp",
			mcpAddStepType:     "http",
			mcpAddStepEnv:      "TOKEN=" + secret,
			mcpAddStepHeaders:  "Authorization=Bearer " + secret,
		})
		out := mcpAddStripANSI(m.renderMCPAddModal())
		if strings.Contains(out, secret) {
			t.Errorf("completed headers line leaks the value %q:\n%s", secret, out)
		}
		if !strings.Contains(out, "Authorization=•••••") {
			t.Errorf("completed headers line is not masked:\n%s", out)
		}
	})
}

// TestMCPAddRender_ReviewBlock: the review step renders one line per
// applicable slot, the warnings from the snapshot, the auth hint and the
// review hints — and no input bar.
func TestMCPAddRender_ReviewBlock(t *testing.T) {
	i18n.InitWithLanguage("en")
	inv := mcpAddFormInventory("fs", mcp.LayerGlobal)
	m := &Model{
		formValues: func() []string {
			v := make([]string, mcpAddStepCount)
			v[mcpAddStepName] = "fs"
			v[mcpAddStepKind] = "stdio"
			v[mcpAddStepEndpoint] = "npx"
			v[mcpAddStepArgs] = "-y,pkg"
			v[mcpAddStepEnv] = "LOG=info"
			v[mcpAddStepDescription] = "files"
			v[mcpAddStepLayer] = "global"
			return v
		}(),
		formStepIndex:     mcpAddStepReview,
		mcpInventory:      inv,
		mcpInventoryValid: true,
	}
	out := mcpAddStripANSI(m.renderMCPAddModal())
	// The review step renders the applicable fields one more time as a
	// summary, so each label appears exactly TWICE (step line + review line).
	// The per-label helper would flag that as a duplicate, so relax it here.
	mcpAssertLabelsInOrderCount(t, out, 2, []string{
		i18n.T("tui.mcpAddStepName"), i18n.T("tui.mcpAddStepCommand"),
		i18n.T("tui.mcpAddStepArgs"), i18n.T("tui.mcpAddStepEnv"),
		i18n.T("tui.mcpAddStepDescription"), i18n.T("tui.mcpAddStepLayer"),
	}, nil)
	mcpAssertEachLabelTwice(t, out, []string{
		i18n.T("tui.mcpAddStepName"), i18n.T("tui.mcpAddStepCommand"),
		i18n.T("tui.mcpAddStepArgs"), i18n.T("tui.mcpAddStepEnv"),
		i18n.T("tui.mcpAddStepDescription"), i18n.T("tui.mcpAddStepLayer"),
	})

	// The warning from the snapshot (a copy already in the target layer).
	wantWarn := fmt.Sprintf(i18n.T("tui.mcpAddReviewWarnReplace"), "fs", mcp.LayerGlobal)
	if !strings.Contains(out, wantWarn) {
		t.Errorf("review misses the replace warning %q:\n%s", wantWarn, out)
	}
	// The OAuth-out-of-scope hint, verbatim from the locale.
	if !strings.Contains(out, i18n.T("tui.mcpAddReviewAuthHint")) {
		t.Errorf("review misses the auth hint %q:\n%s", i18n.T("tui.mcpAddReviewAuthHint"), out)
	}
	// The review-slot hints.
	if !strings.Contains(out, i18n.T("tui.mcpAddReviewHints")) {
		t.Errorf("review misses the hints %q:\n%s", i18n.T("tui.mcpAddReviewHints"), out)
	}
	// No live input on the review step.
	if strings.Contains(out, i18n.T("tui.mcpAddHints")) {
		t.Errorf("review shows the non-review hints %q:\n%s", i18n.T("tui.mcpAddHints"), out)
	}
}

// TestMCPAddRender_ShowsErrorAndHints: formError renders on its own line;
// a non-review step shows the input bar and the regular hints.
func TestMCPAddRender_ShowsErrorAndHints(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := mcpAddFormModel(mcpAddStepKind, map[int]string{
		mcpAddStepName: "fs",
	})
	m.formError = i18n.T("tui.mcpAddErrKind")
	out := mcpAddStripANSI(m.renderMCPAddModal())

	if !strings.Contains(out, "✗ "+i18n.T("tui.mcpAddErrKind")) {
		t.Errorf("formError missing from the output:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.mcpAddHints")) {
		t.Errorf("hints %q missing from a non-review step:\n%s", i18n.T("tui.mcpAddHints"), out)
	}
	if !strings.Contains(out, i18n.T("tui.mcpAdd")) {
		t.Errorf("title %q missing from the output:\n%s", i18n.T("tui.mcpAdd"), out)
	}
}

// TestMCPAddRender_EmptyValuesUseDetailNone: a completed optional slot with
// no value renders the localized "(none)" and the empty current input
// renders the "…" placeholder.
func TestMCPAddRender_EmptyValuesUseDetailNone(t *testing.T) {
	i18n.InitWithLanguage("en")
	// Current step = layer: args, env AND description are completed-and-empty.
	m := mcpAddFormModel(mcpAddStepLayer, map[int]string{
		mcpAddStepName:     "fs",
		mcpAddStepKind:     "stdio",
		mcpAddStepEndpoint: "npx",
		// args, env and description left empty on purpose
	})
	out := mcpAddStripANSI(m.renderMCPAddModal())

	none := i18n.T("tui.mcpDetailNone")
	// Both empty completed slots (args at pos 3, env at pos 4) render it.
	if got := strings.Count(out, "✓ "+i18n.T("tui.mcpAddStepArgs")+": "+none); got != 1 {
		t.Errorf("args line with %q appears %d times, want 1:\n%s", none, got, out)
	}
	if got := strings.Count(out, "✓ "+i18n.T("tui.mcpAddStepEnv")+": "+none); got != 1 {
		t.Errorf("env line with %q appears %d times, want 1:\n%s", none, got, out)
	}
	if got := strings.Count(out, "✓ "+i18n.T("tui.mcpAddStepDescription")+": "+none); got != 1 {
		t.Errorf("description line with %q appears %d times, want 1:\n%s", none, got, out)
	}
	// The current step's empty input renders the ellipsis placeholder.
	if !strings.Contains(out, "▶ "+i18n.T("tui.mcpAddStepLayer")+": […]") {
		t.Errorf("current step does not render the […] placeholder:\n%s", out)
	}
}

// TestMCPAddRender_ZeroValueModelRenders: the renderer must not panic on a
// bare &Model{} (no form state at all) — the renderModal contract a
// zero-value Model must satisfy.
func TestMCPAddRender_ZeroValueModelRenders(t *testing.T) {
	m := &Model{}
	if out := m.renderMCPAddModal(); out == "" {
		t.Error("renderMCPAddModal() on a zero-value Model returned \"\"")
	}
}

// mcpAssertEachLabelTwice asserts that each label appears in exactly two
// "label" lines: the completed step line and the review line. The step pane
// and the review block both list the applicable fields, so a label showing
// up once (missing review line) or three times (duplicated step/review
// block) is a regression.
func mcpAssertEachLabelTwice(t *testing.T, out string, labels []string) {
	t.Helper()
	lines := visibleLines(t, out)
	for _, label := range labels {
		n := 0
		for _, l := range lines {
			if strings.Contains(l, label+":") {
				n++
			}
		}
		if n != 2 {
			t.Errorf("label %q appears in %d lines, want 2 (step line + review line):\n%s", label, n, out)
		}
	}
}

// ── empty-state affordance (moved from the frozen pkg/tui/mcp_test.go) ──

// TestMCPEmptyStateRowAdvertisesAddHint: DoD 1's second half — the
// empty-state row itself must tell the user HOW to add the first server
// (the `N` key), not just that there are none. The row and the footer
// phrase it differently ("press N to add one" vs the footer's "N: add
// server"), so the assertion pins the ACTUAL shortcut letter that the
// footer hint carries — that is what a user presses — across every builtin
// locale (a fragment dropped from one file would otherwise stay invisible
// because i18n.T falls back to the default locale).
//
// This test and its two helpers belong to the wizard's DoD; they moved out
// of pkg/tui/mcp_test.go so that pre-existing test file stays byte-identical
// to HEAD. Keep them in a new *_test.go file, never in a frozen one.
func TestMCPEmptyStateRowAdvertisesAddHint(t *testing.T) {
	prev := i18n.GetLanguage()
	t.Cleanup(func() { i18n.SetLanguage(prev) })
	for _, lang := range []string{"en", "es", "pt"} {
		i18n.SetLanguage(lang)
		hint := i18n.T("tui.mcpAddHint")
		key, _, _ := strings.Cut(hint, ":") // "N: add server" → "N"
		if key == "" {
			t.Errorf("[%s] tui.mcpAddHint = %q carries no shortcut", lang, hint)
			continue
		}
		got := i18n.T("tui.mcpNoServers")
		// The empty-state row must reference the add key as a standalone
		// token (e.g. "press N", "pulsa N"), not merely contain the letter
		// somewhere — a naive substring check would pass on "No hay …"
		// because "No" contains "N".
		if !referencesKey(got, key) {
			t.Errorf("[%s] mcpNoServers = %q does not advertise the add key %q from the hint %q",
				lang, got, key, hint)
		}
	}
}

// referencesKey reports whether s mentions key as a standalone shortcut
// token (surrounded by non-alphanumeric characters), so that "press N to
// add" matches key "N" but "No hay servidores" does not.
func referencesKey(s, key string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], key)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(key)
		beforeOK := start == 0 || !isAlnumASCII(s[start-1])
		afterOK := end == len(s) || !isAlnumASCII(s[end])
		if beforeOK && afterOK {
			return true
		}
		i = start + 1
	}
}

func isAlnumASCII(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
