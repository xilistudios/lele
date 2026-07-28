package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// sgrMouseEscapeRe matches complete SGR mouse escape sequences:
// ESC [ < col ; row ; button M/m
var sgrMouseEscapeRe = regexp.MustCompile(`(?:\x1b)?\[<\d+;\d+;\d+[Mm]`)

// stripMouseEscapeSequences removes all SGR mouse sequences from s.
func stripMouseEscapeSequences(s string) string {
	return sgrMouseEscapeRe.ReplaceAllString(s, "")
}

// filterAndBufferEscapes combines any previously buffered incomplete escape
// fragment (m.escBuffer) with msg.Runes, strips complete SGR mouse escape
// sequences, stores any trailing incomplete escape fragment back into
// m.escBuffer, and returns the cleaned runes plus a boolean indicating
// whether the message should be forwarded to the text input.
func filterAndBufferEscapes(m *Model, msg tea.KeyMsg) ([]rune, bool) {
	// Combine buffer with incoming runes.
	runes := make([]rune, 0, len(m.escBuffer)+len(msg.Runes))
	runes = append(runes, m.escBuffer...)
	runes = append(runes, msg.Runes...)
	m.escBuffer = m.escBuffer[:0]

	s := string(runes)
	cleaned := stripMouseEscapeSequences(s)

	// Check if the cleaned string ends with an incomplete escape fragment
	// (e.g. lone \x1b, \x1b[, \x1b[<, \x1b[<12, etc.) that might be the
	// start of a sequence split across multiple KeyMsg events.
	// We look for ESC at the end followed by optional incomplete CSI bytes.
	incomplete := findTrailingIncompleteEscape(cleaned)
	if incomplete > 0 {
		// Buffer the incomplete tail for next time.
		m.escBuffer = []rune(cleaned[len(cleaned)-incomplete:])
		cleaned = cleaned[:len(cleaned)-incomplete]
	}

	if len(cleaned) == 0 {
		return nil, false // nothing to pass through
	}

	return []rune(cleaned), true
}

