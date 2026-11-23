package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

func (m *Model) handleModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.syncTextInputEcho()
	if m.subagentPickerActive {
		switch msg.String() {
		case "up", "k":
			if m.subagentPickerIdx > 0 {
				m.subagentPickerIdx--
			}
			return m, nil
		case "down", "j":
			if m.subagentPickerIdx < len(m.subagentPickerItems)-1 {
				m.subagentPickerIdx++
			}
			return m, nil
		case " ":
			m.toggleSubagentPicker()
			return m, nil
		case "enter":
			m.saveSubagentPicker()
			return m, nil
		case "esc":
			m.cancelSubagentPicker()
			m.loadAgentDetail(m.settingsAgentID)
			return m, nil
		case "q":
			return m, nil
		}
	}
	if m.settingsSelectorActive {
		switch msg.String() {
		case "up", "k", "down", "j":
			m.handleSelectorNavigation(msg)
			return m, nil
		case "enter":
			return m, m.handleSelectorConfirm()
		case "esc":
			m.handleSelectorCancel()
			return m, nil
		case "q":
			return m, nil
		}
	}
	if m.addModelCatalogActive {
		switch msg.String() {
		case "up":
			if m.addModelCatalogIdx > 0 {
				m.addModelCatalogIdx--
			}
			return m, nil
		case "down":
			if m.addModelCatalogIdx < len(m.addModelCatalogIDs)-1 {
				m.addModelCatalogIdx++
			}
			return m, nil
		case "tab":
			if m.addModelCatalogIdx < len(m.addModelCatalogIDs) {
				m.textInput.SetValue(m.addModelCatalogIDs[m.addModelCatalogIdx])
				m.refreshCatalogSuggestions(m.textInput.Value())
			}
			return m, nil
		case "esc":
			m.closeCatalogPicker()
			return m, nil
		}
	}
	if m.modalMode == ModalAddProvider && m.providerTypePicker {
		max := m.providerTypePickerMax
		if max <= 0 {
			max = len(providerPresets) + 1
		}
		switch msg.String() {
		case "up", "k":
			if m.providerTypePickerIdx > 0 {
				m.providerTypePickerIdx--
				if m.providerTypePickerIdx < m.modalScrollOffset {
					m.modalScrollOffset = m.providerTypePickerIdx
				}
			}
			return m, nil
		case "down", "j":
			if m.providerTypePickerIdx < max-1 {
				m.providerTypePickerIdx++
			}
			return m, nil
		case "pgup":
			m.providerTypePickerIdx -= 10
			if m.providerTypePickerIdx < 0 {
				m.providerTypePickerIdx = 0
			}
			m.modalScrollOffset = m.providerTypePickerIdx
			return m, nil
		case "pgdown":
			m.providerTypePickerIdx += 10
			if m.providerTypePickerIdx > max-1 {
				m.providerTypePickerIdx = max - 1
			}
			return m, nil
		case "home":
			m.providerTypePickerIdx = 0
			m.modalScrollOffset = 0
			return m, nil
		case "end":
			m.providerTypePickerIdx = max - 1
			return m, nil
		case "esc":
			m.modalScrollOffset = 0
			if !m.connectRowPicker {
				m.providerTypePicker = false
				m.formStepIndex = 1
				m.textInput.SetValue("")
				m.textInput.Placeholder = "Provider type (e.g. openai, anthropic, openrouter)"
				return m, nil
			}
			m.providerTypePicker = false
			m.connectSuccess = false
			m.providerSavedInFlow = false
			m.formValues = nil
			m.formStepIndex = 0
			m.modalMode = ModalNone
			m.syncTextInputEcho()
			return m, nil
		case "q":
			return m, nil
		}
	}
	if m.settingsEditField != "" && !m.settingsSelectorActive && !m.subagentPickerActive {
		switch msg.String() {
		case "up", "k", "down", "j", "pgup", "pgdown":
			if isFormModal(m.modalMode, true) {
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				return m, cmd
			}
		}
	}
	if cmd, handled := m.handleMCPKey(msg); handled {
		return m, cmd
	}
	if m.modalMode == ModalConnectOAuth {
		if cmd, handled := m.handleConnectOAuthKey(msg); handled {
			return m, cmd
		}
	}
	switch msg.String() {
	case "up", "k":
		if isListModal(m.modalMode) && m.modalSelectedIdx > 0 {
			m.modalSelectedIdx--
			if m.modalSelectedIdx < m.modalScrollOffset {
				m.modalScrollOffset = m.modalSelectedIdx
			}
			m.clearCommandDeleteConfirm()
			m.clearProviderDeleteConfirm()
		}
		if m.modalMode == ModalSettingsTUI && m.themePickerActive && m.modalSelectedIdx < len(m.themePickerItems) {
			item := m.themePickerItems[m.modalSelectedIdx]
			if item.kind == "builtin" {
				m.previewTheme(item.name)
			} else if item.kind == "community" && theme.IsInstalledCommunity(item.name, m.installedCommunity) {
				m.previewTheme(item.name)
			}
		}
	case "down", "j":
		if isListModal(m.modalMode) && m.modalSelectedIdx < len(m.modalItems)-1 {
			m.modalSelectedIdx++
			m.clearCommandDeleteConfirm()
			m.clearProviderDeleteConfirm()
			maxVisible := m.height - 8
			if maxVisible < 3 {
				maxVisible = 3
			}
			if m.modalSelectedIdx >= m.modalScrollOffset+maxVisible {
				m.modalScrollOffset = m.modalSelectedIdx - maxVisible + 1
			}
		}
		if m.modalMode == ModalSettingsTUI && m.themePickerActive && m.modalSelectedIdx < len(m.themePickerItems) {
			item := m.themePickerItems[m.modalSelectedIdx]
			if item.kind == "builtin" {
				m.previewTheme(item.name)
			} else if item.kind == "community" && theme.IsInstalledCommunity(item.name, m.installedCommunity) {
				m.previewTheme(item.name)
			}
		}
	case "enter":
		if m.addModelCatalogActive {
			m.applyCatalogSelectionOnEnter()
		}
		if m.modalMode == ModalSettingsTUI && m.settingsEditField != "" {
			m.handleTUISettingsInput(m.textInput.Value())
			return m, nil
		}
		if m.modalMode == ModalAddProvider {
			if m.connectSuccess {
				m.connectSuccess = false
				m.providerSavedInFlow = false
				m.formStepIndex = 0
				m.formValues = nil
				m.modalMode = ModalNone
				m.syncTextInputEcho()
				m.reloadSessions()
				return m, nil
			}
			if m.providerTypePicker {
				sel := m.providerTypePickerIdx
				max := m.providerTypePickerMax
				if max <= 0 {
					max = len(connectRows())
				}
				if sel >= 0 && sel < max {
					rowFirst := m.connectRowPicker
					var row connectRow
					if rowFirst {
						var ok bool
						if row, ok = connectRowByIndex(sel); !ok {
							m.formError = i18n.T("tui.connectPickType")
							return m, nil
						}
					}
					m.providerTypePicker = false
					m.modalScrollOffset = 0
					m.formError = ""
					m.connectRowPicker = false
					if !rowFirst {
						m.applyLegacyTypePreset(sel)
					} else if cmd := m.applyConnectRow(sel, row); cmd != nil {
						return m, cmd
					}
					return m, nil
				}
				m.formError = i18n.T("tui.connectPickType")
				return m, nil
			}

			val := strings.TrimSpace(m.textInput.Value())
			if val == "" && m.formStepIndex < len(m.formValues) && m.formValues[m.formStepIndex] != "" {
				val = m.formValues[m.formStepIndex]
			}
			allowEmpty := (m.formStepIndex == 2 && !m.providerSavedInFlow) ||
				(m.formStepIndex >= 9 && m.providerSavedInFlow)
			if val == "" && !allowEmpty {
				m.formError = "This field is required"
				return m, nil
			}
			m.formError = ""
			m.formValues[m.formStepIndex] = val

			if !m.providerSavedInFlow {
				if m.formStepIndex == 0 {
					key := strings.ToLower(strings.TrimSpace(val))
					if m.cfg != nil && m.cfg.Providers != nil && m.cfg.Providers.Named != nil {
						if p, exists := m.cfg.Providers.Named[key]; exists && !p.IsEmpty() {
							m.formError = fmt.Sprintf("Provider %q already exists", key)
							return m, nil
						}
					}
					if row := connectRowAt(m.connectRowIdx); row != nil {
						switch row.kind {
						case connectRowCustomOpenAI, connectRowCustomAnthropic:
							m.providerTypeFromPreset = true
							m.formStepIndex = 2
							m.textInput.SetValue("")
							m.textInput.Placeholder = "API Key"
							if p := providerPresetByType(row.typ); p != nil && p.keyHint != "" {
								m.textInput.Placeholder = "API Key (" + p.keyHint + ")"
							}
							m.syncTextInputEcho()
							return m, nil
						}
					}
					m.formStepIndex = 1
					m.connectRowIdx = -1
					m.connectRowPicker = false
					m.providerTypePicker = true
					m.providerTypePickerIdx = 0
					m.modalScrollOffset = 0
					m.providerTypePickerMax = len(providerPresets) + 1
					m.textInput.SetValue("")
					m.textInput.Placeholder = ""
					return m, nil
				}
				if m.formStepIndex == 1 {
					typ := strings.ToLower(val)
					if p := providerPresetByType(typ); p != nil {
						m.providerTypeFromPreset = true
						m.formValues[3] = p.apiBase
					}
					m.formStepIndex = 2
					m.textInput.SetValue("")
					m.textInput.Placeholder = "API Key"
					if p := providerPresetByType(typ); p != nil && p.keyHint != "" {
						m.textInput.Placeholder = "API Key (" + p.keyHint + ")"
					}
					return m, nil
				}
				if m.formStepIndex == 2 {
					providerType := strings.ToLower(strings.TrimSpace(m.formValues[1]))
					isLocal := providerType == "ollama"
					if strings.TrimSpace(val) == "" && !isLocal {
						m.formError = "API key is required for this provider"
						return m, nil
					}
					m.formValues[2] = val
					if m.connectSkipAPIBase {
						if !m.connectSaveProvider() {
							return m, nil
						}
						if !m.connectRouteAfterProviderSave() {
							return m, nil
						}
						return m, nil
					}
					m.formStepIndex = 3
					m.textInput.SetValue("")
					if m.providerTypeFromPreset {
						p := providerPresetByType(m.formValues[1])
						if p != nil && p.apiBase != "" {
							m.textInput.SetValue(p.apiBase)
							m.textInput.Placeholder = "API Base URL (default for this provider)"
						} else {
							m.textInput.Placeholder = "API Base URL (e.g. https://api.example.com/v1)"
						}
					} else {
						m.textInput.Placeholder = "API Base URL (e.g. https://api.example.com/v1)"
					}
					return m, nil
				}
				if m.formStepIndex >= 3 {
					if !m.connectSaveProvider() {
						return m, nil
					}
					if !m.connectRouteAfterProviderSave() {
						return m, nil
					}
					return m, nil
				}
				m.formStepIndex++
				m.textInput.SetValue("")
				switch m.formStepIndex {
				case 1:
					m.textInput.Placeholder = "Provider type (e.g. openai, anthropic, openrouter)"
				case 2:
					m.textInput.Placeholder = "API Key"
				case 3:
					m.textInput.Placeholder = "API Base URL (e.g. https://api.openai.com/v1)"
				}
				return m, nil
			}

			if m.formStepIndex <= 8 {
				if m.formStepIndex == 6 || m.formStepIndex == 7 {
					if val != "" {
						if _, err := strconv.Atoi(val); err != nil {
							m.formError = "Must be a valid integer"
							return m, nil
						}
					}
				}
				if m.formStepIndex == 8 {
					lower := strings.ToLower(val)
					if lower != "yes" && lower != "no" {
						m.formError = "Enter 'yes' or 'no'"
						return m, nil
					}
					m.formValues[m.formStepIndex] = lower
					m.formStepIndex = 9
					m.textInput.SetValue("")
					m.textInput.Placeholder = "auto, deepseek, openai, openrouter, qwen or none"
					m.closeCatalogPicker()
					return m, nil
				}
				m.formStepIndex++
				nextVal := ""
				if m.formStepIndex < len(m.formValues) {
					nextVal = m.formValues[m.formStepIndex]
				}
				m.textInput.SetValue(nextVal)
				switch m.formStepIndex {
				case 5:
					m.textInput.Placeholder = "Actual model name (e.g. gpt-4o-2024-08-06)"
					m.startCatalogPickerIfNeeded()
				case 6:
					m.textInput.Placeholder = "Context window (e.g. 128000)"
					m.closeCatalogPicker()
				case 7:
					m.textInput.Placeholder = "Max tokens (e.g. 4096)"
					m.closeCatalogPicker()
				case 8:
					m.textInput.Placeholder = "Vision support? (yes/no)"
					m.closeCatalogPicker()
				}
				return m, nil
			}

			if m.formStepIndex == 9 {
				thinkType, err := thinkSystemFieldValue(val)
				if err != nil {
					m.formError = err.Error()
					return m, nil
				}
				m.formValues[9] = thinkType
				m.formStepIndex = 10
				m.textInput.SetValue("")
				m.textInput.Placeholder = ""
				return m, nil
			}

			ctxWin, _ := strconv.Atoi(m.formValues[6])
			maxTok, _ := strconv.Atoi(m.formValues[7])
			vision := m.formValues[8] == "yes"
			if err := m.addModelToProvider(m.providerSelectedName, m.formValues[4], m.formValues[5], ctxWin, maxTok, vision, m.formValues[9]); err != nil {
				m.formError = err.Error()
				return m, nil
			}
			if m.onboardingActive {
				m.modalMode = ModalNone
				m.connectSuccess = false
				m.providerSavedInFlow = false
				m.formStepIndex = 0
				m.formValues = nil
				m.syncTextInputEcho()
				m.onboardingStep = obVerify
				m.obVerifying = true
				m.obFinalizeSetup()
				return m, tea.Batch(m.obVerifyKeyCmd(), m.tickCmd())
			}
			m.connectSuccess = true
			m.providerSavedInFlow = false
			m.formStepIndex = 10
			return m, nil
		} else if m.modalMode == ModalAddModel || m.modalMode == ModalEditModel {
			lastStep := modelFormLastStep(m.modalMode)
			val := strings.TrimSpace(m.textInput.Value())
			if val == "" && m.formStepIndex < len(m.formValues) && m.formValues[m.formStepIndex] != "" {
				val = m.formValues[m.formStepIndex]
			}
			if val == "" && !modelFormAllowsEmptyValue(m.modalMode, m.formStepIndex) {
				m.formError = "This field is required"
				return m, nil
			}
			m.formError = ""
			switch m.formStepIndex {
			case addModelContextWindowStep, addModelMaxTokensStep:
				if val != "" {
					if _, err := strconv.Atoi(val); err != nil {
						m.formError = fmt.Sprintf("Invalid number: %s", val)
						return m, nil
					}
				}
			case editModelReasoningEnableStep:
				if _, err := normalizeReasoningEnable(val); err != nil {
					m.formError = err.Error()
					return m, nil
				}
				val = strings.ToLower(val)
			case editModelReasoningEffortStep:
				if _, err := normalizeReasoningEffort(val); err != nil {
					m.formError = err.Error()
					return m, nil
				}
				val = strings.ToLower(val)
			case editModelReasoningMaxTokensStep:
				if _, err := normalizeReasoningMaxTokens(val); err != nil {
					m.formError = err.Error()
					return m, nil
				}
			case editModelReasoningExcludeStep:
				if _, err := normalizeReasoningExclude(val); err != nil {
					m.formError = err.Error()
					return m, nil
				}
				val = strings.ToLower(val)
			case editModelReasoningSummaryStep:
				if _, err := normalizeReasoningSummary(val); err != nil {
					m.formError = err.Error()
					return m, nil
				}
				val = strings.ToLower(val)
			}
			m.formValues[m.formStepIndex] = val
			if m.formStepIndex >= lastStep {
				thinkType, err := thinkSystemFieldValue(m.formValues[addModelThinkSystemStep])
				if err != nil {
					m.formError = err.Error()
					return m, nil
				}
				ctxWin := 0
				if s := strings.TrimSpace(m.formValues[addModelContextWindowStep]); s != "" {
					n, err := strconv.Atoi(s)
					if err != nil {
						m.formError = fmt.Sprintf("Invalid context window: %s", m.formValues[addModelContextWindowStep])
						return m, nil
					}
					ctxWin = n
				}
				maxTok := 0
				if s := strings.TrimSpace(m.formValues[addModelMaxTokensStep]); s != "" {
					n, err := strconv.Atoi(s)
					if err != nil {
						m.formError = fmt.Sprintf("Invalid max tokens: %s", m.formValues[addModelMaxTokensStep])
						return m, nil
					}
					maxTok = n
				}
				vision := m.formValues[addModelVisionStep] == "yes"
				if m.modalMode == ModalEditModel {
					reasoning, err := reasoningFromFormValues(m.formValues)
					if err != nil {
						m.formError = err.Error()
						return m, nil
					}
					if err := m.updateModelInProvider(m.providerSelectedName, m.modelEditAlias, m.formValues[addModelAliasStep], m.formValues[addModelModelNameStep], ctxWin, maxTok, vision, thinkType, reasoning); err != nil {
						m.formError = err.Error()
						return m, nil
					}
					saved := strings.ToLower(strings.TrimSpace(m.formValues[addModelAliasStep]))
					m.enterProviderDetail(m.providerSelectedName, saved)
					m.closeCatalogPicker()
					m.syncTextInputEcho()
					return m, nil
				}
				if err := m.addModelToProvider(m.providerSelectedName, m.formValues[addModelAliasStep], m.formValues[addModelModelNameStep], ctxWin, maxTok, vision, thinkType); err != nil {
					m.formError = err.Error()
					return m, nil
				}
				m.modalMode = ModalNone
				m.closeCatalogPicker()
				m.syncTextInputEcho()
				return m, nil
			}
			m.formStepIndex++
			nextVal := ""
			if m.formStepIndex < len(m.formValues) {
				nextVal = m.formValues[m.formStepIndex]
			}
			m.textInput.SetValue(nextVal)
			switch m.formStepIndex {
			case addModelModelNameStep:
				m.textInput.Placeholder = "Actual model name (e.g. gpt-4o-2024-08-06)"
				m.startCatalogPickerIfNeeded()
			case addModelContextWindowStep:
				m.textInput.Placeholder = "Context window (e.g. 128000)"
				m.closeCatalogPicker()
			case addModelMaxTokensStep:
				m.textInput.Placeholder = "Max tokens (e.g. 4096)"
				m.closeCatalogPicker()
			case addModelVisionStep:
				m.textInput.Placeholder = "Vision support? (yes/no)"
				m.closeCatalogPicker()
			case addModelThinkSystemStep:
				m.textInput.Placeholder = "auto, deepseek, openai, openrouter, qwen or none"
				m.closeCatalogPicker()
			case editModelReasoningEnableStep:
				m.textInput.Placeholder = "Reasoning enabled? (yes/no)"
				m.closeCatalogPicker()
			case editModelReasoningEffortStep:
				m.textInput.Placeholder = "low, medium, high or default"
				m.closeCatalogPicker()
			case editModelReasoningMaxTokensStep:
				m.textInput.Placeholder = "Max reasoning tokens (blank or default = unset)"
				m.closeCatalogPicker()
			case editModelReasoningExcludeStep:
				m.textInput.Placeholder = "Exclude reasoning tokens? (yes/no/default)"
				m.closeCatalogPicker()
			case editModelReasoningSummaryStep:
				m.textInput.Placeholder = "auto, detailed, concise or default"
				m.closeCatalogPicker()
			}
			return m, nil
		} else if m.modalMode == ModalEditProvider {
			val := strings.TrimSpace(m.textInput.Value())
			if val == "" && m.formStepIndex < len(m.formValues) && m.formValues[m.formStepIndex] != "" {
				val = m.formValues[m.formStepIndex]
			}
			if m.formStepIndex == editProviderTypeStep && val == "" {
				m.formError = "Provider type is required"
				return m, nil
			}
			switch m.formStepIndex {
			case editProviderWebSearchStep:
				token, err := parseWebSearchToken(val)
				if err != nil {
					m.formError = err.Error()
					return m, nil
				}
				val = token
			case editProviderHeadersStep:
				if _, _, err := parseHeaderEditValue(nil, val); err != nil {
					m.formError = err.Error()
					return m, nil
				}
			}
			m.formError = ""
			m.formValues[m.formStepIndex] = val
			if m.formStepIndex < len(m.formValues)-1 {
				m.formStepIndex++
				m.textInput.SetValue(m.formValues[m.formStepIndex])
				switch m.formStepIndex {
				case editProviderAPIKeyStep:
					m.textInput.Placeholder = "API Key"
				case editProviderAPIBaseStep:
					m.textInput.Placeholder = "API Base URL (e.g. https://api.example.com/v1)"
				case editProviderProxyStep:
					m.textInput.Placeholder = "Proxy URL (- to clear)"
				case editProviderAuthMethodStep:
					m.textInput.Placeholder = "Auth method, e.g. oauth (- to clear)"
				case editProviderConnectModeStep:
					m.textInput.Placeholder = "stdio or grpc — GitHub Copilot only (- to clear)"
				case editProviderWebSearchStep:
					m.textInput.Placeholder = "Web search: yes, no or default"
				case editProviderHeadersStep:
					m.textInput.Placeholder = `Headers JSON, e.g. {"X-Title":"lele"}`
				}
				m.syncTextInputEcho()
				return m, nil
			}
			values := providerEditValues{
				Type:        m.formValues[editProviderTypeStep],
				APIKey:      m.formValues[editProviderAPIKeyStep],
				APIBase:     m.formValues[editProviderAPIBaseStep],
				Proxy:       m.formValues[editProviderProxyStep],
				AuthMethod:  m.formValues[editProviderAuthMethodStep],
				ConnectMode: m.formValues[editProviderConnectModeStep],
				WebSearch:   m.formValues[editProviderWebSearchStep],
				Headers:     m.formValues[editProviderHeadersStep],
			}
			if err := m.updateProvider(m.providerSelectedName, values); err != nil {
				m.formError = err.Error()
				return m, nil
			}
			m.providerFeedback = i18n.T("tui.providerUpdated")
			m.enterProviderDetail(m.providerSelectedName, "")
			m.syncTextInputEcho()
			return m, nil
		} else if m.modalMode == ModalAddSecret {
			val := m.textInput.Value()
			if m.formStepIndex <= 1 && strings.TrimSpace(val) == "" {
				m.formError = "This field is required"
				return m, nil
			}
			m.formError = ""
			if m.formStepIndex == 0 {
				val = strings.TrimSpace(val)
			}
			m.formValues[m.formStepIndex] = val
			if m.formStepIndex >= 4 {
				svc := m.keyringSvc()
				if svc == nil {
					m.formError = i18n.T("tui.secretsUnavailable")
					return m, nil
				}
				tags := splitCSV(m.formValues[3])
				scope := splitCSV(m.formValues[4])
				if err := svc.SetFromUI(m.formValues[0], m.formValues[1], m.formValues[2], tags, scope, "tui"); err != nil {
					m.formError = err.Error()
					return m, nil
				}
				secretName := m.formValues[0]
				m.resetModal(ModalSecrets)
				m.loadSecrets()
				m.reselectSecret(secretName)
				return m, nil
			}
			m.formStepIndex++
			m.textInput.SetValue("")
			switch m.formStepIndex {
			case 1:
				m.textInput.Placeholder = "Secret value (stored encrypted)"
			case 2:
				m.textInput.Placeholder = "Description (optional)"
			case 3:
				m.textInput.Placeholder = "Tags, comma-separated (optional)"
			case 4:
				m.textInput.Placeholder = "Scope: agent IDs, comma-separated (empty = all)"
			}
			return m, nil
		} else if m.modalMode == ModalAddCommand {
			if m.formStepIndex == commandTemplateStep {
				return m, m.saveCommandForm()
			}
			return m, m.advanceCommandWizard(strings.TrimSpace(m.textInput.Value()))
		} else if len(m.modalItems) > 0 {
			m.clampModalCursor()
			if m.modalSelectedIdx >= 0 && m.modalSelectedIdx < len(m.modalItems) {
				selectedVal := m.modalItems[m.modalSelectedIdx]
				if m.modalMode == ModalAgent {
					m.applyAgentSelection(selectedVal)
				} else if m.modalMode == ModalModel {
					if m.showWelcome {
						m.pendingModel = selectedVal
					}
					if m.currentKey != "" {
						m.agentLoop.GetProvidable().SetSessionModel(m.currentKey, selectedVal)
					}
					m.lastViewportKey = ""
					m.renderedBaseKey = ""
				} else if m.modalMode == ModalThink {
					if m.showWelcome {
						m.pendingThink = selectedVal
					}
					if m.currentKey != "" {
						m.agentLoop.GetProvidable().SetThinkLevel(m.currentKey, selectedVal)
					}
					m.lastViewportKey = ""
					m.renderedBaseKey = ""
				} else if m.modalMode == ModalSessions {
					if m.modalSelectedIdx < len(m.modalSessionKeys) {
						m.setCurrentChatKey(m.modalSessionKeys[m.modalSelectedIdx])
						m.showWelcome = false
						m.clearStreamingState()
						m.pendingModel = m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
					}
				} else if m.modalMode == ModalSubagents {
					if m.modalSelectedIdx < len(m.modalSubagentKeys) {
						if m.parentSessionKey == "" {
							m.parentSessionKey = m.currentKey
						}
						m.setCurrentChatKey(m.modalSubagentKeys[m.modalSelectedIdx])
						m.showWelcome = false
						m.clearStreamingState()
					}
				} else if m.modalMode == ModalLang {
					langCode := ""
					if m.modalSelectedIdx < len(m.modalLangCodes) {
						langCode = m.modalLangCodes[m.modalSelectedIdx]
					}
					if langCode == "" {
						if idx := strings.LastIndex(selectedVal, "("); idx != -1 {
							langCode = strings.TrimRight(selectedVal[idx+1:], ")")
						}
						if langCode == "" || strings.Contains(selectedVal, i18n.T("tui.languages.downloadMore")) {
							return m, m.openRemoteLanguageBrowser()
						}
					}
					if err := m.applyLanguage(langCode); err != nil {
						m.queueFeedback = formatLangFeedback(langCode, err)
					} else {
						m.queueFeedback = ""
					}
				} else if m.modalMode == ModalLangRemote {
					return m, m.handleLangInstallSelect()
				} else if m.modalMode == ModalProviders {
					if m.modalSelectedIdx < len(m.modalItems) &&
						m.modalItems[m.modalSelectedIdx] == i18n.T("tui.connectAction") {
						return m, m.executeCommand("/connect")
					}
					if m.modalSelectedIdx < len(m.providerModalKeys) {
						m.enterProviderDetail(m.providerModalKeys[m.modalSelectedIdx], "")
					}
					return m, nil
				} else if m.modalMode == ModalProviderDetail {
					if m.modalSelectedIdx < len(m.modalItems) {
						if m.modalSelectedIdx == m.providerDetailEditIdx {
							m.clearProviderDeleteConfirm()
							m.openProviderEditFlow()
							return m, nil
						} else if m.modalSelectedIdx == m.providerDetailAddIdx {
							m.clearProviderDeleteConfirm()
							m.modalMode = ModalAddModel
							m.formStepIndex = 0
							m.formValues = make([]string, addModelStepCount)
							m.formError = ""
							m.formConfirmMode = false
							m.textInput.SetValue("")
							m.textInput.Placeholder = "Model alias (e.g. gpt-4o)"
							return m, nil
						} else if m.modalSelectedIdx == m.providerDetailDelIdx {
							m.clearProviderDeleteConfirm()
							if err := m.deleteProvider(m.providerSelectedName); err != nil {
								m.formError = err.Error()
								return m, nil
							}
							m.modalMode = ModalNone
							return m, nil
						} else if alias, ok := m.providerDetailRows[m.modalSelectedIdx]; ok {
							m.clearProviderDeleteConfirm()
							m.openModelEditFlow(alias)
							return m, nil
						}
					}
					return m, nil
				} else if m.modalMode == ModalBackgroundExecs {
					if m.bgExecViewMode {
						m.bgExecViewMode = false
						m.bgExecViewOutput = ""
						m.bgExecViewStatus = ""
						return m, m.tickCmd()
					}
					if m.modalSelectedIdx < len(m.bgExecModalKeys) {
						procID := m.bgExecModalKeys[m.modalSelectedIdx]
						m.bgExecViewMode = true
						m.bgExecViewID = procID
						output, status, _, err := m.agentLoop.GetProvidable().GetBackgroundExecOutput(procID, 5000)
						if err == nil {
							m.bgExecViewOutput = output
							m.bgExecViewStatus = status
						}
						return m, m.tickCmd()
					}
				} else if m.modalMode == ModalCron {
					if m.cronDetailMode {
						m.cronDetailMode = false
						m.cronDetailJobID = ""
						m.loadCronJobs()
						return m, m.tickCmd()
					}
					if m.modalSelectedIdx < len(m.cronModalKeys) {
						m.cronDetailMode = true
						m.cronDetailJobID = m.cronModalKeys[m.modalSelectedIdx]
						return m, m.tickCmd()
					}
				} else if m.modalMode == ModalSecrets {
					if m.secretsDetailMode {
						m.secretsDetailMode = false
						m.secretsDetailName = ""
						m.secretsReveal = false
						m.loadSecrets()
						return m, m.tickCmd()
					}
					if m.modalSelectedIdx < len(m.secretsModalKeys) {
						m.secretsDetailMode = true
						m.secretsDetailName = m.secretsModalKeys[m.modalSelectedIdx]
						m.secretsReveal = false
						return m, m.tickCmd()
					}
				} else if m.modalMode == ModalSkills {
					return m, m.handleSkillsEnter()
				} else if m.modalMode == ModalSkillInstall {
					return m, m.handleSkillInstallSubmit()
				} else if m.modalMode == ModalSkillPicker {
					return m, m.handleSkillPickerEnter()
				} else if m.modalMode == ModalCommands {
					m.clearCommandDeleteConfirm()
					return m, m.handleCommandsEnter()
				} else if m.modalMode == ModalCommandDetail {
					m.exitCommandDetail()
					return m, m.tickCmd()
				} else if m.modalMode == ModalSettings {
					switch m.modalSelectedIdx {
					case 0:
						m.resetModal(ModalSettingsAgents)
						m.settingsSection = "agents"
						m.loadAgentsSettings()
					case 1:
						m.modalMode = ModalSettingsSystem
						m.settingsSection = ""
						m.loadSystemSettings()
					case 2:
						m.modalMode = ModalSettingsTUI
						m.settingsSection = "tui"
						m.loadTUISettings()
					}
					m.modalSelectedIdx = 0
					m.modalScrollOffset = 0
					return m, nil
				} else if m.modalMode == ModalSettingsSystem {
					groupIdx := m.modalSelectedIdx
					m.resetModal(ModalSettingsSystemEdit)
					m.settingsSection = sysSubViewName(groupIdx)
					m.reloadSystemSubView()
					return m, nil
				} else if m.modalMode == ModalSettingsSystemEdit {
					if m.settingsSelectorActive {
						return m, m.handleSelectorConfirm()
					}
					if m.settingsEditField != "" {
						m.handleSystemSettingsInput(m.textInput.Value())
						return m, nil
					}
					return m, m.handleSystemSubEnter()
				} else if m.modalMode == ModalSettingsAgents {
					if m.settingsEditField != "" {
						m.handleAgentSettingsInput(m.textInput.Value())
						return m, nil
					}
					return m, m.handleAgentsEnter()
				} else if m.modalMode == ModalSettingsAgentEdit {
					if m.settingsSelectorActive {
						return m, m.handleSelectorConfirm()
					}
					if m.settingsEditField != "" {
						m.handleAgentSettingsInput(m.textInput.Value())
						return m, nil
					}
					return m, m.handleAgentEditEnter()
				} else if m.modalMode == ModalSettingsTUI {
					if m.themePickerActive {
						if m.modalSelectedIdx < len(m.themePickerItems) {
							item := m.themePickerItems[m.modalSelectedIdx]
							switch item.kind {
							case "builtin":
								m.applyThemeByName(item.name)
								m.themePreviewName = ""
								m.themePickerActive = false
								m.loadTUISettings()
								m.modalSelectedIdx = 0
								return m, nil
							case "community":
								if theme.IsInstalledCommunity(item.name, m.installedCommunity) {
									m.applyThemeByName(item.name)
									m.themePreviewName = ""
									m.themePickerActive = false
									m.loadTUISettings()
									m.modalSelectedIdx = 0
									return m, nil
								}
								m.communityLoading = true
								m.loadThemePickerItems()
								return m, m.installCommunityThemeCmd(item.name)
							case "retry":
								m.communityLoading = true
								m.communityErr = ""
								m.loadThemePickerItems()
								return m, m.fetchCommunityIndexCmd()
							}
						}
						return m, nil
					}
					cmd := m.handleTUISettingsEnter()
					if m.themePickerActive {
						return m, cmd
					}
					if m.modalSelectedIdx == tuiSettingRowMouse {
						return m, m.toggleTUIMouse()
					}
					return m, nil
				}
				if !m.bgExecViewMode && !m.cronDetailMode && !m.secretsDetailMode {
					m.modalMode = ModalNone
				}
				m.reloadSessions()
			}
		}
	case "esc", "q":
		if msg.String() == "q" && isFormModal(m.modalMode, m.settingsEditField != "") {
			break
		}
		m.clearProviderDeleteConfirm()
		if m.modalMode == ModalSettingsTUI && m.themePickerActive {
			if m.themePreviewName != "" {
				m.previewTheme(m.themePreviewName)
				m.currentThemeName = m.themePreviewName
				m.themePreviewName = ""
			}
			m.themePickerActive = false
			m.loadTUISettings()
			m.modalSelectedIdx = 0
			return m, nil
		}
		if m.modalMode == ModalSettingsTUI && m.settingsEditField != "" {
			m.settingsEditField = ""
			m.formError = ""
			m.loadTUISettings()
			return m, nil
		}
		if m.modalMode == ModalBackgroundExecs && m.bgExecViewMode {
			if msg.String() == "q" || msg.String() == "esc" {
				m.bgExecViewMode = false
				m.bgExecViewOutput = ""
				m.bgExecViewStatus = ""
				return m, m.tickCmd()
			}
		}
		if m.modalMode == ModalCron && m.cronDetailMode {
			m.cronDetailMode = false
			m.cronDetailJobID = ""
			m.loadCronJobs()
			return m, m.tickCmd()
		}
		if m.modalMode == ModalSecrets && m.secretsDetailMode {
			m.secretsDetailMode = false
			m.secretsDetailName = ""
			m.secretsReveal = false
			m.loadSecrets()
			return m, m.tickCmd()
		}
		if m.modalMode == ModalSettingsSystemEdit {
			if m.settingsSelectorActive {
				m.handleSelectorCancel()
				return m, nil
			}
			if m.settingsEditField != "" {
				m.settingsEditField = ""
				m.formError = ""
				m.reloadSystemSubView()
				return m, nil
			}
			m.modalMode = ModalSettingsSystem
			m.settingsSection = ""
			m.loadSystemSettings()
			m.modalSelectedIdx = 0
			m.modalScrollOffset = 0
			return m, m.tickCmd()
		}
		if m.modalMode == ModalSettingsAgentEdit {
			if m.settingsSelectorActive {
				m.handleSelectorCancel()
				return m, nil
			}
			if m.settingsEditField != "" {
				m.settingsEditField = ""
				m.formError = ""
				m.loadAgentDetail(m.settingsAgentID)
				return m, nil
			}
			m.modalMode = ModalSettingsAgents
			m.settingsAgentID = ""
			m.loadAgentsSettings()
			m.modalSelectedIdx = 0
			m.modalScrollOffset = 0
			return m, m.tickCmd()
		}
		if m.modalMode == ModalSettingsAgents ||
			m.modalMode == ModalSettingsSystem ||
			m.modalMode == ModalSettingsTUI ||
			m.modalMode == ModalSettingsSystemEdit {
			m.modalMode = ModalSettings
			m.settingsSection = ""
			m.settingsEditField = ""
			m.settingsAgentID = ""
			m.modalItems = []string{
				i18n.T("tui.settings.agents"),
				i18n.T("tui.settings.system"),
				i18n.T("tui.settings.interface"),
			}
			m.modalSelectedIdx = 0
			m.modalScrollOffset = 0
			return m, m.tickCmd()
		}
		if m.modalMode == ModalAddCommand {
			key := m.commandsEditKey
			m.exitCommandWizard()
			m.loadCommandsList()
			m.reselectCommandRow(key)
			return m, m.tickCmd()
		}
		if m.modalMode == ModalCommandDetail {
			m.exitCommandDetail()
			return m, m.tickCmd()
		}
		if m.modalMode == ModalSkillInstall {
			m.modalMode = ModalSkills
			m.textInput.SetValue("")
			m.formError = ""
			m.loadSkillsList()
			return m, m.tickCmd()
		}
		if m.modalMode == ModalSkillPicker {
			m.modalMode = ModalSkillInstall
			m.skillsScanResults = nil
			m.skillsSelectedMap = nil
			m.formError = ""
			return m, m.tickCmd()
		}
		if m.modalMode == ModalEditModel {
			provider, alias := m.providerSelectedName, m.modelEditAlias
			m.closeCatalogPicker()
			m.formError = ""
			m.enterProviderDetail(provider, alias)
			m.syncTextInputEcho()
			return m, nil
		}
		if m.modalMode == ModalEditProvider {
			provider := m.providerSelectedName
			m.formError = ""
			m.enterProviderDetail(provider, "")
			m.syncTextInputEcho()
			return m, nil
		}
		if m.modalMode == ModalAddProvider {
			if m.providerTypePicker {
				m.providerTypePicker = false
				m.modalScrollOffset = 0
				m.formStepIndex = 1
				m.textInput.SetValue("")
				m.textInput.Placeholder = "Provider type (e.g. openai, anthropic, openrouter)"
				return m, nil
			}
			m.providerSavedInFlow = false
			m.connectSuccess = false
			m.modalScrollOffset = 0
			if m.onboardingActive && m.onboardingStep == obConnect {
				m.modalMode = ModalNone
				m.formValues = nil
				m.formStepIndex = 0
				m.onboardingStep = obProviderPicker
				m.modalSelectedIdx = 0
				m.closeCatalogPicker()
				m.syncTextInputEcho()
				return m, nil
			}
		}
		m.closeCatalogPicker()
		m.modalMode = ModalNone
		m.syncTextInputEcho()
	case "s":
		if m.modalMode == ModalBackgroundExecs && !m.bgExecViewMode {
			if m.modalSelectedIdx < len(m.bgExecModalKeys) {
				procID := m.bgExecModalKeys[m.modalSelectedIdx]
				_ = m.agentLoop.GetProvidable().StopBackgroundExec(procID)
				return m, m.executeCommand("/bg")
			}
		}
	case "e":
		if m.modalMode == ModalProviderDetail {
			m.clearProviderDeleteConfirm()
			m.openProviderEditFlow()
			return m, m.tickCmd()
		}
		if m.modalMode == ModalCommands {
			if key := m.selectedCommandKey(); key != "" && key != commandsNewKey {
				m.startCommandEdit(key)
				return m, m.tickCmd()
			}
		}
		if m.modalMode == ModalCommandDetail {
			key := m.commandsDetailKey
			m.exitCommandDetail()
			m.startCommandEdit(key)
			return m, m.tickCmd()
		}
		if m.modalMode == ModalCron && m.cronService != nil {
			jobID := m.selectedCronJobID()
			if jobID != "" {
				if job := m.cronService.GetJob(jobID); job != nil {
					m.cronService.EnableJob(jobID, !job.Enabled)
					if m.cronDetailMode {
						m.cronDetailJobID = jobID
					}
					m.loadCronJobs()
					m.reselectCronJob(jobID)
					return m, m.tickCmd()
				}
			}
		}
	case "r":
		if m.modalMode == ModalCron && m.cronService != nil {
			jobID := m.selectedCronJobID()
			if jobID != "" {
				_ = m.cronService.RunJobNow(jobID)
				m.loadCronJobs()
				m.reselectCronJob(jobID)
				return m, m.tickCmd()
			}
		}
		if m.modalMode == ModalSecrets && m.secretsDetailMode {
			m.secretsReveal = !m.secretsReveal
			return m, m.tickCmd()
		}
	case "n":
		if m.modalMode == ModalCommands {
			m.startCommandCreate()
			return m, m.tickCmd()
		}
	case "a":
		if m.modalMode == ModalSecrets && !m.secretsDetailMode {
			if m.keyringSvc() != nil {
				m.startAddSecret()
				return m, m.tickCmd()
			}
		}
	case "d":
		if m.modalMode == ModalProviderDetail {
			if alias, ok := m.providerDetailRows[m.modalSelectedIdx]; ok {
				return m, m.requestProviderModelDelete(m.providerSelectedName, alias)
			}
			m.providerFeedback = i18n.T("tui.modelDeleteNoRow")
			return m, nil
		}
		if m.modalMode == ModalSettingsTUI &&
			m.settingsEditField == "" && !m.themePickerActive &&
			m.keepAwakeRequestedButFailed() {
			m.cancelKeepAwake()
			return m, nil
		}
		if m.modalMode == ModalCron && m.cronService != nil {
			jobID := m.selectedCronJobID()
			if jobID != "" {
				m.cronService.RemoveJob(jobID)
				m.cronDetailMode = false
				m.cronDetailJobID = ""
				m.loadCronJobs()
				return m, m.tickCmd()
			}
		}
		if m.modalMode == ModalSecrets && m.keyringSvc() != nil {
			name := m.selectedSecretName()
			if name != "" {
				_ = m.keyringSvc().DeleteFromUI(name, "tui")
				m.secretsDetailMode = false
				m.secretsDetailName = ""
				m.secretsReveal = false
				m.loadSecrets()
				return m, m.tickCmd()
			}
		}
		if m.modalMode == ModalSkills {
			if m.modalSelectedIdx < len(m.skillsModalKeys) {
				skillName := m.skillsModalKeys[m.modalSelectedIdx]
				if skillName != "" && skillName != "__install__" {
					return m, m.deleteSkillCmd(skillName)
				}
			}
		}
		if m.modalMode == ModalCommands {
			if key := m.selectedCommandKey(); key != "" && key != commandsNewKey {
				return m, m.requestCommandDelete(key)
			}
		}
		if m.modalMode == ModalCommandDetail {
			return m, m.requestCommandDelete(m.commandsDetailKey)
		}
	case " ":
		if m.modalMode == ModalSkillPicker {
			m.handleSkillPickerToggle()
			return m, m.tickCmd()
		}
	}
	if isFormModal(m.modalMode, m.settingsEditField != "") {
		if m.modalMode == ModalAddCommand && m.formStepIndex == commandTemplateStep {
			var cmd tea.Cmd
			m.templateInput, cmd = m.templateInput.Update(msg)
			return m, cmd
		}
		m.syncTextInputEcho()
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		if m.addModelCatalogActive && m.isModelNameFormStep() {
			m.refreshCatalogSuggestions(m.textInput.Value())
		}
		if m.isSessionProcessing() {
			return m, tea.Batch(cmd, m.tickCmd())
		}
		return m, cmd
	}
	if m.isSessionProcessing() {
		return m, m.tickCmd()
	}
	return m, nil

}

