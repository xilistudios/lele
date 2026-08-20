package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// loadTUISettings populates the modal items for the TUI/Interface settings
// sub-menu. The three rows are: mouse toggle, max rendered messages and
// stream throttle interval (ms).
func (m *Model) loadTUISettings() {
	mouseStatus := "✓"
	if !m.mouseEnabled {
		mouseStatus = "✗"
	}
	m.modalItems = []string{
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
func (m *Model) handleTUISettingsEnter() {
	switch m.modalSelectedIdx {
	case 0: // Mouse toggle
		return
	case 1: // Max messages — enter edit mode
		m.settingsEditField = "maxMessages"
		m.textInput.SetValue(strconv.Itoa(m.maxRenderedMessages))
		m.textInput.Focus()
	case 2: // Stream throttle — enter edit mode
		m.settingsEditField = "streamThrottle"
		m.textInput.SetValue(strconv.Itoa(int(m.streamThrottleInterval.Milliseconds())))
		m.textInput.Focus()
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
