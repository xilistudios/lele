package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// bidiStripCodepoints is the exact strip list documented on bidiFormatRe in
// sanitize.go. Keep in sync with that regexp.
//
// WHY: these invisible FORMAT characters make the terminal's painted output
// disagree with ansi.StringWidth/runewidth measurement (bidi overrides REORDER
// rendered text; zero-width/soft-hyphen marks are painted nondeterministically),
// which shifts every cell after them on the row and visibly breaks borders and
// line layout.
var bidiStripCodepoints = []struct {
	name string
	r    rune
}{
	{"SOFT_HYPHEN", '\u00AD'},
	{"MONGOLIAN_VOWEL_SEPARATOR", '\u180E'},
	{"ZERO_WIDTH_SPACE", '\u200B'},
	{"LEFT_TO_RIGHT_MARK", '\u200E'},
	{"RIGHT_TO_LEFT_MARK", '\u200F'},
	{"LRE", '\u202A'},
	{"RLE", '\u202B'},
	{"PDF", '\u202C'},
	{"LRO", '\u202D'},
	{"RLO", '\u202E'},
	{"WORD_JOINER", '\u2060'},
	{"LRI", '\u2066'},
	{"RLI", '\u2067'},
	{"FSI", '\u2068'},
	{"PDI", '\u2069'},
	{"ZWNBSP_BOM", '\uFEFF'},
}

// isBidiStripRune reports whether r is on the strip list. Mirrors bidiFormatRe
// in sanitize.go — implemented as an independent rune switch (not the
// production regexp) so the width-invariant test does not merely test itself.
func isBidiStripRune(r rune) bool {
	switch r {
	case '\u00AD', '\u180E', '\u200B', '\u200E', '\u200F', '\u2060', '\uFEFF':
		return true
	}
	return (r >= '\u202A' && r <= '\u202E') || (r >= '\u2066' && r <= '\u2069')
}

