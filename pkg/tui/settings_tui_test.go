package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/keepawake"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

// newSettingsTestModel builds a Model owned by a temp config dir so that
// persistTUISettings can write through saveConfigToDisk.
func newSettingsTestModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				Model:     "test-model",
			},
		},
		Providers: &config.ProvidersConfig{},
		TUI: config.TUIConfig{
			MouseEnabled:        true,
			MaxRenderedMessages: 200,
			StreamThrottleMS:    32,
		},
	}
	if err := config.SaveConfig(config.DefaultConfigPath(), cfg); err != nil {
		t.Fatalf("saving initial config: %v", err)
	}
	ti := textinput.New()
	ti.Focus()
	return &Model{
		mouseEnabled:           true,
		maxRenderedMessages:    200,
		streamThrottleInterval: 32 * time.Millisecond,
		cfg:                    cfg,
		modalItems:             nil,
		modalSelectedIdx:       0,
		settingsEditField:      "",
		textInput:              ti,
	}
}

func TestLoadTUISettingsItems(t *testing.T) {
	m := newSettingsTestModel(t)
	m.mouseEnabled = true
	m.maxRenderedMessages = 200
	m.streamThrottleInterval = 32 * time.Millisecond
	m.currentThemeName = "dracula"
	m.loadTUISettings()

	// 5 items: theme, mouse, max messages, stream throttle, keep awake.
	if len(m.modalItems) != 5 {
		t.Fatalf("expected 5 items, got %d", len(m.modalItems))
	}
	if !strings.Contains(m.modalItems[0], "dracula") {
		t.Errorf("theme item should show active theme: %q", m.modalItems[0])
	}
	if !strings.Contains(m.modalItems[1], "✓") {
		t.Errorf("mouse item should show enabled checkmark: %q", m.modalItems[1])
	}
	if !strings.Contains(m.modalItems[2], "200") {
		t.Errorf("max messages item should show 200: %q", m.modalItems[2])
	}
	if !strings.Contains(m.modalItems[3], "32") {
		t.Errorf("stream throttle item should show 32: %q", m.modalItems[3])
	}
	// Keep awake: never requested → ✗ (the row is off, not merely inactive).
	if !strings.Contains(m.modalItems[tuiSettingRowKeepAwake], "✗") {
		t.Errorf("keep-awake item should show ✗ while the request is off: %q", m.modalItems[tuiSettingRowKeepAwake])
	}

	// Disabled mouse should show ✗
	m.mouseEnabled = false
	m.loadTUISettings()
	if !strings.Contains(m.modalItems[1], "✗") {
		t.Errorf("mouse item should show disabled mark: %q", m.modalItems[1])
	}

	// Requested AND started inhibitor → ✓ (the row reflects the EFFECTIVE
	// state, not the intent).
	m.keepAwakeEnabled = true
	m.keepAwake = keepawake.NewWithStarter(func() (func(), error) { return func() {}, nil })
	if err := m.keepAwake.Start(); err != nil {
		t.Fatalf("starting test inhibitor: %v", err)
	}
	m.loadTUISettings()
	if !strings.Contains(m.modalItems[tuiSettingRowKeepAwake], "✓") {
		t.Errorf("keep-awake item should show ✓ while the inhibition is active: %q", m.modalItems[tuiSettingRowKeepAwake])
	}
}

// TestTUISettingRowIndicesAreStable pins the iota block of settings_tui.go to
// the numbers handleTUISettingsEnter switches on, so a future row inserted in
// the middle cannot silently repoint Enter onto the wrong action.
func TestTUISettingRowIndicesAreStable(t *testing.T) {
	want := []struct {
		name string
		got  int
	}{
		{"tuiSettingRowTheme", tuiSettingRowTheme},
		{"tuiSettingRowMouse", tuiSettingRowMouse},
		{"tuiSettingRowMaxMessages", tuiSettingRowMaxMessages},
		{"tuiSettingRowStreamThrottle", tuiSettingRowStreamThrottle},
		{"tuiSettingRowKeepAwake", tuiSettingRowKeepAwake},
	}
	for i, c := range want {
		if c.got != i {
			t.Errorf("%s = %d, want %d (row order is the Enter contract)", c.name, c.got, i)
		}
	}
}

func TestHandleTUISettingsEnterEditMode(t *testing.T) {
	m := newSettingsTestModel(t)
	m.modalItems = []string{"theme", "mouse", "max", "throttle"}

	// Theme picker activation at index 0.
	m.modalSelectedIdx = 0
	m.handleTUISettingsEnter()
	if !m.themePickerActive {
		t.Fatal("expected theme picker to activate on theme row")
	}

	// Max messages → edit mode
	m.modalSelectedIdx = 2
	m.handleTUISettingsEnter()
	if m.settingsEditField != "maxMessages" {
		t.Fatalf("expected edit field maxMessages, got %q", m.settingsEditField)
	}
	if got := m.textInput.Value(); got != "200" {
		t.Fatalf("expected text input value 200, got %q", got)
	}

	// Stream throttle → edit mode
	m.modalSelectedIdx = 3
	m.handleTUISettingsEnter()
	if m.settingsEditField != "streamThrottle" {
		t.Fatalf("expected edit field streamThrottle, got %q", m.settingsEditField)
	}
	if got := m.textInput.Value(); got != "32" {
		t.Fatalf("expected text input value 32, got %q", got)
	}
}

func TestHandleTUISettingsInputSavesValue(t *testing.T) {
	m := newSettingsTestModel(t)

	// maxMessages
	m.settingsEditField = "maxMessages"
	m.handleTUISettingsInput("500")
	if m.maxRenderedMessages != 500 {
		t.Fatalf("expected maxRenderedMessages 500, got %d", m.maxRenderedMessages)
	}
	if m.settingsEditField != "" {
		t.Fatalf("expected edit field cleared after save, got %q", m.settingsEditField)
	}
	if m.cfg.TUI.MaxRenderedMessages != 500 {
		t.Fatalf("expected cfg max rendered 500, got %d", m.cfg.TUI.MaxRenderedMessages)
	}
	// Reload config from disk to confirm persistence.
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.TUI.MaxRenderedMessages != 500 {
		t.Fatalf("expected persisted max rendered 500, got %d", reloaded.TUI.MaxRenderedMessages)
	}

	// streamThrottle
	m.settingsEditField = "streamThrottle"
	m.handleTUISettingsInput("5")
	if m.streamThrottleInterval != 5*time.Millisecond {
		t.Fatalf("expected throttle 5ms, got %v", m.streamThrottleInterval)
	}
	if m.cfg.TUI.StreamThrottleMS != 5 {
		t.Fatalf("expected cfg throttle 5, got %d", m.cfg.TUI.StreamThrottleMS)
	}
}

