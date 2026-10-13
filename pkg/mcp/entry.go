package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// This file is the entry-level half of the T2 writer. SetDisabled (write.go)
// flips ONE flag inside an entry that already exists; the two functions here
// ADD, REPLACE and DELETE whole "mcpServers" entries, which is what the
// `lele mcp add|remove|edit` commands need. Both halves share the same
// machinery: every mutation is a byte splice over the spans parseRawDoc
// (rawdoc.go) captured, so top-level member order, unknown keys, inner
// whitespace and every untouched entry survive byte-for-byte — and the write
// itself goes through SaveRawFile (temp file + fsync + chmod 0600 + rename).
//
// ${VAR} references are NEVER expanded on write and env/header VALUES are
// never logged: an entry written from a CLI travels to the disk exactly as it
// arrived, and the reader (ParseFile) is the only place expansion happens.

// UpsertEntryResult reports what UpsertEntry changed.
type UpsertEntryResult struct {
	Path string
	Name string
	// Created is true when the file, the "mcpServers" object or the entry
	// itself had to be created (any of the three).
	Created bool
	// Replaced is true when an existing entry was overwritten by different
	// bytes. It implies Changed: a call whose entry already sits on disk
	// byte-identically is a pure no-op (all three false).
	Replaced bool
	// Changed is true when bytes were written to the file.
	Changed bool
}

// DeleteEntryResult reports what DeleteEntry changed.
type DeleteEntryResult struct {
	Path string
	Name string
	// Deleted is true when an entry was found and removed. It implies
	// Changed.
	Deleted bool
	// Changed is true when bytes were written to the file.
	Changed bool
}

// UpsertEntry inserts or replaces the server entry `name` in the
// "mcpServers" object of the JSON file at path, splicing bytes so ALL other
// content keeps its exact original bytes (order, unknown keys, whitespace,
// ${VAR} references). It creates the file (parent directories included, mode
// 0600) and/or the "mcpServers" object when missing, using the same envelope
// shapes as SetDisabled (write.go): a "mcpServers": null member is
// materialised in place, an absent member is appended after the last
// top-level member, an empty object or a top-level null document is
// rebuilt — see planEnvelope for the exact bytes and the one caveat (a
// winning null envelope un-clears names an earlier occurrence contributed).
//
// Validation happens BEFORE anything is written:
//   - name must be non-empty and must not contain '/' (validEntryName, the
//     same rule SetDisabled enforces);
//   - the entry must pass (*ServerConfig).validate (command XOR url, type ∈
//     {"", "http", "sse"}); validate is run on a COPY, so the Type it
//     normalizes to "http" for remote entries never leaks into the file —
//     an omitted "type" is read back as "http" by ParseFile, which keeps the
//     written object minimal.
//
// A refused call (bad name, invalid entry, malformed document) returns an
// error and leaves the file byte-identical. A call whose entry serializes to
// the bytes already on disk returns the zero result without rewriting
// anything.
//
// Duplicate names follow SetDisabled's semantics exactly (write_test.go T10):
// only the WINNING occurrence — the last one in file order, the copy
// encoding/json's map holds — is replaced or, for a new name, the entry is
// appended to the object; earlier copies stay verbatim.
//
// The entry is serialized as a canonical COMPACT single-line object
// containing only non-zero fields, in this order: command, args, env, url,
// type, headers, description, and disabled only when it is true (a written
// entry is enabled unless the caller asks otherwise). That is the shape
// planDisable's inserted stub uses, so both writers produce the same style.
// env/header keys are sorted for deterministic output; their values, like
// every other string, are emitted as JSON string literals with HTML escaping
// turned off, i.e. raw '&', '<', '>' and (crucially) literal "${VAR}" and
// "$$" bytes.
func UpsertEntry(path, name string, entry ServerConfig) (UpsertEntryResult, error) {
	res := UpsertEntryResult{Path: path, Name: name}
	if err := validEntryName(name); err != nil {
		return res, err
	}
	// The reader's own validation, on a copy that is discarded: the type
	// normalization it performs (a remote entry without an explicit type
	// becomes "http") stays a reader-side default and is never written as if
	// the caller had asked for it. One rule, zero duplication.
	checked := entry
	if err := checked.validate(); err != nil {
		return res, fmt.Errorf("mcp: entry %q: %w", name, err)
	}
	serialized := marshalEntry(entry)

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return res, fmt.Errorf("mcp: read %s: %w", path, err)
		}
		content := singleEntryDocument(name, serialized)
		// Same invariant as the splice path below: bytes that would not
		// parse must never reach the disk.
		if _, err := parseRawDoc(content); err != nil {
			return res, fmt.Errorf("mcp: internal entry document for %q is invalid: %w", name, err)
		}
		if err := SaveRawFile(path, content); err != nil {
			return res, err
		}
		res.Created, res.Changed = true, true
		return res, nil
	}

	doc, err := parseRawDoc(data)
	if err != nil {
		return res, fmt.Errorf("mcp: parse %s: %w", path, err)
	}
	edits, created, replaced, err := planUpsertEntry(doc, name, serialized)
	if err != nil {
		return res, fmt.Errorf("mcp: edit %s: %w", path, err)
	}
	if len(edits) == 0 {
		return res, nil // byte-identical: idempotent no-op, file untouched
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
	res.Created, res.Replaced, res.Changed = created, replaced, true
	return res, nil
}

