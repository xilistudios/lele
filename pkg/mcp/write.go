package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file is the T2 writer: surgical, secret-safe edits of an mcp.json on
// disk. Every mutation is a byte splice over the spans captured by
// parseRawDoc (rawdoc.go) — the file is never re-serialised, so top-level
// member order, unknown keys, inner whitespace and every untouched entry
// survive byte-for-byte. ${VAR} references are never expanded and neither
// file contents nor env/header values are ever logged: errors mention the
// path and the parse verdict only.

// ToggleResult reports what the write actually did, so callers can explain a
// no-op instead of pretending something changed.
type ToggleResult struct {
	Path, Name string
	Enabled    bool // requested state (true = enable, false = disable)
	Changed    bool // bytes on disk changed
	Removed    bool // entry deleted (enable on a pure stub)
	Created    bool // entry created (disable on a missing entry / file)
}

// edit is one byte-span replacement within a document: [start, end) is
// removed and repl spliced in its place.
type edit struct {
	start, end int
	repl       []byte
}

// SetDisabled sets or clears the "disabled" flag of one server entry in the
// raw bytes of one mcp.json, preserving everything else.
//
// Disable (disabled=true): sets "disabled": true on the entry of that file;
// if the entry does not exist in the file it is created as the stub
// {"disabled": true} (the "disable only in this workspace" primitive), and a
// missing file (or missing "mcpServers" object) is created too (parent dirs
// included). Enable (disabled=false): DELETES the "disabled" key — never
// writes "disabled": false — and, when deleting the key leaves a pure stub
// (no command, no url), deletes the whole entry so the lower layer wins
// again (EVIDENCE B1: a stub that stays behind keeps shadowing the lower
// layer while the UI claims "enabled"). Enable on a missing file or missing
// entry is a no-op reported as Changed=false; the file is not created.
//
// The write is atomic and 0600 (see SaveRawFile). fileContent carries
// secrets only in literal ${VAR} form: this function never expands and
// never logs. name must be non-empty and must not contain '/' (the HTTP
// layer guards the same shapes).
func SetDisabled(path, name string, disabled bool) (ToggleResult, error) {
	res := ToggleResult{Path: path, Name: name, Enabled: !disabled}
	if err := validEntryName(name); err != nil {
		return res, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return res, fmt.Errorf("mcp: read %s: %w", path, err)
		}
		if !disabled {
			// Enable on a missing file: nothing to enable, and creating an
			// empty file would be a lie (plan Q3).
			return res, nil
		}
		content := stubDocument(name)
		// Same invariant as the splice path below: bytes that would not
		// parse must never reach the disk.
		if _, err := parseRawDoc(content); err != nil {
			return res, fmt.Errorf("mcp: internal stub for %q is invalid: %w", name, err)
		}
		if err := SaveRawFile(path, content); err != nil {
			return res, err
		}
		res.Created = true
		res.Changed = true
		return res, nil
	}

	doc, err := parseRawDoc(data)
	if err != nil {
		return res, fmt.Errorf("mcp: parse %s: %w", path, err)
	}

	var edits []edit
	if disabled {
		edits, res.Created, err = planDisable(doc, name)
	} else {
		edits, res.Removed, err = planEnable(doc, name)
	}
	if err != nil {
		return res, fmt.Errorf("mcp: edit %s: %w", path, err)
	}
	if len(edits) == 0 {
		return res, nil // idempotent no-op: bytes untouched
	}

	next := applyEdits(data, edits)
	// Defensive: a splice bug must never reach the disk. parseRawDoc's
	// accept set is ParseFile's, so this round-trip proves the edited
	// document would still be loaded by the reader.
	if _, err := parseRawDoc(next); err != nil {
		return res, fmt.Errorf("mcp: edit %s would corrupt the document: %w", path, err)
	}
	if bytes.Equal(next, data) {
		return res, nil
	}
	if err := SaveRawFile(path, next); err != nil {
		return res, err
	}
	res.Changed = true
	return res, nil
}

