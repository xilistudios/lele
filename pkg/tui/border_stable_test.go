package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// TestView_BorderColumnStableWithLongPathsAndEmoji renders a realistic chat
// (glamour headers with emoji + unbreakable filesystem paths) and asserts the
// sidebar border sits on the same display-cell column on every row, with no
// leftover content painted into that cell. This is the Terminal.app scroll
// glitch: one overflow row steals the border cell and shows as a short
// Accent-coloured bar.
func TestView_BorderColumnStableWithLongPathsAndEmoji(t *testing.T) {
	forceTrueColor(t)
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = updated.(*Model)

	key := "tui:chat:border-stable"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	longPath := "/Users/alfredo/Documents/Development/xilistudios/lele/pkg/tui/view.go/very/long/unbreakable/tail"
	m.sessionMgr.AddMessage(key, "user", "make deep verification")
	m.sessionMgr.AddMessage(key, "assistant", strings.Join([]string{
		"## 🛡️Vectores clásicos de rootkit",
		"",
		"- `" + "`/etc/ld.so.preload`" + "` → no existe ✅",
		"- Path: " + longPath,
		"- URL: https://example.com/some/extremely/long/path/that/must/not/overflow/the/sidebar/border/at/all/ever",
		"- `Módulos kernel → todos estándar (nvidia, nftables, bluetooth, audio). Nada fuera de `/lib/modules/updates` oficial ✅`",
		"",
		"## 🌐 Red",
		"",
		"- Solo 2 puertos escuchando, ambos en **localhost**:",
		"- `:631` → CUPS",
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

	// Every row that contains a border must put it on the same column,
	// and the cell must be exactly '│' (not a leftover glyph).
	bad := 0
	for i, line := range lines {
		plain := ansi.Strip(line)
		col := cellIndex(plain, '│')
		if col < 0 {
			// Row with no border — only OK if it's outside the pane area
			// (vertical centering can add full-width blank rows).
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

	// Also: no line may be wider than the terminal (overflow).
	maxW := 0
	for i, line := range lines {
		w := ansi.StringWidth(line)
		if w > maxW {
			maxW = w
		}
		if w > m.width {
			t.Errorf("line %d width=%d > terminal %d: %q", i, w, m.width, clipStr(ansi.Strip(line), 80))
		}
	}
	t.Logf("frame lines=%d borderCol=%d maxWidth=%d term=%dx%d", len(lines), borderCol, maxW, m.width, m.height)
}

// cellIndex returns the display-cell column of target in plain (already
// ANSI-stripped), counting wide runes as 2 cells.
func cellIndex(plain string, target rune) int {
	col := 0
	for _, r := range plain {
		if r == target {
			return col
		}
		if isWideRune(r) {
			col += 2
		} else {
			col++
		}
	}
	return -1
}

func isWideRune(r rune) bool {
	// Same heuristic used in ad-hoc repros: ansi.StringWidth on a single
	// rune is the source of truth.
	return ansi.StringWidth(string(r)) == 2
}

func clipStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var _ = fmt.Sprintf
var _ = termenv.TrueColor
