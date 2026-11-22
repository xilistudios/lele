package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	mm, ok := model.(*Model)
	if !ok {
		return model, cmd
	}
	mm.refreshSubagentsCache()
	if flushCmd := mm.maybeFlushQueue(); flushCmd != nil {
		cmd = tea.Batch(cmd, flushCmd)
	}
	if focusCmd := mm.syncChatInputFocus(); focusCmd != nil {
		cmd = tea.Batch(cmd, focusCmd)
	}
	if repaintCmd := mm.consumeLayoutRepaint(); repaintCmd != nil {
		cmd = tea.Batch(cmd, repaintCmd)
	}
	return mm, cmd
}

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
		mcpToggleResultMsg, mcpAddResultMsg, mcpAuthStartMsg, mcpAuthPollMsg, mcpAuthSubmitMsg,
		connectOAuthStartMsg, connectOAuthPollMsg, connectOAuthSubmitMsg,
		streamThrottleMsg, langCatalogMsg, langInstallResultMsg:
		return m.handleAsyncResult(msg, cmds)

	case tea.WindowSizeMsg:
		return m.handleWindowSizeMsg(msg, cmds)

	default:
		return m.finishUpdate(msg, cmds)
	}
}

func (m *Model) handleKeyMsg(msg tea.KeyMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if m.onboardingActive && m.onboardingStep != obConnect {
		return m.handleOnboardingKey(msg)
	}
	if m.modalMode != ModalNone {
		return m.handleModalKey(msg)
	}

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
	return mm.finishUpdate(msg, []tea.Cmd{cmd})
}

func (m *Model) finishUpdate(msg tea.Msg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if m.modalMode == ModalAddCommand && m.formStepIndex == commandTemplateStep {
		if _, ok := msg.(cursor.BlinkMsg); ok {
			m.templateInput, _ = m.templateInput.Update(msg)
			return m, tea.Cmd(textarea.Blink)
		}
	}
	if m.modalMode == ModalNone {
		switch msg := msg.(type) {
		case outboundMsg, completeMsg, tickMsg, streamThrottleMsg, tea.MouseMsg, compactResultMsg, skillsScanResultMsg, skillsInstallResultMsg, skillToggleResultMsg, skillDeleteResultMsg, mcpToggleResultMsg, mcpAddResultMsg, mcpAuthStartMsg, mcpAuthPollMsg, mcpAuthSubmitMsg, connectOAuthStartMsg, connectOAuthPollMsg, connectOAuthSubmitMsg:
		case tea.KeyMsg:
			if m.isEscapeSequenceFragment(msg) {
				break
			}
			if msg.Type == tea.KeyRunes {
				cleaned, consume := filterAndBufferEscapes(m, msg)
				if !consume {
					break
				}
				msg.Runes = cleaned
			}
			if msg.String() == "enter" || msg.String() == "ctrl+m" {
				break
			}
			var cmd tea.Cmd
			m.chatInput, cmd = m.chatInput.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
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
