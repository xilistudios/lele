package tui

import (
	"log"

	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

// applyThemeByName resolves a theme by name, applies it live (colors + styles
// + input widget styling + render cache invalidation), persists the choice
// to tui.json, and updates m.currentThemeName. It never returns a fatal
// error — worst case the theme falls back to Dracula.
func (m *Model) applyThemeByName(name string) {
	t := theme.Get(name, nil) // custom themes loaded separately; nil here for now
	ApplyTheme(t)
	m.applyThemeToInputs()
	m.invalidateRenderCache()
	m.currentThemeName = name

	// Persist to tui.json
	path := theme.DefaultPath()
	if err := theme.Save(path, name, nil); err != nil {
		log.Printf("warning: could not save tui.json: %v", err)
	}
}

// previewTheme applies a theme live (colors + styles + inputs + cache
// invalidation) WITHOUT persisting to tui.json. Used for live preview while
// navigating the theme picker. Esc reverts via m.themePreviewName.
func (m *Model) previewTheme(name string) {
	t := theme.Get(name, nil)
	ApplyTheme(t)
	m.applyThemeToInputs()
	m.invalidateRenderCache()
}

// applyThemeToInputs re-applies foreground-only theme colors to the bubbles
// widgets (textarea + textinput). Must be called at init and on every theme
// change. All styles stay foreground-only so the enclosing container
// background shows through (bubbles stock defaults emit raw ANSI backgrounds
// that break reapplyBackground).
func (m *Model) applyThemeToInputs() {
	// Textarea styles
	m.chatInput.FocusedStyle.Base = lipgloss.NewStyle()
	m.chatInput.FocusedStyle.Text = lipgloss.NewStyle().Foreground(Foreground)
	m.chatInput.FocusedStyle.CursorLine = lipgloss.NewStyle().Foreground(Foreground)
	m.chatInput.FocusedStyle.CursorLineNumber = lipgloss.NewStyle().Foreground(CommentColor)
	m.chatInput.FocusedStyle.LineNumber = lipgloss.NewStyle().Foreground(CommentColor)
	m.chatInput.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(CommentColor)
	m.chatInput.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(CommentColor)
	m.chatInput.FocusedStyle.EndOfBuffer = lipgloss.NewStyle()
	m.chatInput.BlurredStyle = m.chatInput.FocusedStyle

	// Re-bind the textarea's internal style pointer. bubbles caches the
	// internal pointer to the style struct at Focus() time, and copying the
	// value into the Model struct keeps that stale pointer. Re-focusing makes
	// the internal pointer point at m.chatInput.FocusedStyle again, so the
	// freshly applied styles actually render.
	m.chatInput.Focus()

	// Textinput styles
	m.textInput.PromptStyle = lipgloss.NewStyle().Foreground(CommentColor)
	m.textInput.TextStyle = lipgloss.NewStyle().Foreground(Foreground)
	m.textInput.PlaceholderStyle = lipgloss.NewStyle().Foreground(CommentColor)
	m.textInput.CompletionStyle = lipgloss.NewStyle().Foreground(CommentColor)
}

// invalidateRenderCache clears all cached rendered output so the next View()
// rebuilds everything with the new theme colors.
func (m *Model) invalidateRenderCache() {
	m.msgRenderCacheLines = nil
	m.msgRenderCacheWidth = 0
	m.renderedBaseValid = false
	m.renderedBaseKey = ""
}
