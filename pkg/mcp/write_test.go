package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T1: disable must touch ONLY the target entry — top-level member order,
// unknown keys and every other entry survive byte-for-byte (rule 5).
func TestSetDisabled_T1_DisablePreservesEveryOtherByte(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	in := `{"version":"1","custom":{"deep":[1,2]},"mcpServers":{"keep":{"command":"k","args":["--a"]},"target":{"command":"t"}},"other":true}`
	// Golden: the single difference from `in` is the flag inside the target.
	want := strings.Replace(in, `{"command":"t"}`, `{"command":"t", "disabled": true}`, 1)
	writeRawFixture(t, path, in)

	res, err := SetDisabled(path, "target", true)
	if err != nil {
		t.Fatalf("SetDisabled(disable) error = %v", err)
	}
	if res.Path != path || res.Name != "target" {
		t.Fatalf("result identity = %q/%q, want %q/%q", res.Path, res.Name, path, "target")
	}
	if !res.Changed || res.Created || res.Removed || res.Enabled {
		t.Fatalf("result = %+v, want Changed=true only", res)
	}
	if got := readRawFixture(t, path); got != want {
		t.Fatalf("file changed outside the target entry:\n got: %s\nwant: %s", got, want)
	}
}

// T2: enable DELETES the "disabled" key from a real entry (never writes
// "disabled": false — rule 2) and every other byte stays identical (rule 5).
func TestSetDisabled_T2_EnableDeletesKeyFromRealEntry(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "key last",
			in:   `{"version":3,"mcpServers":{"s":{"command":"x","env":{"K":"v"},"disabled":true}}}`,
			want: `{"version":3,"mcpServers":{"s":{"command":"x","env":{"K":"v"}}}}`,
		},
		{
			name: "key first",
			in:   `{"mcpServers":{"s":{"disabled":true,"command":"x"}}}`,
			want: `{"mcpServers":{"s":{"command":"x"}}}`,
		},
		{
			name: "key first spaced",
			in:   `{"mcpServers":{"s":{"disabled": true, "command": "x"}}}`,
			want: `{"mcpServers":{"s":{"command": "x"}}}`,
		},
		{
			name: "key last spaced",
			in:   `{"mcpServers":{"s":{"command":"x", "disabled": true}}}`,
			want: `{"mcpServers":{"s":{"command":"x"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			writeRawFixture(t, path, tt.in)

			res, err := SetDisabled(path, "s", false)
			if err != nil {
				t.Fatalf("SetDisabled(enable) error = %v", err)
			}
			if !res.Changed || !res.Enabled || res.Removed || res.Created {
				t.Fatalf("result = %+v, want Changed+Enabled only", res)
			}
			if got := readRawFixture(t, path); got != tt.want {
				t.Fatalf("enable must delete only the key:\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

// T3 (MANDATORY): enabling a pure stub in the agent layer DELETES the whole
// entry so the global layer wins again — the EVIDENCE B1 un-shadowing (rule 2).
func TestSetDisabled_T3_EnablePureStubRestoresLowerLayer(t *testing.T) {
	root := t.TempDir()
	leleDir := filepath.Join(root, "lele")
	agentDir := filepath.Join(root, "agent")
	globalPath := filepath.Join(leleDir, "mcp.json")
	agentPath := filepath.Join(agentDir, "mcp.json")
	writeRawFixture(t, globalPath, `{"mcpServers":{"s":{"command":"x"}}}`)
	writeRawFixture(t, agentPath, `{"mcpServers":{"s":{"disabled":true}}}`)

	res, err := SetDisabled(agentPath, "s", false)
	if err != nil {
		t.Fatalf("SetDisabled(enable) error = %v", err)
	}
	if !res.Removed || !res.Changed || !res.Enabled {
		t.Fatalf("result = %+v, want Removed+Changed+Enabled", res)
	}
	if got := readRawFixture(t, agentPath); got != `{"mcpServers":{}}` {
		t.Fatalf("agent file = %s, want entry deleted ({} mcpServers)", got)
	}

	got, err := Discover(Paths{LeleDir: leleDir, AgentWorkspace: agentDir})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	cfg, ok := got.Servers["s"]
	if !ok {
		t.Fatalf("s not active after un-shadowing: Disabled=%v Layers=%v", got.Disabled, got.Layers)
	}
	if cfg.Command != "x" {
		t.Fatalf("s.Command = %q, want the global entry's x", cfg.Command)
	}
	if got.Layers["s"] != LayerGlobal {
		t.Fatalf("s layer = %q, want %q", got.Layers["s"], LayerGlobal)
	}
}

// T4: disable on a missing file creates the stub document (parents and all)
// with mode 0600; enable on a missing file is a no-op — no file is created
// (rules 1, 3, 6).
func TestSetDisabled_T4_MissingFile(t *testing.T) {
	t.Run("disable creates stub with 0600", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "deep", "mcp.json")

		res, err := SetDisabled(path, "s", true)
		if err != nil {
			t.Fatalf("SetDisabled(disable) error = %v", err)
		}
		if !res.Created || !res.Changed || res.Removed || res.Enabled {
			t.Fatalf("result = %+v, want Created+Changed", res)
		}
		if got := readRawFixture(t, path); got != `{"mcpServers":{"s":{"disabled":true}}}` {
			t.Fatalf("created file = %s, want the disable stub", got)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := fi.Mode().Perm(); perm != 0600 {
			t.Fatalf("mode = %o, want 600", perm)
		}
	})

	t.Run("enable on missing file is a no-op", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")

		res, err := SetDisabled(path, "s", false)
		if err != nil {
			t.Fatalf("SetDisabled(enable) error = %v", err)
		}
		if res.Changed || res.Created || res.Removed {
			t.Fatalf("result = %+v, want a pure no-op", res)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("file exists after enable on missing file (stat err = %v)", err)
		}
	})
}

// T5: a missing "mcpServers" member (absent or null) is created while every
// other top-level member keeps its bytes and position; an existing
// mcpServers without the entry gets the stub appended (rules 1, 5).
func TestSetDisabled_T5_MissingMCPServers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "member absent",
			in:   `{"version":"1","note":"keep me"}`,
			want: `{"version":"1","note":"keep me", "mcpServers": {"s":{"disabled":true}}}`,
		},
		{
			name: "member null",
			in:   `{"top":1,"mcpServers":null}`,
			want: `{"top":1,"mcpServers":{"s":{"disabled":true}}}`,
		},
		{
			name: "entry absent from existing object",
			in:   `{"mcpServers":{"other":{"command":"o"}}}`,
			want: `{"mcpServers":{"other":{"command":"o"}, "s": {"disabled":true}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			writeRawFixture(t, path, tt.in)

			res, err := SetDisabled(path, "s", true)
			if err != nil {
				t.Fatalf("SetDisabled(disable) error = %v", err)
			}
			if !res.Changed || res.Removed || res.Enabled {
				t.Fatalf("result = %+v, want Changed", res)
			}
			if !res.Changed || !res.Created || res.Removed || res.Enabled {
				t.Fatalf("result = %+v, want Changed+Created", res)
			}
			if got := readRawFixture(t, path); got != tt.want {
				t.Fatalf("\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

// T6: SaveRawFile follows the atomic recipe — success leaves no *.tmp and
// mode 0600; a failing rename removes the temp and never touches the target
// (rule 6).
func TestSaveRawFile_T6_Atomicity(t *testing.T) {
	t.Run("success leaves no temp", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "mcp.json")
		content := []byte(`{"mcpServers":{"s":{"command":"x"}}}`)

		if err := SaveRawFile(path, content); err != nil {
			t.Fatalf("SaveRawFile() error = %v", err)
		}
		if got := readRawFixture(t, path); got != string(content) {
			t.Fatalf("file = %s, want %s", got, content)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := fi.Mode().Perm(); perm != 0600 {
			t.Fatalf("mode = %o, want 600", perm)
		}
		assertNoTmp(t, dir)
	})

	t.Run("rename failure removes temp and keeps target", func(t *testing.T) {
		dir := t.TempDir()
		// The target is a non-empty directory: writing the temp file
		// succeeds, os.Rename fails (EISDIR/ENOTEMPTY) after Sync+Close.
		path := filepath.Join(dir, "mcp.json")
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatalf("mkdir target: %v", err)
		}
		sentinel := filepath.Join(path, "sentinel")
		if err := os.WriteFile(sentinel, []byte("original"), 0644); err != nil {
			t.Fatalf("write sentinel: %v", err)
		}

		if err := SaveRawFile(path, []byte(`{"mcpServers":{}}`)); err == nil {
			t.Fatal("SaveRawFile() error = nil, want rename failure")
		}
		if got := readRawFixture(t, sentinel); got != "original" {
			t.Fatalf("target altered: sentinel = %s", got)
		}
		assertNoTmp(t, dir)
	})

	t.Run("read-only directory keeps original untouched", func(t *testing.T) {
		dir := t.TempDir()
		original := `{"mcpServers":{"s":{"command":"x"}}}`
		writeRawFixture(t, dir+"/mcp.json", original)
		if err := os.Chmod(dir, 0555); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		defer os.Chmod(dir, 0755) // let t.TempDir clean up

		if err := SaveRawFile(dir+"/mcp.json", []byte(`{"mcpServers":{}}`)); err == nil {
			t.Fatal("SaveRawFile() error = nil, want failure in a read-only dir")
		}
		if got := readRawFixture(t, dir+"/mcp.json"); got != original {
			t.Fatalf("original rewritten: %s", got)
		}
		assertNoTmp(t, dir)
	})
}

// T7: envelope problems are fatal (err), per-entry validation problems are
// warnings that name the entry — invalid entries are non-fatal by design
// (rule 9).
func TestValidateRawJSON_T7_EnvelopeVsWarnings(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantErr  bool
		wantWarn []string // entry names expected in the warnings
	}{
		{name: "incomplete envelope", in: `{`, wantErr: true},
		{name: "servers not an object", in: `{"mcpServers":[]}`, wantErr: true},
		{name: "top-level not an object", in: `[]`, wantErr: true},
		{name: "entry not decodable", in: `{"mcpServers":{"s":true}}`, wantErr: true},
		{name: "valid stdio", in: `{"mcpServers":{"s":{"command":"x"}}}`},
		{name: "valid remote", in: `{"mcpServers":{"r":{"url":"http://x"}}}`},
		{name: "empty servers", in: `{"mcpServers":{}}`},
		{name: "null document", in: `null`},
		{name: "missing command and url", in: `{"mcpServers":{"bad":{}}}`, wantWarn: []string{"bad"}},
		{name: "pure stub warns not fails", in: `{"mcpServers":{"s":{"disabled":true}}}`, wantWarn: []string{"s"}},
		{name: "mixes stdio and remote", in: `{"mcpServers":{"m":{"command":"x","url":"http://y"}}}`, wantWarn: []string{"m"}},
		{name: "unknown type", in: `{"mcpServers":{"u":{"url":"http://x","type":"ftp"}}}`, wantWarn: []string{"u"}},
		{name: "only broken entry warns", in: `{"mcpServers":{"ok":{"command":"x"},"bad":{}}}`, wantWarn: []string{"bad"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := ValidateRawJSON([]byte(tt.in))
			if tt.wantErr {
				if err == nil {
					t.Fatal("err = nil, want envelope error")
				}
				if warnings != nil {
					t.Fatalf("warnings = %v, want nil when err != nil", warnings)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil (per-entry problems are warnings)", err)
			}
			if len(warnings) != len(tt.wantWarn) {
				t.Fatalf("got %d warning(s) %v, want %v", len(warnings), warnings, tt.wantWarn)
			}
			for _, name := range tt.wantWarn {
				found := false
				for _, w := range warnings {
					if strings.Contains(w.Error(), `mcp server "`+name+`"`) {
						found = true
					}
				}
				if !found {
					t.Errorf("no warning names entry %q: %v", name, warnings)
				}
			}
		})
	}
}

// T8: path-metacharacter names must round-trip through disable→enable with
// ONLY that entry touched; empty and '/'-shaped names are rejected before
// anything is written (rules 5, 8).
func TestSetDisabled_T8_NameBattery(t *testing.T) {
	names := []string{"a.b", "a:b", "a*b", "a?b", "a#b", `a\b`, "ñ", "a b"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			base := `{"mcpServers":{"other":{"command":"o"},` + mustJSONKey(name) + `:{"command":"n"}}}`
			writeRawFixture(t, path, base)
			wantDisabled := strings.Replace(base, `{"command":"n"}`, `{"command":"n", "disabled": true}`, 1)

			res, err := SetDisabled(path, name, true)
			if err != nil {
				t.Fatalf("SetDisabled(disable, %q) error = %v", name, err)
			}
			if !res.Changed || res.Created || res.Removed {
				t.Fatalf("disable result = %+v, want Changed", res)
			}
			if got := readRawFixture(t, path); got != wantDisabled {
				t.Fatalf("disable touched more than the entry:\n got: %s\nwant: %s", got, wantDisabled)
			}

			res, err = SetDisabled(path, name, false)
			if err != nil {
				t.Fatalf("SetDisabled(enable, %q) error = %v", name, err)
			}
			if !res.Changed || !res.Enabled || res.Removed {
				t.Fatalf("enable result = %+v, want Changed+Enabled", res)
			}
			if got := readRawFixture(t, path); got != base {
				t.Fatalf("round-trip is not byte-exact:\n got: %s\nwant: %s", got, base)
			}
		})
	}

	rejected := []struct {
		name       string
		bad        string
		wantDetail string
	}{
		{name: "empty name", bad: "", wantDetail: "empty"},
		{name: "slash in name", bad: "a/b", wantDetail: "must not contain '/'"},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			base := `{"mcpServers":{"s":{"command":"x"}}}`
			writeRawFixture(t, path, base)

			res, err := SetDisabled(path, tt.bad, true)
			if err == nil {
				t.Fatalf("SetDisabled(%q) error = nil, want rejection", tt.bad)
			}
			if !strings.Contains(err.Error(), tt.wantDetail) {
				t.Fatalf("error = %q, want it to mention %q", err, tt.wantDetail)
			}
			if res.Changed || res.Created || res.Removed {
				t.Fatalf("result = %+v, want a pure rejection", res)
			}
			if got := readRawFixture(t, path); got != base {
				t.Fatalf("file modified by a rejected call: %s", got)
			}
		})
	}
}

