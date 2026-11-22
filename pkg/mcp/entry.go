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

type UpsertEntryResult struct {
	Path     string
	Name     string
	Created  bool
	Replaced bool
	Changed  bool
}

type DeleteEntryResult struct {
	Path    string
	Name    string
	Deleted bool
	Changed bool
}

func UpsertEntry(path, name string, entry ServerConfig) (UpsertEntryResult, error) {
	res := UpsertEntryResult{Path: path, Name: name}
	if err := validEntryName(name); err != nil {
		return res, err
	}
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
	res.Created, res.Replaced, res.Changed = created, replaced, true
	return res, nil
}

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
		return res, nil
	}

	doc, err := parseRawDoc(data)
	if err != nil {
		return res, fmt.Errorf("mcp: parse %s: %w", path, err)
	}
	if doc.servers == nil {
		return res, nil
	}
	m, ok := doc.servers.get(name)
	if !ok {
		return res, nil
	}
	span, err := planDeleteEntry(doc, m)
	if err != nil {
		return res, fmt.Errorf("mcp: edit %s: %w", path, err)
	}

	next := applyEdits(data, []edit{span})
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
	m, ok := doc.servers.get(name)
	if !ok {
		return nil, false, nil
	}
	return m.raw, true, nil
}

func planUpsertEntry(doc *rawDoc, name string, entry []byte) (edits []edit, created, replaced bool, err error) {
	if doc.servers == nil {
		edits, err = planEnvelope(doc, name, entry)
		return edits, err == nil, false, err
	}
	sm, ok := lastMember(doc.members, "mcpServers")
	if !ok {
		return nil, false, false, errors.New("mcpServers object is not present at the top level")
	}
	m, ok := doc.servers.get(name)
	if !ok {
		e, err := planAppendEntry(doc, sm, name, entry)
		if err != nil {
			return nil, false, false, err
		}
		return []edit{e}, true, false, nil
	}
	if bytes.Equal(m.raw, entry) {
		return nil, false, false, nil
	}
	return []edit{{m.start, m.end, entry}}, false, true, nil
}

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
		add("type", quoteJSONString(e.Type))
	}
	if len(e.Headers) > 0 {
		add("headers", quoteStringMap(e.Headers))
	}
	if e.Auth != nil {
		add("auth", marshalAuthSpec(e.Auth))
	}
	if e.Description != "" {
		add("description", quoteJSONString(e.Description))
	}
	if e.Disabled {
		add("disabled", "true")
	}
	return []byte("{" + strings.Join(parts, ",") + "}")
}

func marshalAuthSpec(a *AuthSpec) string {
	parts := make([]string, 0, 7)
	add := func(key, value string) {
		parts = append(parts, quoteJSONString(key)+":"+value)
	}
	if a.Type != "" {
		add("type", quoteJSONString(a.Type))
	}
	if len(a.Scopes) > 0 {
		scopes := make([]string, len(a.Scopes))
		for i, s := range a.Scopes {
			scopes[i] = quoteJSONString(s)
		}
		add("scopes", "["+strings.Join(scopes, ",")+"]")
	}
	if a.ClientID != "" {
		add("client_id", quoteJSONString(a.ClientID))
	}
	if a.ClientSecret != "" {
		add("client_secret", quoteJSONString(a.ClientSecret))
	}
	if a.RedirectURI != "" {
		add("redirect_uri", quoteJSONString(a.RedirectURI))
	}
	if a.AuthServerMetadataURL != "" {
		add("auth_server_metadata_url", quoteJSONString(a.AuthServerMetadataURL))
	}
	if a.ProtectedResourceMetadataURL != "" {
		add("protected_resource_metadata_url", quoteJSONString(a.ProtectedResourceMetadataURL))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func quoteStringMap(m map[string]string) string {
	keys := keyNames(m)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, quoteJSONString(k)+":"+quoteJSONString(m[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func quoteJSONString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		b, _ := json.Marshal(s)
		return string(b)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
