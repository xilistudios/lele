package mcp

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// This file pins the FROZEN manual-paste grammar
// (.lele/tasks/mcp-oauth-paste-grammar.md) row by row: the shared example table
// is reproduced verbatim, then the properties the three surfaces rely on are
// proved separately — the two rejected-by-design cases, the fragment fallback,
// the purity of ParseAuthPaste, the single 8 KiB input bound that tames a hostile
// paste, and the fact that nothing outside the sanitized code can reach an error
// message.

// authPasteOutcome is the frozen result vocabulary, one word per outcome. It is
// a test-local mirror of the sentinels so a row can name its expectation the way
// the grammar document does.
type authPasteOutcome string

const (
	pasteOK           authPasteOutcome = "ok"
	pasteEmpty        authPasteOutcome = "empty"
	pasteUnrecognized authPasteOutcome = "unrecognized"
	pasteNoCode       authPasteOutcome = "no-code"
	pasteDenied       authPasteOutcome = "denied"
)

// authPasteOutcomeOf classifies an error the way every surface must, i.e. with
// errors.Is only. An error that matches nothing is reported verbatim so a new
// sentinel cannot slip through unnoticed.
func authPasteOutcomeOf(err error) authPasteOutcome {
	switch {
	case err == nil:
		return pasteOK
	case errors.Is(err, ErrAuthPasteEmpty):
		return pasteEmpty
	case errors.Is(err, ErrAuthPasteUnrecognized):
		return pasteUnrecognized
	case errors.Is(err, ErrAuthPasteNoCode):
		return pasteNoCode
	case errors.Is(err, ErrAuthPasteDenied):
		return pasteDenied
	default:
		return authPasteOutcome("UNEXPECTED:" + err.Error())
	}
}

// authPasteAssertSafeError holds the two invariants EVERY outcome's message must
// satisfy: short, and printable ASCII — so no control byte (the ESC of
// `error=%1B%5B31mhax`), no newline from a pasted body and no attacker prose can
// ride along. The bound is the sanitizer's own worst case:
// "…: " + 64 characters.
func authPasteAssertSafeError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	if len(msg) > 128 {
		t.Errorf("error message is %d bytes, want the short fixed sentences (+ a ≤64-byte code): %q", len(msg), msg)
	}
	for _, r := range msg {
		if r < 0x20 || r > 0x7e {
			t.Errorf("error message carries a non-printable-ASCII rune %q: %q", r, msg)
			break
		}
	}
}

// canaryPasteProse is the `error_description` an authorization server is hoping
// a surface will render. It must NEVER appear in an error string.
const canaryPasteProse = "CANARY-error-description-must-never-be-echoed"