// T9: an identical second call is a no-op — Changed=false and the bytes on
// disk are untouched, in both directions (rule 4).
func TestSetDisabled_T9_Idempotent(t *testing.T) {
	t.Run("second disable is a no-op", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")
		writeRawFixture(t, path, `{"mcpServers":{"s":{"command":"x"}}}`)

		if res, err := SetDisabled(path, "s", true); err != nil || !res.Changed {
			t.Fatalf("first disable: res = %+v, err = %v", res, err)
		}
		after := readRawFixture(t, path)

		res, err := SetDisabled(path, "s", true)
		if err != nil {
			t.Fatalf("second disable error = %v", err)
		}
		if res.Changed || res.Created {
			t.Fatalf("second disable result = %+v, want Changed=false", res)
		}
		if got := readRawFixture(t, path); got != after {
			t.Fatalf("bytes rewritten by a no-op:\n got: %s\nwant: %s", got, after)
		}
	})

	t.Run("second enable is a no-op", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")
		writeRawFixture(t, path, `{"mcpServers":{"s":{"command":"x","disabled":true}}}`)

		if res, err := SetDisabled(path, "s", false); err != nil || !res.Changed {
			t.Fatalf("first enable: res = %+v, err = %v", res, err)
		}
		after := readRawFixture(t, path)

		res, err := SetDisabled(path, "s", false)
		if err != nil {
			t.Fatalf("second enable error = %v", err)
		}
		if res.Changed || res.Removed {
			t.Fatalf("second enable result = %+v, want Changed=false", res)
		}
		if got := readRawFixture(t, path); got != after {
			t.Fatalf("bytes rewritten by a no-op:\n got: %s\nwant: %s", got, after)
		}
	})

	t.Run("enable of an absent entry is a no-op", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")
		base := `{"mcpServers":{"other":{"command":"o"}}}`
		writeRawFixture(t, path, base)

		res, err := SetDisabled(path, "missing", false)
		if err != nil {
			t.Fatalf("enable error = %v", err)
		}
		if res.Changed || res.Created || res.Removed {
			t.Fatalf("result = %+v, want a pure no-op", res)
		}
		if got := readRawFixture(t, path); got != base {
			t.Fatalf("file modified: %s", got)
		}
	})
}

