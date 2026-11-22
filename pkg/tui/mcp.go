package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

type mcpToggleResultMsg struct {
	name    string
	enabled bool
	changed bool
	path    string
	err     error
	logout  bool
}

var (
	errMCPUnavailable    = errors.New("mcp unavailable")
	errMCPOwnerNoPath    = errors.New("owning layer has no file")
	errMCPInvalid        = errors.New("mcp entry is invalid")
	errMCPPathNotAllowed = errors.New("mcp path not allowed")
)

const (
	mcpFilterAll int = iota
	mcpFilterGlobal
	mcpFilterAgent
	mcpFilterProject
	mcpFilterCount
)

func mcpFilterLayer(filter int) string {
	switch filter {
	case mcpFilterGlobal:
		return mcp.LayerGlobal
	case mcpFilterAgent:
		return mcp.LayerAgent
	case mcpFilterProject:
		return mcp.LayerProject
	default:
		return ""
	}
}

func (m *Model) mcpFilterLabel() string {
	switch m.mcpFilter {
	case mcpFilterGlobal:
		return i18n.T("tui.mcpFilterGlobal")
	case mcpFilterAgent:
		return i18n.T("tui.mcpFilterAgent")
	case mcpFilterProject:
		return i18n.T("tui.mcpFilterProject")
	default:
		return i18n.T("tui.mcpFilterAll")
	}
}

func (m *Model) mcpFilterHint() string {
	return fmt.Sprintf(i18n.T("tui.mcpFilter"), m.mcpFilterLabel())
}

func (m *Model) mcpPaths() (mcp.Paths, bool) {
	if m.agentLoop == nil || m.agentLoop.MessageBus() == nil {
		return mcp.Paths{}, false
	}
	agentID := "main"
	if prov := m.agentLoop.GetProvidable(); prov != nil {
		if id := prov.GetSessionAgent(m.currentKey); id != "" {
			agentID = id
		}
	}
	return m.agentLoop.MCPPathsFor(agentID)
}

func (m *Model) loadMCPList() {
	paths, ok := m.mcpPaths()
	if !ok {
		m.modalItems = []string{i18n.T("tui.mcpUnavailable")}
		m.mcpModalKeys = []string{""}
		m.mcpInventory = mcp.Inventory{}
		m.mcpInventoryValid = false
		m.mcpAuth = nil
		m.clampModalCursor()
		return
	}
	m.loadMCPListWith(paths)
}

func (m *Model) loadMCPListWith(paths mcp.Paths) {
	m.modalItems = nil
	m.mcpModalKeys = nil

	inv := mcp.ReadInventory(paths)
	m.mcpInventory = inv
	m.mcpInventoryValid = true
	m.mcpAuth = mcpAuthVerdicts(paths, inv)
	filterLayer := mcpFilterLayer(m.mcpFilter)
	names := make([]string, 0, len(inv.Winner))
	for name := range inv.Winner {
		if filterLayer != "" && inv.Winner[name] != filterLayer {
			continue
		}
		names = append(names, name)
	}

	if len(names) == 0 {
		m.modalItems = append(m.modalItems, i18n.T("tui.mcpNoServers"))
		m.mcpModalKeys = append(m.mcpModalKeys, "")
		m.clampModalCursor()
		return
	}

	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	for _, name := range names {
		m.modalItems = append(m.modalItems, formatMCPItem(
			name, inv.Winner[name], inv.Effective[name], mcpAuthSuffix(m.mcpAuth[name])))
		m.mcpModalKeys = append(m.mcpModalKeys, name)
	}
	m.clampModalCursor()
}

func (m *Model) handleMCPKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.modalMode == ModalMCPDetail {
		if s := msg.String(); s == "esc" || s == "q" {
			m.modalMode = ModalMCP
		} else if s == "a" {
			return m.mcpAuthStartFromDetail(), true
		} else if s == "d" {
			return m.mcpAuthLogoutCmd(m.mcpDetailName), true
		}
		return nil, true
	}
	if m.modalMode == ModalMCPAuth {
		return m.handleMCPAuthKey(msg)
	}
	if m.modalMode == ModalMCPAdd {
		return m.handleMCPAddKey(msg)
	}
	if m.modalMode != ModalMCP {
		return nil, false
	}
	switch msg.String() {
	case "n":
		return m.startMCPAdd(), true
	case " ", "t":
		if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.mcpModalKeys) {
			return nil, true
		}
		name := m.mcpModalKeys[m.modalSelectedIdx]
		if name == "" {
			return nil, true
		}
		return m.mcpToggleCmd(name), true
	case "d":
		if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.mcpModalKeys) {
			return nil, true
		}
		name := m.mcpModalKeys[m.modalSelectedIdx]
		if name == "" {
			return nil, true
		}
		return m.mcpAuthLogoutCmd(name), true
	case "f":
		m.mcpFilter = (m.mcpFilter + 1) % mcpFilterCount
		m.loadMCPList()
		return nil, true
	case "enter":
		if m.modalSelectedIdx >= 0 && m.modalSelectedIdx < len(m.mcpModalKeys) {
			if name := m.mcpModalKeys[m.modalSelectedIdx]; name != "" && m.mcpInventoryValid {
				m.mcpDetailName = name
				m.modalMode = ModalMCPDetail
			}
		}
		return nil, true
	case "esc", "q":
		m.modalMode = ModalNone
		return nil, true
	}
	return nil, false
}

