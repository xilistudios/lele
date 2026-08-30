package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

// sgrMouseEscapeRe matches complete SGR mouse escape sequences:
// ESC [ < col ; row ; button M/m
var sgrMouseEscapeRe = regexp.MustCompile(`(?:\x1b)?\[<\d+;\d+;\d+[Mm]`)

// stripMouseEscapeSequences removes all SGR mouse sequences from s.
func stripMouseEscapeSequences(s string) string {
	return sgrMouseEscapeRe.ReplaceAllString(s, "")
}

// filterAndBufferEscapes combines any previously buffered incomplete escape
// fragment (m.escBuffer) with msg.Runes, strips complete SGR mouse escape
// sequences, stores any trailing incomplete escape fragment back into
// m.escBuffer, and returns the cleaned runes plus a boolean indicating
// whether the message should be forwarded to the text input.
func filterAndBufferEscapes(m *Model, msg tea.KeyMsg) ([]rune, bool) {
	// Combine buffer with incoming runes.
	runes := make([]rune, 0, len(m.escBuffer)+len(msg.Runes))
	runes = append(runes, m.escBuffer...)
	runes = append(runes, msg.Runes...)
	m.escBuffer = m.escBuffer[:0]

	s := string(runes)
	cleaned := stripMouseEscapeSequences(s)

	// Check if the cleaned string ends with an incomplete escape fragment
	// (e.g. lone \x1b, \x1b[, \x1b[<, \x1b[<12, etc.) that might be the
	// start of a sequence split across multiple KeyMsg events.
	// We look for ESC at the end followed by optional incomplete CSI bytes.
	incomplete := findTrailingIncompleteEscape(cleaned)
	if incomplete > 0 {
		// Buffer the incomplete tail for next time.
		m.escBuffer = []rune(cleaned[len(cleaned)-incomplete:])
		cleaned = cleaned[:len(cleaned)-incomplete]
	}

	if len(cleaned) == 0 {
		return nil, false // nothing to pass through
	}

	return []rune(cleaned), true
}

// findTrailingIncompleteEscape returns the number of trailing runes that look
// like the start of an incomplete SGR mouse escape sequence (ESC + partial
// CSI, or a headless "[<" partial SGR mouse fragment without the leading ESC).
// Returns 0 if the string doesn't end with such a fragment.
func findTrailingIncompleteEscape(s string) int {
	if len(s) == 0 {
		return 0
	}
	runes := []rune(s)
	n := len(runes)

	// Walk backwards to find the last ESC character.
	for i := n - 1; i >= 0; i-- {
		if runes[i] == 0x1b {
			// Everything from here to the end could be an incomplete sequence.
			fragment := string(runes[i:])
			tail := fragment[1:] // strip leading ESC

			// If the tail is empty (just a lone ESC), it's incomplete.
			if len(tail) == 0 {
				return n - i
			}
			// If it starts with [ or [<  followed by digits/; but no final byte, it's incomplete.
			if strings.HasPrefix(tail, "[<") {
				rest := tail[2:]
				if len(rest) == 0 {
					return n - i // just "\x1b[<"
				}
				// All remaining chars should be digits or ';' for an in-progress sequence.
				allParams := true
				for _, c := range rest {
					if !((c >= '0' && c <= '9') || c == ';') {
						allParams = false
						break
					}
				}
				if allParams {
					return n - i // incomplete: "\x1b[<NNN;NNN"
				}
			} else if tail == "[" {
				return n - i // just "\x1b["
			}
			// Otherwise it's a complete or unrelated sequence — don't buffer.
			return 0
		}
	}

	// No ESC found. Bubbletea may have consumed the ESC byte, leaving a
	// headless partial SGR mouse fragment like "[<", "[<65", or "[<65;57;25".
	// Buffer it if the string ends with "[<" followed only by digits/';'.
	// Scan backwards from the last '[' we can find.
	for i := n - 1; i >= 0; i-- {
		if runes[i] == '[' {
			// Must be followed by '<' (if there's room) to be an SGR mouse fragment.
			if i+1 >= n {
				// Trailing "[" alone — not a mouse fragment, ignore.
				return 0
			}
			if runes[i+1] != '<' {
				return 0
			}
			// "[<" present — check that everything after is digits/';'.
			rest := string(runes[i+2:])
			if len(rest) == 0 {
				return n - i // just "[<"
			}
			allParams := true
			for _, c := range rest {
				if !((c >= '0' && c <= '9') || c == ';') {
					allParams = false
					break
				}
			}
			if allParams {
				return n - i // incomplete headless SGR mouse fragment
			}
			// Contains non-param chars — not a mouse fragment.
			return 0
		}
	}

	return 0
}

// Update is the Bubble Tea entry point. It delegates to update and then
// synchronizes the chat input focus with the current application state, so
// the input cursor is only visible when the input is the active surface
// (no modal open, no pending approval, no onboarding wizard).
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	mm, ok := model.(*Model)
	if !ok {
		return model, cmd
	}
	if focusCmd := mm.syncChatInputFocus(); focusCmd != nil {
		cmd = tea.Batch(cmd, focusCmd)
	}
	return mm, cmd
}

