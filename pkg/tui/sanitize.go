package tui

import "regexp"

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

// c0ControlRe matches C0 control characters except tab (\x09) and line-feed
// (\x0a), plus DEL (\x7f). These characters, when injected into rendered text,
// would be interpreted by the terminal as e.g. carriage returns (\x0d),
// backspaces, or bell; they must be removed so they cannot corrupt the frame.
var c0ControlRe = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0d\x0e-\x1f\x7f]`)

// sanitizeDisplayText strips terminal control characters and ANSI escape
// sequences from a string destined for terminal display. It preserves \n, \t,
// and all printable Unicode / multi-byte UTF-8. The returned string is safe to
// render without corrupting the terminal frame.
func sanitizeDisplayText(s string) string {
	s = sanitizeEscapeRe.ReplaceAllString(s, "")
	return c0ControlRe.ReplaceAllString(s, "")
}