// removeStripList returns s with exactly the strip-list codepoints removed.
func removeStripList(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !isBidiStripRune(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// (a) Every codepoint in the strip list is removed: alone, between ASCII
// chars, and at string start/end.
func TestSanitizeDisplayText_StripsBidiFormatChars(t *testing.T) {
	for _, c := range bidiStripCodepoints {
		ch := string(c.r)
		tests := []struct {
			name  string
			input string
			want  string
		}{
			{"alone", ch, ""},
			{"between_ascii", "a" + ch + "b", "ab"},
			{"string_start", ch + "ab", "ab"},
			{"string_end", "ab" + ch, "ab"},
		}
		for _, tt := range tests {
			t.Run(c.name+"/"+tt.name, func(t *testing.T) {
				got := sanitizeDisplayText(tt.input)
				if got != tt.want {
					t.Errorf("sanitizeDisplayText(%q) = %q, want %q", tt.input, got, tt.want)
				}
			})
		}
	}
}

// (b) Content that must survive sanitization untouched: emoji sequences that
// depend on ZWJ/variation selectors/keycap codepoints, plain text, newlines,
// and the tab-expansion policy.
func TestSanitizeDisplayText_PreservesEmojiAndText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// U+200D ZWJ is deliberately kept: it binds multi-emoji clusters.
		{"ZWJ cluster (kept)", "\U0001F468\u200D\U0001F4BB", "\U0001F468\u200D\U0001F4BB"},
		// U+FE0F VS16 / U+FE0E VS15 are deliberately kept (normalizeEmojiWidth).
		{"VS16 heart (kept)", "\u2764\uFE0F", "\u2764\uFE0F"},
		{"VS15 text (kept)", "a\uFE0Eb", "a\uFE0Eb"},
		// U+20E3 keycap is deliberately kept (normalizeEmojiWidth policy).
		{"keycap incl U+20E3 (kept)", "1\uFE0F\u20E3", "1\uFE0F\u20E3"},
		{"simple emoji", "\U0001F600", "\U0001F600"},
		{"accented latin", "café", "café"},
		{"CJK", "日本語", "日本語"},
		{"CJK mixed", "中文测试", "中文测试"},
		{"newline preserved", "a\nb", "a\nb"},
		// Tab-expansion policy unchanged: tab -> 4 spaces.
		{"tab expands to 4 spaces", "a\tb", "a    b"},
		{"tab at start expands", "\tx", "    x"},
		{"tab and newline together", "a\tb\nc", "a    b\nc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeDisplayText(tt.input); got != tt.want {
				t.Errorf("sanitizeDisplayText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// (c) Combined attack string: several strip-list chars interleaved with
// visible text collapse to the plain concatenation.
//
// Exact U+00AD (soft hyphen) policy: FULLY removed — no hyphen is ever
// painted (some terminals would paint one, others not, which is exactly the
// nondeterminism we are eliminating). So "gh" + U+00AD + "ij" becomes "ghij"
// with no separator, yielding "abcdefghij".
func TestSanitizeDisplayText_CombinedBidiZeroWidthAttack(t *testing.T) {
	in := "ab\u202Ecd\u200Bef\uFEFFgh\u00ADij"
	want := "abcdefghij"
	got := sanitizeDisplayText(in)
	if got != want {
		t.Errorf("sanitizeDisplayText(%q) = %q, want %q", in, got, want)
	}

	// Mixed with content that must survive: ZWJ cluster + RLO + text.
	in2 := "\U0001F468\u200D\U0001F4BB\u202Ebold"
	want2 := "\U0001F468\u200D\U0001F4BBbold"
	if got := sanitizeDisplayText(in2); got != want2 {
		t.Errorf("sanitizeDisplayText(%q) = %q, want %q", in2, got, want2)
	}
}

// (d) Regression: the pre-existing control/escape stripping still works.
func TestSanitizeDisplayText_StillStripsControlsAndEscapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"SGR sequences", "hello \x1b[31mred\x1b[0m world", "hello red world"},
		{"carriage return (C0)", "line1\rline2", "line1line2"},
		{"bell (C0)", "beep\x07", "beep"},
		{"C1 CSI", "a\u009Bb", "ab"},
		{"truncated CSI at end", "hello \x1b[3", "hello "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeDisplayText(tt.input); got != tt.want {
				t.Errorf("sanitizeDisplayText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// bidiWidthCorpus returns every kind of input exercised by the tests above so
// the width invariant can be checked across all of them.
func bidiWidthCorpus() []string {
	var corpus []string
	for _, c := range bidiStripCodepoints {
		ch := string(c.r)
		corpus = append(corpus, ch, "a"+ch+"b", ch+"ab", "ab"+ch)
	}
	return append(corpus,
		// preserved content
		"\U0001F468\u200D\U0001F4BB", // 👨‍💻 ZWJ cluster
		"\u2764\uFE0F",               // ❤️ VS16
		"a\uFE0Eb",                   // VS15
		"1\uFE0F\u20E3",              // 1️⃣ keycap
		"\U0001F600",                 // 😀
		"café",
		"日本語",
		"中文测试",
		"a\nb",
		"\n",
		// tab strings: see the tab-policy note in the invariant test
		"a\tb",
		"\tx",
		"end\t",
		"a\tb\nc",
		// combined attack
		"ab\u202Ecd\u200Bef\uFEFFgh\u00ADij",
		"\U0001F468\u200D\U0001F4BB\u202Ebold",
		// control/escape regressions
		"hello \x1b[31mred\x1b[0m world",
		"line1\rline2",
		"beep\x07",
		"a\u009Bb",
		"hello \x1b[3",
	)
}

// (e) Width invariant: stripping invisible characters must never change the
// measured width of VISIBLE content.
//
//	ansi.StringWidth(sanitizeDisplayText(s)) == ansi.StringWidth(s with strip-list removed)
//
// The only documented adjustment is tab expansion: sanitizeDisplayText applies
// the pre-existing policy tab -> 4 spaces (tab measures 0 cells, the four
// spaces measure 4). That policy is intentionally width-changing and is
// unrelated to the strip list, so the reference side applies the same tab
// policy to isolate strip-list effects. Strings with invalid UTF-8 are also
// out of scope here: their U+FFFD replacement is a separate, intentionally
// width-changing policy (covered by sanitize_test.go).
func TestSanitizeDisplayText_WidthInvariant(t *testing.T) {
	for i, s := range bidiWidthCorpus() {
		got := ansi.StringWidth(sanitizeDisplayText(s))

		want := removeStripList(s)
		if strings.ContainsRune(want, '\t') {
			want = strings.ReplaceAll(want, "\t", "    ")
		}
		if w := ansi.StringWidth(want); got != w {
			t.Errorf("case %d %q: width(sanitized) = %d, want %d (width of strip-removed reference %q)",
				i, s, got, w, want)
		}
	}
}
