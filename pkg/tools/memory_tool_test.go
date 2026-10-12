package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// newMemoryTool is a helper building an UpdateMemoryTool rooted at dir with
// the workspace restriction on (inside-workspace regular files keep working;
// symlink escapes are denied — see TestUpdateMemoryTool_RejectsSymlinkEscape).
func newMemoryTool(dir string, maxLines int) *UpdateMemoryTool {
	return NewUpdateMemoryTool(dir, maxLines, true)
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
	tool := NewUpdateMemoryTool("", 5, true)

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
	tool := NewUpdateMemoryTool("/tmp", 5, true)

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
	capped := NewUpdateMemoryTool("/tmp", 5, true)
	desc := capped.Description()
	if !strings.Contains(desc, "5 lines") {
		t.Errorf("expected capped description to mention the cap, got: %s", desc)
	}
	for _, mode := range []string{"append", "replace"} {
		if !strings.Contains(desc, mode) {
			t.Errorf("expected description to name mode %q, got: %s", mode, desc)
		}
	}

	uncapped := NewUpdateMemoryTool("/tmp", 0, true)
	desc = uncapped.Description()
	if strings.Contains(desc, "lines:") {
		t.Errorf("expected uncapped description to have no cap sentence, got: %s", desc)
	}
}

// --- M1: workspace confinement (restrict_to_workspace) ---------------------

// TestUpdateMemoryTool_RejectsSymlinkEscape mirrors
// TestFilesystemTool_ReadFile_RejectsSymlinkEscape: with restrict on, a
// MEMORY.md symlinked outside the workspace must be denied for BOTH modes,
// exactly like write_file/append_file deny it, and the symlink target must
// stay untouched.
func TestUpdateMemoryTool_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	target := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(target, []byte("ORIGINAL\n"), 0644); err != nil {
		t.Fatalf("failed to write target file: %v", err)
	}

	link := filepath.Join(workspace, MemoryFileName)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	tool := NewUpdateMemoryTool(workspace, 5, true)
	for _, mode := range []string{"append", "replace"} {
		result := tool.Execute(context.Background(), map[string]interface{}{
			"content": "INJECTED-BY-AGENT",
			"mode":    mode,
		})
		if !result.IsError {
			t.Fatalf("mode %q: expected symlink escape to be blocked", mode)
		}
		if !strings.Contains(result.ForLLM, "access denied") {
			t.Errorf("mode %q: expected access-denied error, got: %s", mode, result.ForLLM)
		}
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("failed to read symlink target: %v", err)
	}
	if string(data) != "ORIGINAL\n" {
		t.Errorf("target file was written through the symlink: got %q", string(data))
	}
}

// TestUpdateMemoryTool_RestrictAllowsRegularFile pins the other half of the
// policy: restrict=true must not break the normal case — a regular
// in-workspace MEMORY.md keeps working.
func TestUpdateMemoryTool_RestrictAllowsRegularFile(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "a\n")
	tool := NewUpdateMemoryTool(dir, 10, true)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "b",
	})

	if result.IsError {
		t.Fatalf("expected regular in-workspace file to be writable with restrict=true, got IsError=true: %s", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "a\nb\n" {
		t.Errorf("expected file %q, got %q", "a\nb\n", got)
	}
}

// --- M2: hard byte ceiling --------------------------------------------------

// TestUpdateMemoryTool_CapExceededBytesWritesNothing: the line cap alone
// cannot stop flooding — a single 2 MiB line passes any line count. The hard
// byte ceiling must reject it with nothing written, regardless of the line
// cap being disabled (0) or generous (10000).
func TestUpdateMemoryTool_CapExceededBytesWritesNothing(t *testing.T) {
	const seed = "seed\n"
	hugeLine := strings.Repeat("a", 2*1024*1024) // one single 2 MiB line

	for _, tc := range []struct {
		name     string
		maxLines int
	}{
		{"uncapped line budget", 0},
		{"large line budget", 10000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMemoryFile(t, dir, seed)
			tool := newMemoryTool(dir, tc.maxLines)

			result := tool.Execute(context.Background(), map[string]interface{}{
				"content": hugeLine,
				"mode":    "replace",
			})

			if !result.IsError {
				t.Fatalf("expected byte-cap error, got IsError=false: %s", result.ForLLM)
			}
			for _, want := range []string{"bytes", fmt.Sprintf("%d-byte cap", maxMemoryBytes), "replace"} {
				if !strings.Contains(result.ForLLM, want) {
					t.Errorf("expected byte-cap error to contain %q, got: %s", want, result.ForLLM)
				}
			}
			// Nothing may have been written: byte-compare before/after.
			if got := readMemoryFile(t, dir); got != seed {
				t.Errorf("expected file untouched after byte-cap rejection, got %q", got)
			}
		})
	}
}