// TestParseAuthPasteSharedTable is the frozen shared example table, one subtest
// per row, in the document's order. Rows are copied from the grammar (not
// re-invented) so the three surfaces can diff their tables against this one.
func TestParseAuthPasteSharedTable(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want authPasteOutcome
		// code/state are asserted on EVERY outcome (they are "" on an error).
		code  string
		state string
		// deniedCode is the sanitized error code the typed refusal must carry.
		deniedCode string
		// absentFromError must not occur in err.Error() (the canary rows).
		absentFromError string
	}{
		{
			name: "empty string",
			in:   "",
			want: pasteEmpty,
		},
		{
			name: "spaces only",
			in:   "   ",
			want: pasteEmpty,
		},
		{
			name: "newlines and tabs only",
			in:   " \n\t ",
			want: pasteEmpty,
		},
		{
			name: "bare code",
			in:   "abc123",
			want: pasteOK,
			code: "abc123",
		},
		{
			name: "bare code with the full URL-safe alphabet",
			in:   "abc-12_3.x~y",
			want: pasteOK,
			code: "abc-12_3.x~y",
		},
		{
			// The shape check has NO length rule: a 600-byte code is opaque and
			// therefore valid (a 512-byte cap used to refuse exactly this row).
			name: "long opaque real code",
			in:   strings.Repeat("a", 600),
			want: pasteOK,
			code: strings.Repeat("a", 600),
		},
		{
			// The opposite end: the single input bound refuses the paste before
			// any shape rule runs.
			name: "bare code over the input bound",
			in:   strings.Repeat("a", 9000),
			want: pasteUnrecognized,
		},
		{
			name: "bare code padded by spaces",
			in:   "  abc123  ",
			want: pasteOK,
			code: "abc123",
		},
		{
			name: "bare code followed by a newline",
			in:   "abc123\n",
			want: pasteOK,
			code: "abc123",
		},
		{
			name: "punctuation only",
			in:   "!!!",
			want: pasteUnrecognized,
		},
		{
			name: "bare code with a bang",
			in:   "abc!def",
			want: pasteUnrecognized,
		},
		{
			name: "three tokens",
			in:   "a b c",
			want: pasteUnrecognized,
		},
		{
			name:  "two tokens",
			in:    "abc xyz",
			want:  pasteOK,
			code:  "abc",
			state: "xyz",
		},
		{
			name:  "two tokens separated by a run of spaces",
			in:    "abc   xyz",
			want:  pasteOK,
			code:  "abc",
			state: "xyz",
		},
		{
			name: "two tokens, first not a code",
			in:   "abc! xyz",
			want: pasteUnrecognized,
		},
		{
			name: "query without the question mark",
			in:   "code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
			name:  "query with code and state",
			in:    "code=abc&state=xyz",
			want:  pasteOK,
			code:  "abc",
			state: "xyz",
		},
		{
			name:  "query with a leading question mark",
			in:    "?code=abc&state=xyz",
			want:  pasteOK,
			code:  "abc",
			state: "xyz",
		},
		{
			name:  "full redirect URL",
			in:    "http://127.0.0.1:37401/cb?code=abc&state=xyz",
			want:  pasteOK,
			code:  "abc",
			state: "xyz",
		},
		{
			name:  "full redirect URL, state before code",
			in:    "http://127.0.0.1:37401/cb?state=xyz&code=abc",
			want:  pasteOK,
			code:  "abc",
			state: "xyz",
		},
		{
			name:  "percent-decoded code (a%2Bb ⇒ a+b)",
			in:    "http://127.0.0.1:37401/cb?code=a%2Bb&state=s",
			want:  pasteOK,
			code:  "a+b",
			state: "s",
		},
		{
			name:  "percent-decoded plus (code=a+b ⇒ a b) matches URLSearchParams",
			in:    "code=a+b&state=s",
			want:  pasteOK,
			code:  "a b",
			state: "s",
		},
		{
			name: "URL with a code and a trailing fragment",
			in:   "http://127.0.0.1:37401/cb?code=abc#frag",
			want: pasteOK,
			code: "abc",
		},
		{
			name: "URL with an empty code",
			in:   "http://127.0.0.1:37401/cb?code=",
			want: pasteNoCode,
		},
		{
			name: "URL with a state but no code",
			in:   "http://127.0.0.1:37401/cb?state=xyz",
			want: pasteNoCode,
		},
		{
			name:       "server refusal",
			in:         "http://127.0.0.1:37401/cb?error=access_denied",
			want:       pasteDenied,
			deniedCode: "access_denied",
		},
		{
			name:            "server refusal with an error_description canary",
			in:              "http://127.0.0.1:37401/cb?error=access_denied&error_description=" + canaryPasteProse,
			want:            pasteDenied,
			deniedCode:      "access_denied",
			absentFromError: canaryPasteProse,
		},
		{
			name: "server refusal whose code is not echoable",
			in:   "http://127.0.0.1:37401/cb?error=%1B%5B31mhax",
			want: pasteDenied,
		},
		{
			name: "unknown parameter is not a code",
			in:   "foo=bar",
			want: pasteUnrecognized,
		},
		{
			// A malformed escape: Go's ParseQuery drops the pair, the browser
			// submits the literal `%zz`. The grammar refuses the paste instead
			// of letting either decoder decide.
			name: "malformed escape in the code",
			in:   "http://127.0.0.1:37401/cb?code=%zz",
			want: pasteUnrecognized,
		},
		{
			// The measured cross-surface divergence: Go would submit code=abc
			// with state="", the browser code=abc with state="%zz".
			name: "malformed escape in the state",
			in:   "http://127.0.0.1:37401/cb?code=abc&state=%zz",
			want: pasteUnrecognized,
		},
		{
			// The QUERY is the chosen substring and is decoded first, so its
			// malformed escape is refused before the fragment is ever looked at.
			name: "malformed escape in the query beats the fragment",
			in:   "http://127.0.0.1:37401/cb?code=%zz#error=access_denied",
			want: pasteUnrecognized,
		},
		{
			// Go yields a raw byte (invalid UTF-8), the browser U+FFFD.
			name: "high-bit escape in the code",
			in:   "http://127.0.0.1:37401/cb?code=%C3%A9",
			want: pasteUnrecognized,
		},
		{
			// Go drops the whole pair, the browser keeps `a;b`: two answers
			// about the SAME paste.
			name: "semicolon separator in a parameter value",
			in:   "http://127.0.0.1:37401/cb?code=a;b",
			want: pasteUnrecognized,
		},
		{
			// A blank `error` is NOT a refusal. Reading it as one here while the
			// browser falls through to `code` is the forbidden direction: a
			// refusal URL becoming a submitted code.
			name: "blank error is not a refusal",
			in:   "http://127.0.0.1:37401/cb?error=%20&code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
			name: "blank error alone carries no code",
			in:   "http://127.0.0.1:37401/cb?error=%20",
			want: pasteNoCode,
		},
		{
			// U+0085 is a Go space (unicode.IsSpace) and NOT an ECMAScript one:
			// with strings.Fields this paste split into two tokens and was
			// submitted as ok("abc","xyz"), while the browser kept it as one
			// token and refused it.
			name: "U+0085 is not whitespace",
			in:   "abc\u0085xyz",
			want: pasteUnrecognized,
		},
		{
			// U+FEFF: the reverse divergence — ECMAScript trims the BOM, Go's
			// TrimSpace does not (a paste copied out of a browser can carry it).
			name: "leading BOM is trimmed",
			in:   "\ufeffabc123",
			want: pasteOK,
			code: "abc123",
		},
		{
			name: "trailing NBSP is trimmed",
			in:   "abc123\u00a0",
			want: pasteOK,
			code: "abc123",
		},
		{
			name: "code parameter plus a stray token",
			in:   "code=abc def",
			want: pasteUnrecognized,
		},
		{
			name:  "fragment fallback rescues a response_mode=fragment answer",
			in:    "http://127.0.0.1:37401/cb#code=abc&state=s",
			want:  pasteOK,
			code:  "abc",
			state: "s",
		},
		{
			name: "fragment fallback when the query has neither code nor error",
			in:   "http://127.0.0.1:37401/cb?state=xyz#code=abc",
			want: pasteOK,
			code: "abc",
		},
		// -- the `#?` carrier family: a `#` followed by an optional leading `?`
		//    is a legitimate carrier (an SPA or a gateway redirect writes
		//    `…#?code=…` in the address bar). `URLSearchParams` silently drops
		//    ONE leading `?`, so the Go side strips exactly one too; before this
		//    rule the fragment kept the `?` as part of the FIRST KEY (`?code`),
		//    which made Go refuse (`no-code`) exactly these pastes while the
		//    WebUI submitted them. The rows below are the measured divergence,
		//    row for row.
		{
			// The shape that already agreed — because the paste's FIRST `?` sits
			// inside the fragment, so the query slice is `code=abc` and the
			// fragment is never consulted. Kept so the agreement stays pinned.
			name: "hash carrier, single question mark",
			in:   "http://127.0.0.1:37401/cb#?code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
			name: "hash carrier after an unrelated query",
			in:   "http://127.0.0.1:37401/cb?state=1#?code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
			name: "bare query then a hash carrier",
			in:   "?a=1#?code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
			name:  "hash carrier carrying a state",
			in:    "http://127.0.0.1:37401/cb?a=1#?code=abc&state=s",
			want:  pasteOK,
			code:  "abc",
			state: "s",
		},
		{
			name:       "hash carrier with a refusal",
			in:         "http://127.0.0.1:37401/cb?a=1#?error=access_denied",
			want:       pasteDenied,
			deniedCode: "access_denied",
		},
		{
			// Exactly ONE `?` is stripped, never all of them: the fragment's
			// first key is then the literal `?code`, not `code`, which is the
			// same key `URLSearchParams` reports for the same bytes — so both
			// surfaces refuse this paste.
			name: "hash carrier, two question marks",
			in:   "?a=1#??code=abc",
			want: pasteNoCode,
		},
		{
			// The `?`-prefix family, one level up from the fragment fix: the
			// QUERY slice for `??code=abc` is `?code=abc`, and exactly ONE
			// leading `?` is stripped from the chosen substring (query or
			// fragment), so the first key is `code` — as URLSearchParams/WebUI
			// reports for the same bytes. Before this rule Go kept `?code` as
			// the first key and refused the paste with no-code.
			name: "bare query, two question marks",
			in:   "??code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
			// The same rule reached through the fragment slice (`??code=abc`):
			// the query slice stumbles on its own `?code`, the fragment strips
			// its one `?` and answers. Both surfaces agree.
			name: "hash carrier, two question marks inside the carrier",
			in:   "http://127.0.0.1:37401/cb#??code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
			// Non-vacuity control: only ONE `?` is stripped, never a loop.
			// After the strip the first key is the literal `?code`, exactly
			// what URLSearchParams reports — so both surfaces refuse.
			name: "bare query, three question marks",
			in:   "???code=abc",
			want: pasteNoCode,
		},
		{
			name:       "error wins over code",
			in:         "http://127.0.0.1:37401/cb?error=access_denied&code=abc",
			want:       pasteDenied,
			deniedCode: "access_denied",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, state, err := ParseAuthPaste(tc.in)
			if got := authPasteOutcomeOf(err); got != tc.want {
				t.Fatalf("ParseAuthPaste(%q) outcome = %s (%v), want %s", tc.in, got, err, tc.want)
			}
			if code != tc.code || state != tc.state {
				t.Fatalf("ParseAuthPaste(%q) = (%q, %q), want (%q, %q)", tc.in, code, state, tc.code, tc.state)
			}
			// An error is ALWAYS code-less and state-less: a surface must never
			// submit a (possibly different) code it could not parse.
			if err != nil && (code != "" || state != "") {
				t.Fatalf("ParseAuthPaste(%q) returned a code/state alongside %v", tc.in, err)
			}
			// Every non-denied message is the fixed sentence itself: short and
			// value-free, carrying no slice of what was pasted (a refusal is
			// the one outcome allowed to append its ≤64-byte sanitized code).
			switch tc.want {
			case pasteEmpty:
				if err.Error() != ErrAuthPasteEmpty.Error() {
					t.Fatalf("ParseAuthPaste(%q) message = %q, want %q", tc.in, err.Error(), ErrAuthPasteEmpty.Error())
				}
			case pasteUnrecognized:
				if err.Error() != ErrAuthPasteUnrecognized.Error() {
					t.Fatalf("ParseAuthPaste(%q) message = %q, want %q", tc.in, err.Error(), ErrAuthPasteUnrecognized.Error())
				}
			case pasteNoCode:
				if err.Error() != ErrAuthPasteNoCode.Error() {
					t.Fatalf("ParseAuthPaste(%q) message = %q, want %q", tc.in, err.Error(), ErrAuthPasteNoCode.Error())
				}
			}
			if tc.want == pasteDenied {
				var denied *AuthPasteDeniedError
				if !errors.Is(err, ErrAuthPasteDenied) || !errors.As(err, &denied) {
					t.Fatalf("ParseAuthPaste(%q) = %v, want *AuthPasteDeniedError wrapping ErrAuthPasteDenied", tc.in, err)
				}
				if denied.Code != tc.deniedCode {
					t.Fatalf("ParseAuthPaste(%q) denied code = %q, want %q", tc.in, denied.Code, tc.deniedCode)
				}
				// The message is the fixed sentence plus, at most, the sanitized
				// code — nothing else.
				if want := ErrAuthPasteDenied.Error(); !strings.HasPrefix(denied.Error(), want) {
					t.Fatalf("denied message %q does not start with the fixed sentence %q", denied.Error(), want)
				}
			}
			if tc.absentFromError != "" {
				if err == nil || strings.Contains(err.Error(), tc.absentFromError) {
					t.Fatalf("ParseAuthPaste(%q) error %v echoes the canary %q", tc.in, err, tc.absentFromError)
				}
			}
			authPasteAssertSafeError(t, err)
		})
	}
}

