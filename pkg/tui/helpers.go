package tui

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"

	tea "github.com/charmbracelet/bubbletea"
)

// streamThrottleMsg is sent by tea.Tick to trigger deferred updates.
type streamThrottleMsg struct{}

// throttledUpdateViewport queues a viewport refresh for streaming events.
// Instead of rendering on every chunk, it coalesces updates so at most one
// render happens per streamThrottleInterval.
func (m *Model) throttledUpdateViewport() tea.Cmd {
	if m.streamThrottleActive {
		// A throttle is already scheduled — just mark that a new chunk arrived.
		m.streamPendingUpdate = true
		return nil
	}

	// Render the first chunk immediately for responsiveness!
	m.updateViewport()
	m.streamThrottleActive = true
	m.streamPendingUpdate = false

	return tea.Tick(m.streamThrottleInterval, func(t time.Time) tea.Msg {
		return streamThrottleMsg{}
	})
}

// flushStreamUpdate forces an immediate viewport render.
// Called when streaming ends so the user always sees the complete response immediately.
func (m *Model) flushStreamUpdate() {
	if m.streamPendingUpdate {
		m.streamPendingUpdate = false
		m.updateViewport()
	}
	m.streamThrottleActive = false
}

// completeCmd builds the command that finalizes a streaming turn.
//
// bubbletea executes tea.Cmd functions asynchronously on a separate goroutine
// ("this can be long"), so mutable per-session Model state MUST be captured at
// closure creation time and never read at execution time. Capturing late would
// both race on m.currentKey and misattribute the completion to whatever
// session the user switched to in the meantime (the `msg.sessionKey ==
// m.currentKey` guard in the completeMsg handler would then pass for the wrong
// session, clearing its loading state while the real completion is lost).
func (m *Model) completeCmd() tea.Cmd {
	key := m.currentKey
	return func() tea.Msg {
		return completeMsg{sessionKey: key}
	}
}

func (m *Model) startOutboundListener() tea.Cmd {
	return func() tea.Msg {
		for {
			select {
			case <-m.ctx.Done():
				return nil
			default:
				outMsg, ok := m.agentLoop.MessageBus().SubscribeOutbound(m.ctx)
				if !ok {
					return nil
				}
				return outboundMsg{msg: outMsg}
			}
		}
	}
}

// tickCmd returns a tea.Cmd that schedules the next animation tick.
// It returns nil if a tick is already pending to prevent multiple tick chains
// from accumulating (which causes the spinner to accelerate).
func (m *Model) tickCmd() tea.Cmd {
	if m.tickPending {
		return nil
	}
	m.tickPending = true
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// submitMessage reads the composer, clears it and publishes its content as a
// user turn. The input clear is caller-owned so queued messages (which never
// lived in the composer at flush time) can reuse publishUserMessage directly.
func (m *Model) submitMessage() tea.Cmd {
	content := strings.TrimSpace(m.chatInput.Value())
	if content == "" {
		return nil
	}
	m.chatInput.SetValue("")
	return m.publishUserMessage(content)
}

// publishUserMessage starts a turn with the given content: it sets the local
// processing state, resets streaming fields and publishes the inbound message
// on the bus. Behavior is identical to the old inline submitMessage body
// minus reading/clearing the composer.
func (m *Model) publishUserMessage(content string) tea.Cmd {
	// If we're on the welcome screen with no session, create one now
	if m.currentKey == "" {
		m.createNewChat()
	}
	m.showWelcome = false

	m.compactFeedback = ""
	m.queueFeedback = ""
	m.processing = true
	m.startTime = time.Now()
	m.elapsedTime = 0
	m.currentMessageID = uuid.New().String()
	m.pendingSubagentCompletions = 0
	m.parentCompletionObserved = false
	m.currentStream = ""
	m.currentThinking = ""
	m.currentToolAction = ""

	// Store the user message so it renders immediately in the viewport.
	// The LLM runner will also add it to the session; we clear our copy
	// once we see it appear in the session history (on reloadSessions).
	// TrimSpace matches the normalization applied by the agent's
	// RenderUserMessage before the message is stored in history, so the
	// equality checks in viewport.go/model.go succeed and the pending
	// copy is cleared as soon as the message lands in history.
	m.pendingUserMessage = strings.TrimSpace(content)
	m.reloadSessions()

	m.agentLoop.MessageBus().PublishInbound(bus.InboundMessage{
		Channel:    "native",
		SenderID:   "tui",
		ChatID:     m.currentKey,
		Content:    content,
		SessionKey: m.currentKey,
		Metadata:   map[string]string{"message_id": m.currentMessageID},
	})

	return m.tickCmd()
}

func (m *Model) filterAutocomplete(val string) {
	m.autocompleteItems = nil
	for _, cmd := range allCommands {
		if strings.HasPrefix(cmd.name, val) {
			m.autocompleteItems = append(m.autocompleteItems, cmd)
		}
	}
	if m.autocompleteIdx >= len(m.autocompleteItems) {
		m.autocompleteIdx = 0
	}
}

// getGroupProfiles returns the list of configured group profiles from the
// config snapshot. Returns nil if the snapshot or groups are unavailable.
func (m *Model) getGroupProfiles() []config.GroupProfile {
	snapshot := m.agentLoop.GetProvidable().GetConfigSnapshot()
	if snapshot == nil {
		return nil
	}
	return snapshot.Groups.List
}

// submitGroupStart constructs a /group start command and publishes it to the
// message bus so the backend command handler processes it. It handles session
// creation, UI state cleanup, and returns a tick command for the loading animation.
func (m *Model) submitGroupStart(profileID, task string) tea.Cmd {
	groupCmd := groupStartCommand(profileID, task)

	if m.currentKey == "" {
		m.createNewChat()
	}
	m.showWelcome = false
	m.chatInput.SetValue("")
	m.compactFeedback = ""
	m.processing = true
	m.startTime = time.Now()
	m.elapsedTime = 0
	m.currentMessageID = uuid.New().String()
	m.pendingSubagentCompletions = 0
	m.parentCompletionObserved = false
	m.currentStream = ""
	m.currentThinking = ""
	m.currentToolAction = ""
	m.reloadSessions()

	m.agentLoop.MessageBus().PublishInbound(bus.InboundMessage{
		Channel:    "native",
		SenderID:   "tui",
		ChatID:     m.currentKey,
		Content:    groupCmd,
		SessionKey: m.currentKey,
		Metadata:   map[string]string{"message_id": m.currentMessageID},
	})

	return m.tickCmd()
}

// recordGroupCompleteStatus updates the tracked status of a group when a
// group.complete event arrives.
//
// The backend guarantees exactly one terminal signal pair per group: a terminal
// group.status (done | stopped | error) immediately followed by one
// group.complete — including on failure paths. So group.complete is NOT itself
// proof of success. Preserve the terminal status already recorded instead of
// hardcoding "done", which would mislabel failed or stopped groups as
// successful. Fall back to "done" only when no terminal status was seen yet
// (e.g. a lost/misordered status event or a legacy backend).
func (m *Model) recordGroupCompleteStatus(groupID string) {
	if m.groupStatus == nil {
		m.groupStatus = make(map[string]string)
	}
	switch m.groupStatus[groupID] {
	case "done", "stopped", "error":
		// Keep the terminal status already reported by group.status.
	case "started", "":
		m.groupStatus[groupID] = "done"
	default:
		m.groupStatus[groupID] = "done"
	}
}
