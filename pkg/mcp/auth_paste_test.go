package mcp

import (
	"errors"
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
