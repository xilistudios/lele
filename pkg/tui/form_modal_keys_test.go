package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestQKeyTypesIntoFormModal(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/connect")
	if m.modalMode != ModalAddProvider {
		t.Fatalf("expected ModalAddProvider, got %v", m.modalMode)
	}
	m = pickFreeTypeRow(t, m)
	if m.formStepIndex != 0 {
		t.Fatalf("expected the name step, got %d", m.formStepIndex)
	}

	m = sendKeys(m, "q")
	if m.modalMode != ModalAddProvider {
		t.Fatalf("'q' closed the form modal; modal = %v", m.modalMode)
	}
	if got := m.textInput.Value(); got != "q" {
		t.Fatalf("'q' not inserted into text input, got %q", got)
	}

	m = sendKeys(m, "\x1b")
	if m.modalMode != ModalNone {
		t.Fatalf("ESC did not close the form modal; modal = %v", m.modalMode)
	}
}

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

	m = sendKeys(m, "\x1b")
	if m.modalMode != ModalSettingsTUI {
		t.Fatalf("ESC should return to the settings list, got modal %v", m.modalMode)
	}
	if m.settingsEditField != "" {
		t.Fatalf("ESC should clear settingsEditField, got %q", m.settingsEditField)
	}
}

func TestQKeyStillClosesNonFormModals(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalSessions
	m = sendKeys(m, "q")
	if m.modalMode != ModalNone {
		t.Fatalf("'q' should close list modals, got modal %v", m.modalMode)
	}
}

func TestWrapText_CJKVisualWidth(t *testing.T) {
	input := "\u65e5\u672c\u8a9e \u65e5\u672c\u8a9e \u65e5\u672c\u8a9e"
	limit := 7
	got := wrapText(input, limit)

	for i, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w > limit {
			t.Fatalf("line %d exceeds visual limit: width %d > %d (%q)", i, w, limit, line)
		}
	}

	if lines := strings.Split(got, "\n"); len(lines) != 3 {
		t.Fatalf("expected 3 wrapped lines, got %d (%q)", len(lines), got)
	}
}

func TestWrapText_EmojiVisualWidth(t *testing.T) {
	input := "🦞🦞🦞 🦞🦞🦞"
	got := wrapText(input, 6)
	for i, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w > 6 {
			t.Fatalf("line %d exceeds visual limit: width %d > 6 (%q)", i, w, line)
		}
	}
}

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

func TestQKeyTypesIntoConnectOAuthModal(t *testing.T) {
	m, _ := connectOAuthEnterPending(t, "anthropic")
	if m.modalMode != ModalConnectOAuth {
		t.Fatalf("modal = %v, want ModalConnectOAuth", m.modalMode)
	}
	if m.connectOAuthState != connectOAuthPending {
		t.Fatalf("state = %d, want connectOAuthPending", m.connectOAuthState)
	}

	m = sendKeys(m, "q")
	if m.modalMode != ModalConnectOAuth {
		t.Fatalf("'q' closed the OAuth modal; modal = %v", m.modalMode)
	}
	if m.connectOAuthFlow == nil {
		t.Error("'q' dropped the live flow (the listener would leak)")
	}
	if got := m.textInput.Value(); got != "q" {
		t.Fatalf("'q' not inserted into the paste box, got %q", got)
	}
}
