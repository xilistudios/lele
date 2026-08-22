package tools

import (
	"strings"
	"testing"
)

// TestExtractJSONFromMarkdown covers the various markdown JSON block shapes.
func TestExtractJSONFromMarkdown(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // expected regex capture; empty means no match
	}{
		{
			name: "json fenced block",
			in:   "prefix\n```json\n{\"status\": \"completed\"}\n```\nsuffix",
			want: `{"status": "completed"}`,
		},
		{
			name: "json fenced uppercase tag",
			in:   "```JSON\n{\"a\":1}\n```",
			want: `{"a":1}`,
		},
		{
			name: "bare fenced block starting with brace",
			in:   "```\n{\"status\": \"not_done\"}\n```",
			want: `{"status": "not_done"}`,
		},
		{
			name: "bare fenced block with code not json",
			in:   "```\nfmt.Println(\"hi\")\n```",
			want: "",
		},
		{
			name: "no fence",
			in:   "just text",
			want: "",
		},
		{
			name: "json fence with extra whitespace",
			in:   "```json   \n{\"s\":1}\n    ```",
			want: `{"s":1}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSONFromMarkdown(tt.in)
			if tt.want == "" && got != "" {
				t.Fatalf("expected no match, got %q", got)
			}
			if tt.want != "" && got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStripOuterCodeBlock covers the fence-stripping helper.
func TestStripOuterCodeBlock(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "text fence",
			in:   "```text\nhello world\n```",
			want: "hello world",
		},
		{
			name: "plain fence",
			in:   "```\ncontent here\n```",
			want: "content here",
		},
		{
			name: "no fence passthrough",
			in:   "plain output",
			want: "plain output",
		},
		{
			name: "triple backtick not a fence (single line)",
			in:   "```",
			want: "```",
		},
		{
			name: "open fence but no closing",
			in:   "```text\nno close",
			want: "```text\nno close",
		},
		{
			name: "multi-line content preserved",
			in:   "```\nline1\nline2\nline3\n```",
			want: "line1\nline2\nline3",
		},
		{
			name: "whitespace trim",
			in:   "   ```json\n{\"x\":1}\n```   ",
			want: `{"x":1}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripOuterCodeBlock(tt.in); got != tt.want {
				t.Errorf("stripOuterCodeBlock(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestBuildSubagentSystemPrompt covers both the base-context-empty and
// non-empty branches, including the identity and contract lines.
func TestBuildSubagentSystemPrompt(t *testing.T) {
	// Empty base context => identity only + contract.
	p := buildSubagentSystemPrompt("", "coder", "Software Coder", "/w")
	if !strings.Contains(p, "You are a focused coder subagent.") {
		t.Fatalf("missing identity line: %q", p)
	}
	if !strings.Contains(p, "## Subagent Contract") {
		t.Fatalf("missing contract: %q", p)
	}
	if strings.Contains(p, "Agent Type") {
		t.Fatalf("should not include agent type with empty base context: %q", p)
	}

	// Non-empty base context => identity + agent meta + contract.
	p2 := buildSubagentSystemPrompt("BASE", "", "", "")
	if !strings.Contains(p2, "BASE") || !strings.Contains(p2, "## Subagent Identity") {
		t.Fatalf("p2 = %q", p2)
	}
	if !strings.Contains(p2, "**Agent Type:**  ()") {
		t.Fatalf("p2 missing agent type block: %q", p2)
	}
}

// TestNormalizeSubagentStatus covers the status normalization mapping.
func TestNormalizeSubagentStatus(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"completed", SubagentStatusCompleted},
		{"complete", SubagentStatusCompleted},
		{"done", SubagentStatusCompleted},
		{"finished", SubagentStatusCompleted},
		{"success", SubagentStatusCompleted},
		{"succeeded", SubagentStatusCompleted},
		{"accomplished", SubagentStatusCompleted},
		{"TASK_COMPLETED", SubagentStatusCompleted},
		{"Task Succeeded", SubagentStatusCompleted},
		{"not_done", SubagentStatusNotDone},
		{"notdone", SubagentStatusNotDone},
		{"failed", SubagentStatusNotDone},
		{"failure", SubagentStatusNotDone},
		{"unable", SubagentStatusNotDone},
		{"blocked", SubagentStatusNotDone},
		{"incomplete", SubagentStatusNotDone},
		{"needs_context", SubagentStatusNeedsContext},
		{"needs more context", SubagentStatusNeedsContext},
		{"waiting", SubagentStatusNeedsContext},
		{"paused", SubagentStatusNeedsContext},
		{"requires_input", SubagentStatusNeedsContext},
		{"cancelled", SubagentStatusCancelled},
		{"canceled", SubagentStatusCancelled},
		{"unknown-status", SubagentStatusCompleted}, // default
		{"Needs Guidance", SubagentStatusNeedsContext},
	}
	for _, tt := range tests {
		if got := normalizeSubagentStatus(tt.in); got != tt.want {
			t.Errorf("normalizeSubagentStatus(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestIsSubagentTerminalStatus covers terminal and non-terminal statuses.
func TestIsSubagentTerminalStatus(t *testing.T) {
	terminal := map[string]bool{
		SubagentStatusCompleted:    true,
		SubagentStatusFailed:       true,
		SubagentStatusNotDone:      true,
		SubagentStatusCancelled:    true,
		SubagentStatusNeedsContext: false,
		SubagentStatusRunning:      false,
		SubagentStatusPending:      false,
	}
	for status, want := range terminal {
		if got := isSubagentTerminalStatus(status); got != want {
			t.Errorf("isSubagentTerminalStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

// TestSummarizeSubagentText covers the summary extraction helper.
func TestSummarizeSubagentText(t *testing.T) {
	if got := summarizeSubagentText("  Hello world\nsecond line"); got != "Hello world" {
		t.Fatalf("got %q", got)
	}
	if got := summarizeSubagentText("\n\n\n"); got != "" {
		t.Fatalf("empty text summary = %q", got)
	}
	if got := summarizeSubagentText("single"); got != "single" {
		t.Fatalf("got %q", got)
	}
	if got := summarizeSubagentText(""); got != "" {
		t.Fatalf("got %q", got)
	}
}

// TestParseSubagentOutcome covers the structured text, JSON, and heuristic paths.
func TestParseSubagentOutcome(t *testing.T) {
	// JSON in markdown fence.
	raw := "```json\n{\"status\":\"needs_context\",\"summary\":\"need access\",\"context_needed\":\"token\",\"details\":\"cannot proceed\"}\n```"
	out := parseSubagentOutcome(raw)
	if out.Status != SubagentStatusNeedsContext {
		t.Fatalf("json status = %q", out.Status)
	}
	if out.Summary != "need access" || out.ContextRequest != "token" {
		t.Fatalf("summary=%q ctx=%q", out.Summary, out.ContextRequest)
	}
	if out.Details != "cannot proceed" {
		t.Fatalf("details=%q", out.Details)
	}

	// JSON as raw object (no fence).
	out = parseSubagentOutcome(`{"status":"completed","summary":"s","details":"d"}`)
	if out.Status != SubagentStatusCompleted || out.Summary != "s" || out.Details != "d" {
		t.Fatalf("raw json = %+v", out)
	}

	// JSON without status field should fall through to text parsing.
	out = parseSubagentOutcome("STATUS: not_done\nSUMMARY: x\nDETAILS:\nsome detail line")
	if out.Status != SubagentStatusNotDone || out.Summary != "x" {
		t.Fatalf("text parse = %+v", out)
	}
	if !strings.Contains(out.Details, "some detail line") {
		t.Fatalf("details = %q", out.Details)
	}

	// Strip outer code block.
	out = parseSubagentOutcome("```text\nSTATUS: completed\nSUMMARY: done\n```")
	if out.Status != SubagentStatusCompleted || out.Summary != "done" {
		t.Fatalf("fenced text = %+v", out)
	}

	// Fallback heuristic: "cannot find" signal maps to needs_context (checked first).
	out = parseSubagentOutcome("I couldn't find the file anywhere, cannot find it.")
	if out.Status != SubagentStatusNeedsContext {
		t.Fatalf("heuristic needs_context = %q", out.Status)
	}

	// not_done heuristic via "could not" / "failed to".
	out = parseSubagentOutcome("The build failed to complete and could not finish.")
	if out.Status != SubagentStatusNotDone {
		t.Fatalf("heuristic not_done = %q", out.Status)
	}

	// needs_context heuristic.
	out = parseSubagentOutcome("I need more context about the API key to proceed, please provide it.")
	if out.Status != SubagentStatusNeedsContext {
		t.Fatalf("heuristic needs_context = %q", out.Status)
	}

	// Empty context request falls back to summary for needs_context (text path).
	out = parseSubagentOutcome("STATUS: needs_context\nSUMMARY: what color?\n")
	if out.Status != SubagentStatusNeedsContext || out.ContextRequest != "what color?" {
		t.Fatalf("ctx fallback = %+v", out)
	}

	// completed flags alternative-prefix lines: question, request.
	out = parseSubagentOutcome("STATUS: completed\nQUESTION: how high?")
	if out.ContextRequest != "how high?" {
		t.Fatalf("question ctx = %q", out.ContextRequest)
	}

	// Fenced JSON with empty details → fall back to raw details.
	out = parseSubagentOutcome("```json\n{\"status\":\"completed\",\"summary\":\"done\",\"details\":\"\"}\n```")
	if out.Status != SubagentStatusCompleted || out.Summary != "done" {
		t.Fatalf("fenced json empty details = %+v", out)
	}
	if !strings.Contains(out.Details, "status") {
		t.Fatalf("expected raw details fallback, got %q", out.Details)
	}

	// Fenced JSON with empty summary → summarized from details.
	out = parseSubagentOutcome("```json\n{\"status\":\"not_done\",\"details\":\"The build failed to link.\"}\n```")
	if out.Status != SubagentStatusNotDone {
		t.Fatalf("fenced json no summary status = %q", out.Status)
	}
	if out.Summary != "The build failed to link." {
		t.Fatalf("summary fallback = %q", out.Summary)
	}

	// Fenced JSON needs_context without context_needed → falls back to summary.
	out = parseSubagentOutcome("```json\n{\"status\":\"needs_context\",\"summary\":\"need API key\"}\n```")
	if out.Status != SubagentStatusNeedsContext || out.ContextRequest != "need API key" {
		t.Fatalf("ctx fallback from json = %+v", out)
	}

	// Plain ``` fence (no language tag) containing JSON starting with '{'.
	out = parseSubagentOutcome("```\n{\"status\":\"completed\",\"summary\":\"plain fence\"}\n```")
	if out.Status != SubagentStatusCompleted || out.Summary != "plain fence" {
		t.Fatalf("plain fence json = %+v", out)
	}

	// Fenced JSON with an empty status parses as JSON block extraction but
	// falls through to text heuristics (Status empty → not used). Text path
	// finds no STATUS line so default completed remains.
	out = parseSubagentOutcome("```json\n{\"summary\":\"no status\"}\n```")
	if out.Status != SubagentStatusCompleted {
		t.Fatalf("expected default completed, got %q", out.Status)
	}
	if !strings.Contains(out.Details, "no status") {
		t.Fatalf("details = %q", out.Details)
	}
}

// TestTruncateString covers truncateString branches.
func TestTruncateString(t *testing.T) {
	if got := truncateString("hello", 0); got != "" {
		t.Fatalf("maxLen 0 = %q", got)
	}
	if got := truncateString("hello", 10); got != "hello" {
		t.Fatalf("short = %q", got)
	}
	if got := truncateString("hello world", 3); got != "hel" {
		t.Fatalf("tiny maxLen = %q", got)
	}
	if got := truncateString("hello world", 8); got != "hello..." {
		t.Fatalf("truncated = %q", got)
	}
	// Unicode handling: "héllo wörld" has 12 runes; truncating to 6 runes keeps
	// runes[:3] ("hél") + "...".
	if got := truncateString("héllo wörld", 6); got != "hél..." {
		t.Fatalf("unicode = %q", got)
	}
}

// TestRetryConfigPtr verifies retryConfigPtr returns a default config pointer.
func TestRetryConfigPtr(t *testing.T) {
	c := retryConfigPtr()
	if c == nil {
		t.Fatal("nil retry config")
	}
	def := DefaultRetryConfig()
	if c.MaxRetries != def.MaxRetries {
		t.Fatalf("MaxRetries = %d, want %d", c.MaxRetries, def.MaxRetries)
	}
}
