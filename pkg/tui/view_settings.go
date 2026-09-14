package tui

import (
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"

	"github.com/charmbracelet/lipgloss"
)

// renderTUISettings renders the Interface settings modal. When an inline edit
// is active (settingsEditField set) it shows the text input for the field;
// otherwise it renders the list of toggleable/editable settings.
func (m *Model) renderTUISettings(modalTitle string) string {
	if m.themePickerActive {
		return m.renderModal(i18n.T("tui.settings.themePickerTitle"))
	}

	if m.settingsEditField != "" {
		var sb strings.Builder
		sb.WriteString(TitleStyle.Render(modalTitle) + "\n\n")

		label := ""
		switch m.settingsEditField {
		case "maxMessages":
			label = i18n.T("tui.settings.maxMessages")
		case "streamThrottle":
			label = i18n.T("tui.settings.streamThrottle")
		}
		m.textInput.Width = 40
		sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  %s: %s", label, m.textInputView())) + "\n")

		if m.formError != "" {
			sb.WriteString("\n" + lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n")
		}
		sb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.settings.editHint")))

		modalView := ModalContainer.Render(sb.String())
		return m.paintFrame(modalView)
	}

	// List mode — reuse the standard scrollable modal renderer.
	return m.renderModal(modalTitle)
}

// systemSettingsTitle returns the localized title for the active system
// settings sub-view, or the generic System title when none is active.
func (m *Model) systemSettingsTitle() string {
	switch m.settingsSection {
	case sysSubViewName(sysGroupSession):
		return i18n.T("tui.settings.session")
	case sysSubViewName(sysGroupTools):
		return i18n.T("tui.settings.tools")
	case sysSubViewName(sysGroupLogs):
		return i18n.T("tui.settings.logs")
	case sysSubViewName(sysGroupLanguage):
		return i18n.T("tui.settings.language")
	case sysSubViewName(sysGroupGoal):
		return i18n.T("tui.settings.goal")
	case sysSubViewName(sysGroupUpdates):
		return i18n.T("tui.settings.updates")
	}
	return i18n.T("tui.settings.system")
}

// renderSystemSettingsEdit renders a system settings inline-edit view for the
// currently editing field (settingsEditField). It shows the text input plus an
// optional validation error.
func (m *Model) renderSystemSettingsEdit(title string) string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(title) + "\n\n")

	label := m.settingsEditField
	m.textInput.Width = 40
	sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  %s: %s", label, m.textInputView())) + "\n")

	if m.formError != "" {
		sb.WriteString("\n" + lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n")
	}
	sb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.settings.editHint")))

	modalView := ModalContainer.Render(sb.String())
	return m.paintFrame(modalView)
}

// renderAgentEditInput renders the inline edit view for the currently editing
// agent field (settingsEditField set). For delete confirmation it shows the
// confirm prompt instead of a text input. Returns to the list once committed.
func (m *Model) renderAgentEditInput() string {
	title := i18n.T("tui.settings.agents")
	if m.settingsAgentID != "" {
		title = m.settingsAgentID
	}

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(title) + "\n\n")

	if m.settingsEditField == "confirmDelete" {
		sb.WriteString(ModalItemActive.Render("  "+m.formError) + "\n")
		sb.WriteString(HelpStyle.Render("  " + i18n.T("tui.settings.confirmDeleteHint")))
	} else {
		label := m.settingsEditField
		fieldLabels := map[string]string{
			"agentName":                   "Name",
			"agentDescription":            "Description",
			"agentWorkspace":              "Workspace",
			"agentModel":                  i18n.T("tui.model"),
			"agentTemperature":            "Temperature",
			"agentSubagentsMaxConcurrent": "Subagents MaxConcurrent",
			"newAgentID":                  "Agent ID",
			"confirmDelete":               "",
		}
		if l, ok := fieldLabels[m.settingsEditField]; ok && l != "" {
			label = l
		}
		m.textInput.Width = 60
		sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  %s: %s", label, m.textInputView())) + "\n")
		if m.formError != "" {
			sb.WriteString("\n" + lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n")
		}
		sb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.settings.editHint")))
	}

	modalView := ModalContainer.Render(sb.String())
	return m.paintFrame(modalView)
}

