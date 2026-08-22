package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetConfigPath(t *testing.T) {
	path := getConfigPath()
	if path == "" {
		t.Error("getConfigPath() returned empty string")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("getConfigPath() = %q, want absolute path", path)
	}
}

func TestGetLeleDir(t *testing.T) {
	dir := getLeleDir()
	if dir == "" {
		t.Error("getLeleDir() returned empty string")
	}
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".lele")
	if dir != expected {
		t.Errorf("getLeleDir() = %q, want %q", dir, expected)
	}
}

func TestCopyDirectory_Basic(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	srcFile := filepath.Join(srcDir, "test.txt")
	if err := os.WriteFile(srcFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := copyDirectory(srcDir, filepath.Join(dstDir, "subdir")); err != nil {
		t.Fatalf("copyDirectory failed: %v", err)
	}

	dstFile := filepath.Join(dstDir, "subdir", "test.txt")
	data, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("copied content = %q, want %q", string(data), "hello")
	}
}

func TestCopyDirectory_NestedDirectories(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	nestedDir := filepath.Join(srcDir, "nested")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	nestedFile := filepath.Join(nestedDir, "file.txt")
	if err := os.WriteFile(nestedFile, []byte("nested content"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := copyDirectory(srcDir, dstDir); err != nil {
		t.Fatalf("copyDirectory failed: %v", err)
	}

	dstNestedFile := filepath.Join(dstDir, "nested", "file.txt")
	data, err := os.ReadFile(dstNestedFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "nested content" {
		t.Errorf("copied content = %q, want %q", string(data), "nested content")
	}
}

func TestCopyDirectory_SourceNotExist(t *testing.T) {
	dstDir := t.TempDir()

	err := copyDirectory("/nonexistent/path", dstDir)
	if err == nil {
		t.Error("copyDirectory should return error when source does not exist")
	}
}

// TestCopyDirectory_DstOpenError triggers the os.OpenFile failure branch by
// making the destination itself a regular file (so joining a file under it
// yields ENOTDIR).
func TestCopyDirectory_DstOpenError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based test, skipping as root")
	}
	tmp := t.TempDir()

	// Source: a single file inside a directory.
	srcDir := filepath.Join(tmp, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Destination is a regular file, not a directory.
	dst := filepath.Join(tmp, "dstfile")
	if err := os.WriteFile(dst, nil, 0644); err != nil {
		t.Fatalf("WriteFile dst: %v", err)
	}

	if err := copyDirectory(srcDir, dst); err == nil {
		t.Error("copyDirectory should return error when opening the destination file fails")
	}
}

// TestCopyDirectory_SrcOpenError triggers the os.Open failure branch by making
// a source file unreadable.
func TestCopyDirectory_SrcOpenError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based test, skipping as root")
	}
	tmp := t.TempDir()

	srcDir := filepath.Join(tmp, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Mode 0000 makes the file unreadable to its owner.
	f := filepath.Join(srcDir, "no.txt")
	if err := os.WriteFile(f, []byte("secret"), 0000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Ensure Walk still descends and attempts to open the file.
	if err := os.Chmod(f, 0000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	dstDir := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatalf("MkdirAll dst: %v", err)
	}

	if err := copyDirectory(srcDir, dstDir); err == nil {
		t.Error("copyDirectory should return error when opening a source file fails")
	}
}

// TestCopyEmbeddedToTarget_Error triggers the MkdirAll failure branch by
// pointing the target underneath a regular file.
func TestCopyEmbeddedToTarget_Error(t *testing.T) {
	tmp := t.TempDir()
	// Create a regular file then use it as if it were a base directory.
	blocker := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blocker, nil, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	target := filepath.Join(blocker, "sub")

	if err := copyEmbeddedToTarget(target); err == nil {
		t.Error("copyEmbeddedToTarget should return error when target dir cannot be created")
	}
}

// TestCreateWorkspaceTemplates_Error verifies createWorkspaceTemplates does not
// panic when copying fails and returns quietly (it only prints an error).
func TestCreateWorkspaceTemplates_Error(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blocker, nil, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Must not panic / exit.
	createWorkspaceTemplates(filepath.Join(blocker, "sub"))
}

func TestLogoConstant(t *testing.T) {
	if logo != "🦞" {
		t.Errorf("logo = %q, want %q", logo, "🦞")
	}
}