// findTrailingIncompleteEscape returns the number of trailing runes that look
// like the start of an incomplete SGR mouse escape sequence (ESC + partial
// CSI, or a headless "[<" partial SGR mouse fragment without the leading ESC).
// Returns 0 if the string doesn't end with such a fragment.
func findTrailingIncompleteEscape(s string) int {
	if len(s) == 0 {
		return 0
	}
	runes := []rune(s)
	n := len(runes)

	// Walk backwards to find the last ESC character.
	for i := n - 1; i >= 0; i-- {
		if runes[i] == 0x1b {
			// Everything from here to the end could be an incomplete sequence.
			fragment := string(runes[i:])
			tail := fragment[1:] // strip leading ESC

			// If the tail is empty (just a lone ESC), it's incomplete.
			if len(tail) == 0 {
				return n - i
			}
			// If it starts with [ or [<  followed by digits/; but no final byte, it's incomplete.
			if strings.HasPrefix(tail, "[<") {
				rest := tail[2:]
				if len(rest) == 0 {
					return n - i // just "\x1b[<"
				}
				// All remaining chars should be digits or ';' for an in-progress sequence.
				allParams := true
				for _, c := range rest {
					if !((c >= '0' && c <= '9') || c == ';') {
						allParams = false
						break
					}
				}
				if allParams {
					return n - i // incomplete: "\x1b[<NNN;NNN"
				}
			} else if tail == "[" {
				return n - i // just "\x1b["
			}
			// Otherwise it's a complete or unrelated sequence — don't buffer.
			return 0
		}
	}

	// No ESC found. Bubbletea may have consumed the ESC byte, leaving a
	// headless partial SGR mouse fragment like "[<", "[<65", or "[<65;57;25".
	// Buffer it if the string ends with "[<" followed only by digits/';'.
	// Scan backwards from the last '[' we can find.
	for i := n - 1; i >= 0; i-- {
		if runes[i] == '[' {
			// Must be followed by '<' (if there's room) to be an SGR mouse fragment.
			if i+1 >= n {
				// Trailing "[" alone — not a mouse fragment, ignore.
				return 0
			}
			if runes[i+1] != '<' {
				return 0
			}
			// "[<" present — check that everything after is digits/';'.
			rest := string(runes[i+2:])
			if len(rest) == 0 {
				return n - i // just "[<"
			}
			allParams := true
			for _, c := range rest {
				if !((c >= '0' && c <= '9') || c == ';') {
					allParams = false
					break
				}
			}
			if allParams {
				return n - i // incomplete headless SGR mouse fragment
			}
			// Contains non-param chars — not a mouse fragment.
			return 0
		}
	}

	return 0
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.modalMode != ModalNone {
			switch msg.String() {
			case "up", "k":
				if isListModal(m.modalMode) && m.modalSelectedIdx > 0 {
					m.modalSelectedIdx--
					if m.modalSelectedIdx < m.modalScrollOffset {
						m.modalScrollOffset = m.modalSelectedIdx
					}
				}
			case "down", "j":
				if isListModal(m.modalMode) && m.modalSelectedIdx < len(m.modalItems)-1 {
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
						if m.showWelcome {
							m.pendingAgent = selectedVal
						}
						if m.currentKey != "" {
							m.agentLoop.GetProvidable().SetSessionAgent(m.currentKey, selectedVal)
						}
					} else if m.modalMode == ModalModel {
						if m.showWelcome {
							m.pendingModel = selectedVal
						}
						if m.currentKey != "" {
							m.agentLoop.GetProvidable().SetSessionModel(m.currentKey, selectedVal)
						}
					} else if m.modalMode == ModalThink {
						if m.showWelcome {
							m.pendingThink = selectedVal
						}
						if m.currentKey != "" {
							m.agentLoop.GetProvidable().SetThinkLevel(m.currentKey, selectedVal)
						}
					} else if m.modalMode == ModalSessions {
						if m.modalSelectedIdx < len(m.modalSessionKeys) {
							m.currentKey = m.modalSessionKeys[m.modalSelectedIdx]
							m.showWelcome = false
							m.clearStreamingState()
							// Sync pendingModel to the target session's model so
							// that creating a new chat from here inherits it.
							m.pendingModel = m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
						}
					} else if m.modalMode == ModalSubagents {
						if m.modalSelectedIdx < len(m.modalSubagentKeys) {
							// Remember the parent chat so the user can navigate back (ctrl+b)
							if m.parentSessionKey == "" {
								m.parentSessionKey = m.currentKey
							}
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
						m.chatInput.Placeholder = i18n.T("tui.placeholder")
					} else if m.modalMode == ModalProviders {
						if m.modalSelectedIdx < len(m.providerModalKeys) {
							providerName := m.providerModalKeys[m.modalSelectedIdx]
							m.providerSelectedName = providerName
							m.modalMode = ModalProviderDetail
							// Build detail view items
							m.modalItems = nil
							m.modalSelectedIdx = 0
							m.modalScrollOffset = 0
							// Show provider info
							snapshot := m.agentLoop.GetProvidable().GetConfigSnapshot()
							if snapshot != nil && snapshot.Providers != nil {
								if p, ok := snapshot.Providers.GetNamed(providerName); ok {
									m.modalItems = append(m.modalItems, fmt.Sprintf("Type: %s", p.Type))
									m.modalItems = append(m.modalItems, fmt.Sprintf("API Base: %s", p.APIBase))
									keyDisplay := p.APIKey
									if len(keyDisplay) > 8 {
										keyDisplay = keyDisplay[:4] + "..." + keyDisplay[len(keyDisplay)-4:]
									}
									m.modalItems = append(m.modalItems, fmt.Sprintf("API Key: %s", keyDisplay))
								}
							}
							m.modalItems = append(m.modalItems, "---")
							// List models
							models := m.listProviderModels(providerName)
							for _, alias := range models {
								m.modalItems = append(m.modalItems, fmt.Sprintf("  %s", alias))
							}
							if len(models) == 0 {
								m.modalItems = append(m.modalItems, "  (no models)")
							}
							m.modalItems = append(m.modalItems, "---")
							m.modalItems = append(m.modalItems, "+ Add model")
							m.modalItems = append(m.modalItems, "- Delete provider")
						}
						return m, nil
					} else if m.modalMode == ModalProviderDetail {
						if m.modalSelectedIdx < len(m.modalItems) {
							selectedItem := m.modalItems[m.modalSelectedIdx]
							if selectedItem == "+ Add model" {
								m.modalMode = ModalAddModel
								m.formStepIndex = 0
								m.formValues = make([]string, 5)
								m.formError = ""
								m.formConfirmMode = false
								m.textInput.SetValue("")
								m.textInput.Placeholder = "Model alias (e.g. gpt-4o)"
								return m, nil
							} else if selectedItem == "- Delete provider" {
								if err := m.deleteProvider(m.providerSelectedName); err != nil {
									m.formError = err.Error()
									return m, nil
								}
								m.modalMode = ModalNone
								return m, nil
							} else if strings.HasPrefix(selectedItem, "  ") && !strings.HasPrefix(selectedItem, "  (") {
								// Model entry — delete it
								modelAlias := strings.TrimSpace(selectedItem)
								if err := m.deleteModelFromProvider(m.providerSelectedName, modelAlias); err != nil {
									m.formError = err.Error()
									return m, nil
								}
								// Refresh detail view
								providerName := m.providerSelectedName
								m.modalItems = nil
								m.modalSelectedIdx = 0
								m.modalScrollOffset = 0
								snapshot := m.agentLoop.GetProvidable().GetConfigSnapshot()
								if snapshot != nil && snapshot.Providers != nil {
									if p, ok := snapshot.Providers.GetNamed(providerName); ok {
										m.modalItems = append(m.modalItems, fmt.Sprintf("Type: %s", p.Type))
										m.modalItems = append(m.modalItems, fmt.Sprintf("API Base: %s", p.APIBase))
										keyDisplay := p.APIKey
										if len(keyDisplay) > 8 {
											keyDisplay = keyDisplay[:4] + "..." + keyDisplay[len(keyDisplay)-4:]
										}
										m.modalItems = append(m.modalItems, fmt.Sprintf("API Key: %s", keyDisplay))
									}
								}
								m.modalItems = append(m.modalItems, "---")
								models := m.listProviderModels(providerName)
								for _, alias := range models {
									m.modalItems = append(m.modalItems, fmt.Sprintf("  %s", alias))
								}
								if len(models) == 0 {
									m.modalItems = append(m.modalItems, "  (no models)")
								}
								m.modalItems = append(m.modalItems, "---")
								m.modalItems = append(m.modalItems, "+ Add model")
								m.modalItems = append(m.modalItems, "- Delete provider")
								return m, nil
							}
						}
						return m, nil
					} else if m.modalMode == ModalAddProvider {
						// Form-based modal: validate and advance steps
						val := strings.TrimSpace(m.textInput.Value())
						if val == "" {
							m.formError = "This field is required"
							return m, nil
						}
						m.formError = ""
						m.formValues[m.formStepIndex] = val
						if m.formStepIndex >= 3 {
							// Last step — save provider
							if err := m.addProvider(m.formValues[0], m.formValues[1], m.formValues[2], m.formValues[3]); err != nil {
								m.formError = err.Error()
								return m, nil
							}
							m.modalMode = ModalNone
							return m, nil
						}
						// Advance to next step
						m.formStepIndex++
						m.textInput.SetValue("")
						switch m.formStepIndex {
						case 1:
							m.textInput.Placeholder = "Provider type (e.g. openai, anthropic, openrouter)"
						case 2:
							m.textInput.Placeholder = "API Key"
						case 3:
							m.textInput.Placeholder = "API Base URL (e.g. https://api.openai.com/v1)"
						}
						return m, nil
					} else if m.modalMode == ModalAddModel {
						// Form-based modal: validate and advance steps
						val := strings.TrimSpace(m.textInput.Value())
						if val == "" {
							m.formError = "This field is required"
							return m, nil
						}
						m.formError = ""
						m.formValues[m.formStepIndex] = val
						if m.formStepIndex >= 4 {
							// Last step — save model
							ctxWin, err := strconv.Atoi(m.formValues[2])
							if err != nil {
								m.formError = fmt.Sprintf("Invalid context window: %s", m.formValues[2])
								return m, nil
							}
							maxTok, err := strconv.Atoi(m.formValues[3])
							if err != nil {
								m.formError = fmt.Sprintf("Invalid max tokens: %s", m.formValues[3])
								return m, nil
							}
							vision := m.formValues[4] == "yes"
							if err := m.addModelToProvider(m.providerSelectedName, m.formValues[0], m.formValues[1], ctxWin, maxTok, vision); err != nil {
								m.formError = err.Error()
								return m, nil
							}
							m.modalMode = ModalNone
							return m, nil
						}
						// Advance to next step
						m.formStepIndex++
						m.textInput.SetValue("")
						switch m.formStepIndex {
						case 1:
							m.textInput.Placeholder = "Actual model name (e.g. gpt-4o-2024-08-06)"
						case 2:
							m.textInput.Placeholder = "Context window (e.g. 128000)"
						case 3:
							m.textInput.Placeholder = "Max tokens (e.g. 4096)"
						case 4:
							m.textInput.Placeholder = "Vision support? (yes/no)"
						}
						return m, nil
					} else if m.modalMode == ModalBackgroundExecs {
						if m.bgExecViewMode {
							// We're in output view mode - go back to list
							m.bgExecViewMode = false
							m.bgExecViewOutput = ""
							m.bgExecViewStatus = ""
							return m, m.tickCmd()
						}
						if m.modalSelectedIdx < len(m.bgExecModalKeys) {
							procID := m.bgExecModalKeys[m.modalSelectedIdx]
							m.bgExecViewMode = true
							m.bgExecViewID = procID
							// Fetch initial output
							output, status, _, err := m.agentLoop.GetProvidable().GetBackgroundExecOutput(procID, 5000)
							if err == nil {
								m.bgExecViewOutput = output
								m.bgExecViewStatus = status
							}
							return m, m.tickCmd()
						}
					}
					if !m.bgExecViewMode {
						m.modalMode = ModalNone
					}
					m.reloadSessions()
				}
			case "esc", "q":
				if m.modalMode == ModalBackgroundExecs && m.bgExecViewMode {
					// In output view mode: go back to list
					if msg.String() == "q" || msg.String() == "esc" {
						m.bgExecViewMode = false
						m.bgExecViewOutput = ""
						m.bgExecViewStatus = ""
						return m, m.tickCmd()
					}
				}
				m.modalMode = ModalNone
			case "s":
				if m.modalMode == ModalBackgroundExecs && !m.bgExecViewMode {
					if m.modalSelectedIdx < len(m.bgExecModalKeys) {
						procID := m.bgExecModalKeys[m.modalSelectedIdx]
						_ = m.agentLoop.GetProvidable().StopBackgroundExec(procID)
						// Refresh the list
						return m, m.executeCommand("/bg")
					}
				}
			}
			// Forward keystrokes to textInput for form-based modals
			// so users can type in the input fields.
			if m.modalMode == ModalAddProvider || m.modalMode == ModalAddModel {
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				if m.isSessionProcessing() {
					return m, tea.Batch(cmd, m.tickCmd())
				}
				return m, cmd
			}
			// Restart tick animation if the target session is actively processing.
			// This keeps the loading dots when switching to a busy session/subagent.
			if m.isSessionProcessing() {
				return m, m.tickCmd()
			}
			return m, nil
		}

		// Handle command approval when pending — intercept keys before normal input.
		// The agent is blocked waiting for the user's decision, so this takes priority
		// over typing new messages. Scrolling is still allowed to review context.
		if m.pendingApprovalID != "" && m.modalMode == ModalNone {
			switch msg.String() {
			case "y", "Y":
				m.handleApproval(true)
				return m, nil
			case "n", "N", "esc":
				m.handleApproval(false)
				return m, nil
			case "up", "down", "pgup", "pgdown":
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
			// Block all other input while approval is pending
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
					m.chatInput.SetValue(completed)
					m.showAutocomplete = false
					if msg.String() == "enter" {
						cmd := m.executeCommand(completed)
						if cmd != nil {
							cmds = append(cmds, cmd)
						}
						m.chatInput.SetValue("")
					}
				}
				return m, tea.Batch(cmds...)
			case "esc":
				m.showAutocomplete = false
				return m, nil
			}
		}

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
				return m, nil
			}

		case "tab":
			// Cycle mode: agent -> chat -> [group ->] agent.
			// Only when no autocomplete and no modal is active.
			if m.showAutocomplete || m.modalMode != ModalNone {
				return m, nil
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
			m.updateViewport()
			return m, nil

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
			if m.isSessionProcessing() {
				return m, m.tickCmd()
			}
			return m, nil

		case "ctrl+p":
			m.showAutocomplete = true
			m.filterAutocomplete("/")
			return m, nil

		case "ctrl+m":
			cmd := m.executeCommand("/models")
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, nil

		case "ctrl+a":
			cmd := m.executeCommand("/agents")
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, nil

		case "ctrl+t":
			// Toggle mouse capture so the user can select/copy text natively.
			m.mouseEnabled = !m.mouseEnabled
			if m.mouseEnabled {
				return m, tea.EnableMouseCellMotion
			}
			return m, tea.DisableMouse

		case "ctrl+s":
			cmd := m.executeCommand("/sessions")
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, nil

		case "ctrl+y":
			m.copyLastAssistantMessage()

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
					return m, nil
				}
			}
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)
		case "home":
			m.viewport.GotoTop()
		case "end":
			m.viewport.GotoBottom()

		case "enter":
			inputVal := m.chatInput.Value()

			// Group mode: wrap non-command input as /group start <profileID> <task>
			if m.currentMode == ModeGroup && !strings.HasPrefix(inputVal, "/") &&
				strings.TrimSpace(inputVal) != "" {
				profiles := m.getGroupProfiles()
				if len(profiles) > 0 && m.groupProfileIdx >= 0 && m.groupProfileIdx < len(profiles) {
					cmd := m.submitGroupStart(profiles[m.groupProfileIdx].ID, inputVal)
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
					return m, tea.Batch(cmds...)
				}
				// No valid profiles — fall through to normal submitMessage
			}

			if strings.HasPrefix(inputVal, "/") {
				cmd := m.executeCommand(inputVal)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chatInput.SetValue("")
			} else if !m.processing && !m.hasRunningSubagents() {
				cmd := m.submitMessage()
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}

	case tea.MouseMsg:
		if !m.mouseEnabled {
			break
		}
		// Handle mouse scrolling on the viewport
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

		// Handle mouse clicks on subagent items in the sidebar (only if no modal is active)
		if m.modalMode == ModalNone && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			// Check if click is in the right sidebar area
			leftWidth := int(float64(m.width) * leftColumnRatio)
			rightWidth := m.width - leftWidth - 3
			sidebarStartX := leftWidth + 1 // +1 for separator

			if msg.X >= sidebarStartX && msg.X < sidebarStartX+rightWidth {
				// Check if click matches any subagent click target Y-coordinate in the sidebar text
				for _, target := range m.subagentClickTargets {
					if msg.Y >= target.yStart && msg.Y < target.yEnd {
						// Clicked on a subagent item - switch to that subagent's chat
						if m.parentSessionKey == "" {
							m.parentSessionKey = m.currentKey
						}
						m.currentKey = target.key
						m.showWelcome = false
						m.clearStreamingState()
						m.reloadSessions()
						return m, nil
					}
				}
			}
		}

	case tickMsg:
		// Reset tick pending flag to allow the next tick to be scheduled
		m.tickPending = false
		// Refresh background exec output if viewing a process
		if m.modalMode == ModalBackgroundExecs && m.bgExecViewMode && m.bgExecViewID != "" {
			output, status, _, _ := m.agentLoop.GetProvidable().GetBackgroundExecOutput(m.bgExecViewID, 5000)
			m.bgExecViewOutput = output
			m.bgExecViewStatus = status
			if status == "running" {
				cmds = append(cmds, m.tickCmd())
			}
			// Don't fall through to the session processing tick
			break
		}
		if m.isSessionProcessing() {
			m.elapsedTime = time.Since(m.startTime)
			m.animationTick++
			cmds = append(cmds, m.tickCmd())
		}
		if m.escHint && time.Since(m.escLastPress) > escHintTimeout {
			m.escHint = false
			m.escPressCount = 0
		}

	case outboundMsg:
		if msg.msg.ChatID == m.currentKey {
			switch msg.msg.Event {
			case "subagent.result":
				// The result is also queued as a system message for the parent.
				// Keep loading active while that continuation waits for the session
				// lock, even if the original turn has already completed.
				if !m.processing {
					m.parentCompletionObserved = true
				}
				m.pendingSubagentCompletions++
				m.processing = true
				m.startTime = time.Now()
				m.lastDuration = 0
				m.updateViewport()
				cmds = append(cmds, m.tickCmd())
			case "approval.request":
				// Command approval requested by the agent — store the pending
				// approval and update the viewport to show the prompt.
				m.pendingApprovalID = msg.msg.Metadata["id"]
				m.pendingApprovalCmd = msg.msg.Metadata["command"]
				m.pendingApprovalReason = msg.msg.Metadata["reason"]
				m.approvalResult = ""
				m.updateViewport()
			case "message.stream":
				// Clear pending user message on first stream chunk — by the time
				// the LLM starts streaming, the user message is already in history.
				if m.pendingUserMessage != "" {
					m.pendingUserMessage = ""
					m.renderedBaseKey = "" // invalidate cache so history re-renders without duplicate
				}
				m.currentToolAction = "" // streaming text means tool call is done
				// Only reset streaming state if the MessageID is truly different.
				if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
					m.currentAssistantMsgID = msg.msg.MessageID
					m.resetStreamState()
				}
				m.currentStream += msg.msg.Content
				if cmd := m.throttledUpdateViewport(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case "message.thinking":
				// Only reset streaming state if the MessageID is truly different.
				if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
					m.currentAssistantMsgID = msg.msg.MessageID
					m.resetStreamState()
				}
				m.currentThinking += msg.msg.Content
				if cmd := m.throttledUpdateViewport(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case "tool.executing":
				// Only clear streaming state if it's a different message.
				if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
					m.currentStream = ""
					m.currentThinking = ""
					m.currentAssistantMsgID = msg.msg.MessageID
				}

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
				// Clear any approval result feedback when the tool completes.
				m.approvalResult = ""
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
			case "group.status":
				// Group status event: started/done/stopped/error
				groupID := msg.msg.Metadata["group_id"]
				if groupID == "" {
					break
				}
				status := msg.msg.Metadata["status"]
				if m.groupStatus == nil {
					m.groupStatus = make(map[string]string)
				}
				m.groupStatus[groupID] = status
				// Track active group and store participants metadata
				if status == "started" {
					if m.activeGroupID == "" {
						m.activeGroupID = groupID
					}
					m.processing = true
					if participants := msg.msg.Metadata["participants"]; participants != "" {
						if m.groupMeta == nil {
							m.groupMeta = make(map[string]groupMeta)
						}
						if gm, ok := m.groupMeta[groupID]; ok {
							gm.participants = participants
							m.groupMeta[groupID] = gm
						} else {
							m.groupMeta[groupID] = groupMeta{participants: participants}
						}
					}
				}
				// Stop processing when the group finishes
				if status == "done" || status == "stopped" || status == "error" {
					m.processing = false
				}
				m.updateViewport()
			case "group.turn":
				// Group turn: an agent produced a response within the group
				groupID := msg.msg.Metadata["group_id"]
				if groupID == "" {
					break
				}
				if m.groupTranscripts == nil {
					m.groupTranscripts = make(map[string][]groupTurn)
				}
				layer, _ := strconv.Atoi(msg.msg.Metadata["layer"])
				turnIndex, _ := strconv.Atoi(msg.msg.Metadata["turn_index"])
				turn := groupTurn{
					index:   turnIndex,
					layer:   layer,
					speaker: msg.msg.Metadata["speaker"],
					label:   msg.msg.Metadata["label"],
					role:    msg.msg.Metadata["role"],
					content: msg.msg.Content,
				}
				m.groupTranscripts[groupID] = append(m.groupTranscripts[groupID], turn)
				m.processing = true
				if m.activeGroupID == "" {
					m.activeGroupID = groupID
				}
				m.updateViewport()
			case "group.complete":
				// Group complete: final synthesis available
				groupID := msg.msg.Metadata["group_id"]
				if groupID == "" {
					break
				}
				if m.groupMeta == nil {
					m.groupMeta = make(map[string]groupMeta)
				}
				layers, _ := strconv.Atoi(msg.msg.Metadata["layers"])
				totalTokens, _ := strconv.Atoi(msg.msg.Metadata["total_tokens"])
				gm := m.groupMeta[groupID]
				gm.strategy = msg.msg.Metadata["strategy"]
				gm.layers = layers
				gm.totalTokens = totalTokens
				gm.synthesis = msg.msg.Content
				m.groupMeta[groupID] = gm
				m.processing = false
				if m.groupStatus == nil {
					m.groupStatus = make(map[string]string)
				}
				m.groupStatus[groupID] = "done"
				m.updateViewport()
			case "":
				// Stream finished — flush any pending throttled viewport update
				// so the final content is visible immediately.
				m.flushStreamUpdate()
				idMatches := msg.msg.MessageID == m.currentMessageID ||
					msg.msg.ReplyTo == m.currentMessageID ||
					(m.currentMessageID != "" && strings.HasPrefix(msg.msg.MessageID, m.currentMessageID+"-"))

				if m.processing && (m.pendingSubagentCompletions > 0 || idMatches) {
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
			if m.pendingSubagentCompletions > 0 {
				if !m.parentCompletionObserved {
					// This is the completion of the original parent turn. The
					// queued subagent result will start another parent turn.
					m.parentCompletionObserved = true
					m.reloadSessions()
					break
				}
				m.pendingSubagentCompletions--
				if m.pendingSubagentCompletions > 0 {
					m.reloadSessions()
					break
				}
			}
			m.parentCompletionObserved = true
			m.lastDuration = time.Since(m.startTime)
			m.clearStreamingState()
			// clearStreamingState preserves an active backend session when
			// navigating between chats. This completion belongs to the current
			// turn, so its local loading state must be cleared explicitly.
			m.processing = false
			m.reloadSessions()
		}

	case compactResultMsg:
		if msg.sessionKey == m.currentKey {
			m.lastDuration = time.Since(m.startTime)
			m.processing = false
			m.compactFeedback = msg.result
			m.forceGotoBottom = true
			m.reloadSessions()
		}

	case streamThrottleMsg:
		m.streamThrottleActive = false
		if m.streamPendingUpdate {
			m.updateViewport()
			m.streamPendingUpdate = false
			m.streamThrottleActive = true
			cmds = append(cmds, tea.Tick(m.streamThrottleInterval, func(t time.Time) tea.Msg {
				return streamThrottleMsg{}
			}))
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Invalidate ALL render caches so content is re-wrapped to the new width.
		// View() recalculates viewport dimensions on every render, so we don't
		// need to set them here — we just need to ensure caches are cleared.
		m.streamRenderedLines = nil
		m.thinkingRenderedLines = nil
		m.renderedBase = ""
		m.renderedBaseKey = ""
		m.cachedRenderer = nil
		m.cachedRendererWidth = 0

		m.updateViewport()
	}
	if m.modalMode == ModalNone {
		// Only forward message types the textarea knows how to handle.
		// Custom TUI messages (outboundMsg, tickMsg, completeMsg, streamThrottleMsg, tea.MouseMsg) must be
		// excluded to prevent garbage characters in the input field.
		switch msg := msg.(type) {
		case outboundMsg, completeMsg, tickMsg, streamThrottleMsg, tea.MouseMsg, compactResultMsg:
			// skip — not relevant to textarea
		case tea.KeyMsg:
			if m.isEscapeSequenceFragment(msg) {
				break
			}
			// For rune messages, filter out SGR mouse escape sequences
			// that bubbletea may have failed to parse as tea.MouseMsg.
			if msg.Type == tea.KeyRunes {
				cleaned, consume := filterAndBufferEscapes(m, msg)
				if !consume {
					break
				}
				msg.Runes = cleaned
			}
			// Do NOT forward "enter" to textarea — it's handled above for sending.
			// Do NOT forward "ctrl+m" — it's used for /models.
			if msg.String() == "enter" || msg.String() == "ctrl+m" {
				break
			}
			var cmd tea.Cmd
			m.chatInput, cmd = m.chatInput.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			// Defensive: strip any mouse escape sequences that slipped through.
			if cleaned := stripMouseEscapeSequences(m.chatInput.Value()); cleaned != m.chatInput.Value() {
				m.chatInput.SetValue(cleaned)
			}
		default:
			var cmd tea.Cmd
			m.chatInput, cmd = m.chatInput.Update(msg)
			cmds = append(cmds, cmd)
		}

		val := m.chatInput.Value()
		if strings.HasPrefix(val, "/") {
			m.showAutocomplete = true
			m.filterAutocomplete(val)
		} else {
			m.showAutocomplete = false
		}
	}

	return m, tea.Batch(cmds...)
}

