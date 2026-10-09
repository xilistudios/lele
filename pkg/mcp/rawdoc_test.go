package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// rawFixture is a hand-formatted mcp.json: 2-space indent, an unknown
// top-level key, an entry with unknown fields and messy inner spacing. Every
// raw-span assertion below depends on this exact byte layout.
const rawFixture = "{\n" +
	"  \"mcpServers\": {\n" +
	"    \"first\": { \"command\" : \"c1\" ,\n" +
	"       \"env\" : { \"A\" : \"${SECRET}\" } },\n" +
	"    \"second\": {\"url\":\"https://x/mcp\",\"type\":\"sse\",\"extra\":true}\n" +
	"  },\n" +
	"  \"extra\": {\"any\": [1, 2, 3]},\n" +
	"  \"note\": \"hello world\"\n" +
	"}\n"

func TestParseRawDocCapturesSpansAndOrder(t *testing.T) {
	// Even with CANARY set in the environment, parseRawDoc must never expand.
	t.Setenv("SECRET", "top-secret-value")
	data := []byte(rawFixture)

	doc, err := parseRawDoc(data)
	if err != nil {
		t.Fatalf("parseRawDoc() error = %v", err)
	}

	// Top-level member order is file order.
	var keys []string
	for _, m := range doc.members {
		keys = append(keys, m.key)
	}
	if want := []string{"mcpServers", "extra", "note"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("top-level member order = %v, want %v", keys, want)
	}
	// Every captured span is the document's own bytes at those offsets.
	for _, m := range doc.members {
		if m.start < 0 || m.end > len(data) || !bytes.Equal(data[m.start:m.end], m.raw) {
			t.Errorf("member %q span [%d,%d] does not match document bytes", m.key, m.start, m.end)
		}
	}

	// mcpServers: member order is file order, raw bytes keep inner whitespace
	// verbatim (including the literal ${SECRET}, which must not be expanded).
	if doc.servers == nil {
		t.Fatal("servers object not captured")
	}
	var entryKeys []string
	for _, m := range doc.servers.members {
		entryKeys = append(entryKeys, m.key)
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(entryKeys, want) {
		t.Fatalf("entry order = %v, want %v", entryKeys, want)
	}
	first, ok := doc.servers.get("first")
	if !ok {
		t.Fatal("entry \"first\" not found")
	}
	if !bytes.Equal(data[first.start:first.end], first.raw) {
		t.Errorf("entry span does not match document bytes")
	}
	if !bytes.Contains(first.raw, []byte("\"command\" : \"c1\"")) {
		t.Errorf("entry raw lost inner spacing: %q", first.raw)
	}
	if !bytes.Contains(first.raw, []byte("${SECRET}")) {
		t.Errorf("entry raw lost the literal placeholder: %q", first.raw)
	}
	if bytes.Contains(first.raw, []byte("top-secret-value")) {
		t.Errorf("entry raw contains an expanded value: %q", first.raw)
	}
	second, ok := doc.servers.get("second")
	if !ok {
		t.Fatal("entry \"second\" not found")
	}
	if !bytes.Contains(second.raw, []byte("\"extra\":true")) {
		t.Errorf("entry raw lost unknown fields: %q", second.raw)
	}
}

func TestParseRawDocAccepts(t *testing.T) {
	tests := []struct {
		name        string
		data        string
		wantMembers int
		wantServers bool
	}{
		{name: "plain object", data: `{"mcpServers":{"a":{"command":"c"}}}`, wantMembers: 1, wantServers: true},
		{name: "empty object", data: `{}`, wantMembers: 0, wantServers: false},
		{name: "top-level null", data: `null`, wantMembers: 0, wantServers: false},
		{name: "null mcpServers", data: `{"mcpServers":null}`, wantMembers: 1, wantServers: false},
		{name: "empty mcpServers", data: `{"mcpServers":{}}`, wantMembers: 1, wantServers: true},
		{name: "trailing whitespace", data: "  {\"mcpServers\":{}} \n\t", wantMembers: 1, wantServers: true},
		{name: "unknown top-level members of any shape", data: `{"mcpServers":{},"list":[1],"num":5,"str":"x"}`, wantMembers: 4, wantServers: true},
		{name: "null entry parses, invalid verdict lives in Summary", data: `{"mcpServers":{"a":null}}`, wantMembers: 1, wantServers: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := parseRawDoc([]byte(tt.data))
			if err != nil {
				t.Fatalf("parseRawDoc(%q) error = %v", tt.data, err)
			}
			if len(doc.members) != tt.wantMembers {
				t.Errorf("members = %d, want %d", len(doc.members), tt.wantMembers)
			}
			if (doc.servers != nil) != tt.wantServers {
				t.Errorf("servers captured = %v, want %v", doc.servers != nil, tt.wantServers)
			}
			if tt.name == "null entry parses, invalid verdict lives in Summary" {
				s, ok := doc.Summary("a")
				if !ok {
					t.Fatal("Summary(\"a\") ok = false, want true")
				}
				if !strings.Contains(s.Invalid, "needs command") {
					t.Errorf("Summary.Invalid = %q, want the validate() verdict for a zero entry", s.Invalid)
				}
			}
		})
	}
}

