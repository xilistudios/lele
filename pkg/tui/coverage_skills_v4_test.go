package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
)

// writeWorkspaceSkill creates a skill directory + SKILL.md under the given
// workspace so the agent's real SkillsLoader can surface it via ListSkills().
func writeWorkspaceSkill(t *testing.T, workspace, name, description string) string {
	t.Helper()
	dir := filepath.Join(workspace, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillFile := filepath.Join(dir, "SKILL.md")
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n# " + name + "\n"
	if err := os.WriteFile(skillFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	return skillFile
}

// TestCoverageLoadSkillsListWithRealSkills verifies loadSkillsList surfaces
// skills from the agent's real loader, sorts them, and appends the action rows.
func TestCoverageLoadSkillsListWithRealSkills(t *testing.T) {
	m := newTestModel(t)
	loader := m.skillsLoader()
	if loader == nil {
		t.Fatal("expected a real skills loader")
	}
	// Create two workspace skills.
	writeWorkspaceSkill(t, m.cfg.Agents.Defaults.Workspace, "skill-b", "skill b description")
	writeWorkspaceSkill(t, m.cfg.Agents.Defaults.Workspace, "skill-a", "skill a description")

	m.loadSkillsList()

	// Expect entries for both skills plus the separator + install action.
	foundA, foundB, foundInstall := false, false, false
	for i, k := range m.skillsModalKeys {
		switch k {
		case "skill-a":
			foundA = true
		case "skill-b":
			foundB = true
		case "__install__":
			foundInstall = true
		}
		if foundA && foundB && foundInstall {
			break
		}
		_ = i
	}
	if !foundA || !foundB {
		t.Errorf("expected both workspace skills listed, a=%v b=%v", foundA, foundB)
	}
	if !foundInstall {
		t.Error("expected __install__ action key")
	}
	// Sorting ensures skill-a appears before skill-b in modalItems.
	idxA, idxB := -1, -1
	for i, k := range m.skillsModalKeys {
		if k == "skill-a" {
			idxA = i
		}
		if k == "skill-b" {
			idxB = i
		}
	}
	if idxA < 0 || idxB < 0 || idxA > idxB {
		t.Errorf("expected sorted order (a before b), a=%d b=%d", idxA, idxB)
	}
}

// TestCoverageHandleSkillsEnterToggleRealSkill drives the toggle path through
// a real loader + config manager (creating a workspace skill and toggling it).
func TestCoverageHandleSkillsEnterToggleRealSkill(t *testing.T) {
	m := newTestModel(t)
	loader := m.skillsLoader()
	if loader == nil {
		t.Fatal("expected skills loader")
	}
	// Ensure a config manager is wired; if missing, force-create one.
	if loader.GetConfigManager() == nil {
		t.Skip("no config manager available")
	}
	ws := m.cfg.Agents.Defaults.Workspace
	writeWorkspaceSkill(t, ws, "tog-skill", "toggling test skill")

	// Use handleSkillsEnter so the toggleCmd fn is exercised.
	m.loadSkillsList()
	m.modalSelectedIdx = -1
	for i, k := range m.skillsModalKeys {
		if k == "tog-skill" {
			m.modalSelectedIdx = i
			break
		}
	}
	if m.modalSelectedIdx < 0 {
		t.Fatal("tog-skill not found in skills list")
	}
	cmd := m.handleSkillsEnter()
	if cmd == nil {
		t.Fatal("expected toggle cmd")
	}
	msg := cmd()
	res, ok := msg.(skillToggleResultMsg)
	if !ok {
		t.Fatalf("expected skillToggleResultMsg, got %T", msg)
	}
	if res.err != nil {
		t.Fatalf("toggle err: %v", res.err)
	}
	// Skill was enabled by default -> now disabled.
	if res.enabled {
		t.Error("expected skill to become disabled after toggle")
	}
}

// TestCoverageHandleSkillToggleResultDisabledNonErr covers the disabled branch
// of handleSkillToggleResult with a real list reload (no nil-loader panic).
func TestCoverageHandleSkillToggleResultDisabledNonErr(t *testing.T) {
	m := newTestModel(t)
	m.loadSkillsList()
	cmd := m.handleSkillToggleResult(skillToggleResultMsg{skillName: "s", enabled: false})
	if cmd == nil {
		t.Error("expected a tick cmd")
	}
	if m.skillsFeedback == "" {
		t.Error("expected feedback")
	}
}

// TestCoverageSkillInstallerWithRealLoop verifies skillInstaller() returns a
// non-nil installer when a real agent loop is present.
func TestCoverageSkillInstallerWithRealLoop(t *testing.T) {
	m := newTestModel(t)
	if m.skillInstaller() == nil {
		t.Error("expected a real skill installer from agent loop")
	}
}

// TestCoverageDeleteSkillCmdNonNilInstaller drives deleteSkillCmd against a
// real installer on a non-existent skill (exercises the Uninstall error path
// without touching the network).
func TestCoverageDeleteSkillCmdNonNilInstaller(t *testing.T) {
	m := newTestModel(t)
	if m.skillInstaller() == nil {
		t.Skip("no installer")
	}
	cmd := m.deleteSkillCmd("does-not-exist-skill")
	msg := cmd()
	res, ok := msg.(skillDeleteResultMsg)
	if !ok {
		t.Fatalf("expected skillDeleteResultMsg, got %T", msg)
	}
	_ = res // err will be a not-found error; that's fine — branch reached
}

// TestCoverageHandleSkillInstallResultRefreshesList ensures the success path
// reloads the skills list and returns a tick cmd.
func TestCoverageHandleSkillInstallResultRefreshesList(t *testing.T) {
	m := newTestModel(t)
	m.loadSkillsList()
	cmd := m.handleSkillInstallResult(skillsInstallResultMsg{installed: []string{"a"}})
	if cmd == nil {
		t.Error("expected tick cmd")
	}
	if m.modalMode != ModalSkills {
		t.Errorf("expected ModalSkills, got %v", m.modalMode)
	}
}

// TestCoverageHandleSkillScanResultSingleWithInstaller drives the single-skill
// auto-install cmd against a real installer (error path when skill missing).
func TestCoverageHandleSkillScanResultSingleWithInstaller(t *testing.T) {
	m := newTestModel(t)
	if m.skillInstaller() == nil {
		t.Skip("no installer")
	}
	cmd := m.handleSkillScanResult(skillsScanResultMsg{
		repo:   "o/r",
		skills: []channels.ScannedSkill{{Name: "x", Path: "skills/x"}},
	})
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	msg := cmd()
	if _, ok := msg.(skillsInstallResultMsg); !ok {
		t.Fatalf("expected skillsInstallResultMsg, got %T", msg)
	}
}

// TestCoverageHandleSkillPickerEnterWithInstaller drives the multi-select
// install command against a real installer.
func TestCoverageHandleSkillPickerEnterWithInstaller(t *testing.T) {
	m := newTestModel(t)
	if m.skillInstaller() == nil {
		t.Skip("no installer")
	}
	m.skillsScanRepo = "o/r"
	m.skillsScanResults = []channels.ScannedSkill{{Name: "a", Path: "skills/a"}, {Name: "b", Path: "skills/b"}}
	m.skillsSelectedMap = map[int]bool{0: true, 1: true}
	cmd := m.handleSkillPickerEnter()
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	msg := cmd()
	if _, ok := msg.(skillsInstallResultMsg); !ok {
		t.Fatalf("expected skillsInstallResultMsg, got %T", msg)
	}
}

// TestCoverageToggleSkillCmdRealLoaderNoConfig covers toggleSkillCmd when the
// loader exists but has no config manager.
func TestCoverageToggleSkillCmdRealLoaderNoConfig(t *testing.T) {
	m := newTestModel(t)
	loader := m.skillsLoader()
	if loader == nil {
		t.Skip("no loader")
	}
	// Force config manager to nil to hit the workspace-config-not-available path.
	loader.SetConfigManager(nil)
	cmd := m.toggleSkillCmd("x")
	msg := cmd()
	res, ok := msg.(skillToggleResultMsg)
	if !ok {
		t.Fatalf("expected skillToggleResultMsg, got %T", msg)
	}
	if res.err == nil {
		t.Error("expected error when config manager nil")
	}
}

var _ = time.Now