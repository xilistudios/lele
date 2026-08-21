package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// System settings groups (indices in ModalSettingsSystem list). Each group maps
// to a sub-view rendered in ModalSettingsSystemEdit and identified by the
// settingsSection value "sys_<idx>".
const (
	sysGroupSession  = 0
	sysGroupTools    = 1
	sysGroupLogs     = 2
	sysGroupLanguage = 3
	sysGroupGoal     = 4
	sysGroupUpdates  = 5
)

// valueOr returns s when non-empty, otherwise fallback.
func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// checkMark returns "✓" when cond is true, otherwise an empty string. Used for
// simple single-select lists (e.g. language). For toggles the full "✓/✗" pair
// is displayed inline via load*Settings.
func checkMark(cond bool) string {
	if cond {
		return "✓"
	}
	return ""
}

// sysSubViewName returns "sys_<idx>" for a system group index.
func sysSubViewName(idx int) string {
	return fmt.Sprintf("sys_%d", idx)
}

// sysGroupFromSection returns the group index for a settingsSection value like
// "sys_0". Returns -1 if the section is not a valid system sub-view.
func sysGroupFromSection(section string) int {
	if !strings.HasPrefix(section, "sys_") {
		return -1
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(section, "sys_"))
	if err != nil {
		return -1
	}
	return idx
}

// loadSystemSettings populates the system settings group list (6 groups).
func (m *Model) loadSystemSettings() {
	m.modalItems = []string{
		i18n.T("tui.settings.session"),
		i18n.T("tui.settings.tools"),
		i18n.T("tui.settings.logs"),
		i18n.T("tui.settings.language"),
		i18n.T("tui.settings.goal"),
		i18n.T("tui.settings.updates"),
	}
}

// loadSessionSettings populates the Session sub-view.
func (m *Model) loadSessionSettings() {
	ephemeralStatus := "✗"
	if m.cfg.Session.Ephemeral {
		ephemeralStatus = "✓"
	}
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.ephemeral"), ephemeralStatus),
		fmt.Sprintf("%s: %ds", i18n.T("tui.settings.ephemeralThreshold"), m.cfg.Session.EphemeralThreshold),
		fmt.Sprintf("%s: %d%%", i18n.T("tui.settings.compactionPercent"), m.cfg.Session.CompactionThresholdPercent),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.compactionModel"), valueOr(m.cfg.Session.CompactionModel, "default")),
	}
}

// loadToolsSettings populates the Tools sub-view.
func (m *Model) loadToolsSettings() {
	denyStatus := "✗"
	if m.cfg.Tools.Exec.EnableDenyPatterns {
		denyStatus = "✓"
	}
	m.modalItems = []string{
		fmt.Sprintf("%s: %ds", i18n.T("tui.settings.execTimeout"), m.cfg.Tools.Exec.TimeoutSeconds),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.denyPatterns"), denyStatus),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.customDenyPatterns"), valueOr(strings.Join(m.cfg.Tools.Exec.CustomDenyPatterns, ", "), "none")),
	}
}

// loadLogsSettings populates the Logs sub-view.
func (m *Model) loadLogsSettings() {
	enabledStatus := "✗"
	if m.cfg.Logs.Enabled {
		enabledStatus = "✓"
	}
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.logsEnabled"), enabledStatus),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.logsPath"), valueOr(m.cfg.Logs.Path, "default")),
		fmt.Sprintf("%s: %d", i18n.T("tui.settings.logsMaxDays"), m.cfg.Logs.MaxDays),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.logsRotation"), valueOr(m.cfg.Logs.Rotation, "daily")),
	}
}

// loadLanguageSettings populates the Language sub-view. Current language is
// marked with ✓.
func (m *Model) loadLanguageSettings() {
	m.modalItems = []string{
		fmt.Sprintf("Español (es) %s", checkMark(m.cfg.Language == "es")),
		fmt.Sprintf("English (en) %s", checkMark(m.cfg.Language == "en")),
		fmt.Sprintf("Português (pt) %s", checkMark(m.cfg.Language == "pt")),
	}
}

// loadGoalSettings populates the Goal sub-view.
func (m *Model) loadGoalSettings() {
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.goalJudgeMode"), valueOr(m.cfg.Goal.Judge.Mode, "inline")),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.goalJudgeAgent"), valueOr(m.cfg.Goal.Judge.Agent, "default")),
	}
}

// loadUpdatesSettings populates the Updates sub-view.
func (m *Model) loadUpdatesSettings() {
	enabledStatus := "✗"
	if m.cfg.Updates.Enabled {
		enabledStatus = "✓"
	}
	m.modalItems = []string{
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.updatesEnabled"), enabledStatus),
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.updatesChannel"), valueOr(m.cfg.Updates.Channel, "stable")),
	}
}

