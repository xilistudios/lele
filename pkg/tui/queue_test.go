package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func drainInbound(t *testing.T, m *Model) bus.InboundMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, ok := m.agentLoop.MessageBus().ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no inbound message published within 2s")
	}
	return msg
}

func expectNoInbound(t *testing.T, m *Model) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if msg, ok := m.agentLoop.MessageBus().ConsumeInbound(ctx); ok {
		t.Fatalf("unexpected inbound message published: %q", msg.Content)
	}
}

func newQuietQueueModel(t *testing.T, key string) *Model {
	t.Helper()
	cfg := testModelConfig(t)
	al := agent.NewAgentLoop(cfg, bus.NewMessageBus())

	sessionMgr := al.SessionManager()
	if sessionMgr == nil {
		t.Fatal("session manager not initialized")
	}
	m := NewModel(cfg, al, sessionMgr)
	m.onboardingActive = false
	m.width, m.height = 120, 36

	sessionMgr.GetOrCreate(key)
	_ = sessionMgr.SetMode(key, "agent")
	m.setCurrentChatKey(key)
	m.showWelcome = false
	return m
}

func newQueueTestModel() *Model {
	m := &Model{currentKey: "tui:chat:a", chatInput: textarea.New()}
	return m
}

func TestQueueEnqueueFIFOAndPop(t *testing.T) {
	m := newQueueTestModel()

	if !m.enqueueMessage("one") || !m.enqueueMessage("two") {
		t.Fatal("enqueue failed")
	}
	if got := m.queueDepth(); got != 2 {
		t.Fatalf("queueDepth = %d, want 2", got)
	}
	if got := m.queuePreview(); got != "one" {
		t.Fatalf("queuePreview = %q, want %q", got, "one")
	}

	first, ok := m.popQueuedMessage("tui:chat:a")
	if !ok || first != "one" {
		t.Fatalf("pop = (%q,%v), want (one,true)", first, ok)
	}
	second, ok := m.popQueuedMessage("tui:chat:a")
	if !ok || second != "two" {
		t.Fatalf("pop = (%q,%v), want (two,true)", second, ok)
	}
	if _, ok := m.popQueuedMessage("tui:chat:a"); ok {
		t.Fatal("pop on empty queue returned ok")
	}
	if got := m.queueDepth(); got != 0 {
		t.Fatalf("queueDepth after draining = %d, want 0", got)
	}
}

func TestQueueIsScopedPerSession(t *testing.T) {
	m := newQueueTestModel()
	m.enqueueMessage("for-a")

	m.currentKey = "tui:chat:b"
	if got := m.queueDepth(); got != 0 {
		t.Fatalf("depth for session b = %d, want 0 (queue must be per session)", got)
	}
	m.enqueueMessage("for-b")

	if got, _ := m.popQueuedMessage("tui:chat:b"); got != "for-b" {
		t.Fatalf("pop b = %q, want for-b", got)
	}
	m.currentKey = "tui:chat:a"
	if got := m.queuePreview(); got != "for-a" {
		t.Fatalf("preview a = %q, want for-a", got)
	}
}

func TestQueueClearOnlyAffectsCurrentSession(t *testing.T) {
	m := newQueueTestModel()
	m.enqueueMessage("a1")
	m.currentKey = "tui:chat:b"
	m.enqueueMessage("b1")

	m.clearQueue()
	if m.queueDepth() != 0 {
		t.Fatalf("b depth after clear = %d, want 0", m.queueDepth())
	}

	m.currentKey = "tui:chat:a"
	if m.queueDepth() != 1 {
		t.Fatalf("a depth after clearing b = %d, want 1", m.queueDepth())
	}
}

func TestQueueEnforceMaxDepth(t *testing.T) {
	m := newQueueTestModel()
	for i := 0; i < maxQueuedMessages; i++ {
		if !m.enqueueMessage("msg") {
			t.Fatalf("enqueue %d unexpectedly rejected", i)
		}
	}
	if m.enqueueMessage("overflow") {
		t.Fatal("enqueue accepted a message beyond maxQueuedMessages")
	}
	if got := m.queueDepth(); got != maxQueuedMessages {
		t.Fatalf("depth = %d, want %d", got, maxQueuedMessages)
	}
}

