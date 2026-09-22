package tui

import (
	"strings"
	"unicode/utf8"

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

// padRightCells pads s with spaces to exactly cells display columns.
func padRightCells(s string, cells int) string {
	w := ansi.StringWidth(s)
	if w >= cells {
		return s
	}
	return s + strings.Repeat(" ", cells-w)
}

// escapeEnd returns the index just past the escape sequence starting at s[i]
// (s[i] must be ESC). It recognises CSI (ESC [ … final byte), OSC (ESC ] …
// BEL / ST) and two-byte escapes; a truncated sequence consumes the rest of
// the string. Used so emoji normalization never rewrites bytes inside an
// escape sequence (SGR state must survive intact).
func escapeEnd(s string, i int) int {
	if i+1 >= len(s) {
		return i + 1
	}
	switch s[i+1] {
	case '[': // CSI: parameter bytes (0x30-0x3F), intermediate (0x20-0x2F), final (0x40-0x7E)
		j := i + 2
		for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3f {
			j++
		}
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
			j++
		}
		if j < len(s) {
			j++ // final byte
		}
		return j
	case ']': // OSC: payload until BEL (0x07) or ST (ESC \)
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
		return len(s)
	default:
		return i + 2
	}
}

// isKeycapBase reports whether r can start a keycap sequence
// ([0-9#*] U+FE0F? U+20E3, e.g. "1️⃣", "#️⃣").
func isKeycapBase(r rune) bool {
	return r == '#' || r == '*' || (r >= '0' && r <= '9')
}

// normalizeEmojiWidth rewrites emoji presentation sequences whose painted
// cell width is terminal-dependent into forms whose ansi.StringWidth equals
// the width real terminals paint, so clampPaneLine can trust its measurement.
// It is the fix for the sidebar separator "line break": lipgloss JoinHorizontal
// positions the │ border by measured width, so any row where measured width
// != painted width displaces the border by the delta on that row only.
//
// Two rewrites, both width-deterministic. Escape sequences are copied through
// byte-for-byte (only plain-text runs are rewritten), so SGR state and
// closeOpenSGR behavior are unaffected:
//
//  1. Keycap sequences ([0-9#*] U+FE0F? U+20E3, e.g. "1️⃣" → "1 "): ansi.StringWidth
//     counts them as 1, but common terminals (Terminal.app, iTerm2, kitty,
//     VTE…) paint the keycap glyph 2 cells wide — the row comes out 1 cell
//     wider than computed and the border shifts on that row. POLICY: replace
//     the whole sequence with the base rune followed by a plain space. We
//     chose base+space over stripping to just the base rune because it
//     preserves the 2-cell footprint terminals give keycaps: content after
//     the keycap stays in the column the terminal would have painted it in,
//     and ansi.StringWidth("1 ") == 2 == painted width, so measurement and
//     paint agree without silently shrinking every keycap row by a cell.
//
//  2. U+FE0F directly after a rune that ansi.StringWidth already counts as 2
//     (e.g. "☕️" U+2615+VS16, "😀️"): dropping the selector is a no-op for the
//     measured width (both sides measure 2) and removes a sequence terminals
//     have historically painted inconsistently. VS16 after width-1 runes
//     ("❤️" U+2764+VS16, "☺️") is KEPT: there the selector is what bumps the
//     cluster to the 2 cells emoji presentation paints, so the measurement
//     already matches the terminal.
//
// Sequences ansi already measures at their painted width ("😀"=2, "🇪🇸"=2,
// "👨‍💻"=2) pass through untouched.
func normalizeEmojiWidth(s string) string {
	if !strings.ContainsAny(s, "\uFE0F\u20E3") {
		return s // fast path: no keycap or variation selector present
	}
	var b strings.Builder
	b.Grow(len(s))
	prev := rune(0) // last visible (non-escape) rune written
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			e := escapeEnd(s, i)
			b.WriteString(s[i:e])
			i = e
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		// Rewrite 1: keycap → base rune + space (measured 2 == painted 2).
		if isKeycapBase(r) {
			rest := s[i+size:]
			off := 0
			nxt, sz := utf8.DecodeRuneInString(rest) // ("", …) → RuneError, 0 → no match
			if nxt == '\uFE0F' {
				off = sz
				nxt, sz = utf8.DecodeRuneInString(rest[off:])
			}
			if nxt == '\u20E3' {
				b.WriteRune(r)
				b.WriteByte(' ')
				prev = r
				i += size + off + sz
				continue
			}
		}
		// Rewrite 2: VS16 after an already-wide rune is a measured no-op.
		if r == '\uFE0F' && prev != 0 && ansi.StringWidth(string(prev)) == 2 {
			i += size
			continue
		}
		b.WriteRune(r)
		prev = r
		i += size
	}
	return b.String()
}

// clampPaneLine forces a pane row to exactly cells display columns: normalize
// ambiguous emoji widths, truncate if too wide, pad with spaces if too short.
//
// Why this exists: macOS Terminal.app leaves stale cells (the sidebar border
// turning into a short cyan bar) when the chat viewport scrolls and a row
// with a wide/VS16 emoji replaces a plain row. lipgloss.Width/MaxWidth alone
// does not guarantee every cell is overwritten; explicit padding does.
// The emoji normalization must run BEFORE measuring: ansi.StringWidth
// undercounts keycap sequences (1 vs painted 2), so a row padded to "cells"
// by measurement still paints cells+1 columns and displaces the │ border.
func clampPaneLine(line string, cells int) string {
	if cells <= 0 {
		return ""
	}
	line = normalizeEmojiWidth(line)
	if ansi.StringWidth(line) > cells {
		line = closeOpenSGR(ansi.Truncate(line, cells, ""))
	}
	// Pad unconditionally so the returned row is EXACTLY cells columns even
	// when a wide rune at the cut point left the truncation a cell short —
	// the separator is positioned by measured width, so underflow breaks it
	// just like overflow does.
	return padRightCells(line, cells)
}

// clampPaneLines applies clampPaneLine to every row of a rendered pane.
func clampPaneLines(pane string, cells int) string {
	if pane == "" || cells <= 0 {
		return pane
	}
	lines := strings.Split(pane, "\n")
	for i, line := range lines {
		lines[i] = clampPaneLine(line, cells)
	}
	return strings.Join(lines, "\n")
}
