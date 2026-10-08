package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// ── Message types for async MCP operations ─────────────────────────────

// mcpToggleResultMsg is the result of one toggle write (mcpToggleCmdFor).
type mcpToggleResultMsg struct {
	name    string
	enabled bool // state AFTER the toggle
	changed bool // bytes on disk changed
	err     error
}

// Sentinel errors carried by mcpToggleResultMsg. The result handler maps
// them to localized feedback, so no i18n lookup happens inside the
// tea.Cmd goroutine.
var (
	errMCPUnavailable = errors.New("mcp unavailable")
	errMCPOwnerNoPath = errors.New("owning layer has no file")
)

// ── Source filter (/mcp "f" key): all → global → agent → project ──────

const (
	mcpFilterAll int = iota
	mcpFilterGlobal
	mcpFilterAgent
	mcpFilterProject
	mcpFilterCount // cycle modulus for the "f" key
)

// mcpFilterLayer returns the winner layer a filter selects ("" = all).
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

// mcpFilterLabel is the localized name of the active filter.
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

// mcpFilterHint is the "Filter: <label>" line rendered under the list.
func (m *Model) mcpFilterHint() string {
	return fmt.Sprintf(i18n.T("tui.mcpFilter"), m.mcpFilterLabel())
}

// ── Paths / agent resolution ───────────────────────────────────────────

// mcpPaths resolves the mcp.json roots for the current session's agent,
// mirroring how view.go derives the agent ID: the session's agent, falling
// back to "main". ok=false means the loop cannot resolve them (no loop,
// bare loop, agent not live) — the zero Paths must not be used.
func (m *Model) mcpPaths() (mcp.Paths, bool) {
	if m.agentLoop == nil || m.agentLoop.MessageBus() == nil {
		// The bus is set by every real constructor; a bare &AgentLoop{}
		// has none — and its providable is a typed-nil behind a non-nil
		// interface, so calling into it would panic instead of reporting
		// the unavailability MCPPathsFor would report below.
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

// ── loadMCPList refreshes the MCP server list in the modal ─────────────

// loadMCPList resolves the paths and rebuilds the listing synchronously
// (like loadSkillsList). Unresolvable paths yield the "MCP unavailable"
// row instead of a panic.
func (m *Model) loadMCPList() {
	paths, ok := m.mcpPaths()
	if !ok {
		m.modalItems = []string{i18n.T("tui.mcpUnavailable")}
		m.mcpModalKeys = []string{""}
		m.clampModalCursor()
		return
	}
	m.loadMCPListWith(paths)
}

// loadMCPListWith builds the filtered, name-sorted listing for explicit
// paths. m.mcpModalKeys stays 1:1 with m.modalItems ("" for the
// empty-state row) so index-based actions can never desync.
func (m *Model) loadMCPListWith(paths mcp.Paths) {
	m.modalItems = nil
	m.mcpModalKeys = nil

	inv := mcp.ReadInventory(paths)
	filterLayer := mcpFilterLayer(m.mcpFilter)
	names := make([]string, 0, len(inv.Winner))
	for name := range inv.Winner {
		if filterLayer != "" && inv.Winner[name] != filterLayer {
			continue
		}
		names = append(names, name)
	}

	if len(names) == 0 {
		// Empty inventory (or nothing under the active filter).
		m.modalItems = append(m.modalItems, i18n.T("tui.mcpNoServers"))
		m.mcpModalKeys = append(m.mcpModalKeys, "")
		m.clampModalCursor()
		return
	}

	// Sort by name for stable display (case-insensitive, like skills).
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	for _, name := range names {
		m.modalItems = append(m.modalItems, formatMCPItem(name, inv.Winner[name], inv.Effective[name]))
		m.mcpModalKeys = append(m.mcpModalKeys, name)
	}
	m.clampModalCursor()
}

// ── handleMCPKey owns the keys of the /mcp list modal ──────────────────
//
// space/t toggle the selected server, f cycles the source filter, enter is
// a deliberate no-op until ModalMCPDetail renders, esc/q close. Navigation
// (up/down) and every other key fall through to the generic modal switch.
// Returns handled=false so the caller forwards un-owned keys.
func (m *Model) handleMCPKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.modalMode != ModalMCP {
		return nil, false
	}
	switch msg.String() {
	case " ", "t":
		if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.mcpModalKeys) {
			return nil, true
		}
		name := m.mcpModalKeys[m.modalSelectedIdx]
		if name == "" {
			return nil, true // empty-state row
		}
		return m.mcpToggleCmd(name), true
	case "f":
		m.mcpFilter = (m.mcpFilter + 1) % mcpFilterCount
		m.loadMCPList()
		return nil, true
	case "enter":
		// ModalMCPDetail opens here in a later task; keep the cursor.
		return nil, true
	case "esc", "q":
		m.modalMode = ModalNone
		return nil, true
	}
	return nil, false
}

