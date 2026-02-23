package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/sipeed/picoclaw/pkg/logger"
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

	// Use atomic rename for safety
	// Windows requires special handling - rename can't overwrite
	// Use a 2-step process: temp -> backup, temp_copy -> original
	
	backupPath := resolvedPath + ".fmod.bak"
	
	// Create backup of original
	originalContent, err := os.ReadFile(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read original file: %v", err))
	}
	
	if err := os.WriteFile(backupPath, originalContent, 0644); err != nil {
		return ErrorResult(fmt.Sprintf("failed to create backup: %v", err))
	}

	// Read temp file
	tempContent, err := os.ReadFile(tempPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read temp file: %v", err))
	}

	// Write temp content to original (atomic-ish on most systems)
	if err := os.WriteFile(resolvedPath, tempContent, 0644); err != nil {
		// Restore backup on failure
		os.WriteFile(resolvedPath, originalContent, 0644)
		os.Remove(backupPath)
		return ErrorResult(fmt.Sprintf("failed to write original file: %v", err))
	}

	// Success - remove temp and backup files
	if err := os.Remove(tempPath); err != nil {
		logger.WarnCF("apply", "Failed to remove temp file",
			map[string]interface{}{"path": tempPath, "error": err.Error()})
	}
	
	if err := os.Remove(backupPath); err != nil {
		logger.WarnCF("apply", "Failed to remove backup file",
			map[string]interface{}{"path": backupPath, "error": err.Error()})
	}

	logger.InfoCF("apply", "Changes applied successfully",
		map[string]interface{}{"path": path})

	return SilentResult(fmt.Sprintf("Changes applied to %s successfully.", path))
}
