package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/bus"
)

func setupContinuationModel(t *testing.T) (*Model, string) {
	t.Helper()
	key := "tui:chat:continuation"
	m := newQuietQueueModel(t, key)
	m.chatInput.Focus()
	return m, key
}

func resumedSubagentResult(chatID, taskID string) bus.OutboundMessage {
	return bus.OutboundMessage{
		Channel:  "native",
		ChatID:   chatID,
		Event:    "subagent.result",
		Metadata: map[string]string{"task_id": taskID, "resumed": "true"},
	}
}

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

	_, _ = m.Update(completeMsg{sessionKey: key})
	if m.subagentContinuationPending {
		t.Fatal("one completeMsg must clear subagentContinuationPending")
	}
	if m.processing {
		t.Fatal("one completeMsg must clear m.processing")
	}
}

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
	m.processing = false
	m.currentMessageID = "msg-local"
	m.subagentContinuationPending = true

	ok := streamEndSchedulesCompletion(t, m, bus.OutboundMessage{
		Channel:   "native",
		ChatID:    key,
		Event:     "",
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

func TestSubagentResult_InjectedMidTurn_DoesNotArmContinuation(t *testing.T) {
	m, key := setupContinuationModel(t)
	m.processing = false
	m.startTime = time.Time{}
	m.tickPending = false
	m.subagentProgress = map[string]string{"subagent-1": "working"}

	deliverEvent(t, m, bus.OutboundMessage{
		Channel: "native",
		ChatID:  key,
		Event:   "subagent.result",
		Metadata: map[string]string{
			"task_id":  "subagent-1",
			"resumed":  "true",
			"injected": "true",
		},
	})

	if _, ok := m.subagentProgress["subagent-1"]; ok {
		t.Fatal("subagent.result must delete the progress row even when injected mid-turn")
	}
	if m.subagentContinuationPending {
		t.Fatal("injected=true must NOT set subagentContinuationPending: the running turn clears loading")
	}
	if m.processing {
		t.Fatal("injected=true must NOT start a processing state")
	}
	if m.tickPending {
		t.Fatal("injected=true must NOT arm the animation tick: the running turn already drives it")
	}
}

func TestSubagentResult_ResumedWithoutInjected_StillArmsContinuation(t *testing.T) {
	m, key := setupContinuationModel(t)
	m.processing = false
	m.subagentProgress = map[string]string{"subagent-1": "working"}

	deliverEvent(t, m, bus.OutboundMessage{
		Channel:  "native",
		ChatID:   key,
		Event:    "subagent.result",
		Metadata: map[string]string{"task_id": "subagent-1", "resumed": "true", "injected": "false"},
	})

	if !m.subagentContinuationPending {
		t.Fatal("resumed=true without injected must still set subagentContinuationPending")
	}
	if !m.processing {
		t.Fatal("resumed=true without injected must mark the session as processing")
	}
	if _, ok := m.subagentProgress["subagent-1"]; ok {
		t.Fatal("subagent.result must delete the task's progress row")
	}
}
