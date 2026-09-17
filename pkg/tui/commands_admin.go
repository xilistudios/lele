package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/harness"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Custom (harness) slash-command administration, read-only part.
//
// The panel answers the question the palette cannot: for every name the agent
// could run, WHICH definition wins, where it comes from, and what it expands
// to. It mirrors the REST catalog (pkg/channels agentCommandRows) so the two
// UIs never disagree about precedence: the harness Manager is the only place
// that knows it, and this file never reimplements the merge.
//
// Part A is deliberately read-only: list + detail. Create/edit/delete land in a
// later part, which is why the row model already carries the composite key
// ("source:name") a write handler needs to target one specific level, and why
// the list renderer already has a feedback line to reuse.

// commandSources is the discovery order from lowest to highest precedence,
// matching harness.Manager's flatten order (config < global < workspace <
// directory). Listing in this order keeps same-name rows adjacent so the
// winner and its shadows read as one group.
var commandSources = []harness.Source{
	harness.SourceConfig,
	harness.SourceGlobal,
	harness.SourceWorkspace,
	harness.SourceDirectory,
}

// commandRow is one line of the /commands list: a single definition of a single
// discovery level (so a name defined twice produces two rows, the shadowed one
// carrying ShadowedBy). It is the TUI counterpart of channels.AgentCommandInfo.
type commandRow struct {
	Name        string
	Description string
	Source      string
	Path        string
	Agent       string
	Model       string
	AllowShell  bool
	// AllowAbsoluteFiles is tri-state: nil means "inherit the harness default".
	AllowAbsoluteFiles *bool
	// ShadowedBy is the source of the command that wins over this row; "" means
	// this row IS the effective command.
	ShadowedBy string
	// Effective mirrors ShadowedBy == "" for the renderers.
	Effective bool
}

// key is the composite identifier of the row, unique across levels so Enter can
// select exactly one definition even when two files declare the same name.
func (r commandRow) key() string {
	return r.Source + ":" + r.Name
}

// commandsWorkspace resolves the workspace whose commands the TUI must show:
// the one of the agent handling the CURRENT session, falling back to the
// defaults workspace. It mirrors pkg/agent harnessWorkspaceKey exactly (""
// selects cfg.WorkspacePath(); a non-empty path is only Cleaned, never
// expanded) so the panel lists what the agent would actually run.
func (m *Model) commandsWorkspace() string {
	if m == nil || m.agentLoop == nil {
		return ""
	}
	prov := m.agentLoop.GetProvidable()
	if prov == nil {
		return ""
	}
	// Raw workspace of the agent that handles this session; "" selects the
	// defaults below (identical to harnessWorkspaceKey's rule).
	ws := ""
	if m.currentKey != "" {
		if id := prov.GetSessionAgent(m.currentKey); id != "" {
			if info, ok := prov.GetAgentInfo(id); ok {
				ws = info.Workspace
			}
		}
	}
	if ws == "" {
		// GetConfigSnapshot lives on the providable (channels.AgentProvidable),
		// not on the loop itself.
		if cfg := prov.GetConfigSnapshot(); cfg != nil {
			return cfg.WorkspacePath()
		}
		return ""
	}
	return filepath.Clean(ws)
}

// commandsManager returns the cached command manager of a workspace, or nil when
// no backend loop is wired.
func (m *Model) commandsManager(workspace string) *harness.Manager {
	if m == nil || m.agentLoop == nil {
		return nil
	}
	return m.agentLoop.HarnessManagerFor(workspace)
}

