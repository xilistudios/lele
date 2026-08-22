package tui

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
)

func TestSkillsLoaderNil(t *testing.T) {
	m := &Model{}
	if m.skillsLoader() != nil {
		t.Error("expected nil skillsLoader when agentLoop nil")
	}
	if m.skillInstaller() != nil {
		t.Error("expected nil skillInstaller when agentLoop nil")
	}
}

func TestLoadSkillsListNilLoader(t *testing.T) {
	m := &Model{}
	m.loadSkillsList()
	if len(m.modalItems) == 0 {
		t.Fatal("expected unavailable message")
	}
	// When the loader is nil, the function returns early after appending the
	// unavailable message — no action keys are added.
	if len(m.skillsModalKeys) != 0 {
		t.Errorf("expected 0 action keys for nil loader, got %v", m.skillsModalKeys)
	}
}

func TestLoadSkillsListWithSkills(t *testing.T) {
	m := newTestModel(t)
	loader := m.skillsLoader()
	if loader == nil {
		t.Skip("no skills loader in test model")
	}
	m.loadSkillsList()
	// Should have action entries
	foundInstall := false
	for _, k := range m.skillsModalKeys {
		if k == "__install__" {
			foundInstall = true
		}
	}
	if !foundInstall {
		t.Error("expected __install__ action key")
	}
}

func TestHandleSkillsEnterOutOfRange(t *testing.T) {
	m := &Model{skillsModalKeys: []string{"a"}, modalSelectedIdx: 5}
	if cmd := m.handleSkillsEnter(); cmd != nil {
		t.Error("expected nil cmd for out-of-range index")
	}
}

func TestHandleSkillsEnterSectionHeader(t *testing.T) {
	m := &Model{skillsModalKeys: []string{"", "b"}, modalSelectedIdx: 0}
	if cmd := m.handleSkillsEnter(); cmd != nil {
		t.Error("expected nil cmd for section header")
	}
}

func TestHandleSkillsEnterInstall(t *testing.T) {
	m := &Model{skillsModalKeys: []string{"__install__"}, modalSelectedIdx: 0, width: 80, height: 24}
	cmd := m.handleSkillsEnter()
	if m.modalMode != ModalSkillInstall {
		t.Errorf("expected ModalSkillInstall, got %v", m.modalMode)
	}
	if cmd == nil {
		t.Error("expected a cmd for install mode entry")
	}
}

func TestHandleSkillsEnterToggle(t *testing.T) {
	m := newTestModel(t)
	loader := m.skillsLoader()
	if loader == nil {
		t.Skip("no skills loader")
	}
	m.skillsModalKeys = []string{"some-skill"}
	m.modalSelectedIdx = 0
	cmd := m.handleSkillsEnter()
	if cmd == nil {
		t.Fatal("expected toggle cmd")
	}
	msg := cmd()
	toggleMsg, ok := msg.(skillToggleResultMsg)
	if !ok {
		t.Fatalf("expected skillToggleResultMsg, got %T", msg)
	}
	if toggleMsg.skillName != "some-skill" {
		t.Errorf("expected skillName some-skill, got %q", toggleMsg.skillName)
	}
}

func TestToggleSkillCmdNilLoader(t *testing.T) {
	m := &Model{}
	cmd := m.toggleSkillCmd("foo")
	msg := cmd()
	res, ok := msg.(skillToggleResultMsg)
	if !ok {
		t.Fatalf("expected skillToggleResultMsg, got %T", msg)
	}
	if res.err == nil {
		t.Error("expected error when loader nil")
	}
}

func TestHandleSkillInstallSubmitEmpty(t *testing.T) {
	m := &Model{}
	m.textInput.SetValue("   ")
	if cmd := m.handleSkillInstallSubmit(); cmd != nil {
		t.Error("expected nil cmd for empty repo")
	}
	if m.formError == "" {
		t.Error("expected formError for empty repo")
	}
}

func TestHandleSkillInstallSubmitInvalid(t *testing.T) {
	m := &Model{}
	m.textInput.SetValue("just-one-token")
	if cmd := m.handleSkillInstallSubmit(); cmd != nil {
		t.Error("expected nil cmd for invalid repo")
	}
	if m.formError == "" {
		t.Error("expected formError for invalid repo")
	}
}

