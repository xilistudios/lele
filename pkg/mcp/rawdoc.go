package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// This file is the expansion-free twin of config.go: it decodes the same
// mcp.json bytes WITHOUT running ${VAR} expansion, so everything it exposes
// is byte-for-byte what sits on disk. That is what makes a listing safe
// (secrets stay in literal "${VAR}" form) and what allows the writer (T2) to
// splice entries without re-encoding — and therefore without destroying the
// user's formatting, member order or unknown keys.
//
// The envelope rules mirror ParseFile exactly: a document is accepted iff
// json.Unmarshal into File would accept it (top-level object or null,
// "mcpServers" an object, absent or null, every entry decodable into
// ServerConfig). A document ParseFile rejects, parseRawDoc rejects, so a
// layer Discover skips is a layer ReadInventory never enumerates. Per-entry
// VALIDATION is deliberately not done here: it runs in (*rawDoc).Summary
// via the existing (*ServerConfig).validate — one rule, zero duplication.

// rawMember is one member of a JSON object, captured verbatim from the
// source document instead of being re-encoded.
type rawMember struct {
	key   string // decoded member name
	raw   []byte // exact value bytes as they appear in the file
	start int    // byte offset of raw[0] within the whole document
	end   int    // byte offset just past raw[len(raw)-1]
}

// rawObject is a JSON object decoded in file order without re-encoding.
// Lookups are last-wins, matching encoding/json's map semantics for duplicate
// member names, while members keeps every occurrence for byte-faithful
// re-emission.
type rawObject struct {
	members []rawMember    // file order
	index   map[string]int // key → position of the LAST occurrence
}

// add appends a member and points the lookup index at it, so a duplicate key
// resolves to the last occurrence (what encoding/json's map would hold).
func (o *rawObject) add(m rawMember) {
	if o.index == nil {
		o.index = make(map[string]int)
	}
	o.index[m.key] = len(o.members)
	o.members = append(o.members, m)
}

// get returns the member that wins for key (the last one written).
func (o *rawObject) get(key string) (rawMember, bool) {
	if o == nil || o.index == nil {
		return rawMember{}, false
	}
	i, ok := o.index[key]
	if !ok {
		return rawMember{}, false
	}
	return o.members[i], true
}

// rawDoc is one parsed mcp.json file: the document bytes, the top-level
// members in file order, and the "mcpServers" object (nil when the member is
// absent or null, mirroring json null clearing a map).
type rawDoc struct {
	data    []byte
	members []rawMember // top-level, file order
	servers *rawObject
}

// parseRawDoc decodes the raw bytes of one mcp.json without expansion.
//
// Accepts a JSON object or null at the top level, "mcpServers" an object,
// absent or null, and every entry decodable into ServerConfig — the exact
// set of documents ParseFile accepts. Anything else is an error, so callers
// that skip a failed parse reproduce Discover's "broken layer is skipped"
// behaviour. Trailing data after the top-level value is rejected the way
// json.Unmarshal rejects it.
func parseRawDoc(data []byte) (*rawDoc, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	first, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("parse raw mcp config: %w", err)
	}
	doc := &rawDoc{data: data}
	if first == nil {
		// Top-level null: json.Unmarshal into a struct accepts it as an
		// empty document, so we do too.
		if err := expectEOF(dec); err != nil {
			return nil, fmt.Errorf("parse raw mcp config: %w", err)
		}
		return doc, nil
	}
	if d, ok := first.(json.Delim); !ok || d != '{' {
		return nil, errors.New("parse raw mcp config: top-level value is not an object")
	}
	for dec.More() {
		m, err := nextMember(dec, 0)
		if err != nil {
			return nil, fmt.Errorf("parse raw mcp config: %w", err)
		}
		doc.members = append(doc.members, m)
	}
	if err := closeObject(dec); err != nil {
		return nil, fmt.Errorf("parse raw mcp config: %w", err)
	}
	if err := expectEOF(dec); err != nil {
		return nil, fmt.Errorf("parse raw mcp config: %w", err)
	}
	// Decode every "mcpServers" occurrence: json.Unmarshal errors if ANY of
	// them is mistyped, and the last occurrence wins (duplicate-key map
	// semantics, including "null" clearing what an earlier one set).
	for _, m := range doc.members {
		if m.key != "mcpServers" {
			continue
		}
		servers, err := parseObjectAt(data, m.start, m.raw)
		if err != nil {
			return nil, fmt.Errorf("parse raw mcp config: %w", err)
		}
		if err := gateEntries(servers); err != nil {
			return nil, err
		}
		doc.servers = servers
	}
	return doc, nil
}

