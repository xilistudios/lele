package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

type rawMember struct {
	key   string
	raw   []byte
	start int
	end   int
}

type rawObject struct {
	members []rawMember
	index   map[string]int
}

func (o *rawObject) add(m rawMember) {
	if o.index == nil {
		o.index = make(map[string]int)
	}
	o.index[m.key] = len(o.members)
	o.members = append(o.members, m)
}

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

type rawDoc struct {
	data    []byte
	members []rawMember
	servers *rawObject
}

func parseRawDoc(data []byte) (*rawDoc, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	first, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("parse raw mcp config: %w", err)
	}
	doc := &rawDoc{data: data}
	if first == nil {
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
	for _, m := range doc.members {
		if !strings.EqualFold(m.key, "mcpServers") {
			continue
		}
		servers, err := parseObjectAt(data, m.start, m.raw)
		if err != nil {
			return nil, fmt.Errorf("parse raw mcp config: %w", err)
		}
		if err := gateEntries(servers); err != nil {
			return nil, err
		}
		if servers == nil {
			doc.servers = nil
			continue
		}
		if doc.servers == nil {
			doc.servers = &rawObject{}
		}
		for _, em := range servers.members {
			doc.servers.add(em)
		}
	}
	return doc, nil
}

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

func parseObjectAt(data []byte, start int, raw []byte) (*rawObject, error) {
	if !bytes.Equal(data[start:start+len(raw)], raw) {
		return nil, errors.New("parse raw mcp config: captured span does not match document")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	first, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if first == nil {
		return nil, expectEOF(dec)
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

func expectEOF(dec *json.Decoder) error {
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected data after the top-level value")
		}
		return err
	}
	return nil
}

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
		return ServerSummary{Name: name, Invalid: err.Error()}, true
	}
	verdict := sc.validate()

	s := ServerSummary{
		Name:        name,
		Command:     sc.Command,
		URL:         sc.URL,
		Type:        sc.Type,
		Description: sc.Description,
		Args:        len(sc.Args),
		Disabled:    sc.Disabled,
		EnvKeys:     keyNames(sc.Env),
		HeaderKeys:  keyNames(sc.Headers),
	}
	if sc.Auth != nil {
		s.AuthType = sc.Auth.Type
		s.AuthScopes = len(sc.Auth.Scopes)
		s.AuthClientIDSet = sc.Auth.ClientID != ""
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
