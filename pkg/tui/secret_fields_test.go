package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Tests for audit M2: API keys / secret values must never render in clear
// text in the /connect (ModalAddProvider) or add-secret (ModalAddSecret)
// forms — neither via the textinput widget echo, the current-step line, nor
// the completed-step / review lines.

func TestIsSecretInputStep(t *testing.T) {
	cases := []struct {
		mode  modalType
		step  int
		want  bool
		label string
	}{
		{ModalAddProvider, 2, true, "provider step 2 = API Key"},
		{ModalAddProvider, 1, false, "provider step 1 = type"},
		{ModalAddProvider, 3, false, "provider step 3 = api base"},
		{ModalAddProvider, 9, false, "provider review step"},
		{ModalAddSecret, 1, true, "secret step 1 = value"},
		{ModalAddSecret, 0, false, "secret step 0 = name"},
		{ModalAddSecret, 2, false, "secret step 2 = description"},
		{ModalAddModel, 1, false, "add-model has no secret steps"},
		{ModalSkillInstall, 2, false, "skill install has no secret steps"},
		{ModalSecrets, 1, false, "list modal never secret step"},
		{ModalNone, 2, false, "no modal"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			m := &Model{modalMode: tc.mode, formStepIndex: tc.step}
			if got := m.isSecretInputStep(); got != tc.want {
				t.Fatalf("isSecretInputStep(mode=%v step=%d) = %v, want %v", tc.mode, tc.step, got, tc.want)
			}
		})
	}
}

func TestIsSecretFormValue(t *testing.T) {
	cases := []struct {
		mode modalType
		step int
		want bool
	}{
		{ModalAddProvider, 2, true},
		{ModalAddProvider, 0, false},
		{ModalAddProvider, 3, false},
		{ModalAddSecret, 1, true},
		{ModalAddSecret, 0, false},
		{ModalAddModel, 1, false},
		{ModalNone, 2, false},
	}
	for _, tc := range cases {
		m := &Model{modalMode: tc.mode}
		if got := m.isSecretFormValue(tc.step); got != tc.want {
			t.Fatalf("isSecretFormValue(mode=%v step=%d) = %v, want %v", tc.mode, tc.step, got, tc.want)
		}
	}
}

func TestMaskSecretDisplay(t *testing.T) {
	if got := maskSecretDisplay(""); got != "" {
		t.Fatalf("empty must stay empty, got %q", got)
	}
	// Short secrets are fully hidden (maskSecretValue would echo the whole value).
	if got := maskSecretDisplay("abc123"); got != "••••••••" {
		t.Fatalf("short secret = %q, want 8 bullets", got)
	}
	if got := maskSecretDisplay("12345678"); got != "••••••••" {
		t.Fatalf("8-char secret = %q, want 8 bullets", got)
	}
	// Long secrets reuse the exact 4+8+4 policy of maskSecretValue.
	v := "sk-abcdefghijklmnopqrstuvwxyz"
	want := v[:4] + strings.Repeat("•", 8) + v[len(v)-4:]
	if got := maskSecretDisplay(v); got != want {
		t.Fatalf("long secret = %q, want %q", got, want)
	}
}

// newFormTestModel builds a sized model pinned to English, ready to render a
// form modal through the real View() path.
func newFormTestModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	i18n.InitWithLanguage("en") // NewModel may pick up the host locale
	up, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = up.(*Model)
	m.showWelcome = false
	return m
}