func TestQueueRowLineFeedback(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := newQueueTestModel()
	if got := m.queueRowLine(80); got != "" {
		t.Fatalf("empty queue row = %q, want empty", got)
	}

	m.enqueueMessage("hello")
	if got := m.queueRowLine(80); !strings.Contains(got, "1") || !strings.Contains(got, "hello") {
		t.Fatalf("queue row = %q, want depth and preview", got)
	}

	m.queueFeedback = "queue full"
	if got := m.queueRowLine(80); got != "queue full" {
		t.Fatalf("queue row = %q, want the feedback text", got)
	}

	m = newQueueTestModel()
	m.queueFeedback = i18n.T("tui.queue.removed")
	if got := m.queueRowLine(80); got != m.queueFeedback {
		t.Fatalf("queue row = %q, want the removal feedback with an empty queue", got)
	}
	long := strings.Repeat("feedback ", 20)
	m.queueFeedback = long
	if got := m.queueRowLine(30); lipgloss.Width(got) > 30 {
		t.Fatalf("feedback row = %q (%d cells), want <= 30", got, lipgloss.Width(got))
	}

	if got := m.queueRowLine(0); got != "" {
		t.Fatalf("queue row at width 0 = %q, want empty", got)
	}
}

func TestQueueRowLine(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := newQueueTestModel()

	if got := m.queueRowLine(80); got != "" {
		t.Fatalf("idle queue row = %q, want empty", got)
	}

	m.enqueueMessage("deploy the staging build")
	got := m.queueRowLine(80)
	if !strings.Contains(got, "1") {
		t.Fatalf("queue row %q does not mention the pending count", got)
	}
	if !strings.Contains(got, "deploy the staging build") {
		t.Fatalf("queue row %q does not preview the oldest pending message", got)
	}
	if strings.Contains(got, "· ·") {
		t.Fatalf("queue row %q rendered a doubled separator", got)
	}

	m.enqueueMessage("second message")
	if got := m.queueRowLine(80); !strings.Contains(got, "deploy the staging build") || strings.Contains(got, "second message") {
		t.Fatalf("queue row %q should preview the FIFO head", got)
	}

	m2 := newQueueTestModel()
	m2.enqueueMessage("first line\nsecond line\twith tabs")
	if got := m2.queueRowLine(80); strings.Contains(got, "\n") || !strings.Contains(got, "first line second line") {
		t.Fatalf("queue row %q was not collapsed to a single line", got)
	}

	m3 := newQueueTestModel()
	m3.enqueueMessage("\x1b[31mred\x1b[0m\x07bell")
	if got := m3.queueRowLine(80); strings.Contains(got, "\x1b") || strings.Contains(got, "\x07") {
		t.Fatalf("queue row %q leaked terminal control characters", got)
	}
}

func TestQueueRowLineRespectsWidthBudget(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := newQueueTestModel()
	m.enqueueMessage(strings.Repeat("wide payload ", 20))

	for _, avail := range []int{1, 2, 3, 5, 10, 40, 200} {
		got := m.queueRowLine(avail)
		if got == "" {
			continue
		}
		if w := lipgloss.Width(got); w > avail {
			t.Errorf("queue row at avail=%d renders %d cells: %q", avail, w, got)
		}
		if strings.Contains(got, "\n") {
			t.Errorf("queue row at avail=%d is multi-line: %q", avail, got)
		}
		if strings.HasSuffix(got, " ") {
			t.Errorf("queue row at avail=%d has trailing whitespace: %q", avail, got)
		}
	}
}

func TestEnterWhileBusyEnqueuesAndClearsInput(t *testing.T) {
	m := newQueueTestModel()
	m.chatInput.SetValue("  queued text  ")
	m.processing = true

	if cmd := m.enqueueCurrentInput(); cmd == nil {
		t.Fatal("enqueue returned no cmd; the queue needs a retry tick")
	}
	if got := m.queuePreview(); got != "queued text" {
		t.Fatalf("queued %q, want %q", got, "queued text")
	}
	if got := m.chatInput.Value(); got != "" {
		t.Fatalf("input value after enqueue = %q, want empty", got)
	}
}

func TestEnterWhileBusyWithEmptyInputDoesNotQueue(t *testing.T) {
	m := newQueueTestModel()
	m.processing = true

	if cmd := m.enqueueCurrentInput(); cmd != nil {
		t.Fatalf("empty Enter returned a cmd (%v), want nil", cmd)
	}
	if got := m.queueDepth(); got != 0 {
		t.Fatalf("depth after Enter with empty input = %d, want 0", got)
	}
}