func TestHandleTUISettingsInputInvalid(t *testing.T) {
	m := newSettingsTestModel(t)
	m.settingsEditField = "maxMessages"
	m.handleTUISettingsInput("-5")
	if m.maxRenderedMessages != 200 {
		t.Fatalf("invalid input must not change value, got %d", m.maxRenderedMessages)
	}
	if m.settingsEditField == "" {
		t.Fatal("edit field should stay set on invalid input")
	}
	if m.formError == "" {
		t.Fatal("expected form error on invalid input")
	}
}

func TestToggleTUIMouse(t *testing.T) {
	m := newSettingsTestModel(t)
	m.mouseEnabled = true

	// Toggle off — the returned cmd must be non-nil and callable (Bubble Tea
	// mouse commands are unexported message types; we assert functionality).
	cmd := m.toggleTUIMouse()
	if cmd == nil {
		t.Fatal("expected a tea.Cmd when disabling mouse")
	}
	if m.mouseEnabled {
		t.Fatal("expected mouse disabled after toggle")
	}
	if m.cfg.TUI.MouseEnabled != false {
		t.Fatalf("expected cfg mouse false, got %v", m.cfg.TUI.MouseEnabled)
	}
	msg := cmd() // must return without panicking (a mouse control message)
	if msg == nil {
		t.Fatal("expected a non-nil msg from disable cmd")
	}

	// Toggle back on
	cmd = m.toggleTUIMouse()
	if cmd == nil {
		t.Fatal("expected a tea.Cmd when enabling mouse")
	}
	if !m.mouseEnabled {
		t.Fatal("expected mouse enabled after second toggle")
	}

	// Confirm persistence.
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if !reloaded.TUI.MouseEnabled {
		t.Fatal("expected persisted mouse enabled true")
	}
}

func TestLoadThemePickerItems(t *testing.T) {
	m := newSettingsTestModel(t)
	m.currentThemeName = "nord"
	m.loadThemePickerItems()
	if len(m.modalItems) == 0 {
		t.Fatal("expected at least one theme item")
	}
	foundActive := false
	for _, item := range m.modalItems {
		if strings.HasPrefix(item, "• ") {
			if !strings.Contains(item, "nord") {
				t.Errorf("active marker should be on current theme, got %q", item)
			}
			foundActive = true
		}
	}
	if !foundActive {
		t.Error("expected active theme to be marked with •")
	}
	// Items are either headers (── ... ──) or selectable rows (2-space or •).
	hasHeader := false
	for _, item := range m.modalItems {
		switch {
		case strings.HasPrefix(item, "── "):
			hasHeader = true
		case strings.HasPrefix(item, "  "), strings.HasPrefix(item, "• "):
			// ok
		default:
			t.Errorf("theme item should be a header or start with 2 spaces/•: %q", item)
		}
	}
	if !hasHeader {
		t.Error("expected a section header in the theme picker list")
	}
	// Structured items should mirror the modal labels.
	if len(m.themePickerItems) != len(m.modalItems) {
		t.Fatalf("themePickerItems len %d != modalItems len %d", len(m.themePickerItems), len(m.modalItems))
	}
	for i, it := range m.themePickerItems {
		if it.label != m.modalItems[i] {
			t.Errorf("label mismatch at %d: %q != %q", i, it.label, m.modalItems[i])
		}
	}
}

// TestThemePickerStructuredSections verifies buildThemePickerItems emits the
// built-in and community section headers plus per-kind rows.
func TestThemePickerStructuredSections(t *testing.T) {
	m := newSettingsTestModel(t)
	m.currentThemeName = "dracula"
	m.installedCommunity = []string{"dracula", "solarized"}
	m.communityIndex = []theme.CommunityThemeEntry{
		{Name: "solarized", Description: "S"},
	}
	m.loadThemePickerItems()

	var kinds []string
	for _, it := range m.themePickerItems {
		kinds = append(kinds, it.kind)
	}
	// Header, builtins..., header, community entries
	if kinds[0] != "header" {
		t.Fatalf("expected first item to be a header, got %q", kinds[0])
	}
	if !containsKind(kinds, "community") {
		t.Fatal("expected community section items")
	}
	// The installed marker should appear on the installed community theme.
	foundInstalled := false
	for _, it := range m.themePickerItems {
		if it.kind == "community" && strings.Contains(it.label, "✓") {
			foundInstalled = true
		}
	}
	if !foundInstalled {
		t.Error("expected an installed community theme to show ✓")
	}
}

// TestThemePickerLoadingError verifies the loading and error branches of
// buildThemePickerItems.
func TestThemePickerLoadingError(t *testing.T) {
	m := newSettingsTestModel(t)

	// Loading state
	m.communityLoading = true
	m.loadThemePickerItems()
	foundLoading := false
	for _, it := range m.themePickerItems {
		if it.kind == "loading" {
			foundLoading = true
		}
	}
	if !foundLoading {
		t.Fatal("expected a loading item while communityLoading is true")
	}

	// Error state
	m = newSettingsTestModel(t)
	m.communityErr = "boom"
	m.loadThemePickerItems()
	var kinds []string
	for _, it := range m.themePickerItems {
		kinds = append(kinds, it.kind)
	}
	if !containsKind(kinds, "error") || !containsKind(kinds, "retry") {
		t.Fatalf("expected error and retry kinds, got %v", kinds)
	}
}

