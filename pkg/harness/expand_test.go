// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tpl(s string) *Command {
	return &Command{Name: "t", Template: s, Source: SourceConfig}
}

func mustExpand(t *testing.T, c *Command, args string, opts ExpandOptions) string {
	t.Helper()
	out, err := Expand(c, args, opts)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	return out
}

func TestExpandNilCommand(t *testing.T) {
	if _, err := Expand(nil, "", ExpandOptions{}); err == nil {
		t.Fatal("nil command must error")
	}
}

func TestExpandArguments(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		args string
		want string
	}{
		{"verbatim", "echo $ARGUMENTS now", "hello   big  world", "echo hello   big  world now"},
		{"no args", "run $ARGUMENTS", "", "run "},
		{"positionals", "$1-$2-$3", "a b c", "a-b-c"},
		{"missing positional empty", "[$1][$3]", "a b", "[a][]"},
		{"quoted grouping", "[$1][$2]", `'hello world' "x y" plain`, "[hello world][x y]"},
		{"escaped double quote", "[$1]", `"say \"hi\""`, "[say \"hi\"]"},
		{"dollar inside quotes literal", "[$1]", `'$2'`, "[$2]"},
		{"dollar0 untouched", "$0 $1", "one", "$0 one"},
		{"dollar10 untouched", "cost $10 and $ARGUMENTS", "x", "cost $10 and x"},
		{"arguments not eaten by one", "$ARGUMENTS | $1", "full text", "full text | full"},
		{"no substitutions", "plain text", "ignored", "plain text"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mustExpand(t, tpl(tc.tmpl), tc.args, ExpandOptions{WorkDir: t.TempDir()})
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTokenizeArgs(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"a b  c", []string{"a", "b", "c"}},
		{"'single quoted'", []string{"single quoted"}},
		{`"double quoted" tail`, []string{"double quoted", "tail"}},
		{`"esc \"aped"`, []string{`esc "aped`}},
		{`"back\\slash"`, []string{`back\slash`}},
		{`"dollar $VAR stays"`, []string{"dollar $VAR stays"}},
		{`mix'ed'up`, []string{"mixedup"}},
		{"unterminated 'quote", []string{"unterminated", "quote"}}, // unterminated quote tolerated: opens a token that runs to EOF
		{"tab\tand\nnewline", []string{"tab", "and", "newline"}},
	}
	for _, tc := range tests {
		got := tokenizeArgs(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("tokenizeArgs(%q) = %q, want %q", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("tokenizeArgs(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestExpandShellDisabled(t *testing.T) {
	got := mustExpand(t, tpl("before !`echo hi` after"), "", ExpandOptions{WorkDir: t.TempDir(), AllowShell: false})
	if got != "before [shell disabled] after" {
		t.Errorf("got %q", got)
	}
}

func TestExpandShellEnabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c not available on windows")
	}
	dir := t.TempDir()
	got := mustExpand(t, tpl("out:!`echo hello`"), "", ExpandOptions{WorkDir: dir, AllowShell: true})
	if got != "out:hello" {
		t.Errorf("got %q, want %q", got, "out:hello")
	}

	// stderr is captured too and errors still inject partial output
	got = mustExpand(t, tpl("!`echo part; echo err >&2; exit 3`"), "", ExpandOptions{WorkDir: dir, AllowShell: true})
	if !strings.HasPrefix(got, "[error:") || !strings.Contains(got, "part") || !strings.Contains(got, "err") {
		t.Errorf("error case got %q", got)
	}

	// runs in WorkDir
	got = mustExpand(t, tpl("!`pwd`"), "", ExpandOptions{WorkDir: dir, AllowShell: true})
	if resolved, _ := filepath.EvalSymlinks(dir); !strings.HasSuffix(strings.TrimSpace(got), filepath.Base(resolved)) {
		t.Errorf("pwd %q not run in %q", got, dir)
	}
}

func TestExpandShellOutputCap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c not available on windows")
	}
	got := mustExpand(t, tpl("!`seq 1 2000`"), "", ExpandOptions{WorkDir: t.TempDir(), AllowShell: true, MaxShellOutput: 50})
	if !strings.HasSuffix(got, "\n...[truncated]") {
		t.Errorf("missing truncation marker: %q", got[len(got)-30:])
	}
	if len(got) > 50+len("\n...[truncated]") {
		t.Errorf("output longer than cap: %d", len(got))
	}
}

