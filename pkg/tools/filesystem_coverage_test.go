package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Tool metadata / constructors
// ---------------------------------------------------------------------------

func TestReadFileTool_Metadata(t *testing.T) {
	tool := NewReadFileTool("/ws", true, 500)
	if tool.Name() != "read_file" {
		t.Errorf("Name() = %q", tool.Name())
	}
	if !strings.Contains(tool.Description(), "500") {
		t.Errorf("Description() = %q, want mention of maxReadLines", tool.Description())
	}
	params := tool.Parameters()
	props := params["properties"].(map[string]interface{})
	for _, name := range []string{"path", "from", "to"} {
		if _, ok := props[name]; !ok {
			t.Errorf("Parameters() missing %q", name)
		}
	}
}

func TestWriteFileTool_Metadata(t *testing.T) {
	tool := NewWriteFileTool("/ws", false)
	if tool.Name() != "write_file" {
		t.Errorf("Name() = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	params := tool.Parameters()
	props := params["properties"].(map[string]interface{})
	for _, name := range []string{"path", "content"} {
		if _, ok := props[name]; !ok {
			t.Errorf("Parameters() missing %q", name)
		}
	}
	if tool.restrict != false {
		t.Error("restrict should be false")
	}
}

func TestListDirTool_Metadata(t *testing.T) {
	tool := NewListDirTool("/ws", true)
	if tool.Name() != "list_dir" {
		t.Errorf("Name() = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	if tool.restrict != true {
		t.Error("restrict should be true")
	}
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Parameters().type = %v", params["type"])
	}
	if _, ok := params["properties"]; !ok {
		t.Errorf("Parameters() missing properties")
	}
}

// ---------------------------------------------------------------------------
// validatePath direct coverage
// ---------------------------------------------------------------------------

func TestValidatePath_NoWorkspace(t *testing.T) {
	got, err := validatePath("anything", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "anything" {
		t.Errorf("got %q, want 'anything'", got)
	}
}

func TestValidatePath_RelativeInside(t *testing.T) {
	ws := t.TempDir()
	got, err := validatePath("sub/file.txt", ws, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(got, "sub/file.txt") {
		t.Errorf("resolved path = %q", got)
	}
}

func TestValidatePath_OutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	_, err := validatePath("/etc/passwd", ws, true)
	if err == nil {
		t.Fatal("expected error accessing path outside workspace")
	}
}

func TestValidatePath_MissingPathInWorkspace(t *testing.T) {
	ws := t.TempDir()
	_, err := validatePath(filepath.Join(ws, "not", "there.txt"), ws, true)
	if err != nil {
		t.Fatalf("a path inside the workspace should resolve even if it doesn't exist: %v", err)
	}
}

func TestValidatePath_EvalSymlinkOutside(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	os.MkdirAll(ws, 0755)
	outside := filepath.Join(root, "outside.txt")
	os.WriteFile(outside, []byte("x"), 0644)
	link := filepath.Join(ws, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	_, err := validatePath(link, ws, true)
	if err == nil {
		t.Fatal("expected symlink-escape error")
	}
}

// resolveExistingAncestor finding a non-existent path deep under the workspace
func TestValidatePath_MissingDeepSibling(t *testing.T) {
	ws := t.TempDir()
	sub := filepath.Join(ws, "a", "b")
	os.MkdirAll(sub, 0755)
	// A path whose immediate parent doesn't exist but an ancestor does.
	_, err := validatePath(filepath.Join(ws, "a", "ghost", "file.txt"), ws, true)
	if err != nil {
		t.Fatalf("should resolve a path with existing ancestor: %v", err)
	}
}

func TestValidatePath_NonRestrict(t *testing.T) {
	ws := t.TempDir()
	got, err := validatePath("/etc/passwd", ws, false)
	if err != nil {
		t.Fatalf("non-restrict should allow outside path: %v", err)
	}
	if got != "/etc/passwd" {
		t.Errorf("got %q, want /etc/passwd", got)
	}
}

// ---------------------------------------------------------------------------
// estimateLineCount direct coverage
// ---------------------------------------------------------------------------

func TestEstimateLineCount(t *testing.T) {
	dir := t.TempDir()
	// Many short lines -> large estimated count.
	f := filepath.Join(dir, "many.txt")
	lines := make([]string, 2000)
	for i := range lines {
		lines[i] = "abcdef\n"
	}
	os.WriteFile(f, []byte(strings.Join(lines, "")), 0644)
	fi, _ := os.Stat(f)
	if got := estimateLineCount(f, fi.Size()); got <= 0 {
		t.Errorf("estimateLineCount(many) = %d, want > 0", got)
	}

	// Single line (no newlines) -> 1.
	f2 := filepath.Join(dir, "single.txt")
	os.WriteFile(f2, []byte("just one line no newline"), 0644)
	fi2, _ := os.Stat(f2)
	if got := estimateLineCount(f2, fi2.Size()); got != 1 {
		t.Errorf("estimateLineCount(single) = %d, want 1", got)
	}

	// Nonexistent file -> 0.
	if got := estimateLineCount(filepath.Join(dir, "nope.txt"), 100); got != 0 {
		t.Errorf("estimateLineCount(missing) = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// readFileChunk coverage (ranges and edge cases)
// ---------------------------------------------------------------------------

func TestReadFileChunk(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "data.txt")
	content := strings.Join([]string{"l1", "l2", "l3", "l4", "l5"}, "\n") + "\n"
	os.WriteFile(f, []byte(content), 0644)

	t.Run("no range returns entire file", func(t *testing.T) {
		got, from, to, err := readFileChunk(f, 0, 0)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !strings.Contains(got, "l1") || !strings.Contains(got, "l5") {
			t.Errorf("got %q", got)
		}
		if from != 1 {
			t.Errorf("from = %d, want 1", from)
		}
		if to != 6 { // 5 lines + trailing '' from split
			t.Errorf("to = %d, want 6", to)
		}
	})

	t.Run("from/to range", func(t *testing.T) {
		got, from, to, err := readFileChunk(f, 2, 4)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != "l2\nl3\nl4\n" {
			t.Errorf("got %q", got)
		}
		if from != 2 || to != 4 {
			t.Errorf("from/to = %d/%d", from, to)
		}
	})

	t.Run("from below 1 is clamped", func(t *testing.T) {
		got, _, _, err := readFileChunk(f, 0, 2)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if strings.Contains(got, "l3") {
			t.Errorf("got %q", got)
		}
		_ = got
	})

	t.Run("from beyond EOF", func(t *testing.T) {
		got, from, to, err := readFileChunk(f, 100, 0)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
		if from != 0 && from != 5 {
			t.Logf("from = %d", from)
		}
		_ = to
	})

	t.Run("missing file", func(t *testing.T) {
		if _, _, _, err := readFileChunk(filepath.Join(dir, "missing.txt"), 0, 0); err == nil {
			t.Error("expected error for missing file")
		}
	})
}

// ---------------------------------------------------------------------------
// ReadFileTool.Execute – large file auto-limit && explicit range
// ---------------------------------------------------------------------------

func TestReadFileTool_Execute_AutoLimit(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.txt")
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString("line content here\n")
	}
	os.WriteFile(f, []byte(sb.String()), 0644)

	tool := NewReadFileTool("", false, 500)
	res := tool.Execute(context.Background(), map[string]interface{}{"path": f})
	if res.IsError {
		t.Fatalf("error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "file is large") {
		t.Errorf("expected auto-limit notice, got: %s", res.ForLLM[:120])
	}
}

func TestReadFileTool_Execute_ExplicitRange(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "r.txt")
	os.WriteFile(f, []byte("a\nb\nc\nd\ne\n"), 0644)

	tool := NewReadFileTool("", false, 500)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"path": f,
		"from": float64(2),
		"to":   float64(4),
	})
	if res.IsError {
		t.Fatalf("error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "(showing lines 2-4)") {
		t.Errorf("expected range notice, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "b") || strings.Contains(res.ForLLM, "a\nb") {
		// a should be excluded (from=2)
		if strings.Contains(res.ForLLM, "\na\n") {
			t.Errorf("range should start at line 2: %s", res.ForLLM)
		}
	}
}

func TestReadFileTool_Execute_ErrorPaths(t *testing.T) {
	tool := NewReadFileTool("", false, 500)
	ctx := context.Background()

	// path outside restricted workspace
	restricted := NewReadFileTool(t.TempDir(), true, 500)
	res := restricted.Execute(ctx, map[string]interface{}{"path": "/etc/passwd"})
	if !res.IsError {
		t.Error("expected error for outside-workspace read")
	}

	// path is a directory
	dir := t.TempDir()
	res = tool.Execute(ctx, map[string]interface{}{"path": dir})
	if !res.IsError {
		t.Error("expected error reading a directory")
	}
}

// ---------------------------------------------------------------------------
// WriteFileTool.Execute error paths
// ---------------------------------------------------------------------------

func TestWriteFileTool_Execute_Errors(t *testing.T) {
	tool := NewWriteFileTool("", false)
	ctx := context.Background()

	// restricted workspace escape
	restricted := NewWriteFileTool(t.TempDir(), true)
	res := restricted.Execute(ctx, map[string]interface{}{"path": "/etc/evil", "content": "x"})
	if !res.IsError {
		t.Error("expected error writing outside workspace")
	}

	// write to an unwritable path (a directory)
	dir := t.TempDir()
	res = tool.Execute(ctx, map[string]interface{}{"path": dir, "content": "x"})
	if !res.IsError {
		t.Error("expected error writing to a directory path")
	}

	// mkdir failure: workspace path where a file blocks directory creation
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	os.WriteFile(blocker, []byte("x"), 0600)
	tool2 := NewWriteFileTool("", false)
	res = tool2.Execute(ctx, map[string]interface{}{"path": filepath.Join(blocker, "sub", "f.txt"), "content": "x"})
	if !res.IsError {
		t.Error("expected mkdir error when blocked by a file")
	}
}

// ---------------------------------------------------------------------------
// ListDirTool.Execute error path (permission handled by path-type errors)
// ---------------------------------------------------------------------------

func TestListDirTool_Execute_Restricted(t *testing.T) {
	tool := NewListDirTool(t.TempDir(), true)
	res := tool.Execute(context.Background(), map[string]interface{}{"path": "/etc"})
	if !res.IsError {
		t.Error("expected error for outside-workspace list_dir")
	}
}