// syncChatInputFocus aligns the chat textarea's focus state with the app
// state. The input is considered active only when no modal is open, no
// command approval is pending, and the onboarding wizard is not running.
// Blurring hides the blinking cursor; focusing restores it and restarts the
// blink loop via the returned command. Returns a non-nil tea.Cmd only when
// the focus state transitions from blurred to focused, so callers can batch
// it without spamming blink commands on every update.
func (m *Model) syncChatInputFocus() tea.Cmd {
	shouldFocus := m.modalMode == ModalNone &&
		m.pendingApprovalID == "" &&
		!m.onboardingActive
	if shouldFocus {
		if !m.chatInput.Focused() {
			return m.chatInput.Focus()
		}
		return nil
	}
	if m.chatInput.Focused() {
		m.chatInput.Blur()
	}
	return nil
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case communityIndexMsg:
		m.communityLoading = false
		if msg.err != "" {
			m.communityErr = msg.err
		} else {
			m.communityErr = ""
			m.communityIndex = msg.entries
		}
		if m.themePickerActive {
			m.loadThemePickerItems()
		}
		return m, nil
	case installThemeMsg:
		m.communityLoading = false
		if msg.err != "" {
			m.communityErr = msg.err
		} else {
			m.communityErr = ""
			if m.customThemes == nil {
				m.customThemes = make(map[string]theme.Theme)
			}
			m.customThemes[msg.name] = msg.theme
			m.installedCommunity = theme.AddInstalledCommunity(msg.name, m.installedCommunity)
			m.applyThemeByName(msg.name)
		}
		if m.themePickerActive {
			m.loadThemePickerItems()
		}
		return m, nil
	case tea.KeyMsg:
		// Handle onboarding wizard keys. The obConnect step reuses the
		// ModalAddProvider form modal, so its keys are delegated to the
		// normal modal handler below (it must NOT be intercepted here).
		if m.onboardingActive && m.onboardingStep != obConnect {
			return m.handleOnboardingKey(msg)
		}
		if m.modalMode != ModalNone {
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
										keyDisplay := p.APIKey
										if len(keyDisplay) > 8 {
											keyDisplay = keyDisplay[:4] + "..." + keyDisplay[len(keyDisplay)-4:]
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
											keyDisplay := p.APIKey
											if len(keyDisplay) > 8 {
												keyDisplay = keyDisplay[:4] + "..." + keyDisplay[len(keyDisplay)-4:]
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
							// Handle skills list selection
							if m.modalSelectedIdx < len(m.skillsModalKeys) {
								selectedKey := m.skillsModalKeys[m.modalSelectedIdx]
								if selectedKey == "__install__" {
									// Switch to install modal
									m.modalMode = ModalSkillInstall
									m.textInput.SetValue("")
									m.textInput.Placeholder = "user/repo or user/repo/skill-name"
									m.formError = ""
									return m, m.tickCmd()
								} else if selectedKey != "" {
									// Toggle skill enabled/disabled
									return m, m.toggleSkillCmd(selectedKey)
								}
							}
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
							if m.modalSelectedIdx == 1 {
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
						return m, nil
					}
				}
				m.modalMode = ModalNone
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
			if m.modalMode == ModalAddProvider || m.modalMode == ModalAddModel || m.modalMode == ModalAddSecret || m.modalMode == ModalSkillInstall ||
				(m.modalMode == ModalSettingsTUI && m.settingsEditField != "") ||
				(m.modalMode == ModalSettingsSystemEdit && m.settingsEditField != "") ||
				(m.modalMode == ModalSettingsAgentEdit && m.settingsEditField != "") ||
				(m.modalMode == ModalSettingsAgents && m.settingsEditField != "") {
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

		// Handle command approval when pending — intercept keys before normal input.
		// The agent is blocked waiting for the user's decision, so this takes priority
		// over typing new messages. Scrolling is still allowed to review context.
		if m.pendingApprovalID != "" && m.modalMode == ModalNone {
			switch msg.String() {
			case "y", "Y":
				m.handleApproval(true)
				return m, nil
			case "n", "N", "esc":
				m.handleApproval(false)
				return m, nil
			case "up", "down", "pgup", "pgdown":
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				if msg.String() == "up" || msg.String() == "pgup" {
					m.maybeExpandRenderWindow()
				}
				return m, cmd
			}
			// Block all other input while approval is pending
			return m, nil
		}

		if m.showAutocomplete {
			switch msg.String() {
			case "up", "ctrl+k":
				if m.autocompleteIdx > 0 {
					m.autocompleteIdx--
				} else {
					m.autocompleteIdx = len(m.autocompleteItems) - 1
				}
				return m, nil
			case "down", "ctrl+j":
				if m.autocompleteIdx < len(m.autocompleteItems)-1 {
					m.autocompleteIdx++
				} else {
					m.autocompleteIdx = 0
				}
				return m, nil
			case "tab":
				if len(m.autocompleteItems) > 0 {
					completed := m.autocompleteItems[m.autocompleteIdx].name
					m.chatInput.SetValue(completed)
					m.showAutocomplete = false
					// Tab only fills the input — lets the user add arguments
					// before pressing Enter to execute.
					return m, nil
				}
				m.showAutocomplete = false
			case "enter":
				if len(m.autocompleteItems) > 0 {
					completed := m.autocompleteItems[m.autocompleteIdx].name
					m.showAutocomplete = false
					// If the user already typed arguments beyond the command
					// name (e.g. "/goal achieve X"), execute the full input
					// directly instead of just the completed command.
					inputVal := m.chatInput.Value()
					if strings.HasPrefix(inputVal, completed) && len(inputVal) > len(completed) && inputVal[len(completed)] == ' ' {
						m.chatInput.SetValue("")
						cmd := m.executeCommand(inputVal)
						if cmd != nil {
							return m, cmd
						}
						return m, nil
					}
					// /goal needs a text argument — fill but don't execute.
					if completed == "/goal" {
						m.chatInput.SetValue(completed)
						return m, nil
					}
					// All other commands execute immediately.
					m.chatInput.SetValue("")
					cmd := m.executeCommand(completed)
					if cmd != nil {
						return m, cmd
					}
					return m, nil
				}
				// No autocomplete items — dismiss autocomplete and let
				// Enter fall through to send the message.
				m.showAutocomplete = false
			case "esc":
				m.showAutocomplete = false
				return m, nil
			}
		}

		if msg.Type == tea.KeyEsc {
			m.lastEscTime = time.Now()
		}
		switch msg.String() {
		case "esc":
			if m.isSessionProcessing() || m.processing {
				now := time.Now()
				if now.Sub(m.escLastPress) < 1*time.Second {
					// Double press detected - cancel the agent
					m.escPressCount = 0
					m.escHint = false
					m.agentLoop.GetProvidable().StopAgent(m.currentKey)
					m.clearStreamingState()
					m.reloadSessions()
				} else {
					// First press - show hint
					m.escPressCount = 1
					m.escHint = true
				}
				m.escLastPress = now
				return m, nil
			}

		case "tab":
			// Cycle mode: agent -> chat -> [group ->] agent.
			// Only when no autocomplete and no modal is active.
			if m.showAutocomplete || m.modalMode != ModalNone {
				return m, nil
			}
			if m.cfg.Groups.Enabled {
				switch m.currentMode {
				case ModeAgent:
					m.currentMode = ModeChat
				case ModeChat:
					m.currentMode = ModeGroup
				case ModeGroup:
					m.currentMode = ModeAgent
				}
			} else {
				// Groups disabled: toggle between agent and chat only
				switch m.currentMode {
				case ModeAgent:
					m.currentMode = ModeChat
				default:
					m.currentMode = ModeAgent
				}
			}
			m.reloadSessions()
			return m, nil

		case "ctrl+c":
			m.cancel()
			return m, tea.Quit

		case "ctrl+b":
			// Go back to parent chat if currently viewing a subagent session.
			if m.parentSessionKey != "" {
				m.setCurrentChatKey(m.parentSessionKey)
				m.parentSessionKey = ""
				m.clearStreamingState()
				m.reloadSessions()
			}
			// Restart tick animation if the parent session has active subagents.
			if m.isSessionProcessing() {
				return m, m.tickCmd()
			}
			return m, nil

		case "ctrl+p":
			m.showAutocomplete = true
			m.filterAutocomplete("/")
			return m, nil

		case "ctrl+m":
			cmd := m.executeCommand("/models")
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, nil

		case "ctrl+a":
			cmd := m.executeCommand("/agents")
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, nil

		case "ctrl+t":
			// Toggle mouse capture as fallback for terminals without Shift bypass.
			m.mouseEnabled = !m.mouseEnabled
			if m.mouseEnabled {
				return m, tea.EnableMouseCellMotion
			}
			return m, tea.DisableMouse

		case "ctrl+s":
			cmd := m.executeCommand("/sessions")
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, nil

		case "ctrl+,":
			cmd := m.executeCommand("/settings")
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, nil

		case "ctrl+y":
			m.copyLastAssistantMessage()

		case "up", "down", "pgup", "pgdown":
			// Group mode welcome: cycle profile selection with up/down arrows
			if (msg.String() == "up" || msg.String() == "down") &&
				m.currentMode == ModeGroup && m.showWelcome &&
				!m.showAutocomplete && m.modalMode == ModalNone {
				profiles := m.getGroupProfiles()
				if len(profiles) > 0 {
					if msg.String() == "up" {
						m.groupProfileIdx--
						if m.groupProfileIdx < 0 {
							m.groupProfileIdx = len(profiles) - 1
						}
					} else {
						m.groupProfileIdx++
						if m.groupProfileIdx >= len(profiles) {
							m.groupProfileIdx = 0
						}
					}
					return m, nil
				}
			}
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			if msg.String() == "up" || msg.String() == "pgup" {
				m.maybeExpandRenderWindow()
			}
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		case "home":
			m.viewport.GotoTop()
			return m, nil
		case "end":
			m.viewport.GotoBottom()
			return m, nil

		case "enter":
			inputVal := m.chatInput.Value()

			// Group mode: wrap non-command input as /group start <profileID> <task>
			if m.currentMode == ModeGroup && !strings.HasPrefix(inputVal, "/") &&
				strings.TrimSpace(inputVal) != "" {
				profiles := m.getGroupProfiles()
				if len(profiles) > 0 && m.groupProfileIdx >= 0 && m.groupProfileIdx < len(profiles) {
					cmd := m.submitGroupStart(profiles[m.groupProfileIdx].ID, inputVal)
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
					return m, tea.Batch(cmds...)
				}
				// No valid profiles — fall through to normal submitMessage
			}

			if strings.HasPrefix(inputVal, "/") {
				cmd := m.executeCommand(inputVal)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chatInput.SetValue("")
			} else if !m.processing && !m.hasRunningSubagents() {
				cmd := m.submitMessage()
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}

	case tea.MouseMsg:
		if !m.mouseEnabled {
			break
		}
		// Handle mouse scrolling on the viewport
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			if msg.Button == tea.MouseButtonWheelUp {
				m.maybeExpandRenderWindow()
			}
			return m, cmd
		}

		// Handle text selection via click+drag in the viewport area
		if msg.Button == tea.MouseButtonLeft {
			leftWidth := int(float64(m.width) * leftColumnRatio)
			inViewportArea := msg.X < leftWidth-1 && msg.Y < m.viewport.Height

			switch msg.Action {
			case tea.MouseActionPress:
				if inViewportArea && m.modalMode == ModalNone {
					m.startSelection(msg.X, msg.Y)
					return m, nil
				}
			case tea.MouseActionMotion:
				if m.selecting {
					m.updateSelection(msg.X, msg.Y)
					return m, nil
				}
			case tea.MouseActionRelease:
				if m.selecting {
					m.finishSelection()
					return m, nil
				}
			}
		}

		// Handle mouse clicks on subagent items in the sidebar (only if no modal is active)
		if m.modalMode == ModalNone && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			// Check if click is in the right sidebar area
			leftWidth := int(float64(m.width) * leftColumnRatio)
			rightWidth := m.width - leftWidth - 3
			sidebarStartX := leftWidth + 1 // +1 for separator

			if msg.X >= sidebarStartX && msg.X < sidebarStartX+rightWidth {
				// Check if click matches any subagent click target Y-coordinate in the sidebar text
				for _, target := range m.subagentClickTargets {
					if msg.Y >= target.yStart && msg.Y < target.yEnd {
						// Clicked on a subagent item - switch to that subagent's chat
						if m.parentSessionKey == "" {
							m.parentSessionKey = m.currentKey
						}
						m.setCurrentChatKey(target.key)
						m.showWelcome = false
						m.clearStreamingState()
						m.reloadSessions()
						// Restart tick animation if the target subagent is actively processing.
						if m.isSessionProcessing() {
							return m, m.tickCmd()
						}
						return m, nil
					}
				}
			}
		}

	case tickMsg:
		// Reset tick pending flag to allow the next tick to be scheduled
		m.tickPending = false
		// Clear selection feedback after timeout
		m.clearSelectionFeedback()
		// Onboarding verify spinner — animate while we wait for the async
		// key validation result.
		if m.obVerifying {
			m.animationTick++
			cmds = append(cmds, m.tickCmd())
		}
		// Refresh background exec output if viewing a process
		if m.modalMode == ModalBackgroundExecs && m.bgExecViewMode && m.bgExecViewID != "" {
			output, status, _, _ := m.agentLoop.GetProvidable().GetBackgroundExecOutput(m.bgExecViewID, 5000)
			m.bgExecViewOutput = output
			m.bgExecViewStatus = status
			if status == "running" {
				cmds = append(cmds, m.tickCmd())
			}
			// Don't fall through to the session processing tick
			break
		}
		if m.isSessionProcessing() {
			m.elapsedTime = time.Since(m.startTime)
			m.animationTick++
			cmds = append(cmds, m.tickCmd())
		}
		if m.escHint && time.Since(m.escLastPress) > escHintTimeout {
			m.escHint = false
			m.escPressCount = 0
		}

	case outboundMsg:
		// Approval prompts must never be dropped by the currentKey gate below:
		// the blocked agent waits on ApprovalManager's full timeout otherwise.
		// Requests for the visible session surface immediately; requests for
		// background sessions are stashed and restored on switch.
		if msg.msg.Event == "approval.request" {
			id := msg.msg.Metadata["id"]
			command := msg.msg.Metadata["command"]
			reason := msg.msg.Metadata["reason"]
			if msg.msg.ChatID == m.currentKey {
				m.queueApprovalForCurrentChat(id, command, reason)
			} else {
				m.stashApprovalForSession(msg.msg.ChatID, id, command, reason)
			}
			m.updateViewport()
			break
		}
		if msg.msg.ChatID == m.currentKey {
			switch msg.msg.Event {
			case "subagent.result":
				// The result is also queued as a system message for the parent.
				// Keep loading active while that continuation waits for the session
				// lock, even if the original turn has already completed.
				if !m.processing {
					m.parentCompletionObserved = true
				}
				m.pendingSubagentCompletions++
				m.processing = true
				m.startTime = time.Now()
				m.lastDuration = 0
				m.invalidateSubagentsCache() // subagent status changed
				m.updateViewport()
				cmds = append(cmds, m.tickCmd())
			case "message.stream":
				// Clear pending user message on first stream chunk — by the time
				// the LLM starts streaming, the user message is already in history.
				if m.pendingUserMessage != "" {
					m.pendingUserMessage = ""
					m.renderedBaseKey = "" // invalidate cache so history re-renders without duplicate
				}
				m.currentToolAction = "" // streaming text means tool call is done
				// Only reset streaming state if the MessageID is truly different.
				if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
					m.currentAssistantMsgID = msg.msg.MessageID
					m.resetStreamState()
				}
				m.currentStream += msg.msg.Content
				if cmd := m.throttledUpdateViewport(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case "message.thinking":
				// Only reset streaming state if the MessageID is truly different.
				if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
					m.currentAssistantMsgID = msg.msg.MessageID
					m.resetStreamState()
				}
				m.currentThinking += msg.msg.Content
				if cmd := m.throttledUpdateViewport(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case "tool.executing":
				// Only clear streaming state if it's a different message.
				if msg.msg.MessageID != "" && msg.msg.MessageID != m.currentAssistantMsgID {
					m.currentStream = ""
					m.currentThinking = ""
					m.currentAssistantMsgID = msg.msg.MessageID
				}

				// Show the currently executing tool call in the viewport.
				// Use the compact "tool: action" format from metadata.
				toolName := msg.msg.Metadata["tool"]
				action := msg.msg.Metadata["action"]
				if action != "" {
					m.currentToolAction = action
				} else if toolName != "" {
					m.currentToolAction = toolName
				}
				m.updateViewport()
			case "tool.result":
				// Tool completed; clear the active tool action display.
				m.currentToolAction = ""
				// Clear any approval result feedback when the tool completes.
				m.approvalResult = ""
				// When a spawn tool completes, clear its subagent progress entry.
				if msg.msg.Metadata["tool"] == "spawn" {
					m.invalidateSubagentsCache() // a new subagent task now exists
					if saKey := msg.msg.Metadata["subagent_session_key"]; saKey != "" {
						// Extract the task ID suffix (e.g. "subagent-1") from the session key
						if idx := strings.LastIndex(saKey, ":"); idx >= 0 {
							delete(m.subagentProgress, saKey[idx+1:])
						}
					}
				}
				m.updateViewport()
			case "group.status":
				// Group status event: started/done/stopped/error
				groupID := msg.msg.Metadata["group_id"]
				if groupID == "" {
					break
				}
				status := msg.msg.Metadata["status"]
				if m.groupStatus == nil {
					m.groupStatus = make(map[string]string)
				}
				m.groupStatus[groupID] = status
				// Track active group and store participants metadata
				if status == "started" {
					if m.activeGroupID == "" {
						m.activeGroupID = groupID
					}
					m.processing = true
					if participants := msg.msg.Metadata["participants"]; participants != "" {
						if m.groupMeta == nil {
							m.groupMeta = make(map[string]groupMeta)
						}
						if gm, ok := m.groupMeta[groupID]; ok {
							gm.participants = participants
							m.groupMeta[groupID] = gm
						} else {
							m.groupMeta[groupID] = groupMeta{participants: participants}
						}
					}
				}
				// Stop processing when the group finishes
				if status == "done" || status == "stopped" || status == "error" {
					m.processing = false
				}
				m.updateViewport()
			case "group.turn":
				// Group turn: an agent produced a response within the group
				groupID := msg.msg.Metadata["group_id"]
				if groupID == "" {
					break
				}
				if m.groupTranscripts == nil {
					m.groupTranscripts = make(map[string][]groupTurn)
				}
				layer, _ := strconv.Atoi(msg.msg.Metadata["layer"])
				turnIndex, _ := strconv.Atoi(msg.msg.Metadata["turn_index"])
				turn := groupTurn{
					index:   turnIndex,
					layer:   layer,
					speaker: msg.msg.Metadata["speaker"],
					label:   msg.msg.Metadata["label"],
					role:    msg.msg.Metadata["role"],
					content: msg.msg.Content,
				}
				m.groupTranscripts[groupID] = append(m.groupTranscripts[groupID], turn)
				m.processing = true
				if m.activeGroupID == "" {
					m.activeGroupID = groupID
				}
				m.updateViewport()
			case "group.complete":
				// Group complete: final synthesis available
				groupID := msg.msg.Metadata["group_id"]
				if groupID == "" {
					break
				}
				if m.groupMeta == nil {
					m.groupMeta = make(map[string]groupMeta)
				}
				layers, _ := strconv.Atoi(msg.msg.Metadata["layers"])
				totalTokens, _ := strconv.Atoi(msg.msg.Metadata["total_tokens"])
				gm := m.groupMeta[groupID]
				gm.strategy = msg.msg.Metadata["strategy"]
				gm.layers = layers
				gm.totalTokens = totalTokens
				gm.synthesis = msg.msg.Content
				m.groupMeta[groupID] = gm
				m.processing = false
				if m.groupStatus == nil {
					m.groupStatus = make(map[string]string)
				}
				m.groupStatus[groupID] = "done"
				m.updateViewport()
			case "":
				// Stream finished — flush any pending throttled viewport update
				// so the final content is visible immediately.
				m.flushStreamUpdate()
				idMatches := msg.msg.MessageID == m.currentMessageID ||
					msg.msg.ReplyTo == m.currentMessageID ||
					(m.currentMessageID != "" && strings.HasPrefix(msg.msg.MessageID, m.currentMessageID+"-"))

				if m.processing && (m.pendingSubagentCompletions > 0 || idMatches) {
					cmds = append(cmds, func() tea.Msg {
						return completeMsg{sessionKey: m.currentKey}
					})
				}
			}
		} else if m.isSubagentOfCurrentChat(msg.msg.ChatID) {
			// Events from subagents spawned by the current parent chat.
			// Capture their tool.executing / tool.result events to show
			// real-time progress in the parent viewport.
			taskID := m.currentSubagentTaskID(msg.msg.ChatID)
			if taskID != "" {
				switch msg.msg.Event {
				case "tool.executing":
					action := msg.msg.Metadata["action"]
					if action == "" {
						action = msg.msg.Metadata["tool"]
					}
					if action != "" {
						if m.subagentProgress == nil {
							m.subagentProgress = make(map[string]string)
						}
						m.subagentProgress[taskID] = action
						m.updateViewport()
					}
				case "message.stream":
					// Subagent is streaming a final response — update progress label.
					if m.subagentProgress == nil {
						m.subagentProgress = make(map[string]string)
					}
					m.subagentProgress[taskID] = "finalizing…"
					m.updateViewport()
				}
			}
		}
		cmds = append(cmds, m.startOutboundListener())

	case completeMsg:
		if msg.sessionKey == m.currentKey {
			if m.pendingSubagentCompletions > 0 {
				if !m.parentCompletionObserved {
					// This is the completion of the original parent turn. The
					// queued subagent result will start another parent turn.
					m.parentCompletionObserved = true
					m.reloadSessions()
					break
				}
				m.pendingSubagentCompletions--
				if m.pendingSubagentCompletions > 0 {
					m.reloadSessions()
					break
				}
			}
			m.parentCompletionObserved = true
			m.lastDuration = time.Since(m.startTime)
			m.clearStreamingState()
			// clearStreamingState preserves an active backend session when
			// navigating between chats. This completion belongs to the current
			// turn, so its local loading state must be cleared explicitly.
			m.processing = false
			m.reloadSessions()

			// Schedule a follow-up tick so the view re-renders once the
			// backend's deferred session-cancel cleanup runs. Without this
			// tick the loading indicator can stay stuck because
			// isSessionProcessing() still returns true via the backend check
			// even after m.processing is false, and no event triggers a
			// re-render to detect the backend state change.
			if m.isSessionProcessing() {
				cmds = append(cmds, m.tickCmd())
			}
		}

	case compactResultMsg:
		if msg.sessionKey == m.currentKey {
			m.lastDuration = time.Since(m.startTime)
			m.processing = false
			m.currentToolAction = ""
			m.compactFeedback = msg.result
			m.forceGotoBottom = true
			m.reloadSessions()
		}

	case skillsScanResultMsg:
		return m, m.handleSkillScanResult(msg)

	case skillsInstallResultMsg:
		return m, m.handleSkillInstallResult(msg)

	case skillToggleResultMsg:
		return m, m.handleSkillToggleResult(msg)

	case skillDeleteResultMsg:
		return m, m.handleSkillDeleteResult(msg)

	case obVerifyResultMsg:
		m.obVerifying = false
		if !msg.success {
			m.obVerifyFailed = true
		}
		m.onboardingStep = obDone
		m.obFinalizeSetup()
		return m, nil

	case streamThrottleMsg:
		m.streamThrottleActive = false
		if m.streamPendingUpdate {
			m.updateViewport()
			m.streamPendingUpdate = false
			m.streamThrottleActive = true
			cmds = append(cmds, tea.Tick(m.streamThrottleInterval, func(t time.Time) tea.Msg {
				return streamThrottleMsg{}
			}))
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Invalidate ALL render caches so content is re-wrapped to the new width.
		// View() recalculates viewport dimensions on every render, so we don't
		// need to set them here — we just need to ensure caches are cleared.
		m.streamRenderedLines = nil
		m.thinkingRenderedLines = nil
		m.streamRenderedJoined = ""
		m.thinkingRenderedJoined = ""
		m.renderedBaseValid = false
		m.renderedBaseKey = ""
		m.msgRenderCacheLines = nil // width changed — all rendered output is stale
		m.cachedRenderer = nil
		m.cachedRendererWidth = 0

		m.updateViewport()
	}
	if m.modalMode == ModalNone {
		// Only forward message types the textarea knows how to handle.
		// Custom TUI messages (outboundMsg, tickMsg, completeMsg, streamThrottleMsg, tea.MouseMsg) must be
		// excluded to prevent garbage characters in the input field.
		switch msg := msg.(type) {
		case outboundMsg, completeMsg, tickMsg, streamThrottleMsg, tea.MouseMsg, compactResultMsg, skillsScanResultMsg, skillsInstallResultMsg, skillToggleResultMsg, skillDeleteResultMsg:
			// skip — not relevant to textarea
		case tea.KeyMsg:
			if m.isEscapeSequenceFragment(msg) {
				break
			}
			// For rune messages, filter out SGR mouse escape sequences
			// that bubbletea may have failed to parse as tea.MouseMsg.
			if msg.Type == tea.KeyRunes {
				cleaned, consume := filterAndBufferEscapes(m, msg)
				if !consume {
					break
				}
				msg.Runes = cleaned
			}
			// Do NOT forward "enter" to textarea — it's handled above for sending.
			// Do NOT forward "ctrl+m" — it's used for /models.
			if msg.String() == "enter" || msg.String() == "ctrl+m" {
				break
			}
			var cmd tea.Cmd
			m.chatInput, cmd = m.chatInput.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			// Defensive: strip any mouse escape sequences that slipped through.
			if cleaned := stripMouseEscapeSequences(m.chatInput.Value()); cleaned != m.chatInput.Value() {
				m.chatInput.SetValue(cleaned)
			}
		default:
			var cmd tea.Cmd
			m.chatInput, cmd = m.chatInput.Update(msg)
			cmds = append(cmds, cmd)
		}

		val := m.chatInput.Value()
		if strings.HasPrefix(val, "/") {
			m.showAutocomplete = true
			m.filterAutocomplete(val)
		} else {
			m.showAutocomplete = false
		}
	}

	return m, tea.Batch(cmds...)
}

// handleOnboardingKey handles key presses while the first-run onboarding
// wizard is active. Esc never quits the app here — it only opens the skip
// confirmation or steps back to the previous wizard step.
func (m *Model) handleOnboardingKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.onboardingStep {
	case obWelcome:
		if m.obSkipConfirm {
			// Skip confirmation dialog
			switch msg.String() {
			case "up", "k":
				m.obSelectedPreset = 0 // toggle selection
			case "down", "j":
				m.obSelectedPreset = 1
			case "enter":
				if m.obSelectedPreset == 0 {
					// "Yes, skip" — exit onboarding
					m.onboardingActive = false
					m.obSkipConfirm = false
					m.cfg.TUI.OnboardingCompleted = true
					m.saveConfigToDisk()
				} else {
					// "No, continue" — dismiss confirmation
					m.obSkipConfirm = false
				}
			case "esc":
				m.obSkipConfirm = false
			}
			return m, nil
		}
		switch msg.String() {
		case "enter":
			m.onboardingStep = obLanguage
			m.modalSelectedIdx = 0
		case "esc":
			m.obSkipConfirm = true
			m.obSelectedPreset = 1 // default to "No, continue"
		}

	case obLanguage:
		langs := []string{"en", "es", "pt"}
		switch msg.String() {
		case "up", "k":
			if m.modalSelectedIdx > 0 {
				m.modalSelectedIdx--
			}
		case "down", "j":
			if m.modalSelectedIdx < len(langs)-1 {
				m.modalSelectedIdx++
			}
		case "enter":
			lang := langs[m.modalSelectedIdx]
			i18n.SetLanguage(lang)
			m.cfg.Language = lang
			m.onboardingStep = obTheme
			m.themePreviewName = m.currentThemeName // save for Esc revert
			// Pre-select the current theme in the picker
			names := theme.Builtins()
			m.modalSelectedIdx = 0
			for i, n := range names {
				if n == m.currentThemeName {
					m.modalSelectedIdx = i
					break
				}
			}
		case "esc":
			m.onboardingStep = obWelcome
			m.obSkipConfirm = false
		}

	case obTheme:
		names := theme.Builtins()
		switch msg.String() {
		case "up", "k":
			if m.modalSelectedIdx > 0 {
				m.modalSelectedIdx--
			}
			// Live preview
			m.previewTheme(names[m.modalSelectedIdx])
		case "down", "j":
			if m.modalSelectedIdx < len(names)-1 {
				m.modalSelectedIdx++
			}
			// Live preview
			m.previewTheme(names[m.modalSelectedIdx])
		case "enter":
			name := names[m.modalSelectedIdx]
			m.applyThemeByName(name) // persist
			m.themePreviewName = ""
			m.onboardingStep = obProviderPicker
			m.modalSelectedIdx = 0
		case "esc":
			// Revert to the saved theme
			if m.themePreviewName != "" {
				m.previewTheme(m.themePreviewName)
				m.currentThemeName = m.themePreviewName
				m.themePreviewName = ""
			}
			m.onboardingStep = obLanguage
			m.modalSelectedIdx = 0
		}

	case obProviderPicker:
		// Total items: len(providerPresets) + 2 (other/custom + skip)
		totalItems := len(providerPresets) + 2
		switch msg.String() {
		case "up", "k":
			if m.modalSelectedIdx > 0 {
				m.modalSelectedIdx--
			}
		case "down", "j":
			if m.modalSelectedIdx < totalItems-1 {
				m.modalSelectedIdx++
			}
		case "enter":
			if m.modalSelectedIdx < len(providerPresets) {
				// Selected a preset — pre-fill the connect flow.
				m.obSelectedPreset = m.modalSelectedIdx
				m.onboardingStep = obConnect
				m.startConnectFlow(&providerPresets[m.modalSelectedIdx])
			} else if m.modalSelectedIdx == len(providerPresets) {
				// "Other / custom" — set a sentinel, no pre-fill.
				m.obSelectedPreset = -1
				m.onboardingStep = obConnect
				m.startConnectFlow(nil)
			} else {
				// "Skip for now" — exit onboarding
				m.onboardingActive = false
				m.cfg.TUI.OnboardingCompleted = true
				m.saveConfigToDisk()
			}
			m.modalSelectedIdx = 0
		case "esc":
			m.onboardingStep = obTheme
			// Restore theme selection index
			names := theme.Builtins()
			m.modalSelectedIdx = 0
			for i, n := range names {
				if n == m.currentThemeName {
					m.modalSelectedIdx = i
					break
				}
			}
		}

	case obVerify:
		// During verification, only Esc to skip ahead to the done screen.
		if msg.String() == "esc" {
			m.onboardingStep = obDone
			m.obVerifying = false
			m.obFinalizeSetup() // set defaults + persist before showing done
		}

	case obDone:
		if msg.String() == "enter" {
			// Clear onboarding, return to the welcome screen so the user
			// can start chatting from the normal entry point.
			m.onboardingActive = false
			m.showWelcome = true
			m.obVerifying = false
			m.obVerifyFailed = false
			m.chatInput.Focus()
		}
	}
	return m, nil
}

// isEscapeSequenceFragment detects and consumes fragments of CSI escape
// sequences that leak through as tea.KeyMsg (common with mouse scroll in
// terminals like Konsole). It uses a state machine to track multi-rune
// sequences, with a 200ms safety timeout to avoid blocking legitimate input.
func (m *Model) isEscapeSequenceFragment(msg tea.KeyMsg) bool {
	now := time.Now()

	// Step 1: Safety timeout — if we've been in a sequence for >200ms without
	// a new rune, the sequence is stale. Reset and let input through.
	if m.escSeqActive && now.Sub(m.escSeqLastRune) > 200*time.Millisecond {
		m.escSeqActive = false
	}

	// Step 2: Detect START of a new escape sequence.
	if !m.escSeqActive {
		// Case A: bubbletea delivers ESC as tea.KeyEscape.
		if msg.Type == tea.KeyEscape {
			m.escSeqActive = true
			m.escSeqLastRune = now
			return true
		}
		// Case A-2: ESC rune delivered inside msg.Runes (bubbletea grouped bytes)
		for _, r := range msg.Runes {
			if r == 0x1b {
				m.escSeqActive = true
				m.escSeqLastRune = now
				return true
			}
		}
		// Case B: The '[' or '<' that arrives immediately after an ESC
		// (within 50ms) — bubbletea may split ESC from the rest.
		if time.Since(m.lastEscTime) < 50*time.Millisecond && len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r == '[' || r == '<' {
				m.escSeqActive = true
				m.escSeqLastRune = now
				return true
			}
		}
		return false
	}

	// Step 3: We're inside an active escape sequence — classify the rune(s).
	m.escSeqLastRune = now

	runes := msg.Runes
	if len(runes) == 0 {
		// Some KeyMsg types carry no runes (e.g. special keys). These are
		// definitely not CSI bytes. End the sequence and don't consume.
		m.escSeqActive = false
		return false
	}

	for _, r := range runes {
		switch {
		case r == '[' || r == '<':
			// CSI introducer '[' and private-parameter marker '<' — these
			// keep the sequence active (they must NOT be treated as final
			// bytes even though '[' falls within the 0x40-0x7E final range).
		case isCSIIntermediate(r):
			// Valid intermediate byte — stay in sequence.
		case isCSIFinal(r):
			// Terminating byte — sequence is complete.
			m.escSeqActive = false
		default:
			// Unexpected byte. End sequence to avoid getting stuck, but still
			// consume this rune (it's likely garbage from the broken sequence).
			m.escSeqActive = false
		}
	}
	return true
}

