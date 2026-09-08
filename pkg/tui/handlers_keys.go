package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleNormalKey processes a keystroke on the main input surface: no modal,
// no pending approval and no active autocomplete. It owns global shortcuts,
// viewport scrolling, mode cycling and message submission/queueing. cmds
// carries commands collected earlier in update() so batching semantics are
// preserved. The bool reports whether a key case consumed the event; an
// unconsumed KeyMsg (no case matched — bare runes, left/right, …) must still
// reach finishUpdate's chatInput forwarding, exactly like the original
// inline switch falling out of the key switch into the post-switch block.
func (m *Model) handleNormalKey(msg tea.KeyMsg, cmds []tea.Cmd) (*Model, tea.Cmd, bool) {
	if msg.Type == tea.KeyEsc {
		m.lastEscTime = time.Now()
	}
	switch msg.String() {
	case "esc":
		if m.isSessionProcessing() || m.processing {
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
			return m, nil, true
		}

	case "tab":
		// Cycle mode: agent -> chat -> [group ->] agent.
		// Only when no autocomplete and no modal is active.
		if m.showAutocomplete || m.modalMode != ModalNone {
			return m, nil, true
		}
		if m.cfg.Groups.Enabled {
			switch m.currentMode {
			case ModeAgent:
				m.currentMode = ModeChat
			case ModeChat:
				m.currentMode = ModeGroup
			case ModeGroup:
				m.currentMode = ModeAgent
			}
		} else {
			// Groups disabled: toggle between agent and chat only
			switch m.currentMode {
			case ModeAgent:
				m.currentMode = ModeChat
			default:
				m.currentMode = ModeAgent
			}
		}
		m.reloadSessions()
		return m, nil, true

	case "ctrl+c":
		m.cancel()
		return m, tea.Quit, true

	case "ctrl+b":
		// Go back to parent chat if currently viewing a subagent session.
		if m.parentSessionKey != "" {
			m.setCurrentChatKey(m.parentSessionKey)
			m.parentSessionKey = ""
			m.clearStreamingState()
			m.reloadSessions()
		}
		// Restart tick animation if the parent session has active subagents.
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
		return m, nil, true

	case "ctrl+a":
		cmd := m.executeCommand("/agents")
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, nil, true

	case "ctrl+t":
		// Toggle mouse capture as fallback for terminals without Shift bypass.
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
		return m, nil, true

	case "ctrl+,":
		cmd := m.executeCommand("/settings")
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, nil, true

	case "ctrl+y":
		m.copyLastAssistantMessage()

	case queueRemoveKey:
		// Undo the last queued message of the current session. Only acts
		// on an empty composer so it can never eat text the user is still
		// typing — with a draft the key keeps its textarea meaning
		// (delete word forward) and is not consumed here.
		if strings.TrimSpace(m.chatInput.Value()) == "" {
			m.removeLastQueued()
			return m, nil, true
		}

	case "up", "down", "pgup", "pgdown":
		// Group mode welcome: cycle profile selection with up/down arrows
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
		m.viewport.GotoTop()
		return m, nil, true
	case "end":
		m.viewport.GotoBottom()
		return m, nil, true

	case "enter":
		inputVal := m.chatInput.Value()
		// The busy gate guards every path that starts a turn: group mode
		// and plain messages fall through to the queue instead of starting
		// a concurrent second turn. Slash commands stay exempt — they run
		// locally in the TUI (see below).
		busy := m.processing || m.hasRunningSubagents()

		// Group mode: wrap non-command input as /group start <profileID> <task>
		if m.currentMode == ModeGroup && !strings.HasPrefix(inputVal, "/") &&
			strings.TrimSpace(inputVal) != "" {
			profiles := m.getGroupProfiles()
			if len(profiles) > 0 && m.groupProfileIdx >= 0 && m.groupProfileIdx < len(profiles) {
				// submitGroupStart publishes immediately, so it must not run
				// while the session is busy. Queue the SAME wrapped command
				// the idle path would have sent — the flushed turn then
				// behaves identically to one started while idle.
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
			// No valid profiles — fall through to normal submitMessage
		}

		if strings.HasPrefix(inputVal, "/") {
			// Slash commands run locally in the TUI (modals, /clearq,
			// /quit, …), so they must stay responsive while the agent is
			// busy — queuing them would defer UI actions that are exactly
			// what the user reaches for during a turn.
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
			// Agent is busy: park the message in the per-session FIFO
			// queue instead of swallowing it. It is auto-submitted when
			// the turn ends (see maybeFlushQueue).
			if cmd := m.enqueueCurrentInput(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, tea.Batch(cmds...), false
}