// TestParseAuthPasteEscapePreCheck pins the substring pre-check the frozen
// grammar added once a client review PROVED the two decoders disagree about
// malformed and non-ASCII escapes: Go refuses `%zz` while the browser submits
// the literal text, Go yields a raw (possibly invalid-UTF-8) byte for `%C3%A9`
// while the browser yields U+FFFD, and Go drops a `;`-bearing pair the browser
// keeps. The check is an explicit scan of the CHOSEN substring (query first,
// fragment only when the query carries neither code nor error), because the
// rule belongs to the grammar and the WebUI runs the same scan.
func TestParseAuthPasteEscapePreCheck(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  authPasteOutcome
		code  string
		state string
	}{
		// A `%` that is not a complete, low escape.
		{"trailing percent", "?code=abc%", pasteUnrecognized, "", ""},
		{"short escape", "?code=abc%8", pasteUnrecognized, "", ""},
		{"percent then no hex digits", "?code=abc%zz", pasteUnrecognized, "", ""},
		{"percent in a non-code parameter", "?error=access_denied&state=%", pasteUnrecognized, "", ""},
		{
			// The pre-check runs BEFORE the query's usability is judged, so a
			// malformed query escape is not rescued by a good fragment.
			name: "malformed query escape is not rescued by the fragment",
			in:   "?code=abc%#code=xyz",
			want: pasteUnrecognized,
		},
		// An escape whose byte has the high bit set, either case.
		{"high-bit escape, upper case", "?code=abc%FF", pasteUnrecognized, "", ""},
		{"high-bit escape, lower case", "?code=abc%ff", pasteUnrecognized, "", ""},
		{"high-bit escape, mixed case", "?code=abc%8a", pasteUnrecognized, "", ""},
		{"high-bit escape in the state", "?code=abc&state=%C3%A9", pasteUnrecognized, "", ""},
		{"high-bit escape in the CHOSEN fragment", "?state=xyz#code=%C3%A9", pasteUnrecognized, "", ""},
		{
			// A `#?` carrier is CHOSEN as the fragment (the query carries
			// neither code nor error), so the pre-check scans it there and
			// refuses the high-bit escape. NOTE: this row does NOT pin the
			// one-leading-`?` strip — a leading `?` adds no refused byte, so the
			// outcome is identical with the strip reverted. The strip itself is
			// pinned, discriminatingly, by the `?`-prefix rows in
			// TestParseAuthPasteSharedTable.
			name: "high-bit escape in the CHOSEN hash carrier",
			in:   "?state=xyz#?code=%C3%A9",
			want: pasteUnrecognized,
		},
		// A literal `;` anywhere in the chosen substring.
		{"semicolon in the code value", "?code=a;b", pasteUnrecognized, "", ""},
		{"semicolon as a separator", "?code=abc;state=xyz", pasteUnrecognized, "", ""},
		{"semicolon in the chosen fragment", "?state=xyz#code=a;b", pasteUnrecognized, "", ""},
		// The chosen substring is clean: everything here is byte-identical in
		// both languages, so the paste is accepted.
		{"a low escape is ordinary data", "?code=abc%7E", pasteOK, "abc~", ""},
		{"an ESCAPED semicolon is data, not a separator", "?code=a%3Bb", pasteOK, "a;b", ""},
		{"a comma is an ordinary character", "?code=a,b", pasteOK, "a,b", ""},
		{"an ampersand still separates parameters", "?code=a&state=b", pasteOK, "a", "b"},
		{
			// Only the CHOSEN substring is pre-checked: the query carries the
			// code, so the fragment is never decoded.
			name: "a bad fragment is never decoded when the query carries a code",
			in:   "?code=abc#state=%zz",
			want: pasteOK,
			code: "abc",
		},
		{"an unused fragment with a semicolon is harmless", "?code=abc#x;y", pasteOK, "abc", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.want == pasteUnrecognized {
				// Refusals all share the contract: nothing is submitted and the
				// message stays the short, value-free fixed sentence.
				assertPasteRefused(t, tc.in)
				return
			}
			code, state, err := ParseAuthPaste(tc.in)
			if err != nil || code != tc.code || state != tc.state {
				t.Fatalf("ParseAuthPaste(%q) = (%q, %q, %v), want (%q, %q)", tc.in, code, state, err, tc.code, tc.state)
			}
		})
	}
}

