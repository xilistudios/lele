package tui

import (
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"strings"
)

// Command-approval interaction: user decision handling and whitelist
// persistence.
func (m *Model) handleApproval(approved bool) {
	if m.pendingApprovalID == "" {
		return
	}
	// Capture the id before clearing the visible fields so the defensive
	// snapshot cleanup below can verify it still refers to the same request.
	answeredID := m.pendingApprovalID
	am := m.agentLoop.GetApprovalManager()
	var err error
	if am != nil {
		_, err = am.HandleApproval(answeredID, approved)
	}
	if err != nil {
		// The approval already expired (cleanupExpired deleted it) or was
		// answered elsewhere — surface a distinct warning instead of the
		// misleading ✅/❌ confirmation.
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
	// Drop the stashed snapshot for this session if it still mirrors the
	// request just answered. A newer approval.request that replaced it (or a
	// prompt abandoned by a switch to a different session) keeps its snapshot,
	// so it cannot be resurrected on the next clearStreamingState.
	if snap, ok := m.pendingApprovals[m.currentKey]; ok && snap.id == answeredID {
		delete(m.pendingApprovals, m.currentKey)
	}
	m.updateViewport()
}

// handleWhitelistApproval answers the pending approval with "approve" and
// permanently adds the command to the exec allowlist. The decision is
// persisted to the config file and hot-applied to the live agents, so the same
// command never asks again — not after a restart either.
func (m *Model) handleWhitelistApproval() {
	if m.pendingApprovalID == "" {
		return
	}
	command := m.pendingApprovalCmd

	// Persist first: if the save fails, leave the prompt pending so the user
	// can still decide with y/n instead of silently losing the entry.
	if !m.addCommandToWhitelistConfig(command) {
		m.approvalResult = ApprovalRejected.Render("⚠️ " + i18n.T("tui.approvalWhitelistFailed"))
		m.updateViewport()
		return
	}

	m.handleApproval(true)
	m.approvalResult = ApprovalApproved.Render("✅ " + i18n.T("tui.approvalWhitelisted"))
	m.updateViewport()
}

// addCommandToWhitelistConfig appends command to tools.exec.whitelist_commands
// in the in-memory config, saves it to disk and hot-applies it to the running
// agents. Returns false when the config could not be persisted.
func (m *Model) addCommandToWhitelistConfig(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	key := tools.NormalizeWhitelistKey(command)

	// Deduplicate against the existing entries using the same normalization
	// the exec tool applies at check time.
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
		// Roll the in-memory change back so cfg never diverges from the last
		// known-good file on disk (a later unrelated save must not silently
		// persist an entry the user was told failed).
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
