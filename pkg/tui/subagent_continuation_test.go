package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/bus"
)

// B1 — the subagent continuation protocol. A subagent.result is delivered
// through the real outbound-listener path and the tests assert the resulting
// client state (continuation flag, processing flag, tick, progress row) and the
// stream-end gate that finalises a backend-initiated continuation turn.
//
// The AM2 amendment (see the plan) is the load-bearing rule here: the backend
// publishes subagent.result on EVERY terminal outcome and stamps
// metadata["resumed"] = "true"|"false" to say whether the result was claimed
// for injection (i.e. a continuation turn is owed). The TUI must delete the
// progress row always, but only arm the turn when resumed == "true".

// setupContinuationModel builds a model on a real, unstarted agent loop with
// the composer focused (so Update does not batch an extra focus command).
func setupContinuationModel(t *testing.T) (*Model, string) {
	t.Helper()
	key := "tui:chat:continuation"
	m := newQuietQueueModel(t, key)
	m.chatInput.Focus()
	return m, key
}

// resumedSubagentResult builds the event the backend publishes when a subagent
// result was claimed for injection (a continuation turn is owed).
func resumedSubagentResult(chatID, taskID string) bus.OutboundMessage {
	return bus.OutboundMessage{
		Channel:  "native",
		ChatID:   chatID,
		Event:    "subagent.result",
		Metadata: map[string]string{"task_id": taskID, "resumed": "true"},
	}
}

// deliverEvent publishes one outbound event and feeds it through the real
// Update path, discarding the returned commands. The batch's last member is the
// re-armed outbound listener, which must never be executed — it blocks until
// the next event.
func deliverEvent(t *testing.T, m *Model, ev bus.OutboundMessage) {
	t.Helper()
	m.agentLoop.MessageBus().PublishOutbound(ev)
	listen := m.startOutboundListener()
	msg := listen()
	om, ok := msg.(outboundMsg)
	if !ok {
		t.Fatalf("outbound listener returned %T, want outboundMsg", msg)
	}
	_, _ = m.Update(om)
}

// streamEndSchedulesCompletion reports whether delivering a stream-end event
// produces a command that yields a completeMsg. It cancels the model context
// first so the re-armed outbound listener — which may be the batch's sole
// member because tea.Batch collapses a one-command batch — returns immediately
// instead of blocking when the returned command is invoked.
func streamEndSchedulesCompletion(t *testing.T, m *Model, ev bus.OutboundMessage) bool {
	t.Helper()
	m.agentLoop.MessageBus().PublishOutbound(ev)
	listen := m.startOutboundListener()
	msg := listen()
	om, ok := msg.(outboundMsg)
	if !ok {
		t.Fatalf("outbound listener returned %T, want outboundMsg", msg)
	}
	_, returned := m.Update(om)
	if returned == nil {
		return false
	}
	m.cancel()
	got := returned()
	batch, ok := got.(tea.BatchMsg)
	if !ok {
		_, isComplete := got.(completeMsg)
		return isComplete
	}
	for _, c := range batch {
		if c == nil {
			continue
		}
		if _, isComplete := c().(completeMsg); isComplete {
			return true
		}
	}
	return false
}

func TestSubagentResult_SetsContinuationPendingOnce(t *testing.T) {
	m, key := setupContinuationModel(t)
	m.processing = false
	m.subagentProgress = map[string]string{"subagent-1": "finalizing…"}

	// Two results for the same task must not accumulate state: the boolean is
	// idempotent, unlike the counter it replaced (which reached 2 and needed two
	// completions to drain).
	deliverEvent(t, m, resumedSubagentResult(key, "subagent-1"))
	deliverEvent(t, m, resumedSubagentResult(key, "subagent-1"))

	if !m.subagentContinuationPending {
		t.Fatal("a resumed subagent.result must set subagentContinuationPending")
	}
	if !m.processing {
		t.Fatal("a resumed subagent.result must mark the session as processing")
	}
	if _, ok := m.subagentProgress["subagent-1"]; ok {
		t.Fatal("subagent.result must delete the task's progress row")
	}

	// A single completion clears the flag and the processing state.
	_, _ = m.Update(completeMsg{sessionKey: key})
	if m.subagentContinuationPending {
		t.Fatal("one completeMsg must clear subagentContinuationPending")
	}
	if m.processing {
		t.Fatal("one completeMsg must clear m.processing")
	}
}

