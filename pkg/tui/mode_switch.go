package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Agent and mode switching
// ------------------------
// Two orthogonal shortcuts live here:
//
//   - TAB          → cycleAgent(): walk the configured agent list circularly.
//   - /mode [name] → switch the session filter: agent (default), chat or group.
//
// They used to be one thing (TAB cycled modes). Splitting them keeps TAB a
// one-key hot path for "talk to a different agent" while the mode — a rarer,
// more deliberate choice — moves behind an explicit command.

// cycleAgent switches the agent on screen (or the pending agent of a chat that
// is about to be created) to the next configured agent, wrapping around at the
// end of the list. TAB is bound to it.
//
// It reports whether an agent was actually switched; with fewer than two
// configured agents there is nothing to cycle and the caller leaves the UI
// untouched.
func (m *Model) cycleAgent() bool {
	prov := m.agentLoop.GetProvidable()
	ids := prov.ListAvailableAgentIDs()
	if len(ids) < 2 {
		return false
	}
	// The registry returns IDs in map order; sorting gives the cycle a stable
	// order so pressing TAB twice never lands back on the same agent.
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

// currentAgentID reports the agent bound to the session on screen, falling back
// to the pending selection (welcome screen) and finally to the default agent.
func (m *Model) currentAgentID() string {
	prov := m.agentLoop.GetProvidable()
	if m.currentKey != "" {
		// GetSessionAgent already falls back to the default agent, so an
		// empty result here only means "no session provider at all".
		if id := prov.GetSessionAgent(m.currentKey); id != "" {
			return id
		}
	}
	if m.pendingAgent != "" {
		return m.pendingAgent
	}
	return prov.GetDefaultAgentID()
}

// applyAgentSelection binds agentID to the session on screen (when there is
// one) and remembers it as the pending agent, i.e. the agent the next chat
// created from the welcome screen or via /new will start with — picking an
// agent is an explicit "this is who I want to talk to" and it would be
// surprising for the next chat to silently revert to the default.
//
// Shared by the /agents picker and the TAB cycle so both paths invalidate the
// same render caches: the agent name is rendered in the viewport header, the
// bottom bar and the welcome selector, so the cached frames must be dropped.
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

// cycleMode advances the active mode: agent → chat → [group →] agent. Group is
// part of the cycle only when group chats are enabled in the config.
//
// The mode is a session FILTER: it selects which kind of chats the sidebar lists
// and which kind /new creates. It does not re-categorize the chat on screen.
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

// setMode switches to an explicit mode by name. It reports false — leaving the
// active mode untouched — for an unknown name and for "group" when group chats
// are disabled.
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

// handleModeCommand implements /mode: bare "/mode" advances to the next mode
// (the behavior TAB used to have), "/mode <name>" switches to that mode. An
// unknown or unavailable name surfaces the accepted values instead of failing
// silently.
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
	// The usage hint lives in the viewport overlay, which only exists in the
	// chat layout — mirror /status and materialize it now so it shows on the
	// very next frame instead of waiting for the next event.
	if m.currentKey != "" {
		m.updateViewport()
	}
}

// groupsEnabled reports whether group chats are enabled, tolerating a Model
// built without a config (unit tests, early bootstrap).
func (m *Model) groupsEnabled() bool {
	return m.cfg != nil && m.cfg.Groups.Enabled
}

// modeNames lists the modes available under the current config, for the usage
// hint shown when /mode gets an argument it cannot honor.
func (m *Model) modeNames() string {
	if m.groupsEnabled() {
		return strings.Join([]string{ModeAgent.String(), ModeChat.String(), ModeGroup.String()}, ", ")
	}
	return strings.Join([]string{ModeAgent.String(), ModeChat.String()}, ", ")
}