// TestFormModalHidesSecretTyping drives the real Update path: after entering
// /connect and landing on the API-key step, typed characters must switch the
// widget to password echo, and the rendered frame must not contain the raw
// value.
func TestFormModalHidesSecretTyping(t *testing.T) {
	m := newFormTestModel(t)

	m.executeCommand("/connect")
	if m.modalMode != ModalAddProvider || m.formStepIndex != 0 {
		t.Fatalf("expected /connect at step 0, got mode=%v step=%d", m.modalMode, m.formStepIndex)
	}
	// Provider name → type picker.
	m.textInput.SetValue("openai-test")
	m = sendKeys(m, "\r")
	m.providerTypePickerIdx = 0 // openai preset → lands on step 2 (API Key)
	m = sendKeys(m, "\r")
	if m.formStepIndex != 2 {
		t.Fatalf("expected API-key step 2, got %d", m.formStepIndex)
	}

	const rawKey = "skZmysecretskyvalue"
	m = sendKeys(m, "z", "m", "y", "s", "e", "c", "r", "e", "t")
	if m.textInput.Value() != "zmysecret" {
		t.Fatalf("forwarding broke input: %q", m.textInput.Value())
	}
	if m.textInput.EchoMode != textinput.EchoPassword {
		t.Fatalf("EchoMode = %v, want EchoPassword on API-key step", m.textInput.EchoMode)
	}

	out := m.View()
	if strings.Contains(out, rawKey) || strings.Contains(out, "zmysecret") {
		t.Fatalf("raw typed key leaked into render:\n%s", out)
	}
	if !strings.Contains(out, "•") {
		t.Fatalf("expected bullet mask in render:\n%s", out)
	}
}

// TestCompletedStepMasked: after advancing past the API-key step, the stored
// key must render masked in the completed-step line.
func TestCompletedStepMasked(t *testing.T) {
	m := newFormTestModel(t)
	m.modalMode = ModalAddProvider
	m.formValues = []string{"openai-test", "openai", "sk-abcdefghijklmnopqrstuvwxyz", "https://api.openai.com/v1"}
	m.formStepIndex = 3

	out := m.View()
	if strings.Contains(out, "sk-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("stored API key leaked in completed-step line:\n%s", out)
	}
	if !strings.Contains(out, maskSecretDisplay(m.formValues[2])) {
		t.Fatalf("expected maskSecretDisplay output in render:\n%s", out)
	}
}

// TestReviewStepSecretMasked: the review step must use the shared masking
// predicate (previously a bespoke i==2 rule that skipped short keys).
func TestReviewStepSecretMasked(t *testing.T) {
	m := newFormTestModel(t)
	m.modalMode = ModalAddProvider
	m.providerSavedInFlow = true
	m.formValues = []string{"openai-test", "openai", "shortkey", "https://api.openai.com/v1", "gpt-4o", "gpt-4o-2024-08-06", "128000", "4096", "no", ""}
	m.formStepIndex = 9

	out := m.View()
	if strings.Contains(out, "shortkey") {
		t.Fatalf("short API key leaked in review step (old bespoke mask only covered len>8):\n%s", out)
	}
	if !strings.Contains(out, maskSecretDisplay("shortkey")) {
		t.Fatalf("expected full-bullet mask in review render:\n%s", out)
	}
}

// TestAddSecretValueStepMasked: the add-secret form must password-echo on
// step 1 and hide the raw value in the render.
func TestAddSecretValueStepMasked(t *testing.T) {
	m := newFormTestModel(t)
	m.modalMode = ModalAddSecret
	m.formValues = make([]string, 5)
	m.formValues[0] = "my.api_key"
	m.formStepIndex = 1

	// Typing on the value step switches the widget to password echo.
	m = sendKeys(m, "h", "u", "n", "t", "e", "r")
	if m.textInput.EchoMode != textinput.EchoPassword {
		t.Fatalf("EchoMode = %v, want EchoPassword on secret-value step", m.textInput.EchoMode)
	}

	// Advance stores the value; completed-step line must mask it too.
	m = sendKeys(m, "\r")
	if m.formStepIndex != 2 {
		t.Fatalf("expected step 2 after value, got %d", m.formStepIndex)
	}
	if m.formValues[1] != "hunter" {
		t.Fatalf("formValues[1] = %q, want hunter", m.formValues[1])
	}
	// View() is the universal render-side sync point that closes the gap
	// left by Enter-transitions returning before the Update-side sync; in
	// the real app bubbletea always calls View() after Update, so the
	// widget a user ever sees is already in the correct echo mode.
	if out := m.View(); strings.Contains(out, "hunter") {
		t.Fatalf("secret value leaked in render:\n%s", out)
	}
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Fatalf("EchoMode = %v, want EchoNormal after leaving secret step", m.textInput.EchoMode)
	}

	out := m.View()
	if strings.Contains(out, "hunter") {
		t.Fatalf("secret value leaked in render:\n%s", out)
	}
	if !strings.Contains(out, maskSecretDisplay("hunter")) {
		t.Fatalf("expected bullet mask for completed secret value:\n%s", out)
	}
}

