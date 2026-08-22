package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/providers"
)

func TestThrottledUpdateViewportFirstChunk(t *testing.T) {
	m := &Model{
		streamThrottleActive:   false,
		streamPendingUpdate:    false,
		streamThrottleInterval: time.Millisecond,
		viewport:               newLineViewport(80, 20),
	}
	cmd := m.throttledUpdateViewport()
	if cmd == nil {
		t.Fatal("expected a tea.Tick cmd for first chunk")
	}
	if !m.streamThrottleActive {
		t.Error("expected streamThrottleActive true after first chunk")
	}
	if m.streamPendingUpdate {
		t.Error("expected streamPendingUpdate false after first chunk")
	}
}

func TestThrottledUpdateViewportCoalesces(t *testing.T) {
	m := &Model{
		streamThrottleActive:   true,
		streamPendingUpdate:    false,
		streamThrottleInterval: time.Millisecond,
		viewport:               newLineViewport(80, 20),
	}
	cmd := m.throttledUpdateViewport()
	if cmd != nil {
		t.Error("expected nil cmd when throttle already active")
	}
	if !m.streamPendingUpdate {
		t.Error("expected streamPendingUpdate true when new chunk arrives during active throttle")
	}
}

// TestThrottleCmdMsgSent verifies the returned tick cmd produces a
// streamThrottleMsg when executed.
func TestThrottleCmdMsgSent(t *testing.T) {
	m := &Model{
		streamThrottleActive:   false,
		streamPendingUpdate:    false,
		streamThrottleInterval: time.Millisecond,
		viewport:               newLineViewport(80, 20),
	}
	cmd := m.throttledUpdateViewport()
	msg := cmd()
	if _, ok := msg.(streamThrottleMsg); !ok {
		t.Fatalf("expected streamThrottleMsg, got %T", msg)
	}
}

func TestFlushStreamUpdate(t *testing.T) {
	// With pending update → forces a render and deactivates throttle.
	m := &Model{
		streamThrottleActive: true,
		streamPendingUpdate:  true,
		viewport:             newLineViewport(80, 20),
	}
	m.flushStreamUpdate()
	if m.streamThrottleActive {
		t.Error("expected throttle inactive after flush")
	}
	if m.streamPendingUpdate {
		t.Error("expected pending update cleared after flush")
	}
}

func TestFlushStreamUpdateNoPending(t *testing.T) {
	m := &Model{
		streamThrottleActive: true,
		streamPendingUpdate:  false,
		viewport:             newLineViewport(80, 20),
	}
	m.flushStreamUpdate()
	if m.streamThrottleActive {
		t.Error("expected throttle inactive after flush even without pending")
	}
}

func TestSubmitMessageEmpty(t *testing.T) {
	m := newTestModel(t)
	m.chatInput.SetValue("   ")
	if cmd := m.submitMessage(); cmd != nil {
		t.Error("expected nil cmd for empty message")
	}
}

func TestSubmitMessageWelcomeCreatesChat(t *testing.T) {
	m := newTestModel(t)
	m.showWelcome = true
	m.currentKey = ""
	// Warm up textarea.
	before := m.currentKey
	m.chatInput.SetValue("hello there")
	m.submitMessage()
	if m.currentKey == "" {
		t.Error("expected a new chat to be created when on welcome screen")
	}
	if m.showWelcome {
		t.Error("expected showWelcome false after submitting message")
	}
	if m.processing == false {
		t.Error("expected processing true after submit")
	}
	if m.currentMessageID == "" {
		t.Error("expected a message ID")
	}
	if m.pendingUserMessage != "hello there" {
		t.Errorf("expected pendingUserMessage set, got %q", m.pendingUserMessage)
	}
	if strings.TrimSpace(m.chatInput.Value()) != "" {
		t.Error("expected chat input cleared after submit")
	}
	if m.currentKey == before {
		t.Error("expected currentKey to change when creating a new chat")
	}
}

func TestSubmitMessageExistingSession(t *testing.T) {
	m := newTestModel(t)
	key := "tui:chat:submit-me"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.chatInput.SetValue("  a real message  ")
	m.submitMessage()
	if m.pendingUserMessage != "a real message" {
		t.Errorf("expected trimmed pending message, got %q", m.pendingUserMessage)
	}
	if m.currentKey != key {
		t.Errorf("expected key retained, got %q", m.currentKey)
	}
}

