package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// ellipsis is the tail/head marker used by the cell-aware truncation helpers.
const ellipsis = "..."

// goalBadgePrefix is the fixed prefix rendered before a goal label in the
// status line. Its display width must be subtracted from the label budget.
const goalBadgePrefix = "🎯 "

// closeOpenSGR appends an SGR reset when s ends with an unterminated escape
// sequence state (colour bleed guard). ansi.Truncate/TruncateLeft preserve
// the active style of the kept text but do not always close it.
func closeOpenSGR(s string) string {
	if strings.Contains(s, "\x1b") && !strings.HasSuffix(s, "\x1b[0m") {
		return s + "\x1b[0m"
	}
	return s
}

// truncateRightCells cuts s so it occupies at most cells terminal columns,
// appending an ellipsis when text is cut. Unlike a []rune slice it is
// ANSI-safe — it never splits an escape sequence mid-flight (which would
// bleed colour into following lines) — and it measures grapheme clusters by
// display width, so emoji and East-Asian text are counted correctly.
// When cells is too small to fit an ellipsis alongside any content, the tail
// marker is dropped (ansi.Truncate would otherwise collapse to "").
func truncateRightCells(s string, cells int) string {
	if cells <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= cells {
		return s
	}
	if cells < ansi.StringWidth(ellipsis) {
		return closeOpenSGR(ansi.Truncate(s, cells, ""))
	}
	return closeOpenSGR(ansi.Truncate(s, cells, ellipsis))
}

// truncateLeftCells keeps the final cells terminal columns of s, prefixing an
// ellipsis when text is cut from the left. Used for workspace paths, where
// the tail (directory name) carries more information than the head. Like
// truncateRightCells it is ANSI- and wide-character-aware. When cells is too
// small to hold an ellipsis plus at least one content cell, the function
// degrades to a right-truncation so the result still fits the budget.
func truncateLeftCells(s string, cells int) string {
	if cells <= 0 {
		return ""
	}
	width := ansi.StringWidth(s)
	if width <= cells {
		return s
	}
	if cells < ansi.StringWidth(ellipsis)+1 {
		return truncateRightCells(s, cells)
	}
	// ansi.TruncateLeft removes n cells from the start and prepends prefix,
	// so the result is width-n+prefixWidth cells; solve for n to fit cells.
	n := width - cells + ansi.StringWidth(ellipsis)
	return closeOpenSGR(ansi.TruncateLeft(s, n, ellipsis))
}

// truncateGoalLabel fits a goal badge ("🎯 " + label) into remaining display
// cells, truncating the label by cells (not runes) and adding an ellipsis
// when cut.
func truncateGoalLabel(label string, remaining int) string {
	budget := remaining - ansi.StringWidth(goalBadgePrefix)
	if budget <= 0 {
		return ""
	}
	return truncateRightCells(label, budget)
}