// SaveRawFile writes an editor payload verbatim (validated by the caller)
// using the same atomic recipe as config.SaveConfig: temp file in the SAME
// directory → Write → Sync → Chmod 0600 → Close → os.Rename, with the temp
// removed on any failure, so a crash never leaves a truncated mcp.json.
// Parent directories are created. 0600 because entries hold API keys and
// Authorization headers. A symlinked mcp.json is replaced by a regular file
// (same as config.SaveConfig; documented, not special-cased).
func SaveRawFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mcp: create directory for %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".mcp-*.tmp")
	if err != nil {
		return fmt.Errorf("mcp: create temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("mcp: write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("mcp: sync %s: %w", path, err)
	}
	// CreateTemp already uses 0600 on Unix; be explicit for portability.
	// Must happen before Close: fchmod on a closed fd fails on Linux.
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("mcp: chmod %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mcp: close %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("mcp: replace %s: %w", path, err)
	}
	committed = true
	return nil
}

// ValidateRawJSON is the envelope/per-entry check used by the raw editor and
// by the PUT handler: err != nil only for a broken envelope (not JSON,
// "mcpServers" not an object, an entry that will not decode — the exact set
// parseRawDoc rejects, i.e. what ParseFile rejects). Per-entry VALIDATION
// problems come back as warnings because invalid entries are non-fatal by
// design (config.go validate) and the editor is the only way to fix them.
// Warnings are *ServerError values, so each one names its entry.
func ValidateRawJSON(content []byte) (warnings []error, err error) {
	doc, err := parseRawDoc(content)
	if err != nil {
		return nil, err
	}
	if doc.servers == nil {
		return nil, nil
	}
	seen := make(map[string]bool, len(doc.servers.members))
	for _, m := range doc.servers.members {
		if seen[m.key] {
			continue // one warning per name, verdict of the winning entry
		}
		seen[m.key] = true
		s, ok := doc.Summary(m.key)
		if !ok || s.Invalid == "" {
			continue
		}
		warnings = append(warnings, &ServerError{Server: m.key, Err: errors.New(s.Invalid)})
	}
	return warnings, nil
}

// validEntryName rejects names the writer must never act on: empty and
// path-shaped. '/' is what makes a name a path; the HTTP layer returns 400
// for the same shapes before reaching this function.
func validEntryName(name string) error {
	if name == "" {
		return errors.New("mcp: server name must not be empty")
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("mcp: server name %q must not contain '/'", name)
	}
	return nil
}

// stubDocument is the exact bytes written for a fresh disable-only file:
// {"mcpServers":{"<name>":{"disabled":true}}}.
func stubDocument(name string) []byte {
	return []byte(`{"mcpServers":{` + mustJSONKey(name) + `:{"disabled":true}}}`)
}

// mustJSONKey JSON-encodes an object member name. json.Marshal cannot fail
// for a string; it escapes only control characters, quotes and (as \u003c
// etc.) HTML bytes, all of which decode back to the exact name.
func mustJSONKey(name string) string {
	b, _ := json.Marshal(name)
	return string(b)
}

// lastMember returns the LAST top-level occurrence of key (map semantics:
// the last duplicate wins) so a mutation never targets a shadowed copy.
func lastMember(members []rawMember, key string) (rawMember, bool) {
	for i := len(members) - 1; i >= 0; i-- {
		if members[i].key == key {
			return members[i], true
		}
	}
	return rawMember{}, false
}

