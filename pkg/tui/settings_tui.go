package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/keepawake"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

const (
	tuiSettingRowTheme = iota
	tuiSettingRowMouse
	tuiSettingRowMaxMessages
	tuiSettingRowStreamThrottle
	tuiSettingRowKeepAwake
)

func (m *Model) modalTextBudget(indentCells int) int {
	budget := m.width - 8
	if budget < 20 {
		budget = 20
	}
	budget -= indentCells
	if budget < 8 {
		budget = 8
	}
	return budget
}

func (m *Model) loadTUISettings() {
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.theme"), m.currentThemeName),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.mouse"), boolStatus(m.mouseEnabled)),
		fmt.Sprintf("%s: %d", i18n.T("tui.settings.maxMessages"), m.maxRenderedMessages),
		fmt.Sprintf("%s: %dms", i18n.T("tui.settings.streamThrottle"), int(m.streamThrottleInterval.Milliseconds())),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.keepAwake"), m.keepAwakeStatus()),
	}
}

func (m *Model) persistTUISettings() {
	if m.cfg == nil {
		return
	}
	m.cfg.TUI.MouseEnabled = m.mouseEnabled
	m.cfg.TUI.MaxRenderedMessages = m.maxRenderedMessages
	m.cfg.TUI.StreamThrottleMS = int(m.streamThrottleInterval.Milliseconds())
	m.cfg.TUI.KeepAwake = m.keepAwakeEnabled
	if err := m.saveConfigToDisk(); err != nil {
		m.formError = err.Error()
	}
}

func (m *Model) toggleTUIMouse() tea.Cmd {
	m.mouseEnabled = !m.mouseEnabled
	m.persistTUISettings()
	m.loadTUISettings()
	if m.mouseEnabled {
		return tea.EnableMouseCellMotion
	}
	return tea.DisableMouse
}

func (m *Model) handleTUISettingsEnter() tea.Cmd {
	switch m.modalSelectedIdx {
	case tuiSettingRowTheme:
		m.themePickerActive = true
		m.themePreviewName = m.currentThemeName
		m.modalSelectedIdx = 0
		m.loadThemePickerItems()
		if !m.communityLoading && len(m.communityIndex) == 0 && m.communityErr == "" {
			m.communityLoading = true
			m.loadThemePickerItems()
			return m.fetchCommunityIndexCmd()
		}
	case tuiSettingRowMouse:
		return nil
	case tuiSettingRowMaxMessages:
		m.settingsEditField = "maxMessages"
		m.textInput.SetValue(strconv.Itoa(m.maxRenderedMessages))
		m.textInput.Focus()
	case tuiSettingRowStreamThrottle:
		m.settingsEditField = "streamThrottle"
		m.textInput.SetValue(strconv.Itoa(int(m.streamThrottleInterval.Milliseconds())))
		m.textInput.Focus()
	case tuiSettingRowKeepAwake:
		m.toggleKeepAwake()
	}
	return nil
}

type themePickerItem struct {
	kind  string
	name  string
	label string
}

func (m *Model) loadThemePickerItems() {
	items := m.buildThemePickerItems()
	m.themePickerItems = items
	m.modalItems = make([]string, len(items))
	for i, item := range items {
		m.modalItems[i] = item.label
	}
	m.modalSelectedIdx = 0
	m.modalScrollOffset = 0
	for i, item := range items {
		if (item.kind == "builtin" || item.kind == "community") && item.name == m.currentThemeName {
			m.modalSelectedIdx = i
			break
		}
	}
}

func (m *Model) buildThemePickerItems() []themePickerItem {
	var items []themePickerItem

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

func (m *Model) fetchCommunityIndexCmd() tea.Cmd {
	return func() tea.Msg {
		entries, err := theme.FetchCommunityIndex()
		if err != nil {
			return communityIndexMsg{err: err.Error()}
		}
		return communityIndexMsg{entries: entries}
	}
}

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

func (m *Model) installCommunityThemeCmd(name string) tea.Cmd {
	return func() tea.Msg {
		t, err := theme.FetchCommunityTheme(name)
		if err != nil {
			return installThemeMsg{name: name, err: err.Error()}
		}
		return installThemeMsg{name: name, theme: t}
	}
}

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

func (m *Model) keepAwakeStatus() string {
	if !m.keepAwakeEnabled {
		return "✗"
	}
	if m.keepAwake != nil && m.keepAwake.Active() {
		return "✓"
	}
	return "⚠"
}

func (m *Model) keepAwakeRequestedButFailed() bool {
	return m.keepAwakeEnabled && (m.keepAwake == nil || !m.keepAwake.Active())
}

func (m *Model) startKeepAwake() error {
	if m.keepAwake == nil {
		m.keepAwake = keepawake.New()
	}
	err := m.keepAwake.Start()
	if err != nil {
		m.keepAwakeErr = err.Error()
	} else {
		m.keepAwakeErr = ""
	}
	return err
}

func (m *Model) toggleKeepAwake() {
	switch {
	case m.keepAwakeRequestedButFailed():
		_ = m.startKeepAwake()
	case m.keepAwakeEnabled:
		m.keepAwakeEnabled = false
		if m.keepAwake != nil {
			m.keepAwake.Stop()
		}
		m.keepAwakeErr = ""
	default:
		m.keepAwakeEnabled = true
		_ = m.startKeepAwake()
	}
	m.persistTUISettings()
	m.loadTUISettings()
}

func (m *Model) cancelKeepAwake() {
	if !m.keepAwakeRequestedButFailed() {
		return
	}
	m.keepAwakeEnabled = false
	m.keepAwakeErr = ""
	if m.keepAwake != nil {
		m.keepAwake.Stop()
	}
	m.persistTUISettings()
	m.loadTUISettings()
}

func (m *Model) StartKeepAwakeIfEnabled() error {
	if !m.keepAwakeEnabled || m.keepAwake == nil {
		return nil
	}
	return m.startKeepAwake()
}

func (m *Model) Close() {
	if m.keepAwake != nil {
		m.keepAwake.Stop()
	}
}
