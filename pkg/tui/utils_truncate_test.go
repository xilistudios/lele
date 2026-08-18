package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestTruncateRunes_NoSplitMultibyte(t *testing.T) {
	input := "café🦞café🦞"
	got := truncateRunes(input, 7)
	want := "café🦞ca"
	if got != want {
		t.Fatalf("truncateRunes(%q, 7) = %q, want %q", input, got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncateRunes result %q is not valid UTF-8", got)
	}
	// Verify the exact rune count.
	if gotRunes := len([]rune(got)); gotRunes != 7 {
		t.Fatalf("truncateRunes(%q, 7) produced %d runes, want 7", input, gotRunes)
	}
}

func TestTruncateRunes_ShortUnchanged(t *testing.T) {
	got := truncateRunes("abc", 10)
	if got != "abc" {
		t.Fatalf("truncateRunes(\"abc\", 10) = %q, want \"abc\"", got)
	}
}

func TestFormatToolCallArgsCompact_Sanitizes(t *testing.T) {
	tc := providers.ToolCall{
		Name:      "exec",
		Arguments: map[string]interface{}{"command": "echo \x1b[2J hi\tthere\r\n"},
	}
	out := formatToolCallArgsCompact(tc)

	if strings.Contains(out, "\x1b") {
		t.Fatalf("output contains raw escape char: %q", out)
	}
	if strings.Contains(out, "\r") {
		t.Fatalf("output contains carriage return: %q", out)
	}
	if strings.Contains(out, "\t") {
		t.Fatalf("output contains tab: %q", out)
	}
	if !strings.Contains(out, "echo") {
		t.Fatalf("output lost expected substring \"echo\": %q", out)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("output lost expected substring \"hi\": %q", out)
	}
}
