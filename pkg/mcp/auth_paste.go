package mcp

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// This file is the ONE canonical implementation of the "manual OAuth paste"
// grammar: the rule that turns a single value a user pasted into the
// (code, state) pair the flow's completion endpoint expects. The three surfaces
// that accept a pasted authorization result — the TUI (ModalMCPAuth), the CLI
// (`lele mcp auth --manual`) and the WebUI (MCPAuthModal) — compile against
// these symbols because a client review proved three hand-written copies
// DISAGREED: the same paste was submitted by one surface, refused by another, or
// submitted as a DIFFERENT code (`code=abc` reached the server as the literal
// string "code=abc" from the WebUI, `foo=bar` was submitted as a code by two
// surfaces, `!!!` by the TUI). The grammar is FROZEN in
// .lele/tasks/mcp-oauth-paste-grammar.md; the code below reproduces its decision
// tree and its rules rather than paraphrasing them, and a rule that looks
// surprising is documented where it is implemented.
//
// FOUR accepted forms (whitespace around the whole paste is ignored — a copied
// URL routinely picks up a trailing newline; whitespace INSIDE a bare code is
// not, because a code is never split across lines):
//
//  1. a full redirect URL, `http://127.0.0.1:37401/cb?code=…&state=…`;
//  2. a query string, `?code=…&state=…` or `code=…&state=…`;
//  3. a bare authorization code, `SplxlOBeZQQYbYS6WxSbIA`;
//  4. two whitespace-separated tokens, `<code> <state>` (a terminal
//     convenience: some authorization servers print the two on separate lines).
//
// TWO cases are rejected BY DESIGN, so nobody "fixes" them later:
//
//   - `foo=bar` is UNRECOGNIZED, never a submitted code. Input carrying `=`
//     (and no `?`) is parameter-shaped, and a parameter substring holding none
//     of `code`, `state`, `error` is garbage. That is the whole point: a
//     refusal must not be invented where there is nothing to submit.
//   - a bare code with `=` padding (`YWJj=`) is rejected for the SAME reason:
//     any `=` routes the paste to the parameter rule before the shape check can
//     see it, which is why the shape alphabet below still lists `=` even though
//     the bare-code path can never reach it. Authorization codes are opaque and
//     every real-world server uses an unpadded, URL-safe alphabet; the
//     trade-off is deliberate.
//
// THE FRAGMENT FALLBACK is deliberate and mirrored from the WebUI (which had it,
// tested, before this grammar existed): an authorization server answering in
// `response_mode=fragment` leaves `…/callback#code=…&state=…` in the address
// bar, the browser sends a fragment nowhere, so the paste is the only place
// that response exists — the fallback can only ever rescue a user. The frozen
// grammar conditions it on the QUERY carrying neither `code` nor `error`; an
// empty fragment (`…/cb?state=x#`) is treated as "no fragment", so a stray
// trailing `#` cannot discard a usable query.
//
// Nothing here reads or returns a secret: an authorization code is a
// single-use value the user pasted, and it is forwarded, never logged.

// The four outcome sentinels, one per result of ParseAuthPaste. They are
// variables (not inline errors.New calls) so a surface can map an outcome to
// its own localized sentence with errors.Is, and so the "fixed, value-free
// string" rule is auditable in one place. Callers WRAP them, never replace
// them.
var (
	// ErrAuthPasteEmpty marks a paste with nothing in it (empty or whitespace
	// only): there is nothing to submit and nothing wrong to report.
	ErrAuthPasteEmpty = errors.New("no authorization response was pasted")
	// ErrAuthPasteUnrecognized marks a value that is none of the four accepted
	// forms — `!!!`, `a b c`, `foo=bar`, a bare code that fails the shape check.
	// It is deliberately distinct from ErrAuthPasteNoCode so a surface may (or
	// may not) word them the same way.
	ErrAuthPasteUnrecognized = errors.New("unrecognized authorization response")
	// ErrAuthPasteNoCode marks a URL or query that ARRIVED but carries no
	// authorization code (`…?state=xyz`, `…?code=`): the paste is well-formed,
	// the response just has no code in it.
	ErrAuthPasteNoCode = errors.New("the pasted authorization response carries no code")
	// ErrAuthPasteDenied marks a REFUSAL: the authorization server itself put a
	// non-empty `error` parameter in the response (`access_denied` is the
	// everyday case). The typed error below carries the sanitized code.
	ErrAuthPasteDenied = errors.New("authorization denied by the authorization server")
)

// AuthPasteDeniedError is returned when the pasted response is a REFUSAL. Code
// is the SANITIZED error code (see SanitizeOAuthCode), or "" when nothing in the
// response may be echoed — a caller then uses its own fixed sentence.
//
// SECRECY: the message contains Code and nothing else. `error_description` is
// attacker-controlled prose (the authorization server may echo back whatever it
// was sent) and MUST NOT appear, not even inside an error string, so it is
// dropped at the parse boundary rather than trimmed here. Unwrap returns
// ErrAuthPasteDenied, so errors.Is(err, ErrAuthPasteDenied) works without a
// type assertion while errors.As still recovers the code.
type AuthPasteDeniedError struct{ Code string }

