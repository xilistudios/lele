package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The tests below mirror write_test.go's rigour for the entry-level writer:
// every assertion is a CONTENT assertion (exact bytes / ParseFile round-trip)
// so each one fails when the feature is broken, and every rejected or no-op
// call is checked to have left the file byte-identical.

// filePerm returns the permission bits of an existing file.
func filePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}

// T-U1: inserting a NEW entry must not touch any pre-existing byte — odd
// whitespace, unknown keys, JSON escapes, tabs. The inserted member lands
// after the object's last member, so everything before the splice point is
// the original prefix and everything after it the original suffix.
func TestUpsertEntry_TU1_InsertPreservesEveryOtherByte(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	in := "{\n" +
		"\t\"version\": \"1\",\n" +
		"\t\"custom\": {\"deep\": [1, 2], \"nested\": {\"a\": null}},\n" +
		"\t\"mcpServers\": {\n" +
		"\t\t\"keep\": {\"command\": \"k\", \"args\": [\"--a\"]}\n" +
		"\t},\n" +
		"\t\"other\": \"\\u00e9\\u0026\"\n" +
		"}\n"
	writeRawFixture(t, path, in)

	res, err := UpsertEntry(path, "added", ServerConfig{Command: "a"})
	if err != nil {
		t.Fatalf("UpsertEntry() error = %v", err)
	}
	if !res.Created || !res.Changed || res.Replaced {
		t.Fatalf("result = %+v, want Created+Changed only", res)
	}
	if res.Path != path || res.Name != "added" {
		t.Fatalf("result identity = %q/%q, want %q/%q", res.Path, res.Name, path, "added")
	}

	anchor := `{"command": "k", "args": ["--a"]}`
	want := strings.Replace(in, anchor, anchor+`, "added": {"command":"a"}`, 1)
	got := readRawFixture(t, path)
	if got != want {
		t.Fatalf("inserted entry is not what was expected:\n got: %s\nwant: %s", got, want)
	}
	// Explicit prefix/suffix proof (T1 style): the original bytes survive on
	// both sides of the splice.
	i := strings.Index(in, anchor) + len(anchor)
	if !strings.HasPrefix(got, in[:i]) || !strings.HasSuffix(got, in[i:]) {
		t.Fatalf("bytes outside the splice changed:\n got: %q\nwant prefix %q + suffix %q", got, in[:i], in[i:])
	}
	// The reader must load the result and see both names.
	f, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile(result) error = %v", err)
	}
	if len(f.EntryErrors) != 0 {
		t.Fatalf("EntryErrors = %v, want none", f.EntryErrors)
	}
	if got := f.MCPServers["added"].Command; got != "a" {
		t.Fatalf("added.Command = %q, want a", got)
	}
	if got := f.MCPServers["keep"].Command; got != "k" {
		t.Fatalf("keep.Command = %q, want k", got)
	}
}

// T-U2: replacing an existing entry rewrites ONLY that member's value bytes
// (the key and the user's own spacing stay), reports Replaced, and a second
// call with the same entry is a pure no-op.
func TestUpsertEntry_TU2_ReplaceTouchesOnlyThatMember(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	in := `{"mcpServers":{"keep":{"command":"k"},"target":  {"command":"t","args":["1"]}  },"other":true}`
	writeRawFixture(t, path, in)

	res, err := UpsertEntry(path, "target", ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}})
	if err != nil {
		t.Fatalf("UpsertEntry() error = %v", err)
	}
	if res.Created || !res.Replaced || !res.Changed {
		t.Fatalf("result = %+v, want Replaced+Changed only", res)
	}
	want := strings.Replace(in, `{"command":"t","args":["1"]}`, `{"command":"npx","args":["-y","pkg"]}`, 1)
	if got := readRawFixture(t, path); got != want {
		t.Fatalf("replace changed more than the member's value:\n got: %s\nwant: %s", got, want)
	}

	f, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile(result) error = %v", err)
	}
	if want := (ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}); !reflect.DeepEqual(f.MCPServers["target"], want) {
		t.Fatalf("target = %+v, want %+v", f.MCPServers["target"], want)
	}
	if got := f.MCPServers["keep"].Command; got != "k" {
		t.Fatalf("keep.Command = %q, want k (a non-target entry changed)", got)
	}

	after := readRawFixture(t, path)
	res, err = UpsertEntry(path, "target", ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}})
	if err != nil {
		t.Fatalf("second UpsertEntry() error = %v", err)
	}
	if res.Created || res.Replaced || res.Changed {
		t.Fatalf("idempotent replace result = %+v, want a pure no-op", res)
	}
	if got := readRawFixture(t, path); got != after {
		t.Fatalf("bytes rewritten by a no-op:\n got: %s\nwant: %s", got, after)
	}
}

// T-U3: file and/or envelope creation. Every shape SetDisabled can create
// must be created byte-identically here, and the result must always be a
// document ParseFile loads with the new name present.
func TestUpsertEntry_TU3_CreatesFileAndEnvelope(t *testing.T) {
	tests := []struct {
		name   string
		in     string // "" = the file does not exist
		want   string
		reason string
	}{
		{
			name:   "missing file",
			in:     "",
			want:   `{"mcpServers":{"s":{"command":"x"}}}`,
			reason: "fresh one-entry document (singleEntryDocument)",
		},
		{
			name:   "envelope member absent",
			in:     `{"version":"1","note":"keep me"}`,
			want:   `{"version":"1","note":"keep me", "mcpServers": {"s":{"command":"x"}}}`,
			reason: "appended after the last top-level member",
		},
		{
			name:   "envelope member null",
			in:     `{"top":1,"mcpServers":null}`,
			want:   `{"top":1,"mcpServers":{"s":{"command":"x"}}}`,
			reason: "materialised in place, neighbours keep their bytes",
		},
		{
			name:   "empty top-level object",
			in:     `{}`,
			want:   `{"mcpServers": {"s":{"command":"x"}}}`,
			reason: "member inserted before the closing brace",
		},
		{
			name:   "empty top-level object with whitespace",
			in:     "{ }\n",
			want:   "{ \"mcpServers\": {\"s\":{\"command\":\"x\"}}}\n",
			reason: "same, whitespace preserved",
		},
		{
			name:   "top-level null",
			in:     `null`,
			want:   `{"mcpServers":{"s":{"command":"x"}}}`,
			reason: "a null document has nothing to preserve",
		},
		{
			name:   "empty mcpServers object",
			in:     `{"mcpServers":{}}`,
			want:   `{"mcpServers":{"s": {"command":"x"}}}`,
			reason: "entry inserted before the object's closing brace",
		},
		{
			name:   "entry absent from a populated object",
			in:     `{"mcpServers":{"other":{"command":"o"}}}`,
			want:   `{"mcpServers":{"other":{"command":"o"}, "s": {"command":"x"}}}`,
			reason: "appended after the last member of the object",
		},
		{
			// The fold unions both occurrences, so its last member lives in
			// the FIRST envelope while the last occurrence is the empty one:
			// the entry goes next to the fold's last member (planAppendEntry),
			// exactly like SetDisabled's stub — both names stay visible.
			name:   "duplicate envelopes, last one empty",
			in:     `{"mcpServers":{"other":{"command":"o"}},"mcpServers":{}}`,
			want:   `{"mcpServers":{"other":{"command":"o"}, "s": {"command":"x"}},"mcpServers":{}}`,
			reason: "appended after the fold's last member, never across an envelope boundary",
		},
		{
			name:   "case-variant envelope only",
			in:     `{"MCPServers":{"c":{"command":"c"}}}`,
			want:   `{"MCPServers":{"c":{"command":"c"}, "s": {"command":"x"}}}`,
			reason: "the envelope encoding/json honours is the one extended",
		},
		{
			name: "winning null envelope after a real one",
			in:   `{"mcpServers":{"a":{"command":"x"}},"mcpServers":null}`,
			want: `{"mcpServers":{"a":{"command":"x"}},"mcpServers":{"s":{"command":"x"}}}`,
			reason: "the winning null is materialised in place (SetDisabled's rule): " +
				"the earlier occurrence's names become visible again, which is why a null occurrence means 'empty'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "nested", "deep", "mcp.json")
			if tt.in != "" {
				writeRawFixture(t, path, tt.in)
			}

			res, err := UpsertEntry(path, "s", ServerConfig{Command: "x"})
			if err != nil {
				t.Fatalf("UpsertEntry() error = %v", err)
			}
			if !res.Created || !res.Changed || res.Replaced {
				t.Fatalf("result = %+v, want Created+Changed only", res)
			}
			if got := readRawFixture(t, path); got != tt.want {
				t.Fatalf("%s\n got: %s\nwant: %s", tt.reason, got, tt.want)
			}
			if perm := filePerm(t, path); perm != 0600 {
				t.Fatalf("mode = %o, want 600", perm)
			}
			f, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile(result) error = %v", err)
			}
			if got := f.MCPServers["s"].Command; got != "x" {
				t.Fatalf("s.Command = %q, want x", got)
			}
		})
	}
}

