package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppendFileTool_ValidatePathError exercises the validatePath error branch
// in AppendFileTool.Execute (edit.go:150-152) via a path outside the workspace.
func TestAppendFileTool_ValidatePathError(t *testing.T) {
	tool := NewAppendFileTool(t.TempDir(), true)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":    "/etc/shadow",
		"content": "x",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected validatePath error for path outside workspace")
	}
}

// TestPatchTool_ValidatePathError exercises patch.go:83-85.
func TestPatchTool_ValidatePathError(t *testing.T) {
	tool := NewPatchTool(t.TempDir(), true)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": "/etc/shadow",
		"diff": "@@ -1 +1 @@\n-old\n+new",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected validatePath error for path outside workspace")
	}
}

// TestPatchTool_InvalidDiffPathError exercises the diff-from-file path with a
// diff path that fails validation (patch.go:99-101).
func TestPatchTool_InvalidDiffPathError(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "target.txt")
	if err := os.WriteFile(target, []byte("old content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewPatchTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": "target.txt",
		"diff": "@/etc/shadow",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected invalid diff file path error")
	}
}

// TestPatchTool_ReadDiffFileError exercises patch.go:103-106 (failed to read
// diff file) by pointing at a missing diff file.
func TestPatchTool_ReadDiffFileError(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "target.txt")
	if err := os.WriteFile(target, []byte("old content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewPatchTool(tmpDir, true)
	missing := filepath.Join(tmpDir, "nope.diff")
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": "target.txt",
		"diff": "@" + missing,
	})
	if result == nil || !result.IsError {
		t.Fatal("expected read diff file error")
	}
	if !strings.Contains(result.ForLLM, "failed to read diff file") {
		t.Errorf("ForLLM = %q", result.ForLLM)
	}
}

// TestPatchTool_MissingTargetFile exercises the os.Stat file-not-found branch
// (patch.go:88-90).
func TestPatchTool_MissingTargetFile(t *testing.T) {
	tool := NewPatchTool(t.TempDir(), true)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": "missing.txt",
		"diff": "@@ -1 +1 @@\n-old\n+new",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected file-not-found error")
	}
}

// TestPatchTool_ReadContentError exercises patch.go:112-114 (failed to read the
// target file content) via a path that passes stat but fails read (a directory).
func TestPatchTool_ReadContentError(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewPatchTool(tmpDir, false)
	// Passing a directory as path: stat succeeds, read fails.
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": ".",
		"diff": "@@ -1 +1 @@\n-old\n+new",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected read-content error")
	}
	if !strings.Contains(result.ForLLM, "failed to read file") {
		t.Errorf("ForLLM = %q", result.ForLLM)
	}
}

// TestSmartEditTool_ReadContentError exercises smart_edit.go:104-106.
func TestSmartEditTool_ReadContentError(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewSmartEditTool(tmpDir, false)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":    ".",
		"oldValue": "x",
		"newValue": "y",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected read-content error")
	}
}

// EditFileTool validatePath error branch (edit.go:150)
func TestEditFileTool_ValidatePathError(t *testing.T) {
	tool := NewEditFileTool(t.TempDir(), true)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":    "/etc/hosts",
		"content": "x",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected validatePath error")
	}
}