// TestParseAuthPasteSemicolonIsTheOnlyRefusedSeparator is the control for the
// `;` rule, and it exists to stop a later "helpful" widening of it: the refused
// character is the LITERAL semicolon, i.e. the one byte Go's url.ParseQuery
// treats as a separator and the browser's URLSearchParams treats as an ordinary
// character. Every other separator-ish byte is understood the same way by both
// decoders and is accepted (either as data in a value, or as the parameter
// separator both languages agree on: `&`).
func TestParseAuthPasteSemicolonIsTheOnlyRefusedSeparator(t *testing.T) {
	accepted := []struct {
		name  string
		in    string
		code  string
		state string
	}{
		{"ampersand separates (both languages agree)", "?code=a&state=b", "a", "b"},
		{"comma is data", "?code=a,b", "a,b", ""},
		{"plus decodes to a space in both", "?code=a+b", "a b", ""},
		{"pipe is data", "?code=a|b", "a|b", ""},
		{"an ESCAPED semicolon is data in both", "?code=a%3Bb", "a;b", ""},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			code, state, err := ParseAuthPaste(tc.in)
			if err != nil || code != tc.code || state != tc.state {
				t.Fatalf("ParseAuthPaste(%q) = (%q, %q, %v), want (%q, %q)", tc.in, code, state, err, tc.code, tc.state)
			}
		})
	}

	// Same shapes, one byte different: the literal `;` is the only one that
	// makes the two decoders disagree, so it is the only one refused.
	refused := []string{
		"?code=a;b",
		"?code=abc;state=xyz",
		"?state=xyz#code=a;b",
	}
	for _, in := range refused {
		t.Run("refused "+in, func(t *testing.T) { assertPasteRefused(t, in) })
	}
}

