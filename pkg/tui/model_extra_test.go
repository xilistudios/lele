package tui

import (
	"strings"
	"testing"
	"time"
)

// --- model.go additional coverage ---

func TestInit(t *testing.T) {
	m := newTestModel(t)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected a non-nil batch cmd")
	}
	// The batch cmd should start the outbound listener and produce messages.
	msg := cmd()
	if msg == nil {
		t.Fatal("expected init cmd to produce at least one msg when executed")
	}
}

func TestResetStreamState(t *testing.T) {
	m := &Model{
		currentStream:       "s",
		currentThinking:     "t",
		streamRenderedLines: []string{"a"},
		thinkingRenderedLines: []string{"b"},
		streamRenderedJoined: "a",
		thinkingRenderedJoined: "b",
	}
	m.resetStreamState()
	if m.currentStream != "" || m.currentThinking != "" {
		t.Error("expected stream/thinking cleared")
	}
	if m.streamRenderedLines != nil || m.thinkingRenderedLines != nil {
		t.Error("expected rendered lines cleared")
	}
	if m.streamRenderedJoined != "" || m.thinkingRenderedJoined != "" {
		t.Error("expected joined cleared")
	}
}

func TestClearStreamingState(t *testing.T) {
	m := newTestModel(t)
	key := "tui:chat:cs"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.streamThrottleActive = true
	m.streamPendingUpdate = true
	m.compactFeedback = "old"
	m.currentStream = "stale"
	m.currentThinking = "stale"
	m.currentToolAction = "tool"
	m.currentMessageID = "mid"
	m.currentAssistantMsgID = "aid"
	m.pendingSubagentCompletions = 3
	m.pendingUserMessage = "pending"
	m.escHint = true
	m.escPressCount = 2
	m.pendingApprovalID = "approval-id"
	m.pendingApprovalCmd = "rm -rf"
	m.pendingApprovalReason = "danger"
	m.approvalResult = "result"
	m.activeGroupID = "g1"
	m.renderedBaseValid = true
	m.renderedBaseKey = "k"
	m.subagentProgress = map[string]string{"s1": "step"}
	m.msgRenderCacheLines = map[string][]string{"f": {"line"}}
	m.forceGotoBottom = false

	m.clearStreamingState()
	if m.streamThrottleActive || m.streamPendingUpdate {
		t.Error("expected throttle state cleared")
	}
	if m.compactFeedback != "" {
		t.Error("expected compactFeedback cleared")
	}
	if m.currentStream != "" || m.currentThinking != "" || m.currentToolAction != "" {
		t.Error("expected streaming state cleared")
	}
	if m.currentMessageID != "" || m.currentAssistantMsgID != "" {
		t.Error("expected message IDs cleared")
	}
	if m.pendingUserMessage != "" {
		t.Error("expected pending message cleared")
	}
	if m.escHint || m.escPressCount != 0 {
		t.Error("expected esc state cleared")
	}
	if m.pendingApprovalID != "" || m.pendingApprovalCmd != "" {
		t.Error("expected approval state cleared")
	}
	if m.activeGroupID != "" {
		t.Error("expected active group cleared")
	}
	if m.renderedBaseValid || m.renderedBaseKey != "" {
		t.Error("expected render cache invalidated")
	}
	if !m.forceGotoBottom {
		t.Error("expected forceGotoBottom true")
	}
}

func TestIsSubagentSessionKey(t *testing.T) {
	if !isSubagentSessionKey("native:tui:chat:x:subagent-1") {
		t.Error("expected true for subagent session key")
	}
	if isSubagentSessionKey("tui:chat:normal") {
		t.Error("expected false for normal session")
	}
}

func TestIsSubagentOfCurrentChat(t *testing.T) {
	m := &Model{currentKey: "tui:chat:parent"}
	if !m.isSubagentOfCurrentChat("native:tui:chat:parent:subagent-2") {
		t.Error("expected true for subagent of current chat")
	}
	if m.isSubagentOfCurrentChat("native:tui:chat:other:subagent-2") {
		t.Error("expected false for other chat's subagent")
	}
}

func TestCurrentSubagentTaskID(t *testing.T) {
	m := &Model{currentKey: "tui:chat:parent"}
	if got := m.currentSubagentTaskID("native:tui:chat:parent:subagent-3"); got != "subagent-3" {
		t.Errorf("currentSubagentTaskID = %q, want subagent-3", got)
	}
	if got := m.currentSubagentTaskID("native:tui:chat:other:subagent-1"); got != "" {
		t.Errorf("expected empty for foreign chat, got %q", got)
	}
}

func TestIsSubagentSession(t *testing.T) {
	m := &Model{}
	if !m.isSubagentSession("tui:chat:x:subagent-1") {
		t.Error("expected subagent key true")
	}
	if !m.isSubagentSession("subagent:foo") {
		t.Error("expected 'subagent:' prefix true")
	}
	if m.isSubagentSession("tui:chat:normal") {
		t.Error("expected false for normal key")
	}
}