func containsKind(kinds []string, kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// TestThemePickerEscRevertsPreview verifies that after previewing a different
// theme and pressing Esc, the original theme is restored.
func TestThemePickerEscRevertsPreview(t *testing.T) {
	m := newTestModel(t)
	m.currentThemeName = "dracula"
	m.themePickerActive = true
	m.themePreviewName = "dracula"
	m.loadThemePickerItems()
	// Set up the picker so modalSelectedIdx matches the current theme
	for i, item := range m.modalItems {
		if strings.Contains(item, "dracula") && strings.HasPrefix(item, "•") {
			m.modalSelectedIdx = i
			break
		}
	}
	// Navigate to a different theme (preview)
	if m.modalSelectedIdx+1 < len(m.modalItems) {
		m.modalSelectedIdx++
	} else {
		m.modalSelectedIdx--
	}
	item := m.modalItems[m.modalSelectedIdx]
	themeName := strings.TrimSpace(strings.TrimPrefix(item, "•"))
	themeName = strings.TrimSpace(themeName)
	m.previewTheme(themeName)
	if m.currentThemeName != "dracula" {
		t.Fatalf("preview should not update currentThemeName, got %q", m.currentThemeName)
	}
	// Esc should revert — simulate the Esc handler logic
	if m.themePreviewName != "" {
		m.previewTheme(m.themePreviewName)
		m.currentThemeName = m.themePreviewName
		m.themePreviewName = ""
	}
	m.themePickerActive = false
	m.loadTUISettings()
	if m.currentThemeName != "dracula" {
		t.Fatalf("expected theme reverted to dracula, got %q", m.currentThemeName)
	}
}

func TestNewModelAppliesTUIConfig(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				Model:     "test-model",
			},
		},
		Providers: &config.ProvidersConfig{},
		TUI: config.TUIConfig{
			MouseEnabled:        false,
			MaxRenderedMessages: 300,
			StreamThrottleMS:    10,
		},
	}
	m := NewModel(cfg, nil, nil)
	if m.maxRenderedMessages != 300 {
		t.Fatalf("expected maxRenderedMessages 300, got %d", m.maxRenderedMessages)
	}
	if m.mouseEnabled != false {
		t.Fatalf("expected mouse disabled from config, got %v", m.mouseEnabled)
	}
	if m.streamThrottleInterval != 10*time.Millisecond {
		t.Fatalf("expected throttle 10ms, got %v", m.streamThrottleInterval)
	}
}

func TestNewModelDefaultsWhenNoTUIConfig(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				Model:     "test-model",
			},
		},
		Providers: &config.ProvidersConfig{},
	}
	m := NewModel(cfg, nil, nil)
	if m.maxRenderedMessages != 200 {
		t.Fatalf("expected default 200, got %d", m.maxRenderedMessages)
	}
	if m.mouseEnabled != true {
		t.Fatalf("expected default mouse on, got %v", m.mouseEnabled)
	}
	if m.streamThrottleInterval != 32*time.Millisecond {
		t.Fatalf("expected default throttle 32ms, got %v", m.streamThrottleInterval)
	}
}

// countingStarter returns a keepawake starter that records how many times the
// inhibition was acquired and released. releaseCount is incremented from the
// returned stop func, so a Stop that runs twice shows up immediately.
// flakyFailCalls > 0 makes the first flakyFailCalls attempts fail (the rest
// succeed), which is how a one-shot transient failure is simulated: the test
// then asserts startsRun to prove ENTER really retried.
type countingStarter struct {
	acquires       int
	releases       int
	startErr       error
	startsRun      int
	flakyFailCalls int
}

func (c *countingStarter) starter() (func(), error) {
	c.startsRun++
	if c.startErr != nil {
		return nil, c.startErr
	}
	if c.startsRun <= c.flakyFailCalls {
		return nil, errors.New("mechanism down (transient)")
	}
	c.acquires++
	return func() { c.releases++ }, nil
}

// TestToggleKeepAwakeStartsAndStopsInhibitor covers the happy path of the
// Interface row: the first Enter realises the intent (Start) and persists it,
// the second one releases the inhibition and clears the intent.
func TestToggleKeepAwakeStartsAndStopsInhibitor(t *testing.T) {
	m := newSettingsTestModel(t)
	counter := &countingStarter{}
	m.keepAwake = keepawake.NewWithStarter(counter.starter)

	m.toggleKeepAwake()

	if !m.cfg.TUI.KeepAwake {
		t.Error("expected cfg.TUI.KeepAwake to be persisted as true")
	}
	if !m.keepAwake.Active() {
		t.Error("expected the inhibitor to be active after enabling")
	}
	if !strings.Contains(m.modalItems[tuiSettingRowKeepAwake], "✓") {
		t.Errorf("keep-awake row should read ✓, got %q", m.modalItems[tuiSettingRowKeepAwake])
	}
	if m.keepAwakeStatus() != "✓" {
		t.Errorf("keepAwakeStatus() = %q, want ✓", m.keepAwakeStatus())
	}
	if counter.acquires != 1 {
		t.Errorf("acquires = %d, want 1", counter.acquires)
	}

	// The intent must survive a process restart: read it back from disk.
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if !reloaded.TUI.KeepAwake {
		t.Error("reloaded config lost the keep_awake intent")
	}

	// Second ENTER in the ✓ state: release the inhibition and the intent.
	m.toggleKeepAwake()

	if m.keepAwake.Active() {
		t.Error("expected the inhibitor to be released after disabling")
	}
	if counter.releases != 1 {
		t.Errorf("releases = %d, want 1", counter.releases)
	}
	if m.cfg.TUI.KeepAwake {
		t.Error("expected cfg.TUI.KeepAwake to be cleared")
	}
}

// TestToggleKeepAwakeFailureSurfacesPlatformError: when the platform cannot
// start the inhibition the row must explain why AND read ⚠ (not ✗ — ✗ means
// "off", and conflating the two is what used to make ENTER silently drop the
// request), keep the intent persisted (the mechanism may exist on the next
// launch) and ENTER must then RETRY instead of turning the intent off.
func TestToggleKeepAwakeFailureSurfacesPlatformError(t *testing.T) {
	m := newSettingsTestModel(t)
	counter := &countingStarter{startErr: errors.New("systemd-inhibit not available")}
	m.keepAwake = keepawake.NewWithStarter(counter.starter)

	m.toggleKeepAwake()

	if !strings.Contains(m.keepAwakeErr, "systemd-inhibit not available") {
		t.Errorf("keepAwakeErr = %q, want the platform reason", m.keepAwakeErr)
	}
	if m.keepAwake.Active() {
		t.Error("inhibitor must not report active after a failed Start")
	}
	// ⚠ = requested but NOT active. ✗ would be indistinguishable from "off".
	if !strings.Contains(m.modalItems[tuiSettingRowKeepAwake], "⚠") {
		t.Errorf("keep-awake row should read ⚠ after a failed Start, got %q", m.modalItems[tuiSettingRowKeepAwake])
	}
	if m.keepAwakeStatus() != "⚠" {
		t.Errorf("keepAwakeStatus() = %q, want ⚠", m.keepAwakeStatus())
	}
	if !m.cfg.TUI.KeepAwake {
		t.Error("the intent must be kept even when Start fails")
	}
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if !reloaded.TUI.KeepAwake {
		t.Error("the persisted intent must survive a failed Start")
	}

	// Second ENTER in the ⚠ state: RETRY (calls Start again), the intent is
	// NOT cleared and the error is refreshed by the new attempt.
	m.toggleKeepAwake()

	if counter.startsRun != 2 {
		t.Errorf("starter ran %d times, want 2 (ENTER in ⚠ must retry the start)", counter.startsRun)
	}
	if !m.cfg.TUI.KeepAwake {
		t.Error("ENTER in ⚠ must not clear the intent (retry, not toggle-off)")
	}
	if m.keepAwakeErr == "" {
		t.Error("the failed retry must keep recording the platform reason")
	}
	if m.keepAwakeStatus() != "⚠" {
		t.Errorf("keepAwakeStatus() = %q after a failed retry, want ⚠", m.keepAwakeStatus())
	}
	reloaded, err = config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if !reloaded.TUI.KeepAwake {
		t.Error("the persisted intent must survive a failed retry")
	}
}

