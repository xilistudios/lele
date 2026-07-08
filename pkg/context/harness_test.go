package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildHarnessContext(t *testing.T) {
	// Save current working directory
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	// Create temporary directory
	tempDir := t.TempDir()

	// Change to temporary directory
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalCwd)
	}()

	// Create some files and directories
	if err := os.Mkdir(filepath.Join(tempDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "file1.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create file1.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte("My custom agent rules"), 0644); err != nil {
		t.Fatalf("failed to create AGENTS.md: %v", err)
	}

	// Build context
	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	// Verify contents
	if !strings.Contains(ctxStr, "## Harness Module") {
		t.Errorf("expected output to contain Harness Module heading")
	}
	if !strings.Contains(ctxStr, "Current Directory:") {
		t.Errorf("expected output to contain Current Directory section")
	}
	if !strings.Contains(ctxStr, "- subdir/") {
		t.Errorf("expected output to contain subdir/")
	}
	if !strings.Contains(ctxStr, "- file1.txt") {
		t.Errorf("expected output to contain file1.txt")
	}
	if !strings.Contains(ctxStr, "### AGENTS.md") {
		t.Errorf("expected output to contain AGENTS.md heading")
	}
	if !strings.Contains(ctxStr, "My custom agent rules") {
		t.Errorf("expected output to contain AGENTS.md content")
	}
}

func TestBuildHarnessContext_DirEntriesLimit(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() { _ = os.Chdir(originalCwd) }()

	// Create 150 files with predictable sorted names: file_0001.txt .. file_0150.txt
	for i := 1; i <= 150; i++ {
		fname := filepath.Join(tempDir, fmt.Sprintf("file_%04d.txt", i))
		if err := os.WriteFile(fname, []byte("x"), 0644); err != nil {
			t.Fatalf("failed to create file %d: %v", i, err)
		}
	}

	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	// Should contain truncation notice
	if !strings.Contains(ctxStr, "... and 50 more") {
		t.Errorf("expected output to contain '... and 50 more'")
	}

	// File #1 (file_0001.txt) should be present — it's in the first 100
	if !strings.Contains(ctxStr, "file_0001.txt") {
		t.Errorf("expected file_0001.txt to be listed (within first 100)")
	}

	// File #130 (file_0130.txt) is beyond the 100-entry limit and should NOT appear
	if strings.Contains(ctxStr, "file_0130.txt") {
		t.Errorf("file_0130.txt should NOT appear in output (beyond 100-entry limit)")
	}
}

func TestBuildHarnessContext_AgentsMdTruncation(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() { _ = os.Chdir(originalCwd) }()

	// Create an AGENTS.md larger than maxAgentsFileBytes (16384)
	bigContent := strings.Repeat("A", maxAgentsFileBytes+5000)
	if err := os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(bigContent), 0644); err != nil {
		t.Fatalf("failed to create AGENTS.md: %v", err)
	}

	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	// Must contain truncation marker
	if !strings.Contains(ctxStr, "[AGENTS.md truncated]") {
		t.Errorf("expected output to contain '[AGENTS.md truncated]'")
	}

	// The content portion after "### AGENTS.md\n\n" should be at most maxAgentsFileBytes
	// plus the truncation notice. The full output should be bounded.
	// Extract the AGENTS.md section to check its size.
	idx := strings.Index(ctxStr, "### AGENTS.md\n\n")
	if idx < 0 {
		t.Fatal("AGENTS.md section not found")
	}
	agentsSection := ctxStr[idx+len("### AGENTS.md\n\n"):]
	// agentsSection should be <= maxAgentsFileBytes + len("\n\n... [AGENTS.md truncated]\n")
	maxAllowed := maxAgentsFileBytes + len("\n\n... [AGENTS.md truncated]\n")
	if len(agentsSection) > maxAllowed {
		t.Errorf("AGENTS.md section too large: got %d bytes, max allowed %d", len(agentsSection), maxAllowed)
	}

	// Verify the embedded content is exactly maxAgentsFileBytes of 'A's
	embedded := agentsSection[:maxAgentsFileBytes]
	if embedded != strings.Repeat("A", maxAgentsFileBytes) {
		t.Errorf("embedded content mismatch")
	}
}

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"short string", "hello", 100, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncate ASCII", "hello world", 5, "hello"},
		{
			"truncate at valid rune boundary (multi-byte UTF-8)",
			// "é" is 2 bytes (0xC3 0xA9), "a" is 1 byte
			// "éaé" = 2+1+2 = 5 bytes; truncate at 3 should give "éa" (3 bytes)
			"\u00e9a\u00e9", 3, "\u00e9a",
		},
		{
			"truncate avoids splitting 2-byte rune",
			// "é" = 2 bytes; truncate at 1 should give "" (back off the continuation byte)
			"\u00e9", 1, "",
		},
		{
			"truncate avoids splitting 3-byte rune (emoji-ish)",
			// "€" is 3 bytes (0xE2 0x82 0xAC)
			"\u20ac", 2, "",
		},
		{
			"truncate with 4-byte rune",
			// "𐍈" (U+10348) is 4 bytes; total string = 5+4+5 = 14 bytes
			// truncate at 7: walk back from idx=7 past continuation bytes → idx=5 → "hello"
			"hello" + "\U00010348" + "world", 7, "hello",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateUTF8(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("truncateUTF8(%q, %d) = %q (len=%d), want %q (len=%d)", tt.s, tt.max, got, len(got), tt.want, len(tt.want))
			}
			// Also verify the result is valid UTF-8
			if !utf8.ValidString(got) {
				t.Errorf("truncateUTF8(%q, %d) returned invalid UTF-8: %q", tt.s, tt.max, got)
			}
		})
	}
}