func TestParseRawDocRejects(t *testing.T) {
	// The rejection set must match ParseFile's (json.Unmarshal into File):
	// anything ParseFile would skip, parseRawDoc must refuse too.
	tests := []struct {
		name string
		data string
	}{
		{name: "empty input", data: ``},
		{name: "not json", data: `not json at all`},
		{name: "top-level array", data: `["wrong", "type"]`},
		{name: "top-level string", data: `"mcpServers"`},
		{name: "top-level number", data: `5`},
		{name: "mcpServers is an array", data: `{"mcpServers":[]}`},
		{name: "mcpServers is a string", data: `{"mcpServers":"x"}`},
		{name: "mcpServers is a number", data: `{"mcpServers":5}`},
		{name: "entry is a number", data: `{"mcpServers":{"a":5}}`},
		{name: "entry field mistyped", data: `{"mcpServers":{"a":{"command":5}}}`},
		{name: "entry disabled mistyped", data: `{"mcpServers":{"a":{"command":"c","disabled":"yes"}}}`},
		{name: "truncated", data: `{"mcpServers": {`},
		{name: "trailing garbage", data: `{"mcpServers":{}} garbage`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if doc, err := parseRawDoc([]byte(tt.data)); err == nil {
				t.Errorf("parseRawDoc(%q) = %#v, want an error", tt.data, doc)
			}
		})
	}
}

func TestRawDocSummaryNoExpansion(t *testing.T) {
	t.Setenv("CANARY", "rawdoc-canary-value")
	data := []byte(`{"mcpServers":{"svc":{"command":"run ${CANARY}","args":["--x","${CANARY}"],` +
		`"url":"","env":{"TOKEN":"${CANARY}","ZED":"${CANARY}"},` +
		`"headers":{"Authorization":"Bearer ${CANARY}"}}}}`)
	doc, err := parseRawDoc(data)
	if err != nil {
		t.Fatalf("parseRawDoc() error = %v", err)
	}
	s, ok := doc.Summary("svc")
	if !ok {
		t.Fatal("Summary(\"svc\") ok = false, want true")
	}
	// ${VAR} stays literal everywhere it is exposed.
	if s.Command != "run ${CANARY}" {
		t.Errorf("Command = %q, want the literal placeholder", s.Command)
	}
	// Env/header VALUES are never exposed; KEYS are, sorted, and complete.
	if want := []string{"TOKEN", "ZED"}; !reflect.DeepEqual(s.EnvKeys, want) {
		t.Errorf("EnvKeys = %v, want %v", s.EnvKeys, want)
	}
	if want := []string{"Authorization"}; !reflect.DeepEqual(s.HeaderKeys, want) {
		t.Errorf("HeaderKeys = %v, want %v", s.HeaderKeys, want)
	}
	if strings.Contains(fmt.Sprintf("%+v", s), "rawdoc-canary-value") {
		t.Errorf("Summary leaked the expanded canary: %+v", s)
	}
	if _, ok := doc.Summary("missing"); ok {
		t.Error("Summary(\"missing\") ok = true, want false")
	}
	var nilDoc *rawDoc
	if _, ok := nilDoc.Summary("svc"); ok {
		t.Error("Summary on a nil doc must report false")
	}
}

