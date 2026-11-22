package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

func (m *Model) handleOnboardingKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.onboardingStep {
	case obWelcome:
		if m.obSkipConfirm {
			switch msg.String() {
			case "up", "k":
				m.obSelectedPreset = 0
			case "down", "j":
				m.obSelectedPreset = 1
			case "enter":
				if m.obSelectedPreset == 0 {
					m.onboardingActive = false
					m.obSkipConfirm = false
					m.cfg.TUI.OnboardingCompleted = true
					m.saveConfigToDisk()
				} else {
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
			m.obSelectedPreset = 1
		}

	case obLanguage:
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
			m.themePreviewName = m.currentThemeName
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
			m.previewTheme(names[m.modalSelectedIdx])
		case "down", "j":
			if m.modalSelectedIdx < len(names)-1 {
				m.modalSelectedIdx++
			}
			m.previewTheme(names[m.modalSelectedIdx])
		case "enter":
			name := names[m.modalSelectedIdx]
			m.applyThemeByName(name)
			m.themePreviewName = ""
			m.onboardingStep = obProviderPicker
			m.modalSelectedIdx = 0
		case "esc":
			if m.themePreviewName != "" {
				m.previewTheme(m.themePreviewName)
				m.currentThemeName = m.themePreviewName
				m.themePreviewName = ""
			}
			m.onboardingStep = obLanguage
			m.modalSelectedIdx = 0
		}

	case obProviderPicker:
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
				m.obSelectedPreset = m.modalSelectedIdx
				m.onboardingStep = obConnect
				m.startConnectFlow(&providerPresets[m.modalSelectedIdx])
			} else if m.modalSelectedIdx == len(providerPresets) {
				m.obSelectedPreset = -1
				m.onboardingStep = obConnect
				m.startConnectFlow(nil)
			} else {
				m.onboardingActive = false
				m.cfg.TUI.OnboardingCompleted = true
				m.saveConfigToDisk()
			}
			m.modalSelectedIdx = 0
		case "esc":
			m.onboardingStep = obTheme
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
		if msg.String() == "esc" {
			m.onboardingStep = obDone
			m.obVerifying = false
			m.obFinalizeSetup()
		}

	case obDone:
		if msg.String() == "enter" {
			m.onboardingActive = false
			m.showWelcome = true
			m.obVerifying = false
			m.obVerifyFailed = false
			m.chatInput.Focus()
		}
	}
	return m, nil
}
