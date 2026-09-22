package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Emoji width normalization tests for clampPaneLine/normalizeEmojiWidth.
//
// Context: the sidebar border │ is positioned by measured width
// (ansi.StringWidth via lipgloss JoinHorizontal). Any row where the measured
// width disagrees with what the terminal paints displaces the border by the
// delta on that row only — the reported "separator line break". The
// normalization policy lives in normalizeEmojiWidth (pkg/tui/truncate.go):
//
//   - keycap [0-9#*] U+FE0F? U+20E3: measured 1, painted 2 → replaced by
//     base rune + space ("1️⃣" → "1 "), measured 2 == painted 2;
//   - U+FE0F after an already width-2 rune: stripped (measured no-op);
//   - U+FE0F after a width-1 rune ("❤️", "☺️"): kept (it is what makes the
//     cluster the 2 cells emoji presentation paints);
//   - everything else ansi already measures at painted width: untouched.
const (
	keycap1 = "1\uFE0F\u20E3"              // 1️⃣
	keycap2 = "2\uFE0F\u20E3"              // 2️⃣
	keycapH = "#\uFE0F\u20E3"              // #️⃣
	keycapS = "*\uFE0F\u20E3"              // *️⃣
	heart   = "\u2764\uFE0F"               // ❤️ (U+2764 width 1 + VS16 → 2)
	coffee  = "\u2615\uFE0F"               // ☕️ (U+2615 already width 2 → VS16 stripped)
	smiley  = "\u263A\uFE0F"               // ☺️ (U+263A width 1 + VS16 → 2)
	grin    = "\U0001F600"                 // 😀
	flagES  = "\U0001F1EA\U0001F1F8"       // 🇪🇸
	zwjTech = "\U0001F468\u200D\U0001F4BB" // 👨‍💻
)

// TestNormalizeEmojiWidth_MeasuredMatchesPainted is the policy table: for
// every normalized form we produce, ansi.StringWidth of the FINAL string must
// equal the width the terminal is assumed to paint (the "painted" column).
// The painted widths are the documented assumptions of the fix:
// keycap=2, VS16-after-wide stripped width=2, VS16-after-narrow kept width=2,
// plain wide/flag/ZWJ emoji=2, ASCII per byte.
func TestNormalizeEmojiWidth_MeasuredMatchesPainted(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string // exact normalized output (documents rewrites)
		painted int    // width the terminal is assumed to paint
	}{
		{"keycap digit", keycap1, "1 ", 2},
		{"keycap digit without VS16", "1\u20E3", "1 ", 2},
		{"keycap hash", keycapH, "# ", 2},
		{"keycap star", keycapS, "* ", 2},
		{"two keycaps", keycap1 + keycap2, "1 2 ", 4},
		{"VS16 after wide coffee", coffee, "\u2615", 2},
		{"VS16 after wide grin", grin + "\uFE0F", grin, 2},
		{"VS16 after wide CJK", "\u6C49\uFE0F", "\u6C49", 2},
		{"VS16 after narrow heart kept", heart, heart, 2},
		{"VS16 after narrow smiley kept", smiley, smiley, 2},
		{"plain wide emoji untouched", grin, grin, 2},
		{"flag untouched", flagES, flagES, 2},
		{"zwj sequence untouched", zwjTech, zwjTech, 2},
		{"plain ascii untouched", "hello", "hello", 5},
		{"mixed narrow VS16 text untouched", "a" + heart + "b", "a" + heart + "b", 4},
		{"SGR + keycap escapes preserved", "\x1b[31m" + keycap1 + "\x1b[0m", "\x1b[31m1 \x1b[0m", 2},
		{"SGR + heart escapes preserved", "\x1b[31m" + heart + "\x1b[0m", "\x1b[31m" + heart + "\x1b[0m", 2},
		{"SGR + wide emoji untouched", "\x1b[1;32m" + grin + "\x1b[0m", "\x1b[1;32m" + grin + "\x1b[0m", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeEmojiWidth(tt.in)
			if got != tt.want {
				t.Fatalf("normalizeEmojiWidth(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if w := ansi.StringWidth(got); w != tt.painted {
				t.Errorf("ansi.StringWidth(%q) = %d, want %d (assumed painted width)", got, w, tt.painted)
			}
		})
	}
}

// TestClampPaneLine_ExactWidthWithEmoji asserts the clampPaneLine invariant:
// the returned row measures exactly `cells` columns for every input class
// from the acceptance criteria, and content is never silently deleted —
// except the documented keycap replacement (sequence → base + space) and the
// documented no-op VS16 strip after already-wide runes.
func TestClampPaneLine_ExactWidthWithEmoji(t *testing.T) {
	const cells = 20
	tests := []struct {
		name       string
		in         string
		contains   []string
		notContain []string
	}{
		{"plain ascii", "hello world", []string{"hello world"}, nil},
		{"wide emoji", grin, []string{grin}, nil},
		{"VS16 heart", heart, []string{heart}, nil},
		{"keycap", keycap1, []string{"1"}, []string{"\u20E3", "\uFE0F"}},
		{"flag", flagES, []string{flagES}, nil},
		{"zwj emoji", zwjTech, []string{zwjTech}, nil},
		{"VS16 coffee (strip is no-op)", coffee, []string{"\u2615"}, []string{"\uFE0F"}},
		{"VS16 smiley kept", smiley, []string{smiley}, nil},
		{"mixed content", "ok: " + grin + " " + heart + " " + keycap1 + " done",
			[]string{"ok: ", grin, heart, "1 ", "done"}, []string{"\u20E3"}},
		{"SGR color + emoji", "\x1b[31m" + heart + " red\x1b[0m",
			[]string{"\x1b[31m", heart, "red", "\x1b[0m"}, nil},
		{"SGR color + keycap", "\x1b[31m" + keycap1 + "\x1b[0m",
			[]string{"\x1b[31m", "1 ", "\x1b[0m"}, []string{"\u20E3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampPaneLine(tt.in, cells)
			if w := ansi.StringWidth(got); w != cells {
				t.Errorf("ansi.StringWidth(clampPaneLine(%q, %d)) = %d, want exactly %d (%q)",
					tt.in, cells, w, cells, got)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("result %q does not contain %q (content must not be silently deleted)", got, want)
				}
			}
			for _, bad := range tt.notContain {
				if strings.Contains(got, bad) {
					t.Errorf("result %q must not contain %q (normalized away by policy)", got, bad)
				}
			}
		})
	}
}