// Error renders the refusal. With an empty Code it is exactly the fixed
// sentence a caller would have used, so the two spellings can be compared in a
// test; with a code it is that sentence plus the sanitized code and nothing
// else.
func (e *AuthPasteDeniedError) Error() string {
	if e == nil || e.Code == "" {
		return ErrAuthPasteDenied.Error()
	}
	return ErrAuthPasteDenied.Error() + ": " + e.Code
}

// Unwrap ties the type to the ErrAuthPasteDenied sentinel.
func (e *AuthPasteDeniedError) Unwrap() error { return ErrAuthPasteDenied }

// authPasteMaxInputLen is the grammar's ONLY size rule: a paste longer than this
// many bytes is unrecognized, whatever its shape (see ParseAuthPaste's first
// rule). It exists so no surface feeds an arbitrary amount of pasted bytes into
// a request; 8 KiB is far above any redirect URL a browser produces, so it never
// rejects a real paste.
//
// It deliberately does NOT bound an authorization code: RFC 6749 §A.11 makes a
// code opaque to the client, and encrypted codes are long in the field (a
// Microsoft JWE-wrapped code runs past 1000 bytes), so any per-code cap would
// reject pastes that work.
const authPasteMaxInputLen = 8192

// The three parameter names that make a parameter substring meaningful. Any
// other parameter is ignored on purpose: `error_description` is free text from
// the server, and every other name belongs to somebody else's protocol.
const (
	authPasteParamCode  = "code"
	authPasteParamState = "state"
	authPasteParamError = "error"
)

// ParseAuthPaste turns one pasted value into the code/state pair the completion
// endpoint expects. state is "" when absent. It is PURE — no I/O, no globals, no
// state — so the same table can be unit-tested without a terminal, a browser or
// a gateway, and every surface may call it as often as it likes with identical
// results.
//
// Outcomes, one per sentinel (the frozen result vocabulary):
//
//   - ok:           code/state set, err == nil;
//   - empty:        ErrAuthPasteEmpty;
//   - unrecognized: ErrAuthPasteUnrecognized;
//   - no-code:      ErrAuthPasteNoCode;
//   - denied:       *AuthPasteDeniedError, which wraps ErrAuthPasteDenied.
//
// On EVERY error the returned code and state are "": a surface renders its own
// localized sentence and must not submit anything.
func ParseAuthPaste(input string) (code, state string, err error) {
	// The input bound, the grammar's only size rule, runs FIRST — before any
	// other rule sees the bytes. It exists so no surface feeds an arbitrary
	// amount of pasted bytes into a request; 8 KiB is far above any redirect URL
	// a browser produces, so no real paste is refused here. Nothing below
	// imposes a size rule of its own: a code is opaque (RFC 6749 §A.11) and this
	// bound is what keeps a hostile paste finite. len counts BYTES on purpose —
	// the bound is on bytes, not runes.
	if len(input) > authPasteMaxInputLen {
		return "", "", ErrAuthPasteUnrecognized
	}

	s := strings.TrimSpace(input)
	if s == "" {
		return "", "", ErrAuthPasteEmpty
	}

	if strings.IndexFunc(s, unicode.IsSpace) < 0 {
		// No whitespace: a URL/query (parameter form), a parameter substring,
		// or a bare code.
		if strings.Contains(s, "?") {
			return authPasteFromParams(authPasteParams(s))
		}
		if strings.Contains(s, "=") {
			// Parameter-shaped but not a URL. This is the ONLY branch that
			// applies the "none of code/state/error ⇒ unrecognized" rule, which
			// is what makes `foo=bar` a rejection and keeps a query such as
			// `?foo=bar` (which did carry a `?`) a no-code instead.
			values := authPasteParams(s)
			if !authPasteHasAny(values, authPasteParamCode, authPasteParamState, authPasteParamError) {
				return "", "", ErrAuthPasteUnrecognized
			}
			return authPasteFromParams(values)
		}
		if !authPasteCodeShapeOK(s) {
			return "", "", ErrAuthPasteUnrecognized
		}
		return s, "", nil
	}

	// Whitespace: ONLY the two-token form, and only when each token could be a
	// code. `a b c`, `code=abc def` and `abc! xyz` are all rejections, and a
	// refusal URL pasted with a stray space is never mistaken for a two-token
	// paste.
	fields := strings.Fields(s)
	if len(fields) == 2 &&
		!strings.ContainsAny(fields[0], "?=") && !strings.ContainsAny(fields[1], "?=") &&
		authPasteCodeShapeOK(fields[0]) && authPasteCodeShapeOK(fields[1]) {
		return fields[0], fields[1], nil
	}
	return "", "", ErrAuthPasteUnrecognized
}

