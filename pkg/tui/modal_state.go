package tui

import (
	"time"

	"github.com/xilistudios/lele/pkg/mcp"
)

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
	m.commandsModalKeys = nil
	m.commandsRows = nil
	m.commandsDetailMode = false
	m.commandsDetailKey = ""
	m.commandsFeedback = ""
	m.commandsEditKey = ""
	m.commandsEditScope = ""
	m.commandsDeleteKey = ""
	m.commandsDeleteArmed = time.Time{}
	m.mcpModalKeys = nil
	m.mcpFilter = mcpFilterAll
	m.mcpFeedback = ""
	m.mcpInventory = mcp.Inventory{}
	m.mcpInventoryValid = false
	m.mcpDetailName = ""
	m.mcpAuth = nil
	m.mcpAuthCancel()
	m.connectOAuthCancel()
	m.formStepIndex = 0
	m.formValues = nil
	m.formError = ""
	m.formConfirmMode = false
	m.providerModalKeys = nil
	m.providerSelectedName = ""
	m.providerEditMode = false
	m.providerDetailRows = nil
	m.providerDetailEditIdx = -1
	m.providerDetailAddIdx = -1
	m.providerDetailDelIdx = -1
	m.providerDeleteKey = ""
	m.providerDeleteArmed = time.Time{}
	m.providerFeedback = ""
	m.modelEditAlias = ""
	m.modelEditOrigModel = ""
	m.providerTypePicker = false
	m.providerTypePickerIdx = 0
	m.providerTypePickerMax = 0
	m.connectRowPicker = false
	m.connectRowIdx = -1
	m.connectSkipAPIBase = false
	m.connectAlreadyUsable = false
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
	m.syncTextInputEcho()
}

func isFormModal(mode modalType, editingField bool) bool {
	switch mode {
	case ModalAddProvider, ModalAddModel, ModalEditModel, ModalEditProvider, ModalAddSecret, ModalSkillInstall, ModalAddCommand:
		return true
	case ModalMCPAdd:
		return true
	case ModalMCPAuth:
		return true
	case ModalConnectOAuth:
		return true
	case ModalSettingsAgents, ModalSettingsAgentEdit, ModalSettingsSystemEdit, ModalSettingsTUI:
		return editingField
	default:
		return false
	}
}

func isListModal(mode modalType) bool {
	switch mode {
	case ModalNone, ModalAddProvider, ModalAddModel, ModalEditModel, ModalEditProvider, ModalAddSecret, ModalSkillInstall, ModalMCPAuth, ModalMCPAdd, ModalConnectOAuth:
		return false
	case ModalSettings, ModalSettingsAgents, ModalSettingsAgentEdit, ModalSettingsSystem, ModalSettingsSystemEdit, ModalSettingsTUI:
		return true
	case ModalMCP, ModalMCPDetail:
		return true
	default:
		return true
	}
}