func (m *Model) applyConnectRow(idx int, row connectRow) tea.Cmd {
	m.connectRowIdx = idx
	m.providerTypeFromPreset = false
	m.connectSkipAPIBase = false
	m.formError = ""
	m.modalMode = ModalAddProvider

	switch row.kind {
	case connectRowCustomOpenAI, connectRowCustomAnthropic:
		m.formValues[1] = row.typ
		m.providerTypeFromPreset = true
		m.formStepIndex = 0
		m.textInput.SetValue("")
		m.textInput.Placeholder = i18n.T("tui.connectProviderNamePlaceholder")

	case connectRowAPIKey:
		m.formValues[0] = row.name
		m.formValues[1] = row.typ
		m.formValues[3] = row.apiBase
		m.providerTypeFromPreset = true
		m.connectSkipAPIBase = true
		m.formStepIndex = 2
		m.textInput.SetValue("")
		m.textInput.Placeholder = "API Key"
		if row.keyHint != "" {
			m.textInput.Placeholder = "API Key (" + row.keyHint + ")"
		}

	case connectRowFreeType:
		m.formValues[1] = ""
		m.providerTypeFromPreset = false
		m.connectSkipAPIBase = false
		m.formStepIndex = 0
		m.textInput.SetValue("")
		m.textInput.Placeholder = i18n.T("tui.connectProviderNamePlaceholder")

	case connectRowOAuth:
		m.connectOAuthCancel()
		return m.connectOAuthStartFromRow(row)
	}
	m.syncTextInputEcho()
	return nil
}