// ── mcpToggleCmd returns the command that flips one server ─────────────

// mcpToggleCmd resolves the paths NOW (cheap, no I/O); the inventory read
// and the write run inside the returned command — the only blocking I/O of
// the flow.
func (m *Model) mcpToggleCmd(name string) tea.Cmd {
	paths, ok := m.mcpPaths()
	if !ok {
		return func() tea.Msg {
			return mcpToggleResultMsg{name: name, err: errMCPUnavailable}
		}
	}
	return mcpToggleCmdFor(paths, name)
}

// mcpToggleCmdFor toggles name in the layer that OWNS the winning entry
// (inv.Winner[name]) — never a shadowed copy — and refuses with no write
// when that layer resolves to no file (empty root ⇒ layer disabled).
func mcpToggleCmdFor(paths mcp.Paths, name string) tea.Cmd {
	return func() tea.Msg {
		inv := mcp.ReadInventory(paths)
		path := mcp.LayerFile(paths, inv.Winner[name])
		if path == "" {
			return mcpToggleResultMsg{name: name, err: errMCPOwnerNoPath}
		}
		enabled := inv.Effective[name] == "enabled"
		// SetDisabled(path, name, disabled): passing the CURRENT enabled
		// state as the "disabled" flag flips it.
		res, err := mcp.SetDisabled(path, name, enabled)
		if err != nil {
			return mcpToggleResultMsg{name: name, err: err}
		}
		return mcpToggleResultMsg{name: name, enabled: !enabled, changed: res.Changed}
	}
}

// ── handleMCPToggleResult processes a toggle result ────────────────────

// handleMCPToggleResult writes the feedback line and, on success, calls
// the agent loop's SyncMCPServers (mcp.json is not config-watched — that
// pass is what makes the write visible to the tool/prompt surface) and
// reloads the list.
func (m *Model) handleMCPToggleResult(msg mcpToggleResultMsg) tea.Cmd {
	switch {
	case msg.err == nil:
		state := i18n.T("tui.mcpEnabled")
		if !msg.enabled {
			state = i18n.T("tui.mcpDisabled")
		}
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpToggleSuccess"), msg.name, state)
		if m.agentLoop != nil {
			m.agentLoop.SyncMCPServers()
		}
	case errors.Is(msg.err, errMCPUnavailable):
		m.mcpFeedback = i18n.T("tui.mcpUnavailable")
	case errors.Is(msg.err, errMCPOwnerNoPath):
		m.mcpFeedback = i18n.T("tui.mcpOwnerNoPath")
	default:
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpToggleFailed"), msg.name, msg.err)
	}
	m.loadMCPList()
	return m.tickCmd()
}

// ── Formatting helpers ─────────────────────────────────────────────────

// formatMCPItem renders one row: status glyph (● enabled / ○ disabled /
// ! invalid), the name padded with formatSkillItem's %-15s rule, then the
// owning-layer tag.
func formatMCPItem(name, layer, effective string) string {
	status := "●"
	switch effective {
	case "disabled":
		status = "○"
	case "invalid":
		status = "!"
	}
	return fmt.Sprintf("%s %-15s — [%s]", status, name, layer)
}
