package tui

import (
	"regexp"
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

var systemNoticeRe = regexp.MustCompile(`^\[System:\s*([^\]]+)\]\s*([\s\S]*)$`)

func parseSystemNotice(content string) (sender, body string, ok bool) {
	match := systemNoticeRe.FindStringSubmatch(content)
	if match == nil {
		return "", "", false
	}
	sender = strings.TrimSpace(match[1])
	if sender == "" {
		return "", "", false
	}
	return sender, strings.TrimSpace(match[2]), true
}

func renderSystemNotice(sender, body string, width int) string {
	header := i18n.T("tui.systemNotice")
	if s := sanitizeDisplayText(sender); s != "" {
		header += ": " + s
	}

	var sb strings.Builder
	sb.WriteString(renderToolCallRow(header, width))
	if body = sanitizeDisplayText(body); body != "" {
		sb.WriteString("\n")
		sb.WriteString(renderToolResultBlock(body, width))
	}
	return sb.String()
}
