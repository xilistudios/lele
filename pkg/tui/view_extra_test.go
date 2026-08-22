package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/channels"
)

func TestMaxModalVisible(t *testing.T) {
	m := &Model{height: 24, modalItems: []string{"a", "b", "c", "d", "e"}}
	if got := m.maxModalVisible(); got != 5 {
		t.Errorf("maxModalVisible with many items = %d, want 5 (limited by items)", got)
	}
	m2 := &Model{height: 10, modalItems: make([]string, 100)}
	if got := m2.maxModalVisible(); got != 3 {
		t.Errorf("maxModalVisible with small height = %d, want 3 (min clamp)", got)
	}
	m3 := &Model{height: 2, modalItems: []string{"a", "b", "c"}}
	if got := m3.maxModalVisible(); got != 3 {
		t.Errorf("maxModalVisible min clamp = %d, want 3", got)
	}
	// empty items
	m4 := &Model{height: 24}
	if got := m4.maxModalVisible(); got != 0 {
		t.Errorf("maxModalVisible empty = %d, want 0", got)
	}
}

func TestRenderModal(t *testing.T) {
	m := &Model{width: 60, height: 20, modalItems: []string{"item1", "item2", "item3"}, modalSelectedIdx: 1}
	out := m.renderModal("Modal Title")
	if !strings.Contains(out, "Modal Title") {
		t.Errorf("expected title, got %q", out)
	}
	if !strings.Contains(out, "item2") {
		t.Errorf("expected item2 in output, got %q", out)
	}
}

func TestRenderModalScrollClampAbove(t *testing.T) {
	m := &Model{width: 60, height: 20, modalItems: []string{"a", "b", "c", "d"}, modalSelectedIdx: 0, modalScrollOffset: 5}
	out := m.renderModal("T")
	// modalScrollOffset should be clamped to 0
	if m.modalScrollOffset != 0 {
		t.Errorf("expected scroll offset clamped forward to 0, got %d", m.modalScrollOffset)
	}
	_ = out
}

func TestRenderModalThemePicker(t *testing.T) {
	// When themePickerActive with header/loading/error items.
	m := &Model{width: 60, height: 20, themePickerActive: true, themePickerItems: []themePickerItem{
		{kind: "header", label: "Headers"},
		{kind: "loading", label: "Loading..."},
		{kind: "builtin", name: "dracula"},
	}, modalItems: []string{"Headers", "Loading...", "dracula"}}
	out := m.renderModal("Theme")
	if !strings.Contains(out, "Headers") {
		t.Errorf("expected header label, got %q", out)
	}
}

func TestRenderModalThemePickerSelectable(t *testing.T) {
	m := &Model{width: 60, height: 20, themePickerActive: true, themePickerItems: []themePickerItem{
		{kind: "community", name: "mytheme"},
	}, modalItems: []string{"mytheme"}, modalSelectedIdx: 0}
	out := m.renderModal("Theme")
	if !strings.Contains(out, "mytheme") {
		t.Errorf("expected community theme selectable, got %q", out)
	}
}

func TestRenderTUISettingsEditMode(t *testing.T) {
	m := &Model{width: 60, height: 20, settingsEditField: "maxMessages"}
	m.textInput.SetValue("200")
	out := m.renderTUISettings("Interface")
	if !strings.Contains(out, "Interface") {
		t.Errorf("expected title in output, got %q", out)
	}
	m2 := &Model{width: 60, height: 20, settingsEditField: "streamThrottle", formError: "bad value"}
	out2 := m2.renderTUISettings("Interface")
	_ = out2
}

func TestRenderTUISettingsListMode(t *testing.T) {
	m := &Model{width: 60, height: 20, modalItems: []string{"a", "b"}}
	out := m.renderTUISettings("Interface")
	if !strings.Contains(out, "Interface") {
		t.Errorf("expected title, got %q", out)
	}
}

func TestRenderTUISettingsThemePicker(t *testing.T) {
	m := &Model{width: 60, height: 20, themePickerActive: true}
	out := m.renderTUISettings("Interface")
	if out == "" {
		t.Fatal("expected output")
	}
}

func TestSystemSettingsTitle(t *testing.T) {
	m := &Model{}
	if got := m.systemSettingsTitle(); got == "" {
		t.Error("expected default system title")
	}
	// each group
	for idx := 0; idx <= 5; idx++ {
		m2 := &Model{settingsSection: sysSubViewName(idx)}
		if got := m2.systemSettingsTitle(); got == "" {
			t.Errorf("sys_%d should yield a title", idx)
		}
	}
}