// TestCloseReleasesKeepAwake: Close is the CLI's defer and must be idempotent
// — a double Close (or a Close after the row already released) releases once.
func TestCloseReleasesKeepAwake(t *testing.T) {
	m := newSettingsTestModel(t)
	counter := &countingStarter{}
	m.keepAwake = keepawake.NewWithStarter(counter.starter)

	m.toggleKeepAwake()
	if !m.keepAwake.Active() {
		t.Fatal("expected the inhibitor to be active before Close")
	}

	m.Close()
	m.Close()

	if m.keepAwake.Active() {
		t.Error("Close must release the inhibition")
	}
	if counter.releases != 1 {
		t.Errorf("releases = %d, want 1 (Close must be idempotent)", counter.releases)
	}
}

// TestStartKeepAwakeIfEnabled covers the CLI entry point: it acquires when the
// config asked for it, stays inert otherwise, and records (without clearing)
// the reason when the platform refuses.
func TestStartKeepAwakeIfEnabled(t *testing.T) {
	t.Run("enabled starts once", func(t *testing.T) {
		m := newSettingsTestModel(t)
		counter := &countingStarter{}
		m.keepAwake = keepawake.NewWithStarter(counter.starter)
		m.cfg.TUI.KeepAwake = true
		m.keepAwakeEnabled = true

		if err := m.StartKeepAwakeIfEnabled(); err != nil {
			t.Fatalf("StartKeepAwakeIfEnabled: %v", err)
		}
		if counter.acquires != 1 {
			t.Errorf("acquires = %d, want 1", counter.acquires)
		}
		if m.keepAwakeErr != "" {
			t.Errorf("keepAwakeErr = %q, want empty", m.keepAwakeErr)
		}
	})

	t.Run("disabled is inert", func(t *testing.T) {
		m := newSettingsTestModel(t)
		counter := &countingStarter{}
		m.keepAwake = keepawake.NewWithStarter(counter.starter)
		m.keepAwakeEnabled = false

		if err := m.StartKeepAwakeIfEnabled(); err != nil {
			t.Fatalf("StartKeepAwakeIfEnabled: %v", err)
		}
		if counter.startsRun != 0 {
			t.Errorf("starter ran %d times, want 0", counter.startsRun)
		}
	})

	t.Run("failure is recorded and keeps the intent", func(t *testing.T) {
		m := newSettingsTestModel(t)
		counter := &countingStarter{startErr: errors.New("platform refused")}
		m.keepAwake = keepawake.NewWithStarter(counter.starter)
		m.keepAwakeEnabled = true

		if err := m.StartKeepAwakeIfEnabled(); err == nil {
			t.Fatal("expected an error when the platform refuses to start")
		}
		if !strings.Contains(m.keepAwakeErr, "platform refused") {
			t.Errorf("keepAwakeErr = %q, want the platform reason", m.keepAwakeErr)
		}
		if !m.keepAwakeEnabled {
			t.Error("a failed Start must not clear the intent")
		}
	})
}

// TestKeepAwakeErrorIsRenderedInInterfaceList: the failure reason must be
// visible where the user will look for it — under the Interface settings list —
// and must NOT leak into unrelated modals.
func TestKeepAwakeErrorIsRenderedInInterfaceList(t *testing.T) {
	m := newSettingsTestModel(t)
	m.keepAwakeErr = "boom"
	m.keepAwakeEnabled = true // a reason only exists for a REQUESTED inhibition
	m.modalMode = ModalSettingsTUI
	m.loadTUISettings()

	out := ansi.Strip(m.renderModal(m.modalTitleFor(ModalSettingsTUI)))
	if !strings.Contains(out, "boom") {
		t.Errorf("Interface settings list must render keepAwakeErr:\n%s", out)
	}

	m.modalMode = ModalSettings
	out = ansi.Strip(m.renderModal(m.modalTitleFor(ModalSettings)))
	if strings.Contains(out, "boom") {
		t.Errorf("keepAwakeErr must not leak into other modals:\n%s", out)
	}
}

// ── Keep-awake three-state row (✗ off / ✓ active / ⚠ requested-but-failed) ──

// TestKeepAwakeRowGlyphs pins the three row glyphs: ✗ off, ✓ requested+held,
// ⚠ requested-but-failed. The starter that fails keeps `keep_awake: true` in
// memory AND on disk (the request survives the failure).
func TestKeepAwakeRowGlyphs(t *testing.T) {
	m := newSettingsTestModel(t)

	// ✗ — never requested.
	m.loadTUISettings()
	if got := m.keepAwakeStatus(); got != "✗" {
		t.Errorf("off state: keepAwakeStatus() = %q, want ✗", got)
	}
	if !strings.Contains(m.modalItems[tuiSettingRowKeepAwake], "✗") {
		t.Errorf("off state row = %q, want ✗", m.modalItems[tuiSettingRowKeepAwake])
	}
	if strings.Contains(m.modalItems[tuiSettingRowKeepAwake], "⚠") {
		t.Errorf("off state row must not show ⚠: %q", m.modalItems[tuiSettingRowKeepAwake])
	}

	// ✓ — requested AND active.
	counter := &countingStarter{}
	m.keepAwakeEnabled = true
	m.keepAwake = keepawake.NewWithStarter(counter.starter)
	if err := m.startKeepAwake(); err != nil {
		t.Fatalf("startKeepAwake: %v", err)
	}
	m.loadTUISettings()
	if got := m.keepAwakeStatus(); got != "✓" {
		t.Errorf("active state: keepAwakeStatus() = %q, want ✓", got)
	}
	if !strings.Contains(m.modalItems[tuiSettingRowKeepAwake], "✓") {
		t.Errorf("active state row = %q, want ✓", m.modalItems[tuiSettingRowKeepAwake])
	}
	m.Close() // release before the failing starter takes over

	// ⚠ — requested but the mechanism refused; intent stays true everywhere.
	// Persisted first (as the production paths do: toggle/persist), then the
	// start fails, so the request survives in memory AND on disk.
	failing := &countingStarter{startErr: errors.New("systemd-inhibit not available")}
	m.keepAwakeEnabled = true
	m.keepAwake = keepawake.NewWithStarter(failing.starter)
	m.persistTUISettings() // request as if pressed once
	if err := m.startKeepAwake(); err == nil {
		t.Fatal("expected the failing starter to return an error")
	}
	m.loadTUISettings()
	if got := m.keepAwakeStatus(); got != "⚠" {
		t.Errorf("failed state: keepAwakeStatus() = %q, want ⚠", got)
	}
	row := m.modalItems[tuiSettingRowKeepAwake]
	if !strings.Contains(row, "⚠") {
		t.Errorf("failed state row = %q, want ⚠", row)
	}
	if strings.Contains(row, "✗") {
		t.Errorf("failed state row must NOT read ✗ (it is not 'off'): %q", row)
	}
	if !m.keepAwakeEnabled || !m.cfg.TUI.KeepAwake {
		t.Errorf("intent lost on failure: memory=%v cfg=%v", m.keepAwakeEnabled, m.cfg.TUI.KeepAwake)
	}
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if !reloaded.TUI.KeepAwake {
		t.Error("keep_awake must still be true on disk after a failed start")
	}
}

