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

type ToggleResult struct {
	Path, Name string
	Enabled    bool
	Changed    bool
	Removed    bool
	Created    bool
}

type edit struct {
	start, end int
	repl       []byte
}

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
			return res, nil
		}
		content := stubDocument(name)
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
		return res, nil
	}

	next := applyEdits(data, edits)
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
			continue
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

func validEntryName(name string) error {
	if name == "" {
		return errors.New("mcp: server name must not be empty")
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("mcp: server name %q must not contain '/'", name)
	}
	return nil
}

func stubDocument(name string) []byte {
	return singleEntryDocument(name, []byte(disabledStubJSON))
}

const disabledStubJSON = `{"disabled":true}`

func singleEntryDocument(name string, entry []byte) []byte {
	return []byte(`{"mcpServers":{` + mustJSONKey(name) + `:` + string(entry) + `}}`)
}

func planEnvelope(doc *rawDoc, name string, entry []byte) ([]edit, error) {
	sm, hasServers := lastMember(doc.members, "mcpServers")
	body := `{` + mustJSONKey(name) + `:` + string(entry) + `}`
	switch {
	case hasServers:
		return []edit{{sm.start, sm.end, []byte(body)}}, nil
	case len(doc.members) > 0:
		pos := doc.members[len(doc.members)-1].end
		return []edit{{pos, pos, []byte(", \"mcpServers\": " + body)}}, nil
	default:
		trimmed := bytes.TrimLeft(doc.data, " \t\r\n")
		if len(trimmed) > 0 && trimmed[0] == '{' {
			j := len(doc.data) - len(trimmed) + 1
			for j < len(doc.data) && isSpaceJSON(doc.data[j]) {
				j++
			}
			if j >= len(doc.data) || doc.data[j] != '}' {
				return nil, errors.New("empty top-level object does not close after '{'")
			}
			return []edit{{j, j, []byte(`"mcpServers": ` + body)}}, nil
		}
		return []edit{{0, len(doc.data), singleEntryDocument(name, entry)}}, nil
	}
}

func planAppendEntry(doc *rawDoc, sm rawMember, name string, entry []byte) (edit, error) {
	closePos := sm.end - 1
	if closePos < sm.start || doc.data[closePos] != '}' {
		return edit{}, errors.New("captured mcpServers object does not end with '}'")
	}
	member := mustJSONKey(name) + `: ` + string(entry)
	if n := len(doc.servers.members); n > 0 {
		pos := doc.servers.members[n-1].end
		return edit{pos, pos, []byte(`, ` + member)}, nil
	}
	return edit{closePos, closePos, []byte(member)}, nil
}

func planDeleteEntry(doc *rawDoc, m rawMember) (edit, error) {
	env, ok := envelopeContaining(doc, m.start)
	if !ok {
		return edit{}, errors.New("winning entry is outside every mcpServers envelope")
	}
	envObj, err := parseObjectAt(doc.data, env.start, env.raw)
	if err != nil {
		return edit{}, err
	}
	return deleteMemberSpan(doc.data, envObj.members, indexOfMember(envObj.members, m), env.start+1, env.end-1)
}

func mustJSONKey(name string) string {
	b, _ := json.Marshal(name)
	return string(b)
}

func lastMember(members []rawMember, key string) (rawMember, bool) {
	for i := len(members) - 1; i >= 0; i-- {
		if strings.EqualFold(members[i].key, key) {
			return members[i], true
		}
	}
	return rawMember{}, false
}

func envelopeContaining(doc *rawDoc, valueStart int) (rawMember, bool) {
	for _, m := range doc.members {
		if strings.EqualFold(m.key, "mcpServers") && valueStart >= m.start && valueStart < m.end {
			return m, true
		}
	}
	return rawMember{}, false
}

func planDisable(doc *rawDoc, name string) ([]edit, bool, error) {
	if doc.servers == nil {
		edits, err := planEnvelope(doc, name, []byte(disabledStubJSON))
		return edits, err == nil, err
	}
	sm, hasServers := lastMember(doc.members, "mcpServers")
	if !hasServers {
		return nil, false, errors.New("mcpServers object is not present at the top level")
	}

	m, ok := doc.servers.get(name)
	if !ok {
		e, err := planAppendEntry(doc, sm, name, []byte(disabledStubJSON))
		if err != nil {
			return nil, false, err
		}
		return []edit{e}, true, nil
	}

	if m.raw[0] != '{' {
		return []edit{{m.start, m.end, []byte(disabledStubJSON)}}, false, nil
	}
	entryObj, err := parseObjectAt(m.raw, 0, m.raw)
	if err != nil {
		return nil, false, err
	}
	if dm, has := entryObj.get("disabled"); has {
		if bytes.Equal(dm.raw, []byte("true")) {
			return nil, false, nil
		}
		off := m.start
		return []edit{{off + dm.start, off + dm.end, []byte(`true`)}}, false, nil
	}
	if n := len(entryObj.members); n > 0 {
		pos := m.start + entryObj.members[n-1].end
		return []edit{{pos, pos, []byte(`, "disabled": true`)}}, false, nil
	}
	pos := m.start + len(m.raw) - 1
	return []edit{{pos, pos, []byte(`"disabled": true`)}}, false, nil
}

func planEnable(doc *rawDoc, name string) ([]edit, bool, error) {
	if doc.servers == nil {
		return nil, false, nil
	}
	m, ok := doc.servers.get(name)
	if !ok || m.raw[0] != '{' {
		return nil, false, nil
	}
	entryObj, err := parseObjectAt(m.raw, 0, m.raw)
	if err != nil {
		return nil, false, err
	}
	hasDisabled := false
	for _, em := range entryObj.members {
		if em.key == "disabled" {
			hasDisabled = true
			break
		}
	}
	if !hasDisabled {
		return nil, false, nil
	}

	entry := append([]byte(nil), m.raw...)
	for {
		obj, err := parseObjectAt(entry, 0, entry)
		if err != nil {
			return nil, false, err
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

	var sc ServerConfig
	if err := json.Unmarshal(entry, &sc); err != nil {
		return nil, false, fmt.Errorf("entry %q after removing \"disabled\": %w", name, err)
	}
	if sc.Command == "" && sc.URL == "" {
		span, err := planDeleteEntry(doc, m)
		if err != nil {
			return nil, false, err
		}
		return []edit{span}, true, nil
	}
	return []edit{{m.start, m.end, entry}}, false, nil
}

func indexOfMember(members []rawMember, target rawMember) int {
	for i, m := range members {
		if m.start == target.start && m.end == target.end && m.key == target.key {
			return i
		}
	}
	return -1
}

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

func isSpaceJSON(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

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
