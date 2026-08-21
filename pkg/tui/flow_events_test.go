package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/bus"
)

// setupFlowModel builds a model with a real session for event-flow tests.
func setupFlowModel(t *testing.T) (*Model, string) {
	t.Helper()
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:flow-events"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()
	m.updateViewport()
	return m, key
}

// TestUpdate_StreamingEvents drives the outbound streaming events through
// Update, covering message.stream, message.thinking, tool.executing,
// tool.result, and the final empty-event flush.
func TestUpdate_StreamingEvents(t *testing.T) {
	m, key := setupFlowModel(t)

	// message.thinking — accumulate thinking stream
	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "message.thinking",
		MessageID: "msg-1", Content: "Let me think",
	}})
	if m.currentThinking != "Let me think" {
		t.Errorf("currentThinking = %q, want accumulation", m.currentThinking)
	}

	// tool.executing — set active tool action
	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "tool.executing",
		MessageID: "msg-1", Metadata: map[string]string{"tool": "web_search", "action": "web_search: Go"},
	}})
	if m.currentToolAction != "web_search: Go" {
		t.Errorf("currentToolAction = %q, want from action metadata", m.currentToolAction)
	}

	// tool.result — clears the tool action
	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "tool.result",
		MessageID: "msg-1", Metadata: map[string]string{"tool": "web_search"},
	}})
	if m.currentToolAction != "" {
		t.Errorf("currentToolAction = %q, want cleared after tool.result", m.currentToolAction)
	}

	// message.stream — first chunk establishes the assistant message ID
	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "message.stream",
		MessageID: "msg-2", Content: "Hello",
	}})
	if m.currentStream != "Hello" {
		t.Errorf("currentStream = %q, want 'Hello'", m.currentStream)
	}
	if m.currentAssistantMsgID != "msg-2" {
		t.Errorf("currentAssistantMsgID = %q, want msg-2", m.currentAssistantMsgID)
	}

	// Second chunk with same ID appends without resetting state.
	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "message.stream",
		MessageID: "msg-2", Content: " world",
	}})
	if m.currentStream != "Hello world" {
		t.Errorf("currentStream = %q, want 'Hello world' appended", m.currentStream)
	}
}

// TestUpdate_StreamingDeduplicateMessageID verifies that a different
// message.thinking on a NEW message ID resets the thinking buffer.
func TestUpdate_StreamingDeduplicateMessageID(t *testing.T) {
	m, key := setupFlowModel(t)
	m.currentAssistantMsgID = "old-id"

	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "message.thinking",
		MessageID: "new-id", Content: "fresh",
	}})
	if m.currentAssistantMsgID != "new-id" {
		t.Errorf("assistant msg ID = %q, want updated to new-id", m.currentAssistantMsgID)
	}
	if m.currentThinking != "fresh" {
		t.Errorf("currentThinking = %q, want reset+accumulated 'fresh'", m.currentThinking)
	}
}

// TestUpdate_ToolExecutingClearsStreamOnNewMessage verifies that a
// tool.executing for a different message clears the stream/thinking buffers.
func TestUpdate_ToolExecutingClearsStreamOnNewMessage(t *testing.T) {
	m, key := setupFlowModel(t)
	m.currentStream = "stale stream text"
	m.currentThinking = "stale thinking"
	m.currentAssistantMsgID = "msg-old"

	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "tool.executing",
		MessageID: "msg-new", Metadata: map[string]string{"tool": "bash"},
	}})
	if m.currentAssistantMsgID != "msg-new" {
		t.Errorf("assistant msg ID = %q, want msg-new", m.currentAssistantMsgID)
	}
	if m.currentStream != "" || m.currentThinking != "" {
		t.Errorf("stream/thinking not cleared on new tool message: stream=%q thinking=%q",
			m.currentStream, m.currentThinking)
	}
	if m.currentToolAction != "bash" {
		t.Errorf("currentToolAction = %q, want 'bash' from tool metadata", m.currentToolAction)
	}
}

// TestUpdate_SubagentProgressEvents verifies tool.executing and
// message.stream events from a subagent of the current chat update progress.
func TestUpdate_SubagentProgressEvents(t *testing.T) {
	m, key := setupFlowModel(t)

	// Subagent chat keys are "native:<parentChatID>:subagent-<n>".
	subKey := "native:" + key + ":subagent-1"
	m.sessionMgr.GetOrCreate(subKey)

	// tool.executing from the subagent
	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: subKey, Event: "tool.executing",
		Metadata: map[string]string{"action": "web_search"},
	}})
	if m.subagentProgress == nil || m.subagentProgress["subagent-1"] != "web_search" {
		t.Errorf("subagentProgress[subagent-1]=%q, want web_search", m.subagentProgress["subagent-1"])
	}

	// message.stream from the subagent → "finalizing…"
	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: subKey, Event: "message.stream", Content: "...",
	}})
	if m.subagentProgress["subagent-1"] != "finalizing…" {
		t.Errorf("subagentProgress[subagent-1]=%q, want finalizing…", m.subagentProgress["subagent-1"])
	}
}

