package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// maxQueuedMessages caps the client-side queue so a stuck backend cannot grow
// the list without bound. 10 matches the WebUI queue cap.
const maxQueuedMessages = 10

// queueRetryInterval bounds how often a deferred flush is re-checked while the
// session is idle but the UI cannot accept a turn (modal open, approval
// pending, autocomplete showing). A session that is actually working already
// ticks faster, so this only has to be quick enough to feel instant once the
// blocker clears — without spinning a 10 Hz re-render loop for a modal the user
// left open.
const queueRetryInterval = 500 * time.Millisecond

// queueRetryCmd arms the deferred-flush retry. It reuses tickMsg and the shared
// tickPending latch so a session that is already animating does not get a
// second tick chain, and the existing tick handler stays the single place that
// resets the latch.
func (m *Model) queueRetryCmd() tea.Cmd {
	if m.tickPending {
		return nil
	}
	m.tickPending = true
	return tea.Tick(queueRetryInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// queuedMessage is a message the user submitted while the agent was busy.
// Messages are stored per session key (Model.messageQueue) and flushed FIFO
// once that session is idle.
type queuedMessage struct {
	Content string
}

// enqueueMessage appends content to the current session's FIFO queue. It
// returns false when the queue is already at maxQueuedMessages, and surfaces
// why in queueFeedback so the caller can leave the text where it is instead of
// dropping it silently.
func (m *Model) enqueueMessage(content string) bool {
	return m.enqueueMessageTo(m.currentKey, content)
}

// enqueueMessageTo appends content to the FIFO queue of an explicit session
// key. It exists for callers that must queue into a session other than the one
// on screen; Enter paths use enqueueMessage (current session).
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

// groupStartCommand wraps a group-mode task into the exact string
// submitGroupStart publishes to the bus ("/group start <profileID> <task>").
// Sharing the format between the live submit and the busy-enqueue path is what
// keeps a queued group turn identical to one started while idle.
func groupStartCommand(profileID, task string) string {
	return fmt.Sprintf("/group start %s %s", profileID, task)
}

// enqueueCurrentInputWhileBusy is the busy-time enqueue for the composer
// content: the text is parked in the current session's queue, cleared from the
// composer (so it cannot be edited or duplicated by further typing) and the
// animation tick is armed. It returns the command the Enter handler should
// batch, mirroring enqueueCurrentInput.
//
// The tick matters because the busy flag that routed the message here can be
// stale, and without a live tick chain nothing would ever drain the queue.
// tickCmd() is a no-op when a tick is already pending.
func (m *Model) enqueueCurrentInputWhileBusy(content string) tea.Cmd {
	if !m.enqueueMessage(content) {
		return nil
	}
	m.chatInput.SetValue("")
	return m.tickCmd()
}

// removeLastQueued drops the most recently queued message of the current
// session (LIFO undo of an accidental Enter while busy). It only acts when
// there is a backlog and reports the result through queueFeedback.
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

// pruneQueueToSessions drops queued backlogs for sessions that no longer
// exist, so messages of a deleted chat cannot linger in memory forever or be
// resurrected under a reused key. The current session is always kept — a fresh
// chat is only registered in the session list after its first turn, and
// pruning it would wipe the backlog the user just typed.
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

// enqueueCurrentInput handles Enter while the agent is busy: the composer
// content is parked in the current session's FIFO queue and cleared, so the
// message cannot be edited or duplicated by further typing. A full queue keeps
// the text in the composer and surfaces queueFeedback instead.
//
// Enter with an empty composer attempts an immediate flush instead, which is a
// no-op unless the session really is idle (only the local busy flags were set).
//
// A successful enqueue arms the animation tick: the busy flag that routed the
// message here can be stale (m.processing outlives the turn past its startup
// grace), and without a live tick chain nothing would ever drain the queue.
// tickCmd() is a no-op when a tick is already pending, so a genuinely busy
// session does not get a second chain.
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

// maybeFlushQueue pops the oldest queued message for the current session and
// starts a turn with it, unless the session is busy or the UI cannot accept a
// new turn right now. A deferred flush returns a retry tick so the queue always
// drains eventually, even if the blocking condition clears without another
// queue-aware event.
//
// Returns nil when there is nothing pending or the head was published.
func (m *Model) maybeFlushQueue() tea.Cmd {
	if len(m.messageQueue[m.currentKey]) == 0 {
		return nil
	}
	// An open modal, autocomplete popup or pending approval owns the screen
	// and the input path, so flushing now would interleave a turn with the UI.
	// The slower retry cadence keeps a left-open modal from spinning a re-render
	// loop while its backlog waits.
	if m.modalMode != ModalNone || m.showAutocomplete || m.pendingApprovalID != "" {
		return m.queueRetryCmd()
	}
	// Re-checking busy state here makes every call site safe by construction.
	// isSessionProcessing() already covers m.processing (startup grace + stale
	// reset), running subagents and the backend's own busy flag.
	if m.isSessionProcessing() {
		return m.tickCmd()
	}

	content, ok := m.popQueuedMessage(m.currentKey)
	if !ok {
		return nil
	}
	m.queueFeedback = ""
	// publishUserMessage renders the message in the transcript (pendingUser-
	// Message cache key) and returns the tick chain that drives the next flush
	// when this turn completes.
	return m.publishUserMessage(content)
}

// popQueuedMessage removes and returns the oldest message for the given
// session key. Messages for other keys stay queued untouched.
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

// clearQueue drops all pending messages for the current session (/clearq).
// Other sessions keep their backlog and flush when they become active.
func (m *Model) clearQueue() {
	delete(m.messageQueue, m.currentKey)
	m.queueFeedback = ""
}

// queueDepth returns the number of pending messages for the current session.
func (m *Model) queueDepth() int {
	return len(m.messageQueue[m.currentKey])
}

// queuePreview returns the content of the oldest pending message for the
// current session, or "" when the queue is empty.
func (m *Model) queuePreview() string {
	q := m.messageQueue[m.currentKey]
	if len(q) == 0 {
		return ""
	}
	return q[0].Content
}

// queueRemoveKey is the key that removes the last queued message. It is
// declared here so the handler, the tests and the status-line hint cannot
// drift apart. bubbletea reports it as "alt+delete" (ESC [ 3 ; 3 ~).
const queueRemoveKey = "alt+delete"

// queueStatusLine returns the queue strip text, or "" when there is nothing to
// show. The count is scoped to the session on screen, so switching sessions
// cannot display a stale depth.
//
// available is how many display cells the strip may occupy in the status line
// (the rest is taken by the base status text and the goal badge). The
// remove-key hint is only appended when it fits: view.go clamps the whole line
// by cells, and a hint cut in half is worse than no hint at all. The count
// itself always stays — dropping it would hide pending messages.
func (m *Model) queueStatusLine(available int) string {
	if m.queueFeedback != "" {
		return m.queueFeedback
	}
	n := m.queueDepth()
	if n == 0 {
		return ""
	}
	status := fmt.Sprintf(i18n.T("tui.queue.status"), n)
	hint := fmt.Sprintf(i18n.T("tui.queue.removeHint"), queueRemoveKey)
	if lipgloss.Width(status)+lipgloss.Width(hint) <= available {
		status += hint
	}
	return status
}