// TestEnterOnFailedKeepAwakeRetries: ENTER in the ⚠ state must RETRY the
// start — with a starter that fails first and works second, the second ENTER
// leaves the row ✓, keeps keep_awake true (never writes false) and really
// called the starter again (the counter, not a spy).
func TestEnterOnFailedKeepAwakeRetries(t *testing.T) {
	m := newSettingsTestModel(t)
	// One counter for every attempt: call 1 fails ("mechanism down"), every
	// later call acquires — so startsRun distinguishes retry from no-retry.
	counter := &countingStarter{flakyFailCalls: 1}
	m.keepAwake = keepawake.NewWithStarter(counter.starter)

	// ENTER #1 (off → requested): attempt fails, row ⚠, intent kept.
	m.toggleKeepAwake()
	if m.keepAwakeStatus() != "⚠" {
		t.Fatalf("after 1st ENTER: status = %q, want ⚠", m.keepAwakeStatus())
	}
	if !m.cfg.TUI.KeepAwake {
		t.Fatal("after 1st ENTER: keep_awake must be true")
	}
	if m.keepAwakeErr == "" {
		t.Fatal("after 1st ENTER: expected a recorded error")
	}
	if counter.startsRun != 1 {
		t.Fatalf("starter ran %d times after 1st ENTER, want 1", counter.startsRun)
	}

	// ENTER #2 (⚠ → retry): the mechanism is back; the row must go ✓ and the
	// intent must NOT be cleared.
	m.toggleKeepAwake()
	if counter.startsRun != 2 {
		t.Errorf("starter ran %d times after 2nd ENTER, want 2 (ENTER in ⚠ must retry)", counter.startsRun)
	}
	if m.keepAwakeStatus() != "✓" {
		t.Errorf("after 2nd ENTER: status = %q, want ✓", m.keepAwakeStatus())
	}
	if !m.keepAwakeEnabled || !m.cfg.TUI.KeepAwake {
		t.Errorf("2nd ENTER must keep the intent: memory=%v cfg=%v", m.keepAwakeEnabled, m.cfg.TUI.KeepAwake)
	}
	if m.keepAwakeErr != "" {
		t.Errorf("2nd ENTER succeeded, keepAwakeErr = %q, want empty", m.keepAwakeErr)
	}
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if !reloaded.TUI.KeepAwake {
		t.Error("keep_awake must not be written as false by a retry")
	}
	if !strings.Contains(m.modalItems[tuiSettingRowKeepAwake], "✓") {
		t.Errorf("row after successful retry = %q, want ✓", m.modalItems[tuiSettingRowKeepAwake])
	}
}

// TestEnterOnPersistentlyFailedKeepAwakeKeepsRequest: with a starter that
// always fails, N ENTERs must each call Start again and leave keep_awake true
// in memory and on disk — the no-delete invariant of the ⚠ state.
func TestEnterOnPersistentlyFailedKeepAwakeKeepsRequest(t *testing.T) {
	m := newSettingsTestModel(t)
	counter := &countingStarter{startErr: errors.New("still down")}
	m.keepAwake = keepawake.NewWithStarter(counter.starter)

	const n = 4
	for i := 1; i <= n; i++ {
		m.toggleKeepAwake()
		if !m.keepAwakeEnabled || !m.cfg.TUI.KeepAwake {
			t.Fatalf("ENTER %d: intent cleared (memory=%v cfg=%v), want it kept", i, m.keepAwakeEnabled, m.cfg.TUI.KeepAwake)
		}
		if m.keepAwakeStatus() != "⚠" {
			t.Fatalf("ENTER %d: status = %q, want ⚠", i, m.keepAwakeStatus())
		}
	}
	if counter.startsRun != n {
		t.Errorf("starter ran %d times, want %d (every ENTER must retry)", counter.startsRun, n)
	}
	if m.keepAwakeErr == "" {
		t.Error("expected the failure reason to remain recorded")
	}
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if !reloaded.TUI.KeepAwake {
		t.Error("keep_awake must still be true on disk after N failed ENTERs")
	}
}

// TestCancelKeepAwakeKeyOnFailedState: "d" in ⚠ cancels the request — the
// intent is cleared in memory AND on disk, the error line is gone and the
// starter is NOT called (a cancel is not a retry).
func TestCancelKeepAwakeKeyOnFailedState(t *testing.T) {
	m := newSettingsTestModel(t)
	counter := &countingStarter{startErr: errors.New("no mechanism")}
	m.keepAwake = keepawake.NewWithStarter(counter.starter)
	m.modalMode = ModalSettingsTUI
	m.modalSelectedIdx = tuiSettingRowKeepAwake
	m.loadTUISettings()

	m.toggleKeepAwake() // request it → ⚠
	if m.keepAwakeStatus() != "⚠" {
		t.Fatalf("setup: status = %q, want ⚠", m.keepAwakeStatus())
	}
	startsBefore := counter.startsRun
	if startsBefore != 1 {
		t.Fatalf("setup: starter ran %d times, want 1", startsBefore)
	}
	if m.keepAwakeErr == "" {
		t.Fatal("setup: expected a recorded error")
	}

	m = sendKeys(m, "d")

	if m.keepAwakeEnabled {
		t.Error(`"d" must clear the keep-awake intent in memory`)
	}
	if m.keepAwakeErr != "" {
		t.Errorf(`keepAwakeErr = %q after "d", want "" (error line cleared)`, m.keepAwakeErr)
	}
	if m.keepAwakeStatus() != "✗" {
		t.Errorf(`status after "d" = %q, want ✗`, m.keepAwakeStatus())
	}
	if counter.startsRun != startsBefore {
		t.Errorf("starter ran %d times, want %d (cancel must not retry)", counter.startsRun, startsBefore)
	}
	if counter.releases != 0 {
		t.Errorf("releases = %d, want 0 (nothing was held to release)", counter.releases)
	}
	if m.cfg.TUI.KeepAwake {
		t.Error("cfg must show keep_awake=false after cancel")
	}
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if reloaded.TUI.KeepAwake {
		t.Error("keep_awake must be false on disk after cancel")
	}
	if !strings.Contains(m.modalItems[tuiSettingRowKeepAwake], "✗") {
		t.Errorf("row after cancel = %q, want ✗", m.modalItems[tuiSettingRowKeepAwake])
	}
}

