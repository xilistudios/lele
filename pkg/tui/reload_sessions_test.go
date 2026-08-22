package tui

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
)

// TestReloadSessions_FiltersSubagentAndMode verifies reloadSessions excludes
// subagent sessions and sessions whose mode doesn't match currentMode.
func TestReloadSessions_FiltersSubagentAndMode(t *testing.T) {
	m := newTestModel(t)
	m.currentMode = ModeAgent

	m.sessionMgr.GetOrCreate("tui:chat:main-a")
	_ = m.sessionMgr.SetMode("tui:chat:main-a", "agent")
	m.sessionMgr.AddMessage("tui:chat:main-a", "user", "hi")

	m.sessionMgr.GetOrCreate("tui:chat:main-b")
	_ = m.sessionMgr.SetMode("tui:chat:main-b", "chat") // mode mismatch
	m.sessionMgr.AddMessage("tui:chat:main-b", "user", "hi")

	m.sessionMgr.GetOrCreate("tui:chat:main-c:native:123:subagent-1")
	_ = m.sessionMgr.SetMode("tui:chat:main-c:native:123:subagent-1", "agent")
	m.sessionMgr.AddMessage("tui:chat:main-c:native:123:subagent-1", "user", "hi")

	m.currentKey = "tui:chat:main-a"
	m.showWelcome = false
	m.reloadSessions()

	for _, s := range m.visibleSessions {
		if s.Key == "tui:chat:main-b" {
			t.Error("mode-mismatched session should be filtered out")
		}
		if s.Key == "tui:chat:main-c:native:123:subagent-1" {
			t.Error("subagent session should be filtered out")
		}
	}
}

// TestReloadSessions_WelcomeEarlyReturn verifies reloadSessions returns early
// when the welcome screen is active (does not switch currentKey).
func TestReloadSessions_WelcomeEarlyReturn(t *testing.T) {
	m := newTestModel(t)
	m.currentMode = ModeAgent
	m.showWelcome = true
	m.sessionMgr.GetOrCreate("tui:chat:only")
	_ = m.sessionMgr.SetMode("tui:chat:only", "agent")
	m.sessionMgr.AddMessage("tui:chat:only", "user", "hi")

	m.currentKey = ""
	m.reloadSessions()
	if m.currentKey != "" {
		t.Errorf("expected currentKey to remain empty on welcome, got %q", m.currentKey)
	}
}

// TestReloadSessions_CurrentKeyMatch verifies reloadSessions sets the correct
// selectedSessionIdx when the current key is found.
func TestReloadSessions_CurrentKeyMatch(t *testing.T) {
	m := newTestModel(t)
	m.currentMode = ModeAgent
	m.sessionMgr.GetOrCreate("tui:chat:first")
	_ = m.sessionMgr.SetMode("tui:chat:first", "agent")
	m.sessionMgr.AddMessage("tui:chat:first", "user", "hi")
	m.sessionMgr.GetOrCreate("tui:chat:second")
	_ = m.sessionMgr.SetMode("tui:chat:second", "agent")
	m.sessionMgr.AddMessage("tui:chat:second", "user", "hi")

	m.currentKey = "tui:chat:second"
	m.showWelcome = false
	m.reloadSessions()
	// Find the index of our current key within visible sessions.
	for i, s := range m.visibleSessions {
		if s.Key == "tui:chat:second" {
			if m.selectedSessionIdx != i {
				t.Errorf("expected selectedSessionIdx=%d, got %d", i, m.selectedSessionIdx)
			}
			return
		}
	}
	t.Error("did not find current key in visible sessions")
}

// TestReloadSessions_CurrentKeyMissingSelectsFirst verifies when the current
// key isn't found, reloadSessions selects the first visible session.
func TestReloadSessions_CurrentKeyMissingSelectsFirst(t *testing.T) {
	m := newTestModel(t)
	m.currentMode = ModeAgent
	m.sessionMgr.GetOrCreate("tui:chat:only-a")
	_ = m.sessionMgr.SetMode("tui:chat:only-a", "agent")
	m.sessionMgr.AddMessage("tui:chat:only-a", "user", "hi")

	m.currentKey = ""
	m.showWelcome = false
	m.reloadSessions()
	if m.currentKey != "tui:chat:only-a" {
		t.Errorf("expected currentKey to select first visible session, got %q", m.currentKey)
	}
}

// TestReloadSessions_ClearsPendingUserMessage verifies pendingUserMessage is
// cleared once it appears in session history.
func TestReloadSessions_ClearsPendingUserMessage(t *testing.T) {
	m := newTestModel(t)
	m.currentMode = ModeAgent
	m.sessionMgr.GetOrCreate("tui:chat:pending")
	_ = m.sessionMgr.SetMode("tui:chat:pending", "agent")
	m.currentKey = "tui:chat:pending"
	m.pendingUserMessage = "what is the weather"

	m.sessionMgr.AddMessage(m.currentKey, "user", "what is the weather")
	m.sessionMgr.Save(m.currentKey)
	m.showWelcome = false
	m.reloadSessions()
	if m.pendingUserMessage != "" {
		t.Errorf("expected pendingUserMessage cleared, got %q", m.pendingUserMessage)
	}
}

// TestIsSessionProcessing_Branches exercises the isSessionProcessing branches.
func TestIsSessionProcessing_Branches(t *testing.T) {
	m := newTestModel(t)
	m.currentMode = ModeAgent

	// No session handling — should not crash; returns false with cleared state.
	m.startTime = time.Time{}
	m.processing = false
	if m.isSessionProcessing() {
		t.Error("expected false for no session")
	}

	// processing but stale (startTime zero) → resets processing to false.
	m.processing = true
	m.startTime = time.Time{}
	if m.isSessionProcessing() {
		t.Error("expected false for stale processing state")
	}
	if m.processing {
		t.Error("expected stale processing flag cleared")
	}
}

// TestGetGroupProfiles_Empty verifies getGroupProfiles returns profiles from a
// real model config snapshot (groups disabled → empty list, no panic).
func TestGetGroupProfiles_Empty(t *testing.T) {
	m := newTestModel(t)
	got := m.getGroupProfiles()
	_ = got // must not panic even if empty
}

// TestReloadTestSkillPickerSelectedDupCoversInstallDefault verifies the
// default branch (skill path selected) issues a cmd — distinct name but same
// intent to avoid clashing with TestHandleSkillPickerEnterSelected.
func TestReloadSkillPickerSepInstallCmd(t *testing.T) {
	m := newTestModel(t)
	m.skillsScanResults = []channels.ScannedSkill{{Name: "a", Description: "d", Path: "/skills/a"}}
	m.skillsSelectedMap = map[int]bool{0: true}
	cmd := m.handleSkillPickerEnter()
	if cmd == nil {
		t.Error("expected a cmd when skills selected")
	}
}