var syncMCPServersFor = func(al *agent.AgentLoop) {
	if al != nil {
		al.SyncMCPServers()
	}
}

func (m *Model) mcpToggleCmd(name string) tea.Cmd {
	paths, ok := m.mcpPaths()
	if !ok {
		return func() tea.Msg {
			return mcpToggleResultMsg{name: name, err: errMCPUnavailable}
		}
	}
	return mcpToggleCmdFor(paths, name)
}

func mcpToggleCmdFor(paths mcp.Paths, name string) tea.Cmd {
	return func() tea.Msg {
		inv := mcp.ReadInventory(paths)
		path := mcp.LayerFile(paths, inv.Winner[name])
		if path == "" {
			return mcpToggleResultMsg{name: name, err: errMCPOwnerNoPath}
		}
		if !channels.IsAllowedWorkspacePath(filepath.Dir(path)) {
			return mcpToggleResultMsg{name: name, path: path, err: errMCPPathNotAllowed}
		}
		if inv.Effective[name] == "invalid" {
			return mcpToggleResultMsg{name: name, err: errMCPInvalid}
		}
		enabled := inv.Effective[name] == "enabled"
		res, err := mcp.SetDisabled(path, name, enabled)
		if err != nil {
			return mcpToggleResultMsg{name: name, err: err}
		}
		return mcpToggleResultMsg{name: name, enabled: !enabled, changed: res.Changed}
	}
}

func (m *Model) handleMCPToggleResult(msg mcpToggleResultMsg) tea.Cmd {
	if msg.logout {
		return m.handleMCPAuthLogoutResult(msg.name, msg.err)
	}
	switch {
	case msg.err == nil:
		if msg.changed {
			state := i18n.T("tui.mcpEnabled")
			if !msg.enabled {
				state = i18n.T("tui.mcpDisabled")
			}
			m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpToggleSuccess"), msg.name, state)
		} else {
			m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpToggleNoChange"), msg.name)
		}
		syncMCPServersFor(m.agentLoop)
	case errors.Is(msg.err, errMCPUnavailable):
		m.mcpFeedback = i18n.T("tui.mcpUnavailable")
	case errors.Is(msg.err, errMCPOwnerNoPath):
		m.mcpFeedback = i18n.T("tui.mcpOwnerNoPath")
	case errors.Is(msg.err, errMCPPathNotAllowed):
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpPathNotAllowed"), msg.name, msg.path)
	case errors.Is(msg.err, errMCPInvalid):
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpToggleInvalid"), msg.name)
	default:
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpToggleFailed"), msg.name, msg.err)
	}
	m.loadMCPList()
	return m.tickCmd()
}

func formatMCPItem(name, layer, effective, authSuffix string) string {
	status := "●"
	switch effective {
	case "disabled":
		status = "○"
	case "invalid":
		status = "!"
	}
	return fmt.Sprintf("%s %-15s — [%s]%s", status, name, layer, authSuffix)
}

