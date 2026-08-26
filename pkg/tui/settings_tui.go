package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

// loadTUISettings populates the modal items for the TUI/Interface settings
// sub-menu. The rows are: theme picker, mouse toggle, max rendered messages
// and stream throttle interval (ms).
func (m *Model) loadTUISettings() {
	mouseStatus := "✓"
	if !m.mouseEnabled {
		mouseStatus = "✗"
	}
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.theme"), m.currentThemeName),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.mouse"), mouseStatus),
		fmt.Sprintf("%s: %d", i18n.T("tui.settings.maxMessages"), m.maxRenderedMessages),
		fmt.Sprintf("%s: %dms", i18n.T("tui.settings.streamThrottle"), int(m.streamThrottleInterval.Milliseconds())),
	}
}

// persistTUISettings writes the current TUI values into the config and saves
// to disk. MouseEnabled is written too so that a later launch restores the
// exact state (default true, persisted as false once the user disables it).
func (m *Model) persistTUISettings() {
	if m.cfg == nil {
		return
	}
	m.cfg.TUI.MouseEnabled = m.mouseEnabled
	m.cfg.TUI.MaxRenderedMessages = m.maxRenderedMessages
	m.cfg.TUI.StreamThrottleMS = int(m.streamThrottleInterval.Milliseconds())
	if err := m.saveConfigToDisk(); err != nil {
		m.formError = err.Error()
	}
}

// toggleTUIMouse flips mouse capture on/off, persists the change and returns
// the tea.Cmd to enable/disable mouse in the running program.
func (m *Model) toggleTUIMouse() tea.Cmd {
	m.mouseEnabled = !m.mouseEnabled
	m.persistTUISettings()
	m.loadTUISettings()
	if m.mouseEnabled {
		return tea.EnableMouseCellMotion
	}
	return tea.DisableMouse
}

// handleTUISettingsEnter handles the enter key on a TUI settings item.
// It returns an optional tea.Cmd (e.g. a community index fetch triggered when
// the theme picker opens).
func (m *Model) handleTUISettingsEnter() tea.Cmd {
	switch m.modalSelectedIdx {
	case 0: // Theme — open picker
		m.themePickerActive = true
		m.themePreviewName = m.currentThemeName // save for Esc revert
		m.modalSelectedIdx = 0
		m.loadThemePickerItems()
		// Fetch community themes if not already loaded and not currently loading
		if !m.communityLoading && len(m.communityIndex) == 0 && m.communityErr == "" {
			m.communityLoading = true
			m.loadThemePickerItems()
			return m.fetchCommunityIndexCmd()
		}
	case 1: // Mouse toggle
		return nil
	case 2: // Max messages — enter edit mode
		m.settingsEditField = "maxMessages"
		m.textInput.SetValue(strconv.Itoa(m.maxRenderedMessages))
		m.textInput.Focus()
	case 3: // Stream throttle — enter edit mode
		m.settingsEditField = "streamThrottle"
		m.textInput.SetValue(strconv.Itoa(int(m.streamThrottleInterval.Milliseconds())))
		m.textInput.Focus()
	}
	return nil
}

// themePickerItem represents one row in the theme picker list.
type themePickerItem struct {
	kind  string // "header", "builtin", "community", "loading", "error", "retry"
	name  string // theme name (for builtin and community)
	label string // display label (for headers, loading, error, retry)
}

// loadThemePickerItems populates modalItems with the combined theme picker
// list (built-ins section + community section) built by buildThemePickerItems.
// The currently active theme is prefixed with • to indicate selection.
func (m *Model) loadThemePickerItems() {
	items := m.buildThemePickerItems()
	m.themePickerItems = items
	m.modalItems = make([]string, len(items))
	for i, item := range items {
		m.modalItems[i] = item.label
	}
	// Set selection to the current theme if it's in the list
	m.modalSelectedIdx = 0
	m.modalScrollOffset = 0
	for i, item := range items {
		if (item.kind == "builtin" || item.kind == "community") && item.name == m.currentThemeName {
			m.modalSelectedIdx = i
			break
		}
	}
}

