package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

const (
	normOpen1  = "\x1b[38;5;252m"
	normOpen2  = "\x1b[38;5;141m"
	normReset  = "\x1b[0m"
	normTrueC  = "\x1b[38;2;12;34;56m"
	normBold   = "\x1b[1m"
	normBgOpen = "\x1b[48;5;236m"
)

func TestMergeAdjacentSGR(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no escapes returns input untouched",
			in:   "plain text line",
			want: "plain text line",
		},
		{
			name: "text containing 'x1b[' without a real ESC byte untouched",
			in:   "literal ESC [ without control: x1b[0m",
			want: "literal ESC [ without control: x1b[0m",
		},
		{
			name: "empty line",
			in:   "",
			want: "",
		},
		{
			name: "stray reset with no open in effect is kept",
			in:   "hello" + normReset,
			want: "hello" + normReset,
		},
		{
			name: "rule1 reset followed by identical open",
			in:   normOpen1 + "a" + normReset + normOpen1 + "b" + normReset,
			want: normOpen1 + "ab" + normReset,
		},
		{
			name: "rule1 not applied when next open differs from run open",
			in:   normOpen1 + "a" + normReset + normOpen2 + "b" + normReset,
			want: normOpen1 + "a" + normReset + normOpen2 + "b" + normReset,
		},
		{
			name: "rule1 kept when a stacked open is also in effect (RESET is meaningful)",
			in:   normBold + normOpen1 + "a" + normReset + normOpen1 + "b" + normReset,
			want: normBold + normOpen1 + "a" + normReset + normOpen1 + "b" + normReset,
		},
		{
			name: "empty run RESET kept while another open is still in effect",
			in:   normOpen1 + "a" + normOpen2 + normReset + "b" + normReset,
			want: normOpen1 + "a" + normReset + "b" + normReset,
		},
		{
			name: "rule2 empty styled run",
			in:   "x" + normOpen1 + normReset + "y",
			want: "xy",
		},
		{
			name: "rule2 empty styled run at line start",
			in:   normOpen1 + normReset + "y",
			want: "y",
		},
		{
			name: "rule3 consecutive identical opens collapse to one",
			in:   normOpen1 + normOpen1 + normOpen1 + "a",
			want: normOpen1 + "a",
		},
		{
			name: "different consecutive opens both kept",
			in:   normOpen1 + normOpen2 + "a",
			want: normOpen1 + normOpen2 + "a",
		},
		{
			name: "glamour token triples merge into one run",
			in:   normOpen1 + " " + normReset + normOpen1 + " " + normReset + normOpen1 + " " + normReset,
			want: normOpen1 + "   " + normReset,
		},
		{
			name: "truecolor open merges with itself like any open",
			in:   normTrueC + "a" + normReset + normTrueC + "b",
			want: normTrueC + "ab",
		},
		{
			name: "bold open merges with itself",
			in:   normBold + "a" + normReset + normBold + "b",
			want: normBold + "ab",
		},
		{
			name: "non-SGR CSI preserved; SGR around it still merges",
			in:   "\x1b[2K" + normOpen1 + "a" + normReset + normOpen1 + "b",
			want: "\x1b[2K" + normOpen1 + "ab",
		},
		{
			name: "non-SGR CSI between reset and open acts as text and is preserved",
			in:   normOpen1 + "a" + normReset + "\x1b[1;2H" + normOpen1 + "b" + normReset,
			want: normOpen1 + "a" + normReset + "\x1b[1;2H" + normOpen1 + "b" + normReset,
		},
		{
			name: "OSC hyperlink bytes preserved",
			in:   "\x1b]8;;http://x\x07" + normOpen1 + "a" + normReset + normOpen1 + "b",
			want: "\x1b]8;;http://x\x07" + normOpen1 + "ab",
		},
		{
			name: "CRLF inside a line is literal text and does not block rule1",
			in:   normOpen1 + "a\r\n" + normReset + normOpen1 + "b",
			want: normOpen1 + "a\r\nb",
		},
		{
			name: "UTF-8 multibyte text preserved",
			in:   normOpen1 + "héllo → 🦞" + normReset + normOpen1 + " mundo ✓",
			want: normOpen1 + "héllo → 🦞 mundo ✓",
		},
		{
			name: "background open is distinct from foreground open (no merge)",
			in:   normBgOpen + "a" + normReset + normOpen1 + "b" + normReset,
			want: normBgOpen + "a" + normReset + normOpen1 + "b" + normReset,
		},
		{
			name: "truncated sequence at end of line passes through",
			in:   normOpen1 + "a" + "\x1b[38;5",
			want: normOpen1 + "a" + "\x1b[38;5",
		},
		{
			name: "double reset: both kept (first terminates the run, second clears inherited state)",
			in:   normOpen1 + "a" + normReset + normReset + normOpen1 + "b",
			want: normOpen1 + "a" + normReset + normReset + normOpen1 + "b",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeAdjacentSGR(tc.in)
			if got != tc.want {
				t.Errorf("mergeAdjacentSGR(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMergeAdjacentSGRVisibleTextIdentical(t *testing.T) {
	inputs := []string{
		normOpen1 + "a" + normReset + normOpen1 + "b" + normReset,
		normOpen1 + normReset + normOpen1 + normReset,
		"\x1b[2K" + normOpen1 + "x" + normReset,
		normOpen1 + normOpen2 + "a" + normReset + normBold + "b",
		normReset + normReset + "a",
		strings.Repeat(normOpen1+" "+normReset, 50),
		normOpen1 + "héllo 🦞" + normReset + normTrueC + "wörld" + normReset + normOpen1 + "!",
	}
	for _, in := range inputs {
		if a, b := ansi.Strip(in), ansi.Strip(mergeAdjacentSGR(in)); a != b {
			t.Errorf("visible text differs after merge:\n in %q\n got %q\nwant %q", in, b, a)
		}
	}
}

func TestMergeLines(t *testing.T) {
	lines := []string{
		"plain",
		normOpen1 + " " + normReset + normOpen1 + " ",
		"",
	}
	out := mergeLines(lines)
	if len(out) != len(lines) || &out[0] != &lines[0] {
		t.Errorf("mergeLines must return the same slice, merged in place")
	}
	if want := normOpen1 + "  "; out[1] != want {
		t.Errorf("merged line = %q, want %q", out[1], want)
	}
}

func TestMergeAdjacentSGRIdempotent(t *testing.T) {
	in := normOpen1 + "a" + normReset + normOpen1 + "b" + normReset +
		normOpen2 + "c" + normReset + normOpen2 + normReset + "d" + normReset
	once := mergeAdjacentSGR(in)
	twice := mergeAdjacentSGR(once)
	if once != twice {
		t.Errorf("merge not idempotent:\nonce  %q\ntwice %q", once, twice)
	}
}
