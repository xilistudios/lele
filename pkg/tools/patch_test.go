package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchTool_Metadata(t *testing.T) {
	tool := NewPatchTool("", false)
	if tool.Name() != "patch" {
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
	for _, k := range []string{"path", "diff"} {
		if _, ok := props[k]; !ok {
			t.Errorf("missing param %q", k)
		}
	}
}

// TestPatchTool_Execute_Success verifies applying a unified diff to a file.
func TestPatchTool_Execute_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644)

	diff := "--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,3 @@\n line1\n-line2\n+changed2\n line3\n"

	tool := NewPatchTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path": path,
		"diff": diff,
	})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %+v", res)
	}
	if !res.Silent {
		t.Fatalf("expected silent result")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "changed2") {
		t.Fatalf("expected patched content, got: %q", string(data))
	}
}

// TestPatchTool_Execute_DiffFromFile verifies the "@path" diff file feature.
func TestPatchTool_Execute_DiffFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("aa\nbb\ncc\n"), 0644)

	diffPath := filepath.Join(dir, "changes.diff")
	os.WriteFile(diffPath, []byte("--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,3 @@\n aa\n-bb\n+BB\n cc\n"), 0644)

	tool := NewPatchTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path": "f.txt",
		"diff": "@" + diffPath,
	})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %+v", res)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "BB") {
		t.Fatalf("expected patched content, got: %q", string(data))
	}
}

// TestPatchTool_Execute_DiffFileNotFound verifies error when diff file is missing.
func TestPatchTool_Execute_DiffFileNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("aa\nbb"), 0644)

	tool := NewPatchTool(dir, true)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path": "f.txt",
		"diff": "@missing.diff",
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected error, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "failed to read diff file") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestPatchTool_Execute_PathNotProvided verifies missing path error.
func TestPatchTool_Execute_PathNotProvided(t *testing.T) {
	tool := NewPatchTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{"diff": "---\n+++\n@@ -1,1 +1,1 @@\n a\n-b\n+c\n"})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(res.ForLLM, "path is required") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestPatchTool_Execute_DiffNotProvided verifies missing diff error.
func TestPatchTool_Execute_DiffNotProvided(t *testing.T) {
	tool := NewPatchTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{"path": "/tmp/x"})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing diff")
	}
}

// TestPatchTool_Execute_FileNotFound verifies file-not-found error.
func TestPatchTool_Execute_FileNotFound(t *testing.T) {
	tool := NewPatchTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path": "/nonexistent/definitely/missing.txt",
		"diff": "@@ -1,1 +1,1 @@\n a\n-b\n+c\n",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(res.ForLLM, "file not found") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestPatchTool_Execute_InvalidDiff verifies a malformed diff returns an error.
func TestPatchTool_Execute_InvalidDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("a\nb\nc\n"), 0644)

	tool := NewPatchTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path": path,
		"diff": "this is not a valid diff",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for invalid diff")
	}
	if !strings.Contains(res.ForLLM, "failed to parse diff") && !strings.Contains(res.ForLLM, "no hunks") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestPatchTool_Execute_ContextMismatch verifies apply failure on mismatch.
func TestPatchTool_Execute_ContextMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("AAA\nBBB\nCCC\n"), 0644)

	diff := "--- a\n+++ b\n@@ -1,3 +1,3 @@\n XXX\n-BBB\n+replaced\n CCC\n"

	tool := NewPatchTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path": path,
		"diff": diff,
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for context mismatch")
	}
	if !strings.Contains(res.ForLLM, "failed to apply patch") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestPatchTool_Execute_InvalidHeader verifies invalid hunk header handling.
func TestPatchTool_Execute_InvalidHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("a\nb\n"), 0644)

	tool := NewPatchTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path": path,
		"diff": "@@ bogus @@\n a\n-b\n+c\n",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for invalid hunk header")
	}
}

// TestPatchTool_Execute_EmptyDiff verifies empty diff yields no hunks error.
func TestPatchTool_Execute_EmptyDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("a\nb\n"), 0644)

	tool := NewPatchTool("", false)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path": path,
		"diff": "",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for empty diff")
	}
}

