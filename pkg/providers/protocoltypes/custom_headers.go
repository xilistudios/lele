// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package protocoltypes

import (
	"fmt"
	"sort"
	"strings"
)

// SessionHeaderPlaceholder is the template token replaced at request time with
// the per-conversation wire session id (see WireSessionForHeaders).
const SessionHeaderPlaceholder = "{{session}}"

// placeholderOpen and placeholderClose delimit a template token inside a header
// value. Only the double form is meaningful to users; a single brace is literal
// text, so JSON-style values such as {"a}b"} pass validation untouched.
const (
	placeholderOpen  = "{{"
	placeholderClose = "}}"
)

// sessionPlaceholderName is the one token the expansion understands: the text
// between the braces of SessionHeaderPlaceholder.
const sessionPlaceholderName = "session"

// ExpandHeaderValue substitutes every occurrence of SessionHeaderPlaceholder in
// a custom header value with sessionID.
//
// An empty sessionID is not special-cased here: callers pass
// WireSessionForHeaders(...), which never returns "" (a blank key yields the
// shared unkeyed bucket), so expanding with "" would mean the caller forgot to
// resolve the id — a bug in the caller, not a case to paper over.
func ExpandHeaderValue(value, sessionID string) string {
	return strings.ReplaceAll(value, SessionHeaderPlaceholder, sessionID)
}

// forbiddenHeaderNames are the headers a transport sets itself. Overriding them
// from config would desynchronise the request from what Go actually writes on
// the wire — Go owns Host, Content-Length, Transfer-Encoding and Connection,
// and Content-Type is part of each transport's body encoding — so a user
// setting one is always a mistake rather than an override.
//
// Everything else is allowed on purpose, including the headers the transport
// would otherwise set (authorization, user-agent, x-api-key,
// anthropic-version, accept) and the detected session header
// (x-opencode-session): custom headers are applied last precisely so an
// explicit intent can win, e.g. a gateway whose auth scheme is not Bearer.
var forbiddenHeaderNames = map[string]bool{
	"host": true, "content-length": true, "transfer-encoding": true,
	"connection": true, "keep-alive": true, "proxy-connection": true,
	"upgrade": true, "te": true, "trailer": true, "content-type": true,
}

// ValidateCustomHeaders reports the first problem it finds in a per-provider
// header map, in a message safe to show a user (it names the offending header).
// A nil or empty map is valid.
//
// It runs at config load, not per request: the checks are cheap and a failure
// must surface before the first call, because a malformed name or value would
// otherwise be rejected — or, worse, silently split into extra headers — by the
// upstream gateway, far away from the typo in config.json.
//
// Rules: a name must be a non-empty RFC 7230 token; a value must not contain a
// control character — checked on the raw value, because the transports send it
// untrimmed (an empty value is allowed); transport-owned headers cannot be
// overridden; and the only template token is SessionHeaderPlaceholder.
func ValidateCustomHeaders(headers map[string]string) error {
	// Sorted so that a map with several problems always reports the same one:
	// Go's map iteration order is random, and this error is asserted verbatim
	// by tests and shown verbatim to users.
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if !isHeaderName(name) {
			return fmt.Errorf("custom header %q: invalid name: must be an RFC 7230 token", name)
		}
		if forbiddenHeaderNames[strings.ToLower(strings.TrimSpace(name))] {
			return fmt.Errorf("custom header %q: is set by the transport and cannot be overridden", name)
		}
		value := strings.TrimSpace(headers[name])
		if hasControlChar(headers[name]) {
			return fmt.Errorf("custom header %q: value contains a control character (CR/LF/NUL)", name)
		}
		if err := checkPlaceholders(name, value); err != nil {
			return err
		}
	}
	return nil
}

// isHeaderName reports whether name is a non-empty RFC 7230 token: letters,
// digits and the character class !#$%&'*+-.^_`|~. Space, ":" and "/" are not
// tokens, so a name carrying them is a config typo, never a header.
func isHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isTokenByte(name[i]) {
			return false
		}
	}
	return true
}

// isTokenByte reports whether c is one byte of the RFC 7230 tchar set.
func isTokenByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		return true
	}
	return false
}

// hasControlChar reports whether value holds a byte that cannot appear in a
// header value: anything below the printable ASCII space or DEL. It runs on the
// RAW value, not the trimmed one: the transports expand and send exactly what
// config holds, so a trailing "\r" would pass a trimmed check and then be
// rejected at write time by http.Client with an error that names no header —
// a config mistake must surface at load instead. Surrounding spaces are legal
// (and what people type), so they are not flagged by this check either way.
func hasControlChar(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] == 0x7f {
			return true
		}
	}
	return false
}

// checkPlaceholders verifies that every {{…}} token in a value is
// SessionHeaderPlaceholder. A single brace is literal text and skipped, so only
// an unterminated "{{" or an unknown token name is an error.
func checkPlaceholders(name, value string) error {
	for i := 0; i < len(value); {
		if value[i] != '{' || i+1 >= len(value) || value[i+1] != '{' {
			i++
			continue
		}
		rel := strings.Index(value[i+len(placeholderOpen):], placeholderClose)
		if rel < 0 {
			// Report the rest of the value: it is what the user typed and can
			// see which token they forgot to close.
			return fmt.Errorf("custom header %q: unterminated placeholder %q", name, value[i:])
		}
		start := i + len(placeholderOpen)
		inner := value[start : start+rel]
		if inner != sessionPlaceholderName {
			return fmt.Errorf("custom header %q: unknown placeholder %q (only %s is supported)",
				name, placeholderOpen+inner+placeholderClose, SessionHeaderPlaceholder)
		}
		i = start + rel + len(placeholderClose)
	}
	return nil
}
