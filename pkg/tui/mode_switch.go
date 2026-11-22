package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func (m *Model) cycleAgent() bool {
	prov := m.agentLoop.GetProvidable()
	ids := prov.ListAvailableAgentIDs()
	if len(ids) < 2 {
		return false
	}
	sort.Strings(ids)

	current := m.currentAgentID()
	next := ids[0]
	for i, id := range ids {
		if id == current {
			next = ids[(i+1)%len(ids)]
			break
		}
	}
	m.applyAgentSelection(next)
	return true
}

func (m *Model) currentAgentID() string {
	prov := m.agentLoop.GetProvidable()
	if m.currentKey != "" {
		if id := prov.GetSessionAgent(m.currentKey); id != "" {
			return id
		}
	}
	if m.pendingAgent != "" {
		return m.pendingAgent
	}
	return prov.GetDefaultAgentID()
}

func (m *Model) applyAgentSelection(agentID string) {
	if agentID == "" {
		return
	}
	m.pendingAgent = agentID
	if m.currentKey != "" {
		m.agentLoop.GetProvidable().SetSessionAgent(m.currentKey, agentID)
	}
	m.lastViewportKey = ""
	m.renderedBaseKey = ""
}

func (m *Model) cycleMode() {
	switch m.currentMode {
	case ModeAgent:
		m.currentMode = ModeChat
	case ModeChat:
		if m.groupsEnabled() {
			m.currentMode = ModeGroup
		} else {
			m.currentMode = ModeAgent
		}
	case ModeGroup:
		m.currentMode = ModeAgent
	default:
		m.currentMode = ModeAgent
	}
	m.reloadSessions()
}

func (m *Model) setMode(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "agent":
		m.currentMode = ModeAgent
	case "chat":
		m.currentMode = ModeChat
	case "group":
		if !m.groupsEnabled() {
			return false
		}
		m.currentMode = ModeGroup
	default:
		return false
	}
	m.reloadSessions()
	return true
}

func (m *Model) handleModeCommand(args []string) {
	if len(args) == 0 {
		m.cycleMode()
		return
	}
	if m.setMode(args[0]) {
		return
	}
	m.compactFeedback = ""
	m.statusFeedback = fmt.Sprintf(i18n.T("tui.modeUsage"), m.modeNames())
	if m.currentKey != "" {
		m.updateViewport()
	}
}

func (m *Model) groupsEnabled() bool {
	return m.cfg != nil && m.cfg.Groups.Enabled
}

func (m *Model) modeNames() string {
	if m.groupsEnabled() {
		return strings.Join([]string{ModeAgent.String(), ModeChat.String(), ModeGroup.String()}, ", ")
	}
	return strings.Join([]string{ModeAgent.String(), ModeChat.String()}, ", ")
}