// T-U4: idempotence and deterministic serialization. The canonical form is
// pinned byte-for-byte (fixed field order, sorted env keys, compact) and a
// second identical call must not rewrite the file.
func TestUpsertEntry_TU4_IdempotentAndCanonical(t *testing.T) {
	entry := ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "pkg"},
		Env:     map[string]string{"C": "3", "A": "1", "B": "2"},
	}
	const want = `{"mcpServers":{"s":{"command":"npx","args":["-y","pkg"],"env":{"A":"1","B":"2","C":"3"}}}}`

	t.Run("first upsert writes the canonical bytes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")
		res, err := UpsertEntry(path, "s", entry)
		if err != nil {
			t.Fatalf("UpsertEntry() error = %v", err)
		}
		if !res.Created || !res.Changed {
			t.Fatalf("result = %+v, want Created+Changed", res)
		}
		if got := readRawFixture(t, path); got != want {
			t.Fatalf("canonical bytes:\n got: %s\nwant: %s", got, want)
		}
	})

	t.Run("identical upsert is a no-op", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")
		writeRawFixture(t, path, want)

		res, err := UpsertEntry(path, "s", entry)
		if err != nil {
			t.Fatalf("UpsertEntry() error = %v", err)
		}
		if res.Created || res.Replaced || res.Changed {
			t.Fatalf("result = %+v, want a pure no-op", res)
		}
		if got := readRawFixture(t, path); got != want {
			t.Fatalf("bytes rewritten by a no-op:\n got: %s\nwant: %s", got, want)
		}
	})

	t.Run("serialization is deterministic across runs", func(t *testing.T) {
		// Map iteration order is random: two independent writes of the same
		// entry must still produce identical bytes.
		first := filepath.Join(t.TempDir(), "a", "mcp.json")
		second := filepath.Join(t.TempDir(), "b", "mcp.json")
		for _, p := range []string{first, second} {
			if _, err := UpsertEntry(p, "s", entry); err != nil {
				t.Fatalf("UpsertEntry(%s) error = %v", p, err)
			}
			if got := readRawFixture(t, p); got != want {
				t.Fatalf("non-canonical bytes for %s:\n got: %s\nwant: %s", p, got, want)
			}
		}
	})
}

// T-U5: name battery. Names that need JSON escaping must round-trip through
// the file and back into ParseFile's name set; empty and '/'-shaped names are
// refused before anything is written (rule 1).
func TestUpsertEntry_TU5_NameBattery(t *testing.T) {
	accepted := []string{"a.b", "a:b", "a*b", "a?b", "a#b", "a&b", "ñ", "a b", `a\b`, `x"y`, "a<b>"}
	for _, name := range accepted {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			in := `{"mcpServers":{"other":{"command":"o"}}}`
			writeRawFixture(t, path, in)

			res, err := UpsertEntry(path, name, ServerConfig{Command: "n"})
			if err != nil {
				t.Fatalf("UpsertEntry(%q) error = %v", name, err)
			}
			if !res.Created || !res.Changed || res.Replaced {
				t.Fatalf("result = %+v, want Created+Changed", res)
			}
			want := `{"mcpServers":{"other":{"command":"o"}, ` + mustJSONKey(name) + `: {"command":"n"}}}`
			if got := readRawFixture(t, path); got != want {
				t.Fatalf("wrong bytes:\n got: %s\nwant: %s", got, want)
			}
			f, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile(result) error = %v", err)
			}
			if _, ok := f.MCPServers[name]; !ok {
				t.Fatalf("name %q not in the result: %v", name, f.MCPServers)
			}
			if got := f.MCPServers[name].Command; got != "n" {
				t.Fatalf("%q.Command = %q, want n", name, got)
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

			res, err := UpsertEntry(path, tt.bad, ServerConfig{Command: "n"})
			if err == nil {
				t.Fatalf("UpsertEntry(%q) error = nil, want rejection", tt.bad)
			}
			if !strings.Contains(err.Error(), tt.wantDetail) {
				t.Fatalf("error = %q, want it to mention %q", err, tt.wantDetail)
			}
			if res.Created || res.Replaced || res.Changed {
				t.Fatalf("result = %+v, want a pure rejection", res)
			}
			if got := readRawFixture(t, path); got != base {
				t.Fatalf("file modified by a rejected call: %s", got)
			}
		})
	}
}

// T-U6: invalid entries are refused (the reader's own rule: command XOR url,
// type in {"", "http", "sse"}) and the file is left untouched (rule 2).
func TestUpsertEntry_TU6_InvalidEntryBattery(t *testing.T) {
	tests := []struct {
		name       string
		entry      ServerConfig
		wantDetail string
	}{
		{
			name:       "command and url",
			entry:      ServerConfig{Command: "x", URL: "http://y"},
			wantDetail: "mixes stdio and remote",
		},
		{
			name:       "neither command nor url",
			entry:      ServerConfig{},
			wantDetail: "needs command",
		},
		{
			name:       "type only",
			entry:      ServerConfig{Type: "sse"},
			wantDetail: "needs command",
		},
		{
			name:       "unknown type",
			entry:      ServerConfig{URL: "http://y", Type: "ftp"},
			wantDetail: `unknown type "ftp"`,
		},
		{
			name:       "empty url spelled out",
			entry:      ServerConfig{Command: "", URL: "", Args: []string{"-y"}},
			wantDetail: "needs command",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			base := `{"mcpServers":{"s":{"command":"x"}}}`
			writeRawFixture(t, path, base)

			res, err := UpsertEntry(path, "bad", tt.entry)
			if err == nil {
				t.Fatal("UpsertEntry() error = nil, want a rejection")
			}
			if !strings.Contains(err.Error(), tt.wantDetail) {
				t.Fatalf("error = %q, want it to mention %q", err, tt.wantDetail)
			}
			if !strings.Contains(err.Error(), `"bad"`) {
				t.Fatalf("error = %q, want it to name the entry", err)
			}
			if res.Created || res.Replaced || res.Changed {
				t.Fatalf("result = %+v, want a pure rejection", res)
			}
			if got := readRawFixture(t, path); got != base {
				t.Fatalf("file modified by a rejected call: %s", got)
			}
		})
	}
}