func TestRawDocSummaryValidationReusesRule(t *testing.T) {
	data := []byte(`{"mcpServers":{` +
		`"stdio":{"command":"x","args":["a","b"]},` +
		`"remote":{"url":"https://h/mcp"},` +
		`"sse":{"url":"https://h/mcp","type":"sse"},` +
		`"badtype":{"url":"https://h/mcp","type":"ftp"},` +
		`"mixed":{"command":"x","url":"https://h/mcp"},` +
		`"empty":{},` +
		`"stub":{"disabled":true},` +
		`"off":{"command":"x","disabled":true}` +
		`}}`)
	doc, err := parseRawDoc(data)
	if err != nil {
		t.Fatalf("parseRawDoc() error = %v", err)
	}

	tests := []struct {
		name        string
		wantKind    string
		wantArgs    int
		wantType    string
		wantDis     bool
		wantInvalid string // substring; "" means the entry must validate
	}{
		{name: "stdio", wantKind: kindStdio, wantArgs: 2, wantType: ""},
		{name: "remote", wantKind: kindRemote, wantType: typeHTTP}, // normalized by validate()
		{name: "sse", wantKind: kindRemote, wantType: typeSSE},
		{name: "badtype", wantKind: kindRemote, wantType: "ftp", wantInvalid: `unknown type "ftp"`},
		{name: "mixed", wantKind: kindStdio, wantInvalid: "mixes stdio and remote fields"},
		{name: "empty", wantKind: kindUnknown, wantInvalid: "needs command (stdio) or url (remote)"},
		{name: "stub", wantKind: kindUnknown, wantDis: true, wantInvalid: "needs command (stdio) or url (remote)"},
		{name: "off", wantKind: kindStdio, wantDis: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := doc.Summary(tt.name)
			if !ok {
				t.Fatalf("Summary(%q) ok = false, want true", tt.name)
			}
			if s.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", s.Kind, tt.wantKind)
			}
			if s.Args != tt.wantArgs {
				t.Errorf("Args = %d, want %d", s.Args, tt.wantArgs)
			}
			if s.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", s.Type, tt.wantType)
			}
			if s.Disabled != tt.wantDis {
				t.Errorf("Disabled = %v, want %v", s.Disabled, tt.wantDis)
			}
			if tt.wantInvalid == "" && s.Invalid != "" {
				t.Errorf("Invalid = %q, want a valid entry", s.Invalid)
			}
			if tt.wantInvalid != "" && !strings.Contains(s.Invalid, tt.wantInvalid) {
				t.Errorf("Invalid = %q, want it to contain %q", s.Invalid, tt.wantInvalid)
			}
		})
	}
}

func TestRawDocDuplicateMembersLastWins(t *testing.T) {
	// Duplicate top-level "mcpServers": encoding/json's map keeps the last
	// occurrence, so Summary must too (both raw spans stay captured).
	data := []byte(`{"mcpServers":{"a":{"command":"first"}},` +
		`"mcpServers":{"a":{"url":"https://second/mcp"}}}`)
	doc, err := parseRawDoc(data)
	if err != nil {
		t.Fatalf("parseRawDoc() error = %v", err)
	}
	s, ok := doc.Summary("a")
	if !ok {
		t.Fatal("Summary(\"a\") ok = false, want true")
	}
	if s.Command != "" || s.URL != "https://second/mcp" {
		t.Errorf("Summary = %+v, want the LAST mcpServers occurrence to win", s)
	}

	// Duplicate entry name inside one object: same rule.
	data = []byte(`{"mcpServers":{"a":{"command":"first"},"a":{"command":"second"}}}`)
	doc, err = parseRawDoc(data)
	if err != nil {
		t.Fatalf("parseRawDoc() error = %v", err)
	}
	if s, _ := doc.Summary("a"); s.Command != "second" {
		t.Errorf("Summary.Command = %q, want \"second\"", s.Command)
	}
	if len(doc.servers.members) != 2 {
		t.Errorf("captured members = %d, want both duplicates preserved for byte-faithful re-emission", len(doc.servers.members))
	}
}

// runtimeEnvelope decodes data exactly the way ParseFile's decode step does
// — json.Unmarshal into File — and returns each name's winning copy (its
// Command) or the decode error. It is the ORACLE the raw reader is compared
// against: whatever this returns, parseRawDoc's name set and winner must
// match for the same bytes.
func runtimeEnvelope(data string) (map[string]string, error) {
	var f File
	if err := json.Unmarshal([]byte(data), &f); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(f.MCPServers))
	for name, srv := range f.MCPServers {
		out[name] = srv.Command
	}
	return out, nil
}

