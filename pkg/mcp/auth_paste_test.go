package mcp

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type authPasteOutcome string

const (
	pasteOK           authPasteOutcome = "ok"
	pasteEmpty        authPasteOutcome = "empty"
	pasteUnrecognized authPasteOutcome = "unrecognized"
	pasteNoCode       authPasteOutcome = "no-code"
	pasteDenied       authPasteOutcome = "denied"
)

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

const canaryPasteProse = "CANARY-error-description-must-never-be-echoed"

func TestParseAuthPasteSharedTable(t *testing.T) {
	tests := []struct {
		name            string
		in              string
		want            authPasteOutcome
		code            string
		state           string
		deniedCode      string
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
			name: "long opaque real code",
			in:   strings.Repeat("a", 600),
			want: pasteOK,
			code: strings.Repeat("a", 600),
		},
		{
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
			name: "malformed escape in the code",
			in:   "http://127.0.0.1:37401/cb?code=%zz",
			want: pasteUnrecognized,
		},
		{
			name: "malformed escape in the state",
			in:   "http://127.0.0.1:37401/cb?code=abc&state=%zz",
			want: pasteUnrecognized,
		},
		{
			name: "malformed escape in the query beats the fragment",
			in:   "http://127.0.0.1:37401/cb?code=%zz#error=access_denied",
			want: pasteUnrecognized,
		},
		{
			name: "high-bit escape in the code",
			in:   "http://127.0.0.1:37401/cb?code=%C3%A9",
			want: pasteUnrecognized,
		},
		{
			name: "semicolon separator in a parameter value",
			in:   "http://127.0.0.1:37401/cb?code=a;b",
			want: pasteUnrecognized,
		},
		{
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
			name: "U+0085 is not whitespace",
			in:   "abc\u0085xyz",
			want: pasteUnrecognized,
		},
		{
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
		{
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
			name: "hash carrier, two question marks",
			in:   "?a=1#??code=abc",
			want: pasteNoCode,
		},
		{
			name: "bare query, two question marks",
			in:   "??code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
			name: "hash carrier, two question marks inside the carrier",
			in:   "http://127.0.0.1:37401/cb#??code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
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
			if err != nil && (code != "" || state != "") {
				t.Fatalf("ParseAuthPaste(%q) returned a code/state alongside %v", tc.in, err)
			}
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

func TestParseAuthPasteEscapePreCheck(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  authPasteOutcome
		code  string
		state string
	}{
		{"trailing percent", "?code=abc%", pasteUnrecognized, "", ""},
		{"short escape", "?code=abc%8", pasteUnrecognized, "", ""},
		{"percent then no hex digits", "?code=abc%zz", pasteUnrecognized, "", ""},
		{"percent in a non-code parameter", "?error=access_denied&state=%", pasteUnrecognized, "", ""},
		{
			name: "malformed query escape is not rescued by the fragment",
			in:   "?code=abc%#code=xyz",
			want: pasteUnrecognized,
		},
		{"high-bit escape, upper case", "?code=abc%FF", pasteUnrecognized, "", ""},
		{"high-bit escape, lower case", "?code=abc%ff", pasteUnrecognized, "", ""},
		{"high-bit escape, mixed case", "?code=abc%8a", pasteUnrecognized, "", ""},
		{"high-bit escape in the state", "?code=abc&state=%C3%A9", pasteUnrecognized, "", ""},
		{"high-bit escape in the CHOSEN fragment", "?state=xyz#code=%C3%A9", pasteUnrecognized, "", ""},
		{
			name: "high-bit escape in the CHOSEN hash carrier",
			in:   "?state=xyz#?code=%C3%A9",
			want: pasteUnrecognized,
		},
		{"semicolon in the code value", "?code=a;b", pasteUnrecognized, "", ""},
		{"semicolon as a separator", "?code=abc;state=xyz", pasteUnrecognized, "", ""},
		{"semicolon in the chosen fragment", "?state=xyz#code=a;b", pasteUnrecognized, "", ""},
		{"a low escape is ordinary data", "?code=abc%7E", pasteOK, "abc~", ""},
		{"an ESCAPED semicolon is data, not a separator", "?code=a%3Bb", pasteOK, "a;b", ""},
		{"a comma is an ordinary character", "?code=a,b", pasteOK, "a,b", ""},
		{"an ampersand still separates parameters", "?code=a&state=b", pasteOK, "a", "b"},
		{
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

	refused := []string{
		"?code=a;b",
		"?code=abc;state=xyz",
		"?state=xyz#code=a;b",
	}
	for _, in := range refused {
		t.Run("refused "+in, func(t *testing.T) { assertPasteRefused(t, in) })
	}
}

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

func TestParseAuthPasteWhitespaceSetIsECMAScripts(t *testing.T) {
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

	for _, ws := range []string{"\t", "\n", "\r", "\u00a0", "\u3000", "\ufeff"} {
		t.Run("separates "+fmt.Sprintf("%q", ws), func(t *testing.T) {
			in := "abc" + ws + "xyz"
			code, state, err := ParseAuthPaste(in)
			if err != nil || code != "abc" || state != "xyz" {
				t.Errorf("ParseAuthPaste(%q) = (%q, %q, %v), want (\"abc\", \"xyz\")", in, code, state, err)
			}
		})
	}

	t.Run("U+0085 is not whitespace", func(t *testing.T) {
		for _, in := range []string{"abc\u0085xyz", "\u0085abc123", "abc123\u0085", "\u0085", "abc\u0085 xyz", "abc xyz\u0085"} {
			t.Run(fmt.Sprintf("%q", in), func(t *testing.T) { assertPasteRefused(t, in) })
		}
	})
}

func TestParseAuthPasteDeniedCodeSanitization(t *testing.T) {
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

	_, _, err = ParseAuthPaste("?error=" + strings.Repeat("a", 64))
	if !errors.As(err, &denied) || denied.Code != strings.Repeat("a", 64) {
		t.Fatalf("ParseAuthPaste(64-char code) = %v (code %q), want the bounded code echoed", err, denied.Code)
	}
	if got := len(denied.Error()); got != len(ErrAuthPasteDenied.Error())+2+64 {
		t.Fatalf("denied message length = %d, want the fixed sentence plus the code only", got)
	}
}

func TestParseAuthPasteUnrecognizedByDesign(t *testing.T) {
	for _, in := range []string{
		"foo=bar",
		"YWJj=",
		"notacode==",
		"debug=true",
		"code",
	} {
		code, state, err := ParseAuthPaste(in)
		if in == "code" {
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

func TestParseAuthPasteGrammarEdges(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want authPasteOutcome
		code string
	}{
		{
			name: "query with only an unknown parameter",
			in:   "?foo=bar",
			want: pasteNoCode,
		},
		{
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
			name: "url without a query",
			in:   "http://127.0.0.1:37401/cb",
			want: pasteUnrecognized,
		},
		{
			name: "empty query code hides the fragment",
			in:   "http://127.0.0.1:37401/cb?code=#code=abc",
			want: pasteNoCode,
		},
		{
			name: "fragment-only paste",
			in:   "#code=abc",
			want: pasteOK,
			code: "abc",
		},
		{
			name: "bare trailing hash keeps the query",
			in:   "code=abc&state=xyz#",
			want: pasteOK,
			code: "abc",
		},
		{
			name: "query state is not read from the fragment",
			in:   "code=abc#state=xyz",
			want: pasteOK,
			code: "abc",
		},
		{
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
			if got, want := SanitizeOAuthCode(tc.in), sanitizeOAuthToken(tc.in); got != want {
				t.Fatalf("SanitizeOAuthCode(%q) = %q, sanitizeOAuthToken(%q) = %q: the two spellings diverged", tc.in, got, tc.in, want)
			}
		})
	}
}

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