// T10: with duplicate entry names the LAST occurrence wins (encoding/json
// map semantics) — it is the one mutated, the first is re-emitted verbatim
// (rules 4, 5).
func TestSetDisabled_T10_DuplicateKeyLastOccurrenceMutated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	in := `{"mcpServers":{"s":{"command":"first"},"s":{"command":"last"}}}`
	writeRawFixture(t, path, in)

	res, err := SetDisabled(path, "s", true)
	if err != nil {
		t.Fatalf("SetDisabled(disable) error = %v", err)
	}
	if !res.Changed || res.Created || res.Removed {
		t.Fatalf("result = %+v, want Changed", res)
	}
	want := `{"mcpServers":{"s":{"command":"first"},"s":{"command":"last", "disabled": true}}}`
	got := readRawFixture(t, path)
	if got != want {
		t.Fatalf("\n got: %s\nwant: %s", got, want)
	}
	if !strings.Contains(got, `"s":{"command":"first"}`) {
		t.Fatalf("first occurrence not verbatim: %s", got)
	}

	// Round-trip: enable removes the flag from the last occurrence only.
	res, err = SetDisabled(path, "s", false)
	if err != nil {
		t.Fatalf("SetDisabled(enable) error = %v", err)
	}
	if !res.Changed || res.Removed {
		t.Fatalf("enable result = %+v, want Changed and a kept real entry", res)
	}
	if got := readRawFixture(t, path); got != in {
		t.Fatalf("round-trip is not byte-exact:\n got: %s\nwant: %s", got, in)
	}
}

// --- helpers ---------------------------------------------------------------

func writeRawFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readRawFixture(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// assertNoTmp fails if any temp file survived a SaveRawFile call.
func assertNoTmp(t *testing.T, dir string) {
	t.Helper()
	leaked, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		t.Fatalf("glob *.tmp: %v", err)
	}
	if len(leaked) != 0 {
		t.Fatalf("temp files leaked: %v", leaked)
	}
}
