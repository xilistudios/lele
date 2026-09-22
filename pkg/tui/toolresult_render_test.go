package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Regression tests for tool-result / tool-call row rendering. These rows are
// composed pre-styled and MUST fit within the pane width: lineViewport renders
// its lines through a lipgloss Width() pass which word-wraps any over-wide
// styled row mid-content (reflow breaks at hyphens), dropping the "  → "
// prefix and the box border on continuation rows and shifting every row below
// (the bottom bar fell off screen). See renderToolResultBlock.

const timedOutSummary = "Timed out waiting for subagent subagent-3 after 600 seconds (current status: running)"

func resultRows(block string) []string {
	return strings.Split(strings.ReplaceAll(block, "\r\n", "\n"), "\n")
}

func TestToolResultRow_FitsAllWidths(t *testing.T) {
	for _, width := range []int{40, 55, 60, 80, 120} {
		block := renderToolResultBlock(timedOutSummary, width)
		rows := resultRows(block)
		borderCol := -1
		for i, row := range rows {
			if w := ansi.StringWidth(row); w > width {
				t.Fatalf("width=%d row %d overflows: %d cells: %q", width, i, w, ansi.Strip(row))
			}
			col := cellIndex(ansi.Strip(row), '│')
			if col < 0 {
				t.Fatalf("width=%d row %d missing box border: %q", width, i, ansi.Strip(row))
			}
			if borderCol == -1 {
				borderCol = col
			} else if col != borderCol {
				t.Fatalf("width=%d row %d border col %d != %d: %q", width, i, col, borderCol, ansi.Strip(row))
			}
		}
		joined := ansi.Strip(block)
		if !strings.Contains(joined, "subagent-3") {
			t.Fatalf("width=%d: 'subagent-3' split across rows (mid-word re-wrap):\n%s", width, joined)
		}
		if !strings.Contains(rows[0], "→") {
			t.Fatalf("width=%d row 0 missing '→' label: %q", width, ansi.Strip(rows[0]))
		}
		for i, row := range rows[1:] {
			if !strings.HasPrefix(row, "    ") {
				t.Fatalf("width=%d continuation row %d not indented 4: %q", width, i+1, ansi.Strip(row))
			}
		}
	}
}

func TestToolResultRow_MultiLine(t *testing.T) {
	block := renderToolResultBlock("first line of output\nsecond line of output", 60)
	rows := resultRows(block)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %q", len(rows), rows)
	}
	borderCol := -1
	for i, row := range rows {
		if w := ansi.StringWidth(row); w > 60 {
			t.Fatalf("row %d overflows: %d cells: %q", i, w, ansi.Strip(row))
		}
		col := cellIndex(ansi.Strip(row), '│')
		if col < 0 {
			t.Fatalf("row %d missing box border: %q", i, ansi.Strip(row))
		}
		if borderCol == -1 {
			borderCol = col
		} else if col != borderCol {
			t.Fatalf("row %d border col %d != %d: %q", i, col, borderCol, ansi.Strip(row))
		}
	}
	if !strings.Contains(rows[0], "→") {
		t.Fatalf("row 0 missing label: %q", ansi.Strip(rows[0]))
	}
	for i, row := range rows[1:] {
		if !strings.HasPrefix(row, "    ") {
			t.Fatalf("continuation row %d not indented 4: %q", i+1, ansi.Strip(row))
		}
	}
}

func TestToolCallRow_LongNameArgs(t *testing.T) {
	name := "exec: command=" + strings.Repeat("verylongargumentvalue", 12) + " ❤️ 1️⃣ tail"
	for _, width := range []int{40, 80, 120} {
		rows := resultRows(renderToolCallRow(name, width))
		for i, row := range rows {
			if w := ansi.StringWidth(row); w > width {
				t.Fatalf("width=%d row %d overflows: %d cells: %q", width, i, w, ansi.Strip(row))
			}
		}
		joined := ansi.Strip(strings.Join(rows, "\n"))
		if !strings.Contains(joined, "❤️") {
			t.Fatalf("width=%d emoji cluster '❤️' split across rows:\n%s", width, joined)
		}
		if !strings.Contains(joined, "1️⃣") {
			t.Fatalf("width=%d keycap cluster '1️⃣' split across rows:\n%s", width, joined)
		}
		if !strings.HasPrefix(ansi.Strip(rows[0]), "  ") {
			t.Fatalf("width=%d row 0 missing indent: %q", width, ansi.Strip(rows[0]))
		}
		for i, row := range rows[1:] {
			if !strings.HasPrefix(row, "  ") {
				t.Fatalf("width=%d continuation row %d not indented 2: %q", width, i+1, ansi.Strip(row))
			}
		}
	}
}

// TestToolResultRow_FitsWidthUnchangedWhenSmall pins the common case: a short
// summary at a wide pane must render byte-identical to the previous
// composition (label + box) — no visual change for content that already fits.
func TestToolResultRow_FitsWidthUnchangedWhenSmall(t *testing.T) {
	summary := "grep returned 3 matches"
	want := ToolResultLabel.Render("  → ") + ToolResultBox.Render(summary)
	got := renderToolResultBlock(summary, 120)
	if got != want {
		t.Fatalf("small summary rendering changed:\ngot  %q\nwant %q", got, want)
	}
}

// TestView_ToolResultLongSummary_NoRowOverflow is the frame-level cascade
// guard: a very long tool message must not push any frame row past the
// terminal width, and the summary text must not be re-wrapped mid-word.
func TestView_ToolResultLongSummary_NoRowOverflow(t *testing.T) {
	forceTrueColor(t)
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*Model)

	key := "tui:chat:toolresult-overflow"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "run the task")
	m.sessionMgr.AddMessage(key, "tool", timedOutSummary)

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.reloadSessions()
	m.updateViewport()

	frame := m.View()
	for i, line := range strings.Split(frame, "\n") {
		if w := ansi.StringWidth(line); w > 100 {
			t.Fatalf("frame row %d exceeds terminal width: %d cells: %q", i, w, ansi.Strip(line))
		}
	}
	plain := ansi.Strip(frame)
	if !strings.Contains(plain, "subagent-3") {
		t.Fatalf("'subagent-3' was split across rows (mid-word re-wrap):\n%s", plain)
	}
}
