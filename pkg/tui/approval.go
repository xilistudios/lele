package tui

import (
	"strings"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func (m *Model) handleApproval(approved bool) bool {
	if m.pendingApprovalID == "" {
		return false
	}
	answeredID := m.pendingApprovalID
	am := m.agentLoop.GetApprovalManager()
	var err error
	if am != nil {
		_, err = am.HandleApproval(answeredID, approved)
	}
	if err != nil {
		m.approvalResult = ApprovalRejected.Render("⚠️ " + i18n.T("tui.approvalExpired"))
	} else if approved {
		m.approvalResult = ApprovalApproved.Render("✅ " + i18n.T("tui.approvalApproved"))
	} else {
		m.approvalResult = ApprovalRejected.Render("❌ " + i18n.T("tui.approvalRejected"))
	}
	m.pendingApprovalID = ""
	m.pendingApprovalCmd = ""
	m.pendingApprovalReason = ""
	m.approvalShowFull = false
	if snap, ok := m.pendingApprovals[m.currentKey]; ok && snap.id == answeredID {
		delete(m.pendingApprovals, m.currentKey)
	}
	m.updateViewport()
	return err == nil
}

func (m *Model) handleWhitelistApproval() {
	if m.pendingApprovalID == "" {
		return
	}
	command := m.pendingApprovalCmd

	if !m.addCommandToWhitelistConfig(command) {
		m.approvalResult = ApprovalRejected.Render("⚠️ " + i18n.T("tui.approvalWhitelistFailed"))
		m.updateViewport()
		return
	}

	if m.handleApproval(true) {
		m.approvalResult = ApprovalApproved.Render("✅ " + i18n.T("tui.approvalWhitelisted"))
	}
	m.updateViewport()
}

func (m *Model) addCommandToWhitelistConfig(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	key := tools.NormalizeWhitelistKey(command)

	already := false
	for _, existing := range m.cfg.Tools.Exec.WhitelistCommands {
		if tools.NormalizeWhitelistKey(existing) == key {
			already = true
			break
		}
	}
	if !already {
		m.cfg.Tools.Exec.WhitelistCommands = append(m.cfg.Tools.Exec.WhitelistCommands, command)
	}

	if err := m.saveConfigToDisk(); err != nil {
		if !already {
			m.cfg.Tools.Exec.WhitelistCommands = m.cfg.Tools.Exec.WhitelistCommands[:len(m.cfg.Tools.Exec.WhitelistCommands)-1]
		}
		logger.WarnCF("tui", "failed to persist exec whitelist", map[string]interface{}{"error": err.Error()})
		return false
	}
	if m.agentLoop != nil {
		m.agentLoop.SetExecWhitelist(m.cfg.Tools.Exec.WhitelistCommands)
	}
	return true
}