// TestCancelKeepAwakeKeyOutsideFailedStateDoesNothing: "d" must be a no-op in
// the ✓ (active) and ✗ (off) states — no cancel, no persist, no stop.
func TestCancelKeepAwakeKeyOutsideFailedStateDoesNothing(t *testing.T) {
	t.Run("active state", func(t *testing.T) {
		m := newSettingsTestModel(t)
		counter := &countingStarter{}
		m.keepAwake = keepawake.NewWithStarter(counter.starter)
		m.modalMode = ModalSettingsTUI
		m.toggleKeepAwake() // ✓
		if m.keepAwakeStatus() != "✓" {
			t.Fatalf("setup: status = %q, want ✓", m.keepAwakeStatus())
		}
		// Prove "d" did nothing: perturb the on-disk value, so a persist
		// triggered by the key press would be visible (keep_awake: true).
		m.cfg.TUI.KeepAwake = false
		if err := m.saveConfigToDisk(); err != nil {
			t.Fatalf("saving perturbed config: %v", err)
		}

		m = sendKeys(m, "d")

		if !m.keepAwakeEnabled {
			t.Error(`"d" in ✓ state must not clear the intent`)
		}
		if !m.keepAwake.Active() {
			t.Error(`"d" in ✓ state must not stop the inhibition`)
		}
		if counter.releases != 0 {
			t.Errorf("releases = %d, want 0 (the inhibition must stay held)", counter.releases)
		}
		reloaded, err := config.LoadConfig(config.DefaultConfigPath())
		if err != nil {
			t.Fatalf("reloading config: %v", err)
		}
		if reloaded.TUI.KeepAwake {
			t.Error(`"d" in ✓ state must not persist (disk still shows the perturbed false)`)
		}
	})

	t.Run("off state", func(t *testing.T) {
		m := newSettingsTestModel(t)
		counter := &countingStarter{}
		m.keepAwake = keepawake.NewWithStarter(counter.starter)
		m.modalMode = ModalSettingsTUI
		// Prove "d" did nothing: perturb the on-disk value (keep_awake: true),
		// so any persist triggered by the key press would show up as false.
		m.cfg.TUI.KeepAwake = true
		if err := m.saveConfigToDisk(); err != nil {
			t.Fatalf("saving perturbed config: %v", err)
		}

		m = sendKeys(m, "d")

		if m.keepAwakeEnabled {
			t.Error(`"d" in ✗ state must not flip the intent on`)
		}
		if counter.startsRun != 0 || counter.releases != 0 {
			t.Errorf("starter ran %d times / released %d, want 0/0 (no-op)", counter.startsRun, counter.releases)
		}
		if m.keepAwakeErr != "" {
			t.Errorf(`keepAwakeErr = %q after "d" in ✗ state, want ""`, m.keepAwakeErr)
		}
		reloaded, err := config.LoadConfig(config.DefaultConfigPath())
		if err != nil {
			t.Fatalf("reloading config: %v", err)
		}
		if !reloaded.TUI.KeepAwake {
			t.Error(`"d" in ✗ state must not persist (disk still shows the perturbed true)`)
		}
	})
}

// newFailedKeepAwakeSettingsModel puts the model in the ⚠ state (requested,
// Start failed) inside the Interface settings modal, exactly as ENTER on the
// keep-awake row would: intent set in memory AND on disk, a recorded reason,
// and the starter counted so a cancel (which must NOT retry) is observable.
func newFailedKeepAwakeSettingsModel(t *testing.T) (*Model, *countingStarter) {
	t.Helper()
	m := newSettingsTestModel(t)
	counter := &countingStarter{startErr: errors.New("no mechanism")}
	m.keepAwake = keepawake.NewWithStarter(counter.starter)
	m.modalMode = ModalSettingsTUI
	m.modalSelectedIdx = tuiSettingRowKeepAwake
	m.loadTUISettings()
	m.toggleKeepAwake() // request it → ⚠ (persists + records the reason)
	if m.keepAwakeStatus() != "⚠" {
		t.Fatalf("setup: status = %q, want ⚠", m.keepAwakeStatus())
	}
	if m.keepAwakeErr == "" {
		t.Fatal("setup: expected a recorded error")
	}
	return m, counter
}

// TestCancelKeyIgnoredWhileEditingField: while an inline settings field is
// being edited, "d" is a REGULAR CHARACTER and must reach the text input —
// it must not cancel the keep-awake request hiding behind the edit view.
// Guards the `m.settingsEditField == ""` conjunct of the cancel guard in
// handleModalKey.
func TestCancelKeyIgnoredWhileEditingField(t *testing.T) {
	m, counter := newFailedKeepAwakeSettingsModel(t)
	errBefore := m.keepAwakeErr

	// Enter the max-messages edit field, like ENTER on that row would.
	m.settingsEditField = "maxMessages"
	m.textInput.SetValue(strconv.Itoa(m.maxRenderedMessages))
	m.textInput.Focus()

	m = sendKeys(m, "d")

	if m.settingsEditField != "maxMessages" {
		t.Fatalf(`"d" must not leave the edit field; settingsEditField = %q`, m.settingsEditField)
	}
	if got := m.textInput.Value(); !strings.HasSuffix(got, "d") {
		t.Errorf(`"d" must be typed into the edit input, got %q`, got)
	}
	if !m.keepAwakeEnabled {
		t.Error(`"d" while editing must not clear the keep-awake intent`)
	}
	if m.keepAwakeErr != errBefore {
		t.Errorf("keepAwakeErr changed while editing: %q -> %q", errBefore, m.keepAwakeErr)
	}
	if m.keepAwakeStatus() != "⚠" {
		t.Errorf(`status after "d" while editing = %q, want ⚠ (unchanged)`, m.keepAwakeStatus())
	}
	if counter.startsRun != 1 {
		t.Errorf("starter ran %d times, want 1 (a regular character must not retry the start)", counter.startsRun)
	}
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if !reloaded.TUI.KeepAwake {
		t.Error("keep_awake must stay true on disk while typing in an edit field")
	}
}

