package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// validatePath ensures the given path is within the workspace if restrict is true.
func validatePath(path, workspace string, restrict bool) (string, error) {
	if workspace == "" {
		return path, nil
	}

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace path: %w", err)
	}

	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else {
		absPath, err = filepath.Abs(filepath.Join(absWorkspace, path))
		if err != nil {
			return "", fmt.Errorf("failed to resolve file path: %w", err)
		}
	}

	if restrict {
		if !isWithinWorkspace(absPath, absWorkspace) {
			return "", fmt.Errorf("access denied: path is outside the workspace")
		}

		workspaceReal := absWorkspace
		if resolved, err := filepath.EvalSymlinks(absWorkspace); err == nil {
			workspaceReal = resolved
		}

		if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
			if !isWithinWorkspace(resolved, workspaceReal) {
				return "", fmt.Errorf("access denied: symlink resolves outside workspace")
			}
		} else if os.IsNotExist(err) {
			if parentResolved, err := resolveExistingAncestor(filepath.Dir(absPath)); err == nil {
				if !isWithinWorkspace(parentResolved, workspaceReal) {
					return "", fmt.Errorf("access denied: symlink resolves outside workspace")
				}
			} else if !os.IsNotExist(err) {
				return "", fmt.Errorf("failed to resolve path: %w", err)
			}
		} else {
			return "", fmt.Errorf("failed to resolve path: %w", err)
		}
	}

	return absPath, nil
}

func resolveExistingAncestor(path string) (string, error) {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(current) == current {
			return "", os.ErrNotExist
		}
	}
}

func isWithinWorkspace(candidate, workspace string) bool {
	rel, err := filepath.Rel(filepath.Clean(workspace), filepath.Clean(candidate))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

type ReadFileTool struct {
	workspace    string
	restrict     bool
	maxReadLines int
}

func NewReadFileTool(workspace string, restrict bool, maxReadLines int) *ReadFileTool {
	return &ReadFileTool{workspace: workspace, restrict: restrict, maxReadLines: maxReadLines}
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) Description() string {
	return fmt.Sprintf("Read the contents of a file. For large files (>500 lines), only the first %d lines are shown automatically. Use from/to parameters to read specific line ranges.", t.maxReadLines)
}

func (t *ReadFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to read",
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

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	// Check if file exists and get info
	fileInfo, err := os.Stat(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read file: %v", err))
	}

	// Get explicit line range from args
	var fromLine, toLine int
	if fromVal, ok := args["from"].(float64); ok {
		fromLine = int(fromVal)
	}
	if toVal, ok := args["to"].(float64); ok {
		toLine = int(toVal)
	}

	// If no explicit range and file is large, auto-limit
	autoLimited := false
	if fromLine == 0 && toLine == 0 {
		lineCount := estimateLineCount(resolvedPath, fileInfo.Size())
		if lineCount > t.maxReadLines {
			fromLine = 1
			toLine = t.maxReadLines
			autoLimited = true
		}
	}

	content, actualFrom, actualTo, err := readFileChunk(resolvedPath, fromLine, toLine)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read file: %v", err))
	}

	// Build result with line range indicator
	var result strings.Builder
	if autoLimited {
		result.WriteString(fmt.Sprintf("(showing lines %d-%d of %d - file is large, use from/to to read specific lines)\n\n", actualFrom, actualTo, estimateLineCount(resolvedPath, fileInfo.Size())))
	} else if fromLine > 0 || toLine > 0 {
		result.WriteString(fmt.Sprintf("(showing lines %d-%d)\n\n", actualFrom, actualTo))
	}
	result.WriteString(content)

	return NewToolResult(result.String())
}

// estimateLineCount quickly estimates the number of lines in a file
// by sampling the first part of the file
func estimateLineCount(path string, fileSize int64) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()

	// Sample first 64KB to estimate average line length
	const sampleSize = 64 * 1024
	buf := make([]byte, sampleSize)
	n, err := file.Read(buf)
	if err != nil && err.Error() != "EOF" {
		return 0
	}
	buf = buf[:n]

	// Count newlines in sample
	newlines := 0
	for _, b := range buf {
		if b == '\n' {
			newlines++
		}
	}

	if newlines == 0 {
		return 1
	}

	// Estimate total lines based on sample
	avgLineLength := float64(len(buf)) / float64(newlines)
	return int(float64(fileSize) / avgLineLength)
}

// readFileChunk reads a specific range of lines from a file
// Returns the content and the actual line range read
func readFileChunk(path string, fromLine, toLine int) (string, int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer file.Close()

	// If no range specified, read entire file
	if fromLine == 0 && toLine == 0 {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", 0, 0, err
		}
		lines := strings.Split(string(content), "\n")
		return string(content), 1, len(lines), nil
	}

	// Normalize line numbers (1-indexed)
	if fromLine < 1 {
		fromLine = 1
	}

	var result strings.Builder
	scanner := bufio.NewScanner(file)
	lineNum := 0
	actualFrom := fromLine
	actualTo := fromLine - 1

	for scanner.Scan() {
		lineNum++

		// Skip lines before fromLine
		if lineNum < fromLine {
			continue
		}

		// Stop if we've reached toLine
		if toLine > 0 && lineNum > toLine {
			break
		}

		if actualTo < lineNum {
			actualTo = lineNum
		}

		result.WriteString(scanner.Text())
		result.WriteString("\n")
	}

	if err := scanner.Err(); err != nil {
		return "", 0, 0, err
	}

	// Handle case where fromLine is beyond file length
	if lineNum < fromLine {
		actualFrom = lineNum
		actualTo = lineNum
	}

	return result.String(), actualFrom, actualTo, nil
}

type WriteFileTool struct {
	workspace string
	restrict  bool
}

func NewWriteFileTool(workspace string, restrict bool) *WriteFileTool {
	return &WriteFileTool{workspace: workspace, restrict: restrict}
}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) Description() string {
	return "Write content to a file"
}

func (t *WriteFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to write",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}

	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ErrorResult(fmt.Sprintf("failed to create directory: %v", err))
	}

	if err := os.WriteFile(resolvedPath, []byte(content), 0644); err != nil {
		return ErrorResult(fmt.Sprintf("failed to write file: %v", err))
	}

	return SilentResult(fmt.Sprintf("File written: %s", path))
}

type ListDirTool struct {
	workspace string
	restrict  bool
}

func NewListDirTool(workspace string, restrict bool) *ListDirTool {
	return &ListDirTool{workspace: workspace, restrict: restrict}
}

func (t *ListDirTool) Name() string {
	return "list_dir"
}

func (t *ListDirTool) Description() string {
	return "List files and directories in a path"
}

func (t *ListDirTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to list",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ListDirTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		path = "."
	}

	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	entries, err := os.ReadDir(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read directory: %v", err))
	}

	result := ""
	for _, entry := range entries {
		if entry.IsDir() {
			result += "DIR:  " + entry.Name() + "\n"
		} else {
			result += "FILE: " + entry.Name() + "\n"
		}
	}

	return NewToolResult(result)
}
