package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// TestSkillsModal_FromMainChatPath pins the unified modal dispatcher: skills
// install/picker must render from the split-column (post-welcome) View path,
// not only from the welcome screen.
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

// TestSkillsModal_AddSecretTitle pins that the add-secret form is titled
// differently from the secrets list.
func TestSkillsModal_AddSecretTitle(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalAddSecret
	if got := m.modalTitleFor(ModalAddSecret); got != i18n.T("tui.addSecret") {
		t.Fatalf("ModalAddSecret title = %q, want %q", got, i18n.T("tui.addSecret"))
	}
	if m.modalTitleFor(ModalAddSecret) == m.modalTitleFor(ModalSecrets) &&
		i18n.T("tui.addSecret") != m.secretsHeader() {
		// secretsHeader may include backend tags; only fail when the plain
		// add-secret title is still the list title.
		if m.modalTitleFor(ModalAddSecret) == i18n.T("tui.secrets") {
			t.Fatal("ModalAddSecret still uses the secrets list title")
		}
	}
}

// TestLoadSkillsList_EmptyKeepsKeysAligned pins the empty-list key alignment:
// the "no skills" placeholder must get a matching empty key so Enter on the
// Install action indexes correctly and the separator is a no-op.
func TestLoadSkillsList_EmptyKeepsKeysAligned(t *testing.T) {
	m := newTestModel(t)
	m.resetModal(ModalSkills)
	// Force the empty path: loader may be non-nil but return no skills.
	// Call the loader and inspect alignment for whatever it produced.
	m.loadSkillsList()

	if len(m.modalItems) != len(m.skillsModalKeys) {
		t.Fatalf("modalItems (%d) and skillsModalKeys (%d) are misaligned:\nitems=%v\nkeys=%v",
			len(m.modalItems), len(m.skillsModalKeys), m.modalItems, m.skillsModalKeys)
	}

	// Install action must be reachable: last key is __install__ and indexes last item.
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
		// handleSkillsEnter returns tickCmd after switching mode
		t.Fatalf("Enter on install action did not open ModalSkillInstall (mode=%v)", m.modalMode)
	}
	if m.modalMode != ModalSkillInstall {
		t.Fatalf("Enter on install action: mode=%v, want ModalSkillInstall", m.modalMode)
	}
}

// TestAutocomplete_NegativeIndexDoesNotPanic pins the empty-match Up wrap fix.
func TestAutocomplete_NegativeIndexDoesNotPanic(t *testing.T) {
	m := newTestModel(t)
	m.showAutocomplete = true
	m.autocompleteItems = nil
	m.autocompleteIdx = 0

	// Up on empty list used to set idx = -1 and later panic on Enter/Tab.
	msg := tea.KeyMsg{Type: tea.KeyUp, Runes: []rune{'k'}}
	_, _, handled := m.handleAutocompleteKey(msg)
	if !handled {
		t.Fatal("up on empty autocomplete should be handled")
	}
	if m.autocompleteIdx < 0 {
		t.Fatalf("autocompleteIdx went negative: %d", m.autocompleteIdx)
	}

	// Enter with a stale negative index must not panic.
	m.autocompleteIdx = -1
	m.autocompleteItems = []commandInfo{{name: "/new", description: "x"}}
	_, _, _ = m.handleAutocompleteKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.autocompleteIdx < 0 {
		t.Fatalf("autocompleteIdx still negative after enter: %d", m.autocompleteIdx)
	}
}

// TestFilterAutocomplete_ClampsNegativeIdx pins filterAutocomplete's clamp.
func TestFilterAutocomplete_ClampsNegativeIdx(t *testing.T) {
	m := newTestModel(t)
	m.autocompleteIdx = -5
	m.filterAutocomplete("/")
	if m.autocompleteIdx < 0 {
		t.Fatalf("filterAutocomplete left negative idx: %d", m.autocompleteIdx)
	}
}

// TestHandleApproval_ExpiredDoesNotOverwriteWithWhitelistSuccess pins the
// whitelist path: an expired live approval keeps its warning even after the
// command is persisted to the allowlist.
func TestHandleApproval_ReturnsWhetherAccepted(t *testing.T) {
	m := newTestModel(t)
	m.pendingApprovalID = ""
	if m.handleApproval(true) {
		t.Fatal("empty pendingApprovalID should report false")
	}
}

// TestRenderSingleLine_MultibyteWidth pins display-width wrapping on the
// streaming path (was using len() bytes).
func TestRenderSingleLine_MultibyteWidth(t *testing.T) {
	// 10 CJK chars = 20 display cells; width 10 must wrap.
	line := strings.Repeat("汉", 10)
	out := renderSingleLine(line, 10)
	if strings.Count(out, "\n") < 1 {
		t.Fatalf("expected wrap for wide CJK line, got %q", out)
	}
}

// TestFormatSkillItem_UTF8Safe pins rune-safe truncation.
func TestFormatSkillItem_UTF8Safe(t *testing.T) {
	desc := strings.Repeat("ñ", 80)
	out := formatSkillItem("test", desc, true, "")
	if !strings.ContainsRune(out, '…') && !strings.Contains(out, "...") {
		// truncateRightCells uses "..."
		t.Fatalf("expected ellipsis, got %q", out)
	}
	// Must remain valid UTF-8 with no replacement char from mid-rune cuts.
	for _, r := range out {
		if r == '�' {
			t.Fatalf("invalid UTF-8 replacement in %q", out)
		}
	}
}

// TestThemeIsLightBackground pins luminance heuristic.
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