// TestParseUnifiedDiff covers header parsing and multi-hunk content.
func TestParseUnifiedDiff(t *testing.T) {
	diff := "--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,3 @@\n a\n-b\n+c\n+ d\n@@ -5,1 +6,1 @@\n z\n-z\n+y\n\\ No newline at end of file\n"

	info, err := parseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.OldFile != "a/x.go" {
		t.Fatalf("OldFile = %q", info.OldFile)
	}
	if info.NewFile != "b/x.go" {
		t.Fatalf("NewFile = %q", info.NewFile)
	}
	if len(info.Hunks) != 2 {
		t.Fatalf("Hunks = %d, want 2", len(info.Hunks))
	}
	if info.Hunks[0].OldStart != 1 || info.Hunks[0].OldCount != 2 {
		t.Fatalf("hunk0 OldStart/OldCount = %d/%d", info.Hunks[0].OldStart, info.Hunks[0].OldCount)
	}
	if info.Hunks[0].NewStart != 1 || info.Hunks[0].NewCount != 3 {
		t.Fatalf("hunk0 NewStart/NewCount = %d/%d", info.Hunks[0].NewStart, info.Hunks[0].NewCount)
	}
}

// TestParseUnifiedDiff_NoHunks verifies an error when there are no hunks.
func TestParseUnifiedDiff_NoHunks(t *testing.T) {
	_, err := parseUnifiedDiff("--- a\n+++ b\n some content")
	if err == nil {
		t.Fatal("expected error for no hunks")
	}
	if !strings.Contains(err.Error(), "no hunks") {
		t.Fatalf("err = %v", err)
	}
}

// TestParseUnifiedDiff_InvalidHeader verifies error on malformed hunk header.
func TestParseUnifiedDiff_InvalidHeader(t *testing.T) {
	if _, err := parseUnifiedDiff("@@ nope @@"); err == nil {
		t.Fatal("expected error on bad hunk header")
	}
}

// TestParseUnifiedDiff_DefaultsOneCount verifies default counts for single-line hunks.
func TestParseUnifiedDiff_DefaultsOneCount(t *testing.T) {
	info, err := parseUnifiedDiff("@@ -3 +9 @@\n a\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Hunks[0].OldStart != 3 || info.Hunks[0].OldCount != 1 {
		t.Fatalf("old = %d,%d", info.Hunks[0].OldStart, info.Hunks[0].OldCount)
	}
	if info.Hunks[0].NewStart != 9 || info.Hunks[0].NewCount != 1 {
		t.Fatalf("new = %d,%d", info.Hunks[0].NewStart, info.Hunks[0].NewCount)
	}
}

// TestApplyDiff_EmptyFile tests applying a diff that starts at line 1 on a single-line file.
func TestApplyDiff_MultiHunkOrder(t *testing.T) {
	content := "l1\nl2\nl3\nl4\nl5\n"
	diff := &DiffInfo{
		Hunks: []*Hunk{
			{OldStart: 1, OldCount: 1, NewStart: 1, NewCount: 1, Lines: []string{"-l1", "+x1"}},
			{OldStart: 3, OldCount: 1, NewStart: 3, NewCount: 1, Lines: []string{"-l3", "+x3"}},
		},
	}
	out, err := applyDiff(content, diff)
	if err != nil {
		t.Fatalf("applyDiff: %v", err)
	}
	if out != "x1\nl2\nx3\nl4\nl5\n" {
		t.Fatalf("out = %q", out)
	}
}

// TestApplyHunk_OutOfRange verifies hunk start outside file returns error.
func TestApplyHunk_OutOfRange(t *testing.T) {
	err := applyHunk([]string{"a", "b"}, &Hunk{OldStart: 10, Lines: []string{" a"}})
	if err == nil {
		t.Fatal("expected error for out-of-range hunk")
	}
}

// TestApplyHunk_ShortFile verifies expected-line beyond file returns error.
func TestApplyHunk_ShortFile(t *testing.T) {
	err := applyHunk([]string{"a"}, &Hunk{OldStart: 1, Lines: []string{" a", " b"}})
	if err == nil {
		t.Fatal("expected error for file too short")
	}
}

// TestRebuildLines verifies line reconstruction with context/removal/addition.
func TestRebuildLines(t *testing.T) {
	lines := []string{"a", "b", "c", "d"}
	hunk := &Hunk{
		OldStart: 0,
		Lines:    []string{" a", "-b", "+X", " c"},
	}
	out := rebuildLines(lines, hunk)
	want := []string{"a", "X", "c", "d"}
	if len(out) != len(want) {
		t.Fatalf("out = %v, want %v", out, want)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("out = %v, want %v", out, want)
		}
	}
}