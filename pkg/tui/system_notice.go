package tui

import (
	"regexp"
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Synthetic system notices
// ------------------------
// Some turns are persisted with Role "user" but were not written by the human:
// a background subagent result, a runtime notification, etc. The backend marks
// them with the canonical prefix "[System: <sender>] <body>" (see
// pkg/session/context.go:isHumanUserMessage, the single source of truth for
// which messages are synthetic). Rendering them as a "You" bubble is wrong —
// the user never typed that text — so the TUI renders them as a tool-style card
// (header row + boxed body), mirroring what the web UI does with the synthetic
// `system_notice` tool (web/src/lib/chatMessageBuilder.ts:parseSystemNotice).
//
// The parser below deliberately mirrors the web one (same marker, same
// "user may legitimately type '[System: …' mid-sentence" caveat) instead of
// inventing a second rule: only a prefix match counts.

// systemNoticeRe matches the canonical synthetic marker at the START of the
// content. `[^\]]` (rather than `.`) so a multi-line sender still matches, and
// `[\s\S]*` so the body may span lines — Go's `.` skips newlines.
var systemNoticeRe = regexp.MustCompile(`^\[System:\s*([^\]]+)\]\s*([\s\S]*)$`)

// parseSystemNotice splits a persisted synthetic notice into its sender and
// body. It reports false for anything that is not exactly
// "[System: <sender>] <body>" at the start of content, and for an empty sender
// (an empty body is allowed).
//
// Known false positive, accepted on purpose (same as the web UI): a real user
// message that literally starts with "[System: …" is classified as a notice.
// The backend already treats that prefix as the synthetic marker, so the TUI
// mirrors backend semantics rather than guessing intent.
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

// renderSystemNotice renders a synthetic system notice as a tool-style card:
// a compact header row ("<System notice>: <sender>") followed by the notice
// body inside the tool-result box — the same primitives tool calls and tool
// results use, so the block aligns with the rest of the transcript and honours
// the pane width (see renderToolCallRow / renderToolResultBlock).
//
// The body is NOT truncated (unlike a tool result summary): the notice is a
// message the model actually receives, and the previous plain-text rendering
// showed all of it. The card changes the styling, not the information.
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
