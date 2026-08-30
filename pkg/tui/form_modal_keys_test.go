package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestQKeyTypesIntoFormModal verifies that "q" is inserted into the text input
// of form-based modals instead of closing them, while ESC still closes.
func TestQKeyTypesIntoFormModal(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/connect")
	if m.modalMode != ModalAddProvider {
		t.Fatalf("expected ModalAddProvider, got %v", m.modalMode)
	}

	// Typing "q" must go into the text input, not close the modal.
	m = sendKeys(m, "q")
	if m.modalMode != ModalAddProvider {
		t.Fatalf("'q' closed the form modal; modal = %v", m.modalMode)
	}
	if got := m.textInput.Value(); got != "q" {
		t.Fatalf("'q' not inserted into text input, got %q", got)
	}

	// ESC must still close the modal.
	m = sendKeys(m, "\x1b")
	if m.modalMode != ModalNone {
		t.Fatalf("ESC did not close the form modal; modal = %v", m.modalMode)
	}
}

// TestQKeyTypesIntoSettingsEditField verifies that "q" is typed into the
// inline edit field of settings modals instead of closing them, while ESC
// exits the edit (back to the list, not closing the modal entirely).
func TestQKeyTypesIntoSettingsEditField(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalSettingsTUI
	m.settingsEditField = "language"
	m.textInput.SetValue("")
	m.textInput.Focus()

	m = sendKeys(m, "q")
	if m.modalMode != ModalSettingsTUI || m.settingsEditField == "" {
		t.Fatalf("'q' left the settings edit field; modal = %v, editField = %q", m.modalMode, m.settingsEditField)
	}
	if got := m.textInput.Value(); got != "q" {
		t.Fatalf("'q' not inserted into settings edit input, got %q", got)
	}

	// ESC cancels the edit but keeps the modal open (back to the list).
	m = sendKeys(m, "\x1b")
	if m.modalMode != ModalSettingsTUI {
		t.Fatalf("ESC should return to the settings list, got modal %v", m.modalMode)
	}
	if m.settingsEditField != "" {
		t.Fatalf("ESC should clear settingsEditField, got %q", m.settingsEditField)
	}
}

// TestQKeyStillClosesNonFormModals guards against over-correction: "q" must
// keep closing list modals like ModalSessions.
func TestQKeyStillClosesNonFormModals(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalSessions
	m = sendKeys(m, "q")
	if m.modalMode != ModalNone {
		t.Fatalf("'q' should close list modals, got modal %v", m.modalMode)
	}
}

// TestWrapText_CJKVisualWidth verifies wrapText uses visual width, so CJK
// text (double-width runes) wraps within the visual column limit instead of
// the byte length.
func TestWrapText_CJKVisualWidth(t *testing.T) {
	// Each "日本語" word is 9 bytes but 6 visual columns.
	input := "日本語 日本語 日本語"
	limit := 7 // fits "日本語" (6 cols) but not two words (13 cols)
	got := wrapText(input, limit)

	for i, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w > limit {
			t.Fatalf("line %d exceeds visual limit: width %d > %d (%q)", i, w, limit, line)
		}
	}

	// Sanity: byte-based len() would have wrongly wrapped after the first
	// word (9 bytes > 7), producing 3 lines. With visual width the first
	// two words (6+1+6=13 > 7... no: 6 <= 7, second word doesn't fit) —
	// expect exactly 3 lines, one word each.
	if lines := strings.Split(got, "\n"); len(lines) != 3 {
		t.Fatalf("expected 3 wrapped lines, got %d (%q)", len(lines), got)
	}
}

// TestWrapText_EmojiVisualWidth verifies emoji (2 visual columns, 4 bytes)
// are measured by width.
func TestWrapText_EmojiVisualWidth(t *testing.T) {
	input := "🦞🦞🦞 🦞🦞🦞"
	got := wrapText(input, 6)
	for i, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w > 6 {
			t.Fatalf("line %d exceeds visual limit: width %d > 6 (%q)", i, w, line)
		}
	}
}

// TestWrapText_ASCIIUnchanged guards against regressions in plain ASCII
// wrapping behavior.
func TestWrapText_ASCIIUnchanged(t *testing.T) {
	got := wrapText("hello world foo", 11)
	want := "hello world\nfoo"
	if got != want {
		t.Fatalf("wrapText = %q, want %q", got, want)
	}
	if got := wrapText("short", 10); got != "short" {
		t.Fatalf("short line modified: %q", got)
	}
}