// handleSystemSubEnter handles enter on a system sub-view item and returns a
// tea.Cmd for any immediate actions (e.g. language change). The active sub-view
// is determined by m.settingsSection (e.g. "sys_0" for Session).
func (m *Model) handleSystemSubEnter() tea.Cmd {
	switch m.settingsSection {
	case sysSubViewName(sysGroupSession):
		return m.handleSessionEnter()
	case sysSubViewName(sysGroupTools):
		return m.handleToolsEnter()
	case sysSubViewName(sysGroupLogs):
		return m.handleLogsEnter()
	case sysSubViewName(sysGroupLanguage):
		return m.handleLanguageEnter()
	case sysSubViewName(sysGroupGoal):
		return m.handleGoalEnter()
	case sysSubViewName(sysGroupUpdates):
		return m.handleUpdatesEnter()
	}
	return nil
}

// handleSessionEnter handles enter on Session settings items.
func (m *Model) handleSessionEnter() tea.Cmd {
	switch m.modalSelectedIdx {
	case 0: // Ephemeral toggle
		m.cfg.Session.Ephemeral = !m.cfg.Session.Ephemeral
		m.saveConfigToDisk()
		m.loadSessionSettings()
	case 1: // Ephemeral threshold
		m.settingsEditField = "ephemeralThreshold"
		m.textInput.SetValue(strconv.Itoa(m.cfg.Session.EphemeralThreshold))
		m.textInput.Focus()
	case 2: // Compaction percent
		m.settingsEditField = "compactionPercent"
		m.textInput.SetValue(strconv.Itoa(m.cfg.Session.CompactionThresholdPercent))
		m.textInput.Focus()
	case 3: // Compaction model — selector from configured providers' models
		providerName := m.cfg.Agents.Defaults.Provider
		models := m.listProviderModels(providerName)
		if len(models) == 0 {
			// No models configured — fall back to text input
			m.settingsEditField = "compactionModel"
			m.textInput.SetValue(m.cfg.Session.CompactionModel)
			m.textInput.Focus()
		} else {
			labels := make([]string, 0, len(models)+1)
			values := make([]string, 0, len(models)+1)
			labels = append(labels, "(default)")
			values = append(values, "")
			for _, model := range models {
				labels = append(labels, model)
				values = append(values, model)
			}
			m.startSettingsSelector("compactionModel", m.cfg.Session.CompactionModel, labels, values)
		}
	}
	return nil
}

// handleToolsEnter handles enter on Tools settings items.
func (m *Model) handleToolsEnter() tea.Cmd {
	switch m.modalSelectedIdx {
	case 0: // Exec timeout
		m.settingsEditField = "execTimeout"
		m.textInput.SetValue(strconv.Itoa(m.cfg.Tools.Exec.TimeoutSeconds))
		m.textInput.Focus()
	case 1: // Deny patterns toggle
		m.cfg.Tools.Exec.EnableDenyPatterns = !m.cfg.Tools.Exec.EnableDenyPatterns
		m.saveConfigToDisk()
		m.loadToolsSettings()
	case 2: // Custom deny patterns
		m.settingsEditField = "customDenyPatterns"
		m.textInput.SetValue(strings.Join(m.cfg.Tools.Exec.CustomDenyPatterns, ", "))
		m.textInput.Focus()
	}
	return nil
}

// handleLogsEnter handles enter on Logs settings items.
func (m *Model) handleLogsEnter() tea.Cmd {
	switch m.modalSelectedIdx {
	case 0: // Enabled toggle
		m.cfg.Logs.Enabled = !m.cfg.Logs.Enabled
		m.saveConfigToDisk()
		m.loadLogsSettings()
	case 1: // Path
		m.settingsEditField = "logsPath"
		m.textInput.SetValue(m.cfg.Logs.Path)
		m.textInput.Focus()
	case 2: // Max days
		m.settingsEditField = "logsMaxDays"
		m.textInput.SetValue(strconv.Itoa(m.cfg.Logs.MaxDays))
		m.textInput.Focus()
	case 3: // Rotation — selector: daily / weekly
		labels := []string{"daily", "weekly"}
		values := []string{"daily", "weekly"}
		m.startSettingsSelector("logsRotation", m.cfg.Logs.Rotation, labels, values)
	}
	return nil
}

