package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// LLM failure boxes
// -----------------
// A turn whose LLM request failed after every retry is persisted as an
// assistant message carrying ErrorDetail (pkg/providers/protocoltypes,
// "error_detail") and ExcludeFromContext=true, so it is stored for the UIs but
// never replayed to the model. Rendering it as a normal answer would be wrong on
// two counts: Content is the legacy "❌ Error processing message: …" text meant
// for plain-text channels, and the failure is not an answer at all.
//
// renderErrorBox follows the same recipe as renderSystemNotice: a header row
// plus a boxed body built from the tool-result primitives, so the block aligns
// with the rest of the transcript and honours the pane width.
const errorBoxIndent = "  "

// renderErrorBox renders a persisted LLM-failure message as a red box: a bold
// red header ("tui.llmErrorTitle") followed by the error detail inside a
// red left-bordered box.
//
// Width discipline matches renderToolResultBlock: the text is wrapped BEFORE it
// is styled, so lineViewport's lipgloss Width() pass never re-wraps a styled row
// mid-content (which would drop the indent and the box border on the
// continuation rows). Every row is indented by errorBoxIndent so the box sits
// under the header.
//
// The detail is NOT truncated: the backend already caps it at 4000 characters,
// and the box is the only place the actual failure reason is visible. An empty
// detail renders the header alone.
func renderErrorBox(detail string, width int) string {
	header := renderErrorBoxHeader(width)

	// Trim the ends (interior indentation of a stack trace survives): a
	// whitespace-only detail must render the header alone, not an empty box.
	body := strings.TrimSpace(sanitizeDisplayText(detail))
	if body == "" {
		return header
	}

	// The box costs its border + horizontal padding on top of the indent; wrap
	// the content to what is left so the composed row never exceeds width.
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

// renderErrorBoxHeader wraps the localized title to the pane width BEFORE
// styling it and indents each resulting row.
//
// ErrorTitle has no Width() configured, so lipgloss does not wrap it: without
// this a translation wider than the pane (es/pt at a narrow terminal) would
// overflow the row budget and lineViewport's Width() pass would re-wrap the
// styled row, losing the indent. A title that fits stays a single row.
func renderErrorBoxHeader(width int) string {
	budget := width - ansi.StringWidth(errorBoxIndent)
	if budget < 1 {
		budget = 1
	}

	// wrapText already hard-breaks tokens with no break opportunity, so a
	// locale without spaces cannot overflow either.
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
