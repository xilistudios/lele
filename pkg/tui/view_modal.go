package tui

import (
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// modalTitleFor returns the localized title for a modal mode. Shared by both
// View paths (welcome and split-column) so titles cannot drift.
func (m *Model) modalTitleFor(mode modalType) string {
	switch mode {
	case ModalAgent:
		return i18n.T("tui.selectAgent")
	case ModalModel:
		return i18n.T("tui.selectModel")
	case ModalSessions:
		return i18n.T("tui.selectChat")
	case ModalSubagents:
		return i18n.T("tui.selectSubagent")
	case ModalThink:
		return i18n.T("tui.selectThinkLevel")
	case ModalLang:
		return i18n.T("tui.selectLanguage")
	case ModalLangRemote:
		return i18n.T("tui.languages.downloadMore")
	case ModalBackgroundExecs:
		return i18n.T("tui.backgroundProcesses")
	case ModalCron:
		return i18n.T("tui.cronJobs")
	case ModalSecrets:
		return m.secretsHeader()
	case ModalProviders:
		return i18n.T("tui.selectProvider")
	case ModalProviderDetail:
		return i18n.T("tui.providerDetail")
	case ModalAddProvider:
		return i18n.T("tui.addProvider")
	case ModalAddModel:
		return i18n.T("tui.addModel")
	case ModalAddSecret:
		return i18n.T("tui.addSecret")
	case ModalSkills:
		return i18n.T("tui.skills")
	case ModalSkillInstall:
		return i18n.T("tui.installSkill")
	case ModalSkillPicker:
		return i18n.T("tui.selectSkills")
	case ModalCommands:
		return i18n.T("tui.commands")
	case ModalCommandDetail:
		return i18n.T("tui.commands.detail")
	case ModalAddCommand:
		// One wizard serves both flows; the title says which.
		if m.commandsEditKey != "" {
			return i18n.T("tui.commands.edit")
		}
		return i18n.T("tui.commands.new")
	case ModalSettings, ModalSettingsAgents, ModalSettingsAgentEdit,
		ModalSettingsSystem, ModalSettingsSystemEdit, ModalSettingsTUI:
		title := i18n.T("tui.settings.title")
		switch mode {
		case ModalSettingsAgents:
			title += " › " + i18n.T("tui.settings.agents")
		case ModalSettingsAgentEdit:
			agentLabel := i18n.T("tui.settings.agentDefaults")
			if m.settingsAgentID != "" {
				agentLabel = m.settingsAgentID
			}
			title += " › " + i18n.T("tui.settings.agents") + " › " + agentLabel
		case ModalSettingsSystem:
			title += " › " + i18n.T("tui.settings.system")
		case ModalSettingsSystemEdit:
			title += " › " + i18n.T("tui.settings.system") + " › " + m.systemSettingsTitle()
		case ModalSettingsTUI:
			title += " › " + i18n.T("tui.settings.interface")
		}
		return title
	}
	return ""
}

// renderActiveModal renders the currently open modal. Both View paths
// (welcome and split-column) must use this single dispatcher so every
// modalType has a title and a specialized renderer.
func (m *Model) renderActiveModal() string {
	// Detail overlays take precedence over their parent lists.
	if m.modalMode == ModalBackgroundExecs && m.bgExecViewMode {
		return m.renderBgExecOutput()
	}
	if m.modalMode == ModalCron && m.cronDetailMode {
		return m.renderCronDetail()
	}
	if m.modalMode == ModalSecrets && m.secretsDetailMode {
		return m.renderSecretDetail()
	}

	title := m.modalTitleFor(m.modalMode)

	switch m.modalMode {
	case ModalAddProvider, ModalAddModel, ModalAddSecret:
		return m.renderFormModal(title, m.formStepNames())
	case ModalSecrets:
		return m.renderSecretsList(title)
	case ModalSkillInstall:
		return m.renderFormModal(title, []string{i18n.T("tui.skillRepoPlaceholder")})
	case ModalSkillPicker:
		return m.renderSkillPicker(title)
	case ModalCommandDetail:
		return m.renderCommandDetail()
	case ModalAddCommand:
		// The template step is multi-line and needs a textarea, which the
		// generic single-line form renderer cannot offer.
		if m.formStepIndex == commandTemplateStep {
			return m.renderCommandTemplateStep(title)
		}
		return m.renderFormModal(title, m.formStepNames())
	case ModalSettingsTUI:
		return m.renderTUISettings(title)
	case ModalSettingsAgents:
		// The add-agent flow (settingsEditField == "newAgentID") and its
		// validation errors render in the inline edit view; without this the
		// text input is focused but never shown, so "Add Agent" appears dead.
		if m.settingsEditField != "" {
			return m.renderAgentEditInput()
		}
		return m.renderModal(title)
	case ModalSettingsAgentEdit:
		if m.subagentPickerActive {
			return m.renderSubagentPicker(title)
		}
		if m.settingsSelectorActive {
			return m.renderSettingsSelector(title)
		}
		if m.settingsEditField != "" {
			return m.renderAgentEditInput()
		}
		return m.renderModal(title)
	case ModalSettingsSystemEdit:
		if m.settingsSelectorActive {
			return m.renderSettingsSelector(title)
		}
		if m.settingsEditField != "" {
			return m.renderSystemSettingsEdit(title)
		}
		return m.renderModal(title)
	}
	return m.renderModal(title)
}

// maxModalVisible returns the maximum number of items visible in a modal given terminal height.
func (m *Model) maxModalVisible() int {
	maxVisible := m.height - 8
	if maxVisible < 3 {
		maxVisible = 3
	}
	if maxVisible > len(m.modalItems) {
		maxVisible = len(m.modalItems)
	}
	return maxVisible
}

// renderModal renders a modal overlay with scroll support for long lists.
func (m *Model) renderModal(modalTitle string) string {
	maxVisible := m.maxModalVisible()
	if len(m.modalItems) > maxVisible && maxVisible > 1 {
		maxVisible--
	}

	// Clamp scroll offset so selected item is always visible
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

	// Scroll indicator: show ↑ if there are items above
	if m.modalScrollOffset > 0 {
		modalSb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.moreAbove")) + "\n")
	} else {
		modalSb.WriteString("\n")
	}

	// Render only the visible window of items
	endIdx := m.modalScrollOffset + maxVisible
	if endIdx > len(m.modalItems) {
		endIdx = len(m.modalItems)
	}
	for i := m.modalScrollOffset; i < endIdx; i++ {
		// Theme picker: render section headers differently
		if m.themePickerActive && i < len(m.themePickerItems) {
			tpItem := m.themePickerItems[i]
			if tpItem.kind == "header" || tpItem.kind == "loading" || tpItem.kind == "error" {
				modalSb.WriteString(CommentColorStyle.Render(tpItem.label) + "\n")
				continue
			}
		}
		// Regular selectable items
		item := m.modalItems[i]
		if i == m.modalSelectedIdx {
			modalSb.WriteString(ModalItemActive.Render("> "+item) + "\n")
		} else {
			modalSb.WriteString(ModalItemInactive.Render("  "+item) + "\n")
		}
	}

	// Scroll indicator: show ↓ if there are items below
	if endIdx < len(m.modalItems) {
		modalSb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.moreBelow")) + "\n")
	}

	// Theme picker: show navigation hint at the bottom
	if m.themePickerActive {
		modalSb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.settings.themePickerHint")) + "\n")
	}

	// Skills action feedback
	if m.modalMode == ModalSkills {
		if m.skillsFeedback != "" {
			modalSb.WriteString("\n" + SuccessStyle.Render("  "+m.skillsFeedback) + "\n")
		}
		modalSb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.skillsListHints")) + "\n")
	}

	// Custom-command list: feedback line (reserved for the write flow) + hints.
	if m.modalMode == ModalCommands {
		if m.commandsFeedback != "" {
			modalSb.WriteString("\n" + SuccessStyle.Render("  "+m.commandsFeedback) + "\n")
		}
		modalSb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.commands.listHints")) + "\n")
	}

	modalView := ModalContainer.Render(modalSb.String())
	return m.paintFrame(modalView)
}
