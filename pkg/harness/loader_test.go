// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile creates a fixture file inside t.TempDir(), returning its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestLoadMarkdownFile(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		file    string
		content string
		wantErr bool
		wantCmd *Command // zero fields are compared too
	}{
		{
			name:    "full frontmatter",
			file:    "Test.MD",
			content: "---\ndescription: Run tests\nagent: coder\nmodel: openai/gpt-5\nallow_shell: true\n---\nDo the thing for $ARGUMENTS\n",
			wantCmd: &Command{
				Name:        "test",
				Description: "Run tests",
				Template:    "Do the thing for $ARGUMENTS",
				Agent:       "coder",
				Model:       "openai/gpt-5",
				AllowShell:  true,
				Source:      SourceGlobal,
			},
		},
		{
			name:    "no frontmatter whole file is template",
			file:    "plain.md",
			content: "just a template\nwith lines\n",
			wantCmd: &Command{
				Name:     "plain",
				Template: "just a template\nwith lines",
				Source:   SourceGlobal,
			},
		},
		{
			name:    "quoted values",
			file:    "quoted.md",
			content: "---\ndescription: \"hello: world\"\nagent: 'coder'\n---\nbody\n",
			wantCmd: &Command{
				Name:        "quoted",
				Description: "hello: world", // split on FIRST colon only
				Agent:       "coder",
				Template:    "body",
				Source:      SourceGlobal,
			},
		},
		{
			name:    "allow_shell false explicit",
			file:    "nosafe.md",
			content: "---\nallow_shell: false\n# a comment\nunknown_key: ignored\n---\nbody",
			wantCmd: &Command{
				Name:     "nosafe",
				Template: "body",
				Source:   SourceGlobal,
			},
		},
		{
			name:    "crlf frontmatter",
			file:    "crlf.md",
			content: "---\r\ndescription: win\r\n---\r\nbody line\r\n",
			wantCmd: &Command{
				Name:        "crlf",
				Description: "win",
				Template:    "body line",
				Source:      SourceGlobal,
			},
		},
		{
			name:    "frontmatter closing at EOF without trailing newline",
			file:    "eof.md",
			content: "---\ndescription: only fm\n---",
			wantCmd: &Command{
				Name:        "eof",
				Description: "only fm",
				Source:      SourceGlobal,
			},
		},
		{
			name:    "name derived from multi-dot stem",
			file:    "Foo.Bar.md",
			content: "x",
			wantCmd: &Command{Name: "foo.bar", Template: "x", Source: SourceGlobal},
		},
		{
			name:    "invalid allow_shell",
			file:    "bad.md",
			content: "---\nallow_shell: maybe\n---\nbody",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, dir, tc.file, tc.content)
			got, err := LoadMarkdownFile(path, SourceGlobal)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got command %+v", got)
				}
				if !strings.Contains(err.Error(), path) {
					t.Errorf("error should mention the file: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadMarkdownFile: %v", err)
			}
			tc.wantCmd.Path = path
			if *got != *tc.wantCmd {
				t.Errorf("got %+v, want %+v", *got, *tc.wantCmd)
			}
		})
	}
}

func TestLoadMarkdownFileMissing(t *testing.T) {
	if _, err := LoadMarkdownFile(filepath.Join(t.TempDir(), "nope.md"), SourceConfig); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "beta.md", "---\ndescription: b\n---\nbeta body")
	writeFile(t, dir, "Alpha.MARKDOWN", "alpha body")          // case-insensitive ext
	writeFile(t, dir, "notes.txt", "ignored")                  // wrong ext
	writeFile(t, dir, "bad.md", "---\nallow_shell: nope\n---") // invalid => skipped
	if err := os.MkdirAll(filepath.Join(dir, "sub.md"), 0o755); err != nil {
		t.Fatal(err)
	} // directory named *.md => skipped

	cmds, err := LoadDir(dir, SourceWorkspace)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	var names []string
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	got := strings.Join(names, ",")
	if got != "alpha,beta" { // sorted, invalid/dirs/non-md skipped
		t.Errorf("names = %q, want %q", got, "alpha,beta")
	}
	for _, c := range cmds {
		if c.Source != SourceWorkspace || filepath.Dir(c.Path) != dir {
			t.Errorf("bad tags on %v", c)
		}
	}
}

func TestLoadDirMissingIsNotError(t *testing.T) {
	cmds, err := LoadDir(filepath.Join(t.TempDir(), "does-not-exist"), SourceDirectory)
	if err != nil {
		t.Fatalf("missing dir should not error, got %v", err)
	}
	if cmds != nil {
		t.Fatalf("missing dir should return nil, got %v", cmds)
	}
}

func TestValidateName(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`} {
		if err := validateName(bad, "x.md"); err == nil {
			t.Errorf("validateName(%q) = nil, want error", bad)
		}
	}
	for _, ok := range []string{"a", "foo.bar", "-x", "ünïcode"} {
		if err := validateName(ok, "x.md"); err != nil {
			t.Errorf("validateName(%q) = %v, want nil", ok, err)
		}
	}
}

func TestParseFrontmatterUnknownAndComments(t *testing.T) {
	def, err := parseFrontmatter("# c\nno-colon-line\nfoo: bar\ndescription: d\n", "f.md")
	if err != nil {
		t.Fatal(err)
	}
	if def.Description != "d" || def.Agent != "" || def.Model != "" || def.AllowShell {
		t.Errorf("unexpected def %+v", def)
	}
}