// TestSubagentResult_WaitConsumed_DoesNotArmContinuation is the AM2 mutation
// test: dropping the `resumed == "true"` check in the TUI handler must fail
// here. A result consumed in-turn by wait_for_subagent arrives with
// resumed=false; the row is still dropped, but no turn may be armed.
func TestSubagentResult_WaitConsumed_DoesNotArmContinuation(t *testing.T) {
	m, key := setupContinuationModel(t)
	m.processing = false
	m.subagentProgress = map[string]string{"subagent-1": "working"}

	deliverEvent(t, m, bus.OutboundMessage{
		Channel:  "native",
		ChatID:   key,
		Event:    "subagent.result",
		Metadata: map[string]string{"task_id": "subagent-1", "resumed": "false"},
	})

	if _, ok := m.subagentProgress["subagent-1"]; ok {
		t.Fatal("subagent.result must delete the progress row even when resumed=false")
	}
	if m.subagentContinuationPending {
		t.Fatal("resumed=false must NOT set subagentContinuationPending")
	}
	if m.processing {
		t.Fatal("resumed=false must NOT mark the session as processing")
	}
}

func TestStreamFinished_FiresCompleteForBackendInitiatedTurn(t *testing.T) {
	m, key := setupContinuationModel(t)
	// A backend-initiated continuation carries no id minted on a local submit,
	// so idMatches cannot recognise its stream-end — the flag is the only signal.
	m.processing = false
	m.currentMessageID = "msg-local"
	m.subagentContinuationPending = true

	ok := streamEndSchedulesCompletion(t, m, bus.OutboundMessage{
		Channel:   "native",
		ChatID:    key,
		Event:     "", // stream finished
		MessageID: "backend-turn",
	})
	if !ok {
		t.Fatal("a stream-end for a pending continuation must produce a completeMsg even without a matching id")
	}
}

func TestStreamFinished_NoCompletionWhenIdleAndNoContinuation(t *testing.T) {
	m, key := setupContinuationModel(t)
	m.processing = false
	m.currentMessageID = "msg-local"
	m.subagentContinuationPending = false

	ok := streamEndSchedulesCompletion(t, m, bus.OutboundMessage{
		Channel:   "native",
		ChatID:    key,
		Event:     "",
		MessageID: "unrelated-turn",
	})
	if ok {
		t.Fatal("an unrelated stream-end on an idle session must not finalise a turn")
	}
}

func TestCompleteMsg_ClearsContinuation(t *testing.T) {
	m, key := setupContinuationModel(t)
	m.subagentContinuationPending = true
	m.processing = true
	m.startTime = time.Now().Add(-500 * time.Millisecond)

	_, _ = m.Update(completeMsg{sessionKey: key})

	if m.subagentContinuationPending {
		t.Fatal("completeMsg must clear subagentContinuationPending")
	}
	if m.processing {
		t.Fatal("completeMsg must clear m.processing")
	}
	if m.lastDuration <= 0 {
		t.Fatalf("completeMsg must record lastDuration, got %v", m.lastDuration)
	}
}

func TestSessionSwitch_ClearsContinuationPending(t *testing.T) {
	m, _ := setupContinuationModel(t)
	m.parentSessionKey = "tui:chat:parent"
	m.subagentContinuationPending = true

	// ctrl+b returns to the parent chat: the real switch path that runs
	// setCurrentChatKey + clearStreamingState (where the flag is reset).
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlB})

	if m.currentKey != "tui:chat:parent" {
		t.Fatalf("ctrl+b did not switch sessions, currentKey = %q", m.currentKey)
	}
	if m.subagentContinuationPending {
		t.Fatal("switching sessions must clear subagentContinuationPending")
	}
}

func TestSubagentResult_ArmsTick(t *testing.T) {
	m, key := setupContinuationModel(t)
	m.tickPending = false
	m.processing = false
	m.startTime = time.Time{}

	deliverEvent(t, m, resumedSubagentResult(key, "subagent-1"))

	if !m.tickPending {
		t.Fatal("a resumed subagent.result must arm the animation tick")
	}
	if m.startTime.IsZero() {
		t.Fatal("a resumed subagent.result must set startTime so the startup grace keeps the spinner alive")
	}
}
