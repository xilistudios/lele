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

var commandSources = []harness.Source{
	harness.SourceConfig,
	harness.SourceGlobal,
	harness.SourceWorkspace,
	harness.SourceDirectory,
}

type commandRow struct {
	Name               string
	Description        string
	Source             string
	Path               string
	Agent              string
	Model              string
	AllowShell         bool
	AllowAbsoluteFiles *bool
	ShadowedBy         string
	Effective          bool
}

func (r commandRow) key() string {
	return r.Source + ":" + r.Name
}

func (m *Model) commandsWorkspace() string {
	if m == nil || m.agentLoop == nil {
		return ""
	}
	prov := m.agentLoop.GetProvidable()
	if prov == nil {
		return ""
	}
	ws := ""
	if m.currentKey != "" {
		if id := prov.GetSessionAgent(m.currentKey); id != "" {
			if info, ok := prov.GetAgentInfo(id); ok {
				ws = info.Workspace
			}
		}
	}
	if ws == "" {
		if cfg := prov.GetConfigSnapshot(); cfg != nil {
			return cfg.WorkspacePath()
		}
		return ""
	}
	return filepath.Clean(ws)
}

func (m *Model) commandsManager(workspace string) *harness.Manager {
	if m == nil || m.agentLoop == nil {
		return nil
	}
	return m.agentLoop.HarnessManagerFor(workspace)
}

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

	levels, _ := mgr.Levels()
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

func nameOrDash(name string) string {
	if strings.TrimSpace(name) == "" {
		return "(?)"
	}
	return name
}

const commandsNewKey = "__new__"

func (m *Model) loadCommandsList() {
	m.modalItems = nil
	m.commandsModalKeys = nil
	m.commandsRows = nil

	rows := buildCommandRows(m.commandsManager(m.commandsWorkspace()))
	if len(rows) == 0 {
		m.modalItems = append(m.modalItems, i18n.T("tui.commands.none"))
		m.commandsModalKeys = append(m.commandsModalKeys, "")
		m.commandsRows = append(m.commandsRows, commandRow{})
	}

	for _, row := range rows {
		m.modalItems = append(m.modalItems, formatCommandItem(row))
		m.commandsModalKeys = append(m.commandsModalKeys, row.key())
		m.commandsRows = append(m.commandsRows, row)
	}

	m.modalItems = append(m.modalItems, "---")
	m.commandsModalKeys = append(m.commandsModalKeys, "")
	m.commandsRows = append(m.commandsRows, commandRow{})
	m.modalItems = append(m.modalItems, "+ "+i18n.T("tui.commands.newCommand"))
	m.commandsModalKeys = append(m.commandsModalKeys, commandsNewKey)
	m.commandsRows = append(m.commandsRows, commandRow{})
	m.clampModalCursor()
}

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

func (m *Model) selectedCommandKey() string {
	if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.commandsModalKeys) {
		return ""
	}
	return m.commandsModalKeys[m.modalSelectedIdx]
}

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

func (m *Model) exitCommandDetail() {
	key := m.commandsDetailKey
	m.commandsDetailMode = false
	m.commandsDetailKey = ""
	m.modalMode = ModalCommands
	m.loadCommandsList()
	m.reselectCommandRow(key)
}

func (m *Model) lookupCommandRow(key string) (commandRow, *harness.Manager, bool) {
	mgr := m.commandsManager(m.commandsWorkspace())
	for _, row := range buildCommandRows(mgr) {
		if row.key() == key {
			return row, mgr, true
		}
	}
	return commandRow{}, mgr, false
}

func (m *Model) renderCommandDetail() string {
	row, mgr, ok := m.lookupCommandRow(m.commandsDetailKey)
	if !ok {
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

	if preview := m.commandTemplatePreview(row, mgr); preview != "" {
		sb.WriteString("\n")
		sb.WriteString(labelStyle.Render(i18n.T("tui.commands.template") + ":\n"))
		sb.WriteString(CommentColorStyle.Render(preview) + "\n")
	}

	sb.WriteString("\n")
	sb.WriteString(CommentColorStyle.Render(i18n.T("tui.commands.detailHints")))
	if m.commandsFeedback != "" {
		sb.WriteString("\n" + lipgloss.NewStyle().Foreground(PrimaryColor).Render("  "+m.commandsFeedback) + "\n")
	}

	box := ModalContainer.Width(m.width - 10).Render(sb.String())
	return m.paintFrame(box)
}

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
		if raw, err := os.ReadFile(row.Path); err == nil {
			if cmd, err := harness.ParseCommandMarkdown(row.Name, row.Path, raw); err == nil && cmd != nil {
				template = cmd.Template
			}
		}
	}
	return previewTemplate(template, commandTemplateMaxLines, commandTemplateMaxCells)
}

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

func sourceBadge(source string) string {
	if source == "" {
		return "(unknown)"
	}
	return "[" + source + "]"
}

func commandPathDisplay(row commandRow) string {
	if row.Path != "" {
		return row.Path
	}
	if row.Source == string(harness.SourceConfig) {
		return "config.json"
	}
	return "(none)"
}

func orInherit(value string) string {
	if strings.TrimSpace(value) == "" {
		return CommentColorStyle.Render("(inherit)")
	}
	return value
}

func boolDisplay(value bool) string {
	style := CommentColorStyle
	if value {
		style = SuccessStyle
	}
	return style.Render(fmt.Sprintf("%v", value))
}

func triStateDisplay(value *bool) string {
	if value == nil {
		return CommentColorStyle.Render("inherit")
	}
	return boolDisplay(*value)
}