// TestUpdateMemoryTool_ByteCapBoundaryAllowed: content that lands exactly at
// (and just under) the 512 KiB ceiling is accepted — the cap is `>` not `>=`.
func TestUpdateMemoryTool_ByteCapBoundaryAllowed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bytes int // total resulting file size (joined), newline included
	}{
		{"just under the cap", maxMemoryBytes - 1},
		{"exactly at the cap", maxMemoryBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tool := newMemoryTool(dir, 0) // uncapped lines: bytes are the only limit

			// Replace mode: joined == content, so seed content of exactly the
			// target size ending in a newline.
			content := strings.Repeat("x", tc.bytes-1) + "\n"

			result := tool.Execute(context.Background(), map[string]interface{}{
				"content": content,
				"mode":    "replace",
			})

			if result.IsError {
				t.Fatalf("expected %d-byte write to be accepted, got IsError=true: %s", tc.bytes, result.ForLLM)
			}
			if got := readMemoryFile(t, dir); len(got) != tc.bytes {
				t.Errorf("expected file of %d bytes, got %d", tc.bytes, len(got))
			}
		})
	}
}

// --- M3: read-modify-write race ---------------------------------------------

// TestUpdateMemoryTool_ConcurrentAppendsNoLostUpdates: subagents share the
// parent's tool instance, so N concurrent appends must serialize on the
// tool's mutex — every one of the N distinct lines has to survive (the review
// probe lost 198 of 200 without the lock) and the file must stay under caps.
func TestUpdateMemoryTool_ConcurrentAppendsNoLostUpdates(t *testing.T) {
	const n = 50
	dir := t.TempDir()
	tool := newMemoryTool(dir, 100) // generous but real cap: 50 lines must fit

	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result := tool.Execute(context.Background(), map[string]interface{}{
				"content": fmt.Sprintf("line-%02d", i),
			})
			if result.IsError {
				errs <- result.ForLLM
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Errorf("concurrent append failed: %s", msg)
	}

	got := readMemoryFile(t, dir)
	lines := countMemoryLines(got)
	if lines != n {
		t.Errorf("expected %d lines after %d concurrent appends, got %d:\n%s", n, n, lines, got)
	}
	present := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		present[line] = true
	}
	for i := 0; i < n; i++ {
		if !present[fmt.Sprintf("line-%02d", i)] {
			t.Errorf("lost update: line-%02d missing from final file", i)
		}
	}
	if lines > 100 {
		t.Errorf("final file has %d lines, over the 100-line cap", lines)
	}
}

// --- L1: malformed mode must error, not silently append ---------------------

// TestUpdateMemoryTool_NonStringModeRejected: a present-but-non-string mode
// (e.g. a JSON number) is a malformed tool call and must error; only an absent
// or nil mode falls back to the append default.
func TestUpdateMemoryTool_NonStringModeRejected(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "unchanged\n")
	tool := newMemoryTool(dir, 10)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"content": "payload",
		"mode":    123,
	})
	if !result.IsError {
		t.Fatalf("expected error for non-string mode, got IsError=false: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "invalid mode") {
		t.Errorf("expected invalid-mode message, got: %s", result.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "unchanged\n" {
		t.Errorf("expected file untouched after malformed mode, got %q", got)
	}

	// A nil mode (JSON null) is "unset" and keeps the append default.
	nilMode := tool.Execute(context.Background(), map[string]interface{}{
		"content": "payload",
		"mode":    nil,
	})
	if nilMode.IsError {
		t.Errorf("expected nil mode to fall back to append, got IsError=true: %s", nilMode.ForLLM)
	}
	if got := readMemoryFile(t, dir); got != "unchanged\npayload\n" {
		t.Errorf("expected append for nil mode, got %q", got)
	}
}