// planDisable computes the splice(s) that disable one entry, returning the
// edits and whether the ENTRY was created (name absent from mcpServers).
// nil edits mean "already disabled" — the file must not be rewritten.
func planDisable(doc *rawDoc, name string) ([]edit, bool, error) {
	sm, hasServers := lastMember(doc.members, "mcpServers")

	if doc.servers == nil {
		key := mustJSONKey(name)
		body := `{` + key + `:{"disabled":true}}`
		switch {
		case hasServers:
			// "mcpServers": null → materialise the object in place; the
			// other top-level members stay byte-identical.
			return []edit{{sm.start, sm.end, []byte(body)}}, true, nil
		case len(doc.members) > 0:
			// Append after the last top-level member: order preserved, no
			// existing byte touched.
			pos := doc.members[len(doc.members)-1].end
			repl := []byte(", \"mcpServers\": " + body)
			return []edit{{pos, pos, repl}}, true, nil
		default:
			// No top-level members: an empty object "{}" gets the member
			// inserted before its closing brace; a top-level "null"
			// document (accepted by ParseFile as an empty file) is
			// replaced wholesale - it has nothing to preserve.
			trimmed := bytes.TrimLeft(doc.data, " \t\r\n")
			if len(trimmed) > 0 && trimmed[0] == '{' {
				j := len(doc.data) - len(trimmed) + 1
				for j < len(doc.data) && isSpaceJSON(doc.data[j]) {
					j++
				}
				// Members are absent, so the closing brace follows the
				// opening one (nothing nested can intervene).
				if j >= len(doc.data) || doc.data[j] != '}' {
					return nil, false, errors.New("empty top-level object does not close after '{'")
				}
				repl := []byte(`"mcpServers": ` + body)
				return []edit{{j, j, repl}}, true, nil
			}
			return []edit{{0, len(doc.data), stubDocument(name)}}, true, nil
		}
	}

	m, ok := doc.servers.get(name) // last occurrence: json map semantics
	if !ok {
		// Create the entry inside the existing mcpServers object.
		closePos := sm.end - 1
		if closePos < sm.start || doc.data[closePos] != '}' {
			return nil, false, errors.New("captured mcpServers object does not end with '}'")
		}
		key := mustJSONKey(name)
		body := key + `: {"disabled":true}`
		if n := len(doc.servers.members); n > 0 {
			pos := doc.servers.members[n-1].end
			return []edit{{pos, pos, []byte(`, ` + body)}}, true, nil
		}
		return []edit{{closePos, closePos, []byte(body)}}, true, nil
	}

	if m.raw[0] != '{' {
		// A null entry parses as a zero ServerConfig (json semantics): it
		// can hold no flag, so materialise it as a stub.
		return []edit{{m.start, m.end, []byte(`{"disabled":true}`)}}, false, nil
	}
	entryObj, err := parseObjectAt(m.raw, 0, m.raw)
	if err != nil {
		return nil, false, err // unreachable: parseRawDoc gated this entry
	}
	if dm, has := entryObj.get("disabled"); has {
		if bytes.Equal(dm.raw, []byte("true")) {
			return nil, false, nil // already disabled: no write at all
		}
		// "disabled": false (or null, which leaves the bool at zero) →
		// flip just the value bytes.
		off := m.start
		return []edit{{off + dm.start, off + dm.end, []byte(`true`)}}, false, nil
	}
	// Insert the flag after the entry's last member so every existing byte
	// of the entry stays put.
	if n := len(entryObj.members); n > 0 {
		pos := m.start + entryObj.members[n-1].end
		return []edit{{pos, pos, []byte(`, "disabled": true`)}}, false, nil
	}
	pos := m.start + len(m.raw) - 1 // empty entry: before its closing brace
	return []edit{{pos, pos, []byte(`"disabled": true`)}}, false, nil
}

// planEnable computes the splice(s) that enable one entry: the "disabled"
// KEY is deleted (never set to false), and when that leaves a pure stub the
// whole ENTRY is deleted so the lower layer wins again (EVIDENCE B1).
// nil edits mean "nothing to enable" — the file must not be rewritten.
func planEnable(doc *rawDoc, name string) ([]edit, bool, error) {
	if doc.servers == nil {
		return nil, false, nil
	}
	sm, _ := lastMember(doc.members, "mcpServers")
	m, ok := doc.servers.get(name)
	if !ok || m.raw[0] != '{' {
		// Missing entry, or a null entry: no "disabled" key to delete.
		return nil, false, nil
	}
	entryObj, err := parseObjectAt(m.raw, 0, m.raw)
	if err != nil {
		return nil, false, err // unreachable: parseRawDoc gated this entry
	}
	hasDisabled := false
	for _, em := range entryObj.members {
		if em.key == "disabled" {
			hasDisabled = true
			break
		}
	}
	if !hasDisabled {
		return nil, false, nil // already enabled (an invalid entry stays the caller's problem)
	}

	// Delete EVERY "disabled" member, one at a time with a re-parse in
	// between: a stale span could overlap the next one (duplicate keys like
	// {"disabled":true,"disabled":false}).
	entry := append([]byte(nil), m.raw...)
	for {
		obj, err := parseObjectAt(entry, 0, entry)
		if err != nil {
			return nil, false, err // unreachable: derived from gated bytes
		}
		idx := -1
		for i, em := range obj.members {
			if em.key == "disabled" {
				idx = i
			}
		}
		if idx < 0 {
			break
		}
		span, err := deleteMemberSpan(entry, obj.members, idx, 1, len(entry)-1)
		if err != nil {
			return nil, false, err
		}
		entry = append(entry[:span.start], entry[span.end:]...)
	}

	// Pure stub check on the POST-deletion bytes: no command and no url
	// means the entry was never a real definition — deleting the key alone
	// would leave {} (or {"url":""}), still invalid, still shadowing.
	var sc ServerConfig
	if err := json.Unmarshal(entry, &sc); err != nil {
		return nil, false, fmt.Errorf("entry %q after removing \"disabled\": %w", name, err) // unreachable
	}
	if sc.Command == "" && sc.URL == "" {
		closePos := sm.end - 1
		if closePos < sm.start || doc.data[closePos] != '}' {
			return nil, false, errors.New("captured mcpServers object does not end with '}'")
		}
		span, err := deleteMemberSpan(doc.data, doc.servers.members, indexOfMember(doc.servers.members, m), sm.start+1, closePos)
		if err != nil {
			return nil, false, err
		}
		return []edit{span}, true, nil
	}
	return []edit{{m.start, m.end, entry}}, false, nil
}

