package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditTool_EditFile_Success(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("Hello World\nThis is a test"), 0644)

	tool := NewEditFileTool(tmpDir, true)
	ctx := context.Background()
	args := map[string]interface{}{
		"path":     testFile,
		"old_text": "World",
		"new_text": "Universe",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	if !result.Silent {
		t.Errorf("Expected Silent=true for EditFile, got false")
	}

	if result.ForUser != "" {
		t.Errorf("Expected ForUser to be empty for SilentResult, got: %s", result.ForUser)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read edited file: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "Hello Universe") {
		t.Errorf("Expected file to contain 'Hello Universe', got: %s", contentStr)
	}
	if strings.Contains(contentStr, "Hello World") {
		t.Errorf("Expected 'Hello World' to be replaced, got: %s", contentStr)
	}
}

func TestEditTool_EditFile_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "nonexistent.txt")

	tool := NewEditFileTool(tmpDir, true)
	ctx := context.Background()
	args := map[string]interface{}{
		"path":     testFile,
		"old_text": "old",
		"new_text": "new",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error for non-existent file")
	}

	if !strings.Contains(result.ForLLM, "not found") && !strings.Contains(result.ForUser, "not found") {
		t.Errorf("Expected 'file not found' message, got ForLLM: %s", result.ForLLM)
	}
}

func TestEditTool_EditFile_OldTextNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("Hello World"), 0644)

	tool := NewEditFileTool(tmpDir, true)
	ctx := context.Background()
	args := map[string]interface{}{
		"path":     testFile,
		"old_text": "Goodbye",
		"new_text": "Hello",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when old_text not found")
	}

	if !strings.Contains(result.ForLLM, "not found") && !strings.Contains(result.ForUser, "not found") {
		t.Errorf("Expected 'not found' message, got ForLLM: %s", result.ForLLM)
	}
}

func TestEditTool_EditFile_MultipleMatches(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("test test test"), 0644)

	tool := NewEditFileTool(tmpDir, true)
	ctx := context.Background()
	args := map[string]interface{}{
		"path":     testFile,
		"old_text": "test",
		"new_text": "done",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when old_text appears multiple times")
	}

	if !strings.Contains(result.ForLLM, "times") && !strings.Contains(result.ForUser, "times") {
		t.Errorf("Expected 'multiple times' message, got ForLLM: %s", result.ForLLM)
	}
}

func TestEditTool_EditFile_OutsideAllowedDir(t *testing.T) {
	tmpDir := t.TempDir()
	otherDir := t.TempDir()
	testFile := filepath.Join(otherDir, "test.txt")
	os.WriteFile(testFile, []byte("content"), 0644)

	tool := NewEditFileTool(tmpDir, true)
	ctx := context.Background()
	args := map[string]interface{}{
		"path":     testFile,
		"old_text": "content",
		"new_text": "new",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when path is outside allowed directory")
	}

	if !strings.Contains(result.ForLLM, "outside") && !strings.Contains(result.ForUser, "outside") {
		t.Errorf("Expected 'outside allowed' message, got ForLLM: %s", result.ForLLM)
	}
}

func TestEditTool_EditFile_MissingPath(t *testing.T) {
	tool := NewEditFileTool("", false)
	ctx := context.Background()
	args := map[string]interface{}{
		"old_text": "old",
		"new_text": "new",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when path is missing")
	}
}

func TestEditTool_EditFile_MissingOldText(t *testing.T) {
	tool := NewEditFileTool("", false)
	ctx := context.Background()
	args := map[string]interface{}{
		"path":     "/tmp/test.txt",
		"new_text": "new",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when old_text is missing")
	}
}

func TestEditTool_EditFile_MissingNewText(t *testing.T) {
	tool := NewEditFileTool("", false)
	ctx := context.Background()
	args := map[string]interface{}{
		"path":     "/tmp/test.txt",
		"old_text": "old",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when new_text is missing")
	}
}

func TestEditTool_AppendFile_Success(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("Initial content"), 0644)

	tool := NewAppendFileTool("", false)
	ctx := context.Background()
	args := map[string]interface{}{
		"path":    testFile,
		"content": "\nAppended content",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	if !result.Silent {
		t.Errorf("Expected Silent=true for AppendFile, got false")
	}

	if result.ForUser != "" {
		t.Errorf("Expected ForUser to be empty for SilentResult, got: %s", result.ForUser)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "Initial content") {
		t.Errorf("Expected original content to remain, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "Appended content") {
		t.Errorf("Expected appended content, got: %s", contentStr)
	}
}

func TestEditTool_AppendFile_MissingPath(t *testing.T) {
	tool := NewAppendFileTool("", false)
	ctx := context.Background()
	args := map[string]interface{}{
		"content": "test",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when path is missing")
	}
}

func TestEditTool_AppendFile_MissingContent(t *testing.T) {
	tool := NewAppendFileTool("", false)
	ctx := context.Background()
	args := map[string]interface{}{
		"path": "/tmp/test.txt",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when content is missing")
	}
}

func TestSmartEditTool_DirectWrite_Success(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "smart.txt")
	os.WriteFile(testFile, []byte("Hello World\nThis is a test"), 0644)

	tool := NewSmartEditTool(tmpDir, true)
	ctx := context.Background()
	args := map[string]interface{}{
		"path":     testFile,
		"old_text": "World",
		"new_text": "Universe",
	}

	result := tool.Execute(ctx, args)
	if result.IsError {
		t.Fatalf("Expected success, got error: %s", result.ForLLM)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read edited file: %v", err)
	}
	if string(content) != "Hello Universe\nThis is a test" {
		t.Errorf("unexpected smart_edit content: %q", string(content))
	}

	if _, err := os.Stat(testFile + ".fmod.tmp"); !os.IsNotExist(err) {
		t.Errorf("expected no legacy temp file, got err=%v", err)
	}
}

func TestSequentialReplaceTool_DirectWrite_Success(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "seq.txt")
	os.WriteFile(testFile, []byte("one two three"), 0644)

	tool := NewSequentialReplaceTool(tmpDir, true)
	ctx := context.Background()
	args := map[string]interface{}{
		"path":  testFile,
		"pairs": `[{"old":"one","new":"1"},{"old":"three","new":"3"}]`,
	}

	result := tool.Execute(ctx, args)
	if result.IsError {
		t.Fatalf("Expected success, got error: %s", result.ForLLM)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read edited file: %v", err)
	}
	if string(content) != "1 two 3" {
		t.Errorf("unexpected sequential_replace content: %q", string(content))
	}

	if _, err := os.Stat(testFile + ".fmod.tmp"); !os.IsNotExist(err) {
		t.Errorf("expected no legacy temp file, got err=%v", err)
	}
}

func TestPatchTool_DirectWrite_Success(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "patch.txt")
	os.WriteFile(testFile, []byte("Hello World\nThis is a test"), 0644)

	tool := NewPatchTool(tmpDir, true)
	ctx := context.Background()
	args := map[string]interface{}{
		"path": testFile,
		"diff": "@@ -1,2 +1,2 @@\n-Hello World\n+Hello Universe\n This is a test",
	}

	result := tool.Execute(ctx, args)
	if result.IsError {
		t.Fatalf("Expected success, got error: %s", result.ForLLM)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read patched file: %v", err)
	}
	if string(content) != "Hello Universe\nThis is a test" {
		t.Errorf("unexpected patch content: %q", string(content))
	}

	if _, err := os.Stat(testFile + ".fmod.tmp"); !os.IsNotExist(err) {
		t.Errorf("expected no legacy temp file, got err=%v", err)
	}
}
