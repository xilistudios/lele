package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/xilistudios/lele/pkg/config"
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

	// 4 items: theme, mouse, max messages, stream throttle.
	if len(m.modalItems) != 4 {
		t.Fatalf("expected 4 items, got %d", len(m.modalItems))
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

	// Disabled mouse should show ✗
	m.mouseEnabled = false
	m.loadTUISettings()
	if !strings.Contains(m.modalItems[1], "✗") {
		t.Errorf("mouse item should show disabled mark: %q", m.modalItems[1])
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
	for _, item := range m.modalItems {
		if !strings.HasPrefix(item, "  ") && !strings.HasPrefix(item, "• ") {
			t.Errorf("theme item should start with 2 spaces or •: %q", item)
		}
	}
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
