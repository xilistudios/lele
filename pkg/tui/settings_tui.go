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

// Row indices in the Interface settings list. Callers must use these
// constants so reordering loadTUISettings cannot silently break Enter.
const (
	tuiSettingRowTheme = iota
	tuiSettingRowMouse
	tuiSettingRowMaxMessages
	tuiSettingRowStreamThrottle
	tuiSettingRowKeepAwake
)

// modalTextBudget returns the display-cell budget for the TEXT of a line
// rendered INSIDE the modal box, given the fixed prefix (indent + glyph) it
// will be prefixed with. The rendered line must never exceed the box's inner
// width, or the right border "│" stops painting (lipgloss.Place stops padding
// once a row reaches m.width).
//
// Deliberately MORE STRICT than renderModal's item loop: that loop budgets
// the full row (prefix included) at max(width-8, 20), while this helper
// budgets only the text at that same number MINUS indentCells, i.e. 2 cells
// less for the "  ⚠ " line. The 2-cell mismatch in the item loop is
// preexisting (it affects every row of every modal, not just this one) and is
// out of scope here — this helper stays the conservative one so a keep-awake
// line can never be the row that breaks the border. The floor keeps a readable
// prefix on very narrow terminals.
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

// loadTUISettings populates the modal items for the TUI/Interface settings
// sub-menu. The five rows are: theme picker, mouse toggle, max rendered
// messages, stream throttle interval (ms) and keep device awake.
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
		fmt.Sprintf("%s: %s", i18n.T("tui.settings.keepAwake"), m.keepAwakeStatus()),
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
	m.cfg.TUI.KeepAwake = m.keepAwakeEnabled
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
	case tuiSettingRowTheme: // Theme — open picker
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
	case tuiSettingRowMouse: // Mouse toggle
		return nil
	case tuiSettingRowMaxMessages: // Max messages — enter edit mode
		m.settingsEditField = "maxMessages"
		m.textInput.SetValue(strconv.Itoa(m.maxRenderedMessages))
		m.textInput.Focus()
	case tuiSettingRowStreamThrottle: // Stream throttle — enter edit mode
		m.settingsEditField = "streamThrottle"
		m.textInput.SetValue(strconv.Itoa(int(m.streamThrottleInterval.Milliseconds())))
		m.textInput.Focus()
	case tuiSettingRowKeepAwake: // Keep device awake — toggle the sleep inhibition
		m.toggleKeepAwake()
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

// keepAwakeStatus returns the row glyph for the keep-awake state:
//
//	✗ not requested (!m.keepAwakeEnabled)
//	✓ requested AND actually held (m.keepAwake != nil && Active())
//	⚠ requested but NOT active — Start failed; see the error line below the list
//
// The intent alone never paints ✓, so a machine with no working mechanism
// cannot pretend to be awake; and a failed request never degrades to ✗, which
// would be indistinguishable from "off" and make ENTER look like a plain
// toggle (it would silently drop the request).
//
// Known, deliberate limitation (inherited from pkg/keepawake.Active): ✓ means
// "Start succeeded and nothing has Stop()ed it yet", NOT "the mechanism is
// alive right now". If the mechanism dies AFTER a successful Start — e.g.
// logind restarting and dropping the inhibitor, or caffeinate being killed by
// an external signal — Active() keeps reporting true, so the row keeps its ✓
// until the next toggle (which runs Stop+Start and would re-probe). The
// package deliberately does not liveness-probe the OS per frame: it would be
// far too expensive for a TUI status row, and the mechanisms are
// self-releasing (the child exits on its own when the session goes away), so
// the exposure is rare by construction. This is documented, not fixed, on
// purpose.
func (m *Model) keepAwakeStatus() string {
	if !m.keepAwakeEnabled {
		return "✗"
	}
	if m.keepAwake != nil && m.keepAwake.Active() {
		return "✓"
	}
	return "⚠"
}

// keepAwakeRequestedButFailed reports the ⚠ state: the user asked for the
// inhibition, the platform did not deliver it (keepAwakeErr carries the
// reason) and the row offers a retry instead of an off-toggle.
func (m *Model) keepAwakeRequestedButFailed() bool {
	return m.keepAwakeEnabled && (m.keepAwake == nil || !m.keepAwake.Active())
}

// startKeepAwake realises the keep-awake intent: it acquires the inhibition
// (creating the Inhibitor when nil), records the reason in keepAwakeErr on
// failure and clears it on success, and returns the Start error so callers can
// log it. It never touches the intent itself — that is the caller's decision.
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

// toggleKeepAwake is the ENTER action of the Interface keep-awake row. The
// row has three states and ENTER does a different thing in each:
//
//	✗ (off):            request the inhibition — keepAwakeEnabled = true and
//	                    attempt to start it (StartKeepAwakeIfEnabled-style:
//	                    a failure keeps the intent, see startKeepAwake)
//	✓ (active):         release it — keepAwakeEnabled = false, Stop() and
//	                    clear the error line
//	⚠ (requested, failed): RETRY the start ONLY — the request on disk is not
//	                    touched, so a transient failure never loses the
//	                    user's intent; leaving the failed state is a
//	                    deliberate act and needs the "d" key (cancelKeepAwake)
//
// Either way the new state is persisted (persistTUISettings) and the row
// rebuilt (loadTUISettings).
func (m *Model) toggleKeepAwake() {
	switch {
	case m.keepAwakeRequestedButFailed():
		// Retry: intent (memory and disk) stays true, only Start is retried.
		_ = m.startKeepAwake()
	case m.keepAwakeEnabled:
		m.keepAwakeEnabled = false
		if m.keepAwake != nil {
			// Stop() can block ~2 s if a child ignores SIGTERM; it runs on the
			// update loop, which is acceptable: the real children
			// (systemd-inhibit/caffeinate) die on SIGTERM immediately and the
			// bound lives in pkg/keepawake. Not worth a tea.Cmd round-trip.
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

// cancelKeepAwake is the "d" action of the ⚠ state: the deliberate way to
// stop requesting the inhibition when Start keeps failing. It clears the
// intent (and thus the error line), releases any half-held inhibition and
// persists — the row then reads ✗ and the error line disappears. Outside the
// ⚠ state it must not be reachable (see the handler's guard): it would
// silently turn a working inhibition off.
func (m *Model) cancelKeepAwake() {
	if !m.keepAwakeRequestedButFailed() {
		return
	}
	m.keepAwakeEnabled = false
	m.keepAwakeErr = ""
	if m.keepAwake != nil {
		// See toggleKeepAwake: blocking Stop() on the update loop is a
		// deliberate, bounded (pkg/keepawake) choice.
		m.keepAwake.Stop()
	}
	m.persistTUISettings()
	m.loadTUISettings()
}

// StartKeepAwakeIfEnabled acquires the inhibition when the config asked for
// it. Called once by the CLI right before running the program. A failure is
// not fatal and never clears the intent: it is recorded in keepAwakeErr so the
// Interface settings row can explain itself.
func (m *Model) StartKeepAwakeIfEnabled() error {
	if !m.keepAwakeEnabled || m.keepAwake == nil {
		return nil
	}
	return m.startKeepAwake()
}

// Close releases state that outlives a frame. Today that is only the keep-awake
// inhibition; it is idempotent and safe when keep-awake was never enabled.
func (m *Model) Close() {
	if m.keepAwake != nil {
		m.keepAwake.Stop()
	}
}
