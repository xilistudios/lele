package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// SequentialReplaceTool performs multiple replacements in a single operation.
// It detects overlaps and applies replacements from end to start to maintain indices.
type SequentialReplaceTool struct {
	workspace string
	restrict  bool
}

// NewSequentialReplaceTool creates a new SequentialReplaceTool
func NewSequentialReplaceTool(workspace string, restrict bool) *SequentialReplaceTool {
	return &SequentialReplaceTool{
		workspace: workspace,
		restrict:  restrict,
	}
}

func (t *SequentialReplaceTool) Name() string {
	return "sequential_replace"
}

func (t *SequentialReplaceTool) Description() string {
	return "Perform multiple replacements in a single operation. Detects overlaps/conflicts. Use when you need to replace multiple distinct strings in a file. Writes to temp file first."
}

func (t *SequentialReplaceTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to edit",
			},
			"pairs": map[string]interface{}{
				"type":        "string",
				"description": "JSON array of replacement pairs [{\"old\": \"text to find\", \"new\": \"replacement\"}, ...]",
			},
			"regex": map[string]interface{}{
				"type":        "boolean",
				"description": "Use regex matching for all pairs (default: false)",
			},
			"flags": map[string]interface{}{
				"type":        "string",
				"description": "Regex flags: g (global), i (case-insensitive). E.g., 'gi'",
			},
		},
		"required": []string{"path", "pairs"},
	}
}

func (t *SequentialReplaceTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	pairsJSON, ok := args["pairs"].(string)
	if !ok {
		return ErrorResult("pairs is required")
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
		return ErrorResult(err.Error())
	}

	// Check if file exists
	if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
		return ErrorResult(fmt.Sprintf("file not found: %s", path))
	}

	// Parse pairs
	var pairs []ReplacementPair
	if err := json.Unmarshal([]byte(pairsJSON), &pairs); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse pairs JSON: %v", err))
	}

	if len(pairs) == 0 {
		return ErrorResult("no replacement pairs provided")
	}

	// Validate pairs
	for i, pair := range pairs {
		if pair.Old == "" {
			return ErrorResult(fmt.Sprintf("pair %d: old is empty", i))
		}
	}

	// Read file with encoding detection
	content, encoding, err := ReadFileWithEncoding(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read file: %v", err))
	}

	logger.InfoCF("sequential_replace", "Starting replacements",
		map[string]interface{}{
			"path":         path,
			"pair_count":   len(pairs),
			"regex":        useRegex,
			"flags":        flags,
		})

	// Create matching strategy
	var strategy MatchStrategy
	if useRegex {
		strategy = &RegexMatchStrategy{Flags: flags}
	} else {
		strategy = &ExactMatchStrategy{}
	}

	// Apply replacements with overlap detection
	newContent, err := ApplyReplacements(content, pairs, strategy)
	if err != nil {
		logger.ErrorCF("sequential_replace", "Replacement failed",
			map[string]interface{}{"error": err.Error()})
		return ErrorResult(err.Error())
	}

	// Write to temp file
	tempPath := GetTempPath(resolvedPath)
	if err := WriteFileWithEncoding(tempPath, newContent, encoding); err != nil {
		return ErrorResult(fmt.Sprintf("failed to write temp file: %v", err))
	}

	logger.InfoCF("sequential_replace", "Replacements successful",
		map[string]interface{}{
			"path":      path,
			"temp_file": tempPath,
			"pairs":     len(pairs),
		})

	strategyName := "exact"
	if useRegex {
		strategyName = "regex"
	}

	return SilentResult(fmt.Sprintf("Applied %d %s replacement(s) successfully. Use 'preview' to review, 'apply' to commit.",
		len(pairs), strategyName))
}
