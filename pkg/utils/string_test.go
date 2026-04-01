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