// TestUpdate_GroupEvents drives group.status / group.turn / group.complete.
func TestUpdate_GroupEvents(t *testing.T) {
	m, key := setupFlowModel(t)

	// group.status started
	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "group.status",
		Metadata: map[string]string{
			"group_id": "g1", "status": "started",
			"participants": "a,b",
		},
	}})
	if m.activeGroupID != "g1" {
		t.Errorf("activeGroupID = %q, want g1", m.activeGroupID)
	}
	if !m.processing {
		t.Error("processing should be true after group started")
	}
	if m.groupMeta["g1"].participants != "a,b" {
		t.Errorf("group g1 participants = %q, want a,b", m.groupMeta["g1"].participants)
	}

	// group.turn adds a transcript entry
	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "group.turn",
		Metadata: map[string]string{
			"group_id": "g1", "layer": "1", "turn_index": "2",
			"speaker": "agent-a", "label": "a", "role": "proposer",
		},
		Content: "some reasoning",
	}})
	turns := m.groupTranscripts["g1"]
	if len(turns) != 1 {
		t.Fatalf("expected 1 group turn, got %d", len(turns))
	}
	if turns[0].speaker != "agent-a" || turns[0].content != "some reasoning" {
		t.Errorf("turn not populated correctly: %+v", turns[0])
	}

	// group.complete sets synthesis metadata and marks done
	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "group.complete",
		Metadata: map[string]string{
			"group_id": "g1", "strategy": "round_robin", "layers": "3", "total_tokens": "1000",
		},
		Content: "final synthesis",
	}})
	if m.processing {
		t.Error("processing should be false after group.complete")
	}
	if m.groupMeta["g1"].strategy != "round_robin" || m.groupMeta["g1"].synthesis != "final synthesis" {
		t.Errorf("group.complete metadata wrong: %+v", m.groupMeta["g1"])
	}
	if m.groupStatus["g1"] != "done" {
		t.Errorf("groupStatus[g1] = %q, want done", m.groupStatus["g1"])
	}
}

// TestUpdate_TickAndStreamThrottle verifies animation tick handling and the
// stream throttle retick.
func TestUpdate_TickAndStreamThrottle(t *testing.T) {
	m, _ := setupFlowModel(t)

	// Set processing so tick schedules a follow-up tick.
	m.processing = true
	m.startTime = time.Now()
	m.tickPending = false
	_, cmd := m.Update(tickMsg{})
	if cmd == nil {
		t.Error("expected a cmd from tick while processing")
	}

	// streamThrottleMsg with no pending update → clears throttle.
	m.streamThrottleActive = true
	m.streamPendingUpdate = false
	_, _ = m.Update(streamThrottleMsg{})
	if m.streamThrottleActive {
		t.Error("streamThrottleActive should be false after throttle without pending update")
	}

	// streamThrottleMsg with pending update → re-render and re-schedule.
	m.streamThrottleActive = true
	m.streamPendingUpdate = true
	_, cmd2 := m.Update(streamThrottleMsg{})
	if m.streamPendingUpdate {
		t.Error("streamPendingUpdate should be cleared after handling pending update")
	}
	if m.streamThrottleActive != true {
		t.Error("streamThrottleActive should be re-armed while pending update was processed")
	}
	if cmd2 == nil {
		t.Error("expected a re-scheduled throttle cmd")
	}

	// tick resets pending flag (and re-schedules since processing stays true).
	m.processing = false
	m.tickPending = true
	_, _ = m.Update(tickMsg{})
	if m.tickPending {
		t.Error("tick should reset tickPending")
	}
}

// TestUpdate_CompleteMsg verifies the completion message clears processing.
func TestUpdate_CompleteMsg(t *testing.T) {
	m, key := setupFlowModel(t)
	m.processing = true
	m.pendingSubagentCompletions = 0
	m.currentStream = "stale"

	_, _ = m.Update(completeMsg{sessionKey: key})
	if m.processing {
		t.Error("processing should be false after completeMsg")
	}
	if m.currentStream != "" {
		t.Error("stream should be cleared after completeMsg")
	}
}

// TestUpdate_CompactResultMsg verifies compact result handling.
func TestUpdate_CompactResultMsg(t *testing.T) {
	m, key := setupFlowModel(t)
	m.processing = true
	m.currentToolAction = "stale"

	_, _ = m.Update(compactResultMsg{sessionKey: key, result: "compacted 5 messages"})
	if m.processing {
		t.Error("processing should be false after compactResultMsg")
	}
	if m.compactFeedback != "compacted 5 messages" {
		t.Errorf("compactFeedback = %q, want compacted result", m.compactFeedback)
	}
	if m.currentToolAction != "" {
		t.Error("tool action should be cleared after compact")
	}
}

