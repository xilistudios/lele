package mcp

import (
	"os"
	"path/filepath"
	"reflect"
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

// T11: differential — the writer must never change the document's RUNTIME
// name set. For each document, SetDisabled runs the planned edit on disk,
// then the RESULT is loaded with ParseFile (the runtime's own loader, i.e.
// json.Unmarshal into File) and three invariants are asserted:
//   - resulting name set == input name set ∪ {created} \ {deleted}, with
//     created/deleted taken from ToggleResult (Removed/Created);
//   - every NON-targeted name keeps its exact effective value;
//   - the targeted name ends in the requested state (absent iff Removed).
//
// Golden bytes additionally pin that no envelope occurrence collapses into
// another: deleteMemberSpan used to be fed the cross-occurrence fold, so its
// neighbour lookup could splice across TWO duplicate envelopes and merge
// them (or fail outright) — the name set alone does not see a collapse.
func TestWriteEditDifferentialNameSetAndValues(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		target      string
		disable     bool
		wantRemoved bool
		wantCreated bool
		golden      string // exact expected bytes on disk ("" = skip)
	}{
		{
			// THE corner: the winning "a" lives in the exact envelope,
			// "b" in the case-variant one. Deleting the stub used to splice
			// from a's key to b's key ACROSS the envelope boundary,
			// relocating b under "mcpServers" and destroying the
			// "MCPServers" envelope.
			name:        "corner: stub enable across case-variant duplicate envelopes",
			in:          `{"mcpServers":{"a":{"disabled":true}},"MCPServers":{"b":{"command":"y"}}}`,
			target:      "a",
			wantRemoved: true,
			golden:      `{"mcpServers":{},"MCPServers":{"b":{"command":"y"}}}`,
		},
		{
			name:        "stub enable when a later exact envelope is empty",
			in:          `{"mcpServers":{"a":{"disabled":true}},"mcpServers":{}}`,
			target:      "a",
			wantRemoved: true,
			golden:      `{"mcpServers":{},"mcpServers":{}}`,
		},
		{
			name:        "create inside a case-variant-only envelope",
			in:          `{"MCPServers":{"c":{"command":"x"}}}`,
			target:      "d",
			disable:     true,
			wantCreated: true,
			golden:      `{"MCPServers":{"c":{"command":"x"}, "d": {"disabled":true}}}`,
		},
		{
			name:    "disable existing in a case-variant-only envelope",
			in:      `{"MCPServers":{"c":{"command":"x"}}}`,
			target:  "c",
			disable: true,
			golden:  `{"MCPServers":{"c":{"command":"x", "disabled": true}}}`,
		},
		{
			name:    "disable existing in the first of duplicate envelopes",
			in:      `{"mcpServers":{"a":{"command":"x"}},"mcpServers":{"b":{"command":"y"}}}`,
			target:  "a",
			disable: true,
			golden:  `{"mcpServers":{"a":{"command":"x", "disabled": true}},"mcpServers":{"b":{"command":"y"}}}`,
		},
		{
			name:        "create across duplicate envelopes",
			in:          `{"mcpServers":{"a":{"command":"x"}},"mcpServers":{"b":{"command":"y"}}}`,
			target:      "c",
			disable:     true,
			wantCreated: true,
			golden:      `{"mcpServers":{"a":{"command":"x"}},"mcpServers":{"b":{"command":"y"}, "c": {"disabled":true}}}`,
		},
		{
			name:        "materialise a case-variant null envelope in place",
			in:          `{"MCPServers":null}`,
			target:      "c",
			disable:     true,
			wantCreated: true,
			golden:      `{"MCPServers":{"c":{"disabled":true}}}`,
		},
		{
			name:        "stub enable when a later case-variant envelope is empty",
			in:          `{"mcpServers":{"a":{"disabled":true}},"MCPServers":{}}`,
			target:      "a",
			wantRemoved: true,
			golden:      `{"mcpServers":{},"MCPServers":{}}`,
		},
		{
			name:   "enable real entry winning in a later case-variant envelope",
			in:     `{"mcpServers":{"a":{"command":"x"}},"MCPServers":{"a":{"command":"z","disabled":true}}}`,
			target: "a",
			golden: `{"mcpServers":{"a":{"command":"x"}},"MCPServers":{"a":{"command":"z"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			writeRawFixture(t, path, tt.in)

			inFile, err := ParseFile(path) // the runtime's view of the INPUT
			if err != nil {
				t.Fatalf("ParseFile(input) error = %v", err)
			}

			res, err := SetDisabled(path, tt.target, tt.disable)
			if err != nil {
				t.Fatalf("SetDisabled(%q, disable=%v) error = %v", tt.target, tt.disable, err)
			}
			if res.Removed != tt.wantRemoved || res.Created != tt.wantCreated {
				t.Fatalf("result = %+v, want Removed=%v Created=%v", res, tt.wantRemoved, tt.wantCreated)
			}

			if got := readRawFixture(t, path); tt.golden != "" && got != tt.golden {
				t.Errorf("bytes after the edit:\n got: %s\nwant: %s", got, tt.golden)
			}

			outFile, err := ParseFile(path) // the runtime's view of the RESULT
			if err != nil {
				t.Fatalf("ParseFile(result) error = %v (the writer must never emit a document the runtime refuses)", err)
			}

			// Name set: input ∪ {created} \ {deleted}.
			wantNames := make(map[string]bool, len(inFile.MCPServers)+1)
			for n := range inFile.MCPServers {
				wantNames[n] = true
			}
			if res.Removed {
				delete(wantNames, tt.target)
			}
			if res.Created {
				wantNames[tt.target] = true
			}
			for n := range outFile.MCPServers {
				if !wantNames[n] {
					t.Errorf("name %q appeared in the result; want name set %v", n, wantNames)
				}
			}
			for n := range wantNames {
				if _, ok := outFile.MCPServers[n]; !ok {
					t.Errorf("name %q lost from the result; want name set %v", n, wantNames)
				}
			}

			// Per-name effective values: only the target may change.
			for n, srv := range outFile.MCPServers {
				if n == tt.target {
					continue
				}
				before, ok := inFile.MCPServers[n]
				if !ok {
					continue // reported by the name-set checks above
				}
				if !reflect.DeepEqual(before, srv) {
					t.Errorf("non-target %q changed: before %+v, after %+v", n, before, srv)
				}
			}

			// Target state: present with the requested flag, or absent iff
			// the entry was deleted (Removed).
			after, present := outFile.MCPServers[tt.target]
			switch {
			case res.Removed && present:
				t.Errorf("target %q reported Removed but is still in the result", tt.target)
			case !res.Removed && !present:
				t.Errorf("target %q missing from the result without Removed", tt.target)
			case present && after.Disabled != tt.disable:
				t.Errorf("target %q: Disabled = %v, want %v", tt.target, after.Disabled, tt.disable)
			}
		})
	}
}

// T12: accept/reject parity between the raw-editor gate and the runtime.
// ValidateRawJSON is what PUT /api/v1/mcp/{layer}/raw runs BEFORE writing a
// user payload; ParseFile is what Discover runs when loading the file. The
// two must agree on EVERY document: ValidateRawJSON errors iff ParseFile
// errors. Otherwise the endpoint answers 200 and persists bytes the runtime
// refuses to load (or rejects bytes it would happily run). Per-entry
// VALIDATION is deliberately out of scope: ParseFile never fails on it and
// ValidateRawJSON reports it as warnings.
func TestValidateRawJSONParityWithParseFile(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "case-variant envelope with mistyped entry", in: `{"MCPServers":{"x":{"command":123}}}`},
		{name: "exact envelope with mistyped entry", in: `{"mcpServers":{"x":{"command":123}}}`},
		{name: "case-variant entry not decodable", in: `{"MCPServers":{"x":5}}`},
		{name: "duplicate envelope later occurrence mistyped", in: `{"mcpServers":{"a":{"command":"x"}},"mcpServers":"str"}`},
		{name: "case-variant duplicate later occurrence mistyped", in: `{"mcpServers":{"a":{"command":"x"}},"MCPServers":{"b":"str"}}`},
		{name: "entry not decodable", in: `{"mcpServers":{"s":true}}`},
		{name: "servers not an object", in: `{"mcpServers":[]}`},
		{name: "top-level array", in: `[]`},
		{name: "incomplete envelope", in: `{`},
		{name: "trailing data", in: `{"mcpServers":{}}garbage`},
		{name: "duplicate envelope disjoint names", in: `{"mcpServers":{"a":{"command":"x"}},"mcpServers":{"b":{"command":"y"}}}`},
		{name: "null first", in: `{"mcpServers":null,"mcpServers":{"a":{"command":"x"}}}`},
		{name: "null second", in: `{"mcpServers":{"a":{"command":"x"}},"mcpServers":null}`},
		{name: "case-variant MCPServers", in: `{"MCPServers":{"c":{"command":"x"}}}`},
		{name: "case-variant mcpservers", in: `{"mcpservers":{"c":{"command":"x"}}}`},
		{name: "case-variant null", in: `{"mcpServers":{"a":{"command":"x"}},"MCPServers":null}`},
		{name: "null envelope document", in: `null`},
		{name: "empty envelope", in: `{"mcpServers":{}}`},
		{name: "null entry decodes as zero config", in: `{"mcpServers":{"a":null}}`},
		{name: "invalid entry warns but loads", in: `{"mcpServers":{"bad":{}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, verr := ValidateRawJSON([]byte(tt.in))

			path := filepath.Join(t.TempDir(), "mcp.json")
			writeRawFixture(t, path, tt.in)
			_, perr := ParseFile(path) // the runtime's own accept/reject

			if (verr != nil) != (perr != nil) {
				t.Errorf("verdict mismatch: ValidateRawJSON err = %v, ParseFile err = %v", verr, perr)
			}
		})
	}
}