// assertPasteRefused asserts the whole refusal contract in one place: the
// outcome is ErrAuthPasteUnrecognized, NOTHING is submitted (code and state are
// empty — a surface must never send a value it could not parse), and the
// message is the short, value-free fixed sentence.
func assertPasteRefused(t *testing.T, in string) {
	t.Helper()
	code, state, err := ParseAuthPaste(in)
	if !errors.Is(err, ErrAuthPasteUnrecognized) {
		t.Fatalf("ParseAuthPaste(%q) = (%q, %q, %v), want %v", in, code, state, err, ErrAuthPasteUnrecognized)
	}
	if code != "" || state != "" {
		t.Fatalf("ParseAuthPaste(%q) submitted (%q, %q)", in, code, state)
	}
	if err.Error() != ErrAuthPasteUnrecognized.Error() {
		t.Fatalf("ParseAuthPaste(%q) message = %q, want the fixed sentence %q", in, err.Error(), ErrAuthPasteUnrecognized.Error())
	}
	authPasteAssertSafeError(t, err)
}

// TestParseAuthPasteWhitespaceSetIsECMAScripts is the whitespace control: the
// two measured divergences between Go's unicode.IsSpace and ECMAScript's `\s`
// are U+0085 (Go-only separator) and U+FEFF (ECMAScript-only separator), and
// the parser must follow ECMAScript on both. The accepted/rejected pairs below
// are exactly the rows a surface would otherwise disagree on.
func TestParseAuthPasteWhitespaceSetIsECMAScripts(t *testing.T) {
	// Every rune ECMAScript's `\s` matches must be trimmed from both ends.
	ecmaWhitespace := []string{"\t", "\n", "\v", "\f", "\r", " ", "\u00a0", "\u1680", "\u2000", "\u2001", "\u2002", "\u2003", "\u2004", "\u2005", "\u2006", "\u2007", "\u2008", "\u2009", "\u200a", "\u2028", "\u2029", "\u202f", "\u205f", "\u3000", "\ufeff"}
	for _, ws := range ecmaWhitespace {
		t.Run("trims "+fmt.Sprintf("%q", ws), func(t *testing.T) {
			for _, in := range []string{ws + "abc123", "abc123" + ws, ws + "abc123" + ws} {
				code, state, err := ParseAuthPaste(in)
				if err != nil || code != "abc123" || state != "" {
					t.Errorf("ParseAuthPaste(%q) = (%q, %q, %v), want the surrounding whitespace trimmed", in, code, state, err)
				}
			}
		})
	}

	// Each rune also SEPARATES two tokens, exactly like a space (the last one,
	// U+FEFF, is the case Go's strings.TrimSpace/Fields get wrong).
	for _, ws := range []string{"\t", "\n", "\r", "\u00a0", "\u3000", "\ufeff"} {
		t.Run("separates "+fmt.Sprintf("%q", ws), func(t *testing.T) {
			in := "abc" + ws + "xyz"
			code, state, err := ParseAuthPaste(in)
			if err != nil || code != "abc" || state != "xyz" {
				t.Errorf("ParseAuthPaste(%q) = (%q, %q, %v), want (\"abc\", \"xyz\")", in, code, state, err)
			}
		})
	}

	// U+0085 is NOT in the set: it separates nothing and is not a code
	// character, so the paste has to be refused rather than split (which is
	// what strings.Fields used to do: it produced ok("abc","xyz") here while
	// the browser refused the same paste). The last two rows keep U+0085 glued
	// to a token WHILE a real separator is present, so they exercise the
	// two-token SPLIT itself — a split that used strings.Fields would eat the
	// U+0085 and submit ok("abc","xyz") where the browser sees a token carrying
	// a non-code byte and refuses.
	t.Run("U+0085 is not whitespace", func(t *testing.T) {
		for _, in := range []string{"abc\u0085xyz", "\u0085abc123", "abc123\u0085", "\u0085", "abc\u0085 xyz", "abc xyz\u0085"} {
			t.Run(fmt.Sprintf("%q", in), func(t *testing.T) { assertPasteRefused(t, in) })
		}
	})
}

