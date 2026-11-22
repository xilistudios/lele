package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleAsyncResult(msg tea.Msg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case compactResultMsg:
		if msg.sessionKey == m.currentKey {
			m.lastDuration = time.Since(m.startTime)
			m.processing = false
			m.currentToolAction = ""
			m.compactFeedback = sanitizeDisplayText(msg.result)
			m.forceGotoBottom = true
			m.reloadSessions()
		}

	case skillsScanResultMsg:
		return m, m.handleSkillScanResult(msg)

	case skillsInstallResultMsg:
		return m, m.handleSkillInstallResult(msg)

	case skillToggleResultMsg:
		return m, m.handleSkillToggleResult(msg)

	case skillDeleteResultMsg:
		return m, m.handleSkillDeleteResult(msg)

	case mcpToggleResultMsg:
		return m, m.handleMCPToggleResult(msg)

	case mcpAddResultMsg:
		return m, m.handleMCPAddResult(msg)

	case mcpAuthStartMsg:
		return m, m.handleMCPAuthStart(msg)

	case mcpAuthPollMsg:
		return m, m.handleMCPAuthPoll(msg)

	case mcpAuthSubmitMsg:
		return m, m.handleMCPAuthSubmit(msg)

	case connectOAuthStartMsg:
		return m, m.handleConnectOAuthStart(msg)

	case connectOAuthPollMsg:
		return m, m.handleConnectOAuthPoll(msg)

	case connectOAuthSubmitMsg:
		return m, m.handleConnectOAuthSubmit(msg)

	case obVerifyResultMsg:
		m.obVerifying = false
		if !msg.success {
			m.obVerifyFailed = true
		}
		m.onboardingStep = obDone
		m.obFinalizeSetup()
		return m, nil

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

	case langCatalogMsg:
		return m, m.handleLangCatalogMsg(msg)

	case langInstallResultMsg:
		return m, m.handleLangInstallResult(msg)
	}

	return m.finishUpdate(msg, cmds)
}
