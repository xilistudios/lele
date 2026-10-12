package tools

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// newMemoryTool is a helper building an UpdateMemoryTool rooted at dir.
func newMemoryTool(dir string, maxLines int) *UpdateMemoryTool {
	return NewUpdateMemoryTool(dir, maxLines)
}

// writeMemoryFile seeds MEMORY.md in the workspace dir.
func writeMemoryFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, MemoryFileName), []byte(content), 0644); err != nil {
		t.Fatalf("failed to seed %s: %v", MemoryFileName, err)
	}
}

// readMemoryFile reads MEMORY.md from the workspace dir.
func readMemoryFile(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, MemoryFileName))
	if err != nil {
		t.Fatalf("failed to read %s: %v", MemoryFileName, err)
	}
	return string(data)
}

// memoryFileExists reports whether MEMORY.md exists in the workspace dir.
func memoryFileExists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, MemoryFileName))
	return err == nil
}

func TestCountMemoryLines(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a\n", 1},
		{"a\nb\n", 2},
		{"a\nb", 2},
		{"\n", 1},
		{"a\n\n", 2},
	}
	for _, tt := range tests {
		if got := countMemoryLines(tt.in); got != tt.want {
			t.Errorf("countMemoryLines(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestUpdateMemoryTool_AppendCreatesFile(t *testing.T) {
	dir := t.TempDir()
	tool := newMemoryTool(dir, 5)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "hello",
	})

	if result.IsError {
		t.Fatalf("expected success, got IsError=true: %s", result.ForLLM)
	}
	if !result.Silent {
		t.Errorf("expected Silent=true, got false")
	}
	if got := readMemoryFile(t, dir); got != "hello\n" {
		t.Errorf("expected file %q, got %q", "hello\n", got)
	}
	if !strings.Contains(result.ForLLM, "1/5") {
		t.Errorf("expected ForLLM to report budget 1/5, got: %s", result.ForLLM)
	}
}

