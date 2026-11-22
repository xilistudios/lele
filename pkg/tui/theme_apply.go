package tui

import (
	"log"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

func (m *Model) applyThemeByName(name string) {
	t := theme.Get(name, m.customThemes)
	ApplyTheme(t)
	m.themeIsLight = themeIsLightBackground(t.Background)
	m.applyThemeToInputs()
	m.invalidateRenderCache()
	m.currentThemeName = name

	path := theme.DefaultPath()
	if err := theme.Save(path, name, m.customThemes, m.installedCommunity); err != nil {
		log.Printf("warning: could not save tui.json: %v", err)
	}
}

func (m *Model) previewTheme(name string) {
	t := theme.Get(name, m.customThemes)
	ApplyTheme(t)
	m.themeIsLight = themeIsLightBackground(t.Background)
	m.applyThemeToInputs()
	m.invalidateRenderCache()
}

func themeIsLightBackground(bg string) bool {
	bg = strings.TrimSpace(bg)
	if !strings.HasPrefix(bg, "#") || len(bg) != 7 {
		return false
	}
	r, err1 := strconv.ParseInt(bg[1:3], 16, 0)
	g, err2 := strconv.ParseInt(bg[3:5], 16, 0)
	b, err3 := strconv.ParseInt(bg[5:7], 16, 0)
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	lum := (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 255
	return lum > 0.55
}

func (m *Model) applyThemeToInputs() {
	chatWasFocused := m.chatInput.Focused()

	m.chatInput.FocusedStyle.Base = lipgloss.NewStyle()
	m.chatInput.FocusedStyle.Text = lipgloss.NewStyle().Foreground(Foreground)
	m.chatInput.FocusedStyle.CursorLine = lipgloss.NewStyle().Foreground(Foreground)
	m.chatInput.FocusedStyle.CursorLineNumber = lipgloss.NewStyle().Foreground(CommentColor)
	m.chatInput.FocusedStyle.LineNumber = lipgloss.NewStyle().Foreground(CommentColor)
	m.chatInput.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(CommentColor)
	m.chatInput.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(CommentColor)
	m.chatInput.FocusedStyle.EndOfBuffer = lipgloss.NewStyle()
	m.chatInput.BlurredStyle = m.chatInput.FocusedStyle

	if chatWasFocused {
		m.chatInput.Focus()
	} else {
		m.chatInput.Blur()
	}

	m.textInput.PromptStyle = lipgloss.NewStyle().Foreground(CommentColor)
	m.textInput.TextStyle = lipgloss.NewStyle().Foreground(Foreground)
	m.textInput.PlaceholderStyle = lipgloss.NewStyle().Foreground(CommentColor)
	m.textInput.CompletionStyle = lipgloss.NewStyle().Foreground(CommentColor)
}

func (m *Model) invalidateRenderCache() {
	m.msgRenderCacheLines = nil
	m.msgRenderCacheWidth = 0
	m.renderedBaseValid = false
	m.renderedBaseKey = ""
	m.streamRenderedLines = nil
	m.thinkingRenderedLines = nil
	m.streamRenderedJoined = ""
	m.thinkingRenderedJoined = ""
	m.streamRenderCacheWidth = 0
	m.thinkingRenderCacheWidth = 0
	m.cachedRenderer = nil
	m.cachedRendererWidth = 0
	m.cachedRendererStyle = ""
}
