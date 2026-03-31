package channels

import (
	"fmt"
	"regexp"
	"strings"
)

// TextMode represents the formatting mode for Telegram messages
type TextMode string

const (
	// TextModeMarkdown converts Markdown to Telegram HTML (default)
	TextModeMarkdown TextMode = "markdown"
	// TextModeHTML uses HTML directly
	TextModeHTML TextMode = "html"
)

// renderTelegramText renders text for Telegram based on the specified text mode.
// If textMode is "html", the text is used directly (with HTML escaping for safety).
// If textMode is "markdown" (default), Markdown is converted to Telegram HTML.
func renderTelegramText(text string, textMode TextMode) string {
	if text == "" {
		return ""
	}

	switch textMode {
	case TextModeHTML:
		// For HTML mode, we still escape to prevent parse errors from raw model HTML
		return escapeRawHTMLForTelegram(text)
	case TextModeMarkdown, "":
		return markdownToTelegramHTML(text)
	default:
		return markdownToTelegramHTML(text)
	}
}

// escapeRawHTMLForTelegram escapes raw HTML from models to prevent Telegram parse errors.
// This is a safety measure when the model might output HTML that Telegram can't parse.
func escapeRawHTMLForTelegram(text string) string {
	// Telegram only supports specific HTML tags: <b>, <i>, <u>, <s>, <code>, <pre>, <a>
	// We escape any other tags to prevent parse errors
	supportedTags := map[string]bool{
		"b": true, "strong": true,
		"i": true, "em": true,
		"u": true, "ins": true,
		"s": true, "strike": true, "del": true,
		"code": true,
		"pre": true,
		"a": true,
	}

	// Pattern to match HTML tags
	tagPattern := regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9]*)\b[^>]*>`)

	return tagPattern.ReplaceAllStringFunc(text, func(match string) string {
		// Extract the tag name
		submatches := tagPattern.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}

		tagName := strings.ToLower(submatches[2])
		if supportedTags[tagName] {
			// Keep supported tags as-is
			return match
		}

		// Escape unsupported tags
		return escapeHTML(match)
	})
}

// markdownToTelegramHTML converts Markdown text to Telegram-safe HTML.
// Supports: bold (**), italic (_), strikethrough (~~), code (` and ```), links ([]())
func markdownToTelegramHTML(text string) string {
	if text == "" {
		return ""
	}

	codeBlocks := extractCodeBlocks(text)
	text = codeBlocks.text

	inlineCodes := extractInlineCodes(text)
	text = inlineCodes.text

	// Convert Markdown headers to plain text (remove #)
	text = regexp.MustCompile(`^#{1,6}\s+(.+)$`).ReplaceAllString(text, "$1")
	// Convert blockquotes to plain text
	text = regexp.MustCompile(`^>\s*(.*)$`).ReplaceAllString(text, "$1")
	// Escape HTML to prevent injection
	text = escapeHTML(text)
	// Convert Markdown links to HTML
	text = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).ReplaceAllString(text, `<a href="$2">$1</a>`)
	// Convert bold (** and __)
	text = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(text, "<b>$1</b>")
	text = regexp.MustCompile(`__(.+?)__`).ReplaceAllString(text, "<b>$1</b>")

	// Convert italic (_)
	reItalic := regexp.MustCompile(`_([^_]+)_`)
	text = reItalic.ReplaceAllStringFunc(text, func(s string) string {
		match := reItalic.FindStringSubmatch(s)
		if len(match) < 2 {
			return s
		}
		return "<i>" + match[1] + "</i>"
	})

	// Convert strikethrough (~~)
	text = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllString(text, "<s>$1</s>")
	// Convert list items to bullets
	text = regexp.MustCompile(`^[-*]\s+`).ReplaceAllString(text, "• ")

	// Restore inline code blocks
	for i, code := range inlineCodes.codes {
		escaped := escapeHTML(code)
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00IC%d\x00", i), fmt.Sprintf("<code>%s</code>", escaped))
	}

	// Restore multi-line code blocks
	for i, code := range codeBlocks.codes {
		escaped := escapeHTML(code)
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00CB%d\x00", i), fmt.Sprintf("<pre><code>%s</code></pre>", escaped))
	}

	return text
}

type codeBlockMatch struct {
	text  string
	codes []string
}

func extractCodeBlocks(text string) codeBlockMatch {
	re := regexp.MustCompile("```[\\w]*\\n?([\\s\\S]*?)```")
	matches := re.FindAllStringSubmatch(text, -1)

	codes := make([]string, 0, len(matches))
	for _, match := range matches {
		codes = append(codes, match[1])
	}

	i := 0
	text = re.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("\x00CB%d\x00", i)
		i++
		return placeholder
	})

	return codeBlockMatch{text: text, codes: codes}
}

type inlineCodeMatch struct {
	text  string
	codes []string
}

func extractInlineCodes(text string) inlineCodeMatch {
	re := regexp.MustCompile("`([^`]+)`")
	matches := re.FindAllStringSubmatch(text, -1)

	codes := make([]string, 0, len(matches))
	for _, match := range matches {
		codes = append(codes, match[1])
	}

	i := 0
	text = re.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("\x00IC%d\x00", i)
		i++
		return placeholder
	})

	return inlineCodeMatch{text: text, codes: codes}
}

func escapeHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}

// isTelegramParseError checks if an error is a Telegram parse error
// that would benefit from a plain text fallback
func isTelegramParseError(err error) bool {
	if err == nil {
		return false
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "parse") ||
		strings.Contains(errText, "can't parse") ||
		strings.Contains(errText, "can't find end") ||
		strings.Contains(errText, "unexpected") ||
		strings.Contains(errText, "invalid")
}
