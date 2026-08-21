package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

func TestPersistAttachmentsToWorkspace_MovesTemporaryFile(t *testing.T) {
	workspace := t.TempDir()
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "input.txt")
	if err := os.WriteFile(sourcePath, []byte("secret-content"), 0600); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	attachments, err := PersistAttachmentsToWorkspace(workspace, []bus.FileAttachment{{
		Name:      "input.txt",
		Path:      sourcePath,
		Kind:      "file",
		Temporary: true,
	}})
	if err != nil {
		t.Fatalf("PersistAttachmentsToWorkspace returned error: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].Temporary {
		t.Fatalf("expected persisted attachment to be non-temporary")
	}
	if !strings.HasPrefix(attachments[0].Path, filepath.Join(workspace, "attachments")) {
		t.Fatalf("expected attachment path inside workspace, got %q", attachments[0].Path)
	}
	if _, err := os.Stat(attachments[0].Path); err != nil {
		t.Fatalf("expected persisted file to exist: %v", err)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("expected original temp file to be moved away, stat err=%v", err)
	}

	contextText := BuildAttachmentContext(attachments)
	if !strings.Contains(contextText, attachments[0].Path) {
		t.Fatalf("expected attachment context to include stored path, got %q", contextText)
	}
	if strings.Contains(contextText, "secret-content") {
		t.Fatalf("attachment context should not inline file contents")
	}
}
func TestBuildAttachmentContext_Empty(t *testing.T) {
	if got := BuildAttachmentContext(nil); got != "" {
		t.Errorf("expected empty context for nil attachments, got %q", got)
	}
	if got := BuildAttachmentContext([]bus.FileAttachment{}); got != "" {
		t.Errorf("expected empty context for empty attachments, got %q", got)
	}
}

func TestBuildAttachmentContext_EmptyPaths(t *testing.T) {
	ctx := BuildAttachmentContext([]bus.FileAttachment{
		{Path: "  ", Name: "first"},
		{Path: "", Name: ""},
	})
	if strings.Contains(ctx, "attachment-1") == false {
		t.Errorf("expected fallback name attachment-1 for blank path, got %q", ctx)
	}
	if strings.Contains(ctx, "attachment-2") == false {
		t.Errorf("expected fallback name attachment-2 for empty path, got %q", ctx)
	}
}

func TestCleanupTempAttachments(t *testing.T) {
	sourceDir := t.TempDir()
	file1 := filepath.Join(sourceDir, "t1.txt")
	file2 := filepath.Join(sourceDir, "t2.txt")
	os.WriteFile(file1, []byte("a"), 0600)
	os.WriteFile(file2, []byte("b"), 0600)

	// Temporary file should be removed
	CleanupTempAttachments([]bus.FileAttachment{
		{Path: file1, Temporary: true},
	})
	if _, err := os.Stat(file1); !os.IsNotExist(err) {
		t.Errorf("expected temp file1 removed, stat err=%v", err)
	}

	// Non-temporary file should NOT be removed
	CleanupTempAttachments([]bus.FileAttachment{
		{Path: file2, Temporary: false},
	})
	if _, err := os.Stat(file2); err != nil {
		t.Errorf("expected non-temp file2 to remain, stat err=%v", err)
	}

	// Empty path skipped without panic
	CleanupTempAttachments([]bus.FileAttachment{{Path: "", Temporary: true}})

	// non-existent temporary path → no error (IsNotExist)
	CleanupTempAttachments([]bus.FileAttachment{{Path: filepath.Join(sourceDir, "nope.txt"), Temporary: true}})
}

func TestCleanupOldUploads(t *testing.T) {
	t.Run("empty dir returns nil", func(t *testing.T) {
		if err := CleanupOldUploads("", time.Hour); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("non-existent dir returns nil", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		if err := CleanupOldUploads(dir, time.Hour); err != nil {
			t.Errorf("expected nil for non-existent dir, got %v", err)
		}
	})

	t.Run("removes old files, keeps fresh and dirs", func(t *testing.T) {
		dir := t.TempDir()

		oldFile := filepath.Join(dir, "old.txt")
		freshFile := filepath.Join(dir, "fresh.txt")
		os.WriteFile(oldFile, []byte("old"), 0600)
		os.WriteFile(freshFile, []byte("fresh"), 0600)

		os.Chtimes(oldFile, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour))
		os.Chtimes(freshFile, time.Now(), time.Now())

		subDir := filepath.Join(dir, "subdir")
		os.Mkdir(subDir, 0755)
		os.WriteFile(filepath.Join(subDir, "inner.txt"), []byte("x"), 0600)

		if err := CleanupOldUploads(dir, 24*time.Hour); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
			t.Errorf("expected old file removed")
		}
		if _, err := os.Stat(freshFile); err != nil {
			t.Errorf("expected fresh file kept")
		}
		// Subdirectory and its contents untouched
		if _, err := os.Stat(subDir); err != nil {
			t.Errorf("expected subdir kept")
		}
		if _, err := os.Stat(filepath.Join(subDir, "inner.txt")); err != nil {
			t.Errorf("expected inner file kept")
		}
	})

	t.Run("iterator error in cleanup is propagated", func(t *testing.T) {
		// Passing a regular file as the "dir" makes ReadDir fail.
		dir := t.TempDir()
		file := filepath.Join(dir, "afile")
		os.WriteFile(file, []byte("x"), 0600)
		err := CleanupOldUploads(file, time.Hour)
		if err == nil {
			t.Errorf("expected error when reading a non-directory, got nil")
		}
	})
}