// renderBgExecOutput renders the output view for a background process.
func (m *Model) renderBgExecOutput() string {
	statusColor := CommentColor
	switch m.bgExecViewStatus {
	case "running":
		statusColor = YellowColor
	case "completed":
		statusColor = SecondaryColor
	case "failed":
		statusColor = PrimaryColor
	}

	titleText := fmt.Sprintf("Background Process: %s", m.bgExecViewID)
	statusText := lipgloss.NewStyle().Foreground(statusColor).Render(fmt.Sprintf("[%s]", m.bgExecViewStatus))

	titleLine := lipgloss.JoinHorizontal(lipgloss.Center,
		TitleStyle.Render(titleText),
		"  ",
		statusText,
	)

	outputContent := m.bgExecViewOutput
	if outputContent == "" {
		outputContent = CommentColorStyle.Render("(no output)")
	}
	outputContent = sanitizeDisplayText(outputContent)

	availableHeight := m.height - 8
	if availableHeight < 3 {
		availableHeight = 3
	}

	outputLines := strings.Split(outputContent, "\n")
	if len(outputLines) > availableHeight {
		outputLines = outputLines[len(outputLines)-availableHeight:]
	}
	outputContent = strings.Join(outputLines, "\n")

	hintsText := CommentColorStyle.Render(i18n.T("tui.bgOutputHints"))

	var sb strings.Builder
	sb.WriteString(titleLine + "\n\n")
	sb.WriteString(outputContent + "\n\n")
	sb.WriteString(hintsText)

	outputBox := ModalContainer.Width(m.width - 10).Render(sb.String())
	return m.paintFrame(outputBox)
}

// renderSkillPicker renders a multi-select modal for choosing skills to install.
func (m *Model) renderSkillPicker(modalTitle string) string {
	maxVisible := m.maxModalVisible()

	if m.modalSelectedIdx < m.modalScrollOffset {
		m.modalScrollOffset = m.modalSelectedIdx
	}
	if m.modalSelectedIdx >= m.modalScrollOffset+maxVisible {
		m.modalScrollOffset = m.modalSelectedIdx - maxVisible + 1
	}
	if m.modalScrollOffset < 0 {
		m.modalScrollOffset = 0
	}

	var modalSb strings.Builder
	modalSb.WriteString(TitleStyle.Render(modalTitle) + "\n")

	if m.skillsScanRepo != "" {
		modalSb.WriteString(CommentColorStyle.Render("  Repo: "+m.skillsScanRepo) + "\n")
	}
	modalSb.WriteString("\n")

	if m.skillsSelectedMap == nil {
		m.skillsSelectedMap = make(map[int]bool)
		for i := range m.skillsScanResults {
			m.skillsSelectedMap[i] = true
		}
	}

	endIdx := m.modalScrollOffset + maxVisible
	if endIdx > len(m.skillsScanResults) {
		endIdx = len(m.skillsScanResults)
	}

	for i := m.modalScrollOffset; i < endIdx; i++ {
		if i >= len(m.skillsScanResults) {
			break
		}
		skill := m.skillsScanResults[i]
		selected := m.skillsSelectedMap[i]
		item := formatPickerItem(skill.Name, skill.Description, selected)

		if i == m.modalSelectedIdx {
			modalSb.WriteString(ModalItemActive.Render("> "+item) + "\n")
		} else {
			modalSb.WriteString(ModalItemInactive.Render("  "+item) + "\n")
		}
	}

	if m.formError != "" {
		modalSb.WriteString("\n" + lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n")
	}

	modalSb.WriteString("\n" + CommentColorStyle.Render("  "+i18n.T("tui.skillPickerHints")) + "\n")

	modalView := ModalContainer.Render(modalSb.String())
	return m.paintFrame(modalView)
}