// T-U7: round-trip. Written values are RAW: no expansion on write, ever (a
// process variable that the reference names must not leak into the file), and
// the reader is the only thing that expands. Values survive verbatim,
// including the bytes json.Marshal would have rewritten ('&').
func TestUpsertEntry_TU7_RoundTripRawValues(t *testing.T) {
	entry := ServerConfig{
		URL:  "https://x/y?a=1&b=2",
		Type: "http",
		Env:  map[string]string{"TOKEN": "${TOKEN}"},
		Headers: map[string]string{
			"Authorization": "Bearer ${KEY}",
			"X-Esc":         "$${LITERAL}",
			"X-Open":        "${UNTERMINATED",
		},
		Description: "d ${D}",
	}

	t.Run("values are written verbatim", func(t *testing.T) {
		t.Setenv("TOKEN", "LEAKME") // must never appear on disk
		path := filepath.Join(t.TempDir(), "mcp.json")
		if _, err := UpsertEntry(path, "s", entry); err != nil {
			t.Fatalf("UpsertEntry() error = %v", err)
		}
		got := readRawFixture(t, path)
		for _, ref := range []string{
			`"TOKEN":"${TOKEN}"`,
			`"Authorization":"Bearer ${KEY}"`,
			`"X-Esc":"$${LITERAL}"`,
			`"X-Open":"${UNTERMINATED"`,
			`"description":"d ${D}"`,
			`"url":"https://x/y?a=1&b=2"`,
		} {
			if !strings.Contains(got, ref) {
				t.Fatalf("file does not contain %s:\n%s", ref, got)
			}
		}
		if strings.Contains(got, "LEAKME") {
			t.Fatalf("expansion ran at write time:\n%s", got)
		}
		if strings.Contains(got, `\u0026`) {
			t.Fatalf("HTML escaping rewrote user bytes:\n%s", got)
		}

		// The raw view (no expansion) holds exactly what was asked for.
		doc, err := parseRawDoc([]byte(got))
		if err != nil {
			t.Fatalf("parseRawDoc(result) error = %v", err)
		}
		m, ok := doc.servers.get("s")
		if !ok {
			t.Fatal("s missing from the raw view")
		}
		var sc ServerConfig
		if err := json.Unmarshal(m.raw, &sc); err != nil {
			t.Fatalf("unmarshal raw member: %v", err)
		}
		if !reflect.DeepEqual(sc, entry) {
			t.Fatalf("raw member = %+v, want %+v", sc, entry)
		}
	})

	t.Run("the reader expands, the writer does not", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")
		if _, err := UpsertEntry(path, "s", entry); err != nil {
			t.Fatalf("UpsertEntry() error = %v", err)
		}
		f, err := parseFile(path, staticExpand(map[string]string{"TOKEN": "SECRET"}))
		if err != nil {
			t.Fatalf("parseFile() error = %v", err)
		}
		got := f.MCPServers["s"]
		if got.Env["TOKEN"] != "SECRET" {
			t.Fatalf("Env[TOKEN] = %q, want SECRET (expansion must happen on read)", got.Env["TOKEN"])
		}
		if got.Headers["X-Esc"] != "${LITERAL}" {
			t.Fatalf("Headers[X-Esc] = %q, want ${LITERAL} ($$ collapses at read time)", got.Headers["X-Esc"])
		}
		if got.Headers["X-Open"] != "${UNTERMINATED" {
			t.Fatalf("Headers[X-Open] = %q, want the unterminated reference verbatim", got.Headers["X-Open"])
		}
		if got.URL != "https://x/y?a=1&b=2" {
			t.Fatalf("URL = %q, want the verbatim URL", got.URL)
		}
	})

	t.Run("equivalent ServerConfig after ParseFile", func(t *testing.T) {
		tests := []struct {
			name  string
			entry ServerConfig
			want  ServerConfig
		}{
			{
				name:  "stdio",
				entry: ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}, Env: map[string]string{"K": "v"}, Description: "d"},
				want:  ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}, Env: map[string]string{"K": "v"}, Description: "d"},
			},
			{
				name:  "remote with explicit type",
				entry: ServerConfig{URL: "https://x", Type: "sse", Headers: map[string]string{"H": "v"}},
				want:  ServerConfig{URL: "https://x", Type: "sse", Headers: map[string]string{"H": "v"}},
			},
			{
				name:  "remote without type reads back as http",
				entry: ServerConfig{URL: "https://x"},
				want:  ServerConfig{URL: "https://x", Type: "http"},
			},
			{
				name:  "disabled entry is written with the flag",
				entry: ServerConfig{Command: "x", Disabled: true},
				want:  ServerConfig{Command: "x", Disabled: true},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "mcp.json")
				if _, err := UpsertEntry(path, "s", tt.entry); err != nil {
					t.Fatalf("UpsertEntry() error = %v", err)
				}
				if raw := readRawFixture(t, path); tt.entry.Disabled && !strings.Contains(raw, `"disabled":true`) {
					t.Fatalf("disabled entry not flagged on disk: %s", raw)
				} else if !tt.entry.Disabled && strings.Contains(raw, `"disabled"`) {
					t.Fatalf("disabled:false must never be written: %s", raw)
				}
				if tt.entry.Type == "" && strings.Contains(readRawFixture(t, path), `"type"`) {
					t.Fatalf("an unset type must not be written: %s", readRawFixture(t, path))
				}
				f, err := ParseFile(path)
				if err != nil {
					t.Fatalf("ParseFile(result) error = %v", err)
				}
				if len(f.EntryErrors) != 0 {
					t.Fatalf("EntryErrors = %v, want none", f.EntryErrors)
				}
				if got := f.MCPServers["s"]; !reflect.DeepEqual(got, tt.want) {
					t.Fatalf("round-trip = %+v, want %+v", got, tt.want)
				}
			})
		}
	})
}

// T-U8: duplicate names mirror write_test.go T10 exactly — the LAST (winning)
// occurrence is the one replaced / deleted, earlier copies stay verbatim. The
// consequence for delete is pinned too: after removing the winning copy the
// name still resolves to the earlier one, so removing every copy needs a
// second call.
func TestUpsertEntry_TU8_DuplicateKeyLastOccurrenceMutated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	in := `{"mcpServers":{"s":{"command":"first"},"s":{"command":"last"}}}`
	writeRawFixture(t, path, in)

	res, err := UpsertEntry(path, "s", ServerConfig{Command: "third"})
	if err != nil {
		t.Fatalf("UpsertEntry() error = %v", err)
	}
	if res.Created || !res.Replaced || !res.Changed {
		t.Fatalf("result = %+v, want Replaced+Changed", res)
	}
	want := `{"mcpServers":{"s":{"command":"first"},"s":{"command":"third"}}}`
	got := readRawFixture(t, path)
	if got != want {
		t.Fatalf("\n got: %s\nwant: %s", got, want)
	}
	if !strings.Contains(got, `"s":{"command":"first"}`) {
		t.Fatalf("first occurrence not verbatim: %s", got)
	}
	f, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile(result) error = %v", err)
	}
	if got := f.MCPServers["s"].Command; got != "third" {
		t.Fatalf("winning s.Command = %q, want third", got)
	}

	// Replacing the winner with what it already holds is a no-op.
	before := readRawFixture(t, path)
	res, err = UpsertEntry(path, "s", ServerConfig{Command: "third"})
	if err != nil {
		t.Fatalf("second UpsertEntry() error = %v", err)
	}
	if res.Created || res.Replaced || res.Changed {
		t.Fatalf("result = %+v, want a pure no-op", res)
	}
	if got := readRawFixture(t, path); got != before {
		t.Fatalf("bytes rewritten by a no-op: %s", got)
	}

	// Delete removes the winning occurrence only: "s" still resolves (to the
	// earlier copy), which is the documented mirror of T10.
	dres, err := DeleteEntry(path, "s")
	if err != nil {
		t.Fatalf("DeleteEntry() error = %v", err)
	}
	if !dres.Deleted || !dres.Changed {
		t.Fatalf("delete result = %+v, want Deleted+Changed", dres)
	}
	wantDel := `{"mcpServers":{"s":{"command":"first"}}}`
	if got := readRawFixture(t, path); got != wantDel {
		t.Fatalf("delete touched more than the winning copy:\n got: %s\nwant: %s", got, wantDel)
	}
	f, err = ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile(result) error = %v", err)
	}
	if got := f.MCPServers["s"].Command; got != "first" {
		t.Fatalf("after deleting the winner, s.Command = %q, want first (earlier copy)", got)
	}

	// A second delete removes the remaining copy.
	dres, err = DeleteEntry(path, "s")
	if err != nil {
		t.Fatalf("second DeleteEntry() error = %v", err)
	}
	if !dres.Deleted || !dres.Changed {
		t.Fatalf("second delete result = %+v, want Deleted+Changed", dres)
	}
	if got := readRawFixture(t, path); got != `{"mcpServers":{}}` {
		t.Fatalf("second delete bytes = %s, want an empty envelope", got)
	}
}

