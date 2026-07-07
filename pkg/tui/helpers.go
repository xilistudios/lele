package tui

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"

	tea "github.com/charmbracelet/bubbletea"
)

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

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *Model) submitMessage() tea.Cmd {
	content := strings.TrimSpace(m.textInput.Value())
	if content == "" {
		return nil
	}

	// If we're on the welcome screen with no session, create one now
	if m.currentKey == "" || m.showWelcome {
		m.createNewChat()
		m.showWelcome = false
	}

	m.textInput.SetValue("")
	m.processing = true
	m.startTime = time.Now()
	m.elapsedTime = 0
	m.currentMessageID = uuid.New().String()
	m.currentStream = ""
	m.currentThinking = ""
	m.currentToolAction = ""

	// Store the user message so it renders immediately in the viewport.
	// The LLM runner will also add it to the session; we clear our copy
	// once we see it appear in the session history (on reloadSessions).
	m.pendingUserMessage = content
	m.reloadSessions()

	m.agentLoop.MessageBus().PublishInbound(bus.InboundMessage{
		Channel:    "native",
		SenderID:   "tui",
		ChatID:     m.currentKey,
		Content:    content,
		SessionKey: m.currentKey,
		Metadata:   map[string]string{"message_id": m.currentMessageID},
	})

	return tickCmd()
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