// TestClampPaneLine_TruncationStaysExact exercises the too-wide path: after
// normalization the row is truncated and then padded back, so even a wide
// rune at the cut point cannot leave the row a cell short of `cells`.
func TestClampPaneLine_TruncationStaysExact(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		cells int
	}{
		{"wide rune at cut point", strings.Repeat("a", 3) + grin + strings.Repeat("b", 5), 4},
		{"keycaps overflow", keycap1 + keycap2 + keycap1, 3},
		{"keycap truncated to base", keycap1, 1},
		{"long ascii", strings.Repeat("x", 50), 10},
		{"sgr + emoji long", "\x1b[31m" + strings.Repeat("y", 30) + grin + "\x1b[0m", 8},
		{"emoji run", grin + heart + coffee + smiley + zwjTech, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampPaneLine(tt.in, tt.cells)
			if w := ansi.StringWidth(got); w != tt.cells {
				t.Errorf("ansi.StringWidth(clampPaneLine(%q, %d)) = %d, want exactly %d (%q)",
					tt.in, tt.cells, w, tt.cells, got)
			}
			if strings.Contains(got, "\u20E3") {
				t.Errorf("result %q still contains U+20E3 (keycap not normalized)", got)
			}
		})
	}
}

// TestClampPaneLines_UniformWidthWithEmoji covers the pane-level helper used
// by the sidebar layout: every row of a mixed pane comes out at the same
// exact width so JoinHorizontal keeps the │ on one column.
func TestClampPaneLines_UniformWidthWithEmoji(t *testing.T) {
	const cells = 24
	pane := strings.Join([]string{
		"plain ascii row",
		grin + " wide emoji",
		heart + " heart",
		"step " + keycap1 + " done",
		zwjTech + " " + flagES,
		"", // empty row must be padded too
	}, "\n")
	out := clampPaneLines(pane, cells)
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w != cells {
			t.Errorf("line %d width = %d, want exactly %d (%q)", i, w, cells, line)
		}
	}
}

// TestView_BorderColumnStableWithKeycapAndVS16Emoji is a variant of
// border_stable_test.go's TestView_BorderColumnStableWithLongPathsAndEmoji
// with the sequences that measured 1 but paint 2 (keycap) and the VS16
// heart in the message text: the sidebar border must sit on the same
// display-cell column on every row, and no raw keycap sequence may reach
// the frame un-normalized.
func TestView_BorderColumnStableWithKeycapAndVS16Emoji(t *testing.T) {
	forceTrueColor(t)
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = updated.(*Model)

	key := "tui:chat:border-keycap-vs16"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	longPath := "/Users/alfredo/Documents/Development/xilistudios/lele/pkg/tui/view.go/very/long/unbreakable/tail"
	m.sessionMgr.AddMessage(key, "user", "estado de la verificación")
	m.sessionMgr.AddMessage(key, "assistant", strings.Join([]string{
		"## Estado 1️⃣ final",
		"",
		"- Corazón ❤️ y café ☕️ en la misma línea",
		"- Keycap 1️⃣2️⃣ en medio del texto",
		"- Path: " + longPath,
		"- Fin con ❤️ y smiley ☺️",
	}, "\n"))

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.reloadSessions()
	m.updateViewport()

	frame := m.View()
	lines := strings.Split(frame, "\n")
	if len(lines) == 0 {
		t.Fatal("empty frame")
	}

	// Discover the border column from a line that definitely has it.
	borderCol := -1
	borderLine := -1
	for i, line := range lines {
		plain := ansi.Strip(line)
		col := cellIndex(plain, '│')
		if col >= 0 {
			borderCol = col
			borderLine = i
			break
		}
	}
	if borderCol < 0 {
		t.Fatalf("no sidebar border found in frame (%d lines)", len(lines))
	}

	// Every row that contains a border must put it on the same column.
	bad := 0
	for i, line := range lines {
		plain := ansi.Strip(line)
		col := cellIndex(plain, '│')
		if col < 0 {
			continue // rows outside the pane area (vertical centering blanks)
		}
		if col != borderCol {
			t.Errorf("line %d: border at cell %d, want %d\n  plain=%q", i, col, borderCol, clipStr(plain, 120))
			bad++
		}
	}
	if bad > 0 {
		t.Fatalf("%d rows have the sidebar border on the wrong column (first found line=%d col=%d)", bad, borderLine, borderCol)
	}

	// No row may exceed the terminal width.
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > m.width {
			t.Errorf("line %d width=%d > terminal %d: %q", i, w, m.width, clipStr(ansi.Strip(line), 80))
		}
	}

	// The keycap sequences must have been normalized before measuring, so no
	// raw U+20E3 may survive into the frame.
	if strings.Contains(frame, "\u20E3") {
		t.Errorf("frame still contains raw U+20E3 (keycap not normalized): %q", clipStr(ansi.Strip(frame), 200))
	}
	t.Logf("frame lines=%d borderCol=%d term=%dx%d", len(lines), borderCol, m.width, m.height)
}
