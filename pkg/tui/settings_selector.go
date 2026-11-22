package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func (m *Model) startSettingsSelector(field, currentValue string, labels, values []string) {
	m.settingsSelectorActive = true
	m.settingsSelectorField = field
	m.settingsSelectorItems = labels
	m.settingsSelectorValues = values
	m.settingsSelectorIdx = 0
	m.settingsSelectorOrig = currentValue

	for i, v := range values {
		if v == currentValue {
			m.settingsSelectorIdx = i
			break
		}
	}
}

func (m *Model) closeSettingsSelector() {
	m.settingsSelectorActive = false
	m.settingsSelectorItems = nil
	m.settingsSelectorValues = nil
	m.settingsSelectorIdx = 0
	m.settingsSelectorField = ""
	m.settingsSelectorOrig = ""
}

func (m *Model) renderSettingsSelector(title string) string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(title) + "\n\n")

	maxVisible := m.maxModalVisible()
	if maxVisible < 3 {
		maxVisible = 3
	}

	scrollOffset := 0
	if m.settingsSelectorIdx >= maxVisible {
		scrollOffset = m.settingsSelectorIdx - maxVisible + 1
	}

	endIdx := scrollOffset + maxVisible
	if endIdx > len(m.settingsSelectorItems) {
		endIdx = len(m.settingsSelectorItems)
	}

	for i := scrollOffset; i < endIdx; i++ {
		label := m.settingsSelectorItems[i]
		isCurrent := false
		if i < len(m.settingsSelectorValues) && m.settingsSelectorValues[i] == m.settingsSelectorOrig {
			isCurrent = true
		}
		if isCurrent {
			label = label + " ✓"
		}
		if i == m.settingsSelectorIdx {
			sb.WriteString(ModalItemActive.Render(fmt.Sprintf("› %s", label)) + "\n")
		} else {
			sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  %s", label)) + "\n")
		}
	}

	sb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.settings.selectorHint")))

	modalView := ModalContainer.Render(sb.String())
	return m.paintFrame(modalView)
}

func (m *Model) handleSelectorNavigation(msg tea.KeyMsg) bool {
	if !m.settingsSelectorActive {
		return false
	}
	switch msg.String() {
	case "up", "k":
		if m.settingsSelectorIdx > 0 {
			m.settingsSelectorIdx--
		}
		return true
	case "down", "j":
		if m.settingsSelectorIdx < len(m.settingsSelectorItems)-1 {
			m.settingsSelectorIdx++
		}
		return true
	}
	return false
}

func (m *Model) handleSelectorConfirm() tea.Cmd {
	if !m.settingsSelectorActive || m.settingsSelectorIdx >= len(m.settingsSelectorValues) {
		return nil
	}

	value := m.settingsSelectorValues[m.settingsSelectorIdx]
	field := m.settingsSelectorField
	orig := m.settingsSelectorOrig

	if value == "__custom__" {
		m.closeSettingsSelector()
		m.settingsEditField = field
		m.formError = ""
		m.textInput.SetValue(m.settingsSelectorOrig)
		m.textInput.Focus()
		return nil
	}

	m.closeSettingsSelector()

	if value == modelCustomValue {
		m.settingsEditField = field
		m.textInput.SetValue(orig)
		m.textInput.Focus()
		return nil
	}

	m.settingsEditField = field

	switch m.modalMode {
	case ModalSettingsSystemEdit:
		m.handleSystemSettingsInput(value)
	case ModalSettingsAgentEdit:
		m.handleAgentSettingsInput(value)
	}

	m.settingsEditField = ""
	return nil
}

func (m *Model) handleSelectorCancel() {
	if !m.settingsSelectorActive {
		return
	}
	m.closeSettingsSelector()
	m.settingsEditField = ""
	m.formError = ""

	switch m.modalMode {
	case ModalSettingsSystemEdit:
		m.reloadSystemSubView()
	case ModalSettingsAgentEdit:
		m.loadAgentDetail(m.settingsAgentID)
	}
}

func (m *Model) renderSubagentPicker(modalTitle string) string {
	maxVisible := m.maxModalVisible()
	if maxVisible < 3 {
		maxVisible = 3
	}

	if m.subagentPickerIdx < m.modalScrollOffset {
		m.modalScrollOffset = m.subagentPickerIdx
	}
	if m.subagentPickerIdx >= m.modalScrollOffset+maxVisible {
		m.modalScrollOffset = m.subagentPickerIdx - maxVisible + 1
	}
	if m.modalScrollOffset < 0 {
		m.modalScrollOffset = 0
	}

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(modalTitle) + "\n\n")

	endIdx := m.modalScrollOffset + maxVisible
	if endIdx > len(m.subagentPickerItems) {
		endIdx = len(m.subagentPickerItems)
	}

	for i := m.modalScrollOffset; i < endIdx; i++ {
		check := "[ ]"
		if m.subagentPickerSelected[i] {
			check = "[✓]"
		}
		label := check + " " + m.subagentPickerLabels[i]
		if i == m.subagentPickerIdx {
			sb.WriteString(ModalItemActive.Render("> "+label) + "\n")
		} else {
			sb.WriteString(ModalItemInactive.Render("  "+label) + "\n")
		}
	}

	sb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.settings.subagentPickerHint")))

	modalView := ModalContainer.Render(sb.String())
	return m.paintFrame(modalView)
}
