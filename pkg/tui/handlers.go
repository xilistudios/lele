package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	mm, ok := model.(*Model)
	if !ok {
		return model, cmd
	}
	// Safety net for the client-side queue: every event path — including the
	// many early returns inside the modal branch — funnels through here, so a
	// pending backlog can never be left without a live retry chain (e.g. ESC
	// closing the modal that had deferred a flush). maybeFlushQueue is a no-op
	// on an empty queue, re-checks the busy state itself, and its retry tick
	// is deduped by tickPending, so it can never start a second turn or a
	// second tick chain.
	if flushCmd := mm.maybeFlushQueue(); flushCmd != nil {
		cmd = tea.Batch(cmd, flushCmd)
	}
	if focusCmd := mm.syncChatInputFocus(); focusCmd != nil {
		cmd = tea.Batch(cmd, focusCmd)
	}
	return mm, cmd
}

// syncChatInputFocus aligns the chat textarea's focus state with the app
// state. The input is considered active only when no modal is open, no
// command approval is pending, and the onboarding wizard is not running.
// Blurring hides the blinking cursor; focusing restores it and restarts the
// blink loop via the returned command. Returns a non-nil tea.Cmd only when
// the focus state transitions from blurred to focused, so callers can batch
// it without spamming blink commands on every update.
func (m *Model) syncChatInputFocus() tea.Cmd {
	shouldFocus := m.modalMode == ModalNone &&
		m.pendingApprovalID == "" &&
		!m.onboardingActive
	if shouldFocus {
		if !m.chatInput.Focused() {
			return m.chatInput.Focus()
		}
		return nil
	}
	if m.chatInput.Focused() {
		m.chatInput.Blur()
	}
	return nil
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case communityIndexMsg:
		m.communityLoading = false
		if msg.err != "" {
			m.communityErr = msg.err
		} else {
			m.communityErr = ""
			m.communityIndex = msg.entries
		}
		if m.themePickerActive {
			m.loadThemePickerItems()
		}
		return m, nil
	case installThemeMsg:
		m.communityLoading = false
		if msg.err != "" {
			m.communityErr = msg.err
		} else {
			m.communityErr = ""
			if m.customThemes == nil {
				m.customThemes = make(map[string]theme.Theme)
			}
			m.customThemes[msg.name] = msg.theme
			m.installedCommunity = theme.AddInstalledCommunity(msg.name, m.installedCommunity)
			m.applyThemeByName(msg.name)
		}
		if m.themePickerActive {
			m.loadThemePickerItems()
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyMsg(msg, cmds)

	case tea.MouseMsg:
		return m.handleMouseMsg(msg, cmds)

	case tickMsg:
		return m.handleTickMsg(msg, cmds)

	case outboundMsg:
		return m.handleOutboundMsg(msg, cmds)

	case completeMsg:
		return m.handleCompleteMsg(msg, cmds)

	case compactResultMsg, skillsScanResultMsg, skillsInstallResultMsg,
		skillToggleResultMsg, skillDeleteResultMsg, obVerifyResultMsg,
		streamThrottleMsg:
		return m.handleAsyncResult(msg, cmds)

	case tea.WindowSizeMsg:
		return m.handleWindowSizeMsg(msg, cmds)

	default:
		return m.finishUpdate(msg, cmds)
	}
}

// handleKeyMsg routes a keystroke through the input surfaces in priority
// order: onboarding wizard, open modal, pending approval, autocomplete
// dropdown, then the main input keys. An unconsumed key still falls through
// to finishUpdate so typing reaches the chat textarea — the same order the
// original inline switch relied on.
func (m *Model) handleKeyMsg(msg tea.KeyMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	// Handle onboarding wizard keys. The obConnect step reuses the
	// ModalAddProvider form modal, so its keys are delegated to the
	// normal modal handler below (it must NOT be intercepted here).
	if m.onboardingActive && m.onboardingStep != obConnect {
		return m.handleOnboardingKey(msg)
	}
	if m.modalMode != ModalNone {
		return m.handleModalKey(msg)
	}

	// Handle command approval when pending — intercept keys before normal
	// input. The agent is blocked waiting for the user's decision, so this
	// takes priority over typing new messages. Scrolling is still allowed to
	// review context.
	if m.pendingApprovalID != "" && m.modalMode == ModalNone {
		return m.handleApprovalKey(msg)
	}

	if m.showAutocomplete {
		if mm, cmd, handled := m.handleAutocompleteKey(msg); handled {
			return mm, cmd
		}
	}

	mm, cmd, consumed := m.handleNormalKey(msg, cmds)
	if consumed {
		return mm, cmd
	}
	// Not consumed: forward the (possibly accumulated) cmd together with the
	// message to finishUpdate so typing still reaches the textarea, exactly
	// like the original inline switch falling through to the post-switch
	// block. tea.Batch inside finishUpdate skips nil commands.
	return mm.finishUpdate(msg, []tea.Cmd{cmd})
}

// finishUpdate is update()'s shared tail: it forwards the message to the
// chat textarea (unless a modal owns the input), refreshes slash-command
// autocomplete and returns the batched commands. Key handling reaches it
// only when no key case consumed the event; message handlers reach it by
// falling out of their original switch, so they call it explicitly. The
// skip list keeps custom TUI messages out of the textarea so they cannot
// inject garbage runes.
func (m *Model) finishUpdate(msg tea.Msg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if m.modalMode == ModalNone {
		// Only forward message types the textarea knows how to handle.
		// Custom TUI messages (outboundMsg, tickMsg, completeMsg, streamThrottleMsg, tea.MouseMsg) must be
		// excluded to prevent garbage characters in the input field.
		switch msg := msg.(type) {
		case outboundMsg, completeMsg, tickMsg, streamThrottleMsg, tea.MouseMsg, compactResultMsg, skillsScanResultMsg, skillsInstallResultMsg, skillToggleResultMsg, skillDeleteResultMsg:
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