// T-D1: deleting a member removes its key, value and the adjacent comma —
// nothing else, valid JSON left behind and every other member byte-identical.
func TestDeleteEntry_TD1_CommaAndWhitespaceCleanup(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		// The full effective content of the result: the surviving members
		// must be exactly these, with exactly these values.
		wantServers map[string]ServerConfig
	}{
		{
			name:        "middle member",
			in:          `{"version":1,"mcpServers":{"drop": {"command":"d"},"keep":{"url":"u"}},"other":[1,2]}`,
			want:        `{"version":1,"mcpServers":{"keep":{"url":"u"}},"other":[1,2]}`,
			wantServers: map[string]ServerConfig{"keep": {URL: "u", Type: "http"}},
		},
		{
			name:        "last member",
			in:          `{"mcpServers":{"keep":{"command":"k"},"drop":{"command":"d"}}}`,
			want:        `{"mcpServers":{"keep":{"command":"k"}}}`,
			wantServers: map[string]ServerConfig{"keep": {Command: "k"}},
		},
		{
			name:        "only member",
			in:          `{"mcpServers":{"drop":{"command":"d"}}}`,
			want:        `{"mcpServers":{}}`,
			wantServers: map[string]ServerConfig{},
		},
		{
			name: "pretty printed, middle member",
			in: "{\n  \"mcpServers\": {\n    \"drop\": {\"command\": \"d\"},\n" +
				"    \"keep\": {\"command\": \"k\"}\n  },\n  \"tail\": 1\n}\n",
			want:        "{\n  \"mcpServers\": {\n    \"keep\": {\"command\": \"k\"}\n  },\n  \"tail\": 1\n}\n",
			wantServers: map[string]ServerConfig{"keep": {Command: "k"}},
		},
		{
			// The removed member's own indentation and its preceding comma
			// go with it; the neighbour's bytes are untouched, so the
			// closing brace ends up on the neighbour's line.
			name: "pretty printed, last member",
			in: "{\n  \"mcpServers\": {\n    \"keep\": {\"command\": \"k\"},\n" +
				"    \"drop\": {\"command\": \"d\"}\n  }\n}\n",
			want:        "{\n  \"mcpServers\": {\n    \"keep\": {\"command\": \"k\"}}\n}\n",
			wantServers: map[string]ServerConfig{"keep": {Command: "k"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			writeRawFixture(t, path, tt.in)

			res, err := DeleteEntry(path, "drop")
			if err != nil {
				t.Fatalf("DeleteEntry() error = %v", err)
			}
			if !res.Deleted || !res.Changed {
				t.Fatalf("result = %+v, want Deleted+Changed", res)
			}
			if res.Path != path || res.Name != "drop" {
				t.Fatalf("result identity = %q/%q, want %q/%q", res.Path, res.Name, path, "drop")
			}
			got := readRawFixture(t, path)
			if got != tt.want {
				t.Fatalf("\n got: %s\nwant: %s", got, tt.want)
			}
			f, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile(result) error = %v (result must stay loadable)", err)
			}
			if len(f.EntryErrors) != 0 {
				t.Fatalf("EntryErrors = %v, want none", f.EntryErrors)
			}
			if !reflect.DeepEqual(f.MCPServers, tt.wantServers) {
				t.Fatalf("resulting entries = %+v, want %+v", f.MCPServers, tt.wantServers)
			}
		})
	}
}

// T-D2: deleting the last member leaves a valid, empty envelope — and never
// merges duplicate envelopes into one (the collapse bug the envelope-scoped
// splice in planDeleteEntry prevents).
func TestDeleteEntry_TD2_EmptyEnvelopeStaysValid(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		target string
		want   string
		// The full effective content of the result: deleting the winning
		// occurrence of a duplicated name lets the earlier copy win again,
		// so that (still present) name is pinned here too.
		wantPresent map[string]string // name → command
	}{
		{
			name:        "only member, exact envelope",
			in:          `{"mcpServers":{"s":{"command":"x"}}}`,
			target:      "s",
			want:        `{"mcpServers":{}}`,
			wantPresent: map[string]string{},
		},
		{
			name:        "only member, case-variant envelope",
			in:          `{"MCPServers":{"s":{"command":"x"}}}`,
			target:      "s",
			want:        `{"MCPServers":{}}`,
			wantPresent: map[string]string{},
		},
		{
			name:        "last member of the first of two envelopes",
			in:          `{"mcpServers":{"a":{"command":"x"}},"mcpServers":{"b":{"command":"y"}}}`,
			target:      "a",
			want:        `{"mcpServers":{},"mcpServers":{"b":{"command":"y"}}}`,
			wantPresent: map[string]string{"b": "y"},
		},
		{
			name:        "winner of a case-variant envelope",
			in:          `{"mcpServers":{"a":{"command":"x"}},"MCPServers":{"a":{"command":"z"}}}`,
			target:      "a",
			want:        `{"mcpServers":{"a":{"command":"x"}},"MCPServers":{}}`,
			wantPresent: map[string]string{"a": "x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			writeRawFixture(t, path, tt.in)

			res, err := DeleteEntry(path, tt.target)
			if err != nil {
				t.Fatalf("DeleteEntry() error = %v", err)
			}
			if !res.Deleted || !res.Changed {
				t.Fatalf("result = %+v, want Deleted+Changed", res)
			}
			if res.Name != tt.target {
				t.Fatalf("result.Name = %q, want %q", res.Name, tt.target)
			}
			got := readRawFixture(t, path)
			if got != tt.want {
				t.Fatalf("\n got: %s\nwant: %s", got, tt.want)
			}
			f, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile(result) error = %v", err)
			}
			names := make(map[string]string, len(f.MCPServers))
			for n, s := range f.MCPServers {
				names[n] = s.Command
			}
			if !reflect.DeepEqual(names, tt.wantPresent) {
				t.Fatalf("effective entries = %v, want %v", names, tt.wantPresent)
			}
		})
	}
}