func TestEnqueueWhenQueueFullKeepsInputAndWarns(t *testing.T) {
	m := newQueueTestModel()
	m.processing = true
	m.chatInput.SetValue("overflow")
	for i := 0; i < maxQueuedMessages; i++ {
		m.enqueueMessage("filler")
	}

	if cmd := m.enqueueCurrentInput(); cmd != nil {
		t.Fatal("overflow enqueue returned a cmd, want nil")
	}
	if got := m.chatInput.Value(); got != "overflow" {
		t.Fatalf("input = %q, want the unqueued text preserved", got)
	}
	if m.queueFeedback == "" {
		t.Fatal("queueFeedback is empty; the user must be told why the text stayed")
	}
	if got := m.queueDepth(); got != maxQueuedMessages {
		t.Fatalf("depth = %d, want %d", got, maxQueuedMessages)
	}
}

func TestFlushQueueDeferredWhileBusy(t *testing.T) {
	m := newQueueTestModel()
	m.enqueueMessage("pending")
	m.processing = true
	m.startTime = time.Now()

	cmd := m.maybeFlushQueue()
	if cmd == nil {
		t.Fatal("deferred flush returned nil; the retry tick must keep the queue alive")
	}
	if got := m.queueDepth(); got != 1 {
		t.Fatalf("depth after deferred flush = %d, want 1 (nothing may pop while busy)", got)
	}
}

func TestFlushQueueDeferredWhileModalOpen(t *testing.T) {
	m := newQueueTestModel()
	m.enqueueMessage("pending")
	m.modalMode = ModalSessions

	if cmd := m.maybeFlushQueue(); cmd == nil {
		t.Fatal("flush while a modal is open must return a retry tick")
	}
	if got := m.queueDepth(); got != 1 {
		t.Fatalf("depth = %d, want 1 (a modal owns the input path)", got)
	}
}

func TestFlushQueueDeferredWhileApprovalPending(t *testing.T) {
	m := newQueueTestModel()
	m.enqueueMessage("pending")
	m.pendingApprovalID = "approval-1"

	if cmd := m.maybeFlushQueue(); cmd == nil {
		t.Fatal("flush while an approval is pending must return a retry tick")
	}
	if got := m.queueDepth(); got != 1 {
		t.Fatalf("depth = %d, want 1", got)
	}
}

func TestFlushQueueDeferredWhileAutocompleteOpen(t *testing.T) {
	m := newQueueTestModel()
	m.enqueueMessage("pending")
	m.showAutocomplete = true

	if cmd := m.maybeFlushQueue(); cmd == nil {
		t.Fatal("flush while the autocomplete popup is open must return a retry tick")
	}
	if got := m.queueDepth(); got != 1 {
		t.Fatalf("depth = %d, want 1", got)
	}
}

func TestFlushQueueEmptyQueueIsNoop(t *testing.T) {
	m := newQueueTestModel()
	if cmd := m.maybeFlushQueue(); cmd != nil {
		t.Fatal("flush with an empty queue returned a cmd, want nil")
	}
}

func TestQueueEndToEndEnqueueThenFlushOnIdle(t *testing.T) {
	key := "tui:chat:queue-e2e"
	m := newQuietQueueModel(t, key)

	m.processing = true
	m.startTime = time.Now()
	m.chatInput.SetValue("queued while busy")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)

	if got := m.queuePreview(); got != "queued while busy" {
		t.Fatalf("queue preview = %q, want the typed message", got)
	}
	if got := m.chatInput.Value(); got != "" {
		t.Fatalf("input = %q, want cleared after enqueue", got)
	}

	m.processing = false
	m.startTime = time.Time{}
	updated, cmd := m.Update(tickMsg(time.Now()))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("idle tick did not flush the queue")
	}

	msg := drainInbound(t, m)
	if msg.Content != "queued while busy" {
		t.Fatalf("published %q, want %q", msg.Content, "queued while busy")
	}
	if msg.SessionKey != key || msg.ChatID != key {
		t.Fatalf("published to %q/%q, want %q", msg.SessionKey, msg.ChatID, key)
	}
	if got := m.queueDepth(); got != 0 {
		t.Fatalf("depth after flush = %d, want 0", got)
	}
	if m.pendingUserMessage != "queued while busy" {
		t.Fatalf("pendingUserMessage = %q, want the flushed content", m.pendingUserMessage)
	}
	if !m.processing {
		t.Fatal("processing = false after flush, want true")
	}
}

