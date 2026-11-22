package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemTool_ReadFile_Success(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("test content"), 0644)

	tool := &ReadFileTool{}
	ctx := context.Background()
	args := map[string]interface{}{
		"path": testFile,
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	if !strings.Contains(result.ForLLM, "test content") {
		t.Errorf("Expected ForLLM to contain 'test content', got: %s", result.ForLLM)
	}

	if result.ForUser != "" {
		t.Errorf("Expected ForUser to be empty for NewToolResult, got: %s", result.ForUser)
	}
}

func TestFilesystemTool_ReadFile_NotFound(t *testing.T) {
	tool := &ReadFileTool{}
	ctx := context.Background()
	args := map[string]interface{}{
		"path": "/nonexistent_file_12345.txt",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error for missing file, got IsError=false")
	}

	if !strings.Contains(result.ForLLM, "failed to read") && !strings.Contains(result.ForUser, "failed to read") {
		t.Errorf("Expected error message, got ForLLM: %s, ForUser: %s", result.ForLLM, result.ForUser)
	}
}

func TestFilesystemTool_ReadFile_MissingPath(t *testing.T) {
	tool := &ReadFileTool{}
	ctx := context.Background()
	args := map[string]interface{}{}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when path is missing")
	}

	if !strings.Contains(result.ForLLM, "path is required") && !strings.Contains(result.ForUser, "path is required") {
		t.Errorf("Expected 'path is required' message, got ForLLM: %s", result.ForLLM)
	}
}

func TestFilesystemTool_WriteFile_Success(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "newfile.txt")

	tool := &WriteFileTool{}
	ctx := context.Background()
	args := map[string]interface{}{
		"path":    testFile,
		"content": "hello world",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	if !result.Silent {
		t.Errorf("Expected Silent=true for WriteFile, got false")
	}

	if result.ForUser != "" {
		t.Errorf("Expected ForUser to be empty for SilentResult, got: %s", result.ForUser)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read written file: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("Expected file content 'hello world', got: %s", string(content))
	}
}

func TestFilesystemTool_WriteFile_CreateDir(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "subdir", "newfile.txt")

	tool := &WriteFileTool{}
	ctx := context.Background()
	args := map[string]interface{}{
		"path":    testFile,
		"content": "test",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success with directory creation, got IsError=true: %s", result.ForLLM)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read written file: %v", err)
	}
	if string(content) != "test" {
		t.Errorf("Expected file content 'test', got: %s", string(content))
	}
}

func TestFilesystemTool_WriteFile_MissingPath(t *testing.T) {
	tool := &WriteFileTool{}
	ctx := context.Background()
	args := map[string]interface{}{
		"content": "test",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when path is missing")
	}
}

func TestFilesystemTool_WriteFile_MissingContent(t *testing.T) {
	tool := &WriteFileTool{}
	ctx := context.Background()
	args := map[string]interface{}{
		"path": "/tmp/test.txt",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when content is missing")
	}

	if !strings.Contains(result.ForLLM, "content is required") && !strings.Contains(result.ForUser, "content is required") {
		t.Errorf("Expected 'content is required' message, got ForLLM: %s", result.ForLLM)
	}
}

func TestFilesystemTool_ListDir_Success(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte("content"), 0644)
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)

	tool := &ListDirTool{}
	ctx := context.Background()
	args := map[string]interface{}{
		"path": tmpDir,
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	if !strings.Contains(result.ForLLM, "file1.txt") || !strings.Contains(result.ForLLM, "file2.txt") {
		t.Errorf("Expected files in listing, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "subdir") {
		t.Errorf("Expected subdir in listing, got: %s", result.ForLLM)
	}
}

func TestFilesystemTool_ListDir_NotFound(t *testing.T) {
	tool := &ListDirTool{}
	ctx := context.Background()
	args := map[string]interface{}{
		"path": "/nonexistent_directory_12345",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error for non-existent directory, got IsError=false")
	}

	if !strings.Contains(result.ForLLM, "failed to read") && !strings.Contains(result.ForUser, "failed to read") {
		t.Errorf("Expected error message, got ForLLM: %s, ForUser: %s", result.ForLLM, result.ForUser)
	}
}

func TestFilesystemTool_ListDir_DefaultPath(t *testing.T) {
	tool := &ListDirTool{}
	ctx := context.Background()
	args := map[string]interface{}{}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success with default path '.', got IsError=true: %s", result.ForLLM)
	}
}

func TestFilesystemTool_ReadFile_RejectsSymlinkEscape(t *testing.T) {

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0644); err != nil {
		t.Fatalf("failed to write secret file: %v", err)
	}

	link := filepath.Join(workspace, "leak.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	tool := NewReadFileTool(workspace, true, 500)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": link,
	})

	if !result.IsError {
		t.Fatalf("expected symlink escape to be blocked")
	}
	if !strings.Contains(result.ForLLM, "symlink resolves outside workspace") {
		t.Fatalf("expected symlink escape error, got: %s", result.ForLLM)
	}
}