// T-D3: no-op paths — missing file (and no file created), no envelope, null
// envelope, empty envelope, absent entry, null document. All nil error, zero
// result, bytes untouched.
func TestDeleteEntry_TD3_NoOps(t *testing.T) {
	tests := []struct {
		name    string
		in      string // "" = file does not exist
		missing bool
	}{
		{name: "missing file", in: "", missing: true},
		{name: "no envelope member", in: `{"version":1}`},
		{name: "null envelope", in: `{"mcpServers":null}`},
		{name: "empty envelope", in: `{"mcpServers":{}}`},
		{name: "absent entry", in: `{"mcpServers":{"other":{"command":"o"}}}`},
		{name: "null document", in: `null`},
		{name: "case-variant envelope without the name", in: `{"MCPServers":{"other":{"command":"o"}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "nested", "mcp.json")
			if !tt.missing {
				writeRawFixture(t, path, tt.in)
			}

			res, err := DeleteEntry(path, "s")
			if err != nil {
				t.Fatalf("DeleteEntry() error = %v", err)
			}
			if res.Deleted || res.Changed {
				t.Fatalf("result = %+v, want a pure no-op", res)
			}
			if res.Path != path || res.Name != "s" {
				t.Fatalf("result identity = %q/%q", res.Path, res.Name)
			}
			if tt.missing {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("file created by a delete no-op (stat err = %v)", err)
				}
				return
			}
			if got := readRawFixture(t, path); got != tt.in {
				t.Fatalf("file modified by a no-op:\n got: %s\nwant: %s", got, tt.in)
			}
		})
	}
}

// T-D4: the write recipe (0600, no temp file left behind) and the failure
// contract of both functions: a rejected or impossible call leaves the file
// byte-identical (rules 6, 7).
func TestEntryWriter_TD4_AtomicityAndFailures(t *testing.T) {
	t.Run("successful upsert and delete leave 0600 and no temp", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "mcp.json")
		if _, err := UpsertEntry(path, "s", ServerConfig{Command: "x"}); err != nil {
			t.Fatalf("UpsertEntry() error = %v", err)
		}
		if perm := filePerm(t, path); perm != 0600 {
			t.Fatalf("mode after upsert = %o, want 600", perm)
		}
		if _, err := DeleteEntry(path, "s"); err != nil {
			t.Fatalf("DeleteEntry() error = %v", err)
		}
		if perm := filePerm(t, path); perm != 0600 {
			t.Fatalf("mode after delete = %o, want 600", perm)
		}
		assertNoTmp(t, dir)
	})

	t.Run("failing calls never touch the file", func(t *testing.T) {
		base := `{"mcpServers":{"s":{"command":"x"}}}`
		tests := []struct {
			name    string
			in      string
			call    func(path string) error
			wantErr string
		}{
			{
				name: "upsert: empty name",
				in:   base,
				call: func(path string) error {
					_, err := UpsertEntry(path, "", ServerConfig{Command: "n"})
					return err
				},
				wantErr: "empty",
			},
			{
				name: "upsert: slash in name",
				in:   base,
				call: func(path string) error {
					_, err := UpsertEntry(path, "a/b", ServerConfig{Command: "n"})
					return err
				},
				wantErr: "must not contain '/'",
			},
			{
				name: "upsert: invalid entry",
				in:   base,
				call: func(path string) error {
					_, err := UpsertEntry(path, "n", ServerConfig{})
					return err
				},
				wantErr: "needs command",
			},
			{
				name: "upsert: malformed document",
				in:   `{`,
				call: func(path string) error {
					_, err := UpsertEntry(path, "n", ServerConfig{Command: "x"})
					return err
				},
				wantErr: "parse",
			},
			{
				name: "upsert: envelope not an object",
				in:   `{"mcpServers":[]}`,
				call: func(path string) error {
					_, err := UpsertEntry(path, "n", ServerConfig{Command: "x"})
					return err
				},
				wantErr: "parse",
			},
			{
				name: "delete: empty name",
				in:   base,
				call: func(path string) error {
					_, err := DeleteEntry(path, "")
					return err
				},
				wantErr: "empty",
			},
			{
				name: "delete: slash in name",
				in:   base,
				call: func(path string) error {
					_, err := DeleteEntry(path, "a/b")
					return err
				},
				wantErr: "must not contain '/'",
			},
			{
				name: "delete: malformed document",
				in:   `{`,
				call: func(path string) error {
					_, err := DeleteEntry(path, "s")
					return err
				},
				wantErr: "parse",
			},
			{
				name: "delete: envelope not an object",
				in:   `{"mcpServers":[]}`,
				call: func(path string) error {
					_, err := DeleteEntry(path, "s")
					return err
				},
				wantErr: "parse",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "mcp.json")
				writeRawFixture(t, path, tt.in)

				err := tt.call(path)
				if err == nil {
					t.Fatal("error = nil, want a failure")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want it to mention %q", err, tt.wantErr)
				}
				if got := readRawFixture(t, path); got != tt.in {
					t.Fatalf("file modified by a failing call:\n got: %s\nwant: %s", got, tt.in)
				}
				assertNoTmp(t, dir)
			})
		}
	})

	t.Run("unreadable path is an error, not a creation", func(t *testing.T) {
		// A directory cannot be read as a file: neither function may
		// "create" over it.
		dir := t.TempDir()
		path := filepath.Join(dir, "mcp.json")
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if _, err := UpsertEntry(path, "s", ServerConfig{Command: "x"}); err == nil {
			t.Fatal("UpsertEntry() error = nil, want a read failure")
		}
		if _, err := DeleteEntry(path, "s"); err == nil {
			t.Fatal("DeleteEntry() error = nil, want a read failure")
		}
		fi, err := os.Stat(path)
		if err != nil || !fi.IsDir() {
			t.Fatalf("path no longer a directory: isDir=%v err=%v", fi != nil && fi.IsDir(), err)
		}
	})
}

// T-U9: differential — after an upsert, ParseFile must load the result, every
// name that was visible before must still be visible with identical effective
// values, and the target must carry exactly the new entry. Golden bytes pin
// the splice (including the ones the name set alone cannot see).
func TestUpsertEntry_TU9_Differential(t *testing.T) {
	entry := ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}
	tests := []struct {
		name   string
		in     string
		target string
		golden string
	}{
		{
			name:   "plain document",
			in:     `{"mcpServers":{"a":{"command":"a"}},"other":1}`,
			target: "b",
			golden: `{"mcpServers":{"a":{"command":"a"}, "b": {"command":"npx","args":["-y","pkg"]}},"other":1}`,
		},
		{
			name:   "duplicate envelopes",
			in:     `{"mcpServers":{"a":{"command":"a"}},"mcpServers":{"b":{"command":"b"}}}`,
			target: "c",
			golden: `{"mcpServers":{"a":{"command":"a"}},"mcpServers":{"b":{"command":"b"}, "c": {"command":"npx","args":["-y","pkg"]}}}`,
		},
		{
			name:   "replace inside a case-variant envelope",
			in:     `{"MCPServers":{"a":{"command":"a"}}}`,
			target: "a",
			golden: `{"MCPServers":{"a":{"command":"npx","args":["-y","pkg"]}}}`,
		},
		{
			name:   "materialise a null entry",
			in:     `{"mcpServers":{"a":null}}`,
			target: "a",
			golden: `{"mcpServers":{"a":{"command":"npx","args":["-y","pkg"]}}}`,
		},
		{
			name:   "replace the winner of a duplicated name",
			in:     `{"mcpServers":{"s":{"command":"first"},"s":{"command":"last"}}}`,
			target: "s",
			golden: `{"mcpServers":{"s":{"command":"first"},"s":{"command":"npx","args":["-y","pkg"]}}}`,
		},
		{
			name:   "pretty printed document",
			in:     "{\n  \"mcpServers\": {\n    \"a\": {\"command\": \"a\"}\n  }\n}\n",
			target: "b",
			golden: "{\n  \"mcpServers\": {\n    \"a\": {\"command\": \"a\"}, \"b\": {\"command\":\"npx\",\"args\":[\"-y\",\"pkg\"]}\n  }\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			writeRawFixture(t, path, tt.in)

			inFile, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile(input) error = %v", err)
			}
			res, err := UpsertEntry(path, tt.target, entry)
			if err != nil {
				t.Fatalf("UpsertEntry() error = %v", err)
			}
			if !res.Changed {
				t.Fatalf("result = %+v, want Changed", res)
			}
			if got := readRawFixture(t, path); got != tt.golden {
				t.Fatalf("bytes after the edit:\n got: %s\nwant: %s", got, tt.golden)
			}
			outFile, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile(result) error = %v (the writer must never emit a document the runtime refuses)", err)
			}
			// Names visible before stay visible with the same values.
			for n, before := range inFile.MCPServers {
				if n == tt.target {
					continue
				}
				after, ok := outFile.MCPServers[n]
				if !ok {
					t.Fatalf("name %q lost from the result", n)
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("non-target %q changed: before %+v, after %+v", n, before, after)
				}
			}
			after, ok := outFile.MCPServers[tt.target]
			if !ok {
				t.Fatalf("target %q missing from the result", tt.target)
			}
			if !reflect.DeepEqual(after, entry) {
				t.Fatalf("target %q = %+v, want %+v", tt.target, after, entry)
			}
		})
	}
}

// T-D5: differential for delete — the name set may only lose the target and
// every surviving name keeps its effective value; golden bytes pin the splice.
func TestDeleteEntry_TD5_Differential(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		target string
		golden string
		// The full effective content of the result. Deleting the winning
		// occurrence of a duplicated name changes that name's value (the
		// earlier copy wins again) — pinned here rather than assumed away.
		want map[string]ServerConfig
	}{
		{
			name:   "plain document",
			in:     `{"mcpServers":{"a":{"command":"a"},"b":{"command":"b"}}}`,
			target: "a",
			golden: `{"mcpServers":{"b":{"command":"b"}}}`,
			want:   map[string]ServerConfig{"b": {Command: "b"}},
		},
		{
			name:   "first of two envelopes",
			in:     `{"mcpServers":{"a":{"command":"a"}},"mcpServers":{"b":{"command":"b"}}}`,
			target: "a",
			golden: `{"mcpServers":{},"mcpServers":{"b":{"command":"b"}}}`,
			want:   map[string]ServerConfig{"b": {Command: "b"}},
		},
		{
			name:   "duplicated name keeps the earlier copy",
			in:     `{"mcpServers":{"a":{"command":"first"},"a":{"command":"last"}}}`,
			target: "a",
			golden: `{"mcpServers":{"a":{"command":"first"}}}`,
			want:   map[string]ServerConfig{"a": {Command: "first"}},
		},
		{
			name:   "only member",
			in:     `{"mcpServers":{"a":{"command":"a"}}}`,
			target: "a",
			golden: `{"mcpServers":{}}`,
			want:   map[string]ServerConfig{},
		},
		{
			name:   "absent target is a no-op",
			in:     `{"mcpServers":{"a":{"command":"a"},"b":{"command":"b"}}}`,
			target: "zz",
			golden: `{"mcpServers":{"a":{"command":"a"},"b":{"command":"b"}}}`,
			want:   map[string]ServerConfig{"a": {Command: "a"}, "b": {Command: "b"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			writeRawFixture(t, path, tt.in)

			// The input itself must be a document the runtime loads, so a
			// difference in the result is the writer's doing.
			if _, err := ParseFile(path); err != nil {
				t.Fatalf("ParseFile(input) error = %v", err)
			}
			if _, err := DeleteEntry(path, tt.target); err != nil {
				t.Fatalf("DeleteEntry() error = %v", err)
			}
			if got := readRawFixture(t, path); got != tt.golden {
				t.Fatalf("bytes after the delete:\n got: %s\nwant: %s", got, tt.golden)
			}
			outFile, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile(result) error = %v", err)
			}
			if !reflect.DeepEqual(outFile.MCPServers, tt.want) {
				t.Fatalf("effective entries = %+v, want %+v", outFile.MCPServers, tt.want)
			}
		})
	}
}

// T-E1: EntryRaw hands back the entry's value bytes EXACTLY as stored — odd
// whitespace/escaping survive (it is the editor's payload), and ${VAR}/$$ are
// literal, never expanded: an environment variable that the reference names
// must not leak into the returned bytes.
func TestEntryRaw_TE1_ExactStoredBytes(t *testing.T) {
	const canary = "CANARY-t-e1-value"
	t.Setenv("T", canary)

	path := filepath.Join(t.TempDir(), "mcp.json")
	entry := `{"command": "cmd",  "env":{"T":"${T}","E":"$$"},"flush":"a\tb"}`
	in := "{\n" +
		"\t\"version\": \"1\",\n" +
		"\t\"mcpServers\": {\n" +
		"\t\t\"keep\": {\"command\":\"k\"},\n" +
		"\t\t\"s\": " + entry + "\n" +
		"\t}\n" +
		"}\n"
	writeRawFixture(t, path, in)

	raw, found, err := EntryRaw(path, "s")
	if err != nil {
		t.Fatalf("EntryRaw() error = %v", err)
	}
	if !found {
		t.Fatal("EntryRaw() found = false, want true")
	}
	if got := string(raw); got != entry {
		t.Fatalf("raw bytes:\n got: %s\nwant: %s", got, entry)
	}
	// A raw read is expansion-free: the canary's VALUE must not appear.
	if strings.Contains(string(raw), canary) {
		t.Fatalf("expansion ran in EntryRaw: %s", raw)
	}
	// It is the entry object only — no name wrapper.
	if !strings.HasPrefix(string(raw), "{") || strings.Contains(string(raw), `"s"`) {
		t.Fatalf("raw must be the bare entry object, got: %s", raw)
	}
	// The bytes are a copy: mutating them must not affect a second read.
	raw[0] = 'X'
	again, _, err := EntryRaw(path, "s")
	if err != nil {
		t.Fatalf("second EntryRaw() error = %v", err)
	}
	if string(again) != entry {
		t.Fatalf("EntryRaw returned aliased/unsafe bytes:\n got: %s\nwant: %s", again, entry)
	}
}

// T-E2: when a name is duplicated, EntryRaw reports the WINNING occurrence —
// the last one in file order, the copy UpsertEntry would replace. The pairing
// is pinned by editing that same writer afterwards: the first copy must stay
// verbatim, proving both operations agree on which copy is "the entry".
func TestEntryRaw_TE2_DuplicateNameWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	in := `{"mcpServers":{"s":{"command":"first"},"s":{"command":"last"}}}`
	writeRawFixture(t, path, in)

	raw, found, err := EntryRaw(path, "s")
	if err != nil {
		t.Fatalf("EntryRaw() error = %v", err)
	}
	if !found {
		t.Fatal("EntryRaw() found = false, want true")
	}
	if got, want := string(raw), `{"command":"last"}`; got != want {
		t.Fatalf("raw = %s, want the winning (last) copy %s", got, want)
	}

	// UpsertEntry must replace the same occurrence EntryRaw reported.
	if _, err := UpsertEntry(path, "s", ServerConfig{Command: "third"}); err != nil {
		t.Fatalf("UpsertEntry() error = %v", err)
	}
	got := readRawFixture(t, path)
	if want := `{"mcpServers":{"s":{"command":"first"},"s":{"command":"third"}}}`; got != want {
		t.Fatalf("writer and reader disagree on the winner:\n got: %s\nwant: %s", got, want)
	}
}

// T-E3: the envelope rules EntryRaw must inherit from parseRawDoc — a
// case-variant "mcpServers" and repeated occurrences folded with
// last-in-file-order precedence (the name set json.Unmarshal produces).
func TestEntryRaw_TE3_EnvelopeVariants(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string // name → expected raw bytes ("" ⇒ not found)
	}{
		{
			name: "case-variant envelope",
			in:   `{"MCPServers":{"c":{"command":"c"}}}`,
			want: map[string]string{"c": `{"command":"c"}`},
		},
		{
			name: "duplicate envelopes fold, later occurrence wins",
			in:   `{"mcpServers":{"a":{"command":"one"}},"mcpServers":{"a":{"command":"two"},"b":{"command":"bee"}}}`,
			want: map[string]string{"a": `{"command":"two"}`, "b": `{"command":"bee"}`},
		},
		{
			name: "names from both envelopes stay reachable",
			in:   `{"mcpServers":{"a":{"command":"one"}},"mcpServers":{"b":{"command":"two"}}}`,
			want: map[string]string{"a": `{"command":"one"}`, "b": `{"command":"two"}`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			writeRawFixture(t, path, tt.in)
			for name, want := range tt.want {
				raw, found, err := EntryRaw(path, name)
				if err != nil {
					t.Fatalf("EntryRaw(%q) error = %v", name, err)
				}
				if !found {
					t.Fatalf("EntryRaw(%q) found = false, want true", name)
				}
				if got := string(raw); got != want {
					t.Fatalf("EntryRaw(%q) = %s, want %s", name, got, want)
				}
			}
		})
	}
}

// T-E4: the negative battery. A missing file, a missing entry, a document
// whose null envelope cleared an earlier object, and a malformed document —
// each must be classified the way the rest of the entry layer classifies it
// (nil-error "not found" vs. an error naming the path).
func TestEntryRaw_TE4_NotFoundAndErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent", "mcp.json")
		raw, found, err := EntryRaw(path, "s")
		if err != nil {
			t.Fatalf("EntryRaw() error = %v, want nil", err)
		}
		if found || raw != nil {
			t.Fatalf("found = %v raw = %q, want false/nil", found, raw)
		}
	})

	tests := []struct {
		name string
		in   string
	}{
		{name: "missing entry", in: `{"mcpServers":{"other":{"command":"o"}}}`},
		{name: "absent envelope", in: `{"version":"1"}`},
		{name: "empty envelope", in: `{"mcpServers":{}}`},
		{name: "null envelope", in: `{"mcpServers":null}`},
		{name: "null envelope clearing an earlier object", in: `{"mcpServers":{"s":{"command":"x"}},"mcpServers":null}`},
		{name: "top-level null", in: `null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			writeRawFixture(t, path, tt.in)
			raw, found, err := EntryRaw(path, "s")
			if err != nil {
				t.Fatalf("EntryRaw() error = %v, want nil", err)
			}
			if found || raw != nil {
				t.Fatalf("found = %v raw = %q, want false/nil", found, raw)
			}
		})
	}

	malformed := []struct {
		name string
		in   string
	}{
		{name: "not json", in: `not json at all`},
		{name: "truncated", in: `{"mcpServers": {"s": `},
		{name: "envelope is an array", in: `{"mcpServers":[1,2]}`},
		{name: "trailing data", in: `{"mcpServers":{"s":{"command":"x"}}} extra`},
	}
	for _, tt := range malformed {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			writeRawFixture(t, path, tt.in)
			raw, found, err := EntryRaw(path, "s")
			if err == nil {
				t.Fatalf("EntryRaw() error = nil, want a parse failure")
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error = %q, want it to name the path %s", err, path)
			}
			if strings.Contains(err.Error(), "read") {
				t.Fatalf("error = %q, want a parse failure, not a read failure", err)
			}
			if found || raw != nil {
				t.Fatalf("found = %v raw = %q, want false/nil on error", found, raw)
			}
		})
	}
}

