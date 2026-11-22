package tui

import (
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var sgrMouseEscapeRe = regexp.MustCompile(`(?:\x1b)?\[<\d+;\d+;\d+[Mm]`)

func stripMouseEscapeSequences(s string) string {
	return sgrMouseEscapeRe.ReplaceAllString(s, "")
}

func filterAndBufferEscapes(m *Model, msg tea.KeyMsg) ([]rune, bool) {
	runes := make([]rune, 0, len(m.escBuffer)+len(msg.Runes))
	runes = append(runes, m.escBuffer...)
	runes = append(runes, msg.Runes...)
	m.escBuffer = m.escBuffer[:0]

	s := string(runes)
	cleaned := stripMouseEscapeSequences(s)

	incomplete := findTrailingIncompleteEscape(cleaned)
	if incomplete > 0 {
		m.escBuffer = []rune(cleaned[len(cleaned)-incomplete:])
		cleaned = cleaned[:len(cleaned)-incomplete]
	}

	if len(cleaned) == 0 {
		return nil, false
	}

	return []rune(cleaned), true
}

func findTrailingIncompleteEscape(s string) int {
	if len(s) == 0 {
		return 0
	}
	runes := []rune(s)
	n := len(runes)

	for i := n - 1; i >= 0; i-- {
		if runes[i] == 0x1b {
			fragment := string(runes[i:])
			tail := fragment[1:]

			if len(tail) == 0 {
				return n - i
			}
			if strings.HasPrefix(tail, "[<") {
				rest := tail[2:]
				if len(rest) == 0 {
					return n - i
				}
				allParams := true
				for _, c := range rest {
					if !((c >= '0' && c <= '9') || c == ';') {
						allParams = false
						break
					}
				}
				if allParams {
					return n - i
				}
			} else if tail == "[" {
				return n - i
			}
			return 0
		}
	}

	for i := n - 1; i >= 0; i-- {
		if runes[i] == '[' {
			if i+1 >= n {
				return 0
			}
			if runes[i+1] != '<' {
				return 0
			}
			rest := string(runes[i+2:])
			if len(rest) == 0 {
				return n - i
			}
			allParams := true
			for _, c := range rest {
				if !((c >= '0' && c <= '9') || c == ';') {
					allParams = false
					break
				}
			}
			if allParams {
				return n - i
			}
			return 0
		}
	}

	return 0
}

func (m *Model) isEscapeSequenceFragment(msg tea.KeyMsg) bool {
	now := time.Now()

	if m.escSeqActive && now.Sub(m.escSeqLastRune) > 200*time.Millisecond {
		m.escSeqActive = false
	}

	if !m.escSeqActive {
		if msg.Type == tea.KeyEscape {
			m.escSeqActive = true
			m.escSeqLastRune = now
			return true
		}
		for _, r := range msg.Runes {
			if r == 0x1b {
				m.escSeqActive = true
				m.escSeqLastRune = now
				return true
			}
		}
		if time.Since(m.lastEscTime) < 50*time.Millisecond && len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r == '[' || r == '<' {
				m.escSeqActive = true
				m.escSeqLastRune = now
				return true
			}
		}
		return false
	}

	m.escSeqLastRune = now

	runes := msg.Runes
	if len(runes) == 0 {
		m.escSeqActive = false
		return false
	}

	for _, r := range runes {
		switch {
		case r == '[' || r == '<':
		case isCSIIntermediate(r):
		case isCSIFinal(r):
			m.escSeqActive = false
		default:
			m.escSeqActive = false
		}
	}
	return true
}

func isCSIIntermediate(r rune) bool {
	return (r >= '0' && r <= '9') || r == ';' || r == '?' || r == ':' ||
		(r >= ' ' && r <= '/')
}

func isCSIFinal(r rune) bool {
	return r >= 0x40 && r <= 0x7E
}