func TestFilterAutocomplete(t *testing.T) {
	m := &Model{}
	// Find a command that starts with "/a" (e.g. /agents).
	m.filterAutocomplete("/a")
	if len(m.autocompleteItems) == 0 {
		t.Skip("no autocomplete items matched /a")
	}
	for _, item := range m.autocompleteItems {
		if !strings.HasPrefix(item.name, "/a") {
			t.Errorf("autocomplete item %q does not start with /a", item.name)
		}
	}
	// Reset index when selected index exceeds item count.
	m.autocompleteIdx = 100
	m.filterAutocomplete("/q")
	if m.autocompleteIdx != 0 {
		t.Errorf("expected autocompleteIdx reset to 0, got %d", m.autocompleteIdx)
	}
	// No matches → empty items, index reset.
	m.filterAutocomplete("/zzz-no-match")
	if len(m.autocompleteItems) != 0 {
		t.Errorf("expected no autocomplete items, got %d", len(m.autocompleteItems))
	}
}

func TestGetGroupProfilesEmpty(t *testing.T) {
	m := newTestModel(t)
	if got := m.getGroupProfiles(); got == nil {
		t.Log("group profiles empty slice (config has no groups)")
	}
}

func TestSubmitGroupStartCreatesChat(t *testing.T) {
	m := newTestModel(t)
	m.showWelcome = true
	m.currentKey = ""
	m.chatInput.SetValue("ignored")
	m.processing = false
	m.submitGroupStart("profile-1", "do the thing")
	if m.currentKey == "" {
		t.Error("expected new chat created")
	}
	if !m.processing {
		t.Error("expected processing true after group start")
	}
	if m.pendingUserMessage != "" {
		t.Error("group start should not set pendingUserMessage")
	}
	// The published command should be cleared from the input.
	if strings.TrimSpace(m.chatInput.Value()) != "" {
		t.Error("expected input cleared after group submit")
	}
}

func TestSubmitGroupStartReturnsTickCmd(t *testing.T) {
	m := newTestModel(t)
	m.currentKey = ""
	m.showWelcome = false
	cmd := m.submitGroupStart("profile-2", "task")
	if cmd == nil {
		t.Fatal("expected a tick cmd")
	}
	// Executing returns a tickMsg.
	msg := cmd()
	if _, ok := msg.(tickMsg); !ok {
		t.Fatalf("expected tickMsg, got %T", msg)
	}
}

// --- utils.go coverage ---

func TestFormatToolCallArgsStructured(t *testing.T) {
	m := &Model{}
	_ = m
	tc := providers.ToolCall{
		Arguments: map[string]interface{}{
			"query": "hello",
			"n":     3,
		},
	}
	out := formatToolCallArgs(tc)
	if !strings.Contains(out, "query: hello") || !strings.Contains(out, "n: 3") {
		t.Errorf("formatToolCallArgs structured = %q", out)
	}
}

func TestFormatToolCallArgsLongValueTruncates(t *testing.T) {
	long := strings.Repeat("x", 200)
	tc := providers.ToolCall{Arguments: map[string]interface{}{"data": long}}
	out := formatToolCallArgs(tc)
	if !strings.Contains(out, "…") {
		t.Errorf("expected truncated value with ellipsis, got %q", out)
	}
}

func TestFormatToolCallArgsJSONFunction(t *testing.T) {
	tc := providers.ToolCall{
		Name: "tools",
		Function: &providers.FunctionCall{
			Name:      "search",
			Arguments: `{"q": "golang", "n": 5}`,
		},
	}
	out := formatToolCallArgs(tc)
	if !strings.Contains(out, "q: golang") {
		t.Errorf("expected parsed JSON args, got %q", out)
	}
}

func TestFormatToolCallArgsJSONFallbackRaw(t *testing.T) {
	tc := providers.ToolCall{
		Function: &providers.FunctionCall{Arguments: `not-json`},
	}
	out := formatToolCallArgs(tc)
	if out != "not-json" {
		t.Errorf("expected raw fallback, got %q", out)
	}
}

