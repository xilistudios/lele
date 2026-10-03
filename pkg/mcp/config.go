// Package mcp implements lele's Model Context Protocol (MCP) client support:
// mcp.json parsing with ${VAR} expansion, layered discovery (project > agent
// > global) and, in later tasks, the connection manager and tool wrappers.
//
// Package boundary (see the implementation plan): pkg/mcp must not import
// pkg/config or pkg/agent and must not call os.Getwd — roots are injected by
// the caller and environment access goes through os.LookupEnv, which keeps
// this package unit-testable without any agent machinery.
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// ServerConfig is one entry of the "mcpServers" object of an mcp.json file.
// The json keys follow the de-facto standard used by Claude Desktop/Cursor.
type ServerConfig struct {
	// Command is the stdio launcher (mutually exclusive with URL).
	Command string `json:"command"`
	// Args are the stdio launcher arguments.
	Args []string `json:"args"`
	// Env holds extra environment variables for the stdio child.
	Env map[string]string `json:"env"`
	// URL is the remote endpoint (mutually exclusive with Command).
	URL string `json:"url"`
	// Type selects the remote transport: "http" (default) or "sse".
	// It is ignored for stdio entries.
	Type string `json:"type"`
	// Headers are extra HTTP headers for remote transports.
	Headers map[string]string `json:"headers"`
	// Description is what the LLM sees before the server is loaded.
	Description string `json:"description"`
	// Disabled marks an entry that is parsed but never activated.
	Disabled bool `json:"disabled"`
}

// File is the decoded content of one mcp.json file.
type File struct {
	// MCPServers mirrors the "mcpServers" object exactly as written: invalid
	// and disabled entries stay here so callers can inspect them.
	MCPServers map[string]ServerConfig `json:"mcpServers"`

	// EntryErrors collects validation failures keyed by server name (values
	// are *ServerError). One bad entry never invalidates the file: the other
	// entries parse normally and remain available in MCPServers.
	EntryErrors map[string]error `json:"-"`
}

// ServerError reports a validation problem of a single mcp.json entry. It is
// deliberately non-fatal: ParseFile collects these in File.EntryErrors
// instead of failing the whole file, so one broken entry cannot take every
// other server down with it.
type ServerError struct {
	Server string // entry name inside "mcpServers"
	Err    error  // underlying validation failure
}

// Error implements the error interface.
func (e *ServerError) Error() string {
	return fmt.Sprintf("mcp server %q: %v", e.Server, e.Err)
}

// Unwrap exposes the underlying failure to errors.Is/errors.As.
func (e *ServerError) Unwrap() error { return e.Err }

// ExpandFunc rewrites a config string, replacing environment references.
// It is required to be pure (same input ⇒ same output), which is what makes
// expansion injectable and testable.
type ExpandFunc func(string) string

const (
	// typeHTTP is the default remote transport when "type" is omitted.
	typeHTTP = "http"
	// typeSSE selects the legacy SSE remote transport.
	typeSSE = "sse"
)

// newExpand builds an ExpandFunc backed by lookup. Names the lookup does not
// know expand to the empty string (os.LookupEnv semantics), so ${UNSET}
// disappears from the result.
//
// Escape policy (documented choice): "$$" is the escape for a literal "$" —
// write "$${VAR}" to keep the text "${VAR}" verbatim. A backslash is NOT an
// escape character ("\${VAR}" still expands and keeps its backslash); it was
// not chosen because JSON would force the awkward "\\${VAR}" spelling.
//
// Additional rules:
//   - a lone "$" not followed by "{" is kept verbatim ("costs 5$");
//   - an unterminated "${" is kept verbatim;
//   - expansion is single pass: values returned by lookup are inserted as-is
//     and never re-scanned, so a value containing "${OTHER}" stays literal.
func newExpand(lookup func(name string) (string, bool)) ExpandFunc {
	return func(s string) string { return expandString(s, lookup) }
}

// envExpand returns the ExpandFunc used by ParseFile: ${VAR} resolved against
// the process environment via os.LookupEnv.
func envExpand() ExpandFunc {
	return newExpand(os.LookupEnv)
}

