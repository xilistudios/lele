package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"regexp"
	"strings"
	"time"
)

// Mouse/escape-sequence sanitization for the TUI input.
//
// Some terminals deliver SGR mouse reports as raw escape bytes that
// bubbletea fails to parse into tea.MouseMsg. These helpers detect and
// strip those fragments so they never reach the chat textarea.

// sgrMouseEscapeRe matches complete SGR mouse escape sequences:
// ESC [ < col ; row ; button M/m
var sgrMouseEscapeRe = regexp.MustCompile(`(?:\x1b)?\[<\d+;\d+;\d+[Mm]`)

// stripMouseEscapeSequences removes all SGR mouse sequences from s.
func stripMouseEscapeSequences(s string) string {
	return sgrMouseEscapeRe.ReplaceAllString(s, "")
}

// filterAndBufferEscapes combines any previously buffered incomplete escape
// fragment (m.escBuffer) with msg.Runes, strips complete SGR mouse escape
// sequences, stores any trailing incomplete escape fragment back into
// m.escBuffer, and returns the cleaned runes plus a boolean indicating
// whether the message should be forwarded to the text input.
func filterAndBufferEscapes(m *Model, msg tea.KeyMsg) ([]rune, bool) {
	// Combine buffer with incoming runes.
	runes := make([]rune, 0, len(m.escBuffer)+len(msg.Runes))
	runes = append(runes, m.escBuffer...)
	runes = append(runes, msg.Runes...)
	m.escBuffer = m.escBuffer[:0]

	s := string(runes)
	cleaned := stripMouseEscapeSequences(s)

	// Check if the cleaned string ends with an incomplete escape fragment
	// (e.g. lone \x1b, \x1b[, \x1b[<, \x1b[<12, etc.) that might be the
	// start of a sequence split across multiple KeyMsg events.
	// We look for ESC at the end followed by optional incomplete CSI bytes.
	incomplete := findTrailingIncompleteEscape(cleaned)
	if incomplete > 0 {
		// Buffer the incomplete tail for next time.
		m.escBuffer = []rune(cleaned[len(cleaned)-incomplete:])
		cleaned = cleaned[:len(cleaned)-incomplete]
	}

	if len(cleaned) == 0 {
		return nil, false // nothing to pass through
	}

	return []rune(cleaned), true
}

// findTrailingIncompleteEscape returns the number of trailing runes that look
// like the start of an incomplete SGR mouse escape sequence (ESC + partial
// CSI, or a headless "[<" partial SGR mouse fragment without the leading ESC).
// Returns 0 if the string doesn't end with such a fragment.
func findTrailingIncompleteEscape(s string) int {
	if len(s) == 0 {
		return 0
	}
	runes := []rune(s)
	n := len(runes)

	// Walk backwards to find the last ESC character.
	for i := n - 1; i >= 0; i-- {
		if runes[i] == 0x1b {
			// Everything from here to the end could be an incomplete sequence.
			fragment := string(runes[i:])
			tail := fragment[1:] // strip leading ESC

			// If the tail is empty (just a lone ESC), it's incomplete.
			if len(tail) == 0 {
				return n - i
			}
			// If it starts with [ or [<  followed by digits/; but no final byte, it's incomplete.
			if strings.HasPrefix(tail, "[<") {
				rest := tail[2:]
				if len(rest) == 0 {
					return n - i // just "\x1b[<"
				}
				// All remaining chars should be digits or ';' for an in-progress sequence.
				allParams := true
				for _, c := range rest {
					if !((c >= '0' && c <= '9') || c == ';') {
						allParams = false
						break
					}
				}
				if allParams {
					return n - i // incomplete: "\x1b[<NNN;NNN"
				}
			} else if tail == "[" {
				return n - i // just "\x1b["
			}
			// Otherwise it's a complete or unrelated sequence — don't buffer.
			return 0
		}
	}

	// No ESC found. Bubbletea may have consumed the ESC byte, leaving a
	// headless partial SGR mouse fragment like "[<", "[<65", or "[<65;57;25".
	// Buffer it if the string ends with "[<" followed only by digits/';'.
	// Scan backwards from the last '[' we can find.
	for i := n - 1; i >= 0; i-- {
		if runes[i] == '[' {
			// Must be followed by '<' (if there's room) to be an SGR mouse fragment.
			if i+1 >= n {
				// Trailing "[" alone — not a mouse fragment, ignore.
				return 0
			}
			if runes[i+1] != '<' {
				return 0
			}
			// "[<" present — check that everything after is digits/';'.
			rest := string(runes[i+2:])
			if len(rest) == 0 {
				return n - i // just "[<"
			}
			allParams := true
			for _, c := range rest {
				if !((c >= '0' && c <= '9') || c == ';') {
					allParams = false
					break
				}
			}
			if allParams {
				return n - i // incomplete headless SGR mouse fragment
			}
			// Contains non-param chars — not a mouse fragment.
			return 0
		}
	}

	return 0
}

