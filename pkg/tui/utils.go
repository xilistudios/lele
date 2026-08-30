package tui

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/channels"
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
		if ansi.StringWidth(line) <= limit {
			wrappedLines = append(wrappedLines, line)
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			wrappedLines = append(wrappedLines, "")
			continue
		}
		currentLine, currentWidth := "", 0
		for i, word := range words {
			wordWidth := ansi.StringWidth(word)
			// The first word starts the line with no separator; subsequent
			// words join via a space when they fit.
			if i > 0 {
				if currentWidth+1+wordWidth <= limit {
					currentLine += " " + word
					currentWidth += 1 + wordWidth
					continue
				}
				// Flush the current line before handling this word.
				wrappedLines = append(wrappedLines, currentLine)
				currentLine, currentWidth = "", 0
			}
			if wordWidth > limit {
				// Hard-break words wider than the limit (long URLs, tokens
				// without spaces). Accumulate visual width rune by rune so
				// wide runes (e.g. CJK, width 2) are never split mid-rune.
				// The trailing remainder stays as the current line so short
				// words that follow can still join it if they fit.
				var chunk strings.Builder
				chunkWidth := 0
				for _, r := range word {
					rw := ansi.StringWidth(string(r))
					if chunk.Len() > 0 && chunkWidth+rw > limit {
						wrappedLines = append(wrappedLines, chunk.String())
						chunk.Reset()
						chunkWidth = 0
					}
					chunk.WriteRune(r)
					chunkWidth += rw
				}
				currentLine = chunk.String()
				currentWidth = chunkWidth
			} else {
				currentLine = word
				currentWidth = wordWidth
			}
		}
		wrappedLines = append(wrappedLines, currentLine)
	}

	return strings.Join(wrappedLines, "\n")
}

// truncateRunes truncates s to at most n runes without splitting multi-byte
// UTF-8 characters. It never adds an ellipsis; callers append their own.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func formatToolCallArgs(tc providers.ToolCall) string {
	// Try structured arguments first
	if tc.Arguments != nil {
		var parts []string
		for k, v := range tc.Arguments {
			val := fmt.Sprintf("%v", v)
			if len(val) > 120 {
				val = truncateRunes(val, 120) + "…"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", k, val))
		}
		sort.Strings(parts)
		return sanitizeDisplayText(strings.Join(parts, "  "))
	}
	// Try function.arguments (JSON string)
	if tc.Function != nil && tc.Function.Arguments != "" {
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err == nil {
			var parts []string
			for k, v := range args {
				val := fmt.Sprintf("%v", v)
				if len(val) > 120 {
					val = truncateRunes(val, 120) + "…"
				}
				parts = append(parts, fmt.Sprintf("%s: %s", k, val))
			}
			sort.Strings(parts)
			return sanitizeDisplayText(strings.Join(parts, "  "))
		}
		// Fallback: show raw JSON
		raw := tc.Function.Arguments
		if len(raw) > 200 {
			raw = truncateRunes(raw, 200) + "…"
		}
		return sanitizeDisplayText(raw)
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
				val = truncateRunes(val, 80) + "…"
			}
			parts = append(parts, fmt.Sprintf("%s=%s", k, val))
		}
		sort.Strings(parts)
		return sanitizeDisplayText(strings.Join(parts, ", "))
	}

	args := extractToolCallArgs(tc)
	if args != nil {
		return extract(args)
	}
	// Fallback: try to extract raw string from Function.Arguments
	if tc.Function != nil && tc.Function.Arguments != "" {
		raw := tc.Function.Arguments
		if len(raw) > 120 {
			raw = truncateRunes(raw, 120) + "…"
		}
		return sanitizeDisplayText(raw)
	}
	return ""
}

// truncateToolResult returns a collapsed single-line summary of a tool result.
func truncateToolResult(content string, maxLen int) string {
	content = sanitizeDisplayText(content)
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
		summary = truncateRunes(summary, maxLen) + "…"
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

// sortSubagents sorts subagent tasks in a deterministic way:
// 1. If both task IDs have the format "subagent-<number>", we sort by the number descending (most recent first).
// 2. Otherwise, we fall back to Created timestamp descending.
// 3. If Created timestamps are equal, we sort by TaskID descending.
func sortSubagents(subagents []channels.SubagentTaskInfo) {
	getSubagentNumber := func(taskID string) int {
		if strings.HasPrefix(taskID, "subagent-") {
			numStr := taskID[len("subagent-"):]
			if val, err := strconv.Atoi(numStr); err == nil {
				return val
			}
		}
		return -1
	}

	sort.Slice(subagents, func(i, j int) bool {
		numI := getSubagentNumber(subagents[i].TaskID)
		numJ := getSubagentNumber(subagents[j].TaskID)
		if numI != -1 && numJ != -1 {
			return numI > numJ
		}
		if subagents[i].Created != subagents[j].Created {
			return subagents[i].Created > subagents[j].Created
		}
		return subagents[i].TaskID > subagents[j].TaskID
	})
}

// messageFingerprint returns a fast FNV-64a hash of the message content for
// per-message render caching. The hash covers role, content, reasoning content,
// tool calls, and the target render width. FNV-1a is designed for hash tables
// and processes ~1 GB/s, so even 100K-char messages hash in microseconds.
func messageFingerprint(msg providers.Message, width int) string {
	h := fnv.New64a()
	h.Write([]byte(msg.Role))
	h.Write([]byte(msg.Content))
	h.Write([]byte(msg.ReasoningContent))
	for _, tc := range msg.ToolCalls {
		h.Write([]byte(tc.Name))
		if tc.Function != nil {
			h.Write([]byte(tc.Function.Name))
			h.Write([]byte(tc.Function.Arguments))
		}
	}
	_ = binary.Write(h, binary.LittleEndian, uint32(width))
	return fmt.Sprintf("%016x", h.Sum64())
}
