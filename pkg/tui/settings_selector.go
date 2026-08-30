package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// startSettingsSelector activates the inline selector picker for the given
// field. It populates selectorItems/selectorValues from the provided options
// and pre-selects the current value.
func (m *Model) startSettingsSelector(field, currentValue string, labels, values []string) {
	m.settingsSelectorActive = true
	m.settingsSelectorField = field
	m.settingsSelectorItems = labels
	m.settingsSelectorValues = values
	m.settingsSelectorIdx = 0
	m.settingsSelectorOrig = currentValue

	// Pre-select the current value if it matches one of the options.
	for i, v := range values {
		if v == currentValue {
			m.settingsSelectorIdx = i
			break
		}
	}
}

// closeSettingsSelector deactivates the selector and clears its state.
func (m *Model) closeSettingsSelector() {
	m.settingsSelectorActive = false
	m.settingsSelectorItems = nil
	m.settingsSelectorValues = nil
	m.settingsSelectorIdx = 0
	m.settingsSelectorField = ""
	m.settingsSelectorOrig = ""
}

// renderSettingsSelector renders the inline selector picker. It shows a
// scrollable list of options with ✓ marking the current value and > marking
// the highlighted row. Returns the painted frame.
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
		// Mark the current config value with ✓
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

// handleSelectorNavigation handles up/down/j/k navigation within the selector.
// Returns true if the key was consumed.
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

// handleSelectorConfirm handles Enter in the selector. It saves the selected
// value to config and closes the selector. The field name determines which
// config field to update.
func (m *Model) handleSelectorConfirm() tea.Cmd {
	if !m.settingsSelectorActive || m.settingsSelectorIdx >= len(m.settingsSelectorValues) {
		return nil
	}

	value := m.settingsSelectorValues[m.settingsSelectorIdx]
	field := m.settingsSelectorField
	orig := m.settingsSelectorOrig

	// Close selector first so the save handlers see a clean state.
	m.closeSettingsSelector()

	// "(custom…)" — don't save; open the free-text input pre-filled with the
	// current value so the user can type an arbitrary reference.
	if value == modelCustomValue {
		m.settingsEditField = field
		m.textInput.SetValue(orig)
		m.textInput.Focus()
		return nil
	}

	// Delegate to the existing save handlers by setting settingsEditField
	// and calling the appropriate input handler.
	m.settingsEditField = field

	// Determine which handler to use based on the current modal.
	switch m.modalMode {
	case ModalSettingsSystemEdit:
		m.handleSystemSettingsInput(value)
	case ModalSettingsAgentEdit:
		m.handleAgentSettingsInput(value)
	}

	m.settingsEditField = ""
	return nil
}

// handleSelectorCancel handles Esc in the selector. It closes the selector
// and reloads the current view.
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
