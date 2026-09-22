package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// emojiSet is the measured evidence set from the bug report: multi-codepoint
// clusters where per-rune widths disagree with ansi.StringWidth, plus simple
// single-rune emoji that already measured fine.
var emojiSet = []string{"❤️", "👨‍💻", "🇪🇸", "😀", "🔥"}

// clusterBoundaries returns the byte offsets at which grapheme clusters of s
// start, plus len(s) — i.e. the valid places a line break may occur without
// splitting a cluster.
func clusterBoundaries(s string) map[int]bool {
	bounds := map[int]bool{0: true, len(s): true}
	off := 0
	forEachGraphemeCluster(s, func(cluster string, _ int) {
		off += len(cluster)
		bounds[off] = true
	})
	return bounds
}

// TestWrapText_UnbreakableEmojiWordWithinLimit verifies a >limit unbreakable
// word containing multi-codepoint emoji is hard-broken without any output
// line exceeding the limit and without any cluster split across lines.
// Per-rune measurement used to mis-measure these clusters: "❤️" summed to 1
// (full=2) and flags/ZWJ sequences summed to 4 (full=2).
func TestWrapText_UnbreakableEmojiWordWithinLimit(t *testing.T) {
	const limit = 5
	// One unbreakable word: 18 display columns (a,b,c... = 1 each; each
	// emoji cluster = 2). 18 > limit forces the hard-break branch.
	word := "ab❤️cd👨‍💻ef🇪🇸gh😀ij"
	if w := ansi.StringWidth(word); w != 18 {
		t.Fatalf("probe word width = %d, want 18", w)
	}

	got := wrapText(word, limit)
	lines := strings.Split(got, "\n")

	if len(lines) < 2 {
		t.Fatalf("expected hard-break into multiple lines, got %q", got)
	}

	// (a1) No output line may exceed the limit.
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > limit {
			t.Errorf("line %d width %d > limit %d: %q", i, w, limit, line)
		}
	}

	// (a2) No content lost: rejoining the lines reproduces the word.
	if rejoined := strings.ReplaceAll(got, "\n", ""); rejoined != word {
		t.Errorf("chunks lost content: got %q, want rejoin %q", rejoined, word)
	}

	// (a3) No cluster split across lines: every line-break offset must land
	// on a grapheme-cluster boundary of the original word. Rejoining the
	// lines reproduces the word exactly, so cumulative line lengths are the
	// break offsets into the original word.
	bounds := clusterBoundaries(word)
	off := 0
	for _, line := range lines {
		off += len(line)
		if !bounds[off] {
			t.Errorf("line break at byte %d splits a grapheme cluster (line %q)", off, line)
		}
	}

	// Sanity: every emoji cluster appears intact in exactly one line.
	for _, cluster := range emojiSet[:3] { // ❤️ 👨‍💻 🇪🇸
		found := 0
		for _, line := range lines {
			if strings.Contains(line, cluster) {
				found++
			}
		}
		if found != 1 {
			t.Errorf("cluster %q found intact in %d lines, want exactly 1", cluster, found)
		}
	}
}

// TestGraphemeClusterWidthsSumToAnsiStringWidth verifies the helper's
// per-cluster widths sum exactly to ansi.StringWidth of the full string for
// the whole emoji set (acceptance criterion b).
func TestGraphemeClusterWidthsSumToAnsiStringWidth(t *testing.T) {
	cases := append([]string{}, emojiSet...)
	cases = append(cases,
		"a❤️b👨‍💻c🇪🇸d😀e", // mixed ASCII + clusters
		"plain ascii",
		"你好世界", // CJK
		"",
	)

	for _, s := range cases {
		sum := 0
		clusters := 0
		forEachGraphemeCluster(s, func(cluster string, width int) {
			// Each cluster's width must equal ansi.StringWidth of that cluster.
			if want := ansi.StringWidth(cluster); width != want {
				t.Errorf("cluster %q width = %d, want ansi.StringWidth = %d", cluster, width, want)
			}
			sum += width
			clusters++
		})
		if sum != ansi.StringWidth(s) {
			t.Errorf("%q: cluster width sum = %d, want ansi.StringWidth = %d", s, sum, ansi.StringWidth(s))
		}
		if s != "" && clusters == 0 {
			t.Errorf("%q: no clusters produced", s)
		}
	}
}

