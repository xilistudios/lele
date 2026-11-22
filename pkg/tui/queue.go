package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

const maxQueuedMessages = 10

const queueRetryInterval = 500 * time.Millisecond

func (m *Model) queueRetryCmd() tea.Cmd {
	if m.tickPending {
		return nil
	}
	m.tickPending = true
	return tea.Tick(queueRetryInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type queuedMessage struct {
	Content string
}

func (m *Model) enqueueMessage(content string) bool {
	return m.enqueueMessageTo(m.currentKey, content)
}

func (m *Model) enqueueMessageTo(key, content string) bool {
	if len(m.messageQueue[key]) >= maxQueuedMessages {
		m.queueFeedback = fmt.Sprintf(i18n.T("tui.queue.full"), maxQueuedMessages)
		return false
	}
	if m.messageQueue == nil {
		m.messageQueue = make(map[string][]queuedMessage)
	}
	m.messageQueue[key] = append(m.messageQueue[key], queuedMessage{Content: content})
	m.queueFeedback = ""
	return true
}

func groupStartCommand(profileID, task string) string {
	return fmt.Sprintf("/group start %s %s", profileID, task)
}

func (m *Model) enqueueCurrentInputWhileBusy(content string) tea.Cmd {
	if !m.enqueueMessage(content) {
		return nil
	}
	m.chatInput.SetValue("")
	return m.tickCmd()
}

func (m *Model) removeLastQueued() {
	q := m.messageQueue[m.currentKey]
	if len(q) == 0 {
		return
	}
	m.queueFeedback = i18n.T("tui.queue.removed")
	if len(q) == 1 {
		delete(m.messageQueue, m.currentKey)
		return
	}
	m.messageQueue[m.currentKey] = q[:len(q)-1]
}

func (m *Model) pruneQueueToSessions(liveKeys []string) {
	if len(m.messageQueue) == 0 {
		return
	}
	live := make(map[string]struct{}, len(liveKeys))
	for _, k := range liveKeys {
		live[k] = struct{}{}
	}
	for key := range m.messageQueue {
		if key == m.currentKey {
			continue
		}
		if _, ok := live[key]; !ok {
			delete(m.messageQueue, key)
		}
	}
}

func (m *Model) enqueueCurrentInput() tea.Cmd {
	content := strings.TrimSpace(m.chatInput.Value())
	if content == "" {
		return m.maybeFlushQueue()
	}
	if !m.enqueueMessage(content) {
		return nil
	}
	m.chatInput.SetValue("")
	return m.tickCmd()
}

func (m *Model) maybeFlushQueue() tea.Cmd {
	if len(m.messageQueue[m.currentKey]) == 0 {
		return nil
	}
	if m.modalMode != ModalNone || m.showAutocomplete || m.pendingApprovalID != "" {
		return m.queueRetryCmd()
	}
	if m.isSessionProcessing() {
		return m.tickCmd()
	}

	content, ok := m.popQueuedMessage(m.currentKey)
	if !ok {
		return nil
	}
	m.queueFeedback = ""
	return m.publishUserMessage(content)
}

func (m *Model) popQueuedMessage(key string) (string, bool) {
	q := m.messageQueue[key]
	if len(q) == 0 {
		return "", false
	}
	content := q[0].Content
	if len(q) == 1 {
		delete(m.messageQueue, key)
	} else {
		m.messageQueue[key] = q[1:]
	}
	return content, true
}

func (m *Model) forceSendNextQueued() tea.Cmd {
	if m.queueDepth() == 0 {
		m.queueFeedback = i18n.T("tui.queue.empty")
		return nil
	}
	if m.modalMode != ModalNone || m.showAutocomplete || m.pendingApprovalID != "" {
		return m.queueRetryCmd()
	}
	if m.isSessionProcessing() {
		now := time.Now()
		if now.Sub(m.flushLastPress) >= escHintTimeout {
			m.flushLastPress = now
			m.queueFeedback = i18n.T("tui.queue.flushConfirm")
			return nil
		}
		m.flushLastPress = time.Time{}
		if m.agentLoop != nil && m.agentLoop.GetProvidable() != nil {
			m.agentLoop.GetProvidable().StopAgent(m.currentKey)
		}
		m.clearStreamingState()
		m.processing = false
		m.startTime = time.Time{}
	}
	content, ok := m.popQueuedMessage(m.currentKey)
	if !ok {
		return nil
	}
	m.queueFeedback = ""
	return m.publishUserMessage(content)
}

func (m *Model) clearQueue() {
	delete(m.messageQueue, m.currentKey)
	m.queueFeedback = ""
}

func (m *Model) queueDepth() int {
	return len(m.messageQueue[m.currentKey])
}

func (m *Model) queuePreview() string {
	q := m.messageQueue[m.currentKey]
	if len(q) == 0 {
		return ""
	}
	return q[0].Content
}

const queueRemoveKey = "alt+delete"

const queueFlushKey = "alt+enter"

func (m *Model) queueRowLine(available int) string {
	if available < 1 {
		return ""
	}
	if m.queueFeedback != "" {
		return truncateRightCells(m.queueFeedback, available)
	}

	n := m.queueDepth()
	if n == 0 {
		return ""
	}

	prefix := fmt.Sprintf(i18n.T("tui.queue.row"), n)
	budget := available - lipgloss.Width(prefix)
	if budget < 1 {
		return truncateRightCells(strings.TrimRight(prefix, " "), available)
	}

	preview := truncateRightCells(singleLine(sanitizeDisplayText(m.queuePreview())), budget)
	return prefix + preview
}