// DeleteEntry removes the server entry `name` from the "mcpServers" object of
// the file at path, splicing bytes: every other member keeps its exact bytes
// and the adjacent comma/whitespace is cleaned up by deleteMemberSpan, so the
// result is still the same valid JSON document.
//
// Missing file, missing "mcpServers" object and missing entry are all no-ops:
// the zero result and a nil error, with nothing written (mirroring enable on
// a missing entry, write_test.go T4/T9).
//
// Duplicate names follow SetDisabled's semantics (write_test.go T10): only
// the WINNING occurrence is deleted, earlier copies stay verbatim — so a
// document that lists the same name twice still resolves that name after the
// call (to the earlier copy). Callers that must remove every copy have to run
// the command again.
//
// name must be non-empty and must not contain '/' (validEntryName). A
// malformed document or an unrepresentable splice is an error and leaves the
// file untouched.
func DeleteEntry(path, name string) (DeleteEntryResult, error) {
	res := DeleteEntryResult{Path: path, Name: name}
	if err := validEntryName(name); err != nil {
		return res, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return res, fmt.Errorf("mcp: read %s: %w", path, err)
		}
		return res, nil // nothing to delete, nothing to create
	}

	doc, err := parseRawDoc(data)
	if err != nil {
		return res, fmt.Errorf("mcp: parse %s: %w", path, err)
	}
	if doc.servers == nil {
		return res, nil // no envelope object, hence no entry
	}
	m, ok := doc.servers.get(name) // last occurrence: json map semantics
	if !ok {
		return res, nil
	}
	span, err := planDeleteEntry(doc, m)
	if err != nil {
		return res, fmt.Errorf("mcp: edit %s: %w", path, err)
	}

	next := applyEdits(data, []edit{span})
	// Defensive: a splice bug must never reach the disk (see UpsertEntry).
	if _, err := parseRawDoc(next); err != nil {
		return res, fmt.Errorf("mcp: edit %s would corrupt the document: %w", path, err)
	}
	if bytes.Equal(next, data) {
		return res, nil
	}
	if err := SaveRawFile(path, next); err != nil {
		return res, err
	}
	res.Deleted, res.Changed = true, true
	return res, nil
}

// EntryRaw returns the raw JSON bytes of the server entry `name` as stored in
// the "mcpServers" object of the file at path (the winning occurrence when the
// name is duplicated, matching UpsertEntry/DeleteEntry/SetDisabled semantics:
// the last copy in file order). Values are returned exactly as stored — NO
// ${VAR} expansion is ever applied — which is what `lele mcp edit` needs: it
// hands the user the object that is on disk, not a decoded/expanded one.
//
// found=false with a nil error when the file or the entry does not exist
// (missing file, absent "mcpServers" object, or a name the folded envelope
// never mentions), or when the envelope is a null occurrence that cleared an
// earlier one. A malformed document is an error, in the same shape the other
// entry operations return, so a caller can distinguish "nothing to edit" from
// "cannot read this file".
//
// The returned bytes are a fresh slice: the value spans captured by
// parseRawDoc are decoded copies, never aliases of the file contents, so a
// caller may keep or mutate them freely.
func EntryRaw(path, name string) (raw []byte, found bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, false, fmt.Errorf("mcp: read %s: %w", path, err)
		}
		return nil, false, nil
	}
	doc, err := parseRawDoc(data)
	if err != nil {
		return nil, false, fmt.Errorf("mcp: parse %s: %w", path, err)
	}
	if doc.servers == nil {
		return nil, false, nil
	}
	m, ok := doc.servers.get(name) // last occurrence: json map semantics
	if !ok {
		return nil, false, nil
	}
	return m.raw, true, nil
}

