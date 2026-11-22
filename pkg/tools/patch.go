package tools

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/xilistudios/lele/pkg/logger"
)

type PatchTool struct {
	workspace string
	restrict  bool
}

func NewPatchTool(workspace string, restrict bool) *PatchTool {
	return &PatchTool{
		workspace: workspace,
		restrict:  restrict,
	}
}

func (t *PatchTool) Name() string {
	return "patch"
}

func (t *PatchTool) Description() string {
	return "Apply a unified diff directly to a file. Supports standard unified diff format with multiple hunks."
}

func (t *PatchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to patch",
			},
			"diff": map[string]interface{}{
				"type":        "string",
				"description": "The unified diff content. Can start with '@' to read from file (e.g., @/path/to/diff.txt)",
			},
		},
		"required": []string{"path", "diff"},
	}
}

type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []string
}

type DiffInfo struct {
	OldFile string
	NewFile string
	Hunks   []*Hunk
}

func (t *PatchTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	diffContent, ok := args["diff"].(string)
	if !ok {
		return ErrorResult("diff is required")
	}

	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
		return ErrorResult(fmt.Sprintf("file not found: %s", path))
	}

	if strings.HasPrefix(diffContent, "@") && !strings.HasPrefix(diffContent, "@@") {
		diffPath := strings.TrimPrefix(diffContent, "@")
		diffPath = strings.TrimSpace(diffPath)

		resolvedDiffPath, err := validatePath(diffPath, t.workspace, t.restrict)
		if err != nil {
			return ErrorResult(fmt.Sprintf("invalid diff file path: %v", err))
		}

		data, err := os.ReadFile(resolvedDiffPath)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to read diff file: %v", err))
		}
		diffContent = string(data)
	}

	content, encoding, err := ReadFileWithEncoding(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read file: %v", err))
	}

	diff, err := parseUnifiedDiff(diffContent)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse diff: %v", err))
	}

	logger.InfoCF("patch", "Applying patch",
		map[string]interface{}{
			"path":       path,
			"old_file":   diff.OldFile,
			"new_file":   diff.NewFile,
			"hunk_count": len(diff.Hunks),
		})

	newContent, err := applyDiff(content, diff)
	if err != nil {
		logger.ErrorCF("patch", "Failed to apply patch",
			map[string]interface{}{"error": err.Error()})
		return ErrorResult(fmt.Sprintf("failed to apply patch: %v", err))
	}

	if err := WriteFileWithEncoding(resolvedPath, newContent, encoding); err != nil {
		return ErrorResult(fmt.Sprintf("failed to write file: %v", err))
	}

	logger.InfoCF("patch", "Patch applied successfully",
		map[string]interface{}{"path": path})

	return SilentResult("Patch applied successfully.")
}

func parseUnifiedDiff(diff string) (*DiffInfo, error) {
	lines := strings.Split(diff, "\n")
	info := &DiffInfo{}

	var currentHunk *Hunk
	inHunk := false

	for i, line := range lines {
		line = strings.TrimRight(line, "\r")

		if !inHunk && line == "" {
			continue
		}

		if strings.HasPrefix(line, "--- ") {
			info.OldFile = strings.TrimPrefix(line, "--- ")
			continue
		}
		if strings.HasPrefix(line, "+++ ") {
			info.NewFile = strings.TrimPrefix(line, "+++ ")
			continue
		}

		if strings.HasPrefix(line, "@@") {
			inHunk = true
			if currentHunk != nil {
				info.Hunks = append(info.Hunks, currentHunk)
			}

			re := regexp.MustCompile(`@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
			matches := re.FindStringSubmatch(line)
			if matches == nil {
				return nil, fmt.Errorf("invalid hunk header at line %d: %s", i+1, line)
			}

			oldStart, _ := strconv.Atoi(matches[1])
			oldCount := 1
			if matches[2] != "" {
				oldCount, _ = strconv.Atoi(matches[2])
			}

			newStart, _ := strconv.Atoi(matches[3])
			newCount := 1
			if matches[4] != "" {
				newCount, _ = strconv.Atoi(matches[4])
			}

			currentHunk = &Hunk{
				OldStart: oldStart,
				OldCount: oldCount,
				NewStart: newStart,
				NewCount: newCount,
				Lines:    []string{},
			}
			continue
		}

		if inHunk && currentHunk != nil {
			if len(line) > 0 && (line[0] == '+' || line[0] == '-' || line[0] == ' ') {
				currentHunk.Lines = append(currentHunk.Lines, line)
			} else if line == "" {
				currentHunk.Lines = append(currentHunk.Lines, " "+line)
			} else if strings.HasPrefix(line, "\\") {
				continue
			}
		}
	}

	if currentHunk != nil {
		info.Hunks = append(info.Hunks, currentHunk)
	}

	if len(info.Hunks) == 0 {
		return nil, fmt.Errorf("no hunks found in diff")
	}

	return info, nil
}

func applyDiff(content string, diff *DiffInfo) (string, error) {
	lines := strings.Split(content, "\n")

	for _, hunk := range diff.Hunks {
		hunk.OldStart--
	}

	for i := len(diff.Hunks) - 1; i >= 0; i-- {
		hunk := diff.Hunks[i]

		if err := applyHunk(lines, hunk); err != nil {
			return "", fmt.Errorf("hunk at line %d: %v", hunk.OldStart+1, err)
		}

		lines = rebuildLines(lines, hunk)
	}

	return strings.Join(lines, "\n"), nil
}

func applyHunk(lines []string, hunk *Hunk) error {
	start := hunk.OldStart
	if start < 0 || start > len(lines) {
		return fmt.Errorf("hunk starts outside file (line %d, file has %d lines)", start+1, len(lines))
	}

	contextCount := 0
	removalCount := 0

	for _, line := range hunk.Lines {
		switch line[0] {
		case ' ', '-':
			if line[0] == ' ' {
				contextCount++
			} else {
				removalCount++
			}

			expectedLine := line[1:]
			lineIdx := start + contextCount + removalCount - 1

			if lineIdx >= len(lines) {
				return fmt.Errorf("expected line %d but file has only %d lines", lineIdx+1, len(lines))
			}

			if lines[lineIdx] != expectedLine {
				return fmt.Errorf("context mismatch at line %d:\n  expected: %s\n  actual:   %s",
					lineIdx+1, expectedLine, lines[lineIdx])
			}
		}
	}

	return nil
}

func rebuildLines(lines []string, hunk *Hunk) []string {
	start := hunk.OldStart

	var newLines []string
	newLines = append(newLines, lines[:start]...)

	contextSeen := 0
	removalSeen := 0

	for _, line := range hunk.Lines {
		switch line[0] {
		case ' ':
			newLines = append(newLines, line[1:])
			contextSeen++
		case '-':
			removalSeen++
		case '+':
			newLines = append(newLines, line[1:])
		}
	}

	afterIdx := start + contextSeen + removalSeen
	if afterIdx < len(lines) {
		newLines = append(newLines, lines[afterIdx:]...)
	}

	return newLines
}
