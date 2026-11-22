package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func TestSkillsModal_FromMainChatPath(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = 120, 36
	m.showWelcome = false
	m.modalMode = ModalSkillInstall
	m.formStepIndex = 0

	out := m.View()
	if !strings.Contains(out, i18n.T("tui.installSkill")) {
		t.Fatalf("main-chat path missing skills install title. view:\n%s", out)
	}

	m.modalMode = ModalSkillPicker
	m.skillsScanRepo = "owner/repo"
	m.skillsScanResults = nil
	out = m.View()
	if !strings.Contains(out, i18n.T("tui.selectSkills")) {
		t.Fatalf("main-chat path missing skills picker title. view:\n%s", out)
	}

	m.modalMode = ModalSkills
	m.skillsFeedback = "installed: weather"
	m.modalItems = []string{"weather"}
	m.skillsModalKeys = []string{"weather"}
	out = m.View()
	if !strings.Contains(out, i18n.T("tui.skills")) {
		t.Fatalf("main-chat path missing skills list title. view:\n%s", out)
	}
	if !strings.Contains(out, "installed: weather") {
		t.Fatalf("skillsFeedback not rendered. view:\n%s", out)
	}
}

func TestSkillsModal_AddSecretTitle(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalAddSecret
	if got := m.modalTitleFor(ModalAddSecret); got != i18n.T("tui.addSecret") {
		t.Fatalf("ModalAddSecret title = %q, want %q", got, i18n.T("tui.addSecret"))
	}
	if m.modalTitleFor(ModalAddSecret) == m.modalTitleFor(ModalSecrets) &&
		i18n.T("tui.addSecret") != m.secretsHeader() {
		if m.modalTitleFor(ModalAddSecret) == i18n.T("tui.secrets") {
			t.Fatal("ModalAddSecret still uses the secrets list title")
		}
	}
}

func TestLoadSkillsList_EmptyKeepsKeysAligned(t *testing.T) {
	m := newTestModel(t)
	m.resetModal(ModalSkills)
	m.loadSkillsList()

	if len(m.modalItems) != len(m.skillsModalKeys) {
		t.Fatalf("modalItems (%d) and skillsModalKeys (%d) are misaligned:\nitems=%v\nkeys=%v",
			len(m.modalItems), len(m.skillsModalKeys), m.modalItems, m.skillsModalKeys)
	}

	if len(m.skillsModalKeys) == 0 {
		t.Fatal("expected at least the install action")
	}
	last := len(m.skillsModalKeys) - 1
	if m.skillsModalKeys[last] != "__install__" {
		t.Fatalf("last skills key = %q, want __install__", m.skillsModalKeys[last])
	}
	m.modalSelectedIdx = last
	cmd := m.handleSkillsEnter()
	if m.modalMode != ModalSkillInstall && cmd == nil {
		t.Fatalf("Enter on install action did not open ModalSkillInstall (mode=%v)", m.modalMode)
	}
	if m.modalMode != ModalSkillInstall {
		t.Fatalf("Enter on install action: mode=%v, want ModalSkillInstall", m.modalMode)
	}
}

func TestAutocomplete_NegativeIndexDoesNotPanic(t *testing.T) {
	m := newTestModel(t)
	m.showAutocomplete = true
	m.autocompleteItems = nil
	m.autocompleteIdx = 0

	msg := tea.KeyMsg{Type: tea.KeyUp, Runes: []rune{'k'}}
	_, _, handled := m.handleAutocompleteKey(msg)
	if !handled {
		t.Fatal("up on empty autocomplete should be handled")
	}
	if m.autocompleteIdx < 0 {
		t.Fatalf("autocompleteIdx went negative: %d", m.autocompleteIdx)
	}

	m.autocompleteIdx = -1
	m.autocompleteItems = []commandInfo{{name: "/new", description: "x"}}
	_, _, _ = m.handleAutocompleteKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.autocompleteIdx < 0 {
		t.Fatalf("autocompleteIdx still negative after enter: %d", m.autocompleteIdx)
	}
}

func TestFilterAutocomplete_ClampsNegativeIdx(t *testing.T) {
	m := newTestModel(t)
	m.autocompleteIdx = -5
	m.filterAutocomplete("/")
	if m.autocompleteIdx < 0 {
		t.Fatalf("filterAutocomplete left negative idx: %d", m.autocompleteIdx)
	}
}

func TestHandleApproval_ReturnsWhetherAccepted(t *testing.T) {
	m := newTestModel(t)
	m.pendingApprovalID = ""
	if m.handleApproval(true) {
		t.Fatal("empty pendingApprovalID should report false")
	}
}

func TestRenderSingleLine_MultibyteWidth(t *testing.T) {
	line := strings.Repeat("\u6C49", 10)
	out := renderSingleLine(line, 10)
	if strings.Count(out, "\n") < 1 {
		t.Fatalf("expected wrap for wide CJK line, got %q", out)
	}
}

func TestFormatSkillItem_UTF8Safe(t *testing.T) {
	desc := strings.Repeat("ñ", 80)
	out := formatSkillItem("test", desc, true, "")
	if !strings.ContainsRune(out, '…') && !strings.Contains(out, "...") {
		t.Fatalf("expected ellipsis, got %q", out)
	}
	for _, r := range out {
		if r == '�' {
			t.Fatalf("invalid UTF-8 replacement in %q", out)
		}
	}
}

func TestThemeIsLightBackground(t *testing.T) {
	if !themeIsLightBackground("#FFFFFF") {
		t.Fatal("#FFFFFF should be light")
	}
	if themeIsLightBackground("#000000") {
		t.Fatal("#000000 should be dark")
	}
	if themeIsLightBackground("#181824") {
		t.Fatal("dracula bg should be dark")
	}
}