// planUpsertEntry computes the splice(s) that insert or replace one entry,
// returning the edits plus whether the ENTRY was created and whether an
// existing entry was overwritten. nil edits mean "the file already holds
// these exact bytes" — nothing may be rewritten. On error the caller must
// write nothing.
func planUpsertEntry(doc *rawDoc, name string, entry []byte) (edits []edit, created, replaced bool, err error) {
	if doc.servers == nil {
		// No envelope object survives the fold: absent, null, or a null
		// occurrence that cleared an earlier object. Create it exactly the
		// way the disable primitive does (same bytes, same caveat).
		edits, err = planEnvelope(doc, name, entry)
		return edits, err == nil, false, err
	}
	sm, ok := lastMember(doc.members, "mcpServers")
	if !ok {
		// Unreachable: a non-nil fold comes from an envelope member.
		return nil, false, false, errors.New("mcpServers object is not present at the top level")
	}
	m, ok := doc.servers.get(name) // last occurrence: json map semantics
	if !ok {
		// New entry inside the existing object: append, never re-serialize.
		e, err := planAppendEntry(doc, sm, name, entry)
		if err != nil {
			return nil, false, false, err
		}
		return []edit{e}, true, false, nil
	}
	if bytes.Equal(m.raw, entry) {
		// Idempotent: the winning occurrence already holds these bytes.
		return nil, false, false, nil
	}
	// Replace the winning occurrence's value span, in place: the key and the
	// separating whitespace the user wrote survive (a null entry is
	// materialised here too — it can hold no field, so it is replaced
	// wholesale).
	return []edit{{m.start, m.end, entry}}, false, true, nil
}

// marshalEntry renders one ServerConfig as the canonical compact object
// described in UpsertEntry's doc comment: non-zero fields only, fixed order,
// sorted map keys, no HTML escaping, no expansion whatsoever. The field
// order is the order of the code below — the object is assembled by hand
// because encoding/json gives no such guarantee.
func marshalEntry(e ServerConfig) []byte {
	parts := make([]string, 0, 8)
	add := func(key, value string) {
		parts = append(parts, quoteJSONString(key)+":"+value)
	}
	if e.Command != "" {
		add("command", quoteJSONString(e.Command))
	}
	if len(e.Args) > 0 {
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = quoteJSONString(a)
		}
		add("args", "["+strings.Join(args, ",")+"]")
	}
	if len(e.Env) > 0 {
		add("env", quoteStringMap(e.Env))
	}
	if e.URL != "" {
		add("url", quoteJSONString(e.URL))
	}
	if e.Type != "" {
		// Emitted whenever it is set, even on a stdio entry where the reader
		// ignores it: dropping it would make an upsert silently lose a field
		// the caller asked for instead of round-tripping the entry.
		add("type", quoteJSONString(e.Type))
	}
	if len(e.Headers) > 0 {
		add("headers", quoteStringMap(e.Headers))
	}
	if e.Description != "" {
		add("description", quoteJSONString(e.Description))
	}
	if e.Disabled {
		add("disabled", "true") // "disabled": false is never written
	}
	return []byte("{" + strings.Join(parts, ",") + "}")
}

// quoteStringMap renders a string map as a JSON object with SORTED keys, so
// two runs over the same ServerConfig produce byte-identical output (map
// iteration order is random — idempotence depends on this).
func quoteStringMap(m map[string]string) string {
	keys := keyNames(m) // sorted, nil for an empty map
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, quoteJSONString(k)+":"+quoteJSONString(m[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// quoteJSONString renders s as a JSON string literal that keeps every byte a
// user typed. json.Marshal would rewrite '&', '<' and '>' as \u0026, \u003c
// and \u003e: decode-equivalent, but not what was on the command line, and
// this writer's whole point is that unrelated bytes survive. SetEscapeHTML
// (false) keeps them; U+2028/U+2029 stay escaped by design — both forms are
// valid JSON literals decoding back to s exactly, and expansion is not
// involved either way, so "${VAR}" and "$$" travel verbatim.
func quoteJSONString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		// Unreachable: encoding a string cannot fail. Degrade to
		// json.Marshal rather than hand the caller a truncated literal.
		b, _ := json.Marshal(s)
		return string(b)
	}
	// Encode appends a newline after the literal; it is not part of it.
	return strings.TrimSuffix(buf.String(), "\n")
}
