package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const ellipsis = "..."

const goalBadgePrefix = "🎯 "

func closeOpenSGR(s string) string {
	if strings.Contains(s, "\x1b") && !strings.HasSuffix(s, "\x1b[0m") {
		return s + "\x1b[0m"
	}
	return s
}

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
	n := width - cells + ansi.StringWidth(ellipsis)
	return closeOpenSGR(ansi.TruncateLeft(s, n, ellipsis))
}

func truncateGoalLabel(label string, remaining int) string {
	budget := remaining - ansi.StringWidth(goalBadgePrefix)
	if budget <= 0 {
		return ""
	}
	return truncateRightCells(label, budget)
}

func padRightCells(s string, cells int) string {
	w := ansi.StringWidth(s)
	if w >= cells {
		return s
	}
	return s + strings.Repeat(" ", cells-w)
}

func escapeEnd(s string, i int) int {
	if i+1 >= len(s) {
		return i + 1
	}
	switch s[i+1] {
	case '[':
		j := i + 2
		for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3f {
			j++
		}
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
			j++
		}
		if j < len(s) {
			j++
		}
		return j
	case ']':
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

func isKeycapBase(r rune) bool {
	return r == '#' || r == '*' || (r >= '0' && r <= '9')
}

func normalizeEmojiWidth(s string) string {
	if !strings.ContainsAny(s, "\uFE0F\u20E3") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prev := rune(0)
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			e := escapeEnd(s, i)
			b.WriteString(s[i:e])
			i = e
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if isKeycapBase(r) {
			rest := s[i+size:]
			off := 0
			nxt, sz := utf8.DecodeRuneInString(rest)
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

func clampPaneLine(line string, cells int) string {
	if cells <= 0 {
		return ""
	}
	line = normalizeEmojiWidth(line)
	if ansi.StringWidth(line) > cells {
		line = closeOpenSGR(ansi.Truncate(line, cells, ""))
	}
	return padRightCells(line, cells)
}

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