func TestHandleSkillInstallSubmitValidNoInstaller(t *testing.T) {
	m := &Model{}
	m.textInput.SetValue("owner/repo")
	cmd := m.handleSkillInstallSubmit()
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	if m.skillsScanRepo != "owner/repo" {
		t.Errorf("expected scan repo set, got %q", m.skillsScanRepo)
	}
	msg := cmd()
	res, ok := msg.(skillsScanResultMsg)
	if !ok {
		t.Fatalf("expected skillsScanResultMsg, got %T", msg)
	}
	if res.err == nil {
		t.Error("expected error when installer nil")
	}
}

func TestHandleSkillPickerEnterNoResults(t *testing.T) {
	m := &Model{}
	if cmd := m.handleSkillPickerEnter(); cmd != nil {
		t.Error("expected nil cmd when no results")
	}
}

func TestHandleSkillPickerEnterNoneSelected(t *testing.T) {
	m := &Model{skillsScanResults: []channels.ScannedSkill{{Name: "a"}}, skillsSelectedMap: map[int]bool{0: false}}
	if cmd := m.handleSkillPickerEnter(); cmd != nil {
		t.Error("expected nil when nothing selected")
	}
	if m.formError == "" {
		t.Error("expected formError when nothing selected")
	}
}

func TestHandleSkillPickerEnterSelected(t *testing.T) {
	m := &Model{skillsScanResults: []channels.ScannedSkill{{Name: "a", Path: "skills/a"}}, skillsScanRepo: "o/r", skillsSelectedMap: map[int]bool{0: true}}
	cmd := m.handleSkillPickerEnter()
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	msg := cmd()
	res, ok := msg.(skillsInstallResultMsg)
	if !ok {
		t.Fatalf("expected skillsInstallResultMsg, got %T", msg)
	}
	if res.err == nil {
		t.Error("expected error when installer nil")
	}
}

func TestHandleSkillPickerToggle(t *testing.T) {
	m := &Model{modalSelectedIdx: 2}
	m.handleSkillPickerToggle()
	if !m.skillsSelectedMap[2] {
		t.Error("expected index 2 selected after toggle")
	}
	m.handleSkillPickerToggle()
	if m.skillsSelectedMap[2] {
		t.Error("expected index 2 unselected after second toggle")
	}
}

func TestDeleteSkillCmdNilInstaller(t *testing.T) {
	m := &Model{}
	cmd := m.deleteSkillCmd("foo")
	msg := cmd()
	res, ok := msg.(skillDeleteResultMsg)
	if !ok {
		t.Fatalf("expected skillDeleteResultMsg, got %T", msg)
	}
	if res.err == nil {
		t.Error("expected error when installer nil")
	}
}

func TestHandleSkillScanResultError(t *testing.T) {
	m := &Model{}
	m.handleSkillScanResult(skillsScanResultMsg{repo: "r", err: errForce("boom")})
	if m.formError == "" {
		t.Error("expected formError on scan error")
	}
}

func TestHandleSkillScanResultNoSkills(t *testing.T) {
	m := &Model{}
	m.handleSkillScanResult(skillsScanResultMsg{repo: "r"})
	if m.formError == "" {
		t.Error("expected formError when no skills found")
	}
}

func TestHandleSkillScanResultSingle(t *testing.T) {
	m := &Model{}
	cmd := m.handleSkillScanResult(skillsScanResultMsg{repo: "r", skills: []channels.ScannedSkill{{Path: "skills/a"}}})
	if cmd == nil {
		t.Fatal("expected cmd for single skill")
	}
	if m.modalMode != ModalNone {
		// stays in whatever mode
	}
	if len(m.skillsScanResults) != 1 {
		t.Errorf("expected 1 scan result, got %d", len(m.skillsScanResults))
	}
	msg := cmd()
	res, ok := msg.(skillsInstallResultMsg)
	if !ok {
		t.Fatalf("expected skillsInstallResultMsg, got %T", msg)
	}
	if res.err == nil {
		t.Error("expected error when installer nil")
	}
}

