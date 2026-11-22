package tui

import (
	"regexp"
	"strings"
)

var sanitizeEscapeRe = regexp.MustCompile(
	`\x1b\[[0-9;?]*[a-zA-Z~]` +
		`|\x1b\](?:[^\x07]|\x1b\\)*\x07|\x1b\][^\x1b\\]*\x1b\\` +
		`|\x1b[()][0-9A-B]` +
		`|\x1b[78=>]` +
		`|\x1bc`,
)

var trailingEscapeRe = regexp.MustCompile(`\x1b(?:\[[0-9;?]*|\][^\x07\x1b]*)?$`)

var c0ControlRe = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0d\x0e-\x1f\x7f\x{0080}-\x{009F}]`)

var bidiFormatRe = regexp.MustCompile(
	`[\x{00AD}\x{180E}\x{200B}\x{200E}\x{200F}` +
		`\x{202A}-\x{202E}` +
		`\x{2060}` +
		`\x{2066}-\x{2069}` +
		`\x{FEFF}]`,
)

func sanitizeDisplayText(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")

	s = sanitizeEscapeRe.ReplaceAllString(s, "")

	s = trailingEscapeRe.ReplaceAllString(s, "")

	s = strings.ReplaceAll(s, "\x1b", "")

	s = c0ControlRe.ReplaceAllString(s, "")

	s = bidiFormatRe.ReplaceAllString(s, "")

	s = strings.ReplaceAll(s, "\t", "    ")

	return s
}
