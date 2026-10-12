package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MemoryFileName is the long-term memory document, at the workspace root.
// Mirrors pkg/agent/memory.go's memoryFile (that package can't be imported
// here: pkg/agent imports pkg/tools).
const MemoryFileName = "MEMORY.md"

// UpdateMemoryTool writes the workspace's MEMORY.md so facts persist across
// sessions. It supports appending a fragment or replacing the whole file, and
// refuses to write when the result would exceed a configured line cap.
type UpdateMemoryTool struct {
	workspace string
	maxLines  int // <= 0 means "no cap"
}

// NewUpdateMemoryTool creates an update_memory tool bound to a workspace.
// maxLines <= 0 disables the cap inside the tool; resolving a non-positive
// config value to a default is the caller's job.
func NewUpdateMemoryTool(workspace string, maxLines int) *UpdateMemoryTool {
	return &UpdateMemoryTool{workspace: workspace, maxLines: maxLines}
}

func (t *UpdateMemoryTool) Name() string {
	return "update_memory"
}

func (t *UpdateMemoryTool) Description() string {
	const base = "Update your long-term memory (MEMORY.md) so facts persist across sessions. " +
		"mode=\"append\" (default) adds content to the end; mode=\"replace\" overwrites the file."
	if t.maxLines > 0 {
		return fmt.Sprintf("%s The file must stay within %d lines: if a write would exceed "+
			"that it is rejected and you must compact by rewriting the whole file with "+
			"mode \"replace\".", base, t.maxLines)
	}
	return base
}

func (t *UpdateMemoryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The memory text to write. For \"append\", pass only the new fragment; for \"replace\", pass the complete new contents of MEMORY.md.",
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"append", "replace"},
				"description": "How to apply content: \"append\" (default) adds it to the end of MEMORY.md; \"replace\" overwrites the file entirely.",
			},
		},
		"required": []string{"content"},
	}
}

func (t *UpdateMemoryTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}
	if strings.TrimSpace(content) == "" {
		return ErrorResult("content must not be empty")
	}

	mode := "append"
	if raw, ok := args["mode"].(string); ok && strings.TrimSpace(raw) != "" {
		mode = strings.ToLower(strings.TrimSpace(raw))
	}
	if mode != "append" && mode != "replace" {
		return ErrorResult(fmt.Sprintf("invalid mode %q: expected \"append\" or \"replace\"", mode))
	}

	// Guard against writing MEMORY.md into the process CWD when the agent was
	// built without a workspace.
	if t.workspace == "" {
		return ErrorResult("memory workspace is not configured")
	}

	path := filepath.Join(t.workspace, MemoryFileName)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return ErrorResult(fmt.Sprintf("failed to read %s: %v", MemoryFileName, err))
	}

	var joined string
	if mode == "replace" {
		joined = content
	} else {
		joined = string(existing)
		if joined != "" && !strings.HasSuffix(joined, "\n") {
			joined += "\n"
		}
		joined += content
	}
	// The file must end with exactly one terminating newline.
	if !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}

	n := countMemoryLines(joined)
	// Cap check happens before any write: on overflow nothing is written at all.
	if t.maxLines > 0 && n > t.maxLines {
		return ErrorResult(fmt.Sprintf(
			"update_memory rejected: the result would be %d lines, over the %d-line cap. "+
				"Nothing was written. Rewrite the whole file with mode \"replace\" using %d lines or fewer.",
			n, t.maxLines, t.maxLines))
	}

	if err := os.WriteFile(path, []byte(joined), 0644); err != nil {
		return ErrorResult(fmt.Sprintf("failed to write %s: %v", MemoryFileName, err))
	}

	if t.maxLines > 0 {
		return SilentResult(fmt.Sprintf("Memory updated (%s): %d/%d lines.", mode, n, t.maxLines))
	}
	return SilentResult(fmt.Sprintf("Memory updated (%s): %d lines.", mode, n))
}

// countMemoryLines counts logical lines; a single trailing newline is a
// terminator, not a line: "" -> 0, "a" -> 1, "a\n" -> 1, "a\nb\n" -> 2,
// "\n" -> 1, "a\n\n" -> 2.
func countMemoryLines(s string) int {
	if s == "" {
		return 0
	}
	trimmed := strings.TrimSuffix(s, "\n")
	if trimmed == "" {
		return 1
	}
	return strings.Count(trimmed, "\n") + 1
}