// isEscapeSequenceFragment detects and consumes fragments of CSI escape
// sequences that leak through as tea.KeyMsg (common with mouse scroll in
// terminals like Konsole). It uses a state machine to track multi-rune
// sequences, with a 200ms safety timeout to avoid blocking legitimate input.
func (m *Model) isEscapeSequenceFragment(msg tea.KeyMsg) bool {
	now := time.Now()

	// Step 1: Safety timeout — if we've been in a sequence for >200ms without
	// a new rune, the sequence is stale. Reset and let input through.
	if m.escSeqActive && now.Sub(m.escSeqLastRune) > 200*time.Millisecond {
		m.escSeqActive = false
	}

	// Step 2: Detect START of a new escape sequence.
	if !m.escSeqActive {
		// Case A: bubbletea delivers ESC as tea.KeyEscape.
		if msg.Type == tea.KeyEscape {
			m.escSeqActive = true
			m.escSeqLastRune = now
			return true
		}
		// Case A-2: ESC rune delivered inside msg.Runes (bubbletea grouped bytes)
		for _, r := range msg.Runes {
			if r == 0x1b {
				m.escSeqActive = true
				m.escSeqLastRune = now
				return true
			}
		}
		// Case B: The '[' or '<' that arrives immediately after an ESC
		// (within 50ms) — bubbletea may split ESC from the rest.
		if time.Since(m.lastEscTime) < 50*time.Millisecond && len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r == '[' || r == '<' {
				m.escSeqActive = true
				m.escSeqLastRune = now
				return true
			}
		}
		return false
	}

	// Step 3: We're inside an active escape sequence — classify the rune(s).
	m.escSeqLastRune = now

	runes := msg.Runes
	if len(runes) == 0 {
		// Some KeyMsg types carry no runes (e.g. special keys). These are
		// definitely not CSI bytes. End the sequence and don't consume.
		m.escSeqActive = false
		return false
	}

	for _, r := range runes {
		switch {
		case r == '[' || r == '<':
			// CSI introducer '[' and private-parameter marker '<' — these
			// keep the sequence active (they must NOT be treated as final
			// bytes even though '[' falls within the 0x40-0x7E final range).
		case isCSIIntermediate(r):
			// Valid intermediate byte — stay in sequence.
		case isCSIFinal(r):
			// Terminating byte — sequence is complete.
			m.escSeqActive = false
		default:
			// Unexpected byte. End sequence to avoid getting stuck, but still
			// consume this rune (it's likely garbage from the broken sequence).
			m.escSeqActive = false
		}
	}
	return true
}