// buildCommandRows flattens the four discovery levels of a manager into rows,
// tagging every shadowed definition. Same contract as agentCommandRows in
// pkg/channels/rest_agent_commands.go — precedence comes from the manager, never
// from a local reimplementation.
func buildCommandRows(mgr *harness.Manager) []commandRow {
	if mgr == nil {
		return nil
	}

	winner := map[string]harness.Source{}
	for _, cmd := range mgr.Registry().All() {
		if cmd != nil {
			winner[cmd.Name] = cmd.Source
		}
	}

	levels, _ := mgr.Levels() // per-level errors are already logged by the manager
	rows := make([]commandRow, 0)
	for _, source := range commandSources {
		for _, cmd := range levels[source] {
			if cmd == nil {
				continue
			}
			row := commandRow{
				Name:               cmd.Name,
				Description:        cmd.Description,
				Source:             string(cmd.Source),
				Path:               cmd.Path,
				Agent:              cmd.Agent,
				Model:              cmd.Model,
				AllowShell:         mgr.AllowShell(cmd),
				AllowAbsoluteFiles: cmd.AllowAbsoluteFiles,
			}
			if w, ok := winner[cmd.Name]; ok && w != cmd.Source {
				row.ShadowedBy = string(w)
			} else {
				row.Effective = true
			}
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Source < rows[j].Source
	})
	return rows
}

// formatCommandItem renders one list line: status marker, name, truncated
// description and the source badge. "●" is the effective definition, "○" one
// that the agent never runs because a higher-precedence level shadows it.
func formatCommandItem(row commandRow) string {
	status := "●"
	if !row.Effective {
		status = "○"
	}

	desc := truncateRightCells(row.Description, 50)

	sourceTag := ""
	if row.Source != "" {
		sourceTag = " [" + row.Source + "]"
	}
	hiddenTag := ""
	if row.ShadowedBy != "" {
		hiddenTag = " (" + i18n.T("tui.commands.shadowedBy") + ": " + row.ShadowedBy + ")"
	}

	return fmt.Sprintf("%s %-15s — %s%s%s", status, nameOrDash(row.Name), desc, sourceTag, hiddenTag)
}

// nameOrDash keeps a malformed (nameless) row from collapsing the label.
func nameOrDash(name string) string {
	if strings.TrimSpace(name) == "" {
		return "(?)"
	}
	return name
}

// commandsNewKey is the pseudo-key of the "+ New command" action row, the
// skills panel's "__install__" equivalent. It is deliberately not a valid
// "source:name" composite, so every row lookup misses it by construction.
const commandsNewKey = "__new__"

// loadCommandsList refreshes the custom-command list of the current agent's
// workspace. modalItems, commandsModalKeys and commandsRows stay 1:1 so Enter
// and any later per-row action index correctly. The list ends with the
// "+ New command" action (create is always workspace-scoped).
func (m *Model) loadCommandsList() {
	m.modalItems = nil
	m.commandsModalKeys = nil
	m.commandsRows = nil

	rows := buildCommandRows(m.commandsManager(m.commandsWorkspace()))
	if len(rows) == 0 {
		// Empty state: one non-selectable row (key "") so the cursor and the
		// index math behave exactly like a normal list.
		m.modalItems = append(m.modalItems, i18n.T("tui.commands.none"))
		m.commandsModalKeys = append(m.commandsModalKeys, "")
		m.commandsRows = append(m.commandsRows, commandRow{})
	}

	for _, row := range rows {
		m.modalItems = append(m.modalItems, formatCommandItem(row))
		m.commandsModalKeys = append(m.commandsModalKeys, row.key())
		m.commandsRows = append(m.commandsRows, row)
	}

	// Separator + create action, mirroring the skills panel's install row.
	m.modalItems = append(m.modalItems, "---")
	m.commandsModalKeys = append(m.commandsModalKeys, "")
	m.commandsRows = append(m.commandsRows, commandRow{})
	m.modalItems = append(m.modalItems, "+ "+i18n.T("tui.commands.newCommand"))
	m.commandsModalKeys = append(m.commandsModalKeys, commandsNewKey)
	m.commandsRows = append(m.commandsRows, commandRow{})
	m.clampModalCursor()
}