func TestQueueEndToEndTwoMessagesDrainInOrder(t *testing.T) {
	key := "tui:chat:queue-order"
	m := newQuietQueueModel(t, key)

	m.processing = true
	m.startTime = time.Now()
	for _, text := range []string{"first", "second"} {
		m.chatInput.SetValue(text)
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(*Model)
	}
	if got := m.queueDepth(); got != 2 {
		t.Fatalf("depth = %d, want 2", got)
	}

	m.processing = false
	m.startTime = time.Time{}
	updated, cmd := m.Update(tickMsg(time.Now()))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("idle tick did not flush the queue")
	}
	if msg := drainInbound(t, m); msg.Content != "first" {
		t.Fatalf("published %q, want the oldest message first", msg.Content)
	}
	if got := m.queueDepth(); got != 1 {
		t.Fatalf("depth after one flush = %d, want 1", got)
	}
	if got := m.queuePreview(); got != "second" {
		t.Fatalf("next up = %q, want %q", got, "second")
	}
}

func TestQueueEndToEndSecondEnterWhileFlushedTurnIsBusy(t *testing.T) {
	key := "tui:chat:queue-no-double"
	m := newQuietQueueModel(t, key)

	m.enqueueMessage("in flight")
	m.processing = false
	m.startTime = time.Time{}

	updated, cmd := m.Update(tickMsg(time.Now()))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("flush returned no cmd")
	}
	drainInbound(t, m)

	m.chatInput.SetValue("tail")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if got := m.queuePreview(); got != "tail" {
		t.Fatalf("preview = %q, want the second message queued", got)
	}
	expectNoInbound(t, m)
}

func TestQueueEndToEndTickWhileModalOpenKeepsQueueAlive(t *testing.T) {
	key := "tui:chat:queue-modal"
	m := newQuietQueueModel(t, key)

	m.enqueueMessage("blocked by modal")
	m.processing = false
	m.startTime = time.Time{}
	m.modalMode = ModalSessions

	updated, cmd := m.Update(tickMsg(time.Now()))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("tick with a deferred flush returned no cmd; the queue would stall")
	}
	if got := m.queueDepth(); got != 1 {
		t.Fatalf("depth = %d, want 1 (nothing may flush while a modal is open)", got)
	}

	m.modalMode = ModalNone
	updated, cmd = m.Update(tickMsg(time.Now()))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("tick after closing the modal did not flush")
	}
	if msg := drainInbound(t, m); msg.Content != "blocked by modal" {
		t.Fatalf("published %q", msg.Content)
	}
}

func TestClearQueueCommandDropsCurrentSessionOnly(t *testing.T) {
	m := newQueueTestModel()
	m.enqueueMessage("a1")
	m.enqueueMessage("a2")
	m.currentKey = "tui:chat:b"
	m.enqueueMessage("b1")

	m.clearQueue()
	if m.queueDepth() != 0 {
		t.Fatalf("b depth = %d, want 0", m.queueDepth())
	}
	m.currentKey = "tui:chat:a"
	if m.queueDepth() != 2 {
		t.Fatalf("a depth = %d, want 2 (other sessions keep their backlog)", m.queueDepth())
	}
}
func TestClearQueueSlashCommand(t *testing.T) {
	key := "tui:chat:clearq"
	m := newQuietQueueModel(t, key)

	m.enqueueMessage("one")
	m.enqueueMessage("two")
	m.chatInput.SetValue("/clearq")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)

	if got := m.queueDepth(); got != 0 {
		t.Fatalf("depth after /clearq = %d, want 0", got)
	}
	if m.queueFeedback == "" {
		t.Fatal("queueFeedback empty; /clearq should report the dropped count")
	}
	expectNoInbound(t, m)
}

func TestQueueEndToEndRecoversFromStaleProcessingFlag(t *testing.T) {
	key := "tui:chat:queue-stale"
	m := newQuietQueueModel(t, key)

	m.processing = true
	m.startTime = time.Now().Add(-time.Minute)
	m.chatInput.SetValue("stale busy")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)

	if msg := drainInbound(t, m); msg.Content != "stale busy" {
		t.Fatalf("published %q, want %q", msg.Content, "stale busy")
	}
	if got := m.queueDepth(); got != 0 {
		t.Fatalf("depth after the stale-flag flush = %d, want 0", got)
	}
}