// isCSIIntermediate reports whether r is a valid intermediate byte of a CSI
// sequence (parameter bytes 0x30-0x3F and intermediate bytes 0x20-0x2F).
func isCSIIntermediate(r rune) bool {
	return (r >= '0' && r <= '9') || r == ';' || r == '?' || r == ':' ||
		(r >= ' ' && r <= '/') // 0x20-0x2F includes '<' among others
}

// isCSIFinal reports whether r is a valid final byte of a CSI sequence
// (0x40-0x7E).
func isCSIFinal(r rune) bool {
	return r >= 0x40 && r <= 0x7E
}

// resetModal resets the modal state for a new modal.
func (m *Model) resetModal(mode modalType) {
	m.modalMode = mode
	m.modalScrollOffset = 0
	m.modalItems = nil
	m.modalSessionKeys = nil
	m.modalSubagentKeys = nil
	m.modalSelectedIdx = 0
	m.bgExecModalKeys = nil
	m.bgExecViewMode = false
	m.bgExecViewID = ""
	m.bgExecViewOutput = ""
	m.bgExecViewStatus = ""
	m.formStepIndex = 0
	m.formValues = nil
	m.formError = ""
	m.formConfirmMode = false
	m.providerModalKeys = nil
	m.providerSelectedName = ""
	m.providerEditMode = false
}

// isListModal returns true if the modal type is a list-selection modal
// (navigable with up/down keys), as opposed to form-based modals.
func isListModal(mode modalType) bool {
	switch mode {
	case ModalNone, ModalAddProvider, ModalAddModel:
		return false
	default:
		return true
	}
}

// handleApproval processes the user's approval/rejection decision for a
// pending command approval. It calls the approval manager to unblock the
// agent goroutine and clears the pending state.
func (m *Model) handleApproval(approved bool) {
	if m.pendingApprovalID == "" {
		return
	}
	am := m.agentLoop.GetApprovalManager()
	if am != nil {
		am.HandleApproval(m.pendingApprovalID, approved)
	}
	if approved {
		m.approvalResult = ApprovalApproved.Render("✅ " + i18n.T("tui.approvalApproved"))
	} else {
		m.approvalResult = ApprovalRejected.Render("❌ " + i18n.T("tui.approvalRejected"))
	}
	m.pendingApprovalID = ""
	m.pendingApprovalCmd = ""
	m.pendingApprovalReason = ""
	m.updateViewport()
}
