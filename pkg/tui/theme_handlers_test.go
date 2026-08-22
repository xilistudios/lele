package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

// TestUpdate_CommunityIndexMsgPopulates drives the communityIndexMsg handler.
func TestUpdate_CommunityIndexMsgPopulates(t *testing.T) {
	m := newTestModel(t)
	m.themePickerActive = true
	m.communityLoading = true
	m.communityErr = "old error"

	upd, _ := m.Update(communityIndexMsg{
		entries: []theme.CommunityThemeEntry{
			{Name: "solarized", Description: "d"},
		},
		err: "",
	})
	mm := upd.(*Model)
	if mm.communityLoading {
		t.Error("communityLoading should be false after index msg")
	}
	if mm.communityErr != "" {
		t.Errorf("communityErr = %q, want cleared", mm.communityErr)
	}
	if len(mm.communityIndex) != 1 || mm.communityIndex[0].Name != "solarized" {
		t.Errorf("communityIndex = %+v, want solarized entry", mm.communityIndex)
	}
}

// TestUpdate_CommunityIndexMsgError drives the error path.
func TestUpdate_CommunityIndexMsgError(t *testing.T) {
	m := newTestModel(t)
	m.themePickerActive = true
	upd, _ := m.Update(communityIndexMsg{err: "network fail"})
	mm := upd.(*Model)
	if mm.communityErr != "network fail" {
		t.Errorf("communityErr = %q, want network fail", mm.communityErr)
	}
	if mm.communityLoading {
		t.Error("communityLoading should be false on error")
	}
}

// TestUpdate_InstallThemeMsgApplies drives installThemeMsg success.
func TestUpdate_InstallThemeMsgApplies(t *testing.T) {
	m := newTestModel(t)
	m.themePickerActive = true
	m.communityLoading = true
	m.communityErr = "old"

	if m.customThemes == nil {
		m.customThemes = make(map[string]theme.Theme)
	}

	upd, _ := m.Update(installThemeMsg{
		name:  "draculabright",
		theme: theme.DraculaDefault,
		err:   "",
	})
	mm := upd.(*Model)
	if mm.communityLoading {
		t.Error("communityLoading should be false after install msg")
	}
	if _, ok := mm.customThemes["draculabright"]; !ok {
		t.Error("customThemes should contain draculabright")
	}
	found := false
	for _, n := range mm.installedCommunity {
		if n == "draculabright" {
			found = true
		}
	}
	if !found {
		t.Errorf("installedCommunity should contain draculabright, got %v", mm.installedCommunity)
	}
}

// TestUpdate_InstallThemeMsgError drives installThemeMsg error path.
func TestUpdate_InstallThemeMsgError(t *testing.T) {
	m := newTestModel(t)
	m.themePickerActive = false
	upd, _ := m.Update(installThemeMsg{name: "x", err: "download failed"})
	mm := upd.(*Model)
	if mm.communityErr != "download failed" {
		t.Errorf("communityErr = %q, want download failed", mm.communityErr)
	}
}

// TestUpdate_SettingsSelectorNav drives up/down/enter/esc for an active
// settings inline selector.
func TestUpdate_SettingsSelectorNav(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalSettingsSystemEdit
	m.settingsSelectorActive = true
	m.settingsSelectorItems = []string{"Option A", "Option B"}
	m.settingsSelectorIdx = 0

	// down
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	mm := upd.(*Model)
	if mm.settingsSelectorIdx != 1 {
		t.Errorf("selector idx after down = %d, want 1", mm.settingsSelectorIdx)
	}

	// up
	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	mm = upd.(*Model)
	if mm.settingsSelectorIdx != 0 {
		t.Errorf("selector idx after up = %d, want 0", mm.settingsSelectorIdx)
	}

	// q returns without change
	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	mm = upd.(*Model)
	if !mm.settingsSelectorActive {
		t.Log("q left selector active (expected)")
	}

	// esc cancels
	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm = upd.(*Model)
	if mm.settingsSelectorActive {
		t.Error("esc should cancel the selector")
	}
}