// T-U10: the optional "auth" block is serialized canonically — a compact
// object holding only non-zero fields, in the fixed key order — and it lands
// exactly between "headers" (or "type" when there are no headers) and
// "description".
func TestUpsertEntry_TU10_AuthCanonicalBytes(t *testing.T) {
	full := &AuthSpec{
		Type:                         "oauth",
		Scopes:                       []string{"read", "write"},
		ClientID:                     "client-1",
		ClientSecret:                 "secret-1",
		RedirectURI:                  "http://127.0.0.1:8080/cb",
		AuthServerMetadataURL:        "https://auth.example/.well-known/oauth-authorization-server",
		ProtectedResourceMetadataURL: "https://mcp.example/.well-known/oauth-protected-resource",
	}
	const fullAuth = `{"type":"oauth","scopes":["read","write"],"client_id":"client-1","client_secret":"secret-1","redirect_uri":"http://127.0.0.1:8080/cb","auth_server_metadata_url":"https://auth.example/.well-known/oauth-authorization-server","protected_resource_metadata_url":"https://mcp.example/.well-known/oauth-protected-resource"}`

	tests := []struct {
		name   string
		entry  ServerConfig
		want   string
		before string // the key that must immediately precede "auth"
	}{
		{
			name: "after headers, before description",
			entry: ServerConfig{
				URL:         "https://mcp.example/mcp",
				Type:        "http",
				Headers:     map[string]string{"Authorization": "Bearer ${KEY}"},
				Auth:        full,
				Description: "remote",
			},
			want:   `{"mcpServers":{"s":{"url":"https://mcp.example/mcp","type":"http","headers":{"Authorization":"Bearer ${KEY}"},"auth":` + fullAuth + `,"description":"remote"}}}`,
			before: `"headers":`,
		},
		{
			name: "no headers: after type, before description",
			entry: ServerConfig{
				URL:         "https://x",
				Type:        "sse",
				Auth:        &AuthSpec{Type: "oauth", Scopes: []string{"read"}},
				Description: "d",
			},
			want:   `{"mcpServers":{"s":{"url":"https://x","type":"sse","auth":{"type":"oauth","scopes":["read"]},"description":"d"}}}`,
			before: `"type":`,
		},
		{
			name: "remote entry without an explicit type",
			entry: ServerConfig{
				URL:         "https://x",
				Auth:        full,
				Description: "d",
			},
			want:   `{"mcpServers":{"s":{"url":"https://x","auth":` + fullAuth + `,"description":"d"}}}`,
			before: `"url":`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			res, err := UpsertEntry(path, "s", tt.entry)
			if err != nil {
				t.Fatalf("UpsertEntry() error = %v", err)
			}
			if !res.Created || !res.Changed || res.Replaced {
				t.Fatalf("result = %+v, want Created+Changed", res)
			}
			got := readRawFixture(t, path)
			if got != tt.want {
				t.Fatalf("canonical bytes:\n got: %s\nwant: %s", got, tt.want)
			}
			// Positional proof of the fixed order: "auth" sits after its
			// predecessor key and before "description".
			ai := strings.Index(got, `"auth":`)
			bi := strings.Index(got, tt.before)
			di := strings.Index(got, `"description":`)
			if ai < 0 || bi < 0 || di < 0 {
				t.Fatalf("expected key missing (auth=%d, %s=%d, description=%d): %s", ai, tt.before, bi, di, got)
			}
			if bi >= ai || ai >= di {
				t.Fatalf(`"auth" at %d is not strictly between %s at %d and "description" at %d: %s`, ai, tt.before, bi, di, got)
			}
		})
	}
}

