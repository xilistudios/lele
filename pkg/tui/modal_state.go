package tui

// Modal lifecycle helpers: state reset and modal-kind classification.
func (m *Model) resetModal(mode modalType) {
	m.modalMode = mode
	m.modalScrollOffset = 0
	m.modalItems = nil
	m.modalSessionKeys = nil
	m.modalSubagentKeys = nil
	m.modalSelectedIdx = 0
	m.bgExecModalKeys = nil
	m.bgExecViewMode = false
	m.bgExecViewID = ""
	m.bgExecViewOutput = ""
	m.bgExecViewStatus = ""
	m.cronModalKeys = nil
	m.cronDetailMode = false
	m.cronDetailJobID = ""
	m.secretsModalKeys = nil
	m.secretsDetailMode = false
	m.secretsDetailName = ""
	m.secretsReveal = false
	m.formStepIndex = 0
	m.formValues = nil
	m.formError = ""
	m.formConfirmMode = false
	m.providerModalKeys = nil
	m.providerSelectedName = ""
	m.providerEditMode = false
	m.providerTypePicker = false
	m.providerTypePickerIdx = 0
	m.providerTypePickerMax = 0
	m.connectSuccess = false
	m.providerTypeFromPreset = false
	m.settingsSection = ""
	m.settingsEditField = ""
	m.settingsAgentID = ""
	m.settingsAgentKeys = nil
	m.settingsSelectorActive = false
	m.settingsSelectorItems = nil
	m.settingsSelectorValues = nil
	m.settingsSelectorIdx = 0
	m.settingsSelectorField = ""
	m.settingsSelectorOrig = ""
	m.subagentPickerActive = false
	m.subagentPickerItems = nil
	m.subagentPickerLabels = nil
	m.subagentPickerSelected = nil
	m.subagentPickerIdx = 0
	m.closeCatalogPicker()
	// Audit M2: a fresh modal is never on a secret step (formStepIndex was
	// just reset to 0), so this also clears any stale password echo.
	m.syncTextInputEcho()
}

// isFormModal returns true if the modal type is a form-based modal (or a
// settings modal with an active inline text edit), i.e. a modal where
// keystrokes like "q" must be forwarded to the text input instead of being
// treated as modal shortcuts.
func isFormModal(mode modalType, editingField bool) bool {
	switch mode {
	case ModalAddProvider, ModalAddModel, ModalAddSecret, ModalSkillInstall:
		return true
	case ModalSettingsAgents, ModalSettingsAgentEdit, ModalSettingsSystemEdit, ModalSettingsTUI:
		return editingField
	default:
		return false
	}
}

// isListModal returns true if the modal type is a list-selection modal
// (navigable with up/down keys), as opposed to form-based modals.
func isListModal(mode modalType) bool {
	switch mode {
	case ModalNone, ModalAddProvider, ModalAddModel, ModalAddSecret, ModalSkillInstall:
		return false
	case ModalSettings, ModalSettingsAgents, ModalSettingsAgentEdit, ModalSettingsSystem, ModalSettingsSystemEdit, ModalSettingsTUI:
		return true
	default:
		return true
	}
}

// handleApproval processes the user's approval/rejection decision for a
// pending command approval. It calls the approval manager to unblock the
// agent goroutine and clears the pending state.