// gateEntries decodes every captured entry into ServerConfig without
// expansion — the same structural verdict json.Unmarshal reaches in
// ParseFile. A mistyped entry fails the WHOLE document there (one
// Unmarshal over the file), so it must fail the whole document here too.
// Per-entry validation is NOT part of the gate: it runs in Summary.
func gateEntries(o *rawObject) error {
	if o == nil {
		return nil
	}
	for _, m := range o.members {
		var sc ServerConfig
		if err := json.Unmarshal(m.raw, &sc); err != nil {
			return fmt.Errorf("parse raw mcp config: mcpServers %q: %w", m.key, err)
		}
	}
	return nil
}

// parseObjectAt decodes the object held in data[start:end] (raw), returning
// its members with offsets absolute to data. null yields a nil object, any
// other non-object is an error — ParseFile's verdict for a mistyped
// "mcpServers".
func parseObjectAt(data []byte, start int, raw []byte) (*rawObject, error) {
	if !bytes.Equal(data[start:start+len(raw)], raw) {
		// Defensive: the captured span must be the document's own bytes.
		// If this ever fires, the offset bookkeeping is wrong and byte
		// splicing would corrupt user files.
		return nil, errors.New("parse raw mcp config: captured span does not match document")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	first, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if first == nil {
		return nil, expectEOF(dec) // null → no object (map cleared)
	}
	d, ok := first.(json.Delim)
	if !ok || d != '{' {
		return nil, errors.New("mcpServers value is not a JSON object")
	}
	obj := &rawObject{}
	for dec.More() {
		m, err := nextMember(dec, start)
		if err != nil {
			return nil, err
		}
		obj.add(m)
	}
	if err := closeObject(dec); err != nil {
		return nil, err
	}
	return obj, expectEOF(dec)
}

// nextMember reads one "name": value pair from dec, capturing the value's
// exact bytes. off shifts the relative decoder offsets into absolute
// document offsets (0 for the top level, the member's start for a nested
// object).
func nextMember(dec *json.Decoder, off int) (rawMember, error) {
	tok, err := dec.Token()
	if err != nil {
		return rawMember{}, err
	}
	key, ok := tok.(string)
	if !ok {
		return rawMember{}, errors.New("expected a member name")
	}
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return rawMember{}, err
	}
	end := off + int(dec.InputOffset())
	start := end - len(raw)
	if start < off {
		return rawMember{}, errors.New("captured value overruns its document")
	}
	return rawMember{key: key, raw: raw, start: start, end: end}, nil
}

// closeObject consumes the closing brace of the object being read.
func closeObject(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '}' {
		return errors.New("expected the end of a JSON object")
	}
	return nil
}

// expectEOF asserts that nothing but whitespace follows the value just read,
// mirroring json.Unmarshal's rejection of trailing data.
func expectEOF(dec *json.Decoder) error {
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected data after the top-level value")
		}
		return err
	}
	return nil
}

// Summary reports the display-safe summary of one server entry: it decodes
// the captured bytes into ServerConfig WITHOUT expansion and runs the
// existing (*ServerConfig).validate, so the verdict is exactly ParseFile's
// while ${VAR} references stay literal. Env and header VALUES never leave
// this package: only their key names are exposed, never the values.
func (d *rawDoc) Summary(name string) (ServerSummary, bool) {
	if d == nil || d.servers == nil {
		return ServerSummary{}, false
	}
	m, ok := d.servers.get(name)
	if !ok {
		return ServerSummary{}, false
	}
	var sc ServerConfig
	if err := json.Unmarshal(m.raw, &sc); err != nil {
		// Unreachable: parseRawDoc already gated every entry into
		// ServerConfig. Keep the branch so a future change degrades to an
		// reported-invalid entry instead of a half-decoded one.
		return ServerSummary{Name: name, Invalid: err.Error()}, true
	}
	// One rule for validation: the existing method, never a second one.
	// It also normalizes Type ("": → "http") on remote entries, exactly as
	// ParseFile reports it after expansion (Type is never expanded).
	verdict := sc.validate()

	s := ServerSummary{
		Name:        name,
		Command:     sc.Command, // raw: ${VAR} stays literal
		URL:         sc.URL,     // raw: ${VAR} stays literal
		Type:        sc.Type,
		Description: sc.Description,
		Args:        len(sc.Args),
		Disabled:    sc.Disabled,
		EnvKeys:     keyNames(sc.Env),     // KEYS only, never values
		HeaderKeys:  keyNames(sc.Headers), // KEYS only, never values
	}
	switch {
	case sc.Command != "":
		s.Kind = kindStdio
	case sc.URL != "":
		s.Kind = kindRemote
	default:
		s.Kind = kindUnknown
	}
	if verdict != nil {
		s.Invalid = verdict.Error()
	}
	return s, true
}

// keyNames returns the sorted key names of an env/header map. Values are
// deliberately never read: a listing may show WHICH variables an entry sets,
// never what they contain (they can hold expanded secrets).
func keyNames(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