func TestUpdateMemoryTool_AppendPreservesExisting(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "a\n")
	tool := newMemoryTool(dir, 10)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "b",
	})

	if result.IsError {
		t.Fatalf("expected success, got IsError=true: %s", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "a\nb\n" {
		t.Errorf("expected file %q, got %q", "a\nb\n", got)
	}
}

func TestUpdateMemoryTool_AppendJoinsMissingNewline(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "a")
	tool := newMemoryTool(dir, 10)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "b",
	})

	if result.IsError {
		t.Fatalf("expected success, got IsError=true: %s", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "a\nb\n" {
		t.Errorf("expected file %q (lines must not be glued), got %q", "a\nb\n", got)
	}
}

func TestUpdateMemoryTool_ReplaceOverwrites(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "l1\nl2\nl3\nl4\nl5\n")
	tool := newMemoryTool(dir, 10)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "x",
		"mode":    "replace",
	})

	if result.IsError {
		t.Fatalf("expected success, got IsError=true: %s", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "x\n" {
		t.Errorf("expected file %q, got %q", "x\n", got)
	}
}

func TestUpdateMemoryTool_DefaultModeIsAppend(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "a\n")
	tool := newMemoryTool(dir, 10)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "b",
		// no "mode" key
	})

	if result.IsError {
		t.Fatalf("expected success, got IsError=true: %s", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "a\nb\n" {
		t.Errorf("expected append semantics (file %q), got %q", "a\nb\n", got)
	}
	if !strings.Contains(result.ForLLM, "Memory updated (append)") {
		t.Errorf("expected append mode in result, got: %s", result.ForLLM)
	}
}

func TestUpdateMemoryTool_ModeCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "old\n")
	tool := newMemoryTool(dir, 10)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "new",
		"mode":    "REPLACE",
	})

	if result.IsError {
		t.Fatalf("expected success for uppercase mode, got IsError=true: %s", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "new\n" {
		t.Errorf("expected replace semantics (file %q), got %q", "new\n", got)
	}
}

func TestUpdateMemoryTool_InvalidMode(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "unchanged\n")
	tool := newMemoryTool(dir, 10)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "payload",
		"mode":    "prepend",
	})

	if !result.IsError {
		t.Fatalf("expected error for invalid mode, got IsError=false")
	}
	if !strings.Contains(result.ForLLM, `invalid mode "prepend"`) {
		t.Errorf("expected invalid-mode message, got: %s", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "unchanged\n" {
		t.Errorf("expected file untouched, got %q", got)
	}
}

func TestUpdateMemoryTool_MissingContent(t *testing.T) {
	dir := t.TempDir()
	tool := newMemoryTool(dir, 5)

	result := tool.Execute(context.Background(), map[string]interface{}{})

	if !result.IsError {
		t.Fatalf("expected error when content is missing")
	}
	if result.ForLLM != "content is required" {
		t.Errorf("expected %q, got %q", "content is required", result.ForLLM)
	}
	if memoryFileExists(dir) {
		t.Errorf("expected no file to be created")
	}
}

func TestUpdateMemoryTool_BlankContent(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "unchanged\n")
	tool := newMemoryTool(dir, 5)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "   \n",
	})

	if !result.IsError {
		t.Fatalf("expected error for blank content")
	}
	if result.ForLLM != "content must not be empty" {
		t.Errorf("expected %q, got %q", "content must not be empty", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "unchanged\n" {
		t.Errorf("expected file untouched, got %q", got)
	}
}

func TestUpdateMemoryTool_CapExceededAppendWritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "a\nb\n")
	tool := newMemoryTool(dir, 3)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "c\nd",
	})

	if !result.IsError {
		t.Fatalf("expected cap error, got IsError=false: %s", result.ForLLM)
	}
	for _, want := range []string{"4", "3", "replace"} {
		if !strings.Contains(result.ForLLM, want) {
			t.Errorf("expected cap error to contain %q, got: %s", want, result.ForLLM)
		}
	}
	// Nothing may have been written: byte-compare before/after.
	if got := readMemoryFile(t, dir); got != "a\nb\n" {
		t.Errorf("expected file untouched after cap rejection, got %q", got)
	}
}

