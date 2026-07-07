package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/providers"
)

func getGitBranch(dir string) string {
	headPath := filepath.Join(dir, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err == nil {
		content := strings.TrimSpace(string(data))
		if strings.HasPrefix(content, "ref: refs/heads/") {
			return strings.TrimPrefix(content, "ref: refs/heads/")
		}
	}
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return "main"
}

func wrapText(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	var wrappedLines []string

	for _, line := range lines {
		if len(line) <= limit {
			wrappedLines = append(wrappedLines, line)
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			wrappedLines = append(wrappedLines, "")
			continue
		}
		currentLine := words[0]
		for _, word := range words[1:] {
			if len(currentLine)+1+len(word) <= limit {
				currentLine += " " + word
			} else {
				wrappedLines = append(wrappedLines, currentLine)
				currentLine = word
			}
		}
		wrappedLines = append(wrappedLines, currentLine)
	}

	return strings.Join(wrappedLines, "\n")
}

func formatToolCallArgs(tc providers.ToolCall) string {
	// Try structured arguments first
	if tc.Arguments != nil {
		var parts []string
		for k, v := range tc.Arguments {
			val := fmt.Sprintf("%v", v)
			if len(val) > 120 {
				val = val[:120] + "…"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", k, val))
		}
		sort.Strings(parts)
		return strings.Join(parts, "  ")
	}
	// Try function.arguments (JSON string)
	if tc.Function != nil && tc.Function.Arguments != "" {
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err == nil {
			var parts []string
			for k, v := range args {
				val := fmt.Sprintf("%v", v)
				if len(val) > 120 {
					val = val[:120] + "…"
				}
				parts = append(parts, fmt.Sprintf("%s: %s", k, val))
			}
			sort.Strings(parts)
			return strings.Join(parts, "  ")
		}
		// Fallback: show raw JSON
		raw := tc.Function.Arguments
		if len(raw) > 200 {
			raw = raw[:200] + "…"
		}
		return raw
	}
	return ""
}

// extractToolCallArgs extracts arguments from a ToolCall, handling different formats.
func extractToolCallArgs(tc providers.ToolCall) map[string]interface{} {
	if tc.Arguments != nil {
		return tc.Arguments
	}
	if tc.Function != nil && tc.Function.Arguments != "" {
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err == nil {
			return args
		}
	}
	return nil
}

// formatToolCallArgsCompact returns a single-line compact representation of
// tool call arguments: key=val pairs joined by commas, values truncated to 80 chars.
func formatToolCallArgsCompact(tc providers.ToolCall) string {
	extract := func(args map[string]interface{}) string {
		var parts []string
		for k, v := range args {
			val := fmt.Sprintf("%v", v)
			// Flatten newlines for compact display
			val = strings.ReplaceAll(val, "\n", " ")
			if len(val) > 80 {
				val = val[:80] + "…"
			}
			parts = append(parts, fmt.Sprintf("%s=%s", k, val))
		}
		sort.Strings(parts)
		return strings.Join(parts, ", ")
	}

	args := extractToolCallArgs(tc)
	if args != nil {
		return extract(args)
	}
	// Fallback: try to extract raw string from Function.Arguments
	if tc.Function != nil && tc.Function.Arguments != "" {
		raw := tc.Function.Arguments
		if len(raw) > 120 {
			raw = raw[:120] + "…"
		}
		return raw
	}
	return ""
}

// truncateToolResult returns a collapsed single-line summary of a tool result.
func truncateToolResult(content string, maxLen int) string {
	if content == "" {
		return ""
	}

	// Try to extract meaningful content from JSON if present
	if len(content) > 0 && content[0] == '{' {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(content), &parsed); err == nil {
			// Try common fields that contain the main result
			for _, key := range []string{"output", "result", "error", "message"} {
				if val, ok := parsed[key]; ok {
					if str, ok := val.(string); ok && str != "" {
						content = str
						break
					}
				}
			}
		}
	}

	// Try to extract first meaningful line
	lines := strings.Split(content, "\n")
	summary := ""
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" && l != "{" && l != "}" {
			summary = l
			break
		}
	}
	if summary == "" {
		summary = content
	}
	// Flatten and truncate
	summary = strings.ReplaceAll(summary, "\n", " ")
	if len(summary) > maxLen {
		summary = summary[:maxLen] + "…"
	}
	return summary
}

func formatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var res []string
	for len(s) > 3 {
		res = append([]string{s[len(s)-3:]}, res...)
		s = s[:len(s)-3]
	}
	res = append([]string{s}, res...)
	return strings.Join(res, ",")
}

func formatTokenK(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	k := float64(n) / 1000.0
	if k >= 100 {
		return fmt.Sprintf("%.0fK", k)
	}
	return fmt.Sprintf("%.1fK", k)
}