// expandString is the pure engine behind newExpand.
func expandString(s string, lookup func(name string) (string, bool)) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		switch {
		case s[i] != '$':
			b.WriteByte(s[i])
			i++
		case i+1 < len(s) && s[i+1] == '$': // "$$" escape → literal "$"
			b.WriteByte('$')
			i += 2
		case i+1 < len(s) && s[i+1] == '{': // "${NAME}"
			open := i + 2
			end := strings.IndexByte(s[open:], '}')
			if end < 0 { // unterminated reference: keep verbatim
				b.WriteString(s[i:])
				return b.String()
			}
			value, _ := lookup(s[open : open+end])
			b.WriteString(value)
			i = open + end + 1
		default: // lone "$"
			b.WriteByte('$')
			i++
		}
	}
	return b.String()
}

// ParseFile reads and decodes the mcp.json file at path.
//
// Behavior:
//   - missing file ⇒ (nil, nil): every layer is optional, absence is normal;
//   - unreadable file or malformed JSON ⇒ (nil, wrapped error) mentioning path;
//   - ${VAR} expansion (see newExpand) runs over command, each arg, url, each
//     env value, each headers value and description; env/headers KEYS and the
//     type field are never expanded;
//   - every entry is validated (stdio XOR remote); failures are collected in
//     File.EntryErrors and never fail the file;
//   - Disabled entries parse like any other but are excluded from the active
//     set (see Discover).
//
// Expanded values may contain secrets (env/headers): never log them.
func ParseFile(path string) (*File, error) {
	return parseFile(path, envExpand())
}

// parseFile is ParseFile with an injectable expander (used by tests).
func parseFile(path string, expand ExpandFunc) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, readFailure(path, err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse mcp config %s: %w", path, err)
	}
	f.expand(expand)
	f.validate()
	return &f, nil
}

// readFailure classifies a read error: a missing file is not a failure
// (layers are optional) and maps to nil, anything else is wrapped with path.
func readFailure(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("read mcp config %s: %w", path, err)
}

// expand applies fn to the expandable string fields of every entry, in place.
func (f *File) expand(fn ExpandFunc) {
	for name, srv := range f.MCPServers {
		expandServer(&srv, fn)
		f.MCPServers[name] = srv
	}
}

// expandServer rewrites the expandable fields of one entry in place: command,
// args values, url, env values, headers values and description. Keys of
// env/headers and the type field are not expandable.
func expandServer(s *ServerConfig, fn ExpandFunc) {
	s.Command = fn(s.Command)
	s.URL = fn(s.URL)
	s.Description = fn(s.Description)
	for i := range s.Args {
		s.Args[i] = fn(s.Args[i])
	}
	for k, v := range s.Env {
		s.Env[k] = fn(v)
	}
	for k, v := range s.Headers {
		s.Headers[k] = fn(v)
	}
}

// validate checks every entry after expansion, collects the per-entry
// failures into f.EntryErrors and writes back the normalized copies
// (a remote entry with an empty "type" defaults to "http").
// Disabled entries parse like any other but are excluded from the active set
// by Discover (which records them in DiscoveryResult.Disabled).
func (f *File) validate() {
	for name, srv := range f.MCPServers {
		err := srv.validate()
		f.MCPServers[name] = srv
		if err == nil {
			continue
		}
		if f.EntryErrors == nil {
			f.EntryErrors = make(map[string]error)
		}
		f.EntryErrors[name] = &ServerError{Server: name, Err: err}
	}
}

// validate checks that the entry is exactly one of the two supported shapes
// and normalizes Type for remote entries. It mutates the receiver only to
// apply that default; Type is ignored for stdio entries.
func (s *ServerConfig) validate() error {
	switch {
	case s.Command != "" && s.URL != "":
		return errors.New("mixes stdio and remote fields: set either command or url, not both")
	case s.Command != "":
		return nil // stdio
	case s.URL == "":
		return errors.New("invalid entry: needs command (stdio) or url (remote)")
	}
	switch s.Type {
	case "":
		s.Type = typeHTTP
	case typeHTTP, typeSSE:
	default:
		return fmt.Errorf("unknown type %q: must be %q or %q", s.Type, typeHTTP, typeSSE)
	}
	return nil
}
