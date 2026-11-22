package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

type AuthSpec struct {
	Type                         string   `json:"type,omitempty"`
	Scopes                       []string `json:"scopes,omitempty"`
	ClientID                     string   `json:"client_id,omitempty"`
	ClientSecret                 string   `json:"client_secret,omitempty"`
	RedirectURI                  string   `json:"redirect_uri,omitempty"`
	AuthServerMetadataURL        string   `json:"auth_server_metadata_url,omitempty"`
	ProtectedResourceMetadataURL string   `json:"protected_resource_metadata_url,omitempty"`
}

type ServerConfig struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	URL         string            `json:"url"`
	Type        string            `json:"type"`
	Headers     map[string]string `json:"headers"`
	Auth        *AuthSpec         `json:"auth,omitempty"`
	Description string            `json:"description"`
	Disabled    bool              `json:"disabled"`
	authRuntime *oauthRuntime     `json:"-"`
}

type File struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`

	EntryErrors map[string]error `json:"-"`
}

type ServerError struct {
	Server string
	Err    error
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("mcp server %q: %v", e.Server, e.Err)
}

func (e *ServerError) Unwrap() error { return e.Err }

type ExpandFunc func(string) string

const (
	typeHTTP      = "http"
	typeSSE       = "sse"
	typeAuthNone  = "none"
	typeAuthOAuth = "oauth"
)

func newExpand(lookup func(name string) (string, bool)) ExpandFunc {
	return func(s string) string { return expandString(s, lookup) }
}

func envExpand() ExpandFunc {
	return newExpand(os.LookupEnv)
}

func expandString(s string, lookup func(name string) (string, bool)) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		switch {
		case s[i] != '$':
			b.WriteByte(s[i])
			i++
		case i+1 < len(s) && s[i+1] == '$':
			b.WriteByte('$')
			i += 2
		case i+1 < len(s) && s[i+1] == '{':
			open := i + 2
			end := strings.IndexByte(s[open:], '}')
			if end < 0 {
				b.WriteString(s[i:])
				return b.String()
			}
			value, _ := lookup(s[open : open+end])
			b.WriteString(value)
			i = open + end + 1
		default:
			b.WriteByte('$')
			i++
		}
	}
	return b.String()
}

func ParseFile(path string) (*File, error) {
	return parseFile(path, envExpand())
}

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

func readFailure(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("read mcp config %s: %w", path, err)
}

func (f *File) expand(fn ExpandFunc) {
	for name, srv := range f.MCPServers {
		expandServer(&srv, fn)
		f.MCPServers[name] = srv
	}
}

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
	if s.Auth == nil {
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

func (s *ServerConfig) validate() error {
	switch {
	case s.Command != "" && s.URL != "":
		return errors.New("mixes stdio and remote fields: set either command or url, not both")
	case s.Command != "":
		if s.Auth != nil {
			return errors.New("auth block requires a url (remote entry)")
		}
		return nil
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
		if s.Auth.ClientSecret != "" && s.Auth.ClientID == "" {
			return errors.New("auth client_secret requires client_id")
		}
	}
	return nil
}
