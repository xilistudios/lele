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

func TestLogoConstant(t *testing.T) {
	if logo != "🦞" {
		t.Errorf("logo = %q, want %q", logo, "🦞")
	}
}