func groupQueueModel(t *testing.T, key string) *Model {
	t.Helper()
	m := newQuietQueueModel(t, key)
	m.cfg.Groups.Enabled = true
	m.cfg.Groups.List = []config.GroupProfile{{ID: "alpha", Strategy: "round_robin"}}
	m.agentLoop.UpdateConfigSnapshot(m.cfg.Snapshot())
	m.currentMode = ModeGroup
	m.groupProfileIdx = 0
	return m
}

func TestGroupModeEnterWhileBusyEnqueues(t *testing.T) {
	key := "tui:chat:group-busy"
	m := groupQueueModel(t, key)

	m.processing = true
	m.startTime = time.Now()
	m.chatInput.SetValue("task text")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)

	want := "/group start alpha task text"
	if got := m.queuePreview(); got != want {
		t.Fatalf("queued %q, want the wrapped command %q", got, want)
	}
	if got := m.chatInput.Value(); got != "" {
		t.Fatalf("input = %q, want cleared after enqueue", got)
	}
	expectNoInbound(t, m)

	m.processing = false
	m.startTime = time.Time{}
	updated, cmd := m.Update(tickMsg(time.Now()))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("idle tick did not flush the queue")
	}
	msg := drainInbound(t, m)
	if msg.Content != want {
		t.Fatalf("published %q, want %q", msg.Content, want)
	}
	if msg.SessionKey != key || msg.ChatID != key {
		t.Fatalf("published to %q/%q, want %q", msg.SessionKey, msg.ChatID, key)
	}
}

func TestGroupModeEnterWhileIdleSubmitsDirectly(t *testing.T) {
	key := "tui:chat:group-idle"
	m := groupQueueModel(t, key)

	m.chatInput.SetValue("task text")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)

	msg := drainInbound(t, m)
	if want := "/group start alpha task text"; msg.Content != want {
		t.Fatalf("published %q, want %q", msg.Content, want)
	}
	if got := m.queueDepth(); got != 0 {
		t.Fatalf("depth = %d, want 0 (idle Enter must not queue)", got)
	}
}

func TestGroupModeEnterWhileBusyWithoutProfilesQueuesRaw(t *testing.T) {
	key := "tui:chat:group-noprofile"
	m := newQuietQueueModel(t, key)
	m.currentMode = ModeGroup
	m.groupProfileIdx = 0

	m.processing = true
	m.startTime = time.Now()
	m.chatInput.SetValue("task text")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)

	if got := m.queuePreview(); got != "task text" {
		t.Fatalf("queued %q, want the raw input", got)
	}
	expectNoInbound(t, m)
}

func TestQueueRemoveLastKeybinding(t *testing.T) {
	i18n.InitWithLanguage("en")
	key := "tui:chat:queue-remove"
	m := newQuietQueueModel(t, key)
	m.processing = true
	m.startTime = time.Now()
	m.enqueueMessage("keep")
	m.enqueueMessage("oops")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDelete, Alt: true})
	m = updated.(*Model)

	if got := m.queueDepth(); got != 1 {
		t.Fatalf("depth after removal = %d, want 1", got)
	}
	if got := m.queuePreview(); got != "keep" {
		t.Fatalf("preview = %q, want %q", got, "keep")
	}
	if got, want := m.queueFeedback, i18n.T("tui.queue.removed"); got != want {
		t.Fatalf("queueFeedback = %q, want %q", got, want)
	}
	m.processing = false
	m.startTime = time.Time{}
	updated, cmd := m.Update(tickMsg(time.Now()))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("idle tick did not flush the queue")
	}
	if msg := drainInbound(t, m); msg.Content != "keep" {
		t.Fatalf("published %q, want %q", msg.Content, "keep")
	}
}

func TestQueueRemoveLastIgnoredWithTextOrEmptyQueue(t *testing.T) {
	key := "tui:chat:queue-remove-guard"
	m := newQuietQueueModel(t, key)
	m.processing = true
	m.startTime = time.Now()
	m.enqueueMessage("one")
	m.chatInput.SetValue("draft in progress")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDelete, Alt: true})
	m = updated.(*Model)

	if got := m.queueDepth(); got != 1 {
		t.Fatalf("depth = %d, want 1 (typing composer must block the removal)", got)
	}
	if got := m.chatInput.Value(); got != "draft in progress" {
		t.Fatalf("input = %q, want the draft untouched", got)
	}

	m2 := newQuietQueueModel(t, "tui:chat:queue-remove-empty")
	m2.processing = true
	m2.startTime = time.Now()
	updated, _ = m2.Update(tea.KeyMsg{Type: tea.KeyDelete, Alt: true})
	m2 = updated.(*Model)
	if m2.queueFeedback != "" {
		t.Fatalf("queueFeedback = %q, want empty on an empty queue", m2.queueFeedback)
	}
}

