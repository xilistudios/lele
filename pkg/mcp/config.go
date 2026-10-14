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

// AuthSpec is the optional "auth" block of a REMOTE (url) entry. It opts the
// server into lele's interactive OAuth 2.1 flow; every field except Type is
// optional and filled by discovery / dynamic client registration when empty.
type AuthSpec struct {
	// Type selects the auth mode: "" (unset), "none" or "oauth".
	Type string `json:"type,omitempty"`
	// Scopes are the OAuth scopes to request.
	Scopes []string `json:"scopes,omitempty"`
	// ClientID / ClientSecret are a pre-registered client (DCR fills them
	// when empty).
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	// RedirectURI overrides the loopback callback (pre-registered clients).
	RedirectURI string `json:"redirect_uri,omitempty"`
	// AuthServerMetadataURL / ProtectedResourceMetadataURL override metadata
	// discovery when the server does not advertise them.
	AuthServerMetadataURL        string `json:"auth_server_metadata_url,omitempty"`
	ProtectedResourceMetadataURL string `json:"protected_resource_metadata_url,omitempty"`
}

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
	// Auth is the optional OAuth configuration of a remote entry. Nil means
	// "no auth block declared" (the entry keeps today's behaviour).
	Auth *AuthSpec `json:"auth,omitempty"`
	// Description is what the LLM sees before the server is loaded.
	Description string `json:"description"`
	// Disabled marks an entry that is parsed but never activated.
	Disabled bool `json:"disabled"`
	// authRuntime carries the resolved OAuth state of THIS dial. It is never
	// serialized (json:"-") and never set by the config reader: the manager
	// attaches it per resolve, and buildTransport reads it to decide whether
	// the remote transport is built with OAuth. configFingerprint therefore
	// ignores it by construction.
	authRuntime *oauthRuntime `json:"-"`
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
	// typeAuthNone declares the auth block but asks for no auth at all.
	typeAuthNone = "none"
	// typeAuthOAuth runs the interactive OAuth 2.1 flow.
	typeAuthOAuth = "oauth"
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
//     env value, each headers value, description and the auth string fields;
//     env/headers KEYS and the type fields are never expanded;
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
// args values, url, env values, headers values, description and the auth
// string fields (client_id, client_secret, redirect_uri, the two metadata
// URLs and every scope). Keys of env/headers and the type fields are not
// expandable: expansion is READ-ONLY, and the writers of mcp.json store what
// the user typed so `lele` never bakes a resolved secret into a file.
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
	if s.Auth == nil { // no auth block declared: nothing to expand
		return
	}
	s.Auth.ClientID = fn(s.Auth.ClientID)
	s.Auth.ClientSecret = fn(s.Auth.ClientSecret)
	s.Auth.RedirectURI = fn(s.Auth.RedirectURI)
	s.Auth.AuthServerMetadataURL = fn(s.Auth.AuthServerMetadataURL)
	s.Auth.ProtectedResourceMetadataURL = fn(s.Auth.ProtectedResourceMetadataURL)
	for i := range s.Auth.Scopes {
		s.Auth.Scopes[i] = fn(s.Auth.Scopes[i])
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
// apply that default; Type is ignored for stdio entries. The optional auth
// block is remote-only: OAuth needs a url to talk to, so a stdio entry that
// declares one is a user mistake worth reporting instead of silently ignoring.
func (s *ServerConfig) validate() error {
	switch {
	case s.Command != "" && s.URL != "":
		return errors.New("mixes stdio and remote fields: set either command or url, not both")
	case s.Command != "":
		if s.Auth != nil {
			return errors.New("auth block requires a url (remote entry)")
		}
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
	if s.Auth != nil {
		switch s.Auth.Type {
		case "", typeAuthNone, typeAuthOAuth:
		default:
			return fmt.Errorf("unknown auth type %q: must be %q or %q", s.Auth.Type, typeAuthNone, typeAuthOAuth)
		}
		// A secret without an id cannot be attributed to a client: reject
		// rather than discard it silently.
		if s.Auth.ClientSecret != "" && s.Auth.ClientID == "" {
			return errors.New("auth client_secret requires client_id")
		}
	}
	return nil
}
