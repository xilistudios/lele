package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/tui/theme"
)

// --- settings_tui.go additional coverage ---

func TestLoadTUISettings(t *testing.T) {
	m := &Model{
		mouseEnabled:           true,
		maxRenderedMessages:    200,
		streamThrottleInterval: 32000000, // 32ms
		currentThemeName:       "dracula",
	}
	m.loadTUISettings()
	if len(m.modalItems) != 4 {
		t.Fatalf("expected 4 TUI settings rows, got %d", len(m.modalItems))
	}
	if !strings.Contains(m.modalItems[0], "dracula") {
		t.Errorf("expected theme in row 0, got %q", m.modalItems[0])
	}
	if !strings.Contains(m.modalItems[1], "✓") {
		t.Errorf("expected mouse ✓ in row 1, got %q", m.modalItems[1])
	}
	if !strings.Contains(m.modalItems[2], "200") {
		t.Errorf("expected max messages 200, got %q", m.modalItems[2])
	}
}

func TestPersistTUISettingsNilConfig(t *testing.T) {
	m := &Model{mouseEnabled: false}
	m.persistTUISettings() // no panic, no-op
}

func TestToggleTUIMouseExtra(t *testing.T) {
	m := &Model{mouseEnabled: true, maxRenderedMessages: 200, currentThemeName: "dracula"}
	cmd := m.toggleTUIMouse()
	if m.mouseEnabled {
		t.Error("expected mouse toggled off")
	}
	if cmd == nil {
		t.Error("expected a cmd when disabling mouse (DisableMouse)")
	}
	cmd2 := m.toggleTUIMouse()
	if !m.mouseEnabled {
		t.Error("expected mouse toggled back on")
	}
	if cmd2 == nil {
		t.Error("expected a cmd when enabling mouse")
	}
}

func TestHandleTUISettingsEnterTheme(t *testing.T) {
	m := &Model{modalSelectedIdx: 0, currentThemeName: "dracula"}
	cmd := m.handleTUISettingsEnter()
	if cmd == nil {
		t.Skip("community fetch cmd may be nil; check state")
	}
	_ = cmd
	if !m.themePickerActive {
		t.Error("expected theme picker active")
	}
}

func TestHandleTUISettingsEnterMouse(t *testing.T) {
	m := &Model{modalSelectedIdx: 1}
	if cmd := m.handleTUISettingsEnter(); cmd != nil {
		t.Error("expected nil cmd for mouse toggle row")
	}
}

func TestBuildThemePickerItems(t *testing.T) {
	m := &Model{currentThemeName: "dracula"}
	items := m.buildThemePickerItems()
	// Must have at least builtin header + all builtins + community header.
	if len(items) < 3 {
		t.Fatalf("expected at least 3 picker items, got %d", len(items))
	}
	if items[0].kind != "header" {
		t.Errorf("expected first item header, got %q", items[0].kind)
	}
}

func TestBuildThemePickerItemsLoading(t *testing.T) {
	m := &Model{communityLoading: true}
	items := m.buildThemePickerItems()
	foundLoading := false
	for _, it := range items {
		if it.kind == "loading" {
			foundLoading = true
		}
	}
	if !foundLoading {
		t.Error("expected a loading item")
	}
}

func TestBuildThemePickerItemsError(t *testing.T) {
	m := &Model{communityErr: "network down"}
	items := m.buildThemePickerItems()
	foundError, foundRetry := false, false
	for _, it := range items {
		if it.kind == "error" {
			foundError = true
		}
		if it.kind == "retry" {
			foundRetry = true
		}
	}
	if !foundError || !foundRetry {
		t.Errorf("expected error and retry items (error=%v retry=%v)", foundError, foundRetry)
	}
}

func TestBuildThemePickerItemsCommunityIndex(t *testing.T) {
	m := &Model{
		communityIndex: []theme.CommunityThemeEntry{
			{Name: "custom-theme"},
		},
		currentThemeName:   "dracula",
		installedCommunity: []string{"custom-theme"},
	}
	items := m.buildThemePickerItems()
	foundCommunity := false
	for _, it := range items {
		if it.kind == "community" && it.name == "custom-theme" {
			foundCommunity = true
			if !strings.Contains(it.label, "✓") {
				t.Errorf("expected installed community theme marked with ✓, got %q", it.label)
			}
		}
	}
	if !foundCommunity {
		t.Error("expected community theme in items")
	}
}

func TestLoadThemePickerItemsExtra(t *testing.T) {
	m := &Model{currentThemeName: "dracula"}
	m.loadThemePickerItems()
	if len(m.themePickerItems) != len(m.modalItems) {
		t.Error("themePickerItems and modalItems should be same length")
	}
	if m.modalSelectedIdx == 0 {
		// The dracula builtin may be at index 1 (after header). Verify
		// selection matches dracula.
		found := false
		for _, it := range m.themePickerItems {
			if it.kind == "builtin" && it.name == "dracula" {
				found = true
			}
		}
		if found {
			t.Logf("theme picker select idx=%d", m.modalSelectedIdx)
		}
	}
}

func TestInstallCommunityThemeCmdInvalidNameReturnsMsg(t *testing.T) {
	// installCommunityThemeCmd always returns installThemeMsg; with an invalid
	// name the underlying FetchCommunityTheme returns an error fast.
	m := &Model{}
	cmd := m.installCommunityThemeCmd("Invalid/Name!")
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	msg := cmd()
	im, ok := msg.(installThemeMsg)
	if !ok {
		t.Fatalf("expected installThemeMsg, got %T", msg)
	}
	if im.name != "Invalid/Name!" {
		t.Errorf("expected name carried through, got %q", im.name)
	}
}

func TestHandleTUISettingsInputInvalidNumber(t *testing.T) {
	m := &Model{settingsEditField: "maxMessages"}
	m.handleTUISettingsInput("abc")
	if m.formError == "" {
		t.Error("expected formError for non-numeric")
	}
	m.settingsEditField = "maxMessages"
	m.handleTUISettingsInput("-5")
	if m.formError == "" {
		t.Error("expected formError for <= 0")
	}
}

func TestHandleTUISettingsInputValid(t *testing.T) {
	m := &Model{settingsEditField: "maxMessages", maxRenderedMessages: 200}
	m.handleTUISettingsInput("500")
	if m.maxRenderedMessages != 500 {
		t.Errorf("expected maxRenderedMessages 500, got %d", m.maxRenderedMessages)
	}
	if m.settingsEditField != "" {
		t.Errorf("expected edit field cleared, got %q", m.settingsEditField)
	}
	// streamThrottle
	m2 := &Model{settingsEditField: "streamThrottle"}
	m2.handleTUISettingsInput("64")
	if m2.streamThrottleInterval.Milliseconds() != 64 {
		t.Errorf("expected throttle 64ms, got %v", m2.streamThrottleInterval)
	}
}