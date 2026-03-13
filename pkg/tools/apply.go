package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/xilistudios/lele/pkg/logger"
)

// ApplyTool applies changes from a temp file (.fmod.tmp) to the original file.
// This commits the changes made by smart_edit.
type ApplyTool struct {
	workspace string
	restrict  bool
}

// NewApplyTool creates a new ApplyTool
func NewApplyTool(workspace string, restrict bool) *ApplyTool {
	return &ApplyTool{
		workspace: workspace,
		restrict:  restrict,
	}
}

func (t *ApplyTool) Name() string {
	return "apply"
}

func (t *ApplyTool) Description() string {
	return "Apply changes from the temp file (.fmod.tmp) to the original file. This commits the edits made by smart_edit."
}

func (t *ApplyTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the original file to apply changes to",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ApplyTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	// Validate path
	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	tempPath := GetTempPath(resolvedPath)

	// Check if temp file exists
	if _, err := os.Stat(tempPath); os.IsNotExist(err) {
		return ErrorResult(fmt.Sprintf("temp file not found: %s. Use 'smart_edit' first.", tempPath))
	}

	// Check if original file exists
	if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
		return ErrorResult(fmt.Sprintf("original file not found: %s", path))
	}

	logger.InfoCF("apply", "Applying changes",
		map[string]interface{}{
			"original": path,
			"temp":     tempPath,
		})

	// Get original file permissions to preserve them
	originalInfo, err := os.Stat(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to stat original file: %v", err))
	}
	originalMode := originalInfo.Mode()

	// Read temp file content
	tempContent, err := os.ReadFile(tempPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read temp file: %v", err))
	}

	// Atomic write: write to temp file first, then rename
	// This ensures the original file is never in an inconsistent state
	tempFinalPath := resolvedPath + ".fmod.tmp.final"

	// Write to temporary final file with original permissions
	if err := os.WriteFile(tempFinalPath, tempContent, originalMode); err != nil {
		return ErrorResult(fmt.Sprintf("failed to write temp file: %v", err))
	}

	// Atomic rename: temp -> original
	// This is atomic on POSIX systems and most Windows scenarios
	if err := os.Rename(tempFinalPath, resolvedPath); err != nil {
		// Clean up temp file on failure
		os.Remove(tempFinalPath)
		return ErrorResult(fmt.Sprintf("failed to rename temp file to original: %v", err))
	}

	// Success - remove old temp file
	if err := os.Remove(tempPath); err != nil {
		logger.WarnCF("apply", "Failed to remove temp file",
			map[string]interface{}{"path": tempPath, "error": err.Error()})
	}

	logger.InfoCF("apply", "Changes applied successfully",
		map[string]interface{}{"path": path})

	return SilentResult(fmt.Sprintf("Changes applied to %s successfully.", path))
}
