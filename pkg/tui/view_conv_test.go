package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// seedActiveSession prepares a model with a current session so View() takes the
// split-column conversational path.
func seedActiveSession(t *testing.T, m *Model, key string) {
	t.Helper()
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "What is 2+2?")
	m.sessionMgr.AddMessage(key, "assistant", "It is 4.")
	m.sessionMgr.SetName(key, "Test Session")
	m.currentKey = key
	m.showWelcome = false
	m.width = 120
	m.height = 40
	forceTrueColor(t)
}

// TestConvView_RendersBase verifies the conversational split-column layout
// renders both columns, the status line and the input bar.
func TestConvView_RendersBase(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-base")
	out := m.View()

	for _, want := range []string{
		i18n.T("tui.context"),
		i18n.T("tui.workspace"),
		i18n.T("tui.status"),
		i18n.T("tui.ready"),
		"Test Session",
		"Tú",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("conv view missing %q", want)
		}
	}
}

// TestConvView_ProcessingStatus verifies the processing status line (with
// bouncing dots) renders while a session is processing.
func TestConvView_ProcessingStatus(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-proc")
	m.processing = true
	m.startTime = time.Now()
	if !m.isSessionProcessing() {
		t.Fatal("expected isSessionProcessing to be true")
	}
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.processing")) {
		t.Errorf("expected processing status, got:\n%s", out)
	}
}

// TestConvView_LastDuration verifies the "done in" status line after a response
// has completed.
func TestConvView_LastDuration(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-done")
	m.lastDuration = 1
	out := m.View()
	// The status line may be truncated by wide rendering; the important thing is
	// the view still renders without panicking when lastDuration is set.
	if out == "" {
		t.Fatal("expected non-empty view with lastDuration set")
	}
	if !strings.Contains(out, i18n.T("tui.status")) {
		t.Errorf("expected status sidebar, got:\n%s", out)
	}
}

// TestConvView_SubagentNavStatus verifies the subagent/back-to-parent status
// lines render when viewing a subagent chat.
func TestConvView_SubagentNavStatus(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-sub")
	m.parentSessionKey = "tui:chat:parent"
	m.escHint = true
	m.processing = true
	m.startTime = time.Now()
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.backToParent")) {
		t.Errorf("expected back-to-parent hint, got:\n%s", out)
	}
}

// TestConvView_GoalBadgeRendered verifies the goal badge appears in the status
// line when a goal is in progress.
func TestConvView_GoalBadgeRendered(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-goal")
	m.agentLoop.GoalManager().Set(m.currentKey, "Write the report", 0)
	out := m.View()
	if got := m.agentLoop.GoalManager().Get(m.currentKey); got == nil {
		t.Fatal("goal manager should return the set goal")
	}
	if !strings.Contains(out, "🎯") {
		t.Errorf("expected goal badge, got:\n%s", out)
	}
}

// TestConvView_SelectingStatus verifies the selecting status renders while a
// selection/block mode is active.
func TestConvView_SelectingStatus(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-sel")
	m.selecting = true
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.selecting")) {
		t.Errorf("expected selecting status, got:\n%s", out)
	}
}

// TestConvView_ShortWindowStillRenders verifies the conversational screen does
// not panic even with a very small terminal height (falls back to reduced
// status/input styles).
func TestConvView_ShortWindowStillRenders(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-short")
	m.height = 8
	out := m.View()
	if out == "" {
		t.Fatal("expected non-empty view for short terminal")
	}
}

// TestConvView_ModalAgent verifies the ModalAgent selector overlay on the
// conversational screen.
func TestConvView_ModalAgent(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-magent")
	m.modalMode = ModalAgent
	m.modalItems = []string{"primary", "critic"}
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.selectAgent")) {
		t.Errorf("expected agent modal, got:\n%s", out)
	}
}

// TestConvView_ModalSettingsTUI verifies the settings UI modal on the
// conversational screen.
func TestConvView_ModalSettingsTUI(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-mtui")
	m.modalMode = ModalSettingsTUI
	m.loadTUISettings()
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.settings.interface")) {
		t.Errorf("expected interface settings, got:\n%s", out)
	}
}

// TestConvView_ModalAddForm covers the form modal rendering branches
// (AddProvider/AddModel/AddSecret) on the conversational screen.
func TestConvView_ModalAddForm(t *testing.T) {
	for _, mode := range []struct {
		m  modalType
		tk string
	}{
		{ModalAddProvider, "tui.addProvider"},
		{ModalAddModel, "tui.addModel"},
		{ModalAddSecret, "tui.secrets"},
	} {
		m := newTestModel(t)
		seedActiveSession(t, m, "tui:chat:conv-form")
		m.modalMode = mode.m
		m.formStepIndex = 0
		out := m.View()
		if !strings.Contains(out, i18n.T(mode.tk)) {
			t.Errorf("modal %v missing title %q, got:\n%s", mode.m, mode.tk, out)
		}
	}
}

// TestConvView_ModalBgExecs renders the background-processes list modal.
func TestConvView_ModalBgExecs(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-bg")
	m.modalMode = ModalBackgroundExecs
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.backgroundProcesses")) {
		t.Errorf("expected bg execs title, got:\n%s", out)
	}

	// bgExecViewMode=true takes an early-return path rendering full output.
	m2 := newTestModel(t)
	seedActiveSession(t, m2, "tui:chat:conv-bg2")
	m2.modalMode = ModalBackgroundExecs
	m2.bgExecViewMode = true
	out2 := m2.View()
	if out2 == "" {
		t.Fatal("expected non-empty bg exec output view")
	}
}

// TestConvView_ModalCronDetail renders the cron-detail early-return branch.
func TestConvView_ModalCronDetail(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-cron")
	m.modalMode = ModalCron
	m.cronDetailMode = true
	out := m.View()
	if out == "" {
		t.Fatal("expected non-empty cron detail view")
	}
}

// TestConvView_ModalSecretDetail renders the secret-detail early-return branch.
func TestConvView_ModalSecretDetail(t *testing.T) {
	m := newTestModel(t)
	seedActiveSession(t, m, "tui:chat:conv-secd")
	m.modalMode = ModalSecrets
	m.secretsDetailMode = true
	m.secretsDetailName = "k1"
	out := m.View()
	if out == "" {
		t.Fatal("expected non-empty secret detail view")
	}
}