func TestPersistAttachmentsToWorkspace_Empty(t *testing.T) {
	res, err := PersistAttachmentsToWorkspace(t.TempDir(), nil)
	if err != nil || res != nil {
		t.Errorf("expected (nil,nil) for empty attachments, got (%v, %v)", res, err)
	}
}

func TestPersistAttachmentsToWorkspace_EmptyPathReturnsAsIs(t *testing.T) {
	workspace := t.TempDir()
	attachments := []bus.FileAttachment{{Name: "x", Path: "", Temporary: true}}
	res, err := PersistAttachmentsToWorkspace(workspace, attachments)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].Path != "" {
		t.Errorf("expected empty path preserved, got %+v", res)
	}
}

func TestPersistAttachments_CopiesNonTemporaryFile(t *testing.T) {
	workspace := t.TempDir()
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "keep.txt")
	os.WriteFile(sourcePath, []byte("content"), 0600)

	res, err := PersistAttachmentsToWorkspace(workspace, []bus.FileAttachment{{
		Name: "keep.txt", Path: sourcePath, Kind: "file", Temporary: false,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result")
	}
	// Source should remain
	if _, err := os.Stat(sourcePath); err != nil {
		t.Errorf("expected source to remain after copy: %v", err)
	}
	// Target exists with content
	data, err := os.ReadFile(res[0].Path)
	if err != nil {
		t.Fatalf("failed to read copied file: %v", err)
	}
	if string(data) != "content" {
		t.Errorf("copied content mismatch")
	}
	if res[0].Temporary {
		t.Errorf("expected persisted attachment non-temporary")
	}
}

func TestPersistAttachments_FallbackNameWhenNoName(t *testing.T) {
	workspace := t.TempDir()
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "generated.txt")
	os.WriteFile(sourcePath, []byte("x"), 0600)

	res, err := PersistAttachmentsToWorkspace(workspace, []bus.FileAttachment{{
		Path:      sourcePath,
		Temporary: true,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Name != filepath.Base(res[0].Path) {
		t.Errorf("expected name to be basename of new path, got %q", res[0].Name)
	}
}

func TestSameFilePath(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "file.txt")
	b := filepath.Join(dir, "file.txt")
	c := filepath.Join(dir, "other.txt")

	if !sameFilePath(a, b) {
		t.Errorf("expected same path true for %q and %q", a, b)
	}
	if sameFilePath(a, c) {
		t.Errorf("expected different paths false")
	}
	if !sameFilePath(dir+string(os.PathSeparator), dir) {
		t.Errorf("expected path clean to normalize trailing separator")
	}
	// Case-insensitive compare on Windows-safe paths
	if !sameFilePath(filepath.Join(dir, "FILE.TXT"), filepath.Join(dir, "file.txt")) {
		t.Errorf("expected EqualFold compare")
	}
}

func TestCopyFile_MissingSource(t *testing.T) {
	if err := copyFile(filepath.Join(t.TempDir(), "missing.txt"), filepath.Join(t.TempDir(), "out.txt")); err == nil {
		t.Errorf("expected error copying missing source")
	}
}

func TestMoveFile_CrossDeviceFallsBackToCopy(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.txt")
	os.WriteFile(src, []byte("mv"), 0600)
	dst := filepath.Join(t.TempDir(), "dst.txt")

	// Force rename failure by making destination an existing directory (rename onto dir errors on Linux).
	os.MkdirAll(dst, 0755)

	moveFile(src, dst+".real")
	// After fallback, the file should have been copied to dst.real
	if _, err := os.Stat(dst + ".real"); err != nil {
		t.Errorf("expected fallback copy to create dst.real: %v", err)
	}
}