// TestEchoResetOnNormalStep: keystrokes on non-secret steps keep/restore
// normal echo.
func TestEchoResetOnNormalStep(t *testing.T) {
	m := newFormTestModel(t)
	m.modalMode = ModalAddProvider
	m.formValues = make([]string, 10)
	m.formStepIndex = 3 // API Base URL — not a secret

	m.textInput.EchoMode = textinput.EchoPassword // simulate stale password echo
	m = sendKeys(m, "x")
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Fatalf("EchoMode = %v, want EchoNormal on non-secret step", m.textInput.EchoMode)
	}
}

// TestConnectFlowPresetSecretStep: the onboarding /connect preset path jumps
// directly to the API-key step (formStepIndex=2, or 3 for ollama). The echo
// must already be correct on the first frame — before any keystroke reaches
// the forwarding block.
func TestConnectFlowPresetSecretStep(t *testing.T) {
	m := newFormTestModel(t)

	// Preset with a key: lands on step 2 (API Key).
	m.startConnectFlow(&providerPresets[0])
	if m.formStepIndex != 2 {
		t.Fatalf("expected preset flow at step 2, got %d", m.formStepIndex)
	}
	if m.textInput.EchoMode != textinput.EchoPassword {
		t.Fatalf("EchoMode = %v, want EchoPassword right after startConnectFlow (preset)", m.textInput.EchoMode)
	}

	// Local provider preset (ollama) skips the key step → normal echo.
	var ollama *providerPreset
	for i := range providerPresets {
		if providerPresets[i].typ == "ollama" {
			ollama = &providerPresets[i]
			break
		}
	}
	if ollama == nil {
		t.Skip("no ollama preset")
	}
	m.startConnectFlow(ollama)
	if m.formStepIndex != 3 {
		t.Fatalf("expected ollama flow at step 3, got %d", m.formStepIndex)
	}
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Fatalf("EchoMode = %v, want EchoNormal on ollama (no key step)", m.textInput.EchoMode)
	}

	// No preset: starts at step 0 → normal echo.
	m.startConnectFlow(nil)
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Fatalf("EchoMode = %v, want EchoNormal at step 0", m.textInput.EchoMode)
	}
}

// TestEchoResetAfterFormClosed: leaving the modal (ESC from a secret step)
// must restore normal echo so a stale password mode can't affect the next
// text input render.
func TestEchoResetAfterFormClosed(t *testing.T) {
	m := newFormTestModel(t)
	m.executeCommand("/connect")
	m.textInput.SetValue("prov")
	m = sendKeys(m, "\r")       // name → type picker (step 1)
	m.providerTypePickerIdx = 0 // openai preset → step 2 (API Key)
	m = sendKeys(m, "\r")
	// bubbletea always runs View() after Update(); View() is the universal
	// render-side echo sync, so assert state after a full turn.
	m.View()
	if m.textInput.EchoMode != textinput.EchoPassword {
		t.Fatalf("precondition: expected EchoPassword on step 2, got %v", m.textInput.EchoMode)
	}

	m = sendKeys(m, "\x1b") // ESC closes the modal
	m.View()
	if m.modalMode != ModalNone {
		t.Fatalf("expected modal closed, got %v", m.modalMode)
	}
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Fatalf("EchoMode = %v, want EchoNormal after leaving form", m.textInput.EchoMode)
	}
}