func TestHandleSkillScanResultMultiple(t *testing.T) {
	m := &Model{}
	m.handleSkillScanResult(skillsScanResultMsg{repo: "r", skills: []channels.ScannedSkill{{Name: "a"}, {Name: "b"}}})
	if m.modalMode != ModalSkillPicker {
		t.Errorf("expected ModalSkillPicker, got %v", m.modalMode)
	}
	if len(m.skillsSelectedMap) != 2 || !m.skillsSelectedMap[0] || !m.skillsSelectedMap[1] {
		t.Errorf("expected all selected, got %v", m.skillsSelectedMap)
	}
}

func TestHandleSkillInstallResultError(t *testing.T) {
	m := &Model{}
	m.handleSkillInstallResult(skillsInstallResultMsg{err: errForce("fail")})
	if m.formError == "" {
		t.Error("expected formError on install error")
	}
}

func TestHandleSkillInstallResultNoInstalled(t *testing.T) {
	m := &Model{}
	m.handleSkillInstallResult(skillsInstallResultMsg{})
	if m.skillsFeedback == "" {
		t.Error("expected feedback for no new skills")
	}
	if m.modalMode != ModalSkills {
		t.Errorf("expected ModalSkills, got %v", m.modalMode)
	}
}

func TestHandleSkillInstallResultSomeInstalled(t *testing.T) {
	m := &Model{}
	m.handleSkillInstallResult(skillsInstallResultMsg{installed: []string{"a", "b"}})
	if m.skillsFeedback == "" {
		t.Error("expected feedback")
	}
}

func TestHandleSkillToggleResultError(t *testing.T) {
	m := &Model{}
	m.handleSkillToggleResult(skillToggleResultMsg{skillName: "s", err: errForce("x")})
	if m.skillsFeedback == "" {
		t.Error("expected feedback on toggle error")
	}
}

func TestHandleSkillToggleResultEnabled(t *testing.T) {
	m := &Model{}
	m.handleSkillToggleResult(skillToggleResultMsg{skillName: "s", enabled: true})
	if m.skillsFeedback == "" {
		t.Error("expected feedback on toggle success")
	}
}

func TestHandleSkillToggleResultDisabled(t *testing.T) {
	m := &Model{}
	m.handleSkillToggleResult(skillToggleResultMsg{skillName: "s", enabled: false})
	if m.skillsFeedback == "" {
		t.Error("expected feedback on toggle disable")
	}
}

func TestHandleSkillDeleteResultError(t *testing.T) {
	m := &Model{}
	m.handleSkillDeleteResult(skillDeleteResultMsg{skillName: "s", err: errForce("x")})
	if m.skillsFeedback == "" {
		t.Error("expected feedback on delete error")
	}
}

func TestHandleSkillDeleteResultSuccess(t *testing.T) {
	m := &Model{}
	m.handleSkillDeleteResult(skillDeleteResultMsg{skillName: "s"})
	if m.skillsFeedback == "" {
		t.Error("expected feedback on delete success")
	}
}

func TestHandleSkillInstallResultResetsState(t *testing.T) {
	m := &Model{}
	m.skillsScanResults = []channels.ScannedSkill{{Name: "a"}}
	m.handleSkillInstallResult(skillsInstallResultMsg{installed: []string{"a"}})
	if m.skillsScanResults != nil || m.skillsSelectedMap != nil {
		t.Error("expected scan results and selection map cleared")
	}
}

func TestSkillsCmdChains(t *testing.T) {
	// Ensure commands from toggle/delete return the right message types.
	m := &Model{}
	c1 := m.toggleSkillCmd("x")
	if _, ok := c1().(skillToggleResultMsg); !ok {
		t.Error("toggle cmd should produce skillToggleResultMsg")
	}
	c2 := m.deleteSkillCmd("y")
	if _, ok := c2().(skillDeleteResultMsg); !ok {
		t.Error("delete cmd should produce skillDeleteResultMsg")
	}
}

var _ = time.Now