func TestRenderSystemSettingsEdit(t *testing.T) {
	m := &Model{width: 60, height: 20, settingsEditField: "maxTokens"}
	m.textInput.SetValue("4096")
	out := m.renderSystemSettingsEdit("System")
	if !strings.Contains(out, "System") || !strings.Contains(out, "maxTokens") {
		t.Errorf("expected title and field label, got %q", out)
	}
	m2 := &Model{width: 60, height: 20, settingsEditField: "x", formError: "err"}
	out2 := m2.renderSystemSettingsEdit("System")
	if !strings.Contains(out2, "err") {
		t.Errorf("expected error shown, got %q", out2)
	}
}

func TestRenderAgentEditInput(t *testing.T) {
	m := &Model{width: 60, height: 20, settingsAgentID: "myagent", settingsEditField: "name"}
	m.textInput.SetValue("val")
	out := m.renderAgentEditInput()
	if !strings.Contains(out, "myagent") {
		t.Errorf("expected agent id title, got %q", out)
	}
	// confirmDelete branch
	m2 := &Model{width: 60, height: 20, settingsEditField: "confirmDelete", formError: "Are you sure?"}
	out2 := m2.renderAgentEditInput()
	if !strings.Contains(out2, "Are you sure?") {
		t.Errorf("expected confirm text, got %q", out2)
	}
}

func TestRenderBgExecOutput(t *testing.T) {
	m := &Model{width: 60, height: 24, bgExecViewID: "proc1", bgExecViewStatus: "running", bgExecViewOutput: "line1\nline2"}
	out := m.renderBgExecOutput()
	if !strings.Contains(out, "proc1") {
		t.Errorf("expected process id, got %q", out)
	}
	// empty output
	m2 := &Model{width: 60, height: 24, bgExecViewID: "p", bgExecViewStatus: "completed"}
	out2 := m2.renderBgExecOutput()
	_ = out2
	// statuses
	for _, st := range []string{"completed", "failed", "weird"} {
		m3 := &Model{width: 60, height: 24, bgExecViewID: "p", bgExecViewStatus: st, bgExecViewOutput: "x"}
		m3.renderBgExecOutput()
	}
}

func TestRenderSkillPicker(t *testing.T) {
	m := &Model{width: 60, height: 24, skillsScanRepo: "owner/repo", skillsScanResults: []channels.ScannedSkill{{Name: "alpha", Description: "desc"}}, modalItems: []string{"placeholder"}}
	out := m.renderSkillPicker("Install Skills")
	if !strings.Contains(out, "Install Skills") {
		t.Errorf("expected title, got %q", out)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected skill name, got %q", out)
	}
	// formError path
	m2 := &Model{width: 60, height: 24, skillsScanResults: []channels.ScannedSkill{{Name: "a"}}, skillsSelectedMap: map[int]bool{}, formError: "error here", modalItems: []string{"placeholder"}}
	out2 := m2.renderSkillPicker("Install")
	if !strings.Contains(out2, "error here") {
		t.Errorf("expected error in output, got %q", out2)
	}
	// scroll clamp
	m3 := &Model{width: 60, height: 24, skillsScanResults: []channels.ScannedSkill{{Name: "a"}, {Name: "b"}}, skillsSelectedMap: map[int]bool{0: true, 1: true}, modalItems: []string{"p1", "p2"}, modalSelectedIdx: 5, modalScrollOffset: 0}
	m3.renderSkillPicker("T")
	if m3.modalScrollOffset < 0 {
		t.Error("scroll offset should not go negative")
	}
}

func TestRenderSkillPickerScrollOffset(t *testing.T) {
	m := &Model{width: 60, height: 24, skillsScanResults: []channels.ScannedSkill{{Name: "a"}, {Name: "b"}}, modalItems: []string{"p1", "p2"}, modalSelectedIdx: 0, modalScrollOffset: 3}
	m.renderSkillPicker("T")
	if m.modalSelectedIdx < m.modalScrollOffset {
		t.Errorf("expected selection index >= scroll offset, got idx=%d offset=%d", m.modalSelectedIdx, m.modalScrollOffset)
	}
}