func TestQueueRemoveLastOnlyAffectsCurrentSession(t *testing.T) {
	m := newQueueTestModel()
	m.enqueueMessage("a1")
	m.currentKey = "tui:chat:b"
	m.enqueueMessage("b1")

	m.removeLastQueued()

	if m.queueDepth() != 0 {
		t.Fatalf("b depth = %d, want 0", m.queueDepth())
	}
	m.currentKey = "tui:chat:a"
	if got := m.queuePreview(); got != "a1" {
		t.Fatalf("a preview = %q, want a1 (other sessions keep their backlog)", got)
	}
}

func TestQueueStringsAreLocalized(t *testing.T) {
	keys := []string{
		"tui.queue.full",
		"tui.queue.status",
		"tui.queue.row",
		"tui.queue.removeHint",
		"tui.queue.flushHint",
		"tui.queue.removed",
		"tui.queue.dropped",
		"tui.queue.empty",
	}
	for _, lang := range i18n.AvailableLanguages() {
		i18n.InitWithLanguage(lang)
		for _, key := range keys {
			if got := i18n.T(key); got == key {
				t.Errorf("key %s missing in %s", key, lang)
			}
		}
		m := newQueueTestModel()
		m.enqueueMessage("hello")
		if got, want := m.queueRowLine(200), fmt.Sprintf(i18n.T("tui.queue.row"), 1); !strings.HasPrefix(got, want) {
			t.Errorf("[%s] queue row = %q, want the localized template %q", lang, got, want)
		}
		m.queueFeedback = ""
		for i := 0; i < maxQueuedMessages; i++ {
			m.enqueueMessage("filler")
		}
		m.enqueueMessage("overflow")
		if got, want := m.queueFeedback, fmt.Sprintf(i18n.T("tui.queue.full"), maxQueuedMessages); got != want {
			t.Errorf("[%s] full feedback = %q, want %q", lang, got, want)
		}
		m.queueFeedback = ""
		m.removeLastQueued()
		if got, want := m.queueFeedback, i18n.T("tui.queue.removed"); got != want {
			t.Errorf("[%s] removal feedback = %q, want %q", lang, got, want)
		}
	}
	i18n.InitWithLanguage("en")
}

func TestQueueCapMatchesWebUI(t *testing.T) {
	if maxQueuedMessages != 10 {
		t.Fatalf("maxQueuedMessages = %d, want 10 (WebUI parity)", maxQueuedMessages)
	}
}

func TestPruneQueueToSessions(t *testing.T) {
	m := newQueueTestModel()
	m.enqueueMessageTo("tui:chat:a", "a1")
	m.enqueueMessageTo("tui:chat:gone", "g1")
	m.enqueueMessageTo("tui:chat:sub", "s1")

	m.pruneQueueToSessions([]string{"tui:chat:a", "tui:chat:sub"})

	if got := len(m.messageQueue["tui:chat:a"]); got != 1 {
		t.Fatalf("current session depth = %d, want 1 (never pruned)", got)
	}
	if _, ok := m.messageQueue["tui:chat:gone"]; ok {
		t.Fatal("deleted session kept its backlog")
	}
	if _, ok := m.messageQueue["tui:chat:sub"]; !ok {
		t.Fatal("subagent backlog must survive the prune (live child turn)")
	}
}

func TestReloadSessionsPrunesDeletedSessionQueue(t *testing.T) {
	key := "tui:chat:alive"
	m := newQuietQueueModel(t, key)

	m.enqueueMessageTo(key, "mine")
	m.enqueueMessageTo("tui:chat:deleted", "stale")

	m.reloadSessions()

	if got := len(m.messageQueue[key]); got != 1 {
		t.Fatalf("current session depth = %d, want 1", got)
	}
	if _, ok := m.messageQueue["tui:chat:deleted"]; ok {
		t.Fatal("queue of a session that left the list was not pruned")
	}
}