// handleLanguageEnter handles enter on a language selection. It persists the
// change and switches the i18n locale immediately.
func (m *Model) handleLanguageEnter() tea.Cmd {
	langs := []string{"es", "en", "pt"}
	if m.modalSelectedIdx < len(langs) {
		code := langs[m.modalSelectedIdx]
		m.cfg.Language = code
		m.saveConfigToDisk()
		i18n.SetLanguage(code)
		m.loadLanguageSettings()
	}
	return nil
}

// handleGoalEnter handles enter on Goal settings items.
func (m *Model) handleGoalEnter() tea.Cmd {
	switch m.modalSelectedIdx {
	case 0: // Judge mode — selector: inline / subagent
		labels := []string{"inline", "subagent"}
		values := []string{"inline", "subagent"}
		m.startSettingsSelector("goalJudgeMode", m.cfg.Goal.Judge.Mode, labels, values)
	case 1: // Judge agent — selector from configured agents
		labels := []string{"(default)"}
		values := []string{""}
		for _, a := range m.cfg.Agents.List {
			label := a.ID
			if a.Name != "" {
				label = fmt.Sprintf("%s (%s)", a.Name, a.ID)
			}
			labels = append(labels, label)
			values = append(values, a.ID)
		}
		m.startSettingsSelector("goalJudgeAgent", m.cfg.Goal.Judge.Agent, labels, values)
	}
	return nil
}

// handleUpdatesEnter handles enter on Updates settings items.
func (m *Model) handleUpdatesEnter() tea.Cmd {
	switch m.modalSelectedIdx {
	case 0: // Enabled toggle
		m.cfg.Updates.Enabled = !m.cfg.Updates.Enabled
		m.saveConfigToDisk()
		m.loadUpdatesSettings()
	case 1: // Channel — selector: stable
		labels := []string{"stable"}
		values := []string{"stable"}
		m.startSettingsSelector("updatesChannel", m.cfg.Updates.Channel, labels, values)
	}
	return nil
}

// handleSystemSettingsInput saves an edited system setting from the text input.
// On invalid input it sets m.formError and leaves edit mode active so the user
// can correct the value. Otherwise it persists and reloads the sub-view.
func (m *Model) handleSystemSettingsInput(value string) {
	value = strings.TrimSpace(value)

	switch m.settingsEditField {
	case "ephemeralThreshold":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			m.formError = i18n.T("tui.settings.invalidNumber")
			return
		}
		m.cfg.Session.EphemeralThreshold = v
	case "compactionPercent":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 || v > 100 {
			m.formError = i18n.T("tui.settings.invalidPercent")
			return
		}
		m.cfg.Session.CompactionThresholdPercent = v
	case "compactionModel":
		m.cfg.Session.CompactionModel = value
	case "execTimeout":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			m.formError = i18n.T("tui.settings.invalidNumber")
			return
		}
		m.cfg.Tools.Exec.TimeoutSeconds = v
	case "customDenyPatterns":
		if value == "" {
			m.cfg.Tools.Exec.CustomDenyPatterns = nil
		} else {
			patterns := strings.Split(value, ",")
			for i := range patterns {
				patterns[i] = strings.TrimSpace(patterns[i])
			}
			m.cfg.Tools.Exec.CustomDenyPatterns = patterns
		}
	case "logsPath":
		m.cfg.Logs.Path = value
	case "logsMaxDays":
		v, err := strconv.Atoi(value)
		if err != nil || v < 0 {
			m.formError = i18n.T("tui.settings.invalidNumber")
			return
		}
		m.cfg.Logs.MaxDays = v
	case "logsRotation":
		m.cfg.Logs.Rotation = value
	case "goalJudgeMode":
		m.cfg.Goal.Judge.Mode = value
	case "goalJudgeAgent":
		m.cfg.Goal.Judge.Agent = value
	case "updatesChannel":
		m.cfg.Updates.Channel = value
	}

	m.formError = ""
	m.saveConfigToDisk()
	m.settingsEditField = ""
	m.reloadSystemSubView()
}

// reloadSystemSubView reloads the current system sub-view after a change.
func (m *Model) reloadSystemSubView() {
	switch m.settingsSection {
	case sysSubViewName(sysGroupSession):
		m.loadSessionSettings()
	case sysSubViewName(sysGroupTools):
		m.loadToolsSettings()
	case sysSubViewName(sysGroupLogs):
		m.loadLogsSettings()
	case sysSubViewName(sysGroupLanguage):
		m.loadLanguageSettings()
	case sysSubViewName(sysGroupGoal):
		m.loadGoalSettings()
	case sysSubViewName(sysGroupUpdates):
		m.loadUpdatesSettings()
	}
}