// TestCancelKeyIgnoredWhileThemePickerOpen: while the theme picker is open
// over the Interface settings list, "d" is inert — it must not cancel the
// keep-awake request. Guards the `!m.themePickerActive` conjunct of the
// cancel guard in handleModalKey (the picker replaces the settings list).
func TestCancelKeyIgnoredWhileThemePickerOpen(t *testing.T) {
	m, counter := newFailedKeepAwakeSettingsModel(t)
	errBefore := m.keepAwakeErr

	// Open the picker over the failed keep-awake state, exactly like ENTER on
	// the theme row: switch to the picker list and rebuild the items.
	m.themePickerActive = true
	m.themePreviewName = m.currentThemeName
	m.loadThemePickerItems()
	if len(m.themePickerItems) == 0 {
		t.Fatal("setup: theme picker has no items")
	}
	if !m.keepAwakeRequestedButFailed() {
		t.Fatal("setup: the picker must not have cleared the ⚠ state")
	}

	m = sendKeys(m, "d")

	if !m.themePickerActive {
		t.Error(`"d" must not close the theme picker`)
	}
	if !m.keepAwakeEnabled {
		t.Error(`"d" with the picker open must not clear the keep-awake intent`)
	}
	if m.keepAwakeErr != errBefore {
		t.Errorf("keepAwakeErr changed with the picker open: %q -> %q", errBefore, m.keepAwakeErr)
	}
	if m.keepAwakeStatus() != "⚠" {
		t.Errorf(`status after "d" with the picker open = %q, want ⚠ (unchanged)`, m.keepAwakeStatus())
	}
	if counter.startsRun != 1 {
		t.Errorf("starter ran %d times, want 1 (a regular character must not retry the start)", counter.startsRun)
	}
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if !reloaded.TUI.KeepAwake {
		t.Error("keep_awake must stay true on disk while the theme picker is open")
	}
}

// TestKeepAwakeErrorHiddenInThemePicker: the ⚠ block (reason line + "d: stop
// requesting" hint) rides the Interface settings LIST renderer, but the theme
// picker replaces that list and its "d" is inert, so advertising the cancel
// key there would be a lie (see the cancel guard). The block must be hidden
// while the picker is open and shown again once it closes.
func TestKeepAwakeErrorHiddenInThemePicker(t *testing.T) {
	hint := i18n.T("tui.settings.keepAwakeCancelHint")
	if hint == "" || hint == "tui.settings.keepAwakeCancelHint" {
		t.Fatal("tui.settings.keepAwakeCancelHint missing from the active locale")
	}

	m, _ := newFailedKeepAwakeSettingsModel(t)
	m.width = 80
	m.height = 30

	// Picker closed (the settings list): the reason AND the hint are visible.
	frame := ansi.Strip(m.renderModal(m.modalTitleFor(ModalSettingsTUI)))
	if !strings.Contains(frame, "no mechanism") {
		t.Errorf("picker closed: the ⚠ reason must be rendered:\n%s", frame)
	}
	if !strings.Contains(frame, hint) {
		t.Errorf("picker closed: the cancel hint must be rendered:\n%s", frame)
	}

	// Picker open: neither the reason nor the hint may appear.
	m.themePickerActive = true
	m.loadThemePickerItems()
	frame = ansi.Strip(m.renderModal(i18n.T("tui.settings.themePickerTitle")))
	if strings.Contains(frame, "no mechanism") {
		t.Errorf("picker open: the ⚠ reason must NOT be rendered:\n%s", frame)
	}
	if strings.Contains(frame, hint) {
		t.Errorf("picker open: the cancel hint must NOT be rendered (its key is inert):\n%s", frame)
	}
	// The picker's own content is still there, so the block is hidden, not
	// the whole frame.
	if !strings.Contains(frame, i18n.T("tui.settings.builtinThemes")) {
		t.Errorf("picker open: expected the picker's built-in themes header:\n%s", frame)
	}

	// Closing the picker restores the ⚠ block.
	m.themePickerActive = false
	m.loadTUISettings()
	frame = ansi.Strip(m.renderModal(m.modalTitleFor(ModalSettingsTUI)))
	if !strings.Contains(frame, hint) {
		t.Errorf("picker closed again: the cancel hint must come back:\n%s", frame)
	}
}

// TestKeepAwakeCancelHintOnlyInFailedState: the "stop requesting" hint is
// rendered under the error line ONLY in the ⚠ state — in ✓/✗ the "d" key
// does nothing, so advertising it would be a lie.
func TestKeepAwakeCancelHintOnlyInFailedState(t *testing.T) {
	// The test may run with any active UI language (SetLanguage leaks across
	// tests by design), so take the hint from whatever locale is active —
	// en/es/pt all carry the key (parity is asserted in pkg/tui/i18n).
	hint := i18n.T("tui.settings.keepAwakeCancelHint")
	if hint == "tui.settings.keepAwakeCancelHint" {
		t.Fatal("tui.settings.keepAwakeCancelHint missing from the default locale")
	}
	if hint == "" {
		t.Fatal("tui.settings.keepAwakeCancelHint is empty")
	}

	newRow := func(t *testing.T) *Model {
		t.Helper()
		m := newSettingsTestModel(t)
		m.modalMode = ModalSettingsTUI
		m.width = 80
		m.height = 30
		m.keepAwakeErr = "boom: mechanism refused" // reason present in all 3 states
		m.loadTUISettings()
		return m
	}

	// ⚠ — hint present (below the error line).
	t.Run("failed state shows the hint", func(t *testing.T) {
		m := newRow(t)
		m.keepAwakeEnabled = true
		m.loadTUISettings()
		out := ansi.Strip(m.renderModal(m.modalTitleFor(ModalSettingsTUI)))
		if !strings.Contains(out, hint) {
			t.Errorf("⚠ state must render the cancel hint %q:\n%s", hint, out)
		}
		if !strings.Contains(out, "boom: mechanism refused") {
			t.Errorf("⚠ state must render the reason:\n%s", out)
		}
	})

	// ✓ — no hint.
	t.Run("active state hides the hint", func(t *testing.T) {
		m := newRow(t)
		m.keepAwakeEnabled = true
		m.keepAwake = keepawake.NewWithStarter(func() (func(), error) { return func() {}, nil })
		if err := m.startKeepAwake(); err != nil {
			t.Fatalf("startKeepAwake: %v", err)
		}
		defer m.Close()
		m.loadTUISettings()
		out := ansi.Strip(m.renderModal(m.modalTitleFor(ModalSettingsTUI)))
		if strings.Contains(out, hint) {
			t.Errorf("✓ state must NOT render the cancel hint:\n%s", out)
		}
	})

	// ✗ — no hint (and no error line either, but be explicit about the hint).
	t.Run("off state hides the hint", func(t *testing.T) {
		m := newRow(t)
		m.keepAwakeEnabled = false
		m.loadTUISettings()
		out := ansi.Strip(m.renderModal(m.modalTitleFor(ModalSettingsTUI)))
		if strings.Contains(out, hint) {
			t.Errorf("✗ state must NOT render the cancel hint:\n%s", out)
		}
	})
}

