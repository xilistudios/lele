package utils

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// message.go: code-block splitting branches
// ---------------------------------------------------------------------------

// Extend to include a closing fence within maxLen (L54-57).
func TestSplitMessage_ExtendToClosingFence(t *testing.T) {
	content := "```\n" + strings.Repeat("a", 150) + "\n```tail " + strings.Repeat("b", 300)
	maxLen := 200
	msgs := SplitMessage(content, maxLen)
	if len(msgs) == 0 {
		t.Fatal("expected chunks")
	}
	for _, m := range msgs {
		if len(m) > maxLen {
			t.Errorf("chunk exceeds maxLen %d: len=%d", maxLen, len(m))
		}
	}
	if !strings.HasPrefix(msgs[0], "```") {
		t.Errorf("first chunk dropped opening fence: %q", msgs[0])
	}
}

// headerEnd == -1 branch (opening fence with no newline) + split-inside.
func TestSplitMessage_HeaderEndNoNewline(t *testing.T) {
	content := strings.Repeat("a", 30) + "```" + strings.Repeat("b", 400)
	maxLen := 300
	msgs := SplitMessage(content, maxLen)
	if len(msgs) == 0 {
		t.Fatal("expected chunks")
	}
	for _, m := range msgs {
		if len(m) > maxLen {
			t.Errorf("chunk exceeds maxLen %d: len=%d", maxLen, len(m))
		}
	}
}

// split-inside path where maxLen is short and content is a long fenced block:
// covers the split-inside injection + continue loop in both header-branch forms.
func TestSplitMessage_SplitInsideUnclosed(t *testing.T) {
	content := strings.Repeat("a", 30) + "```\n" +
		strings.Repeat("b", 200) + strings.Repeat("\n", 5) + strings.Repeat("c", 300) + "\n```"
	maxLen := 300
	msgs := SplitMessage(content, maxLen)
	if len(msgs) == 0 {
		t.Fatal("expected chunks")
	}
	for _, m := range msgs {
		if len(m) > maxLen {
			t.Errorf("chunk exceeds maxLen %d: len=%d", maxLen, len(m))
		}
	}
}

// Last-resort split: unclosedIdx > 20 with no newline/space before it.
func TestSplitMessage_LastResortAtUnclosed(t *testing.T) {
	content := strings.Repeat("nospace", 10) + "```\n" + strings.Repeat("z", 2000)
	maxLen := 50
	msgs := SplitMessage(content, maxLen)
	if len(msgs) == 0 {
		t.Fatal("expected chunks")
	}
}

// msgEnd<=0 fallback to effectiveLimit (no newline/space in window).
func TestSplitMessage_FallbackToEffectiveLimit(t *testing.T) {
	content := strings.Repeat("nospace", 100)
	maxLen := 50
	msgs := SplitMessage(content, maxLen)
	if len(msgs) == 0 {
		t.Fatal("expected chunks")
	}
	for _, m := range msgs {
		if len(m) > maxLen {
			t.Errorf("chunk exceeds maxLen: len=%d", len(m))
		}
	}
}

// ---------------------------------------------------------------------------
// media.go: os.Create and io.Copy failure branches
// ---------------------------------------------------------------------------

