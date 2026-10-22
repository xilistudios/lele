package tui

import (
	"time"

	"github.com/xilistudios/lele/pkg/mcp"
)

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
	// Drop the inventory snapshot the previous list was built from: a stale
	// detail must never render after the modal is reopened (/mcp → resetModal).
	m.mcpInventory = mcp.Inventory{}
	m.mcpInventoryValid = false
	m.mcpDetailName = ""
	// The auth verdicts are part of that same snapshot and go with it.
	m.mcpAuth = nil
	// Release a live OAuth flow too. /mcp (and every other command that
	// opens a modal) funnels through here, and the flow owns a loopback
	// listener until it is cancelled: without this, reopening /mcp while an
	// authorization is pending would pin that port for the flow's whole
	// five-minute budget (the old flow's messages are invalidated by the
	// generation bump, so nothing can adopt it later).
	m.mcpAuthCancel()
	m.formStepIndex = 0
	m.formValues = nil
	m.formError = ""
	m.formConfirmMode = false
	m.providerModalKeys = nil
	m.providerSelectedName = ""
	m.providerEditMode = false
	m.providerDetailRows = nil
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
	case ModalAddProvider, ModalAddModel, ModalEditModel, ModalAddSecret, ModalSkillInstall, ModalAddCommand:
		return true
	// The MCP-add wizard (ModalMCPAdd) collects one single-line value per
	// step in the shared text input, like every other form modal: typing
	// must reach the widget (a "q" is a regular character mid-word) and the
	// generic modal switch must not treat this modal as a navigable list.
	case ModalMCPAdd:
		return true
	// ModalMCPAuth collects a pasted authorization code in the shared text
	// input: it must be a "form modal" for the tail of handleModalKey to
	// forward printable keys to the widget (handleMCPAuthKey owns only
	// ENTER/CTRL+Y/ESC and passes the rest through on purpose — "q" included,
	// it is a regular character in a pasted code).
	case ModalMCPAuth:
		return true
	case ModalSettingsAgents, ModalSettingsAgentEdit, ModalSettingsSystemEdit, ModalSettingsTUI:
		return editingField
	default:
		return false
	}
}

// isListModal returns true if the modal type is a list-selection modal
// (navigable with up/down keys), as opposed to form-based modals.
//
// It is the gate the GENERIC modal switch uses for up/k/down/j (see
// handleModalKey), which is what actually keeps a modal out of list
// navigation — handleMCPKey only owns the keys of its own modals and forwards
// the rest. ModalMCPAuth is deliberately NOT a list modal: it has no rows, and
// with no top-level modal registered above it the default arm would let
// up/down move the invisible list cursor behind the modal while the user is
// only moving inside the code input.
func isListModal(mode modalType) bool {
	switch mode {
	case ModalNone, ModalAddProvider, ModalAddModel, ModalEditModel, ModalAddSecret, ModalSkillInstall, ModalMCPAuth, ModalMCPAdd:
		return false
	case ModalSettings, ModalSettingsAgents, ModalSettingsAgentEdit, ModalSettingsSystem, ModalSettingsSystemEdit, ModalSettingsTUI:
		return true
	case ModalMCP, ModalMCPDetail:
		return true
	default:
		return true
	}
}

// handleApproval processes the user's approval/rejection decision for a
// pending command approval. It calls the approval manager to unblock the
// agent goroutine and clears the pending state.
