package utils

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
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

// ──────────────────────────────────────────────────────────────────────────────
// TruncateOutput tests (AGT-04)
// ──────────────────────────────────────────────────────────────────────────────

func TestTruncateOutput_ShortString(t *testing.T) {
	s := "hello world"
	got := TruncateOutput(s, 48*1024, 16*1024)
	if got != s {
		t.Errorf("short string should not be truncated, got len=%d", len(got))
	}
}

func TestTruncateOutput_ExactBoundary(t *testing.T) {
	head := 48 * 1024
	tail := 16 * 1024
	s := strings.Repeat("A", head+tail) // exactly at boundary
	got := TruncateOutput(s, head, tail)
	if got != s {
		t.Errorf("string at exact boundary should not be truncated, got len=%d", len(got))
	}
}

func TestTruncateOutput_HeadTailPreservation(t *testing.T) {
	head := 48 * 1024
	tail := 16 * 1024
	totalSize := 100 * 1024 // 100KB
	s := strings.Repeat("A", head) + strings.Repeat("B", totalSize-head-tail) + strings.Repeat("C", tail)

	got := TruncateOutput(s, head, tail)

	// Must contain the truncation marker.
	if !strings.Contains(got, "bytes truncated") {
		t.Error("expected truncation marker")
	}

	// Must start with the real head.
	if !strings.HasPrefix(got, strings.Repeat("A", head)) {
		t.Error("output does not start with the real head")
	}

	// Must end with the real tail.
	if !strings.HasSuffix(got, strings.Repeat("C", tail)) {
		t.Error("output does not end with the real tail")
	}

	// Must be shorter than the original.
	if len(got) >= len(s) {
		t.Errorf("truncated output (%d bytes) should be shorter than original (%d bytes)", len(got), len(s))
	}
}

func TestTruncateOutput_HeadPlusTailSize(t *testing.T) {
	head := 48 * 1024
	tail := 16 * 1024
	// 1MB input
	s := strings.Repeat("X", 1024*1024)

	got := TruncateOutput(s, head, tail)

	// Output should be <= head + tail + marker (marker ~40 bytes).
	maxExpected := head + tail + 200
	if len(got) > maxExpected {
		t.Errorf("output length %d exceeds expected max %d", len(got), maxExpected)
	}
}

func TestTruncateOutput_MarkerReportsDroppedBytes(t *testing.T) {
	head := 48 * 1024
	tail := 16 * 1024
	totalSize := 200 * 1024
	dropped := totalSize - head - tail
	s := strings.Repeat("Z", totalSize)

	got := TruncateOutput(s, head, tail)

	expectedMarker := fmt.Sprintf("[... %d bytes truncated ...]", dropped)
	if !strings.Contains(got, expectedMarker) {
		t.Errorf("expected marker %q in output", expectedMarker)
	}
}

func TestTruncateOutput_ValidUTF8(t *testing.T) {
	head := 48 * 1024
	tail := 16 * 1024
	// Build a large string of multibyte UTF-8 ("café" = 5 bytes each).
	// Total: 60000 * 5 = 300KB.
	s := strings.Repeat("café\n", 60000)

	got := TruncateOutput(s, head, tail)

	if !utf8.ValidString(got) {
		t.Error("TruncateOutput produced invalid UTF-8")
	}
}
