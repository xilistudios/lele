package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

const errorBoxIndent = "  "

func renderErrorBox(detail string, width int) string {
	header := renderErrorBoxHeader(width)

	body := strings.TrimSpace(sanitizeDisplayText(detail))
	if body == "" {
		return header
	}

	budget := width - ansi.StringWidth(errorBoxIndent) -
		ErrorBox.GetHorizontalFrameSize() - ErrorBox.GetHorizontalPadding()
	if budget < 1 {
		budget = 1
	}

	rows := strings.Split(ErrorBox.Render(wrapText(body, budget)), "\n")
	var sb strings.Builder
	for i, row := range rows {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(errorBoxIndent)
		sb.WriteString(row)
	}
	return header + "\n" + sb.String()
}

func renderErrorBoxHeader(width int) string {
	budget := width - ansi.StringWidth(errorBoxIndent)
	if budget < 1 {
		budget = 1
	}

	titleRows := strings.Split(wrapText(i18n.T("tui.llmErrorTitle"), budget), "\n")
	var sb strings.Builder
	for i, row := range titleRows {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(errorBoxIndent)
		sb.WriteString(ErrorTitle.Render(row))
	}
	return sb.String()
}