// reselectCommandRow restores the cursor to the row with the given composite
// key after the list has been reloaded (returning from the detail view, or any
// future write). While the detail screen is open the up/down keys still move the
// hidden list cursor, so without this the selection would drift under the user.
func (m *Model) reselectCommandRow(key string) {
	if key == "" {
		return
	}
	for i, k := range m.commandsModalKeys {
		if k == key {
			m.modalSelectedIdx = i
			m.clampModalCursor()
			return
		}
	}
	m.clampModalCursor()
}

// selectedCommandKey returns the composite key of the highlighted row, or ""
// when the selection is out of range or points at a non-command row.
func (m *Model) selectedCommandKey() string {
	if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.commandsModalKeys) {
		return ""
	}
	return m.commandsModalKeys[m.modalSelectedIdx]
}

// handleCommandsEnter opens the detail view of the selected command. Separators
// and the empty-state row (key "") are inert.
func (m *Model) handleCommandsEnter() tea.Cmd {
	key := m.selectedCommandKey()
	if key == commandsNewKey {
		m.startCommandCreate()
		return m.tickCmd()
	}
	if key == "" {
		return nil
	}
	m.commandsDetailMode = true
	m.commandsDetailKey = key
	m.modalMode = ModalCommandDetail
	m.modalScrollOffset = 0
	return m.tickCmd()
}

// exitCommandDetail returns from the detail view to the list: state cleared,
// catalog re-read (so out-of-band file edits show up) and the cursor put back on
// the row the detail was opened from.
func (m *Model) exitCommandDetail() {
	key := m.commandsDetailKey
	m.commandsDetailMode = false
	m.commandsDetailKey = ""
	m.modalMode = ModalCommands
	m.loadCommandsList()
	m.reselectCommandRow(key)
}

// lookupCommandRow re-reads the catalog and returns the row with a composite
// key. Re-reading (instead of trusting the cached row) is what makes the detail
// view honest after an out-of-band file edit.
func (m *Model) lookupCommandRow(key string) (commandRow, *harness.Manager, bool) {
	mgr := m.commandsManager(m.commandsWorkspace())
	for _, row := range buildCommandRows(mgr) {
		if row.key() == key {
			return row, mgr, true
		}
	}
	return commandRow{}, mgr, false
}

// renderCommandDetail renders the read-only detail of one command, following the
// cron/secret detail pattern (title line + labelled fields + hint footer).
func (m *Model) renderCommandDetail() string {
	row, mgr, ok := m.lookupCommandRow(m.commandsDetailKey)
	if !ok {
		// The definition disappeared (file deleted, config reloaded): fall back
		// to the list instead of showing a stale detail.
		m.commandsDetailMode = false
		m.modalMode = ModalCommands
		m.loadCommandsList()
		return m.renderModal(m.modalTitleFor(ModalCommands))
	}

	var sb strings.Builder

	statusText := i18n.T("tui.commands.effective")
	statusColor := SecondaryColor
	if !row.Effective {
		statusText = i18n.T("tui.commands.shadowedBy") + ": " + row.ShadowedBy
		statusColor = CommentColor
	}
	titleLine := lipgloss.JoinHorizontal(lipgloss.Center,
		TitleStyle.Render("/"+nameOrDash(row.Name)),
		"  ",
		lipgloss.NewStyle().Foreground(statusColor).Render("["+statusText+"]"),
	)
	// Eyebrow: the section the command came from, so the detail screen never
	// looks like a different panel when reached from the list.
	sb.WriteString(CommentColorStyle.Render(i18n.T("tui.commands.title")) + "\n")
	sb.WriteString(titleLine + "\n\n")

	labelStyle := lipgloss.NewStyle().Foreground(AccentColor)
	addField := func(label, value string) {
		sb.WriteString(labelStyle.Render(label+": ") + value + "\n")
	}

	if row.Description != "" {
		addField(i18n.T("tui.commands.description"), row.Description)
	}
	addField(i18n.T("tui.commands.source"), sourceBadge(row.Source))
	addField(i18n.T("tui.commands.path"), commandPathDisplay(row))
	addField(i18n.T("tui.commands.agent"), orInherit(row.Agent))
	addField(i18n.T("tui.commands.model"), orInherit(row.Model))
	addField(i18n.T("tui.commands.allowShell"), boolDisplay(row.AllowShell))
	addField(i18n.T("tui.commands.allowAbsFiles"), triStateDisplay(row.AllowAbsoluteFiles))

	// Template preview: the merged definition of this name (which for the
	// effective row IS this row's template). Config-defined commands live in the
	// registry too, so one lookup covers every source; reading the file is only
	// the fallback for a level the registry dropped.
	if preview := m.commandTemplatePreview(row, mgr); preview != "" {
		sb.WriteString("\n")
		sb.WriteString(labelStyle.Render(i18n.T("tui.commands.template") + ":\n"))
		sb.WriteString(CommentColorStyle.Render(preview) + "\n")
	}

	sb.WriteString("\n")
	sb.WriteString(CommentColorStyle.Render(i18n.T("tui.commands.detailHints")))

	box := ModalContainer.Width(m.width - 10).Render(sb.String())
	return m.paintFrame(box)
}