// authPasteCodeShapeOK reports whether s can be a bare authorization code (or
// one of the two-token tokens). The alphabet is the RFC 6749 `code` alphabet
// plus the base64 padding character — the URL-safe set gateways actually emit,
// which includes `/` and `+` for Google-style `4/0A…` codes and `~`. Everything
// else (`!`, `?`, `&`, `#`, `:`, `%`, quotes, whitespace, control bytes, any
// non-ASCII rune) can never be a code, so it is a rejection.
//
// The check is the CHARSET only, on purpose: there is deliberately NO length rule
// here. A code is opaque to the client (RFC 6749 §A.11) and encrypted codes are
// long in the field — a Microsoft JWE-wrapped code passes 1000 bytes — so a cap
// would reject pastes that work. The size guarantee lives in ParseAuthPaste's
// single input bound (authPasteMaxInputLen), which runs before this check and
// applies to every form; a code that reached a CALLER as a `code=` parameter
// never passes through here at all and is kept verbatim and opaque.
//
// NOTE the `=` in the alphabet is unreachable from ParseAuthPaste's bare-code
// path (any `=` is routed to the parameter rule first); it is kept because the
// frozen grammar lists it, and because the two-token path is the only caller
// that could ever observe it — where the surrounding rule excludes `=` anyway.
func authPasteCodeShapeOK(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == '~', c == '+', c == '/', c == '=':
		default:
			return false
		}
	}
	return true
}

// authPasteHasAny reports whether the DECODED parameters contain any of keys —
// presence, not a non-empty value, which is what decides the fragment fallback
// (`…?code=` still counts as "the query carries a code", so the fragment is not
// consulted).
func authPasteHasAny(values url.Values, keys ...string) bool {
	for _, k := range keys {
		if _, ok := values[k]; ok {
			return true
		}
	}
	return false
}

// authPasteParams decodes the parameter substring of a parameter-form paste:
// the QUERY (everything after the first `?`, cut at the first `#`; the whole
// paste when there is no `?`), falling back to the FRAGMENT (everything after
// the first `#`) when the query carries neither `code` nor `error`.
//
// Decoding is url.ParseQuery — percent-decoding and `+` ⇒ space — because the
// WebUI's URLSearchParams answers identically for the same bytes; hand-rolling
// it is exactly how the surfaces came to disagree. ParseQuery documents that a
// malformed pair is skipped while the valid ones survive, and that is the
// behaviour taken here: the returned values are always usable.
func authPasteParams(s string) url.Values {
	query := s
	if i := strings.IndexByte(s, '?'); i >= 0 {
		query = s[i+1:]
	}
	if i := strings.IndexByte(query, '#'); i >= 0 {
		query = query[:i]
	}
	values, _ := url.ParseQuery(query)
	if authPasteHasAny(values, authPasteParamCode, authPasteParamError) {
		return values
	}
	// The query carries neither, so the fragment is the only place a
	// response_mode=fragment answer can live. An empty fragment is no fragment:
	// falling back to it would discard a usable query for a stray `#`.
	if i := strings.IndexByte(s, '#'); i >= 0 && i+1 < len(s) {
		if fragment, _ := url.ParseQuery(s[i+1:]); len(fragment) > 0 {
			return fragment
		}
	}
	return values
}

// authPasteFromParams applies the parameter-form rules in the frozen order:
// `error` (a refusal) FIRST, then a non-empty `code`, else no-code.
func authPasteFromParams(values url.Values) (code, state string, err error) {
	if raw := values.Get(authPasteParamError); raw != "" {
		// Only the sanitized code may leave this function: error_description and
		// every other parameter are attacker-controlled prose and are ignored on
		// purpose. A value the sanitizer refuses yields Code == "" and the
		// caller's own fixed sentence.
		return "", "", &AuthPasteDeniedError{Code: SanitizeOAuthCode(raw)}
	}
	if raw := strings.TrimSpace(values.Get(authPasteParamCode)); raw != "" {
		return raw, strings.TrimSpace(values.Get(authPasteParamState)), nil
	}
	return "", "", ErrAuthPasteNoCode
}

// SanitizeOAuthCode is the exported spelling of the engine's existing OAuth
// error-code sanitizer: it keeps a value iff it is non-empty, at most 64
// characters long and made entirely of [A-Za-z0-9_.-], and answers ""
// otherwise ("nothing safe to echo").
//
// It DELEGATES to sanitizeOAuthToken on purpose — exporting it is how the
// surfaces stop re-implementing (and drifting from) the rule, not a second
// implementation to keep in sync. The WebUI's echo of the raw `error` value is
// the bug this closes.
//
// The bound is deliberate: a hostile authorization server can only ever get a
// short ASCII word displayed, never a response body, a token, a control
// character or an escape sequence. See sanitizeOAuthToken for the full
// trade-off.
func SanitizeOAuthCode(s string) string {
	return sanitizeOAuthToken(s)
}
