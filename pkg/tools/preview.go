package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/xilistudios/lele/pkg/logger"
)

// PreviewTool previews a temp file (.fmod.tmp) with optional line range.
// This allows reviewing changes before applying them.
type PreviewTool struct {
	workspace string
	restrict  bool
}

// NewPreviewTool creates a new PreviewTool
func NewPreviewTool(workspace string, restrict bool) *PreviewTool {
	return &PreviewTool{
		workspace: workspace,
		restrict:  restrict,
	}
}

func (t *PreviewTool) Name() string {
	return "preview"
}

func (t *PreviewTool) Description() string {
	return "Preview a temp file (.fmod.tmp) with optional line range. Use this to review changes before applying them."
}

func (t *PreviewTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the original file (temp file will be read)",
			},
			"from": map[string]interface{}{
				"type":        "integer",
				"description": "Start line (1-indexed, inclusive). Omit to start from line 1.",
			},
			"to": map[string]interface{}{
				"type":        "integer",
				"description": "End line (1-indexed, inclusive). Omit to read until end.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *PreviewTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	// Get optional line range
	from := 0 // 0 means from beginning
	to := 0   // 0 means until end

	if v, ok := args["from"].(float64); ok {
		from = int(v)
	} else if v, ok := args["from"].(int); ok {
		from = v
	}

	if v, ok := args["to"].(float64); ok {
		to = int(v)
	} else if v, ok := args["to"].(int); ok {
		to = v
	}

	// Validate path
	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	// Read temp file
	tempPath := GetTempPath(resolvedPath)

	if _, err := os.Stat(tempPath); os.IsNotExist(err) {
		return ErrorResult(fmt.Sprintf("temp file not found: %s. Use 'smart_edit' first to create it.", tempPath))
	}

	content, _, err := ReadFileWithEncoding(tempPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read temp file: %v", err))
	}

	// Determine line range
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if from == 0 {
		from = 1
	}
	if to == 0 || to > totalLines {
		to = totalLines
	}
	if from > totalLines {
		return ErrorResult(fmt.Sprintf("from (%d) exceeds total lines (%d)", from, totalLines))
	}
	if from > to {
		return ErrorResult(fmt.Sprintf("from (%d) must be <= to (%d)", from, to))
	}

	logger.DebugCF("preview", "Previewing file",
		map[string]interface{}{
			"path":        path,
			"temp_path":   tempPath,
			"from":        from,
			"to":          to,
			"total_lines": totalLines,
		})

	// Build output with line numbers
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Preview of %s (temp file):\n", path))
	if from > 1 || to < totalLines {
		result.WriteString(fmt.Sprintf("Showing lines %d-%d of %d:\n", from, to, totalLines))
	}
	result.WriteString("\n")

	for i := from - 1; i < to && i < totalLines; i++ {
		result.WriteString(fmt.Sprintf("%4d | %s\n", i+1, lines[i]))
	}

	if to < totalLines {
		result.WriteString(fmt.Sprintf("\n... %d more lines ...\n", totalLines-to))
	}

	return NewToolResult(result.String())
}