// T-U11: an entry carrying an auth block is idempotent — the second identical
// upsert leaves the file byte-identical and reports a pure no-op (auth
// included, so the block never drifts between two runs).
func TestUpsertEntry_TU11_AuthIdempotent(t *testing.T) {
	entry := ServerConfig{
		URL:  "https://mcp.example/mcp",
		Type: "http",
		// Map iteration order is random: the sorted keys must still make the
		// serialization stable.
		Headers: map[string]string{"X-B": "2", "X-A": "1"},
		Auth: &AuthSpec{
			Type:        "oauth",
			Scopes:      []string{"a", "b"},
			ClientID:    "id",
			RedirectURI: "http://localhost:1234/cb",
		},
		Description: "d",
	}
	path := filepath.Join(t.TempDir(), "mcp.json")

	first, err := UpsertEntry(path, "s", entry)
	if err != nil {
		t.Fatalf("UpsertEntry() error = %v", err)
	}
	if !first.Created || !first.Changed || first.Replaced {
		t.Fatalf("first result = %+v, want Created+Changed", first)
	}
	after := readRawFixture(t, path)
	if !strings.Contains(after, `"auth":{"type":"oauth","scopes":["a","b"],"client_id":"id","redirect_uri":"http://localhost:1234/cb"}`) {
		t.Fatalf("auth block not canonical on disk: %s", after)
	}

	second, err := UpsertEntry(path, "s", entry)
	if err != nil {
		t.Fatalf("second UpsertEntry() error = %v", err)
	}
	if second.Created || second.Replaced || second.Changed {
		t.Fatalf("second result = %+v, want a pure no-op", second)
	}
	if got := readRawFixture(t, path); got != after {
		t.Fatalf("bytes rewritten by a no-op:\n got: %s\nwant: %s", got, after)
	}
}