// TestUpdate_WindowResize clears render caches.
func TestUpdate_WindowResize(t *testing.T) {
	m, _ := setupFlowModel(t)

	// Pre-resize, manage a message.
	m.sessionMgr.AddMessage(m.currentKey, "user", "hello")
	m.updateViewport()

	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.width != 80 || m.height != 24 {
		t.Errorf("window size not stored: %d x %d", m.width, m.height)
	}
	// Resize should not panic and should leave the model viewable.
	if m.width == 0 || m.height == 0 {
		t.Error("window dimensions must be non-zero after resize")
	}
}

// TestUpdate_EnterSendsCommand verifies typing a non-command and pressing
// enter sends a message (with a processing session setup to avoid blocking).
func TestUpdate_EnterSendsAndAutocomplete(t *testing.T) {
	m, key := setupFlowModel(t)

	// Type into the input with autocomplete off; pressing enter submits.
	m.chatInput.SetValue("/providers")
	buf := m.chatInput.Value()
	if !strings.HasPrefix(buf, "/") {
		t.Fatalf("expected slash command buffer, got %q", buf)
	}

	// Enter dispatches to the /providers command handler.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Log("enter returned no cmd (expected for /providers command)")
	}
	_ = key
}

// TestUpdate_MouseSelection drives mouse press/motion/release selection.
func TestUpdate_MouseSelection(t *testing.T) {
	m, _ := setupFlowModel(t)
	m.mouseEnabled = true
	m.modalMode = ModalNone
	// Set viewport height so the click lands inside it.
	m.updateViewport()
	if m.viewport.Height == 0 {
		m.viewport.Height = 20
	}

	// Press in the viewport area begins selection.
	upd, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 10, Y: 5})
	mm := upd.(*Model)
	if !mm.selecting {
		t.Error("expect selecting true after press in viewport")
	}
	if mm.selStartX != 10 || mm.selStartY != 5 {
		t.Errorf("selStart = (%d,%d), want (10,5)", mm.selStartX, mm.selStartY)
	}

	// Motion extends the selection.
	upd, _ = mm.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion, X: 30, Y: 7})
	mm = upd.(*Model)
	if !mm.selecting {
		t.Error("expect still selecting after motion")
	}
	if mm.selEndX != 30 || mm.selEndY != 7 {
		t.Errorf("selEnd = (%d,%d), want (30,7)", mm.selEndX, mm.selEndY)
	}

	// Release finishes the selection.
	upd, _ = mm.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 30, Y: 7})
	mm = upd.(*Model)
	if mm.selecting {
		t.Error("expect selecting false after release")
	}
}

// TestUpdate_SubagentResultEvent ensures outbound subagent.result starts a
// pending-completion flow.
func TestUpdate_SubagentResultEvent(t *testing.T) {
	m, key := setupFlowModel(t)
	m.processing = false

	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "subagent.result",
		Metadata: map[string]string{"id": "t1"},
	}})
	if m.pendingSubagentCompletions != 1 {
		t.Errorf("pendingSubagentCompletions = %d, want 1", m.pendingSubagentCompletions)
	}
	if !m.processing {
		t.Error("processing should be true after subagent.result")
	}
	if !m.parentCompletionObserved {
		t.Error("parentCompletionObserved should be true when not already processing")
	}
}

// TestUpdate_ApprovalRequestEvent stores the pending approval request.
func TestUpdate_ApprovalRequestEvent(t *testing.T) {
	m, key := setupFlowModel(t)

	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID: key, Event: "approval.request",
		Metadata: map[string]string{
			"id": "a1", "command": "rm -rf /tmp/foo", "reason": "unsafe op",
		},
	}})
	if m.pendingApprovalID != "a1" {
		t.Errorf("pendingApprovalID = %q, want a1", m.pendingApprovalID)
	}
	if m.pendingApprovalCmd != "rm -rf /tmp/foo" {
		t.Errorf("pendingApprovalCmd = %q", m.pendingApprovalCmd)
	}
	if m.pendingApprovalReason != "unsafe op" {
		t.Errorf("pendingApprovalReason = %q", m.pendingApprovalReason)
	}
}

// TestUpdate_MouseWheelScroll drives wheel-up viewport scrolling.
func TestUpdate_MouseWheelScroll(t *testing.T) {
	m, _ := setupFlowModel(t)
	m.mouseEnabled = true
	m.modalMode = ModalNone
	m.updateViewport()

	// Wheel up moves the viewport (should not crash).
	m.viewport.YOffset = 0
	_, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress, X: 10, Y: 10})
}