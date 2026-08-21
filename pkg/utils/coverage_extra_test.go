package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

// ---------------------------------------------------------------------------
// Additional attachments.go coverage: mkdir failure, copy/move failure paths,
// sanitize fallback in persistAttachment, cleanup error logging.
// ---------------------------------------------------------------------------

func TestPersistAttachments_MkdirFailure(t *testing.T) {
	// workspace is actually a file, so creating workspace/attachments fails.
	dir := t.TempDir()
	workspaceFile := filepath.Join(dir, "notadir")
	os.WriteFile(workspaceFile, []byte("x"), 0600)

	_, err := PersistAttachmentsToWorkspace(workspaceFile, []bus.FileAttachment{{
		Path: "anything.txt", Temporary: true,
	}})
	if err == nil {
		t.Errorf("expected error when attachments dir cannot be created, got nil")
	} else if !strings.Contains(err.Error(), "create attachments directory") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPersistAttachments_PropagatesMoveError(t *testing.T) {
	workspace := t.TempDir()
	// Source does not exist -> copy fallback in moveFile fails -> error surfaces.
	missing := filepath.Join(t.TempDir(), "missing.txt")

	_, err := PersistAttachmentsToWorkspace(workspace, []bus.FileAttachment{{
		Name: "x.txt", Path: missing, Temporary: true,
	}})
	if err == nil {
		t.Fatalf("expected error when moving missing source")
	}
	if !strings.Contains(err.Error(), "move attachment to workspace") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPersistAttachments_PropagatesCopyError(t *testing.T) {
	workspace := t.TempDir()
	// Non-temporary source that doesn't exist -> copyFile fails -> surfaces copy error.
	missing := filepath.Join(t.TempDir(), "missing.txt")

	_, err := PersistAttachmentsToWorkspace(workspace, []bus.FileAttachment{{
		Name: "x.txt", Path: missing, Temporary: false,
	}})
	if err == nil {
		t.Fatalf("expected error when copying missing source")
	}
	if !strings.Contains(err.Error(), "copy attachment to workspace") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPersistAttachments_SanitizedDotNameFallsBackToAttachment(t *testing.T) {
	workspace := t.TempDir()
	sourceDir := t.TempDir()
	// Filename "..." sanitizes to "." -> fallback name "attachment".
	sourcePath := filepath.Join(sourceDir, "...")
	os.WriteFile(sourcePath, []byte("data"), 0600)

	res, err := PersistAttachmentsToWorkspace(workspace, []bus.FileAttachment{{
		Path: sourcePath, Temporary: true,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if !strings.HasSuffix(res[0].Name, "attachment") {
		t.Errorf("expected fallback name ending in 'attachment', got %q", res[0].Name)
	}
}

func TestCopyFile_CopyFromDirectoryFails(t *testing.T) {
	// os.Open on a directory succeeds, but io.Copy fails reading it.
	srcDir := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out.txt")
	if err := copyFile(srcDir, dst); err == nil {
		t.Errorf("expected error copying from a directory, got nil")
	}
	_ = os.Remove(dst) // cleanup: Create succeeds but io.Copy fails
}

func TestMoveFile_CopyFailureReturnsError(t *testing.T) {
	// rename file->directory fails; copy fallback fails because Create on a
	// directory fails -> moveFile must return the copy error.
	src := filepath.Join(t.TempDir(), "src.txt")
	os.WriteFile(src, []byte("x"), 0600)
	dstDir := t.TempDir() // existing dir as dst

	if err := moveFile(src, dstDir); err == nil {
		t.Errorf("expected moveFile error when copy fallback fails, got nil")
	}
}

func TestCleanupTempAttachments_LogsRemoveError(t *testing.T) {
	// Removing a non-empty directory fails with ENOTEMPTY (not IsNotExist),
	// exercising the error-handling branch that logs.
	srcDir := t.TempDir()
	sub := filepath.Join(srcDir, "sub")
	os.Mkdir(sub, 0755)
	os.WriteFile(filepath.Join(sub, "inner.txt"), []byte("x"), 0600)

	// Must not panic; error is just logged.
	CleanupTempAttachments([]bus.FileAttachment{{Path: sub, Temporary: true}})
}

func TestCleanupOldUploads_EntryInfoErrorSkipped(t *testing.T) {
	dir := t.TempDir()
	oldFile := filepath.Join(dir, "old.txt")
	os.WriteFile(oldFile, []byte("o"), 0600)
	os.Chtimes(oldFile, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour))

	if err := CleanupOldUploads(dir, 24*time.Hour); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SplitMessage coverage for code-block splitting scenarios.
// ---------------------------------------------------------------------------

func TestSplitMessage_CodeBlockSplitInside(t *testing.T) {
	// Make a maxLen such that the content inside one code block fits only by
	// injecting closing/reopening fences (headerEnd+20 branch).
	// Code block with lots of content that exceeds the effective limit.
	padding := strings.Repeat("x", 200)
	content := "```go\n" + padding + "\n```"
	maxLen := 120

	msgs := SplitMessage(content, maxLen)
	if len(msgs) < 2 {
		t.Fatalf("expected multiple chunks for long code block, got %d", len(msgs))
	}
	for _, m := range msgs {
		if len(m) > maxLen {
			t.Errorf("chunk exceeds maxLen: len=%d > %d (%q)", len(m), maxLen, m[:30])
		}
	}
	// Each chunk should be valid.
	if !strings.HasPrefix(msgs[0], "```") {
		t.Errorf("first chunk should still open a code block, got %q", msgs[0])
	}
}

func TestSplitMessage_CodeBlockLastResortSplitInside(t *testing.T) {
	// unclosedIdx <= 20 and header is long: hits the inner "last resort" path
	// that appends "\n```" and continues.
	// Make a code header near the start so unclosedIdx is small but the header
	// extends past 20 chars, and a closing fence far later beyond maxLen.
	content := "`" + "```python\n" + strings.Repeat("y", 300) + "\n```"
	maxLen := 100

	msgs := SplitMessage(content, maxLen)
	if len(msgs) == 0 {
		t.Fatalf("expected at least one chunk")
	}
	for _, m := range msgs {
		if len(m) > maxLen {
			t.Errorf("chunk exceeds maxLen: len=%d", len(m))
		}
	}
}

func TestSplitMessage_MissingClosingFenceLongBlock(t *testing.T) {
	// unclosedIdx > 20, closing fence far away -> the "too long" branch splits
	// before the code block via findLastNewline / findLastSpace.
	header := "```go\n"
	body := strings.Repeat("z", 500)
	content := header + body
	maxLen := 150

	msgs := SplitMessage(content, maxLen)
	if len(msgs) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(msgs))
	}
}

func TestSplitMessage_SingleChunkMetadata(t *testing.T) {
	// Pure metadata chunk: content shorter than a tiny maxLen.
	msgs := SplitMessage("hi", 5)
	if len(msgs) != 1 || msgs[0] != "hi" {
		t.Errorf("expected single short chunk, got %v", msgs)
	}
	// Empty content -> no chunks.
	if got := SplitMessage("", 5); len(got) != 0 {
		t.Errorf("expected no chunks for empty input, got %v", got)
	}
}

func TestSplitMessage_ReachedMaxLenFences(t *testing.T) {
	// closingIdx within maxLen -> extends to include closing fence.
	padding := strings.Repeat("a", 50)
	content := "start\n```\n" + padding + "\n```\nend"
	maxLen := 100

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