// TestParseAuthPasteDeniedCodeSanitization pins the two escapes the sanitizer
// exists for: ESC/control bytes collapse to Code == "", and a well-formed but
// hostile code is echoed only within the ≤64-character bound — the documented,
// deliberate trade-off.
func TestParseAuthPasteDeniedCodeSanitization(t *testing.T) {
	// `error=%1B%5B31mhax` decodes to "\x1b[31mhax": an ANSI escape sequence
	// aimed at the terminal a TUI renders in.
	_, _, err := ParseAuthPaste("?error=%1B%5B31mhax")
	var denied *AuthPasteDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("ParseAuthPaste = %v, want *AuthPasteDeniedError", err)
	}
	if denied.Code != "" {
		t.Fatalf("denied.Code = %q, want \"\" (an escape sequence may never be echoed)", denied.Code)
	}
	if strings.ContainsRune(denied.Error(), 0x1b) {
		t.Fatalf("denied message %q carries an ESC byte", denied.Error())
	}
	if denied.Error() != ErrAuthPasteDenied.Error() {
		t.Fatalf("denied message = %q, want the caller's fixed sentence %q", denied.Error(), ErrAuthPasteDenied.Error())
	}

	// A hostile but sanitizer-SHAPED code is echoed (bounded), which is the
	// rule the WebUI must adopt instead of echoing the raw value.
	_, _, err = ParseAuthPaste("?error=" + strings.Repeat("a", 64))
	if !errors.As(err, &denied) || denied.Code != strings.Repeat("a", 64) {
		t.Fatalf("ParseAuthPaste(64-char code) = %v (code %q), want the bounded code echoed", err, denied.Code)
	}
	if got := len(denied.Error()); got != len(ErrAuthPasteDenied.Error())+2+64 {
		t.Fatalf("denied message length = %d, want the fixed sentence plus the code only", got)
	}
}

// TestParseAuthPasteUnrecognizedByDesign pins the two rejected-by-design cases
// the grammar calls out, so a later "helpful" relaxation is a conscious change:
//
//   - `foo=bar` is NOT submitted as a code (two surfaces used to do that);
//   - a bare code with `=` padding is rejected because every `=`-bearing paste
//     is parameter-shaped and carries no recognized parameter.
func TestParseAuthPasteUnrecognizedByDesign(t *testing.T) {
	for _, in := range []string{
		"foo=bar",
		"YWJj=",      // padded base64, no recognized parameter
		"notacode==", // padding again
		"debug=true", // a plausible-looking parameter nobody accepts
		"code",       // a bare word is a code, not a parameter...
	} {
		code, state, err := ParseAuthPaste(in)
		if in == "code" {
			// ...and this row is the CONTROL: `code` alone has no `=`, so it is
			// a perfectly good opaque code and MUST be accepted (the shape
			// check, not the parameter rule, decides here).
			if err != nil || code != "code" || state != "" {
				t.Fatalf("ParseAuthPaste(%q) = (%q, %q, %v), want the bare code accepted", in, code, state, err)
			}
			continue
		}
		if !errors.Is(err, ErrAuthPasteUnrecognized) {
			t.Fatalf("ParseAuthPaste(%q) = (%q, %q, %v), want %v", in, code, state, err, ErrAuthPasteUnrecognized)
		}
		if code != "" || state != "" {
			t.Fatalf("ParseAuthPaste(%q) submitted (%q, %q)", in, code, state)
		}
	}
}

// TestParseAuthPasteGrammarEdges pins the decisions the frozen example table
// leaves implicit, so the three surfaces cannot drift on them either. Each row
// carries its reason.
func TestParseAuthPasteGrammarEdges(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want authPasteOutcome
		code string
	}{
		{
			// A `?` means a query arrived; the "none of code/state/error ⇒
			// unrecognized" rule is stated for the `=`-branch only, so an
			// unknown parameter inside a real query is no-code, not garbage.
			name: "query with only an unknown parameter",
			in:   "?foo=bar",
			want: pasteNoCode,
		},
		{
			// Same, without the `?`: parameter-shaped, but nothing recognized.
			name: "documented counterpart of ?foo=bar",
			in:   "foo=bar",
			want: pasteUnrecognized,
		},
		{
			name: "bare state parameter",
			in:   "state=xyz",
			want: pasteNoCode,
		},
		{
			// No `?` and no `=`: the bare-code branch sees it and the `:` in the
			// scheme fails the shape check. A URL with no query carries no code.
			name: "url without a query",
			in:   "http://127.0.0.1:37401/cb",
			want: pasteUnrecognized,
		},
		{
			// `code=` present but empty still marks the QUERY as carrying a
			// code, so the fragment is not consulted and there is nothing to
			// submit.
			name: "empty query code hides the fragment",
			in:   "http://127.0.0.1:37401/cb?code=#code=abc",
			want: pasteNoCode,
		},
		{
			// A `#` that is not last: the fragment is parsed as the response.
			name: "fragment-only paste",
			in:   "#code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
			// A stray trailing `#` is not a fragment: a usable query survives.
			name: "bare trailing hash keeps the query",
			in:   "code=abc&state=xyz#",
			want: pasteOK,
			code: "abc",
		},
		{
			// state=… truncated at `#`; the fragment's state is not the
			// response's state.
			name: "query state is not read from the fragment",
			in:   "code=abc#state=xyz",
			want: pasteOK,
			code: "abc",
		},
		{
			// A refusal URL pasted with a stray space is NOT a two-token paste.
			name: "refusal url with a stray space",
			in:   "http://127.0.0.1:37401/cb?error=access_denied &x",
			want: pasteUnrecognized,
		},
		{
			name: "error present but empty falls through to the code",
			in:   "?error=&code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
			// `+` is a space in a decoded parameter, exactly as URLSearchParams
			// reads it; the grammar keeps 1:1 byte agreement with the browser.
			name: "plus in a parameter value decodes to a space",
			in:   "?state=a+b",
			want: pasteNoCode,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, state, err := ParseAuthPaste(tc.in)
			if got := authPasteOutcomeOf(err); got != tc.want {
				t.Fatalf("ParseAuthPaste(%q) outcome = %s (%v), want %s", tc.in, got, err, tc.want)
			}
			if code != tc.code {
				t.Fatalf("ParseAuthPaste(%q) code = %q, want %q", tc.in, code, tc.code)
			}
			if err != nil && state != "" {
				t.Fatalf("ParseAuthPaste(%q) returned state %q alongside %v", tc.in, state, err)
			}
		})
	}
}