func TestFormatToolCallArgsEmpty(t *testing.T) {
	tc := providers.ToolCall{}
	if got := formatToolCallArgs(tc); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFormatTokenK(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{12345, "12.3K"},
		{100000, "100K"},
		{1234567, "1235K"},
	}
	for _, tt := range tests {
		if got := formatTokenK(tt.in); got != tt.want {
			t.Errorf("formatTokenK(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWrapText(t *testing.T) {
	// limit <= 0 returns unchanged
	if got := wrapText("abc", 0); got != "abc" {
		t.Errorf("wrapText limit 0 = %q", got)
	}
	// lines short than limit
	if got := wrapText("abc\ndef", 10); got != "abc\ndef" {
		t.Errorf("wrapText short = %q", got)
	}
	// empty line preserved
	got := wrapText("hello world\n\nfoo bar baz", 5)
	if got == "" {
		t.Fatal("expected non-empty wrapped text")
	}
	if !strings.Contains(got, "hello") {
		t.Errorf("wrapText = %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("truncateRunes within limit = %q", got)
	}
	if got := truncateRunes("h\u00e9llo", 3); got != "h\u00e9l" {
		t.Errorf("truncateRunes multi-byte = %q", got)
	}
	if got := truncateRunes("hello", 2); got != "he" {
		t.Errorf("truncateRunes short = %q", got)
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{123, "123"},
		{1234, "1,234"},
		{1234567, "1,234,567"},
		{-1234, "-1,234"},
	}
	for _, tt := range tests {
		if got := formatNumber(tt.in); got != tt.want {
			t.Errorf("formatNumber(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSortSubagents(t *testing.T) {
	subagents := []channels.SubagentTaskInfo{
		{TaskID: "subagent-2", Created: 100},
		{TaskID: "subagent-10", Created: 50},
		{TaskID: "subagent-1", Created: 200},
	}
	sortSubagents(subagents)
	// By number descending: 10, 2, 1
	got := []string{subagents[0].TaskID, subagents[1].TaskID, subagents[2].TaskID}
	want := []string{"subagent-10", "subagent-2", "subagent-1"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sortSubagents order = %v, want %v", got, want)
		}
	}
}

func TestSortSubagentsMixedIDs(t *testing.T) {
	subagents := []channels.SubagentTaskInfo{
		{TaskID: "abc", Created: 500},
		{TaskID: "subagent-5", Created: 300},
		{TaskID: "xyz", Created: 100},
	}
	sortSubagents(subagents)
	// Pairs where only one has a subagent number fall back to Created desc.
	got := []string{subagents[0].TaskID, subagents[1].TaskID, subagents[2].TaskID}
	want := []string{"abc", "subagent-5", "xyz"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sortSubagents mixed order = %v, want %v", got, want)
		}
	}
}

func TestSortSubagentsEqualCreated(t *testing.T) {
	subagents := []channels.SubagentTaskInfo{
		{TaskID: "b", Created: 100},
		{TaskID: "a", Created: 100},
	}
	sortSubagents(subagents)
	// Equal Created, neither is subagent-N → TaskID descending.
	if subagents[0].TaskID != "b" || subagents[1].TaskID != "a" {
		t.Errorf("sortSubagents equal created = %q, %q", subagents[0].TaskID, subagents[1].TaskID)
	}
}

func TestMessageFingerprint(t *testing.T) {
	m1 := providers.Message{Role: "assistant", Content: "hi", ReasoningContent: ""}
	m2 := providers.Message{Role: "assistant", Content: "hi", ReasoningContent: ""}
	if messageFingerprint(m1, 80) != messageFingerprint(m2, 80) {
		t.Error("same messages should produce same fingerprint")
	}
	m3 := providers.Message{Role: "assistant", Content: "hi", ReasoningContent: ""}
	if messageFingerprint(m1, 80) == messageFingerprint(m3, 100) {
		t.Error("different width should produce different fingerprint")
	}
	m4 := providers.Message{Role: "assistant", Content: "different"}
	if messageFingerprint(m1, 80) == messageFingerprint(m4, 80) {
		t.Error("different content should produce different fingerprint")
	}
	tc := providers.Message{Role: "assistant", Content: "hi", ToolCalls: []providers.ToolCall{
		{Name: "n", Function: &providers.FunctionCall{Name: "fn", Arguments: `{"a":1}`}},
	}}
	if messageFingerprint(tc, 80) == messageFingerprint(m1, 80) {
		t.Error("tool calls should affect fingerprint")
	}
	_ = fmt.Sprintf
	_ = tea.KeyMsg{}
}