func TestCurrentSessionKey(t *testing.T) {
	m := &Model{currentKey: "tui:chat:abc"}
	if m.currentSessionKey() != "tui:chat:abc" {
		t.Errorf("currentSessionKey = %q", m.currentSessionKey())
	}
}

func TestIsSessionProcessingBackend(t *testing.T) {
	m := newTestModel(t)
	key := "tui:chat:isp"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	// Backend not processing → check local startup window.
	m.processing = true
	m.startTime = time.Now()
	if !m.isSessionProcessing() {
		t.Error("expected processing true within startup window")
	}
	// After 3s window, stale processing is cleared.
	m.processing = true
	m.startTime = time.Now().Add(-4e9) // 4 seconds ago
	if m.isSessionProcessing() {
		t.Error("expected processing false after startup window")
	}
	if m.processing {
		t.Error("expected stale processing flag reset")
	}
}

func TestResumeSessionID(t *testing.T) {
	m := &Model{currentKey: "tui:chat:abc", showWelcome: false}
	if got := m.ResumeSessionID(); got != "abc" {
		t.Errorf("ResumeSessionID = %q, want abc", got)
	}
	// Empty key.
	if got := (&Model{}).ResumeSessionID(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	// Subagent parent.
	m2 := &Model{currentKey: "tui:chat:abc", parentSessionKey: "tui:chat:parent", showWelcome: false}
	if got := m2.ResumeSessionID(); got != "parent" {
		t.Errorf("ResumeSessionID with parent = %q, want parent", got)
	}
	// Welcome with no history → empty.
	m3 := newTestModel(t)
	k3 := "tui:chat:ws"
	m3.sessionMgr.GetOrCreate(k3)
	m3.sessionMgr.SetMode(k3, "agent")
	m3.currentKey = k3
	m3.showWelcome = true
	if got := m3.ResumeSessionID(); got != "" {
		t.Errorf("expected empty on welcome, got %q", got)
	}
}

func TestCleanupStreamingIfCompleteNoHistory(t *testing.T) {
	m := newTestModel(t)
	m.currentStream = "streaming..."
	m.currentThinking = ""
	// When currentKey empty, no-op.
	m.currentKey = ""
	m.cleanupStreamingIfComplete()
	if m.currentStream == "" {
		t.Error("should not clear stream when key empty")
	}
}

// --- viewport.go additional coverage ---

func TestLastHistoryRole(t *testing.T) {
	m := newTestModel(t)
	key := "tui:chat:lhr"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "system", "sys")
	m.sessionMgr.AddMessage(key, "user", "u")
	m.sessionMgr.AddMessage(key, "assistant", "a")
	m.currentKey = key
	if got := m.lastHistoryRole(); got != "assistant" {
		t.Errorf("lastHistoryRole = %q, want assistant", got)
	}
	// Only system → empty
	m2 := newTestModel(t)
	k2 := "tui:chat:lhr2"
	m2.sessionMgr.GetOrCreate(k2)
	m2.sessionMgr.SetMode(k2, "agent")
	m2.sessionMgr.AddMessage(k2, "system", "s")
	m2.currentKey = k2
	if got := m2.lastHistoryRole(); got != "" {
		t.Errorf("lastHistoryRole all-system = %q, want ''", got)
	}
}

func TestRenderGroupTurns(t *testing.T) {
	m := &Model{}
	turns := []groupTurn{
		{layer: 0, speaker: "agent1", label: "A", role: "speaker", content: "hello"},
		{layer: 1, speaker: "agent2", label: "B", role: "critic", content: "world"},
	}
	out := stripANSI(m.renderGroupTurns(turns, 80))
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Errorf("renderGroupTurns missing content, got %q", out)
	}
	if !strings.Contains(out, "A") || !strings.Contains(out, "B") {
		t.Errorf("renderGroupTurns missing labels, got %q", out)
	}
	// Empty content tolerated.
	empty := m.renderGroupTurns([]groupTurn{{layer: 0, speaker: "s"}}, 80)
	if empty == "" {
		t.Error("expected output even with empty content")
	}
}

func TestRenderGroupTurnsSynthesis(t *testing.T) {
	m := &Model{
		activeGroupID: "g1",
		groupMeta: map[string]groupMeta{
			"g1": {synthesis: "final synthesis text"},
		},
	}
	turns := []groupTurn{{layer: 0, speaker: "s", content: "c"}}
	out := stripANSI(m.renderGroupTurns(turns, 80))
	if !strings.Contains(out, "final synthesis text") {
		t.Errorf("expected synthesis rendered, got %q", out)
	}
}

func TestBuildRenderedHistory(t *testing.T) {
	m := newTestModel(t)
	key := "tui:chat:brh"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "question")
	m.sessionMgr.AddMessage(key, "assistant", "answer")
	m.currentKey = key
	m.showWelcome = false
	lines := m.buildRenderedHistory()
	if len(lines) == 0 {
		t.Fatal("expected rendered history lines")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "question") {
		t.Errorf("expected content in history, got %v", lines)
	}
}