// commandTemplatePreview returns the first lines of the command template,
// bounded by both line and cell count so a huge prompt cannot push the hints off
// the screen.
const (
	commandTemplateMaxLines = 15
	commandTemplateMaxCells = 600
)

func (m *Model) commandTemplatePreview(row commandRow, mgr *harness.Manager) string {
	template := ""
	if mgr != nil {
		if cmd, ok := mgr.Registry().Get(row.Name); ok && cmd != nil {
			template = cmd.Template
		}
	}
	if template == "" && row.Path != "" {
		// Registry miss (e.g. a shadowed level whose file is still on disk):
		// parse the file so the detail view can show what that level declares.
		if raw, err := os.ReadFile(row.Path); err == nil {
			if cmd, err := harness.ParseCommandMarkdown(row.Name, row.Path, raw); err == nil && cmd != nil {
				template = cmd.Template
			}
		}
	}
	return previewTemplate(template, commandTemplateMaxLines, commandTemplateMaxCells)
}

// previewTemplate cuts a template down to maxLines lines / maxCells cells and
// marks the truncation with an ellipsis.
func previewTemplate(template string, maxLines, maxCells int) string {
	template = strings.ReplaceAll(template, "\r\n", "\n")
	template = strings.TrimSpace(template)
	if template == "" {
		return ""
	}
	if maxLines <= 0 {
		maxLines = commandTemplateMaxLines
	}

	lines := strings.Split(template, "\n")
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	out := strings.Join(lines, "\n")
	if maxCells > 0 {
		cells := ansi.StringWidth(out)
		if cells > maxCells {
			out = truncateRightCells(out, maxCells)
			truncated = true
		}
	}
	if truncated && !strings.HasSuffix(out, "…") {
		out += " …"
	}
	return out
}

// sourceBadge renders a discovery level as a bracketed badge.
func sourceBadge(source string) string {
	if source == "" {
		return "(unknown)"
	}
	return "[" + source + "]"
}

// commandPathDisplay shows where a definition lives. config commands have no
// file of their own — their path is the config file.
func commandPathDisplay(row commandRow) string {
	if row.Path != "" {
		return row.Path
	}
	if row.Source == string(harness.SourceConfig) {
		return "config.json"
	}
	return "(none)"
}

// orInherit renders an optional string override.
func orInherit(value string) string {
	if strings.TrimSpace(value) == "" {
		return CommentColorStyle.Render("(inherit)")
	}
	return value
}

// boolDisplay renders a resolved permission as yes/no.
func boolDisplay(value bool) string {
	style := CommentColorStyle
	if value {
		style = SuccessStyle
	}
	return style.Render(fmt.Sprintf("%v", value))
}

// triStateDisplay renders the allow_absolute_files tri-state: an unset pointer
// inherits the harness-wide default, which is the distinction the harness
// documents and the one a plain bool would hide.
func triStateDisplay(value *bool) string {
	if value == nil {
		return CommentColorStyle.Render("inherit")
	}
	return boolDisplay(*value)
}