func (m *Model) connectSaveProvider() bool {
	if m.onboardingActive && m.cfg != nil && m.cfg.Providers != nil && m.cfg.Providers.Named != nil {
		pnameKey := strings.ToLower(strings.TrimSpace(m.formValues[0]))
		if existing, ok := m.cfg.Providers.Named[pnameKey]; ok && existing.IsEmpty() {
			delete(m.cfg.Providers.Named, pnameKey)
		}
	}
	if m.connectRowIdx >= 0 {
		alreadyUsable, err := m.ensureProvider(m.formValues[0], m.formValues[1], m.formValues[2], m.formValues[3], m.connectSkipAPIBase)
		if err != nil {
			m.formError = err.Error()
			return false
		}
		m.connectAlreadyUsable = alreadyUsable
		return true
	}
	if err := m.addProvider(m.formValues[0], m.formValues[1], m.formValues[2], m.formValues[3]); err != nil {
		m.formError = err.Error()
		return false
	}
	return true
}

func (m *Model) connectRouteAfterProviderSave() bool {
	if m.connectRowIdx >= 0 && m.connectAlreadyUsable {
		m.connectSuccess = true
		m.providerSavedInFlow = false
		m.formStepIndex = 10
		m.syncTextInputEcho()
		return false
	}
	m.providerSavedInFlow = true
	m.providerSelectedName = strings.ToLower(strings.TrimSpace(m.formValues[0]))
	m.formStepIndex = 4
	m.textInput.SetValue("")
	m.textInput.Placeholder = "Model alias (e.g. gpt-4o)"
	m.closeCatalogPicker()
	if p := providerPresetByType(m.formValues[1]); p != nil && p.modelHint != "" {
		m.textInput.Placeholder = "Model alias (" + p.modelHint + ")"
	}
	if m.onboardingActive && m.obSelectedPreset >= 0 && m.obSelectedPreset < len(providerPresets) {
		preset := &providerPresets[m.obSelectedPreset]
		if m.formValues[4] == "" && preset.defaultModelAlias != "" {
			m.formValues[4] = preset.defaultModelAlias
		}
		if m.formValues[5] == "" && preset.defaultModel != "" {
			m.formValues[5] = preset.defaultModel
		}
		if m.formValues[6] == "" {
			m.formValues[6] = "128000"
		}
		if m.formValues[7] == "" {
			m.formValues[7] = "4096"
		}
		if m.formValues[8] == "" {
			m.formValues[8] = "no"
		}
	}
	if m.connectRowIdx >= 0 {
		if row, ok := connectRowByIndex(m.connectRowIdx); ok {
			if m.formValues[4] == "" && row.defaultModelAlias != "" {
				m.formValues[4] = row.defaultModelAlias
			}
			if m.formValues[5] == "" && row.defaultModel != "" {
				m.formValues[5] = row.defaultModel
			}
			if m.formValues[6] == "" {
				m.formValues[6] = "128000"
			}
			if m.formValues[7] == "" {
				m.formValues[7] = "4096"
			}
			if m.formValues[8] == "" {
				m.formValues[8] = "no"
			}
		}
	}
	return true
}

func (m *Model) applyLegacyTypePreset(sel int) {
	if sel < len(providerPresets) {
		p := providerPresets[sel]
		m.formValues[1] = p.typ
		m.providerTypeFromPreset = true
		m.formValues[3] = p.apiBase
		m.formStepIndex = 2
		m.textInput.SetValue("")
		m.textInput.Placeholder = "API Key"
		if p.keyHint != "" {
			m.textInput.Placeholder = "API Key (" + p.keyHint + ")"
		}
		return
	}
	m.formValues[1] = ""
	m.providerTypeFromPreset = false
	m.formStepIndex = 1
	m.textInput.SetValue("")
	m.textInput.Placeholder = "Provider type (e.g. openai, anthropic, openrouter)"
}
