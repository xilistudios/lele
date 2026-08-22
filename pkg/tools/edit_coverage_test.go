package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Metacotext tests for EditFileTool.
func TestEditFileTool_Metadata(t *testing.T) {
	tool := NewEditFileTool("", false)
	if tool.Name() != "edit_file" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("expected description")
	}
	params := tool.Parameters()
	if params == nil {
		t.Fatal("nil parameters")
	}
	props := params["properties"].(map[string]interface{})
	for _, k := range []string{"path", "old_text", "new_text"} {
		if _, ok := props[k]; !ok {
			t.Errorf("missing param %q", k)
		}
	}
}

// TestEditFileTool_Execute_readError targets the "failed to read file" branch:
// a path that is a directory (os.Stat succeeds, but ReadFile on a dir fails on
// most platforms) or unreadable. We use a directory to hit ReadFile failure.
func TestEditFileTool_Execute_readError(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewEditFileTool("", false)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":     tmpDir,
		"old_text": "x",
		"new_text": "y",
	})
	// os.Stat on dir succeeds; not a NotExist error so it proceeds; ReadFile on
	// a directory returns an error on Linux.
	if result == nil {
		t.Fatal("nil result")
	}
	if !result.IsError {
		t.Fatalf("expected error reading directory, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "failed to read file") {
		t.Fatalf("ForLLM = %q (expected read-file failure)", result.ForLLM)
	}
}

// TestEditFileTool_Execute_writeError targets the write failure path: make the
// file read-only after creation. On Linux, WriteFile to a 0444 file owned by the
// test user fails with permission denied.
func TestEditFileTool_Execute_writeError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; cannot test permission-denied write")
	}
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "f.txt")
	if err := os.WriteFile(testFile, []byte("hello content world"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Make the file read-only so the edit tool can read it but cannot write.
	if err := os.Chmod(testFile, 0444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(testFile, 0644) }) // ensure cleanup can delete

	tool := NewEditFileTool("", false)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":     testFile,
		"old_text": "content",
		"new_text": "replaced",
	})
	if result == nil {
		t.Fatal("nil result")
	}
	if !result.IsError {
		t.Fatalf("expected write error, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "failed to write") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestEditFileTool_SuccessNonRestricted verifies edit works with empty allowedDir.
func TestEditFileTool_SuccessNonRestricted(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "a.txt")
	os.WriteFile(testFile, []byte("before"), 0644)

	tool := NewEditFileTool("", false)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":     testFile,
		"old_text": "before",
		"new_text": "after",
	})
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.ForLLM)
	}
	if !result.Silent {
		t.Fatalf("expected silent result")
	}
	data, _ := os.ReadFile(testFile)
	if string(data) != "after" {
		t.Fatalf("content = %q", string(data))
	}
}

// AppendFileTool metadata tests.
func TestAppendFileTool_Metadata(t *testing.T) {
	tool := NewAppendFileTool("", false)
	if tool.Name() != "append_file" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("expected description")
	}
	params := tool.Parameters()
	props := params["properties"].(map[string]interface{})
	for _, k := range []string{"path", "content"} {
		if _, ok := props[k]; !ok {
			t.Errorf("missing param %q", k)
		}
	}
}

// TestAppendFileTool_Execute_opensNewFile verifies create-if-missing.
func TestAppendFileTool_Execute_opensNewFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "new.txt")

	tool := NewAppendFileTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":    "new.txt",
		"content": "hello",
	})
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.ForLLM)
	}
	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q", string(data))
	}
}

// TestAppendFileTool_Execute_writeError targets append write failure (read-only file).
func TestAppendFileTool_Execute_writeError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; cannot test permission-denied append")
	}
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "f.txt")
	os.WriteFile(testFile, []byte("x"), 0644)
	os.Chmod(testFile, 0444)
	t.Cleanup(func() { os.Chmod(testFile, 0644) })

	tool := NewAppendFileTool("", false)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":    testFile,
		"content": "more",
	})
	if result == nil || !result.IsError {
		t.Fatalf("expected append write error, got %+v", result)
	}
	if !strings.Contains(result.ForLLM, "failed to append") && !strings.Contains(result.ForLLM, "failed to open") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}
