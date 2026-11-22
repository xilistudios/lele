package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const (
	keycap1 = "1\uFE0F\u20E3"
	keycap2 = "2\uFE0F\u20E3"
	keycapH = "#\uFE0F\u20E3"
	keycapS = "*\uFE0F\u20E3"
	heart   = "\u2764\uFE0F"
	coffee  = "\u2615\uFE0F"
	smiley  = "\u263A\uFE0F"
	grin    = "\U0001F600"
	flagES  = "\U0001F1EA\U0001F1F8"
	zwjTech = "\U0001F468\u200D\U0001F4BB"
)

func TestNormalizeEmojiWidth_MeasuredMatchesPainted(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		painted int
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

func TestClampPaneLines_UniformWidthWithEmoji(t *testing.T) {
	const cells = 24
	pane := strings.Join([]string{
		"plain ascii row",
		grin + " wide emoji",
		heart + " heart",
		"step " + keycap1 + " done",
		zwjTech + " " + flagES,
		"",
	}, "\n")
	out := clampPaneLines(pane, cells)
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w != cells {
			t.Errorf("line %d width = %d, want exactly %d (%q)", i, w, cells, line)
		}
	}
}

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

	bad := 0
	for i, line := range lines {
		plain := ansi.Strip(line)
		col := cellIndex(plain, '│')
		if col < 0 {
			continue
		}
		if col != borderCol {
			t.Errorf("line %d: border at cell %d, want %d\n  plain=%q", i, col, borderCol, clipStr(plain, 120))
			bad++
		}
	}
	if bad > 0 {
		t.Fatalf("%d rows have the sidebar border on the wrong column (first found line=%d col=%d)", bad, borderLine, borderCol)
	}

	for i, line := range lines {
		if w := ansi.StringWidth(line); w > m.width {
			t.Errorf("line %d width=%d > terminal %d: %q", i, w, m.width, clipStr(ansi.Strip(line), 80))
		}
	}

	if strings.Contains(frame, "\u20E3") {
		t.Errorf("frame still contains raw U+20E3 (keycap not normalized): %q", clipStr(ansi.Strip(frame), 200))
	}
	t.Logf("frame lines=%d borderCol=%d term=%dx%d", len(lines), borderCol, m.width, m.height)
}