// TestKeepAwakeErrorLineKeepsFrameGeometry: a >100-rune failure reason is
// truncated to the item budget, so the rendered box line keeps its right
// border "│" and never reaches m.width (where lipgloss.Place stops padding)
// at widths 60/80/100. It also checks the sanitized/truncated line cannot
// leak control bytes (raw CSI) or bidi formatting characters into the frame.
func TestKeepAwakeErrorLineKeepsFrameGeometry(t *testing.T) {
	// The real-world reason: >100 runes and the actionable fragment at the end
	// of the head ("... executable file not found in $PATH").
	longErr := "exec: \"systemd-inhibit\": executable file not found in $PATH: " +
		"keep-awake could not acquire the sleep inhibition on this host " +
		"(mechanism unavailable or not permitted)"
	if utf8.RuneCountInString(longErr) <= 100 {
		t.Fatalf("test reason must be >100 runes, got %d", utf8.RuneCountInString(longErr))
	}

	newRow := func(t *testing.T, width int) *Model {
		t.Helper()
		m := newSettingsTestModel(t)
		m.modalMode = ModalSettingsTUI
		m.keepAwakeEnabled = true // a reason exists only for a REQUESTED inhibition
		m.keepAwakeErr = longErr
		m.width = width
		m.height = 30
		m.loadTUISettings()
		return m
	}

	for _, width := range []int{60, 80, 100} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m := newRow(t, width)
			frame := m.renderModal(m.modalTitleFor(ModalSettingsTUI))
			lines := strings.Split(frame, "\n")

			errLineIdx := -1
			for i, line := range lines {
				plain := ansi.Strip(line)
				if strings.Contains(plain, "⚠") {
					errLineIdx = i
				}
				// Any border row must be EXACTLY the frame width: a row that
				// reaches m.width is what makes Place stop padding it (and
				// the right "│" disappear).
				if w := ansi.StringWidth(line); w > width {
					t.Errorf("frame line %d is %d cells wide, > %d: %q", i, w, width, plain)
				}
			}
			if errLineIdx < 0 {
				t.Fatalf("the ⚠ error line is missing from the frame:\n%q", frame)
			}

			// paintFrame centers the box, so the row is padded with spaces on
			// both sides: trim them to test the box line itself.
			errLine := strings.TrimSpace(ansi.Strip(lines[errLineIdx]))
			if !strings.HasSuffix(errLine, "│") {
				t.Errorf("error line lost its right border: %q", errLine)
			}
			if !strings.Contains(errLine, "⚠") {
				t.Errorf("error line must keep the ⚠ glyph: %q", errLine)
			}
			if !strings.Contains(errLine, "...") {
				t.Errorf("expected the over-long reason to be truncated with an ellipsis: %q", errLine)
			}
			// The row must leave room for the border: 2 (border) + 2 (padding)
			// + "  ⚠ " (4) + budget(width-8) == width. Anything wider and the
			// border cannot fit.
			if w := ansi.StringWidth(lines[errLineIdx]); w != width {
				t.Errorf("error line width = %d, want exactly %d (content + border + padding)", w, width)
			}
		})
	}
}

// TestKeepAwakeErrorLineSanitizesControlAndBidi: the reason comes from the
// platform (exec errors, wrapped strings) and may carry CSI sequences or bidi
// overrides. Neither may reach the frame: controls desync paint vs measured
// width (border corruption) and bidi characters reorder the row.
func TestKeepAwakeErrorLineSanitizesControlAndBidi(t *testing.T) {
	m := newSettingsTestModel(t)
	m.modalMode = ModalSettingsTUI
	m.keepAwakeEnabled = true
	m.width = 80
	m.height = 30
	// Raw CSI SGR inside the reason + RLO/PDF bidi override (trojan source).
	// The bidi characters are written as Go escapes (\u202e), never as literal
	// bytes: a literal U+202E in the source trips bidichk (golangci-lint
	// `default: all`) and breaks CI, while the escape produces the exact same
	// runtime string.
	m.keepAwakeErr = "boom \x1b[31mred\x1b[0m \u202ereversed\u202e tail " +
		strings.Repeat("keep-awake mechanism unavailable ", 5)
	m.loadTUISettings()

	frame := m.renderModal(m.modalTitleFor(ModalSettingsTUI))
	lines := strings.Split(frame, "\n")

	found := false
	for i, line := range lines {
		plain := ansi.Strip(line)
		if strings.Contains(line, "⚠") {
			found = true
			trimmed := strings.TrimSpace(plain)
			// The reason carried its own SGR (\x1b[31m…\x1b[0m) and
			// sanitizeDisplayText must have removed it. Checking `plain` for an
			// ESC would be VACUOUS (ansi.Strip already stripped them) and
			// checking for any ESC in `line` would false-positive on the yellow
			// styling lipgloss adds (and on its trailing \x1b[0m), so assert on
			// the exact sequence that can only come from the reason.
			if strings.Contains(line, "\x1b[31m") {
				t.Errorf("error line %d leaks the reason's raw SGR: %q", i, line)
			}
			if strings.ContainsRune(plain, '\u202E') {
				t.Errorf("error line %d leaks the bidi RLO U+202E: %q", i, plain)
			}
			if !strings.HasSuffix(trimmed, "│") {
				t.Errorf("error line %d lost its right border: %q", i, trimmed)
			}
		}
		if w := ansi.StringWidth(line); w > m.width {
			t.Errorf("frame line %d is %d cells wide, > %d: %q", i, w, m.width, plain)
		}
	}
	if !found {
		t.Fatalf("the ⚠ error line is missing from the frame:\n%q", frame)
	}
}
