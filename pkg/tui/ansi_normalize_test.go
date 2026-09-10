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

// Expectations below mirror the exact semantics of mergeAdjacentSGR:
//   - the merge tracks the set of OPENs accumulated since the last effective
//     RESET, because SGR is cumulative in a terminal.
//   - rule 1 (RESET + identical OPEN → drop both) only applies when that OPEN
//     is the SOLE one in effect: with stacked opens (BOLD+FG) the RESET also
//     clears the other attribute, so it must be kept.
//   - rule 2 (OPEN + RESET with no text) drops the pair when nothing else is
//     in effect (the RESET then has no run left to terminate), and keeps the
//     RESET when another OPEN is still in effect (it terminates that outer
//     run). The pair is always dropped together because reapplyBackground
//     (style.go) is a stack machine that pops one run per RESET: an orphan
//     RESET is an over-pop that loses the container background, a lone dropped
//     RESET an under-pop that leaks it.
//   - a RESET outside those rules is NEVER dropped, even with no OPEN in effect
//     within the line: inside a frame the lines are concatenated and
//     lipgloss/reapplyBackground leave SGR state inherited across line breaks,
//     so that RESET is observable — it wipes the inherited attributes.
//   - text between RESET and OPEN does not block rule 1; a non-SGR escape
//     (tokenized as text) does.
//   - a trailing RESET is kept when a run is open (it terminates it at EOL).
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
			// Kept: inside a frame lines are concatenated with SGR state
			// inherited across line breaks, so this RESET is observable — it
			// wipes whatever the previous line left behind.
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
			// Nothing is droppable: the first RESET terminates the O1 run (so O2
			// is not an exact restore), O2 styles "b" (non-empty run → rule 2
			// does not apply) and the trailing RESET terminates the O2 run.
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
			// The OPEN paints nothing, and its RESET then has nothing left to
			// clear within the line — the pair goes together. Keeping the lone
			// RESET would break the OPEN/RESET balance that reapplyBackground
			// (style.go) relies on: it pops one run per RESET, so an orphan RESET
			// discards the container background and leaks the frame colours.
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
			// First RESET terminates the O1 run, second is kept as well (it
			// clears state inherited from the previous line — see above).
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

// TestMergeAdjacentSGRVisibleTextIdentical checks the invariant that must hold
// for ANY input: stripping all escapes yields the same visible text.
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

// TestMergeAdjacentSGRIdempotent guards against the merge introducing new
// mergeable pairs (cache-build sites may re-flow already-merged content, e.g.
// after a width change).
func TestMergeAdjacentSGRIdempotent(t *testing.T) {
	in := normOpen1 + "a" + normReset + normOpen1 + "b" + normReset +
		normOpen2 + "c" + normReset + normOpen2 + normReset + "d" + normReset
	once := mergeAdjacentSGR(in)
	twice := mergeAdjacentSGR(once)
	if once != twice {
		t.Errorf("merge not idempotent:\nonce  %q\ntwice %q", once, twice)
	}
}