// isCSIIntermediate reports whether r is a valid intermediate byte of a CSI
// sequence (parameter bytes 0x30-0x3F and intermediate bytes 0x20-0x2F).
func isCSIIntermediate(r rune) bool {
	return (r >= '0' && r <= '9') || r == ';' || r == '?' || r == ':' ||
		(r >= ' ' && r <= '/') // 0x20-0x2F includes '<' among others
}

// isCSIFinal reports whether r is a valid final byte of a CSI sequence
// (0x40-0x7E).
func isCSIFinal(r rune) bool {
	return r >= 0x40 && r <= 0x7E
}

// resetModal resets the modal state for a new modal.
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
func (m *Model) handleApproval(approved bool) {
	if m.pendingApprovalID == "" {
		return
	}
	am := m.agentLoop.GetApprovalManager()
	if am != nil {
		am.HandleApproval(m.pendingApprovalID, approved)
	}
	if approved {
		m.approvalResult = ApprovalApproved.Render("✅ " + i18n.T("tui.approvalApproved"))
	} else {
		m.approvalResult = ApprovalRejected.Render("❌ " + i18n.T("tui.approvalRejected"))
	}
	m.pendingApprovalID = ""
	m.pendingApprovalCmd = ""
	m.pendingApprovalReason = ""
	m.updateViewport()
}