// Update is the Bubble Tea entry point. It delegates to update and then
// synchronizes the chat input focus with the current application state, so
// the input cursor is only visible when the input is the active surface
// (no modal open, no pending approval, no onboarding wizard).
func (m *Model) isEscapeSequenceFragment(msg tea.KeyMsg) bool {
	now := time.Now()

	// Step 1: Safety timeout — if we've been in a sequence for >200ms without
	// a new rune, the sequence is stale. Reset and let input through.
	if m.escSeqActive && now.Sub(m.escSeqLastRune) > 200*time.Millisecond {
		m.escSeqActive = false
	}

	// Step 2: Detect START of a new escape sequence.
	if !m.escSeqActive {
		// Case A: bubbletea delivers ESC as tea.KeyEscape.
		if msg.Type == tea.KeyEscape {
			m.escSeqActive = true
			m.escSeqLastRune = now
			return true
		}
		// Case A-2: ESC rune delivered inside msg.Runes (bubbletea grouped bytes)
		for _, r := range msg.Runes {
			if r == 0x1b {
				m.escSeqActive = true
				m.escSeqLastRune = now
				return true
			}
		}
		// Case B: The '[' or '<' that arrives immediately after an ESC
		// (within 50ms) — bubbletea may split ESC from the rest.
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

	// Step 3: We're inside an active escape sequence — classify the rune(s).
	m.escSeqLastRune = now

	runes := msg.Runes
	if len(runes) == 0 {
		// Some KeyMsg types carry no runes (e.g. special keys). These are
		// definitely not CSI bytes. End the sequence and don't consume.
		m.escSeqActive = false
		return false
	}

	for _, r := range runes {
		switch {
		case r == '[' || r == '<':
			// CSI introducer '[' and private-parameter marker '<' — these
			// keep the sequence active (they must NOT be treated as final
			// bytes even though '[' falls within the 0x40-0x7E final range).
		case isCSIIntermediate(r):
			// Valid intermediate byte — stay in sequence.
		case isCSIFinal(r):
			// Terminating byte — sequence is complete.
			m.escSeqActive = false
		default:
			// Unexpected byte. End sequence to avoid getting stuck, but still
			// consume this rune (it's likely garbage from the broken sequence).
			m.escSeqActive = false
		}
	}
	return true
}

// isCSIIntermediate reports whether r is a valid intermediate byte of a CSI
// sequence (parameter bytes 0x30-0x3F and intermediate bytes 0x20-0x2F).
func isCSIIntermediate(r rune) bool {
	return (r >= '0' && r <= '9') || r == ';' || r == '?' || r == ':' ||
		(r >= ' ' && r <= '/') // 0x20-0x2F includes '<' among others
}

// isCSIFinal reports whether r is a valid final byte of a CSI sequence
// (0x40-0x7E).
func isCSIFinal(r rune) bool {
	return r >= 0x40 && r <= 0x7E
}

// resetModal resets the modal state for a new modal.
