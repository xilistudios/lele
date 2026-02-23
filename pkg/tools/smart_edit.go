package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// SmartEditTool edits a file using intelligent fallback strategies.
// It writes to a temp file (.fmod.tmp) rather than modifying the original directly.
type SmartEditTool struct {
	workspace string
	restrict  bool
}

// NewSmartEditTool creates a new SmartEditTool
func NewSmartEditTool(workspace string, restrict bool) *SmartEditTool {
	return &SmartEditTool{
		workspace: workspace,
		restrict:  restrict,
	}
}

func (t *SmartEditTool) Name() string {
	return "smart_edit"
}

func (t *SmartEditTool) Description() string {
	return "Edit a file with intelligent fallback strategies (exact, whitespace-tolerant, regex). Writes to temp file first, then apply changes."
}

func (t *SmartEditTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to edit",
			},
			"old_text": map[string]interface{}{
				"type":        "string",
				"description": "The text to find and replace",
			},
			"new_text": map[string]interface{}{
				"type":        "string",
				"description": "The text to replace with",
			},
			"regex": map[string]interface{}{
				"type":        "boolean",
				"description": "Use regex matching (default: false)",
			},
			"flags": map[string]interface{}{
				"type":        "string",
				"description": "Regex flags: g (global), i (case-insensitive). E.g., 'gi'",
			},
		},
		"required": []string{"path", "old_text", "new_text"},
	}
}

func (t *SmartEditTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	oldText, ok := args["old_text"].(string)
	if !ok {
		return ErrorResult("old_text is required")
	}

	newText, ok := args["new_text"].(string)
	if !ok {
		return ErrorResult("new_text is required")
	}

	useRegex := false
	if v, ok := args["regex"].(bool); ok {
		useRegex = v
	}

	flags := ""
	if v, ok := args["flags"].(string); ok {
		flags = v
	}

	// Validate path
	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		logger.ErrorCF("smart_edit", "Path validation failed",
			map[string]interface{}{"path": path, "error": err.Error()})
		return ErrorResult(err.Error())
	}

	// Check if file exists
	if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
		return ErrorResult(fmt.Sprintf("file not found: %s", path))
	}

	// Read file with encoding detection
	content, encoding, err := ReadFileWithEncoding(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read file: %v", err))
	}

	logger.InfoCF("smart_edit", "Editing file",
		map[string]interface{}{
			"path":   path,
			"regex":  useRegex,
			"flags":  flags,
			"encode": encoding,
		})

	var matches []Match
	var strategyName string

	if useRegex {
		// Use regex strategy
		strategy := &RegexMatchStrategy{Flags: flags}
		matches = strategy.FindMatches(content, oldText)
		strategyName = "regex"
		
		// If no match, fail immediately (no fallback for regex)
		if len(matches) == 0 {
			logger.ErrorCF("smart_edit", "Regex match failed",
				map[string]interface{}{"pattern": oldText, "flags": flags})
			return ErrorResult(fmt.Sprintf("regex pattern not found: %s", oldText))
		}
	} else {
		// Try exact match first
		exactStrategy := &ExactMatchStrategy{}
		matches = exactStrategy.FindMatches(content, oldText)
		strategyName = "exact"
		
		if len(matches) == 0 {
			// Fallback to whitespace-tolerant
			wsStrategy := &WhitespaceTolerantStrategy{}
			matches = wsStrategy.FindMatches(content, oldText)
			strategyName = "whitespace-tolerant"
			
			if len(matches) == 0 {
				logger.ErrorCF("smart_edit", "Match failed",
					map[string]interface{}{"strategy": "all", "old_text": oldText})
				return ErrorResult(fmt.Sprintf("old_text not found in file (tried exact and whitespace-tolerant matching)"))
			}
		}
	}

	// Check for multiple matches
	if len(matches) > 1 {
		logger.ErrorCF("smart_edit", "Multiple matches found",
			map[string]interface{}{
				"count":    len(matches),
				"strategy": strategyName,
			})
		return ErrorResult(fmt.Sprintf("old_text appears %d times. Please provide more context to make it unique", len(matches)))
	}

	// Apply replacement
	match := matches[0]
	newContent := ReplaceRange(content, match, newText)

	// Write to temp file
	tempPath := GetTempPath(resolvedPath)
	if err := WriteFileWithEncoding(tempPath, newContent, encoding); err != nil {
		logger.ErrorCF("smart_edit", "Failed to write temp file",
			map[string]interface{}{"path": tempPath, "error": err.Error()})
		return ErrorResult(fmt.Sprintf("failed to write temp file: %v", err))
	}

	logger.InfoCF("smart_edit", "Edit successful",
		map[string]interface{}{
			"path":           path,
			"strategy":       strategyName,
			"temp_file":      tempPath,
			"original_match": match,
		})

	return SilentResult(fmt.Sprintf("Edit successful using %s strategy. Use 'preview' to review changes, 'apply' to commit.", strategyName))
}
