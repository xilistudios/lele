package tui

import (
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) renderOnboarding() string {
	var b strings.Builder
	width := m.width
	if width == 0 {
		width = 80
	}

	switch m.onboardingStep {
	case obWelcome:
		b.WriteString(m.renderObWelcome(width))
	case obLanguage:
		b.WriteString(m.renderObLanguage(width))
	case obTheme:
		b.WriteString(m.renderObTheme(width))
	case obProviderPicker:
		b.WriteString(m.renderObProviderPicker(width))
	case obConnect:
		b.WriteString(m.renderObConnect(width))
	case obVerify:
		b.WriteString(m.renderObVerify(width))
	case obDone:
		b.WriteString(m.renderObDone(width))
	default:
		b.WriteString("Coming soon...")
	}

	return b.String()
}

func (m *Model) renderObConnect(width int) string {
	var inner strings.Builder
	inner.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 5, 6))+"\n"+m.renderProgressDots(5)) + "\n\n")
	inner.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		m.renderFormModalContent(i18n.T("tui.addProvider"), m.formStepNames())))
	return inner.String()
}

func (m *Model) renderObWelcome(width int) string {
	var b strings.Builder

	logo := "  _      ______ _      ______\n" +
		" | |    |  ____| |    |  ____|\n" +
		" | |    | |__  | |    | |__   \n" +
		" | |    |  __| | |    |  __|\n" +
		" | |____| |____| |____| |____\n" +
		" |______|______|______|______|"
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, WelcomeLogo.Render(logo)) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, i18n.T("tui.onboard.welcome")) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 1, 6))+"\n"+m.renderProgressDots(1)) + "\n\n")

	hint := i18n.T("tui.onboard.pressEnter") + " · " + i18n.T("tui.onboard.escSkip")
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, HelpStyle.Render(hint)) + "\n")

	if m.obSkipConfirm {
		skip := m.renderSkipConfirm(width)
		var inner strings.Builder
		inner.WriteString(b.String())
		inner.WriteString("\n\n" + skip)
		return inner.String()
	}

	return b.String()
}

func (m *Model) renderSkipConfirm(width int) string {
	var sb strings.Builder
	sb.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, i18n.T("tui.onboard.skipConfirm")) + "\n\n")

	options := []string{i18n.T("tui.onboard.skipYes"), i18n.T("tui.onboard.skipNo")}
	for i, opt := range options {
		var line string
		if i == m.obSelectedPreset {
			line = ModalItemActive.Render(fmt.Sprintf("> %s", opt))
		} else {
			line = ModalItemInactive.Render(fmt.Sprintf("  %s", opt))
		}
		sb.WriteString(line + "\n")
	}

	return lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, ModalContainer.Render(sb.String()))
}

func (m *Model) renderObLanguage(width int) string {
	var b strings.Builder

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, TitleStyle.Render(i18n.T("tui.onboard.language"))) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 2, 6))+"\n"+m.renderProgressDots(2)) + "\n\n")

	langs := []string{
		"English",
		"Español",
		"Português",
	}
	for _, c := range i18n.InstalledLanguages() {
		if c == "en" || c == "es" || c == "pt" {
			continue
		}
		langs = append(langs, i18n.DisplayName(c)+" ("+c+")")
	}
	var listSb strings.Builder
	for i, lang := range langs {
		if i == m.modalSelectedIdx {
			listSb.WriteString(ModalItemActive.Render(fmt.Sprintf("> %s", lang)) + "\n")
		} else {
			listSb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  %s", lang)) + "\n")
		}
	}
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, ModalContainer.Width(60).Render(listSb.String())) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render(i18n.T("tui.onboard.pressEnter")+" · "+i18n.T("tui.onboard.escSkip"))) + "\n")

	return b.String()
}

func (m *Model) renderObTheme(width int) string {
	var b strings.Builder

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, TitleStyle.Render(i18n.T("tui.onboard.theme"))) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 3, 6))+"\n"+m.renderProgressDots(3)) + "\n\n")

	names := theme.Builtins()
	var listSb strings.Builder
	for i, name := range names {
		if i == m.modalSelectedIdx {
			listSb.WriteString(ModalItemActive.Render(fmt.Sprintf("> %s", name)) + "\n")
		} else {
			listSb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  %s", name)) + "\n")
		}
	}
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, ModalContainer.Width(60).Render(listSb.String())) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render(i18n.T("tui.onboard.themeHint"))) + "\n")

	return b.String()
}

