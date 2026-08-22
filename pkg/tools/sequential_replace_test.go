package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSequentialReplaceTool_Metadata(t *testing.T) {
	tool := NewSequentialReplaceTool("", false)
	if tool.Name() != "sequential_replace" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("expected description")
	}
	props := tool.Parameters()["properties"].(map[string]interface{})
	for _, k := range []string{"path", "pairs", "regex", "flags"} {
		if _, ok := props[k]; !ok {
			t.Errorf("missing param %q", k)
		}
	}
}

// TestSequentialReplaceTool_Execute_Exact verifies exact replacements.
func TestSequentialReplaceTool_Execute_Exact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("hello world foo bar"), 0644)

	tool := NewSequentialReplaceTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":  "f.txt",
		"pairs": `[{"old":"world","new":"there"},{"old":"foo","new":"FOO"}]`,
	})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %+v", res)
	}
	if !res.Silent {
		t.Fatal("expected silent")
	}
	if !strings.Contains(res.ForLLM, "2") {
		t.Fatalf("ForLLM = %q (expected 2 replacements)", res.ForLLM)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello there FOO bar" {
		t.Fatalf("content = %q", string(data))
	}
}

// TestSequentialReplaceTool_Execute_Regex verifies regex replacement.
func TestSequentialReplaceTool_Execute_Regex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("abc123def456"), 0644)

	tool := NewSequentialReplaceTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":  "f.txt",
		"pairs": `[{"old":"[0-9]+","new":"N"}]`,
		"regex": true,
	})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %+v", res)
	}
	// Without the 'g' flag, only the first match is replaced.
	data, _ := os.ReadFile(path)
	if string(data) != "abcNdef456" {
		t.Fatalf("content = %q", string(data))
	}
}

// TestSequentialReplaceTool_Execute_RegexInvalidPattern verifies bad regex pattern error.
func TestSequentialReplaceTool_Execute_RegexInvalidPattern(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("abc"), 0644)

	tool := NewSequentialReplaceTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":  "f.txt",
		"pairs": `[{"old":"[","new":"X"}]`,
		"regex": true,
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for invalid regex pattern")
	}
}

// TestSequentialReplaceTool_Execute_Overlap verifies overlap detection error.
func TestSequentialReplaceTool_Execute_Overlap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	tool := NewSequentialReplaceTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":  "f.txt",
		"pairs": `[{"old":"hello","new":"HELLO"},{"old":"llo wo","new":"X"}]`,
	})
	if res == nil || !res.IsError {
		t.Fatal("expected overlap error")
	}
	if !strings.Contains(res.ForLLM, "overlap") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSequentialReplaceTool_Execute_NotFound verifies not-found error.
func TestSequentialReplaceTool_Execute_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	tool := NewSequentialReplaceTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":  "f.txt",
		"pairs": `[{"old":"missing","new":"X"}]`,
	})
	if res == nil || !res.IsError {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(res.ForLLM, "missing") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSequentialReplaceTool_Execute_MissingPath verifies missing path error.
func TestSequentialReplaceTool_Execute_MissingPath(t *testing.T) {
	tool := NewSequentialReplaceTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"pairs": `[{"old":"a","new":"b"}]`,
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing path")
	}
}

// TestSequentialReplaceTool_Execute_MissingPairs verifies missing pairs error.
func TestSequentialReplaceTool_Execute_MissingPairs(t *testing.T) {
	tool := NewSequentialReplaceTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{"path": "/tmp/x"})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing pairs")
	}
}

// TestSequentialReplaceTool_Execute_InvalidPairsJSON verifies parse error.
func TestSequentialReplaceTool_Execute_InvalidPairsJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	tool := NewSequentialReplaceTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":  "f.txt",
		"pairs": `not json`,
	})
	if res == nil || !res.IsError {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(res.ForLLM, "failed to parse pairs") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSequentialReplaceTool_Execute_EmptyPairs verifies empty pairs error.
func TestSequentialReplaceTool_Execute_EmptyPairs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	tool := NewSequentialReplaceTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":  "f.txt",
		"pairs": `[]`,
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for empty pairs")
	}
	if !strings.Contains(res.ForLLM, "no replacement pairs") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSequentialReplaceTool_Execute_EmptyOld verifies empty old string error.
func TestSequentialReplaceTool_Execute_EmptyOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	tool := NewSequentialReplaceTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":  "f.txt",
		"pairs": `[{"old":"","new":"X"}]`,
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for empty old")
	}
}

// TestSequentialReplaceTool_Execute_FileNotFound verifies missing file error.
func TestSequentialReplaceTool_Execute_FileNotFound(t *testing.T) {
	tool := NewSequentialReplaceTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":  "/nonexistent/missing.txt",
		"pairs": `[{"old":"a","new":"b"}]`,
	})
	if res == nil || !res.IsError {
		t.Fatal("expected file-not-found error")
	}
	if !strings.Contains(res.ForLLM, "file not found") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSequentialReplaceTool_Execute_AccessDenied verifies path validation.
func TestSequentialReplaceTool_Execute_AccessDenied(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "..", "outside.txt")
	tool := NewSequentialReplaceTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":  outside,
		"pairs": `[{"old":"a","new":"b"}]`,
	})
	if res == nil || !res.IsError {
		t.Fatal("expected access denied error")
	}
	if !strings.Contains(res.ForLLM, "access denied") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}
