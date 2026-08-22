package utils

import (
	"strings"
	"testing"
)

func TestRandomProcessName(t *testing.T) {
	name1 := RandomProcessName()
	name2 := RandomProcessName()

	// Should generate different names (highly unlikely to be same)
	if name1 == name2 {
		t.Logf("Warning: got same name twice (%s), this is unlikely but possible", name1)
	}

	// Should contain a hyphen
	if !strings.Contains(name1, "-") {
		t.Errorf("Expected name to contain hyphen, got: %s", name1)
	}

	// Should not contain spaces
	if strings.Contains(name1, " ") {
		t.Errorf("Expected name to not contain spaces, got: %s", name1)
	}

	// Should be non-empty
	if name1 == "" {
		t.Error("Expected non-empty name")
	}

	t.Logf("Generated names: %s, %s", name1, name2)
}

func TestRandomProcessNameWithEmoji(t *testing.T) {
	name := RandomProcessNameWithEmoji()

	// Should contain "Process:"
	if !strings.Contains(name, "Process:") {
		t.Errorf("Expected name to contain 'Process:', got: %s", name)
	}

	// Should contain an emoji (any of the defined emojis)
	emojis := []string{"🧰", "⚡", "🔧", "⚙️", "🛠️", "🔨", "📦", "🚀", "💡", "🔍"}
	foundEmoji := false
	for _, emoji := range emojis {
		if strings.Contains(name, emoji) {
			foundEmoji = true
			break
		}
	}
	if !foundEmoji {
		t.Errorf("Expected name to contain one of the defined emojis, got: %s", name)
	}

	t.Logf("Generated name with emoji: %s", name)
}

func TestRandomProcessNameFormat(t *testing.T) {
	for i := 0; i < 10; i++ {
		name := RandomProcessName()
		parts := strings.Split(name, "-")
		if len(parts) != 2 {
			t.Errorf("Expected name to have exactly 2 parts separated by hyphen, got: %s (parts: %v)", name, parts)
		}
		if parts[0] == "" || parts[1] == "" {
			t.Errorf("Expected both parts to be non-empty, got: %s", name)
		}
	}
}
func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"empty string", "", 5, ""},
		{"shorter than maxLen", "hello", 10, "hello"},
		{"exactly maxLen", "hello", 5, "hello"},
		{"basic truncation", "hello world", 8, "hello..."},
		{"maxLen zero", "hello", 0, ""},
		{"maxLen 1", "hello", 1, "h"},
		{"maxLen 3 no ellipsis", "hello", 3, "hel"},
		{"multi-byte unicode", "日本語のテキスト", 4, "日..."}, //nolint:gosmopolitan // testing unicode truncation
		{"emoji truncation", "🦞🦞🦞🦞🦞", 4, "🦞..."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Truncate(tc.s, tc.maxLen); got != tc.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tc.s, tc.maxLen, got, tc.want)
			}
		})
	}
}

func TestTruncate_RuneSafe(t *testing.T) {
	// Ensures no mid-rune slicing produces invalid UTF-8.
	s := "日本語のテキストです" //nolint:gosmopolitan // testing unicode safety
	got := Truncate(s, 5)
	if !validUTF8(got) {
		t.Errorf("Truncate produced invalid UTF-8: %q", got)
	}
}

func validUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}