// TestWrapText_ASCIIRegression captures the pre-change wrapText output for
// plain ASCII inputs (captured from the original rune-based implementation).
// ASCII must remain byte-identical after switching to cluster iteration.
func TestWrapText_ASCIIRegression(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		limit int
		want  string
	}{
		{"simple wrap", "hello world foo", 11, "hello world\nfoo"},
		{"hard break", "aaaaaaaaaaaaaaaaaaaa", 10, "aaaaaaaaaa\naaaaaaaaaa"},
		{"hard break then join", "aaaaaaaaaabbb cc", 10, "aaaaaaaaaa\nbbb cc"},
		{"exact limit", "abcdefghij", 10, "abcdefghij"},
		{"long url", "see https://example.com/very/long/path/that/keeps/going/on forever", 20,
			"see\nhttps://example.com/\nvery/long/path/that/\nkeeps/going/on\nforever"},
		{"sentence wrap", "the quick brown fox jumps over the lazy dog", 13,
			"the quick\nbrown fox\njumps over\nthe lazy dog"},
		{"multiline input", "line one two three\nsecond line", 9,
			"line one\ntwo three\nsecond\nline"},
		{"empty", "", 5, ""},
		{"zero limit passthrough", "x", 0, "x"},
		{"whitespace only", "   ", 2, ""},
		{"small limit", "a bb ccc dddd ee", 4, "a bb\nccc\ndddd\nee"},
		{"equal words", "word1 word2 word3 word4", 10, "word1\nword2\nword3\nword4"},
	}

	for _, c := range cases {
		if got := wrapText(c.text, c.limit); got != c.want {
			t.Errorf("%s: wrapText(%q, %d) = %q, want %q", c.name, c.text, c.limit, got, c.want)
		}
	}
}

// TestSplitByColumns_EmojiClusterAgreement verifies selection column mapping
// agrees with ansi.StringWidth: multi-codepoint emoji occupy one cluster of
// the width the terminal reports, and cluster boundaries are never split.
func TestSplitByColumns_EmojiClusterAgreement(t *testing.T) {
	s := "a❤️b" // widths: a=1, ❤️=2, b=1 → total 4
	if total := ansi.StringWidth(s); total != 4 {
		t.Fatalf("test string width = %d, want 4", total)
	}

	// Aligned boundaries select whole clusters.
	before, selected, after := splitByColumns(s, 1, 3)
	if before != "a" || selected != "❤️" || after != "b" {
		t.Errorf("splitByColumns(%q, 1, 3) = (%q, %q, %q), want (%q, %q, %q)",
			s, before, selected, after, "a", "❤️", "b")
	}

	// Full range preserves the string exactly.
	before, selected, after = splitByColumns(s, 0, ansi.StringWidth(s))
	if before+selected+after != s || selected != s {
		t.Errorf("full-range split lost content: (%q, %q, %q)", before, selected, after)
	}

	// A boundary falling inside a cluster must not split it: the cluster
	// stays whole in exactly one of the three parts.
	flag := "🇪🇸x" // flag=2, x=1 → total 3
	before, selected, after = splitByColumns(flag, 1, 3)
	if before+selected+after != flag {
		t.Errorf("split lost content: (%q, %q, %q)", before, selected, after)
	}
	whole := 0
	for _, part := range []string{before, selected, after} {
		if strings.Contains(part, "🇪🇸") {
			whole++
		}
	}
	if whole != 1 {
		t.Errorf("flag cluster split across parts: (%q, %q, %q)", before, selected, after)
	}
	// Aligned flag boundary selects the whole flag.
	before, selected, after = splitByColumns(flag, 0, 2)
	if selected != "🇪🇸" || after != "x" {
		t.Errorf("splitByColumns(%q, 0, 2) = (%q, %q, %q), want flag then x", flag, before, selected, after)
	}
}