// TestParseAuthPasteIsPure pins the property the three surfaces compile against:
// ParseAuthPaste has NO hidden state, so the same input yields the same result
// every time, in any order. (A string argument is copied by the language, so
// "does not mutate its argument" is free; what is worth proving is RESULT
// identity — a package-level parser, a cache or a mutated shared table would
// break it.)
func TestParseAuthPasteIsPure(t *testing.T) {
	inputs := []string{
		"abc123",
		"code=abc&state=xyz",
		"http://127.0.0.1:37401/cb?error=access_denied&error_description=x",
		"!!!",
		"",
		"abc xyz",
	}
	type result struct {
		code, state string
		err         error
	}
	snapshot := make([]result, 0, len(inputs))
	for _, in := range inputs {
		c, s, err := ParseAuthPaste(in)
		snapshot = append(snapshot, result{c, s, err})
	}
	for pass := 0; pass < 3; pass++ {
		for i, in := range inputs {
			c, s, err := ParseAuthPaste(in)
			got := result{c, s, err}
			// The SAME input must give the same values, and the errors must be
			// interchangeable (errors.Is both ways) — an identical sentinel, or
			// for a refusal the same sanitized code.
			if got.code != snapshot[i].code || got.state != snapshot[i].state {
				t.Fatalf("pass %d, ParseAuthPaste(%q) = (%q, %q), first call gave (%q, %q)",
					pass, in, got.code, got.state, snapshot[i].code, snapshot[i].state)
			}
			if !authPasteSameError(got.err, snapshot[i].err) {
				t.Fatalf("pass %d, ParseAuthPaste(%q) error = %v, first call gave %v", pass, in, got.err, snapshot[i].err)
			}
		}
	}
}

// authPasteSameError reports whether two outcomes are indistinguishable: both
// nil, the same sentinel in both directions, or refusals carrying the same
// sanitized code.
func authPasteSameError(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Error() != b.Error() {
		return false
	}
	var da, db *AuthPasteDeniedError
	asA, asB := errors.As(a, &da), errors.As(b, &db)
	if asA != asB {
		return false
	}
	if asA && da.Code != db.Code {
		return false
	}
	return errors.Is(a, b) || errors.Is(b, a) ||
		(errors.Is(a, ErrAuthPasteDenied) && errors.Is(b, ErrAuthPasteDenied))
}

// TestParseAuthPasteRejectsOversizedInput is the bound test: a paste is a user's
// one-line hand-off, never a payload, and a hostile blob of thousands of bytes
// must be REFUSED with a short, value-free message rather than submitted as a
// credential.
//
// The bound is the grammar's ONLY size rule and it is on the RAW input, so it
// reaches every shape — including a `code=` PARAMETER, which the grammar
// otherwise keeps verbatim and opaque (RFC 6749 §A.11) and which no per-code
// shape rule could ever bound. There is deliberately no cap on a code itself: a
// Microsoft JWE-wrapped code passes 1000 bytes and must keep working.
func TestParseAuthPasteRejectsOversizedInput(t *testing.T) {
	hostile := []struct {
		name string
		in   string
	}{
		{"bare code over the bound", strings.Repeat("a", 9000)},
		{"hostile punctuation over the bound", strings.Repeat("!", 9000)},
		{"over the bound in every shape at once", "!!! ??? === " + strings.Repeat("a", 9000)},
		{"oversized token pair", strings.Repeat("a", 4500) + " " + strings.Repeat("b", 4500)},
		{"parameter form with an oversized code", "http://127.0.0.1:37401/cb?code=" + strings.Repeat("a", 9000)},
		{"parameter form with an oversized state", "http://127.0.0.1:37401/cb?state=" + strings.Repeat("a", 9000)},
	}
	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			code, state, err := ParseAuthPaste(tc.in)
			if err == nil {
				t.Fatalf("ParseAuthPaste(%d bytes) = (%d-byte code, %q), want a rejection", len(tc.in), len(code), state)
			}
			if code != "" || state != "" {
				t.Fatalf("ParseAuthPaste(%d bytes) submitted (%q, %q)", len(tc.in), code, state)
			}
			if !errors.Is(err, ErrAuthPasteUnrecognized) {
				t.Fatalf("ParseAuthPaste(%d bytes) = %v, want %v", len(tc.in), err, ErrAuthPasteUnrecognized)
			}
			// Short AND value-free: the message is the fixed sentence, never a
			// slice of what was pasted.
			if err.Error() != ErrAuthPasteUnrecognized.Error() {
				t.Fatalf("ParseAuthPaste(%d bytes) message = %q, want the fixed sentence %q", len(tc.in), err.Error(), ErrAuthPasteUnrecognized.Error())
			}
			if got := len(err.Error()); got >= 128 {
				t.Fatalf("error message is %d bytes, want a short fixed sentence: %q", got, err.Error())
			}
			authPasteAssertSafeError(t, err)
		})
	}
}