// indexOfMember locates the target member slice position (identity match on
// the captured span, since get() already selected the winning occurrence).
func indexOfMember(members []rawMember, target rawMember) int {
	for i, m := range members {
		if m.start == target.start && m.end == target.end && m.key == target.key {
			return i
		}
	}
	return -1
}

// deleteMemberSpan returns the span of data that removes members[idx] — its
// key, its value and the adjacent comma — leaving the object valid:
//   - not the last member: up to the NEXT member's key (the comma and the
//     next key's indentation are reused, so the neighbour's bytes survive);
//   - last member: back to the previous member's value end (this absorbs
//     the preceding comma) or to just after '{' when it is the only member,
//     through to the closing brace.
//
// bound is the index just after the object's opening '{' (absolute for a
// document span, 1 for a value parsed standalone); closePos is the index of
// the object's closing '}' (sm.end-1 for an absolute span, len(data)-1 for a
// standalone value).
func deleteMemberSpan(data []byte, members []rawMember, idx, bound, closePos int) (edit, error) {
	if idx < 0 || idx >= len(members) {
		return edit{}, fmt.Errorf("member index %d out of range", idx)
	}
	if closePos >= len(data) || data[closePos] != '}' {
		return edit{}, errors.New("object does not close at the recorded position")
	}
	cp := closePos
	ks, ok := memberKeyStart(data, members[idx].start, bound)
	if !ok {
		return edit{}, errors.New("cannot locate the member's key")
	}
	if idx < len(members)-1 {
		nks, ok := memberKeyStart(data, members[idx+1].start, bound)
		if !ok {
			return edit{}, errors.New("cannot locate the next member's key")
		}
		return edit{ks, nks, nil}, nil
	}
	if idx > 0 {
		return edit{members[idx-1].end, cp, nil}, nil
	}
	return edit{bound, cp, nil}, nil
}

// memberKeyStart walks BACKWARD from a member's value start (documented by
// parseRawDoc to sit after ":") to the opening quote of its key: skip
// whitespace, expect ':', skip whitespace, expect the key's closing quote,
// then scan down for a quote not preceded by a backslash run of odd length
// (escaped quotes belong to the key). bound is the lowest legal index (the
// object's first byte after '{'). Returns false when the scan leaves the
// object without finding a key — on bytes parseRawDoc accepted this is
// unreachable; the writer treats it as "abort, never write".
func memberKeyStart(data []byte, valueStart, bound int) (int, bool) {
	i := valueStart - 1
	for i >= bound && isSpaceJSON(data[i]) {
		i--
	}
	if i < bound || data[i] != ':' {
		return 0, false
	}
	for i--; i >= bound && isSpaceJSON(data[i]); i-- {
	}
	if i < bound || data[i] != '"' {
		return 0, false
	}
	for j := i - 1; j >= bound; j-- {
		if data[j] != '"' {
			continue
		}
		k, bs := j-1, 0
		for k >= bound && data[k] == '\\' {
			bs++
			k--
		}
		if bs%2 == 0 {
			return j, true
		}
	}
	return 0, false
}

// isSpaceJSON reports the whitespace json.Decoder skips between tokens.
func isSpaceJSON(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// applyEdits returns data with every edit spliced in (edits sorted by
// position, applied back to front so earlier offsets stay valid).
func applyEdits(data []byte, edits []edit) []byte {
	if len(edits) == 0 {
		return data
	}
	sorted := append([]edit(nil), edits...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].start < sorted[j].start })
	out := append([]byte(nil), data...)
	for i := len(sorted) - 1; i >= 0; i-- {
		e := sorted[i]
		out = append(out[:e.start], append(e.repl, out[e.end:]...)...)
	}
	return out
}
