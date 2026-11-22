package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleNormalKey(msg tea.KeyMsg, cmds []tea.Cmd) (*Model, tea.Cmd, bool) {
	if msg.Type == tea.KeyEsc {
		m.lastEscTime = time.Now()
	}
	switch msg.String() {
	case "esc":
		if m.isSessionProcessing() || m.processing || m.hasRunningSubagents() {
			now := time.Now()
			if now.Sub(m.escLastPress) < escHintTimeout {
				m.escPressCount = 0
				m.escHint = false
				m.agentLoop.GetProvidable().StopAgent(m.currentKey)
				m.clearStreamingState()
				m.reloadSessions()
			} else {
				m.escPressCount = 1
				m.escHint = true
			}
			m.escLastPress = now
			return m, nil, true
		}

	case "tab":
		if m.showAutocomplete || m.modalMode != ModalNone {
			return m, nil, true
		}
		m.cycleAgent()
		return m, nil, true

	case "ctrl+c":
		m.cancel()
		return m, tea.Quit, true

	case "ctrl+b":
		if m.parentSessionKey != "" {
			m.setCurrentChatKey(m.parentSessionKey)
			m.parentSessionKey = ""
			m.clearStreamingState()
			m.reloadSessions()
		}
		if m.isSessionProcessing() {
			return m, m.tickCmd(), true
		}
		return m, nil, true

	case "ctrl+p":
		m.showAutocomplete = true
		m.filterAutocomplete("/")
		return m, nil, true

	case "ctrl+m":
		cmd := m.executeCommand("/models")
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...), true

	case "ctrl+a":
		cmd := m.executeCommand("/agents")
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...), true

	case "ctrl+t":
		m.mouseEnabled = !m.mouseEnabled
		if m.mouseEnabled {
			return m, tea.EnableMouseCellMotion, true
		}
		return m, tea.DisableMouse, true

	case "ctrl+s":
		cmd := m.executeCommand("/sessions")
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...), true

	case "ctrl+,":
		cmd := m.executeCommand("/settings")
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...), true

	case "ctrl+y":
		copied := m.copyLastAssistantMessage()
		m.selectionFeedback = copied
		m.selectionFeedbackAt = time.Now()
		if !copied {
			m.selectionFeedback = false
		}
		return m, nil, true

	case queueRemoveKey:
		if strings.TrimSpace(m.chatInput.Value()) == "" {
			m.removeLastQueued()
			return m, nil, true
		}

	case queueFlushKey:
		if strings.TrimSpace(m.chatInput.Value()) == "" && m.queueDepth() > 0 {
			return m, m.forceSendNextQueued(), true
		}

	case "up", "down", "pgup", "pgdown":
		if (msg.String() == "up" || msg.String() == "down") &&
			m.currentMode == ModeGroup && m.showWelcome &&
			!m.showAutocomplete && m.modalMode == ModalNone {
			profiles := m.getGroupProfiles()
			if len(profiles) > 0 {
				if msg.String() == "up" {
					m.groupProfileIdx--
					if m.groupProfileIdx < 0 {
						m.groupProfileIdx = len(profiles) - 1
					}
				} else {
					m.groupProfileIdx++
					if m.groupProfileIdx >= len(profiles) {
						m.groupProfileIdx = 0
					}
				}
				return m, nil, true
			}
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		if msg.String() == "up" || msg.String() == "pgup" {
			m.maybeExpandRenderWindow()
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...), true
	case "home":
		for i := 0; i < maxHomeExpandIterations; i++ {
			if !m.maybeExpandRenderWindow() {
				break
			}
		}
		m.viewport.GotoTop()
		return m, nil, true
	case "end":
		m.viewport.GotoBottom()
		return m, nil, true

	case "enter":
		inputVal := m.chatInput.Value()
		busy := m.isSessionProcessing()

		if m.currentMode == ModeGroup && !strings.HasPrefix(inputVal, "/") &&
			strings.TrimSpace(inputVal) != "" {
			profiles := m.getGroupProfiles()
			if len(profiles) > 0 && m.groupProfileIdx >= 0 && m.groupProfileIdx < len(profiles) {
				if busy {
					wrapped := groupStartCommand(profiles[m.groupProfileIdx].ID, inputVal)
					if cmd := m.enqueueCurrentInputWhileBusy(wrapped); cmd != nil {
						cmds = append(cmds, cmd)
					}
					return m, tea.Batch(cmds...), true
				}
				cmd := m.submitGroupStart(profiles[m.groupProfileIdx].ID, inputVal)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...), true
			}
		}

		if strings.HasPrefix(inputVal, "/") {
			cmd := m.executeCommand(inputVal)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			m.chatInput.SetValue("")
		} else if !busy {
			cmd := m.submitMessage()
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		} else {
			if cmd := m.enqueueCurrentInput(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, tea.Batch(cmds...), false
}
