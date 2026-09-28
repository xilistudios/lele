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
	// Not a git repo (or git unavailable) — empty hides the branch line.
	return ""
}

// forEachGraphemeCluster calls fn once per grapheme cluster in s, passing the
// cluster and its terminal display width. Clusters are never split, so the
// cluster widths of plain text sum to ansi.StringWidth(s): multi-codepoint
// emoji (VS16 like "❤️", ZWJ sequences like "👨‍💻", flags like "🇪🇸") are
// measured as one unit instead of per code point, where per-rune widths
// disagree with the terminal's rendering.
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
				// without spaces). Accumulate display width per grapheme
				// cluster so wide runes (e.g. CJK, width 2) and multi-codepoint
				// emoji (VS16/ZWJ/flags) are never split mid-cluster.
				// The trailing remainder stays as the current line so short
				// words that follow can still join it if they fit.
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

// renderToolCallRow renders one compact tool-call line ("  " + name) wrapped
// to fit within width display cells. Continuation rows are indented to the
// name column so the block stays aligned. Wrapping happens BEFORE styling:
// lineViewport renders its lines through a lipgloss Width() pass that
// word-wraps any over-wide styled row mid-content (reflow breaks at hyphens),
// which would drop the label indent and shift every row below.
func renderToolCallRow(name string, width int) string {
	const indent = 2 // display cells of ToolCallLabel "  "
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

// renderToolResultBlock renders the tool-result summary as a left-bordered box
// preceded by the "  → " label, wrapped so every row fits within width display
// cells and the box border sits on the same column on every row: row 0 carries
// the label, continuation rows are padded with spaces of the same label width.
// Content wraps at spaces BEFORE styling for the same reason as
// renderToolCallRow: an over-wide composed row would be re-wrapped inside
// lineViewport's lipgloss Width() pass, mid-word at hyphen breakpoints,
// dropping both the "  → " prefix and the box border on the continuation.
func renderToolResultBlock(summary string, width int) string {
	const label = "  → "
	labelWidth := ansi.StringWidth(label) // 4
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

// FNV-64a constants (see https://en.wikipedia.org/wiki/Fowler%E2%80%93Noll%E2%80%93Vo_hash_function).
const (
	fnv64aOffset = 14695981039346656037
	fnv64aPrime  = 1099511628211
)

// fnv64aWriteString mixes the bytes of s into the FNV-64a state h by indexing
// the string directly. Converting to []byte (or going through
// io.WriteString/hash.Hash) would copy the whole field on every call — the
// fingerprint runs once per visible message on every viewport rebuild, and tool
// results can be hundreds of kilobytes — for no benefit, since the FNV loop is
// byte-oriented anyway.
//
// The state is passed and returned by value on purpose: threading it through a
// *uint64 made every iteration go through a store-to-load round trip and halved
// the hashing throughput.
//
// This is the byte-at-a-time primitive. It now only sees short inputs: 8-byte
// length prefixes/tags (fnv64aWriteUint64) and the trailing bytes of a field
// (the tail of fnv64aWriteStringWord).
func fnv64aWriteString(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * fnv64aPrime
	}
	return h
}

// fnv64aWriteStringWord mixes the bytes of s into h eight at a time, reading
// each group as a little-endian uint64: the eight index expressions below
// compile to one 64-bit load, so multi-KB tool results hash at one multiply per
// 8 bytes instead of one per byte (~7x, measured). That latency is paid once
// per visible message on every viewport rebuild, which is exactly the
// long-chat rebuild cost this cache key exists to keep low.
//
// It is a word-at-a-time variant of FNV-1a (one group mixes as
// h = (h ^ group) * prime), not the reference byte stream. Only unambiguity
// matters here, because the fingerprint is a process-local render-cache key: it
// is never compared against a fingerprint computed by another build or process,
// so the concrete mixing order is an implementation detail (see
// messageFingerprint for what the contract actually is).
//
// Alignment is fully deterministic and never straddles a field boundary: each
// field is mixed as its own 8-byte length prefix and then its own bytes, and
// the split into groups depends only on len(s) — pinned by that prefix. So a
// field is cut as floor(len/8) little-endian groups followed by
// len%8 trailing bytes mixed one by one; a tail is never folded into a group of
// the next field.
func fnv64aWriteStringWord(h uint64, s string) uint64 {
	for len(s) >= 8 {
		group := uint64(s[0]) | uint64(s[1])<<8 | uint64(s[2])<<16 | uint64(s[3])<<24 |
			uint64(s[4])<<32 | uint64(s[5])<<40 | uint64(s[6])<<48 | uint64(s[7])<<56
		h = (h ^ group) * fnv64aPrime
		s = s[8:]
	}
	return fnv64aWriteString(h, s)
}

// fnv64aWriteUint64 mixes v into the FNV-64a state, least significant byte
// first, without any intermediate buffer.
func fnv64aWriteUint64(h uint64, v uint64) uint64 {
	for shift := uint(0); shift < 64; shift += 8 {
		h = (h ^ ((v >> shift) & 0xff)) * fnv64aPrime
	}
	return h
}

// fnv64aWriteField mixes a length prefix followed by the bytes of s. The length
// prefix is what keeps different field partitions from colliding: Content "ab"
// + ReasoningContent "c" mixes as len=2,"ab",len=1,"c" while Content "a" +
// ReasoningContent "bc" mixes as len=1,"a",len=2,"bc". Concatenating the raw
// bytes (as the original implementation did) made those two indistinguishable.
func fnv64aWriteField(h uint64, s string) uint64 {
	return fnv64aWriteStringWord(fnv64aWriteUint64(h, uint64(len(s))), s)
}

const fnv64aHexDigits = "0123456789abcdef"

// fnv64aHex formats sum as 16 lower-case hex digits (equivalent to
// fmt.Sprintf("%016x", sum)) into a stack buffer.
func fnv64aHex(sum uint64) string {
	var buf [16]byte
	for i := len(buf) - 1; i >= 0; i-- {
		buf[i] = fnv64aHexDigits[sum&0xf]
		sum >>= 4
	}
	return string(buf[:])
}

// messageFingerprint returns a fast hash of the message content for per-message
// render caching. The hash covers role, content, reasoning content, tool calls
// (name, a per-record payload tag, function name and arguments), and the target
// render width.
//
// Contract: within one process, equal messages produce equal fingerprints and
// different messages produce different ones with overwhelming probability
// (64-bit hash). Nothing outside the process depends on the value, so the
// mixing function may change freely; it is a word-at-a-time FNV-1a variant
// (~5 GB/s, see fnv64aWriteStringWord) instead of the reference byte stream.
//
// What must not change is unambiguity, and it does not: every field is mixed as
// a fixed 8-byte little-endian length prefix followed by its own bytes
// (fnv64aWriteField), each tool call mixes a fixed 8-byte payload tag, and the
// tool-call count and width are mixed as fixed 8-byte values. Field bytes are
// therefore never adjacency-ambiguous across a partition ("ab"+"c" vs
// "a"+"bc") nor across a group boundary of the word-at-a-time mixer.
//
// It copies nothing: strings are consumed in place (no string → []byte
// conversion anywhere) and each field's groups are read straight out of the
// string header. The only allocation left is the 16-byte hex key it returns
// (~200 of them per viewport rebuild, instead of one []byte copy per field).
func messageFingerprint(msg providers.Message, width int) string {
	h := uint64(fnv64aOffset)
	h = fnv64aWriteField(h, msg.Role)
	h = fnv64aWriteField(h, msg.Content)
	h = fnv64aWriteField(h, msg.ReasoningContent)
	h = fnv64aWriteUint64(h, uint64(len(msg.ToolCalls)))
	for _, tc := range msg.ToolCalls {
		h = fnv64aWriteField(h, tc.Name)
		// Per-record tag: every tool call mixes a fixed 8 bytes, so a call
		// without a function payload cannot be confused with one whose payload
		// is empty when the surrounding fields shift between tool calls of the
		// same list (see TestMessageFingerprint_NoPartitionCollision).
		if tc.Function == nil {
			h = fnv64aWriteUint64(h, 0)
			continue
		}
		h = fnv64aWriteUint64(h, 1)
		h = fnv64aWriteField(h, tc.Function.Name)
		h = fnv64aWriteField(h, tc.Function.Arguments)
	}
	// The render width participates in the cache key (32 bits, as before).
	h = fnv64aWriteUint64(h, uint64(uint32(width)))
	return fnv64aHex(h)
}