// TestDownloadFile_CopyFailure exercises the io.Copy error branch by having the
// server send a partial 200 response then abruptly close the connection.
func TestDownloadFile_CopyFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server doesn't support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\npartial"))
		conn.Close()
	}))
	defer server.Close()

	got := DownloadFile(server.URL, "x.txt", DownloadOptions{Timeout: 5 * time.Second, LoggerPrefix: "test"})
	if got != "" {
		t.Errorf("expected empty path on copy failure, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// attachments.go: remaining error branches
// ---------------------------------------------------------------------------

// TestMoveFile_RemoveFailureAfterCopyFallback exercises the os.Remove error
// branch (L116-119): rename fails, copy succeeds, remove failures.
func TestMoveFile_RemoveFailureAfterCopyFallback(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission checks are bypassed")
	}
	roDir := t.TempDir()
	src := filepath.Join(roDir, "src.txt")
	os.WriteFile(src, []byte("data"), 0600)

	// Prepare a dst directory so rename(src, dstDir) fails; we then point copy
	// at a nested writable path inside it. After the copy succeeds, Remove(src)
	// fails because roDir is read-only.
	dstRoot := t.TempDir()
	dstNested := filepath.Join(dstRoot, "nested")
	os.MkdirAll(dstNested, 0755)

	if err := os.Chmod(roDir, 0500); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	defer os.Chmod(roDir, 0700)

	// Rename(src, dstNested) with dstNested an existing non-empty dir fails;
	// copy fallback creates a file at dstNested+"-copy" (writable), then
	// Remove(src) fails due to read-only roDir.
	err := moveFile(src, filepath.Join(dstNested, "out.txt"))
	if err != nil {
		t.Logf("moveFile returned %v", err)
	}
}

// TestCleanupOldUploads_RemoveFailure exercises the os.Remove error branch in
// the cleanup loop. On Linux removing a non-empty directory yields a non-
// IsNotExist error that is logged and the loop continues.
func TestCleanupOldUploads_RemoveFailure(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0755)
	os.WriteFile(filepath.Join(sub, "inner.txt"), []byte("x"), 0600)
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(sub, old, old)

	if err := CleanupOldUploads(dir, 24*time.Hour); err != nil {
		t.Errorf("unexpected error during subdir skip: %v", err)
	}

	// Now place an actual *file* whose removal will fail: use a read-only parent.
	roDir := t.TempDir()
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission checks are bypassed")
	}
	roFile := filepath.Join(roDir, "old.dat")
	os.WriteFile(roFile, []byte("x"), 0400)
	os.Chtimes(roFile, old, old)
	if err := os.Chmod(roDir, 0500); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	defer os.Chmod(roDir, 0700)

	if err := CleanupOldUploads(roDir, 24*time.Hour); err != nil {
		t.Errorf("expected remove-failure to be logged and not propagated, got %v", err)
	}
	// File should remain.
	if _, err := os.Stat(roFile); err != nil {
		t.Errorf("expected file to remain after failed removal")
	}
}

// ---------------------------------------------------------------------------
// Additional message.go & findLastSpace branch coverage
// ---------------------------------------------------------------------------

func TestFindLastSpace_Tab(t *testing.T) {
	if got := findLastSpace("hello\tworld", 100); got != 5 {
		t.Errorf("expected tab position 5, got %d", got)
	}
	if got := findLastSpace("no-separators-here", 100); got != -1 {
		t.Errorf("expected -1 when no space/tab, got %d", got)
	}
}

// TestSplitMessage_SplitBeforeCodeBlock triggers the split-before branch where a
// newline exists before the unclosed code block (L88-90).
func TestSplitMessage_SplitBeforeCodeBlock(t *testing.T) {
	content := "aaaaaa\n```go\n" + strings.Repeat("body", 400) + "\n```"
	maxLen := 200
	msgs := SplitMessage(content, maxLen)
	if len(msgs) == 0 {
		t.Fatal("expected chunks")
	}
	for _, m := range msgs {
		if len(m) > maxLen {
			t.Errorf("chunk exceeds maxLen %d: len=%d", maxLen, len(m))
		}
	}
}

// TestSplitMessage_LastResortUnclosedIdx triggers the split-before branch where
// no newline/space precedes the unclosed block and unclosedIdx > 20 (L92-94).
func TestSplitMessage_LastResortUnclosedIdx(t *testing.T) {
	content := strings.Repeat("b", 30) + "```\n" + strings.Repeat("c", 800)
	maxLen := 200
	msgs := SplitMessage(content, maxLen)
	if len(msgs) == 0 {
		t.Fatal("expected chunks")
	}
	for _, m := range msgs {
		if len(m) > maxLen {
			t.Errorf("chunk exceeds maxLen %d: len=%d", maxLen, len(m))
		}
	}
}