func TestUpdateMemoryTool_CapExceededReplaceWritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "old\n")
	tool := newMemoryTool(dir, 3)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "l1\nl2\nl3\nl4",
		"mode":    "replace",
	})

	if !result.IsError {
		t.Fatalf("expected cap error, got IsError=false: %s", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "old\n" {
		t.Errorf("expected file untouched after cap rejection, got %q", got)
	}
}

func TestUpdateMemoryTool_CapBoundaryAllowed(t *testing.T) {
	dir := t.TempDir()
	tool := newMemoryTool(dir, 3)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "a\nb\nc",
		"mode":    "replace",
	})

	if result.IsError {
		t.Fatalf("expected exact-fit write to be allowed, got IsError=true: %s", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "a\nb\nc\n" {
		t.Errorf("expected file %q, got %q", "a\nb\nc\n", got)
	}
	if !strings.Contains(result.ForLLM, "3/3") {
		t.Errorf("expected ForLLM to report budget 3/3, got: %s", result.ForLLM)
	}
}

func TestUpdateMemoryTool_CapExceededThenCompactedRecovers(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "a\nb\nc\n")
	tool := newMemoryTool(dir, 3)

	// Over-cap append is rejected...
	rejected := tool.Execute(context.Background(), map[string]interface{}{
		"content": "d",
	})
	if !rejected.IsError {
		t.Fatalf("expected over-cap append to be rejected, got IsError=false: %s", rejected.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "a\nb\nc\n" {
		t.Fatalf("expected file untouched after rejection, got %q", got)
	}

	// ...then a compacting replace under the cap succeeds.
	recovered := tool.Execute(context.Background(), map[string]interface{}{
		"content": "a\nb",
		"mode":    "replace",
	})
	if recovered.IsError {
		t.Fatalf("expected compacted replace to succeed, got IsError=true: %s", recovered.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "a\nb\n" {
		t.Errorf("expected file %q after recovery, got %q", "a\nb\n", got)
	}
}

func TestUpdateMemoryTool_UnlimitedCap(t *testing.T) {
	dir := t.TempDir()
	tool := newMemoryTool(dir, 0) // unlimited

	var lines []string
	for i := 0; i < 1000; i++ {
		lines = append(lines, "line")
	}

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": strings.Join(lines, "\n"),
		"mode":    "replace",
	})

	if result.IsError {
		t.Fatalf("expected unlimited cap to accept 1000 lines, got IsError=true: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "/") {
		t.Errorf("expected no n/cap budget form when uncapped, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "1000 lines") {
		t.Errorf("expected line count in result, got: %s", result.ForLLM)
	}
}

func TestUpdateMemoryTool_EmptyWorkspaceRejected(t *testing.T) {
	tool := NewUpdateMemoryTool("", 5)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "hello",
	})

	if !result.IsError {
		t.Fatalf("expected error for empty workspace")
	}
	if result.ForLLM != "memory workspace is not configured" {
		t.Errorf("expected %q, got %q", "memory workspace is not configured", result.ForLLM)
	}
}

func TestUpdateMemoryTool_MissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	tool := newMemoryTool(dir, 5)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "fresh",
		"mode":    "replace",
	})

	if result.IsError {
		t.Fatalf("expected missing file to be treated as empty content, got IsError=true: %s", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "fresh\n" {
		t.Errorf("expected file %q, got %q", "fresh\n", got)
	}
}

func TestUpdateMemoryTool_SilentSuccess(t *testing.T) {
	dir := t.TempDir()
	tool := newMemoryTool(dir, 5)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "hello",
	})

	if result.IsError {
		t.Fatalf("expected success, got IsError=true: %s", result.ForLLM)
	}
	if !result.Silent {
		t.Errorf("expected Silent=true on success, got false")
	}
	if result.ForUser != "" {
		t.Errorf("expected ForUser empty on success, got: %s", result.ForUser)
	}
}

func TestUpdateMemoryTool_ParametersSchema(t *testing.T) {
	tool := NewUpdateMemoryTool("/tmp", 5)

	if tool.Name() != "update_memory" {
		t.Errorf("expected Name() %q, got %q", "update_memory", tool.Name())
	}

	params := tool.Parameters()

	required, ok := params["required"].([]string)
	if !ok {
		t.Fatalf("expected required to be []string, got %T", params["required"])
	}
	if !reflect.DeepEqual(required, []string{"content"}) {
		t.Errorf("expected required [content], got %v", required)
	}

	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties to be a map, got %T", params["properties"])
	}
	mode, ok := props["mode"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected mode property to be a map, got %T", props["mode"])
	}
	enum, ok := mode["enum"].([]string)
	if !ok {
		t.Fatalf("expected mode.enum to be []string, got %T", mode["enum"])
	}
	if !reflect.DeepEqual(enum, []string{"append", "replace"}) {
		t.Errorf("expected mode enum [append replace], got %v", enum)
	}
	if _, ok := props["content"]; !ok {
		t.Errorf("expected content property to be declared")
	}
}

func TestUpdateMemoryTool_DescriptionMentionsCap(t *testing.T) {
	capped := NewUpdateMemoryTool("/tmp", 5)
	desc := capped.Description()
	if !strings.Contains(desc, "5 lines") {
		t.Errorf("expected capped description to mention the cap, got: %s", desc)
	}
	for _, mode := range []string{"append", "replace"} {
		if !strings.Contains(desc, mode) {
			t.Errorf("expected description to name mode %q, got: %s", mode, desc)
		}
	}

	uncapped := NewUpdateMemoryTool("/tmp", 0)
	desc = uncapped.Description()
	if strings.Contains(desc, "lines:") {
		t.Errorf("expected uncapped description to have no cap sentence, got: %s", desc)
	}
}
