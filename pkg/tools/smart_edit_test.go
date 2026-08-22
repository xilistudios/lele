package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSmartEditTool_Metadata(t *testing.T) {
	tool := NewSmartEditTool("", false)
	if tool.Name() != "smart_edit" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("expected description")
	}
	props := tool.Parameters()["properties"].(map[string]interface{})
	for _, k := range []string{"path", "old_text", "new_text", "regex", "flags"} {
		if _, ok := props[k]; !ok {
			t.Errorf("missing param %q", k)
		}
	}
}

// TestSmartEditTool_Execute_Exact verifies exact-match replacement.
func TestSmartEditTool_Execute_Exact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	tool := NewSmartEditTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "f.txt",
		"old_text": "world",
		"new_text": "there",
	})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "exact") {
		t.Fatalf("ForLLM = %q (expected exact strategy)", res.ForLLM)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello there" {
		t.Fatalf("content = %q", string(data))
	}
}

// TestSmartEditTool_Execute_WhitespaceTolerant verifies fallback matching.
func TestSmartEditTool_Execute_WhitespaceTolerant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("hello   multiple   spaces"), 0644)

	tool := NewSmartEditTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "f.txt",
		"old_text": "hello multiple spaces",
		"new_text": "clean",
	})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "whitespace-tolerant") {
		t.Fatalf("ForLLM = %q (expected whitespace-tolerant strategy)", res.ForLLM)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "clean" {
		t.Fatalf("content = %q", string(data))
	}
}

// TestSmartEditTool_Execute_Regex verifies regex replacement.
func TestSmartEditTool_Execute_Regex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("item1 item22"), 0644)

	tool := NewSmartEditTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "f.txt",
		"old_text": "item[0-9]+",
		"new_text": "X",
		"regex":    true,
	})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "regex") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
	// Without the 'g' flag only the first match is replaced.
	data, _ := os.ReadFile(path)
	if string(data) != "X item22" {
		t.Fatalf("content = %q", string(data))
	}
}

// TestSmartEditTool_Execute_RegexGlobal verifies the global flag surfaces multiple matches as an error.
func TestSmartEditTool_Execute_RegexGlobal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("item1 item22"), 0644)

	tool := NewSmartEditTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "f.txt",
		"old_text": "item[0-9]+",
		"new_text": "X",
		"regex":    true,
		"flags":    "g",
	})
	// With two matching occurrences, the tool reports a multiple-match error
	// (it requires a unique match regardless of the global flag).
	if res == nil || !res.IsError {
		t.Fatal("expected multiple-match error with concurrent regex matches")
	}
	if !strings.Contains(res.ForLLM, "appears") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSmartEditTool_Execute_RegexNoMatch verifies regex no-match error.
func TestSmartEditTool_Execute_RegexNoMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("abc"), 0644)

	tool := NewSmartEditTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "f.txt",
		"old_text": "zzz",
		"new_text": "X",
		"regex":    true,
	})
	if res == nil || !res.IsError {
		t.Fatal("expected no-match error")
	}
	if !strings.Contains(res.ForLLM, "regex pattern not found") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSmartEditTool_Execute_NoMatch verifies no-match error (all strategies).
func TestSmartEditTool_Execute_NoMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("abc"), 0644)

	tool := NewSmartEditTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "f.txt",
		"old_text": "zzz",
		"new_text": "X",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected no-match error")
	}
	if !strings.Contains(res.ForLLM, "not found") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSmartEditTool_Execute_MultipleMatches verifies the multiple-match error.
func TestSmartEditTool_Execute_MultipleMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("foo foo foo"), 0644)

	tool := NewSmartEditTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "f.txt",
		"old_text": "foo",
		"new_text": "bar",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected multiple-match error")
	}
	if !strings.Contains(res.ForLLM, "appears") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSmartEditTool_Execute_MissingPath verifies missing path error.
func TestSmartEditTool_Execute_MissingPath(t *testing.T) {
	tool := NewSmartEditTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"old_text": "a",
		"new_text": "b",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(res.ForLLM, "path is required") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSmartEditTool_Execute_MissingOldText verifies missing old_text error.
func TestSmartEditTool_Execute_MissingOldText(t *testing.T) {
	tool := NewSmartEditTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "/tmp/x",
		"new_text": "b",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing old_text")
	}
	if !strings.Contains(res.ForLLM, "old_text is required") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSmartEditTool_Execute_MissingNewText verifies missing new_text error.
func TestSmartEditTool_Execute_MissingNewText(t *testing.T) {
	tool := NewSmartEditTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "/tmp/x",
		"old_text": "a",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing new_text")
	}
	if !strings.Contains(res.ForLLM, "new_text is required") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSmartEditTool_Execute_FileNotFound verifies missing file error.
func TestSmartEditTool_Execute_FileNotFound(t *testing.T) {
	tool := NewSmartEditTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "/nonexistent/missing.txt",
		"old_text": "a",
		"new_text": "b",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected file-not-found error")
	}
	if !strings.Contains(res.ForLLM, "file not found") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSmartEditTool_Execute_AccessDenied verifies path validation error.
func TestSmartEditTool_Execute_AccessDenied(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "..", "out.txt")
	tool := NewSmartEditTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     outside,
		"old_text": "a",
		"new_text": "b",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected access denied error")
	}
	if !strings.Contains(res.ForLLM, "access denied") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSmartEditTool_Execute_RegexFlags verifies case-insensitive flag handling.
func TestSmartEditTool_Execute_RegexFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("Hello hello"), 0644)

	tool := NewSmartEditTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "f.txt",
		"old_text": "hello",
		"new_text": "X",
		"regex":    true,
		"flags":    "i",
	})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %+v", res)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "X hello" {
		t.Fatalf("content = %q", string(data))
	}
}

// TestSmartEditTool_Execute_WriteFailure verifies the write error branch when
// the target file cannot be written (read-only permissions).
func TestSmartEditTool_Execute_WriteFailure(t *testing.T) {
	// chmod'd read-only files can still be edited by the owner on some
	// systems, so run under a user we control by using a subprocess-free
	// approach: make the parent dir read-only won't work either since we
	// need to read the file first. Instead use a directory as the "file"
	// target, which os.ReadFile succeeds on the parent but write into fails,
	// or detect root. Simplest reliable path: point at a path we cannot open
	// for writing without removing read access — use a non-writable directory.
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission checks are unreliable")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	// Ensure the temp dir is writable so ReadFile succeeds; only the file is
	// read-only.
	tool := NewSmartEditTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path":     "f.txt",
		"old_text": "hello",
		"new_text": "goodbye",
	})
	if res == nil {
		t.Fatal("expected a result")
	}
	if !res.IsError {
		if got, _ := os.ReadFile(path); string(got) == "goodbye world" {
			t.Fatal("write unexpectedly succeeded despite read-only file")
		}
		t.Fatal("expected write failure, got success")
	}
}
