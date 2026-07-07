package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.modalMode != ModalNone {
			switch msg.String() {
			case "up", "k":
				if m.modalSelectedIdx > 0 {
					m.modalSelectedIdx--
					if m.modalSelectedIdx < m.modalScrollOffset {
						m.modalScrollOffset = m.modalSelectedIdx
					}
				}
			case "down", "j":
				if m.modalSelectedIdx < len(m.modalItems)-1 {
					m.modalSelectedIdx++
					maxVisible := m.height - 8 // title + borders + padding
					if maxVisible < 3 {
						maxVisible = 3
					}
					if m.modalSelectedIdx >= m.modalScrollOffset+maxVisible {
						m.modalScrollOffset = m.modalSelectedIdx - maxVisible + 1
					}
				}
			case "enter":
				if len(m.modalItems) > 0 {
					selectedVal := m.modalItems[m.modalSelectedIdx]
					if m.modalMode == ModalAgent {
						if m.showWelcome && m.currentKey == "" {
							m.pendingAgent = selectedVal
						} else {
							m.agentLoop.GetProvidable().SetSessionAgent(m.currentKey, selectedVal)
						}
					} else if m.modalMode == ModalModel {
						if m.showWelcome && m.currentKey == "" {
							m.pendingModel = selectedVal
						} else {
							m.agentLoop.GetProvidable().SetSessionModel(m.currentKey, selectedVal)
						}
					} else if m.modalMode == ModalThink {
						if m.showWelcome && m.currentKey == "" {
							m.pendingThink = selectedVal
						} else {
							m.agentLoop.GetProvidable().SetThinkLevel(m.currentKey, selectedVal)
						}
					} else if m.modalMode == ModalSessions {
						if m.modalSelectedIdx < len(m.modalSessionKeys) {
							m.currentKey = m.modalSessionKeys[m.modalSelectedIdx]
							m.showWelcome = false
							m.clearStreamingState()
						}
					} else if m.modalMode == ModalSubagents {
						if m.modalSelectedIdx < len(m.modalSubagentKeys) {
							// Remember the parent chat so the user can navigate back (ctrl+b)
							m.parentSessionKey = m.currentKey
							m.currentKey = m.modalSubagentKeys[m.modalSelectedIdx]
							m.showWelcome = false
							m.clearStreamingState()
						}
					} else if m.modalMode == ModalLang {
						// Extract language code from "Name (code)" format
						langCode := selectedVal
						if idx := strings.LastIndex(selectedVal, "("); idx != -1 {
							langCode = strings.TrimRight(selectedVal[idx+1:], ")")
						}
						m.cfg.SetLanguage(langCode)
						i18n.SetLanguage(langCode)
						m.textInput.Placeholder = i18n.T("tui.placeholder")
					}
					m.modalMode = ModalNone
					m.reloadSessions()
				}
			case "esc", "q":
				m.modalMode = ModalNone
			}
			// Restart tick animation if the target session is actively processing.
			// This keeps the loading dots when switching to a busy session/subagent.
			if m.processing || m.hasRunningSubagents() {
				return m, tickCmd()
			}
			return m, nil
		}

		if m.showAutocomplete {
			switch msg.String() {
			case "up", "ctrl+k":
				if m.autocompleteIdx > 0 {
					m.autocompleteIdx--
				} else {
					m.autocompleteIdx = len(m.autocompleteItems) - 1
				}
				return m, nil
			case "down", "ctrl+j":
				if m.autocompleteIdx < len(m.autocompleteItems)-1 {
					m.autocompleteIdx++
				} else {
					m.autocompleteIdx = 0
				}
				return m, nil
			case "tab", "enter":
				if len(m.autocompleteItems) > 0 {
					completed := m.autocompleteItems[m.autocompleteIdx].name
					m.textInput.SetValue(completed)
					m.showAutocomplete = false
					if msg.String() == "enter" {
						m.executeCommand(completed)
						m.textInput.SetValue("")
					}
				}
				return m, nil
			case "esc":
				m.showAutocomplete = false
				return m, nil
			}
		}

		switch msg.String() {
		case "esc":
			if m.processing || m.hasRunningSubagents() {
				now := time.Now()
				if now.Sub(m.escLastPress) < 1*time.Second {
					// Double press detected - cancel the agent
					m.escPressCount = 0
					m.escHint = false
					m.agentLoop.GetProvidable().StopAgent(m.currentKey)
					m.clearStreamingState()
					m.reloadSessions()
				} else {
					// First press - show hint
					m.escPressCount = 1
					m.escHint = true
				}
				m.escLastPress = now
			}

		case "ctrl+c":
			m.printSessionSummary()
			m.cancel()
			return m, tea.Quit

		case "ctrl+b":
			// Go back to parent chat if currently viewing a subagent session.
			if m.parentSessionKey != "" {
				m.currentKey = m.parentSessionKey
				m.parentSessionKey = ""
				m.clearStreamingState()
				m.reloadSessions()
			}
			// Restart tick animation if the parent session has active subagents.
			if m.processing || m.hasRunningSubagents() {
				return m, tickCmd()
			}
			return m, nil

		case "ctrl+p":
			m.showAutocomplete = true
			m.filterAutocomplete("/")
			return m, nil

		case "ctrl+m":
			m.executeCommand("/models")
			return m, nil

		case "ctrl+a":
			m.executeCommand("/agents")
			return m, nil

		case "ctrl+s":
			m.executeCommand("/sessions")
			return m, nil

		case "up", "down":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)

		case "enter":
			inputVal := m.textInput.Value()
			if strings.HasPrefix(inputVal, "/") {
				m.executeCommand(inputVal)
				m.textInput.SetValue("")
			} else if !m.processing && !m.hasRunningSubagents() {
				cmd := m.submitMessage()
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}

	case tickMsg:
		if m.processing || m.hasRunningSubagents() {
			m.elapsedTime = time.Since(m.startTime)
			m.animationTick++
			cmds = append(cmds, tickCmd())
		}
		if m.escHint && time.Since(m.escLastPress) > escHintTimeout {
			m.escHint = false
			m.escPressCount = 0
		}

	case outboundMsg:
		if msg.msg.ChatID == m.currentKey {
			switch msg.msg.Event {
			case "message.stream":
				// Clear pending user message on first stream chunk — by the time
				// the LLM starts streaming, the user message is already in history.
				if m.pendingUserMessage != "" {
					m.pendingUserMessage = ""
					m.renderedBaseKey = "" // invalidate cache so history re-renders without duplicate
				}
				m.currentToolAction = "" // streaming text means tool call is done
				// Only reset streaming state if the MessageID is truly different
				// and we have existing content (avoids resetting on first chunk)
				if msg.msg.MessageID != "" &&
					msg.msg.MessageID != m.currentAssistantMsgID &&
					m.currentAssistantMsgID != "" {
					m.currentAssistantMsgID = msg.msg.MessageID
					m.currentStream = ""
					m.currentThinking = ""
				}
				m.currentStream += msg.msg.Content
				m.updateViewport()
			case "message.thinking":
				// Only reset streaming state if the MessageID is truly different
				// and we have existing content (avoids resetting on first chunk)
				if msg.msg.MessageID != "" &&
					msg.msg.MessageID != m.currentAssistantMsgID &&
					m.currentAssistantMsgID != "" {
					m.currentAssistantMsgID = msg.msg.MessageID
					m.currentStream = ""
					m.currentThinking = ""
				}
				m.currentThinking += msg.msg.Content
				m.updateViewport()
			case "tool.executing":
				// Clear streaming state since we are now executing a tool
				m.currentStream = ""
				m.currentThinking = ""
				m.currentAssistantMsgID = ""

				// Show the currently executing tool call in the viewport.
				// Use the compact "tool: action" format from metadata.
				toolName := msg.msg.Metadata["tool"]
				action := msg.msg.Metadata["action"]
				if action != "" {
					m.currentToolAction = action
				} else if toolName != "" {
					m.currentToolAction = toolName
				}
				m.updateViewport()
			case "tool.result":
				// Tool completed; clear the active tool action display.
				m.currentToolAction = ""
				// When a spawn tool completes, clear its subagent progress entry.
				if msg.msg.Metadata["tool"] == "spawn" {
					if saKey := msg.msg.Metadata["subagent_session_key"]; saKey != "" {
						// Extract the task ID suffix (e.g. "subagent-1") from the session key
						if idx := strings.LastIndex(saKey, ":"); idx >= 0 {
							delete(m.subagentProgress, saKey[idx+1:])
						}
					}
				}
				m.updateViewport()
			case "":
				if m.processing && (msg.msg.MessageID == m.currentMessageID || msg.msg.ReplyTo == m.currentMessageID) {
					cmds = append(cmds, func() tea.Msg {
						return completeMsg{sessionKey: m.currentKey}
					})
				}
			}
		} else if m.isSubagentOfCurrentChat(msg.msg.ChatID) {
			// Events from subagents spawned by the current parent chat.
			// Capture their tool.executing / tool.result events to show
			// real-time progress in the parent viewport.
			taskID := m.currentSubagentTaskID(msg.msg.ChatID)
			if taskID != "" {
				switch msg.msg.Event {
				case "tool.executing":
					action := msg.msg.Metadata["action"]
					if action == "" {
						action = msg.msg.Metadata["tool"]
					}
					if action != "" {
						if m.subagentProgress == nil {
							m.subagentProgress = make(map[string]string)
						}
						m.subagentProgress[taskID] = action
						m.updateViewport()
					}
				case "message.stream":
					// Subagent is streaming a final response — update progress label.
					if m.subagentProgress == nil {
						m.subagentProgress = make(map[string]string)
					}
					m.subagentProgress[taskID] = "finalizing…"
					m.updateViewport()
				}
			}
		}
		cmds = append(cmds, m.startOutboundListener())

	case completeMsg:
		if msg.sessionKey == m.currentKey {
			m.lastDuration = time.Since(m.startTime)
			m.clearStreamingState()
			m.reloadSessions()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = int(float64(m.width)*0.78) - 4
		m.viewport.Height = m.height - 8
		m.textInput.Width = int(float64(m.width)*0.78) - 4
		m.updateViewport()
	}

	if m.modalMode == ModalNone {
		// Only forward message types the textinput knows how to handle.
		// Custom TUI messages (outboundMsg, tickMsg, completeMsg) must be
		// excluded to prevent garbage characters in the input field.
		switch msg.(type) {
		case outboundMsg, completeMsg, tickMsg:
			// skip — not relevant to textinput
		default:
			var cmd tea.Cmd
			m.textInput, cmd = m.textInput.Update(msg)
			cmds = append(cmds, cmd)
		}

		val := m.textInput.Value()
		if strings.HasPrefix(val, "/") {
			m.showAutocomplete = true
			m.filterAutocomplete(val)
		} else {
			m.showAutocomplete = false
		}
	}

	return m, tea.Batch(cmds...)
}

// resetModal resets the modal state for a new modal.
func (m *Model) resetModal(mode modalType) {
	m.modalMode = mode
	m.modalScrollOffset = 0
	m.modalItems = nil
	m.modalSessionKeys = nil
	m.modalSubagentKeys = nil
	m.modalSelectedIdx = 0
}
