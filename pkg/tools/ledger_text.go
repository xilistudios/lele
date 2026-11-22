package tools

import (
	"strings"
	"unicode"
)

func SanitizeLedgerText(s string, maxBytes int) string {
	if s == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		if r == '\uFEFF' {
			continue
		}
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			r = ' '
		} else if unicode.In(r, unicode.Cf) || (unicode.IsSpace(r) && r != ' ') {
			r = ' '
		}
		if r == ' ' {
			if lastSpace {
				continue
			}
			lastSpace = true
		} else {
			lastSpace = false
		}
		b.WriteRune(r)
	}

	out := stripLedgerDecoration(strings.TrimSpace(b.String()))

	if maxBytes > 0 && len(out) > maxBytes {
		out = truncateOnRuneBoundary(out, maxBytes)
	}
	return out
}

const ledgerDecorationRunes = "-#>…*`_+"

const inlineDecorationRunes = "-*+#"

func isInlineDecoration(segment, run string) bool {
	if strings.ContainsAny(run, ">`_…") {
		return true
	}
	rest := segment[len(run):]
	return rest == "" || rest[0] == ' '
}

func stripLedgerDecoration(s string) string {
	for {
		before := s
		s = stripDecorationAtSegmentStarts(s)
		s = strings.Join(strings.Fields(s), " ")
		if s == before {
			return s
		}
	}
}

func stripDecorationAtSegmentStarts(s string) string {
	segments := strings.Split(s, " ")
	kept := make([]string, 0, len(segments))
	for i, segment := range segments {
		run := leadingDecorationRun(segment)
		trimmed := segment[len(run):]
		if run != "" && i > 0 && !isInlineDecoration(segment, run) {
			trimmed = segment
		}
		if trimmed == "" {
			if segment == "" && i > 0 {
				kept = append(kept, "")
			}
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, " ")
}

func leadingDecorationRun(segment string) string {
	n := 0
	for n < len(segment) && strings.IndexByte(ledgerDecorationRunes, segment[n]) >= 0 {
		n++
	}
	return segment[:n]
}

func truncateOnRuneBoundary(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " ")
}

func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}