func TestExpandFileRefs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("  content here\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct{ tmpl, want string }{
		{"see @hello.txt ok", "see content here ok"},
		{"abs @" + filepath.Join(dir, "hello.txt"), "abs content here"},
		{"email a@b.com stays", "email a@b.com stays"},
		{"mention @hello.txt and @missing.txt", "mention content here and [missing: @missing.txt]"},
		{"glued email x@hello.txt stays", "glued email x@hello.txt stays"},
		{"path with :@hello.txt glued", "path with :@hello.txt glued"},
		{"trailing @ alone", "trailing @ alone"},
	}
	for _, tc := range tests {
		got := mustExpand(t, tpl(tc.tmpl), "", ExpandOptions{WorkDir: dir})
		if got != tc.want {
			t.Errorf("template %q:\n got %q\nwant %q", tc.tmpl, got, tc.want)
		}
	}
}

func TestExpandFileRefEscapeRejected(t *testing.T) {
	parent := t.TempDir()
	work := filepath.Join(parent, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret"), []byte("TOPSECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := mustExpand(t, tpl("@../secret"), "", ExpandOptions{WorkDir: work})
	if got != "[outside workspace: @../secret]" {
		t.Errorf("got %q, want escape placeholder", got)
	}
	// absolute paths are allowed when they exist
	got = mustExpand(t, tpl("@"+filepath.Join(parent, "secret")), "", ExpandOptions{WorkDir: work})
	if got != "TOPSECRET" {
		t.Errorf("abs got %q, want content", got)
	}
}

func TestExpandFileOutputCap(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 10*1024)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	got := mustExpand(t, tpl("@big.txt"), "", ExpandOptions{WorkDir: dir, MaxFileOutput: 100})
	if !strings.HasSuffix(got, "\n...[truncated]") {
		t.Fatalf("missing truncation marker in %q", got[len(got)-40:])
	}
	if len(got) != 100+len("\n...[truncated]") {
		t.Errorf("length = %d, want %d", len(got), 100+len("\n...[truncated]"))
	}
}

func TestExpandOrderSecurity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c not available on windows")
	}
	dir := t.TempDir()

	// args containing a shell placeholder must never execute: $ARGUMENTS is
	// substituted AFTER !`cmd` resolution, so the injected text is inert.
	got := mustExpand(t, tpl("$ARGUMENTS"), "!`echo pwned`", ExpandOptions{WorkDir: dir, AllowShell: false})
	if got != "!`echo pwned`" {
		t.Errorf("args placeholder text altered: %q", got)
	}
	// and even with shell enabled for the template, injected args are not rescanned
	got = mustExpand(t, tpl("$ARGUMENTS"), "!`touch pwned.flag`", ExpandOptions{WorkDir: dir, AllowShell: true})
	if _, err := os.Stat(filepath.Join(dir, "pwned.flag")); err == nil {
		t.Fatal("argument-injected shell ran")
	}

	// file content must not be rescanned for $N either
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("FILE_HAS_$1_LITERAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = mustExpand(t, tpl("@f.txt and $1"), "posA", ExpandOptions{WorkDir: dir})
	if got != "FILE_HAS_$1_LITERAL and posA" {
		t.Errorf("order violated: got %q", got)
	}
}

func TestExpandDefaults(t *testing.T) {
	// zero-value options: WorkDir falls back to cwd, caps to defaults
	got := mustExpand(t, tpl("plain"), "", ExpandOptions{})
	if got != "plain" {
		t.Errorf("got %q", got)
	}
	if runtime.GOOS == "windows" {
		t.Skip("sh -c not available on windows")
	}
	got = mustExpand(t, tpl("!`pwd`"), "", ExpandOptions{AllowShell: true})
	cwd, _ := os.Getwd()
	if strings.TrimSpace(got) != filepath.Base(cwd) && !filepath.IsAbs(got) {
		t.Errorf("pwd default workdir: %q", got)
	}
}