func (m *Model) renderObProviderPicker(width int) string {
	var b strings.Builder

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, TitleStyle.Render(i18n.T("tui.onboard.pickProvider"))) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 4, 6))+"\n"+m.renderProgressDots(4)) + "\n\n")

	var listSb strings.Builder
	total := len(providerPresets) + 2
	for i := 0; i < total; i++ {
		var label, hint string
		switch {
		case i < len(providerPresets):
			label = providerPresets[i].label
			if strings.EqualFold(providerPresets[i].typ, "ollama") {
				hint = i18n.T("tui.onboard.noKeyNeeded")
			} else {
				hint = fmt.Sprintf(i18n.T("tui.onboard.keyFormat"), providerPresets[i].keyHint)
			}
		case i == len(providerPresets):
			label = i18n.T("tui.onboard.otherCustom")
		default:
			label = i18n.T("tui.onboard.skipForNow")
		}

		line := "  " + label
		if i == m.modalSelectedIdx {
			line = "> " + label
		}
		if hint != "" {
			line += "   " + CommentColorStyle.Render(hint)
		}
		if i == m.modalSelectedIdx {
			listSb.WriteString(ModalItemActive.Render(line) + "\n")
		} else {
			listSb.WriteString(ModalItemInactive.Render(line) + "\n")
		}
	}
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, ModalContainer.Width(60).Render(listSb.String())) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		HelpStyle.Render(i18n.T("tui.onboard.pickProviderHint"))) + "\n")

	return b.String()
}

func (m *Model) renderObVerify(width int) string {
	var b strings.Builder

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 6, 6))+"\n"+m.renderProgressDots(6)) + "\n\n")

	switch {
	case m.obVerifying:
		spinner := m.getBouncingDots()
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			spinner+"  "+i18n.T("tui.onboard.verifying")) + "\n\n")
	case m.obVerifyFailed:
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			lipgloss.NewStyle().Foreground(YellowColor).Render("⚠  "+i18n.T("tui.onboard.verifyFailed"))) + "\n\n")
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			HelpStyle.Render(i18n.T("tui.onboard.pressEnter"))) + "\n")
	default:
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			i18n.T("tui.onboard.verifying")) + "\n\n")
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			HelpStyle.Render(i18n.T("tui.onboard.pressEnter"))) + "\n")
	}

	return b.String()
}

func (m *Model) renderObDone(width int) string {
	var b strings.Builder

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		SuccessStyle.Render("✓  "+i18n.T("tui.onboard.done"))) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		CommentColorStyle.Render(fmt.Sprintf(i18n.T("tui.onboard.progress"), 6, 6))+"\n"+m.renderProgressDots(6)) + "\n\n")

	if m.obProviderName != "" {
		var sb strings.Builder
		if m.obProviderName != "" {
			sb.WriteString(CommentColorStyle.Render(i18n.T("tui.onboard.doneProvider")) + ": " + m.obProviderName + "\n")
		}
		if m.obModelName != "" {
			sb.WriteString(CommentColorStyle.Render(i18n.T("tui.onboard.doneModel")) + ": " + m.obModelName + "\n")
		}
		if m.obMaskedKey != "" {
			sb.WriteString(CommentColorStyle.Render(i18n.T("tui.onboard.doneKey")) + ": " + m.obMaskedKey + "\n")
		}
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, ModalContainer.Width(60).Render(sb.String())) + "\n\n")
	}

	if m.obVerifyFailed {
		b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
			lipgloss.NewStyle().Foreground(YellowColor).Render("⚠  "+i18n.T("tui.onboard.verifyFailed"))) + "\n\n")
	}

	var tips strings.Builder
	tips.WriteString(CommentColorStyle.Render(i18n.T("tui.onboard.tips")) + "\n")
	tips.WriteString(HelpStyle.Render(i18n.T("tui.onboard.tipSend")) + "\n")
	tips.WriteString(HelpStyle.Render(i18n.T("tui.onboard.tipModels")) + "\n")
	tips.WriteString(HelpStyle.Render(i18n.T("tui.onboard.tipAgents")) + "\n")
	tips.WriteString(HelpStyle.Render(i18n.T("tui.onboard.tipChats")) + "\n")
	tips.WriteString(HelpStyle.Render(i18n.T("tui.onboard.tipConnect")) + "\n")
	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top, tips.String()) + "\n\n")

	b.WriteString(lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Top,
		SuccessStyle.Render(i18n.T("tui.onboard.pressEnterStart"))) + "\n")

	return b.String()
}

func (m *Model) renderProgressDots(step int) string {
	const total = 6
	dots := make([]rune, total)
	for i := 0; i < total; i++ {
		if i < step {
			dots[i] = '●'
		} else {
			dots[i] = '○'
		}
	}
	return SuccessStyle.Render(string(dots))
}
