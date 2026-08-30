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
	t := theme.Get(name, m.customThemes)
	ApplyTheme(t)
	m.applyThemeToInputs()
	m.invalidateRenderCache()
	m.currentThemeName = name

	// Persist to tui.json
	path := theme.DefaultPath()
	if err := theme.Save(path, name, m.customThemes, m.installedCommunity); err != nil {
		log.Printf("warning: could not save tui.json: %v", err)
	}
}

// previewTheme applies a theme live (colors + styles + inputs + cache
// invalidation) WITHOUT persisting to tui.json. Used for live preview while
// navigating the theme picker. Esc reverts via m.themePreviewName.
func (m *Model) previewTheme(name string) {
	t := theme.Get(name, m.customThemes)
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
	// Preserve the current focus state across the style rebind. bubbles
	// caches an internal pointer to the style struct at Focus()/Blur()
	// time, so after replacing the style values we must re-apply the same
	// focus state to rebind the internal pointer to the fresh styles
	// (Blur rebinds to &m.BlurredStyle, which is required for the new
	// theme values to render when blurred).
	chatWasFocused := m.chatInput.Focused()

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

	// Re-bind the textarea's internal style pointer (see note above),
	// restoring the focus state the input had before the rebind.
	if chatWasFocused {
		m.chatInput.Focus()
	} else {
		m.chatInput.Blur()
	}

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
	// Stream/thinking line caches hold rendered lines with ANSI colors from
	// the previous theme; clear them so active streams re-render with the
	// new palette instead of leaving stale colors on screen.
	m.streamRenderedLines = nil
	m.thinkingRenderedLines = nil
	m.streamRenderedJoined = ""
	m.thinkingRenderedJoined = ""
	m.streamRenderCacheWidth = 0
	m.thinkingRenderCacheWidth = 0
}