// buildThemePickerItems builds the structured theme picker list with two
// section headers: built-in themes and community themes. The community
// section reflects the current loading/error/index state.
func (m *Model) buildThemePickerItems() []themePickerItem {
	var items []themePickerItem

	// Built-in themes section
	items = append(items, themePickerItem{
		kind:  "header",
		label: "── " + i18n.T("tui.settings.builtinThemes") + " ──",
	})
	for _, name := range theme.Builtins() {
		label := "  " + name
		if name == m.currentThemeName {
			label = "• " + name
		}
		items = append(items, themePickerItem{
			kind:  "builtin",
			name:  name,
			label: label,
		})
	}

	// Community themes section
	items = append(items, themePickerItem{
		kind:  "header",
		label: "── " + i18n.T("tui.settings.communityThemes") + " ──",
	})

	if m.communityLoading {
		items = append(items, themePickerItem{
			kind:  "loading",
			label: "  " + i18n.T("tui.settings.loadingCommunity"),
		})
	} else if m.communityErr != "" {
		items = append(items, themePickerItem{
			kind:  "error",
			label: "  " + i18n.T("tui.settings.communityError") + ": " + m.communityErr,
		})
		items = append(items, themePickerItem{
			kind:  "retry",
			label: "  " + i18n.T("tui.settings.communityFetchRetry"),
		})
	} else if len(m.communityIndex) > 0 {
		for _, entry := range m.communityIndex {
			label := "  " + entry.Name
			if entry.Name == m.currentThemeName {
				label = "• " + entry.Name
			}
			if theme.IsInstalledCommunity(entry.Name, m.installedCommunity) {
				label += " ✓"
			} else {
				label += " — " + i18n.T("tui.settings.themeNotInstalled")
			}
			items = append(items, themePickerItem{
				kind:  "community",
				name:  entry.Name,
				label: label,
			})
		}
	}

	return items
}

// fetchCommunityIndexCmd returns a tea.Cmd that fetches the community
// theme index from the awesome-lele repo.
func (m *Model) fetchCommunityIndexCmd() tea.Cmd {
	return func() tea.Msg {
		entries, err := theme.FetchCommunityIndex()
		if err != nil {
			return communityIndexMsg{err: err.Error()}
		}
		return communityIndexMsg{entries: entries}
	}
}

// installCommunityTheme downloads a community theme, adds it to customThemes
// and installedCommunity, and applies it live.
func (m *Model) installCommunityTheme(name string) {
	t, err := theme.FetchCommunityTheme(name)
	if err != nil {
		m.communityErr = err.Error()
		return
	}
	if m.customThemes == nil {
		m.customThemes = make(map[string]theme.Theme)
	}
	m.customThemes[name] = t
	m.installedCommunity = theme.AddInstalledCommunity(name, m.installedCommunity)
	m.applyThemeByName(name)
	m.loadThemePickerItems()
}

// installCommunityThemeCmd returns a tea.Cmd that downloads a community
// theme and returns an installThemeMsg.
func (m *Model) installCommunityThemeCmd(name string) tea.Cmd {
	return func() tea.Msg {
		t, err := theme.FetchCommunityTheme(name)
		if err != nil {
			return installThemeMsg{name: name, err: err.Error()}
		}
		return installThemeMsg{name: name, theme: t}
	}
}

// handleTUISettingsInput saves an edited numeric TUI setting from the text
// input and returns to the list view.
func (m *Model) handleTUISettingsInput(value string) {
	v, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || v <= 0 {
		m.formError = i18n.T("tui.settings.invalidNumber")
		return
	}
	m.formError = ""
	switch m.settingsEditField {
	case "maxMessages":
		m.maxRenderedMessages = v
	case "streamThrottle":
		m.streamThrottleInterval = time.Duration(v) * time.Millisecond
	}
	m.persistTUISettings()
	m.settingsEditField = ""
	m.loadTUISettings()
}
