package utils

import (
	"strings"
	"testing"
)

func TestSplitMessage(t *testing.T) {
	longText := strings.Repeat("a", 2500)
	longCode := "```go\n" + strings.Repeat("fmt.Println(\"hello\")\n", 100) + "```"

	tests := []struct {
		name         string
		content      string
		maxLen       int
		expectChunks int
		checkContent func(t *testing.T, chunks []string)
	}{
		{
			name:         "Empty message",
			content:      "",
			maxLen:       2000,
			expectChunks: 0,
		},
		{
			name:         "Short message fits in one chunk",
			content:      "Hello world",
			maxLen:       2000,
			expectChunks: 1,
		},
		{
			name:         "Simple split regular text",
			content:      longText,
			maxLen:       2000,
			expectChunks: 2,
			checkContent: func(t *testing.T, chunks []string) {
				if len(chunks[0]) > 2000 {
					t.Errorf("Chunk 0 too large: %d", len(chunks[0]))
				}
				if len(chunks[0])+len(chunks[1]) != len(longText) {
					t.Errorf("Total length mismatch. Got %d, want %d", len(chunks[0])+len(chunks[1]), len(longText))
				}
			},
		},
		{
			name:         "Split at newline",
			content:      strings.Repeat("a", 1750) + "\n" + strings.Repeat("b", 300),
			maxLen:       2000,
			expectChunks: 2,
			checkContent: func(t *testing.T, chunks []string) {
				if len(chunks[0]) != 1750 {
					t.Errorf("Expected chunk 0 to be 1750 length (split at newline), got %d", len(chunks[0]))
				}
				if chunks[1] != strings.Repeat("b", 300) {
					t.Errorf("Chunk 1 content mismatch. Len: %d", len(chunks[1]))
				}
			},
		},
		{
			name:         "Long code block split",
			content:      "Prefix\n" + longCode,
			maxLen:       2000,
			expectChunks: 2,
			checkContent: func(t *testing.T, chunks []string) {
				if !strings.HasSuffix(chunks[0], "\n```") {
					t.Error("First chunk should end with injected closing fence")
				}
				if !strings.HasPrefix(chunks[1], "```go") {
					t.Error("Second chunk should start with injected code block header")
				}
			},
		},
		{
			name:         "Preserve Unicode characters",
			content:      strings.Repeat("\u4e16", 1000),
			maxLen:       2000,
			expectChunks: 2,
			checkContent: func(t *testing.T, chunks []string) {
				if !strings.Contains(chunks[0], "\u4e16") {
					t.Error("Chunk should contain unicode characters")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitMessage(tc.content, tc.maxLen)

			if tc.expectChunks == 0 {
				if len(got) != 0 {
					t.Errorf("Expected 0 chunks, got %d", len(got))
				}
				return
			}

			if len(got) != tc.expectChunks {
				t.Errorf("Expected %d chunks, got %d", tc.expectChunks, len(got))
				for i, c := range got {
					t.Logf("Chunk %d length: %d", i, len(c))
				}
				return
			}

			if tc.checkContent != nil {
				tc.checkContent(t, got)
			}
		})
	}
}

func TestSplitMessage_CodeBlockIntegrity(t *testing.T) {

	content := "```go\npackage main\n\nfunc main() {\n\tprintln(\"Hello\")\n}\n```"
	maxLen := 40

	chunks := SplitMessage(content, maxLen)

	if len(chunks) != 2 {
		t.Fatalf("Expected 2 chunks, got %d: %q", len(chunks), chunks)
	}

	if !strings.HasSuffix(chunks[0], "\n```") {
		t.Errorf("First chunk should end with closing fence. Got: %q", chunks[0])
	}

	if !strings.HasPrefix(chunks[1], "```go") {
		t.Errorf("Second chunk should start with code block header. Got: %q", chunks[1])
	}

	if len(chunks[0]) > 40 {
		t.Errorf("First chunk exceeded maxLen: length %d", len(chunks[0]))
	}
}
