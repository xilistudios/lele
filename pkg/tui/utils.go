package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
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
	return ""
}

func forEachGraphemeCluster(s string, fn func(cluster string, width int)) {
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cluster := g.Str()
		fn(cluster, ansi.StringWidth(cluster))
	}
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
			if i > 0 {
				if currentWidth+1+wordWidth <= limit {
					currentLine += " " + word
					currentWidth += 1 + wordWidth
					continue
				}
				wrappedLines = append(wrappedLines, currentLine)
				currentLine, currentWidth = "", 0
			}
			if wordWidth > limit {
				var chunk strings.Builder
				chunkWidth := 0
				forEachGraphemeCluster(word, func(cluster string, clusterWidth int) {
					if chunk.Len() > 0 && chunkWidth+clusterWidth > limit {
						wrappedLines = append(wrappedLines, chunk.String())
						chunk.Reset()
						chunkWidth = 0
					}
					chunk.WriteString(cluster)
					chunkWidth += clusterWidth
				})
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

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func formatToolCallArgs(tc providers.ToolCall) string {
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
		raw := tc.Function.Arguments
		if len(raw) > 200 {
			raw = truncateRunes(raw, 200) + "…"
		}
		return sanitizeDisplayText(raw)
	}
	return ""
}

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

func formatToolCallArgsCompact(tc providers.ToolCall) string {
	extract := func(args map[string]interface{}) string {
		var parts []string
		for k, v := range args {
			val := fmt.Sprintf("%v", v)
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
	if tc.Function != nil && tc.Function.Arguments != "" {
		raw := tc.Function.Arguments
		if len(raw) > 120 {
			raw = truncateRunes(raw, 120) + "…"
		}
		return sanitizeDisplayText(raw)
	}
	return ""
}

func truncateToolResult(content string, maxLen int) string {
	content = sanitizeDisplayText(content)
	if content == "" {
		return ""
	}

	if len(content) > 0 && content[0] == '{' {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(content), &parsed); err == nil {
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
	summary = strings.ReplaceAll(summary, "\n", " ")
	if len(summary) > maxLen {
		summary = truncateRunes(summary, maxLen) + "…"
	}
	return summary
}

func renderToolCallRow(name string, width int) string {
	const indent = 2
	budget := width - indent
	if budget < 1 {
		budget = 1
	}
	rows := strings.Split(wrapText(name, budget), "\n")
	var sb strings.Builder
	for i, row := range rows {
		if i == 0 {
			sb.WriteString(ToolCallLabel.Render("  ") + ToolCallName.Render(row))
		} else {
			sb.WriteString(strings.Repeat(" ", indent) + ToolCallName.Render(row))
		}
		if i < len(rows)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func renderToolResultBlock(summary string, width int) string {
	const label = "  → "
	labelWidth := ansi.StringWidth(label)
	boxOverhead := ToolResultBox.GetHorizontalFrameSize() + ToolResultBox.GetHorizontalPadding()
	budget := width - labelWidth - boxOverhead
	if budget < 1 {
		budget = 1
	}
	rows := strings.Split(ToolResultBox.Render(wrapText(summary, budget)), "\n")
	var sb strings.Builder
	for i, row := range rows {
		if i == 0 {
			sb.WriteString(ToolResultLabel.Render(label))
		} else {
			sb.WriteString(strings.Repeat(" ", labelWidth))
		}
		sb.WriteString(row)
		if i < len(rows)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
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

const (
	fnv64aOffset = 14695981039346656037
	fnv64aPrime  = 1099511628211
)

func fnv64aWriteString(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * fnv64aPrime
	}
	return h
}

func fnv64aWriteStringWord(h uint64, s string) uint64 {
	for len(s) >= 8 {
		group := uint64(s[0]) | uint64(s[1])<<8 | uint64(s[2])<<16 | uint64(s[3])<<24 |
			uint64(s[4])<<32 | uint64(s[5])<<40 | uint64(s[6])<<48 | uint64(s[7])<<56
		h = (h ^ group) * fnv64aPrime
		s = s[8:]
	}
	return fnv64aWriteString(h, s)
}

func fnv64aWriteUint64(h uint64, v uint64) uint64 {
	for shift := uint(0); shift < 64; shift += 8 {
		h = (h ^ ((v >> shift) & 0xff)) * fnv64aPrime
	}
	return h
}

func fnv64aWriteField(h uint64, s string) uint64 {
	return fnv64aWriteStringWord(fnv64aWriteUint64(h, uint64(len(s))), s)
}

const fnv64aHexDigits = "0123456789abcdef"

func fnv64aHex(sum uint64) string {
	var buf [16]byte
	for i := len(buf) - 1; i >= 0; i-- {
		buf[i] = fnv64aHexDigits[sum&0xf]
		sum >>= 4
	}
	return string(buf[:])
}

func messageFingerprint(msg providers.Message, width int) string {
	h := uint64(fnv64aOffset)
	h = fnv64aWriteField(h, msg.Role)
	h = fnv64aWriteField(h, msg.Content)
	h = fnv64aWriteField(h, msg.DisplayContent)
	h = fnv64aWriteField(h, msg.ErrorDetail)
	h = fnv64aWriteField(h, msg.ReasoningContent)
	h = fnv64aWriteUint64(h, uint64(len(msg.ToolCalls)))
	for _, tc := range msg.ToolCalls {
		h = fnv64aWriteField(h, tc.Name)
		if tc.Function == nil {
			h = fnv64aWriteUint64(h, 0)
			continue
		}
		h = fnv64aWriteUint64(h, 1)
		h = fnv64aWriteField(h, tc.Function.Name)
		h = fnv64aWriteField(h, tc.Function.Arguments)
	}
	h = fnv64aWriteUint64(h, uint64(uint32(width)))
	return fnv64aHex(h)
}