// T-U12: auth values are RAW. "${MY_SECRET}" is written byte-for-byte (never
// expanded: the referenced value must not leak into the file) and "$$" is
// written verbatim too (it only collapses at READ time), with no HTML
// escaping either.
func TestUpsertEntry_TU12_AuthValuesWrittenVerbatim(t *testing.T) {
	const canary = "LEAKME-t-u12-auth"
	t.Setenv("MY_SECRET", canary)

	entry := ServerConfig{
		URL: "https://x",
		Auth: &AuthSpec{
			Type:         "oauth",
			Scopes:       []string{"$${LITERAL}", "${SCOPE}"},
			ClientID:     "${CLIENT_ID}",
			ClientSecret: "${MY_SECRET}",
			RedirectURI:  "http://127.0.0.1:0/cb?a=1&b=2",
		},
	}
	path := filepath.Join(t.TempDir(), "mcp.json")
	if _, err := UpsertEntry(path, "s", entry); err != nil {
		t.Fatalf("UpsertEntry() error = %v", err)
	}
	got := readRawFixture(t, path)
	for _, ref := range []string{
		`"client_secret":"${MY_SECRET}"`,
		`"client_id":"${CLIENT_ID}"`,
		`"scopes":["$${LITERAL}","${SCOPE}"]`,
		`"redirect_uri":"http://127.0.0.1:0/cb?a=1&b=2"`,
	} {
		if !strings.Contains(got, ref) {
			t.Fatalf("file does not contain %s:\n%s", ref, got)
		}
	}
	if strings.Contains(got, canary) {
		t.Fatalf("expansion ran at write time:\n%s", got)
	}
	if strings.Contains(got, `\u0026`) || strings.Contains(got, `\$`) {
		t.Fatalf("escaping rewrote user bytes:\n%s", got)
	}

	// The raw view returns the same literal bytes: still unexpanded.
	raw, found, err := EntryRaw(path, "s")
	if err != nil {
		t.Fatalf("EntryRaw() error = %v", err)
	}
	if !found {
		t.Fatal("EntryRaw() found = false, want true")
	}
	if strings.Contains(string(raw), canary) {
		t.Fatalf("expansion ran in EntryRaw: %s", raw)
	}
	var sc ServerConfig
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatalf("unmarshal raw member: %v", err)
	}
	if sc.Auth == nil {
		t.Fatal("auth block lost in the raw view")
	}
	if sc.Auth.ClientSecret != "${MY_SECRET}" {
		t.Fatalf("ClientSecret = %q, want the literal ${MY_SECRET}", sc.Auth.ClientSecret)
	}
	if sc.Auth.Scopes[0] != "$${LITERAL}" {
		t.Fatalf("Scopes[0] = %q, want the literal $${LITERAL}", sc.Auth.Scopes[0])
	}
}

// T-U13: a declared-but-empty auth block is NOT dropped: it is written as
// "{}", the raw view hands it back and the reader sees a non-nil Auth (which
// is how "declared, discovery will fill it" is spelled).
func TestUpsertEntry_TU13_AuthEmptyObjectRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if _, err := UpsertEntry(path, "s", ServerConfig{URL: "https://x", Auth: &AuthSpec{}}); err != nil {
		t.Fatalf("UpsertEntry() error = %v", err)
	}
	want := `{"mcpServers":{"s":{"url":"https://x","auth":{}}}}`
	if got := readRawFixture(t, path); got != want {
		t.Fatalf("canonical bytes:\n got: %s\nwant: %s", got, want)
	}

	raw, found, err := EntryRaw(path, "s")
	if err != nil {
		t.Fatalf("EntryRaw() error = %v", err)
	}
	if !found {
		t.Fatal("EntryRaw() found = false, want true")
	}
	if !strings.Contains(string(raw), `"auth":{}`) {
		t.Fatalf(`raw = %s, want it to contain "auth":{}`, raw)
	}
	var sc ServerConfig
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatalf("unmarshal raw member: %v", err)
	}
	if sc.Auth == nil {
		t.Fatal("Auth = nil, want the declared empty block back")
	}
	if !reflect.DeepEqual(sc.Auth, &AuthSpec{}) {
		t.Fatalf("Auth = %+v, want &AuthSpec{}", sc.Auth)
	}
}

// T-U14: Auth == nil means "the caller declared no auth block" — no "auth"
// key appears anywhere in the file, and the reader reports a nil Auth.
func TestUpsertEntry_TU14_AuthOmittedWhenNil(t *testing.T) {
	entry := ServerConfig{
		URL:         "https://x",
		Type:        "http",
		Headers:     map[string]string{"H": "v"},
		Description: "d",
	}
	path := filepath.Join(t.TempDir(), "mcp.json")
	if _, err := UpsertEntry(path, "s", entry); err != nil {
		t.Fatalf("UpsertEntry() error = %v", err)
	}
	got := readRawFixture(t, path)
	if strings.Contains(got, `"auth"`) {
		t.Fatalf("a nil Auth emitted a block:\n%s", got)
	}
	want := `{"mcpServers":{"s":{"url":"https://x","type":"http","headers":{"H":"v"},"description":"d"}}}`
	if got != want {
		t.Fatalf("canonical bytes:\n got: %s\nwant: %s", got, want)
	}

	raw, found, err := EntryRaw(path, "s")
	if err != nil {
		t.Fatalf("EntryRaw() error = %v", err)
	}
	if !found {
		t.Fatal("EntryRaw() found = false, want true")
	}
	var sc ServerConfig
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatalf("unmarshal raw member: %v", err)
	}
	if sc.Auth != nil {
		t.Fatalf("Auth = %+v, want nil", sc.Auth)
	}
}

// T-U15: the full round-trip an `mcp edit` needs. UpsertEntry → EntryRaw →
// json.Unmarshal must hand back an Auth deep-equal to the one that went in,
// for every spec shape (values are raw, so no expansion intervenes).
func TestUpsertEntry_TU15_AuthRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		auth *AuthSpec
	}{
		{
			name: "full spec",
			auth: &AuthSpec{
				Type:                         "oauth",
				Scopes:                       []string{"read", "write", "admin"},
				ClientID:                     "client-1",
				ClientSecret:                 "s3cret",
				RedirectURI:                  "http://127.0.0.1:9/cb",
				AuthServerMetadataURL:        "https://auth.example/as",
				ProtectedResourceMetadataURL: "https://mcp.example/pr",
			},
		},
		{name: "type only", auth: &AuthSpec{Type: "none"}},
		{name: "client id only", auth: &AuthSpec{ClientID: "just-an-id"}},
		{name: "scopes only", auth: &AuthSpec{Scopes: []string{"a"}}},
		{name: "declared but empty", auth: &AuthSpec{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := ServerConfig{URL: "https://x", Type: "http", Auth: tt.auth}
			path := filepath.Join(t.TempDir(), "mcp.json")
			if _, err := UpsertEntry(path, "s", entry); err != nil {
				t.Fatalf("UpsertEntry() error = %v", err)
			}
			raw, found, err := EntryRaw(path, "s")
			if err != nil {
				t.Fatalf("EntryRaw() error = %v", err)
			}
			if !found {
				t.Fatal("EntryRaw() found = false, want true")
			}
			var got ServerConfig
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal raw member: %v", err)
			}
			if !reflect.DeepEqual(got.Auth, tt.auth) {
				t.Fatalf("round-trip Auth = %+v, want %+v", got.Auth, tt.auth)
			}
		})
	}
}
