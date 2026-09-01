package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildFolderContext_Listing(t *testing.T) {
	dir := t.TempDir()

	// Deterministic names so lexical order (os.ReadDir) is predictable.
	if err := os.Mkdir(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatalf("mkdir alpha: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "beta"), 0o755); err != nil {
		t.Fatalf("mkdir beta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("secret contents"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden-file"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write hidden file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".hidden-dir"), 0o755); err != nil {
		t.Fatalf("mkdir hidden dir: %v", err)
	}

	got := BuildFolderContext(dir)

	// Header + absolute path.
	if !strings.HasPrefix(got, "## Selected Folder\n\n") {
		t.Errorf("missing section header, got %q", got)
	}
	if !strings.Contains(got, fmt.Sprintf("Folder: `%s`", dir)) {
		t.Errorf("missing folder path line; got:\n%s", got)
	}
	if !strings.Contains(got, "### Directory Listing (First-Level)\n") {
		t.Errorf("missing listing header; got:\n%s", got)
	}

	// Dirs get a trailing slash, files do not.
	if !strings.Contains(got, "- alpha/\n") {
		t.Errorf("expected \"- alpha/\" entry; got:\n%s", got)
	}
	if !strings.Contains(got, "- beta/\n") {
		t.Errorf("expected \"- beta/\" entry; got:\n%s", got)
	}
	if !strings.Contains(got, "- notes.txt\n") || strings.Contains(got, "- notes.txt/\n") {
		t.Errorf("expected plain \"- notes.txt\" entry (no slash); got:\n%s", got)
	}

	// Hidden names must be excluded.
	if strings.Contains(got, ".hidden") {
		t.Errorf("hidden entries must be excluded; got:\n%s", got)
	}

	// File CONTENTS must never be read into the prompt.
	if strings.Contains(got, "secret contents") {
		t.Errorf("folder context must not include file contents; got:\n%s", got)
	}
}

func TestBuildFolderContext_Empty(t *testing.T) {
	got := BuildFolderContext(t.TempDir())
	if !strings.Contains(got, "No files or directories found.") {
		t.Errorf("empty dir should report no entries; got:\n%s", got)
	}
}

func TestBuildFolderContext_TruncatesAt100(t *testing.T) {
	dir := t.TempDir()
	const total = maxHarnessDirEntries + 7 // 107
	for i := 0; i < total; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d", i)), nil, 0o644); err != nil {
			t.Fatalf("write f%03d: %v", i, err)
		}
	}

	got := BuildFolderContext(dir)

	if !strings.Contains(got, fmt.Sprintf("- ... and %d more\n", total-maxHarnessDirEntries)) {
		t.Errorf("expected truncation notice for %d hidden entries; got:\n%s", total-maxHarnessDirEntries, got)
	}
	// First 100 listed (f000..f099), the rest only counted.
	if !strings.Contains(got, "- f000\n") {
		t.Errorf("expected first entry f000; got:\n%s", got)
	}
	if strings.Contains(got, "- f100\n") {
		t.Errorf("entry beyond the limit must not be listed")
	}
	// Count the listing lines.
	count := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "- f") {
			count++
		}
	}
	if count != maxHarnessDirEntries {
		t.Errorf("listed %d entries, want %d", count, maxHarnessDirEntries)
	}
}

func TestBuildFolderContext_Nonexistent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if got := BuildFolderContext(missing); got != "" {
		t.Errorf("nonexistent dir should yield empty string, got %q", got)
	}
}

func TestBuildFolderContext_NotADirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if got := BuildFolderContext(file); got != "" {
		t.Errorf("regular file should yield empty string, got %q", got)
	}
}

func TestBuildFolderContext_EmptyInput(t *testing.T) {
	if got := BuildFolderContext(""); got != "" {
		t.Errorf("empty input should yield empty string, got %q", got)
	}
}

func TestBuildFolderContext_UnreadableDir(t *testing.T) {
	// Root bypasses permission bits, so skip when running as root.
	if os.Geteuid() == 0 {
		t.Skip("running as root; directory permissions are not enforced")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir locked: %v", err)
	}
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

	got := BuildFolderContext(sub)
	if !strings.HasPrefix(got, "## Selected Folder\n\n") {
		t.Errorf("unreadable dir should still render the header; got:\n%s", got)
	}
	if !strings.Contains(got, "(unable to list contents)") {
		t.Errorf("unreadable dir should report inability to list; got:\n%s", got)
	}
}
