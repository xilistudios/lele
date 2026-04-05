package channels

import (
	"testing"
)

func TestRenderTelegramText_MarkdownMode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty text",
			input:    "",
			expected: "",
		},
		{
			name:     "plain text",
			input:    "Hello world",
			expected: "Hello world",
		},
		{
			name:     "bold with asterisks",
			input:    "**bold text**",
			expected: "<b>bold text</b>",
		},
		{
			name:     "bold with underscores",
			input:    "__bold text__",
			expected: "<b>bold text</b>",
		},
		{
			name:     "italic",
			input:    "_italic text_",
			expected: "<i>italic text</i>",
		},
		{
			name:     "strikethrough",
			input:    "~~strikethrough~~",
			expected: "<s>strikethrough</s>",
		},
		{
			name:     "inline code",
			input:    "`code`",
			expected: "<code>code</code>",
		},
		{
			name:     "code block",
			input:    "```go\nfunc main() {}\n```",
			expected: "<pre><code>func main() {}\n</code></pre>",
		},
		{
			name:     "link",
			input:    "[link text](https://example.com)",
			expected: `<a href="https://example.com">link text</a>`,
		},
		{
			name:     "header",
			input:    "# Header",
			expected: "Header",
		},
		{
			name:     "blockquote",
			input:    "> quote",
			expected: "quote",
		},
		{
			name:     "list item",
			input:    "- item",
			expected: "• item",
		},
		{
			name:     "mixed formatting",
			input:    "**bold** and _italic_ and `code`",
			expected: "<b>bold</b> and <i>italic</i> and <code>code</code>",
		},
		{
			name:     "HTML escaping",
			input:    "text with <script>alert('xss')</script>",
			expected: "text with &lt;script&gt;alert('xss')&lt;/script&gt;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderTelegramText(tt.input, TextModeMarkdown)
			if result != tt.expected {
				t.Errorf("renderTelegramText(%q, Markdown) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestRenderTelegramText_HTMLMode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain text",
			input:    "Hello world",
			expected: "Hello world",
		},
		{
			name:     "supported HTML tags preserved",
			input:    "<b>bold</b> and <i>italic</i>",
			expected: "<b>bold</b> and <i>italic</i>",
		},
		{
			name:     "unsupported tags escaped",
			input:    "<div>content</div>",
			expected: "&lt;div&gt;content&lt;/div&gt;",
		},
		{
			name:     "script tag escaped",
			input:    "<script>alert('xss')</script>",
			expected: "&lt;script&gt;alert('xss')&lt;/script&gt;",
		},
		{
			name:     "mixed supported and unsupported",
			input:    "<b>bold</b> <div>block</div>",
			expected: "<b>bold</b> &lt;div&gt;block&lt;/div&gt;",
		},
		{
			name:     "all supported tags",
			input:    "<b>b</b> <i>i</i> <u>u</u> <s>s</s> <code>c</code> <pre>p</pre> <a href='x'>a</a>",
			expected: "<b>b</b> <i>i</i> <u>u</u> <s>s</s> <code>c</code> <pre>p</pre> <a href='x'>a</a>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderTelegramText(tt.input, TextModeHTML)
			if result != tt.expected {
				t.Errorf("renderTelegramText(%q, HTML) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestRenderTelegramText_DefaultMode(t *testing.T) {
	// Empty string should default to markdown
	input := "**bold**"
	result := renderTelegramText(input, "")
	expected := "<b>bold</b>"
	if result != expected {
		t.Errorf("renderTelegramText(%q, empty) = %q, want %q", input, result, expected)
	}
}

func TestIsTelegramParseError(t *testing.T) {
	// Test with actual parse errors
	parseErrors := []string{
		"can't parse message",
		"parse error",
		"can't find end of tag",
		"unexpected token",
		"invalid character",
	}

	for _, errText := range parseErrors {
		err := &testError{msg: errText}
		if !isTelegramParseError(err) {
			t.Errorf("isTelegramParseError(%q) = false, want true", errText)
		}
	}

	nonParseErrors := []string{
		"network error",
		"timeout",
		"unauthorized",
	}

	for _, errText := range nonParseErrors {
		err := &testError{msg: errText}
		if isTelegramParseError(err) {
			t.Errorf("isTelegramParseError(%q) = true, want false", errText)
		}
	}

	// Test nil error
	if isTelegramParseError(nil) {
		t.Error("isTelegramParseError(nil) = true, want false")
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

func TestEscapeRawHTMLForTelegram(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty",
			input:    "",
			expected: "",
		},
		{
			name:     "plain text",
			input:    "Hello world",
			expected: "Hello world",
		},
		{
			name:     "supported tag preserved - bold",
			input:    "<b>text</b>",
			expected: "<b>text</b>",
		},
		{
			name:     "supported tag preserved - italic",
			input:    "<i>text</i>",
			expected: "<i>text</i>",
		},
		{
			name:     "unsupported tag escaped",
			input:    "<div>text</div>",
			expected: "&lt;div&gt;text&lt;/div&gt;",
		},
		{
			name:     "self-closing unsupported tag",
			input:    "<br/>",
			expected: "&lt;br/&gt;",
		},
		{
			name:     "nested tags",
			input:    "<b><span>text</span></b>",
			expected: "<b>&lt;span&gt;text&lt;/span&gt;</b>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeRawHTMLForTelegram(tt.input)
			if result != tt.expected {
				t.Errorf("escapeRawHTMLForTelegram(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestMarkdownToTelegramHTML_ComplexCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "code block with language",
			input:    "```python\nprint('hello')\n```",
			expected: "<pre><code>print('hello')\n</code></pre>",
		},
		{
			name:     "multiple inline codes",
			input:    "`first` and `second`",
			expected: "<code>first</code> and <code>second</code>",
		},
		{
			name:     "code block preserves backticks inside",
			input:    "```\n`inside`\n```",
			expected: "<pre><code>`inside`\n</code></pre>",
		},
		{
			name:     "nested bold in italic not supported",
			input:    "_italic **bold** italic_",
			expected: "<i>italic <b>bold</b> italic</i>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := markdownToTelegramHTML(tt.input)
			if result != tt.expected {
				t.Errorf("markdownToTelegramHTML(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