func TestForceSendNextQueuedWhileIdle(t *testing.T) {
	key := "tui:chat:flush-idle"
	m := newQuietQueueModel(t, key)
	m.enqueueMessage("now")
	m.enqueueMessage("later")

	cmd := m.forceSendNextQueued()
	if cmd == nil {
		t.Fatal("forceSendNextQueued returned nil on a non-empty idle queue")
	}
	msg := drainInbound(t, m)
	if msg.Content != "now" {
		t.Fatalf("published %q, want %q", msg.Content, "now")
	}
	if got := m.queuePreview(); got != "later" {
		t.Fatalf("preview = %q, want the remaining head %q", got, "later")
	}
	if !m.processing {
		t.Fatal("processing not set after force send")
	}
}

func TestForceSendNextQueuedWhileBusy(t *testing.T) {
	i18n.InitWithLanguage("en")
	key := "tui:chat:flush-busy"
	m := newQuietQueueModel(t, key)
	m.processing = true
	m.startTime = time.Now()
	m.enqueueMessage("urgent")
	m.enqueueMessage("queue")

	cmd := m.forceSendNextQueued()
	if cmd != nil {
		t.Fatal("first force-send while busy should confirm, not publish")
	}
	expectNoInbound(t, m)
	if got := m.queuePreview(); got != "urgent" {
		t.Fatalf("queue head after first press = %q, want %q still queued", got, "urgent")
	}
	if got, want := m.queueFeedback, i18n.T("tui.queue.flushConfirm"); got != want {
		t.Fatalf("queueFeedback = %q, want confirm hint %q", got, want)
	}

	cmd = m.forceSendNextQueued()
	if cmd == nil {
		t.Fatal("forceSendNextQueued returned nil after confirming while busy")
	}
	msg := drainInbound(t, m)
	if msg.Content != "urgent" {
		t.Fatalf("published %q, want %q", msg.Content, "urgent")
	}
	if got := m.queuePreview(); got != "queue" {
		t.Fatalf("preview = %q, want %q", got, "queue")
	}
	if got, want := m.queueFeedback, ""; got != want {
		t.Fatalf("queueFeedback = %q, want empty after a successful flush", got)
	}
}

func TestForceSendNextQueuedDefersWhenUIBusy(t *testing.T) {
	key := "tui:chat:flush-ui"
	m := newQuietQueueModel(t, key)
	m.enqueueMessage("wait")
	m.modalMode = ModalSessions
	cmd := m.forceSendNextQueued()
	if cmd == nil {
		t.Fatal("force-send under a modal should arm a retry tick")
	}
	expectNoInbound(t, m)
	if got := m.queuePreview(); got != "wait" {
		t.Fatalf("queue head = %q, want message left queued", got)
	}
}

func TestForceSendNextQueuedEmptyQueue(t *testing.T) {
	i18n.InitWithLanguage("en")
	key := "tui:chat:flush-empty"
	m := newQuietQueueModel(t, key)

	cmd := m.forceSendNextQueued()
	if cmd != nil {
		t.Fatal("forceSendNextQueued returned a cmd on an empty queue")
	}
	expectNoInbound(t, m)
	if got, want := m.queueFeedback, i18n.T("tui.queue.empty"); got != want {
		t.Fatalf("queueFeedback = %q, want %q", got, want)
	}
}

func TestFlushqCommand(t *testing.T) {
	key := "tui:chat:flushq-cmd"
	m := newQuietQueueModel(t, key)
	m.enqueueMessage("via command")

	cmd := m.executeCommand("/flushq")
	if cmd == nil {
		t.Fatal("/flushq returned nil cmd with a pending message")
	}
	if msg := drainInbound(t, m); msg.Content != "via command" {
		t.Fatalf("published %q, want %q", msg.Content, "via command")
	}
}

func TestQueueFlushKeybinding(t *testing.T) {
	key := "tui:chat:flush-key"
	m := newQuietQueueModel(t, key)
	m.enqueueMessage("keyed")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = updated.(*Model)
	if msg := drainInbound(t, m); msg.Content != "keyed" {
		t.Fatalf("published %q, want %q", msg.Content, "keyed")
	}

	m.enqueueMessage("draft-blocked")
	m.chatInput.SetValue("editing")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = updated.(*Model)
	if got := m.queuePreview(); got != "draft-blocked" {
		t.Fatalf("queue head = %q, want the message left queued", got)
	}
	expectNoInbound(t, m)
}