func (m *Model) renderMCPDetail() string {
	name := m.mcpDetailName
	inv := m.mcpInventory
	rows, known := inv.ByName[name]
	if !m.mcpInventoryValid || name == "" || !known || len(rows) == 0 {
		m.modalMode = ModalMCP
		return m.renderModal(m.modalTitleFor(ModalMCP))
	}

	w, winnerIdx, ok := m.mcpWinnerRow(name)
	if !ok {
		m.modalMode = ModalMCP
		return m.renderModal(m.modalTitleFor(ModalMCP))
	}
	s := w.Summary

	verdict, verdictLabel := inv.Effective[name], inv.Effective[name]
	switch verdict {
	case "enabled":
		verdictLabel = i18n.T("tui.mcpEnabled")
	case "disabled":
		verdictLabel = i18n.T("tui.mcpDisabled")
	case "invalid":
		verdictLabel = i18n.T("tui.mcpInvalid")
	}
	verdictColor := SecondaryColor
	if verdict != "enabled" {
		verdictColor = CommentColor
	}

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(m.modalTitleFor(ModalMCPDetail)) + "\n")
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Center,
		TitleStyle.Render(name),
		"  ",
		lipgloss.NewStyle().Foreground(verdictColor).Render("["+verdictLabel+"]"),
	) + "\n\n")

	labelStyle := lipgloss.NewStyle().Foreground(AccentColor)
	addField := func(label, value string) {
		sb.WriteString(labelStyle.Render(label+": ") + value + "\n")
	}

	addField(i18n.T("tui.mcpDetailLayer"), w.Layer)
	addField(i18n.T("tui.mcpDetailPath"), w.Path)
	definition := i18n.T("tui.mcpDetailStub")
	if w.Defines {
		definition = i18n.T("tui.mcpDetailReal")
	}
	addField(i18n.T("tui.mcpDetailDefinition"), definition)

	if s.Kind != "" {
		addField(i18n.T("tui.mcpDetailKind"), s.Kind)
	}
	if s.Command != "" {
		addField(i18n.T("tui.mcpDetailCommand"), s.Command)
		addField(i18n.T("tui.mcpDetailArgs"), fmt.Sprintf("%d", s.Args))
	}
	if s.URL != "" {
		addField(i18n.T("tui.mcpDetailURL"), s.URL)
		if s.Type != "" {
			addField(i18n.T("tui.mcpDetailType"), s.Type)
		}
	}
	if s.Description != "" {
		addField(i18n.T("tui.mcpDetailDescription"), s.Description)
	}
	if w.Invalid != "" {
		addField(i18n.T("tui.mcpDetailInvalid"), w.Invalid)
	}
	addField(i18n.T("tui.mcpDetailEnvKeys"), mcpDetailKeyList(s.EnvKeys))
	addField(i18n.T("tui.mcpDetailHeaderKeys"), mcpDetailKeyList(s.HeaderKeys))

	auth, authKnown := m.mcpAuth[name]
	authShown := authKnown && (auth.Authenticated || auth.Required)
	if authShown {
		addField(i18n.T("tui.mcpDetailAuth"), mcpAuthMethodLabel(auth.Method))
		state := i18n.T("tui.mcpAuthRequired")
		if auth.Authenticated {
			state = i18n.T("tui.mcpAuthOK")
		}
		addField(i18n.T("tui.mcpDetailAuthState"), state)
	}

	shadowed := make([]mcp.EntryState, 0, len(rows)-1)
	for i, r := range rows {
		if i != winnerIdx {
			shadowed = append(shadowed, r)
		}
	}
	if len(shadowed) > 0 {
		sb.WriteString("\n")
		sb.WriteString(labelStyle.Render(i18n.T("tui.mcpDetailShadowed")+":") + "\n")
		for _, r := range shadowed {
			line := fmt.Sprintf("  - %s | %s | defines=%t | disabled=%t",
				r.Layer, r.Path, r.Defines, r.Disabled)
			if r.Summary.Command != "" {
				line += " | command=" + r.Summary.Command
			} else if r.Summary.URL != "" {
				line += " | url=" + r.Summary.URL
			}
			sb.WriteString(CommentColorStyle.Render(line) + "\n")
		}
	}

	sb.WriteString("\n")
	if verdict == "invalid" {
		sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.mcpInvalidHint")) + "\n")
	}
	if line := m.mcpAuthFeedbackLine(); line != "" {
		sb.WriteString(line + "\n")
	}
	hints := i18n.T("tui.mcpDetailHints")
	if authShown {
		hints += " | " + i18n.T("tui.mcpLogoutHint")
	}
	sb.WriteString(CommentColorStyle.Render("  " + hints))

	box := ModalContainer.Width(m.width - 10).Render(sb.String())
	return m.paintFrame(box)
}

func (m *Model) mcpWinnerRow(name string) (mcp.EntryState, int, bool) {
	inv := m.mcpInventory
	if !m.mcpInventoryValid || name == "" {
		return mcp.EntryState{}, 0, false
	}
	rows := inv.ByName[name]
	if len(rows) == 0 {
		return mcp.EntryState{}, 0, false
	}
	winnerIdx := -1
	for i := range rows {
		if rows[i].Layer == inv.Winner[name] {
			winnerIdx = i
		}
	}
	if winnerIdx < 0 {
		winnerIdx = len(rows) - 1
	}
	return rows[winnerIdx], winnerIdx, true
}

func mcpDetailKeyList(keys []string) string {
	if len(keys) == 0 {
		return i18n.T("tui.mcpDetailNone")
	}
	return strings.Join(keys, ", ")
}
