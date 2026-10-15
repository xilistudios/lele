package mcp

import (
	"errors"
	"net/url"
	"strings"
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
// WHITESPACE here means the set ECMAScript's `\s` matches, NOT Go's
// unicode.IsSpace: see authPasteWhitespace. The two sets differ on exactly two
// runes — Go adds U+0085 and lacks U+FEFF — and a client review MEASURED that
// divergence (`abc`+U+0085+`xyz` split into two tokens here and stayed one
// token in the browser; a leading BOM was trimmed there and kept here), so
// every trim, split and emptiness test in this file goes through the cutset
// below rather than through the standard library's notion of a space.
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
// THE DECODE IS BOUNDED, not trusted. The two languages' decoders
// (url.ParseQuery here, URLSearchParams in the browser) do NOT agree about a
// malformed escape, a high-bit escape or a `;`, so the chosen substring is
// pre-checked and a paste carrying any of the three is UNRECOGNIZED (see
// authPasteBytesRefused); every escape that then reaches ParseQuery is ASCII,
// which is exactly the region where the two decoders are byte-for-byte
// identical. Nothing is decoded by hand: that is how the surfaces drifted
// apart.
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

// authPasteWhitespace is the grammar's whitespace set: exactly the runes
// ECMAScript's `\s` matches. It is spelled out because NEITHER
// strings.TrimSpace/strings.Fields NOR url.Values implements it, and the
// difference is not academic — a client review MEASURED two cross-surface
// divergences it causes:
//
//   - Go's unicode.IsSpace (and therefore TrimSpace/Fields) counts U+0085
//     (NEL) as a space; ECMAScript's `\s` does not. With strings.Fields the
//     paste `abc`+U+0085+`xyz` therefore split into the two tokens "abc" and
//     "xyz" and was SUBMITTED, while the browser read it as one token, failed
//     the shape check on the U+0085 and refused it: two surfaces, two answers
//     about the same paste. With this set the paste carries no whitespace at
//     all, so it stays a single bare code whose shape check rejects the
//     non-code rune — `unrecognized`, the browser's answer;
//   - the reverse for U+FEFF (the BOM a copy-paste picks up): ECMAScript trims
//     it, Go does not, so a paste with a leading BOM used to be a shape-check
//     rejection here and `ok("abc123", "")` in the browser.
//
// Every trim, split and emptiness test in this file goes through this constant
// (via authPasteTrim/authPasteHasWhitespace/authPasteSplitTokens), so the two
// sets cannot drift apart again.
const authPasteWhitespace = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// authPasteTrim removes the grammar's whitespace from both ends of s. It is
// strings.TrimSpace with the set above instead of unicode.IsSpace.
func authPasteTrim(s string) string { return strings.Trim(s, authPasteWhitespace) }

// authPasteHasWhitespace reports whether s contains any of the grammar's
// whitespace. It is strings.IndexFunc(s, unicode.IsSpace) >= 0 with the set
// above instead of unicode.IsSpace.
func authPasteHasWhitespace(s string) bool { return strings.ContainsAny(s, authPasteWhitespace) }

// authPasteSplitTokens splits s on runs of the grammar's whitespace, exactly
// like ECMAScript's `s.trim().split(/\s+/)`: leading and trailing runs produce
// no empty field. It is strings.Fields with the set above instead of
// unicode.IsSpace.
func authPasteSplitTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune(authPasteWhitespace, r) })
}

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

	s := authPasteTrim(input)
	if s == "" {
		return "", "", ErrAuthPasteEmpty
	}

	if !authPasteHasWhitespace(s) {
		// No whitespace: a URL/query (parameter form), a parameter substring,
		// or a bare code.
		if strings.Contains(s, "?") {
			values, ok := authPasteParams(s)
			if !ok {
				return "", "", ErrAuthPasteUnrecognized
			}
			return authPasteFromParams(values)
		}
		if strings.Contains(s, "=") {
			// Parameter-shaped but not a URL. This is the ONLY branch that
			// applies the "none of code/state/error ⇒ unrecognized" rule, which
			// is what makes `foo=bar` a rejection and keeps a query such as
			// `?foo=bar` (which did carry a `?`) a no-code instead.
			values, ok := authPasteParams(s)
			if !ok {
				return "", "", ErrAuthPasteUnrecognized
			}
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
	// paste. The split uses the grammar's whitespace set, so `abc`+U+0085+`xyz`
	// is ONE token (and therefore unrecognized) exactly as in the browser.
	fields := authPasteSplitTokens(s)
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
// the first `#`) when the query carries neither `code` nor `error`. ok is false
// when the CHOSEN substring tripped the pre-check below, which means the paste
// is unrecognized and nothing may be submitted.
//
// Decoding is url.ParseQuery — percent-decoding and `+` ⇒ space. THIS IS NOT
// because the two languages' decoders agree: the review that froze this
// grammar PROVED they do not (Go refuses `%zz` while the browser submits the
// literal `%zz`, and Go drops a `;`-bearing pair the browser keeps). The
// agreement is BOUNDED, not assumed: authPasteBytesRefused runs first and
// rejects every construct the two decoders read differently, so the only
// escapes that reach ParseQuery are ASCII ones (0x00–0x7F), which is exactly
// the region where its answer and URLSearchParams' are byte-for-byte
// identical. Hand-rolling the decode is still not an option — it is how the
// surfaces came to disagree in the first place — and ParseQuery's documented
// tolerance for a malformed pair is now unreachable: the pre-check refuses
// those pastes before it can be exercised.
func authPasteParams(s string) (url.Values, bool) {
	query := s
	if i := strings.IndexByte(s, '?'); i >= 0 {
		query = s[i+1:]
	}
	if i := strings.IndexByte(query, '#'); i >= 0 {
		query = query[:i]
	}
	// Exactly ONE leading `?` is stripped from the CHOSEN substring — query or
	// fragment — as URLSearchParams/WebUI does, so both surfaces agree for the
	// whole `?`-prefix family; `???…` still refuses because only one is stripped.
	query = strings.TrimPrefix(query, "?")
	if authPasteBytesRefused(query) {
		return nil, false
	}
	values, _ := url.ParseQuery(query)
	if authPasteHasAny(values, authPasteParamCode, authPasteParamError) {
		return values, true
	}
	// The query carries neither, so the fragment is the only place a
	// response_mode=fragment answer can live. An empty fragment is no fragment:
	// falling back to it would discard a usable query for a stray `#`. It is
	// only checked when it is about to be USED — the pre-check is a property of
	// the chosen substring, and an unused fragment is never decoded.
	if i := strings.IndexByte(s, '#'); i >= 0 && i+1 < len(s) {
		// Same rule as the query above: strip exactly ONE leading `?` from the
		// chosen substring (URLSearchParams/WebUI does too).
		fragment := strings.TrimPrefix(s[i+1:], "?")
		if authPasteBytesRefused(fragment) {
			return nil, false
		}
		if values, _ := url.ParseQuery(fragment); len(values) > 0 {
			return values, true
		}
	}
	return values, true
}

// authPasteBytesRefused reports whether a candidate parameter substring (the
// ENCODED query or fragment, before any decoding) contains something Go's
// url.ParseQuery and the browser's URLSearchParams answer DIFFERENTLY about.
// The grammar therefore refuses the paste outright rather than trusting either
// decoder to win. The scan is explicit — it does not lean on ParseQuery's error
// — because the rule belongs to the grammar and the WebUI runs the same one.
// Three constructs are refused:
//
//   - a `%` not followed by two hexadecimal digits (`%zz`, a trailing `%`, a
//     short `%8`): Go's ParseQuery drops the whole pair and reports an error,
//     the browser submits the literal text as the value;
//   - an escape whose byte has the high bit set (`%80`–`%FF`, either case):
//     Go yields a raw, possibly invalid-UTF-8 byte while the browser yields
//     U+FFFD — and no authorization server puts non-ASCII in code/state/error,
//     so refusing costs nothing and removes every Unicode decoding question;
//   - a `;` anywhere: Go drops the whole offending pair, the browser keeps the
//     semicolon as an ordinary character, so the SAME paste means two different
//     things — and a real server never emits one.
//
// A `;` that arrived as an ESCAPE (`%3B`) is not refused: it is data, decoded
// identically by both languages, whereas a literal `;` is the separator they
// disagree about.
func authPasteBytesRefused(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == ';' {
			return true
		}
		if s[i] != '%' {
			continue
		}
		if i+2 >= len(s) || !authPasteIsHex(s[i+1]) || !authPasteIsHex(s[i+2]) {
			return true // malformed or truncated escape
		}
		if authPasteHexByte(s[i+1], s[i+2]) >= 0x80 {
			return true // high-bit escape: the decoders' Unicode answers diverge
		}
		i += 2 // step over the escape so its hex digits are not rescanned
	}
	return false
}

// authPasteIsHex reports whether c is a hexadecimal digit, either case.
func authPasteIsHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// authPasteHexByte is the byte the escape `%hi lo` names. Callers have already
// checked both digits with authPasteIsHex.
func authPasteHexByte(hi, lo byte) byte {
	return authPasteHexNibble(hi)<<4 | authPasteHexNibble(lo)
}

// authPasteHexNibble is the value of one hexadecimal digit.
func authPasteHexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default: // 'A'-'F'; callers have checked the digit set
		return c - 'A' + 10
	}
}

// authPasteFromParams applies the parameter-form rules in the frozen order:
// `error` (a refusal) FIRST, then a non-empty `code`, else no-code. `code`,
// `state` and `error` are all trimmed with the grammar's whitespace set — the
// `error` trim is not cosmetic: reading `error=%20` as a refusal here while the
// browser reads it as a blank and falls through to `code` turns a refusal URL
// into a submitted code, the worst kind of cross-surface divergence.
func authPasteFromParams(values url.Values) (code, state string, err error) {
	if raw := authPasteTrim(values.Get(authPasteParamError)); raw != "" {
		// Only the sanitized code may leave this function: error_description and
		// every other parameter are attacker-controlled prose and are ignored on
		// purpose. A value the sanitizer refuses yields Code == "" and the
		// caller's own fixed sentence.
		return "", "", &AuthPasteDeniedError{Code: SanitizeOAuthCode(raw)}
	}
	if raw := authPasteTrim(values.Get(authPasteParamCode)); raw != "" {
		return raw, authPasteTrim(values.Get(authPasteParamState)), nil
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
