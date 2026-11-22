package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

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
