package tui

import (
	"strings"
	"testing"
)

func TestSanitizeDisplayText_StripsANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"SGR", "hello \x1b[31mred\x1b[0m world", "hello red world"},
		{"SGR multi param", "\x1b[1;31mBold red\x1b[0m", "Bold red"},
		{"CSI erase line", "clear\x1b[2K", "clear"},
		{"CSI private mode", "\x1b[?25l", ""},
		{"CSI tilde final", "\x1b[3~", ""},
		{"OSC BEL", "\x1b]0;title\x07", ""},
		{"OSC ST", "\x1b]0;title\x1b\\", ""},
		{"charset selector", "\x1b(0", ""},
		{"cursor save", "a\x1b7b", "ab"},
		{"cursor restore", "a\x1b8b", "ab"},
		{"RIS", "a\x1bcb", "ab"},
		{"alt mode", "\x1b=", ""},
		{"carriage return", "line1\rline2", "line1line2"},
		{"backspace", "a\bb", "ab"},
		{"bell", "beep\x07", "beep"},
		{"vertical tab", "a\x0bb", "ab"},
		{"form feed", "a\x0cb", "ab"},
		{"DEL", "a\x7fb", "ab"},
		{"NUL", "a\x00b", "ab"},
		{"C1 CSI raw byte", "hello \x9b31m", "hello \uFFFD31m"},
		{"C1 CSI valid utf8", "hello \u009b31m", "hello 31m"},
		{"bare ESC", "a\x1bb", "ab"},
		{"truncated CSI at end", "hello \x1b[3", "hello "},
		{"truncated SGR at end", "hello \x1b[31", "hello "},
		{"unterminated OSC at end", "hello \x1b]0;title", "hello "},
		{"C1 OSC raw byte", "a\x9d0;b", "a\uFFFD0;b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeDisplayText(tt.input); got != tt.want {
				t.Errorf("sanitizeDisplayText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeDisplayText_PreservesReadable(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"newline and tab", "a\tb\nc", "a    b\nc"},
		{"utf8", "café 🦞", "café 🦞"},
		{"plain text", "hello world", "hello world"},
		{"invalid utf8 replaced", "bad\xff\xfe utf8", "bad\uFFFD utf8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeDisplayText(tt.input); got != tt.want {
				t.Errorf("sanitizeDisplayText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncateToolResult_StripsControlChars(t *testing.T) {
	content := "line1\rline2\x1b[31m\x1b[0m result"
	got := truncateToolResult(content, 150)
	if contains := containsAny(got, "\r", "\x1b"); contains {
		t.Fatalf("truncateToolResult returned control chars: %q", got)
	}
	if got != "line1line2 result" {
		t.Errorf("truncateToolResult = %q, want %q", got, "line1line2 result")
	}
}

func TestTruncateToolResult_JSONSummaryStripped(t *testing.T) {
	content := "{\"output\":\"ok\r\x1b[0m done\"}"
	got := truncateToolResult(content, 150)
	if containsAny(got, "\r", "\x1b") {
		t.Fatalf("truncateToolResult returned control chars: %q", got)
	}
	if got != "ok done" {
		t.Errorf("truncateToolResult = %q, want %q", got, "ok done")
	}
}

func TestRenderSingleLine_StripsControlChars(t *testing.T) {
	line := "text \x1b[31mgreen\r thing"
	got := renderSingleLine(line, 80)
	if containsAny(got, "\r", "\x1b") {
		t.Fatalf("renderSingleLine returned control chars: %q", got)
	}
	if got != "text green thing" {
		t.Errorf("renderSingleLine = %q, want %q", got, "text green thing")
	}
}

func TestRenderMarkdown_StripsCarriageReturn(t *testing.T) {
	m := newTestModel(t)
	content := "hello\rworld"
	got := m.renderMarkdown(content, 80)
	if containsAny(got, "\r") {
		t.Fatalf("renderMarkdown returned carriage return: %q", got)
	}
	if !strings.Contains(got, "helloworld") {
		t.Errorf("renderMarkdown = %q, want it to contain %q", got, "helloworld")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