// TestParseAuthPasteInputBoundIsExact pins WHERE the bound sits: it is the raw
// input length in bytes, `>` and not `>=`, so a paste of exactly 8192 bytes still
// parses (in every form), one byte more does not, and the ~8 KiB a browser can
// realistically produce is comfortably inside. Without the upper half the bound
// could be a no-op; without the lower half a tighter cap could sneak back in.
func TestParseAuthPasteInputBoundIsExact(t *testing.T) {
	paramPrefix := "http://127.0.0.1:37401/cb?code="
	paramRoom := authPasteMaxInputLen - len(paramPrefix)

	accepted := []struct {
		name string
		in   string
		code string
	}{
		{"bare code of 8000 bytes", strings.Repeat("a", 8000), strings.Repeat("a", 8000)},
		{
			"bare code of exactly the bound",
			strings.Repeat("a", authPasteMaxInputLen),
			strings.Repeat("a", authPasteMaxInputLen),
		},
		{
			"parameter code filling the bound exactly",
			paramPrefix + strings.Repeat("a", paramRoom),
			strings.Repeat("a", paramRoom),
		},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.in) > authPasteMaxInputLen {
				t.Fatalf("row %q is %d bytes, outside the bound it means to sit on", tc.name, len(tc.in))
			}
			code, state, err := ParseAuthPaste(tc.in)
			if err != nil || code != tc.code || state != "" {
				t.Fatalf("ParseAuthPaste(%d bytes) = (%d-byte code, %q, %v), want the code accepted", len(tc.in), len(code), state, err)
			}
		})
	}

	refused := []struct {
		name string
		in   string
	}{
		{"bare code of 8193 bytes (one over the bound)", strings.Repeat("a", authPasteMaxInputLen+1)},
		{"parameter code of 8193 bytes (one over the bound)", paramPrefix + strings.Repeat("a", paramRoom+1)},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			code, state, err := ParseAuthPaste(tc.in)
			if !errors.Is(err, ErrAuthPasteUnrecognized) || code != "" || state != "" {
				t.Fatalf("ParseAuthPaste(%d bytes) = (%q, %q, %v), want %v", len(tc.in), code, state, err, ErrAuthPasteUnrecognized)
			}
			authPasteAssertSafeError(t, err)
		})
	}
}

// TestSanitizeOAuthCodeDelegatesToTheEngineRule proves the exported spelling is
// the SAME rule as the engine's sanitizer — one source of truth, no behaviour
// fork, which is the whole reason it is exported.
func TestSanitizeOAuthCodeDelegatesToTheEngineRule(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"registered code", "access_denied", "access_denied"},
		{"vendor code", "vendor.specific-Code_1", "vendor.specific-Code_1"},
		{"empty", "", ""},
		{"one byte too long", strings.Repeat("a", 65), ""},
		{"exactly the bound", strings.Repeat("a", 64), strings.Repeat("a", 64)},
		{"prose with spaces", "the user said no", ""},
		{"escape sequence", "\x1b[31mhax", ""},
		{"angle brackets", "<script>", ""},
		{"non-ASCII", "café", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeOAuthCode(tc.in); got != tc.want {
				t.Fatalf("SanitizeOAuthCode(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// Delegation, not a copy: the two spellings must never diverge.
			if got, want := SanitizeOAuthCode(tc.in), sanitizeOAuthToken(tc.in); got != want {
				t.Fatalf("SanitizeOAuthCode(%q) = %q, sanitizeOAuthToken(%q) = %q: the two spellings diverged", tc.in, got, tc.in, want)
			}
		})
	}
}

// TestAuthPasteOutcomeSentinelsAreDistinct guards the error mapping every
// surface writes: the four sentinels must not satisfy each other's errors.Is, or
// a surface would report one outcome for another.
func TestAuthPasteOutcomeSentinelsAreDistinct(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
	}{
		{"empty", ErrAuthPasteEmpty},
		{"unrecognized", ErrAuthPasteUnrecognized},
		{"no-code", ErrAuthPasteNoCode},
		{"denied", ErrAuthPasteDenied},
	}
	for _, a := range sentinels {
		for _, b := range sentinels {
			if a.name == b.name {
				continue
			}
			if errors.Is(a.err, b.err) {
				t.Errorf("%s sentinel also matches %s", a.name, b.name)
			}
		}
		authPasteAssertSafeError(t, a.err)
	}

	// A refusal with no echoable code must render exactly the fixed sentence: a
	// nil receiver must not panic either.
	if got := (&AuthPasteDeniedError{}).Error(); got != ErrAuthPasteDenied.Error() {
		t.Fatalf("AuthPasteDeniedError{}.Error() = %q, want the fixed sentence %q", got, ErrAuthPasteDenied.Error())
	}
	var nilDenied *AuthPasteDeniedError
	if got := nilDenied.Error(); got != ErrAuthPasteDenied.Error() {
		t.Fatalf("(*AuthPasteDeniedError)(nil).Error() = %q, want the fixed sentence", got)
	}
	if !errors.Is((&AuthPasteDeniedError{}), ErrAuthPasteDenied) {
		t.Fatal("AuthPasteDeniedError does not unwrap to ErrAuthPasteDenied")
	}
}