// rawEnvelope is parseRawDoc's verdict for the same bytes: name → winning
// copy's Command (via Summary, i.e. the fold's last-in-file-order winner),
// or the parse error. It also asserts byte-faithfulness of every captured
// fold span while it walks the members.
func rawEnvelope(t *testing.T, data string) (map[string]string, error) {
	t.Helper()
	doc, err := parseRawDoc([]byte(data))
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	if doc.servers == nil {
		return out, nil
	}
	for i, m := range doc.servers.members {
		if doc.servers.index[m.key] != i {
			continue // not the winning copy of this name
		}
		if m.start < 0 || m.end > len(data) || !bytes.Equal([]byte(data[m.start:m.end]), m.raw) {
			t.Errorf("entry %q span [%d,%d] does not match document bytes", m.key, m.start, m.end)
		}
		s, ok := doc.Summary(m.key)
		if !ok {
			t.Errorf("Summary(%q) ok = false for a fold member", m.key)
			continue
		}
		out[m.key] = s.Command
	}
	return out, nil
}

// TestRawDocEnvelopeMatchesRuntime pins the raw reader's NAME SET (and the
// winning copy per name) to the runtime decoder's for the same bytes, over
// the document classes encoding/json accepts and honours but a naive reader
// diverges on: duplicate envelope members (union vs replace), null first /
// null second (clear semantics), case-variant envelope keys (case-insensitive
// struct-field matching) and a case-variant duplicate. Error verdicts are
// compared too: a document ParseFile rejects, parseRawDoc must reject.
func TestRawDocEnvelopeMatchesRuntime(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "duplicate envelope with disjoint names unions",
			data: `{"mcpServers":{"a":{"command":"x"}},"mcpServers":{"b":{"command":"y"}}}`,
		},
		{
			name: "duplicate envelope with overlapping names last wins",
			data: `{"mcpServers":{"a":{"command":"first"}},"mcpServers":{"a":{"command":"second"}}}`,
		},
		{
			name: "null first",
			data: `{"mcpServers":null,"mcpServers":{"a":{"command":"x"}}}`,
		},
		{
			name: "null second clears",
			data: `{"mcpServers":{"a":{"command":"x"}},"mcpServers":null}`,
		},
		{
			name: "case variant MCPServers",
			data: `{"MCPServers":{"c":{"command":"x"}}}`,
		},
		{
			name: "case variant mcpservers",
			data: `{"mcpservers":{"c":{"command":"x"}}}`,
		},
		{
			name: "case-variant duplicate unions disjoint names",
			data: `{"mcpServers":{"a":{"command":"x"}},"MCPServers":{"b":{"command":"y"}}}`,
		},
		{
			name: "case-variant duplicate overlapping names last wins",
			data: `{"mcpServers":{"a":{"command":"1"}},"MCPServers":{"a":{"command":"2"}}}`,
		},
		{
			name: "case-variant null clears",
			data: `{"mcpServers":{"a":{"command":"x"}},"MCPServers":null}`,
		},
		{
			name: "null then case variant",
			data: `{"mcpServers":null,"MCPServers":{"a":{"command":"x"}}}`,
		},
		{
			name: "empty object after object does not clear",
			data: `{"mcpServers":{"a":{"command":"x"}},"mcpServers":{}}`,
		},
		{
			name: "mistyped case-variant occurrence fails the document",
			data: `{"MCPServers":{"a":5}}`,
		},
		{
			name: "mistyped exact occurrence fails the document",
			data: `{"mcpServers":{"a":{"command":"x"}},"mcpServers":"str"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, wantErr := runtimeEnvelope(tt.data)
			got, gotErr := rawEnvelope(t, tt.data)

			if (wantErr != nil) != (gotErr != nil) {
				t.Errorf("runtime err = %v, parseRawDoc err = %v; both verdicts must agree", wantErr, gotErr)
				return
			}
			if wantErr != nil {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("parseRawDoc names/winners = %v, runtime (json.Unmarshal into File) = %v", got, want)
			}
		})
	}
}
