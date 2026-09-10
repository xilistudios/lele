package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

// handleModalKey processes a keystroke while a modal dialog is open. It owns
// every modal surface: picker navigation, list selection, form-based modals,
// the theme picker and the settings editors. The block is terminal — a key
// consumed by a modal never reaches the normal input handlers — which is why
// the caller can return its result unconditionally.
func (m *Model) handleModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Audit M2: universal sync point for every keystroke while a modal
	// is open. The forwarding block below also syncs, but transitions
	// that END on a secret step (ESC-close, Enter-advance, success
	// screen) return before reaching it — syncing at the top of the
	// block guarantees the last processed event always leaves the
	// echo mode consistent with the state it ended in.
	m.syncTextInputEcho()
	// Subagent multi-select picker navigation/toggle/confirm.
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
	// Settings inline selector navigation.
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
	// Provider-type picker navigation (up/down within the preset list).
	if m.modalMode == ModalAddProvider && m.providerTypePicker {
		switch msg.String() {
		case "up", "k":
			if m.providerTypePickerIdx > 0 {
				m.providerTypePickerIdx--
			}
			return m, nil
		case "down", "j":
			max := m.providerTypePickerMax
			if max <= 0 {
				max = len(providerPresets) + 1
			}
			if m.providerTypePickerIdx < max-1 {
				m.providerTypePickerIdx++
			}
			return m, nil
		case "esc":
			// Cancel back to free-form type entry.
			m.providerTypePicker = false
			m.formStepIndex = 1
			m.textInput.SetValue("")
			m.textInput.Placeholder = "Provider type (e.g. openai, anthropic, openrouter)"
			return m, nil
		case "q":
			// "q" must NOT cancel the picker — only ESC does.
			// Swallow it so it doesn't fall through to the global
			// modal-close handler.
			return m, nil
		}
		// Fall through to textInput forwarding below for typing.
	}
	// When a settings field is being edited, list navigation must not fire —
	// j/k/up/down are regular characters in the text input. Without this
	// guard the cursor drifts behind the invisible list and the next Enter
	// acts on the wrong row.
	if m.settingsEditField != "" && !m.settingsSelectorActive && !m.subagentPickerActive {
		// Fall through to the textInput forwarding at the end of this handler.
		// ESC/q still need to reach their normal cancel paths below, so only
		// navigation keys are swallowed here.
		switch msg.String() {
		case "up", "k", "down", "j", "pgup", "pgdown":
			if isFormModal(m.modalMode, true) {
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				return m, cmd
			}
		}
	}
	switch msg.String() {
	case "up", "k":
		if isListModal(m.modalMode) && m.modalSelectedIdx > 0 {
			m.modalSelectedIdx--
			if m.modalSelectedIdx < m.modalScrollOffset {
				m.modalScrollOffset = m.modalSelectedIdx
			}
		}
		// Theme picker live preview: after navigation, preview the
		// highlighted theme without persisting. Esc reverts.
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
			maxVisible := m.height - 8 // title + borders + padding
			if maxVisible < 3 {
				maxVisible = 3
			}
			if m.modalSelectedIdx >= m.modalScrollOffset+maxVisible {
				m.modalScrollOffset = m.modalSelectedIdx - maxVisible + 1
			}
		}
		// Theme picker live preview: after navigation, preview the
		// highlighted theme without persisting. Esc reverts.
		if m.modalMode == ModalSettingsTUI && m.themePickerActive && m.modalSelectedIdx < len(m.themePickerItems) {
			item := m.themePickerItems[m.modalSelectedIdx]
			if item.kind == "builtin" {
				m.previewTheme(item.name)
			} else if item.kind == "community" && theme.IsInstalledCommunity(item.name, m.installedCommunity) {
				m.previewTheme(item.name)
			}
		}
	case "enter":
		// TUI settings inline edit: save value and return to list.
		if m.modalMode == ModalSettingsTUI && m.settingsEditField != "" {
			m.handleTUISettingsInput(m.textInput.Value())
			return m, nil
		}
		// Handle form-based modals first — they don't use m.modalItems.
		if m.modalMode == ModalAddProvider {
			// ── Success screen — any Enter/ESC closes the modal ──
			if m.connectSuccess {
				m.connectSuccess = false
				m.providerSavedInFlow = false
				m.formStepIndex = 0
				m.formValues = nil
				m.modalMode = ModalNone
				m.syncTextInputEcho() // audit M2: clear stale echo on close
				m.reloadSessions()
				return m, nil
			}
			// ── Provider-type picker (step 1, typePicker active) ──
			if m.providerTypePicker {
				sel := m.providerTypePickerIdx
				max := m.providerTypePickerMax
				if max <= 0 {
					max = len(providerPresets)
				}
				if sel >= 0 && sel < max {
					if sel < len(providerPresets) {
						p := providerPresets[sel]
						m.formValues[1] = p.typ
						m.providerTypeFromPreset = true
						// Pre-fill the API base from the preset.
						m.formValues[3] = p.apiBase
						m.formStepIndex = 2 // next: API Key
						m.textInput.SetValue("")
						m.textInput.Placeholder = "API Key"
						if p.keyHint != "" {
							m.textInput.Placeholder = "API Key (" + p.keyHint + ")"
						}
					} else {
						// "custom" entry — free-form type
						m.formValues[1] = ""
						m.providerTypeFromPreset = false
						m.formStepIndex = 1 // stay on type step for free text
						m.textInput.SetValue("")
						m.textInput.Placeholder = "Provider type (e.g. openai, anthropic, openrouter)"
					}
					m.providerTypePicker = false
					m.formError = ""
					return m, nil
				}
				m.formError = "Select a provider type"
				return m, nil
			}

			// Form-based modal: validate and advance steps
			val := strings.TrimSpace(m.textInput.Value())
			// If the input is empty but a default was already
			// pre-filled for this step (e.g. onboarding pre-fills the
			// model steps from the chosen preset), accept the default
			// so pressing Enter through the flow works.
			if val == "" && m.formStepIndex < len(m.formValues) && m.formValues[m.formStepIndex] != "" {
				val = m.formValues[m.formStepIndex]
			}
			// API Key (step 2) is optional — local providers (ollama)
			// and custom endpoints may not require authentication.
			// The review step (9) has no input — any Enter confirms.
			allowEmpty := (m.formStepIndex == 2 && !m.providerSavedInFlow) ||
				(m.formStepIndex == 9 && m.providerSavedInFlow)
			if val == "" && !allowEmpty {
				m.formError = "This field is required"
				return m, nil
			}
			m.formError = ""
			m.formValues[m.formStepIndex] = val

			// ── Provider steps (0-3) ──────────────────────────────
			if !m.providerSavedInFlow {
				if m.formStepIndex == 0 {
					// Provider name — validate duplicates early and
					// offer the type picker on the next step.
					key := strings.ToLower(strings.TrimSpace(val))
					if m.cfg != nil && m.cfg.Providers != nil && m.cfg.Providers.Named != nil {
						if _, exists := m.cfg.Providers.Named[key]; exists {
							m.formError = fmt.Sprintf("Provider %q already exists", key)
							return m, nil
						}
					}
					m.formStepIndex = 1
					m.providerTypePicker = true
					m.providerTypePickerIdx = 0
					// providerPresets + a trailing "custom" entry.
					m.providerTypePickerMax = len(providerPresets) + 1
					m.textInput.SetValue("")
					m.textInput.Placeholder = ""
					return m, nil
				}
				if m.formStepIndex == 1 {
					// Free-form provider type (only reached when the
					// user chose "custom" and typed a type).
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
					// API key — optional for local providers (e.g. ollama).
					// Non-local providers require a non-empty key during onboarding
					// to ensure HasUsableProvider() returns true after saving.
					providerType := strings.ToLower(strings.TrimSpace(m.formValues[1]))
					isLocal := providerType == "ollama"
					if strings.TrimSpace(val) == "" && !isLocal {
						m.formError = "API key is required for this provider"
						return m, nil
					}
					m.formValues[2] = val
					m.formStepIndex = 3
					m.textInput.SetValue("")
					if m.providerTypeFromPreset {
						p := providerPresetByType(m.formValues[1])
						if p != nil && p.apiBase != "" {
							// Pre-filled from preset; keep it.
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
					// Last provider step — save provider
					name := m.formValues[0]
					pnameKey := strings.ToLower(strings.TrimSpace(name))
					// On a first run the in-memory config may already
					// contain an empty placeholder for every known
					// provider name (ensureNamedDefaults adds openai,
					// anthropic, … with no key/model). During
					// onboarding a preset pre-fills one of those
					// names, so replace the empty placeholder instead
					// of failing with "already exists".
					if m.onboardingActive && m.cfg != nil && m.cfg.Providers != nil && m.cfg.Providers.Named != nil {
						if existing, ok := m.cfg.Providers.Named[pnameKey]; ok && existing.APIKey == "" && len(existing.Models) == 0 {
							delete(m.cfg.Providers.Named, pnameKey)
						}
					}
					if err := m.addProvider(m.formValues[0], m.formValues[1], m.formValues[2], m.formValues[3]); err != nil {
						m.formError = err.Error()
						return m, nil
					}
					m.providerSavedInFlow = true
					m.providerSelectedName = strings.ToLower(strings.TrimSpace(m.formValues[0]))
					// Transition to model configuration steps
					m.formStepIndex = 4
					m.textInput.SetValue("")
					m.textInput.Placeholder = "Model alias (e.g. gpt-4o)"
					if p := providerPresetByType(m.formValues[1]); p != nil && p.modelHint != "" {
						m.textInput.Placeholder = "Model alias (" + p.modelHint + ")"
					}
					// Pre-fill model defaults from the preset chosen
					// during onboarding so the user can just press
					// Enter through (still editable).
					if m.onboardingActive && m.obSelectedPreset >= 0 && m.obSelectedPreset < len(providerPresets) {
						preset := &providerPresets[m.obSelectedPreset]
						if m.formValues[4] == "" && preset.defaultModelAlias != "" {
							m.formValues[4] = preset.defaultModelAlias // model alias
						}
						if m.formValues[5] == "" && preset.defaultModel != "" {
							m.formValues[5] = preset.defaultModel // actual model name
						}
						// Pre-fill the integer/vision steps too so a
						// straight Enter run works end to end.
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
					return m, nil
				}
				// Advance to next provider step
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

			// ── Model steps (4-8) ────────────────────────────────
			if m.formStepIndex <= 8 {
				// Validate model fields before advancing
				if m.formStepIndex == 6 || m.formStepIndex == 7 {
					// Context window and max tokens must be integers
					if _, err := strconv.Atoi(val); err != nil {
						m.formError = "Must be a valid integer"
						return m, nil
					}
				}
				if m.formStepIndex == 8 {
					// Vision must be yes/no
					lower := strings.ToLower(val)
					if lower != "yes" && lower != "no" {
						m.formError = "Enter 'yes' or 'no'"
						return m, nil
					}
					m.formValues[m.formStepIndex] = lower
					// Advance to review step
					m.formStepIndex = 9
					m.textInput.SetValue("")
					m.textInput.Placeholder = ""
					return m, nil
				}
				// Advance to next model step
				m.formStepIndex++
				m.textInput.SetValue("")
				switch m.formStepIndex {
				case 5:
					m.textInput.Placeholder = "Actual model name (e.g. gpt-4o-2024-08-06)"
				case 6:
					m.textInput.Placeholder = "Context window (e.g. 128000)"
				case 7:
					m.textInput.Placeholder = "Max tokens (e.g. 4096)"
				case 8:
					m.textInput.Placeholder = "Vision support? (yes/no)"
				}
				return m, nil
			}

			// ── Review step (9) — save model & close ─────────────
			ctxWin, _ := strconv.Atoi(m.formValues[6])
			maxTok, _ := strconv.Atoi(m.formValues[7])
			vision := m.formValues[8] == "yes"
			if err := m.addModelToProvider(m.providerSelectedName, m.formValues[4], m.formValues[5], ctxWin, maxTok, vision); err != nil {
				m.formError = err.Error()
				return m, nil
			}
			// If onboarding, route back to the verify step instead of
			// staying in (or closing) the connect modal. The provider
			// and model are already saved above.
			if m.onboardingActive {
				m.modalMode = ModalNone
				m.connectSuccess = false
				m.providerSavedInFlow = false
				m.formStepIndex = 0
				m.formValues = nil
				m.syncTextInputEcho() // audit M2: clear stale echo on close
				m.onboardingStep = obVerify
				m.obVerifying = true
				m.obFinalizeSetup()                                  // set defaults + persist
				return m, tea.Batch(m.obVerifyKeyCmd(), m.tickCmd()) // start async validation + spinner
			}
			// Show the success screen instead of closing abruptly.
			m.connectSuccess = true
			m.providerSavedInFlow = false
			m.formStepIndex = 10
			return m, nil
		} else if m.modalMode == ModalAddModel {
			// Form-based modal: validate and advance steps
			val := strings.TrimSpace(m.textInput.Value())
			if val == "" {
				m.formError = "This field is required"
				return m, nil
			}
			m.formError = ""
			m.formValues[m.formStepIndex] = val
			if m.formStepIndex >= 4 {
				// Last step — save model
				ctxWin, err := strconv.Atoi(m.formValues[2])
				if err != nil {
					m.formError = fmt.Sprintf("Invalid context window: %s", m.formValues[2])
					return m, nil
				}
				maxTok, err := strconv.Atoi(m.formValues[3])
				if err != nil {
					m.formError = fmt.Sprintf("Invalid max tokens: %s", m.formValues[3])
					return m, nil
				}
				vision := m.formValues[4] == "yes"
				if err := m.addModelToProvider(m.providerSelectedName, m.formValues[0], m.formValues[1], ctxWin, maxTok, vision); err != nil {
					m.formError = err.Error()
					return m, nil
				}
				m.modalMode = ModalNone
				m.syncTextInputEcho() // audit M2: clear stale echo on close
				return m, nil
			}
			// Advance to next step
			m.formStepIndex++
			m.textInput.SetValue("")
			switch m.formStepIndex {
			case 1:
				m.textInput.Placeholder = "Actual model name (e.g. gpt-4o-2024-08-06)"
			case 2:
				m.textInput.Placeholder = "Context window (e.g. 128000)"
			case 3:
				m.textInput.Placeholder = "Max tokens (e.g. 4096)"
			case 4:
				m.textInput.Placeholder = "Vision support? (yes/no)"
			}
			return m, nil
		} else if m.modalMode == ModalAddSecret {
			// Form-based modal: name, value, description, tags, scope
			val := m.textInput.Value()
			// Name and value are required; the rest are optional.
			if m.formStepIndex <= 1 && strings.TrimSpace(val) == "" {
				m.formError = "This field is required"
				return m, nil
			}
			m.formError = ""
			// Name must be stored trimmed — trailing spaces become part of
			// the keyring name and break later lookup/delete. The secret
			// value (step 1) is intentionally left untrimmed.
			if m.formStepIndex == 0 {
				val = strings.TrimSpace(val)
			}
			m.formValues[m.formStepIndex] = val
			if m.formStepIndex >= 4 {
				// Last step — save secret
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
			// Advance to next step
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
		} else if len(m.modalItems) > 0 {
			// Defensive clamp: a list reload that shrank the modal (e.g.
			// deleting the last cron job) can leave modalSelectedIdx past
			// the end. Fix the cursor here too so indexing below can never
			// panic; if the list is empty we simply do nothing.
			m.clampModalCursor()
			if m.modalSelectedIdx >= 0 && m.modalSelectedIdx < len(m.modalItems) {
				selectedVal := m.modalItems[m.modalSelectedIdx]
				if m.modalMode == ModalAgent {
					if m.showWelcome {
						m.pendingAgent = selectedVal
					}
					if m.currentKey != "" {
						m.agentLoop.GetProvidable().SetSessionAgent(m.currentKey, selectedVal)
					}
					// Agent name is rendered in the viewport header — force
					// a re-render even if the content fingerprint is unchanged.
					m.lastViewportKey = ""
					m.renderedBaseKey = ""
				} else if m.modalMode == ModalModel {
					if m.showWelcome {
						m.pendingModel = selectedVal
					}
					if m.currentKey != "" {
						m.agentLoop.GetProvidable().SetSessionModel(m.currentKey, selectedVal)
					}
					// Model name is rendered in the bottom bar — re-render.
					m.lastViewportKey = ""
					m.renderedBaseKey = ""
				} else if m.modalMode == ModalThink {
					if m.showWelcome {
						m.pendingThink = selectedVal
					}
					if m.currentKey != "" {
						m.agentLoop.GetProvidable().SetThinkLevel(m.currentKey, selectedVal)
					}
					// Think level is rendered in the bottom bar — re-render.
					m.lastViewportKey = ""
					m.renderedBaseKey = ""
				} else if m.modalMode == ModalSessions {
					if m.modalSelectedIdx < len(m.modalSessionKeys) {
						m.setCurrentChatKey(m.modalSessionKeys[m.modalSelectedIdx])
						m.showWelcome = false
						m.clearStreamingState()
						// Sync pendingModel to the target session's model so
						// that creating a new chat from here inherits it.
						m.pendingModel = m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
					}
				} else if m.modalMode == ModalSubagents {
					if m.modalSelectedIdx < len(m.modalSubagentKeys) {
						// Remember the parent chat so the user can navigate back (ctrl+b)
						if m.parentSessionKey == "" {
							m.parentSessionKey = m.currentKey
						}
						m.setCurrentChatKey(m.modalSubagentKeys[m.modalSelectedIdx])
						m.showWelcome = false
						m.clearStreamingState()
					}
				} else if m.modalMode == ModalLang {
					// Extract language code from "Name (code)" format
					langCode := selectedVal
					if idx := strings.LastIndex(selectedVal, "("); idx != -1 {
						langCode = strings.TrimRight(selectedVal[idx+1:], ")")
					}
					m.cfg.SetLanguage(langCode)
					i18n.SetLanguage(langCode)
					m.chatInput.Placeholder = i18n.T("tui.placeholder")
				} else if m.modalMode == ModalProviders {
					// "+ Connect a provider" action entry.
					if m.modalSelectedIdx < len(m.modalItems) &&
						m.modalItems[m.modalSelectedIdx] == i18n.T("tui.connectAction") {
						return m, m.executeCommand("/connect")
					}
					if m.modalSelectedIdx < len(m.providerModalKeys) {
						providerName := m.providerModalKeys[m.modalSelectedIdx]
						m.providerSelectedName = providerName
						m.modalMode = ModalProviderDetail
						// Build detail view items
						m.modalItems = nil
						m.modalSelectedIdx = 0
						m.modalScrollOffset = 0
						// Show provider info
						snapshot := m.agentLoop.GetProvidable().GetConfigSnapshot()
						if snapshot != nil && snapshot.Providers != nil {
							if p, ok := snapshot.Providers.GetNamed(providerName); ok {
								m.modalItems = append(m.modalItems, fmt.Sprintf("Type: %s", p.Type))
								m.modalItems = append(m.modalItems, fmt.Sprintf("API Base: %s", p.APIBase))
								// Never print raw key material — even short keys.
								keyDisplay := maskAPIKey(p.APIKey)
								if keyDisplay == "" {
									keyDisplay = "(not set)"
								}
								m.modalItems = append(m.modalItems, fmt.Sprintf("API Key: %s", keyDisplay))
							}
						}
						m.modalItems = append(m.modalItems, "---")
						// List models
						models := m.listProviderModels(providerName)
						for _, alias := range models {
							m.modalItems = append(m.modalItems, fmt.Sprintf("  %s", alias))
						}
						if len(models) == 0 {
							m.modalItems = append(m.modalItems, "  (no models)")
						}
						m.modalItems = append(m.modalItems, "---")
						m.modalItems = append(m.modalItems, "+ Add model")
						m.modalItems = append(m.modalItems, "- Delete provider")
					}
					return m, nil
				} else if m.modalMode == ModalProviderDetail {
					if m.modalSelectedIdx < len(m.modalItems) {
						selectedItem := m.modalItems[m.modalSelectedIdx]
						if selectedItem == "+ Add model" {
							m.modalMode = ModalAddModel
							m.formStepIndex = 0
							m.formValues = make([]string, 5)
							m.formError = ""
							m.formConfirmMode = false
							m.textInput.SetValue("")
							m.textInput.Placeholder = "Model alias (e.g. gpt-4o)"
							return m, nil
						} else if selectedItem == "- Delete provider" {
							if err := m.deleteProvider(m.providerSelectedName); err != nil {
								m.formError = err.Error()
								return m, nil
							}
							m.modalMode = ModalNone
							return m, nil
						} else if strings.HasPrefix(selectedItem, "  ") && !strings.HasPrefix(selectedItem, "  (") {
							// Model entry — delete it
							modelAlias := strings.TrimSpace(selectedItem)
							if err := m.deleteModelFromProvider(m.providerSelectedName, modelAlias); err != nil {
								m.formError = err.Error()
								return m, nil
							}
							// Refresh detail view
							providerName := m.providerSelectedName
							m.modalItems = nil
							m.modalSelectedIdx = 0
							m.modalScrollOffset = 0
							snapshot := m.agentLoop.GetProvidable().GetConfigSnapshot()
							if snapshot != nil && snapshot.Providers != nil {
								if p, ok := snapshot.Providers.GetNamed(providerName); ok {
									m.modalItems = append(m.modalItems, fmt.Sprintf("Type: %s", p.Type))
									m.modalItems = append(m.modalItems, fmt.Sprintf("API Base: %s", p.APIBase))
									keyDisplay := maskAPIKey(p.APIKey)
									if keyDisplay == "" {
										keyDisplay = "(not set)"
									}
									m.modalItems = append(m.modalItems, fmt.Sprintf("API Key: %s", keyDisplay))
								}
							}
							m.modalItems = append(m.modalItems, "---")
							models := m.listProviderModels(providerName)
							for _, alias := range models {
								m.modalItems = append(m.modalItems, fmt.Sprintf("  %s", alias))
							}
							if len(models) == 0 {
								m.modalItems = append(m.modalItems, "  (no models)")
							}
							m.modalItems = append(m.modalItems, "---")
							m.modalItems = append(m.modalItems, "+ Add model")
							m.modalItems = append(m.modalItems, "- Delete provider")
							return m, nil
						}
					}
					return m, nil
				} else if m.modalMode == ModalBackgroundExecs {
					if m.bgExecViewMode {
						// We're in output view mode - go back to list
						m.bgExecViewMode = false
						m.bgExecViewOutput = ""
						m.bgExecViewStatus = ""
						return m, m.tickCmd()
					}
					if m.modalSelectedIdx < len(m.bgExecModalKeys) {
						procID := m.bgExecModalKeys[m.modalSelectedIdx]
						m.bgExecViewMode = true
						m.bgExecViewID = procID
						// Fetch initial output
						output, status, _, err := m.agentLoop.GetProvidable().GetBackgroundExecOutput(procID, 5000)
						if err == nil {
							m.bgExecViewOutput = output
							m.bgExecViewStatus = status
						}
						return m, m.tickCmd()
					}
				} else if m.modalMode == ModalCron {
					if m.cronDetailMode {
						// In detail view - go back to list
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
						// In detail view - go back to list
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
					// Shared with handleSkillsEnter so install/toggle cannot drift.
					return m, m.handleSkillsEnter()
				} else if m.modalMode == ModalSkillInstall {
					// Submit repo URL for scanning
					return m, m.handleSkillInstallSubmit()
				} else if m.modalMode == ModalSkillPicker {
					// Install selected skills
					return m, m.handleSkillPickerEnter()
				} else if m.modalMode == ModalSettings {
					// Top-level settings menu: navigate to sub-menu based on selection.
					// Items correspond to: Agents / System / Interface.
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
					// Navigate into the selected sub-group.
					groupIdx := m.modalSelectedIdx
					m.resetModal(ModalSettingsSystemEdit)
					m.settingsSection = sysSubViewName(groupIdx)
					switch groupIdx {
					case sysGroupSession:
						m.loadSessionSettings()
					case sysGroupTools:
						m.loadToolsSettings()
					case sysGroupLogs:
						m.loadLogsSettings()
					case sysGroupLanguage:
						m.loadLanguageSettings()
					case sysGroupGoal:
						m.loadGoalSettings()
					case sysGroupUpdates:
						m.loadUpdatesSettings()
					}
					return m, nil
				} else if m.modalMode == ModalSettingsSystemEdit {
					// System sub-view: inline edit (save), selector confirm,
					// or row action.
					if m.settingsSelectorActive {
						return m, m.handleSelectorConfirm()
					}
					if m.settingsEditField != "" {
						m.handleSystemSettingsInput(m.textInput.Value())
						return m, nil
					}
					return m, m.handleSystemSubEnter()
				} else if m.modalMode == ModalSettingsAgents {
					// Agent list: navigate to detail, defaults, or start
					// the add-agent flow.
					if m.settingsEditField != "" {
						m.handleAgentSettingsInput(m.textInput.Value())
						return m, nil
					}
					return m, m.handleAgentsEnter()
				} else if m.modalMode == ModalSettingsAgentEdit {
					// Agent detail: save inline edit, selector confirm,
					// or handle row action.
					if m.settingsSelectorActive {
						return m, m.handleSelectorConfirm()
					}
					if m.settingsEditField != "" {
						m.handleAgentSettingsInput(m.textInput.Value())
						return m, nil
					}
					return m, m.handleAgentEditEnter()
				} else if m.modalMode == ModalSettingsTUI {
					// Theme picker is active — handle selection.
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
									// Already installed — just apply
									m.applyThemeByName(item.name)
									m.themePreviewName = ""
									m.themePickerActive = false
									m.loadTUISettings()
									m.modalSelectedIdx = 0
									return m, nil
								}
								// Not installed — download and install
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
						// For headers, loading, error — do nothing
						return m, nil
					}
					// Interface settings: handle row action.
					cmd := m.handleTUISettingsEnter()
					// If theme picker was activated, just return with the fetch cmd
					if m.themePickerActive {
						return m, cmd
					}
					// Mouse toggle returns a cmd
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
		// "q" must not close form-based modals — it's a regular
		// character while typing in a text field. Fall through to the
		// textInput forwarding at the end of the modal handler.
		// ESC always closes.
		if msg.String() == "q" && isFormModal(m.modalMode, m.settingsEditField != "") {
			break
		}
		// Theme picker: ESC cancels and returns to the settings list.
		if m.modalMode == ModalSettingsTUI && m.themePickerActive {
			// Revert to the original theme if a preview was active
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
		// TUI settings inline edit: ESC cancels and returns to the list.
		if m.modalMode == ModalSettingsTUI && m.settingsEditField != "" {
			m.settingsEditField = ""
			m.formError = ""
			m.loadTUISettings()
			return m, nil
		}
		if m.modalMode == ModalBackgroundExecs && m.bgExecViewMode {
			// In output view mode: go back to list
			if msg.String() == "q" || msg.String() == "esc" {
				m.bgExecViewMode = false
				m.bgExecViewOutput = ""
				m.bgExecViewStatus = ""
				return m, m.tickCmd()
			}
		}
		if m.modalMode == ModalCron && m.cronDetailMode {
			// In detail view: go back to list
			m.cronDetailMode = false
			m.cronDetailJobID = ""
			m.loadCronJobs()
			return m, m.tickCmd()
		}
		if m.modalMode == ModalSecrets && m.secretsDetailMode {
			// In detail view: go back to list
			m.secretsDetailMode = false
			m.secretsDetailName = ""
			m.secretsReveal = false
			m.loadSecrets()
			return m, m.tickCmd()
		}
		if m.modalMode == ModalSettingsSystemEdit {
			// System sub-view: ESC cancels selector or inline edit,
			// or if not editing goes back to the system group list.
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
			// Agent detail: ESC cancels selector or inline edit, or
			// if not editing goes back to the agents list.
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
			// Go back to agents list
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
			// Back-navigation: settings sub-menus return to the top-level
			// settings menu (Agents / System / Interface).
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
		if m.modalMode == ModalSkillInstall {
			// Go back to skills list
			m.modalMode = ModalSkills
			m.textInput.SetValue("")
			m.formError = ""
			m.loadSkillsList()
			return m, m.tickCmd()
		}
		if m.modalMode == ModalSkillPicker {
			// Go back to install modal
			m.modalMode = ModalSkillInstall
			m.skillsScanResults = nil
			m.skillsSelectedMap = nil
			m.formError = ""
			return m, m.tickCmd()
		}
		// Reset provider-in-flow state when leaving AddProvider modal
		if m.modalMode == ModalAddProvider {
			m.providerSavedInFlow = false
			m.connectSuccess = false
			m.providerTypePicker = false
			// During onboarding, ESC leaves the connect flow and heads
			// back to the provider picker (it never exits the wizard).
			if m.onboardingActive && m.onboardingStep == obConnect {
				m.modalMode = ModalNone
				m.formValues = nil
				m.formStepIndex = 0
				m.onboardingStep = obProviderPicker
				m.modalSelectedIdx = 0
				// Audit M2: leaving the form from a secret step must
				// not leave a stale password echo on the widget.
				m.syncTextInputEcho()
				return m, nil
			}
		}
		m.modalMode = ModalNone
		// Audit M2: ESC-close of a form modal — reset echo so a stale
		// password mode can't affect any later text-input render.
		m.syncTextInputEcho()
	case "s":
		if m.modalMode == ModalBackgroundExecs && !m.bgExecViewMode {
			if m.modalSelectedIdx < len(m.bgExecModalKeys) {
				procID := m.bgExecModalKeys[m.modalSelectedIdx]
				_ = m.agentLoop.GetProvidable().StopBackgroundExec(procID)
				// Refresh the list
				return m, m.executeCommand("/bg")
			}
		}
	case "e":
		// Toggle enable/disable for a cron job
		if m.modalMode == ModalCron && m.cronService != nil {
			jobID := m.selectedCronJobID()
			if jobID != "" {
				if job := m.cronService.GetJob(jobID); job != nil {
					m.cronService.EnableJob(jobID, !job.Enabled)
					if m.cronDetailMode {
						m.cronDetailJobID = jobID
					}
					m.loadCronJobs()
					// Re-select the same job and restore detail mode if needed.
					m.reselectCronJob(jobID)
					return m, m.tickCmd()
				}
			}
		}
	case "r":
		// Run a cron job now
		if m.modalMode == ModalCron && m.cronService != nil {
			jobID := m.selectedCronJobID()
			if jobID != "" {
				_ = m.cronService.RunJobNow(jobID)
				m.loadCronJobs()
				m.reselectCronJob(jobID)
				return m, m.tickCmd()
			}
		}
		// Reveal/hide a secret value in the detail view
		if m.modalMode == ModalSecrets && m.secretsDetailMode {
			m.secretsReveal = !m.secretsReveal
			return m, m.tickCmd()
		}
	case "a":
		// Add a new secret
		if m.modalMode == ModalSecrets && !m.secretsDetailMode {
			if m.keyringSvc() != nil {
				m.startAddSecret()
				return m, m.tickCmd()
			}
		}
	case "d":
		// Delete a cron job
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
		// Delete a secret
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
		// Delete a skill
		if m.modalMode == ModalSkills {
			if m.modalSelectedIdx < len(m.skillsModalKeys) {
				skillName := m.skillsModalKeys[m.modalSelectedIdx]
				if skillName != "" && skillName != "__install__" {
					return m, m.deleteSkillCmd(skillName)
				}
			}
		}
	case " ":
		// Toggle checkbox in skill picker
		if m.modalMode == ModalSkillPicker {
			m.handleSkillPickerToggle()
			return m, m.tickCmd()
		}
	}
	// Forward keystrokes to textInput for form-based modals
	// so users can type in the input fields.
	if isFormModal(m.modalMode, m.settingsEditField != "") {
		// Audit M2: re-derive echo mode from modalMode+formStepIndex
		// before forwarding — single choke point, self-heals across
		// every step transition.
		m.syncTextInputEcho()
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		if m.isSessionProcessing() {
			return m, tea.Batch(cmd, m.tickCmd())
		}
		return m, cmd
	}
	// Restart tick animation if the target session is actively processing.
	// This keeps the loading dots when switching to a busy session/subagent.
	if m.isSessionProcessing() {
		return m, m.tickCmd()
	}
	return m, nil

}
