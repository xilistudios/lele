package tui

import (
	"regexp"
	"strings"
)

// sanitizeEscapeRe matches terminal control sequences that, if left in a rendered
// string, would be interpreted by the terminal as cursor movement, erase, or
// other control directives. It covers:
//   - CSI: \x1b[ + optional [0-9;?] intermediates + final byte in [a-zA-Z~]
//   - OSC: \x1b] ... terminated by BEL (\x07) or ST (\x1b\)
//   - charset selectors: \x1b(...)
//   - cursor save/restore: \x1b7, \x1b8
//   - RIS: \x1bc
//   - \x1b= / \x1b>
var sanitizeEscapeRe = regexp.MustCompile(
	`\x1b\[[0-9;?]*[a-zA-Z~]` +
		`|\x1b\](?:[^\x07]|\x1b\\)*\x07|\x1b\][^\x1b\\]*\x1b\\` +
		`|\x1b[()][0-9A-B]` +
		`|\x1b[78=>]` +
		`|\x1bc`,
)

// trailingEscapeRe matches an incomplete, unterminated escape sequence at the
// end of a string. These are produced when shell output is truncated by bytes:
// a bare ESC, a partial CSI without its final byte ("\x1b[3"), or an unterminated
// OSC ("\x1b]0;title"). Left in place they would confuse the terminal on the next
// frame, so they are removed.
var trailingEscapeRe = regexp.MustCompile(`\x1b(?:\[[0-9;?]*|\][^\x07\x1b]*)?$`)

// c0ControlRe matches C0 control characters except tab (\x09) and line-feed
// (\x0a), plus DEL (\x7f) and C1 control codepoints U+0080–U+009F (which include
// the 8-bit CSI \x9b and 8-bit OSC \x9d). These characters, when injected into
// rendered text, would be interpreted by the terminal as e.g. carriage returns
// (\x0d), backspaces, bell, or 8-bit control directives; they must be removed so
// they cannot corrupt the frame. Tab is deliberately excluded here and instead
// expanded to spaces (see sanitizeDisplayText) because the terminal expands it to
// a variable-width gap that breaks width accounting.
var c0ControlRe = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0d\x0e-\x1f\x7f\x{0080}-\x{009F}]`)

// sanitizeDisplayText strips terminal control characters and ANSI escape
// sequences from a string destined for terminal display. It preserves only \n and
// printable Unicode / multi-byte UTF-8; tabs are expanded to 4 spaces; invalid
// UTF-8 bytes are replaced with the U+FFFD replacement character (visible,
// width 1). The returned string is safe to render without corrupting the
// terminal frame or breaking width accounting.
func sanitizeDisplayText(s string) string {
	// Replace invalid UTF-8 bytes with the U+FFFD replacement character so the
	// width math (runewidth) and the terminal agree on rendering width.
	s = strings.ToValidUTF8(s, "\uFFFD")

	// Strip complete escape sequences (CSI, OSC, charset selectors, etc.).
	s = sanitizeEscapeRe.ReplaceAllString(s, "")

	// Strip trailing incomplete sequences left behind by byte truncation.
	s = trailingEscapeRe.ReplaceAllString(s, "")

	// Catch-all: remove any lone ESC remaining from malformed mid-string sequences.
	s = strings.ReplaceAll(s, "\x1b", "")

	// Remove C0/C1 control characters (tab and \n excluded from the class).
	s = c0ControlRe.ReplaceAllString(s, "")

	// Expand tabs to 4 spaces; tabs otherwise break terminal width math because
	// the terminal expands them to a variable-width gap while runewidth sees 0.
	s = strings.ReplaceAll(s, "\t", "    ")

	return s
}
