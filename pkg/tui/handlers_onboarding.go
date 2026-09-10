package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

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
		// Installed languages (builtins + any already-downloaded packs).
		// Onboarding stays offline: only show what is already available.
		langs := []string{"en", "es", "pt"}
		for _, c := range i18n.InstalledLanguages() {
			found := false
			for _, existing := range langs {
				if existing == c {
					found = true
					break
				}
			}
			if !found {
				langs = append(langs, c)
			}
		}
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
			_ = m.applyLanguage(lang)
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
