package tui

import (
	"context"
	"github.com/charmbracelet/bubbles/textarea"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
)

// drainInbound reads one inbound message from the bus, failing the test when
// none arrives within the timeout.
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

// expectNoInbound asserts that nothing is published to the bus for a moment.
func expectNoInbound(t *testing.T, m *Model) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if msg, ok := m.agentLoop.MessageBus().ConsumeInbound(ctx); ok {
		t.Fatalf("unexpected inbound message published: %q", msg.Content)
	}
}

// newQuietQueueModel builds a Model around a real (but deliberately not
// started) agent loop: nothing consumes the inbound bus besides the test, so
// every published turn can be observed deterministically.
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

// --- pure queue logic -------------------------------------------------------

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

	// Popping b must leave a untouched.
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

func TestQueueStatusLine(t *testing.T) {
	m := newQueueTestModel()
	if got := m.queueStatusLine(); got != "" {
		t.Fatalf("empty queue status = %q, want empty", got)
	}
	m.enqueueMessage("hello")
	if got := m.queueStatusLine(); !strings.Contains(got, "1") {
		t.Fatalf("status line %q does not mention the pending count", got)
	}
	// A feedback message (e.g. queue full) takes precedence over the count.
	m.queueFeedback = "queue full"
	if got := m.queueStatusLine(); got != "queue full" {
		t.Fatalf("status line = %q, want the feedback text", got)
	}
}

// --- busy Enter path --------------------------------------------------------

func TestEnterWhileBusyEnqueuesAndClearsInput(t *testing.T) {
	m := newQueueTestModel()
	m.chatInput.SetValue("  queued text  ")
	m.processing = true // busy agent

	// The tick arms the retry path: m.processing can be stale, and without a
	// live tick chain a queued message would never drain.
	if cmd := m.enqueueCurrentInput(); cmd == nil {
		t.Fatal("enqueue returned no cmd; the queue needs a retry tick")
	}
	if got := m.queuePreview(); got != "queued text" {
		t.Fatalf("queued %q, want %q", got, "queued text")
	}
	// The composer must be cleared so the parked message cannot be corrupted
	// by further typing.
	if got := m.chatInput.Value(); got != "" {
		t.Fatalf("input value after enqueue = %q, want empty", got)
	}
}

func TestEnterWhileBusyWithEmptyInputDoesNotQueue(t *testing.T) {
	m := newQueueTestModel()
	m.processing = true

	// Enter with an empty composer must not create a phantom queue entry.
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

// --- flush guards -----------------------------------------------------------

func TestFlushQueueDeferredWhileBusy(t *testing.T) {
	m := newQueueTestModel()
	m.enqueueMessage("pending")
	m.processing = true
	m.startTime = time.Now() // inside the startup-grace window

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

// --- end-to-end through Update() -------------------------------------------

func TestQueueEndToEndEnqueueThenFlushOnIdle(t *testing.T) {
	key := "tui:chat:queue-e2e"
	m := newQuietQueueModel(t, key)

	// Simulate a live turn: Enter must park the message instead of sending it.
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

	// Turn ends and the session is idle: the next tick flushes FIFO.
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
	// The flush must render the message once (as a pending user message) and
	// restart the busy state so the next turn cannot interleave.
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

	// Only one message may be in flight per turn: the flush must pop the head
	// and leave the rest queued for the following turn.
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

	// Flush starts a turn (processing = true).
	updated, cmd := m.Update(tickMsg(time.Now()))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("flush returned no cmd")
	}
	drainInbound(t, m)

	// While that turn is live, Enter must queue rather than publish a second
	// inbound message that the backend would have to interleave.
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

	// Closing the modal lets a later tick drain it.
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
	// The command reports what it dropped and must not send anything inbound.
	if m.queueFeedback == "" {
		t.Fatal("queueFeedback empty; /clearq should report the dropped count")
	}
	expectNoInbound(t, m)
}

// The busy flag that routes Enter into the queue can be stale: m.processing
// stays true after a turn ends until isSessionProcessing() resets it past its
// startup grace. A queued message must never be stranded, so the flush path
// re-checks the real state and sends it as soon as the stale flag is cleared.
func TestQueueEndToEndRecoversFromStaleProcessingFlag(t *testing.T) {
	key := "tui:chat:queue-stale"
	m := newQuietQueueModel(t, key)

	m.processing = true
	m.startTime = time.Now().Add(-time.Minute) // grace window long over
	m.chatInput.SetValue("stale busy")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)

	// Either the flush already ran, or a tick chain is armed to run it — the
	// queue may not simply sit there with nobody watching it.
	if msg := drainInbound(t, m); msg.Content != "stale busy" {
		t.Fatalf("published %q, want %q", msg.Content, "stale busy")
	}
	if got := m.queueDepth(); got != 0 {
		t.Fatalf("depth after the stale-flag flush = %d, want 0", got)
	}
}
