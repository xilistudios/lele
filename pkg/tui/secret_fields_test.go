package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

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
		{ModalAddProvider, 10, false, "provider review step"},
		{ModalAddProvider, 9, false, "provider think-system step"},
		{ModalAddSecret, 1, true, "secret step 1 = value"},
		{ModalAddSecret, 0, false, "secret step 0 = name"},
		{ModalAddSecret, 2, false, "secret step 2 = description"},
		{ModalEditProvider, 1, true, "edit-provider step 1 = API key"},
		{ModalEditProvider, 7, true, "edit-provider step 7 = headers JSON"},
		{ModalEditProvider, 0, false, "edit-provider step 0 = type"},
		{ModalEditProvider, 2, false, "edit-provider step 2 = api base"},
		{ModalEditProvider, 3, false, "edit-provider step 3 = proxy"},
		{ModalEditProvider, 4, false, "edit-provider step 4 = auth method"},
		{ModalEditProvider, 5, false, "edit-provider step 5 = connect mode"},
		{ModalEditProvider, 6, false, "edit-provider step 6 = web search"},
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
		{ModalEditProvider, editProviderAPIKeyStep, true},
		{ModalEditProvider, editProviderHeadersStep, true},
		{ModalEditProvider, editProviderTypeStep, false},
		{ModalEditProvider, editProviderAPIBaseStep, false},
		{ModalEditProvider, editProviderProxyStep, false},
		{ModalEditProvider, editProviderAuthMethodStep, false},
		{ModalEditProvider, editProviderConnectModeStep, false},
		{ModalEditProvider, editProviderWebSearchStep, false},
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
	if got := maskSecretDisplay("abc123"); got != "••••••••" {
		t.Fatalf("short secret = %q, want 8 bullets", got)
	}
	if got := maskSecretDisplay("12345678"); got != "••••••••" {
		t.Fatalf("8-char secret = %q, want 8 bullets", got)
	}
	v := "sk-abcdefghijklmnopqrstuvwxyz"
	want := v[:4] + strings.Repeat("•", 8) + v[len(v)-4:]
	if got := maskSecretDisplay(v); got != want {
		t.Fatalf("long secret = %q, want %q", got, want)
	}
}

func newFormTestModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	i18n.InitWithLanguage("en")
	up, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = up.(*Model)
	m.showWelcome = false
	return m
}

func TestFormModalHidesSecretTyping(t *testing.T) {
	m := newFormTestModel(t)

	m.executeCommand("/connect")
	if m.modalMode != ModalAddProvider || !m.providerTypePicker {
		t.Fatalf("expected /connect with the picker open, got mode=%v picker=%v", m.modalMode, m.providerTypePicker)
	}
	m = pickConnectRow(t, m, connectAPIKeyRowIdx(t, "openai"))
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

func TestReviewStepSecretMasked(t *testing.T) {
	m := newFormTestModel(t)
	m.modalMode = ModalAddProvider
	m.providerSavedInFlow = true
	m.formValues = []string{"openai-test", "openai", "shortkey", "https://api.openai.com/v1", "gpt-4o", "gpt-4o-2024-08-06", "128000", "4096", "no", "auto", ""}
	m.formStepIndex = 10

	out := m.View()
	if strings.Contains(out, "shortkey") {
		t.Fatalf("short API key leaked in review step (old bespoke mask only covered len>8):\n%s", out)
	}
	if !strings.Contains(out, maskSecretDisplay("shortkey")) {
		t.Fatalf("expected full-bullet mask in review render:\n%s", out)
	}
}

func TestAddSecretValueStepMasked(t *testing.T) {
	m := newFormTestModel(t)
	m.modalMode = ModalAddSecret
	m.formValues = make([]string, 5)
	m.formValues[0] = "my.api_key"
	m.formStepIndex = 1

	m = sendKeys(m, "h", "u", "n", "t", "e", "r")
	if m.textInput.EchoMode != textinput.EchoPassword {
		t.Fatalf("EchoMode = %v, want EchoPassword on secret-value step", m.textInput.EchoMode)
	}

	m = sendKeys(m, "\r")
	if m.formStepIndex != 2 {
		t.Fatalf("expected step 2 after value, got %d", m.formStepIndex)
	}
	if m.formValues[1] != "hunter" {
		t.Fatalf("formValues[1] = %q, want hunter", m.formValues[1])
	}
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

func TestEchoResetOnNormalStep(t *testing.T) {
	m := newFormTestModel(t)
	m.modalMode = ModalAddProvider
	m.formValues = make([]string, 11)
	m.formStepIndex = 3

	m.textInput.EchoMode = textinput.EchoPassword
	m = sendKeys(m, "x")
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Fatalf("EchoMode = %v, want EchoNormal on non-secret step", m.textInput.EchoMode)
	}
}

func TestConnectFlowPresetSecretStep(t *testing.T) {
	m := newFormTestModel(t)

	m.startConnectFlow(&providerPresets[0])
	if m.formStepIndex != 2 {
		t.Fatalf("expected preset flow at step 2, got %d", m.formStepIndex)
	}
	if m.textInput.EchoMode != textinput.EchoPassword {
		t.Fatalf("EchoMode = %v, want EchoPassword right after startConnectFlow (preset)", m.textInput.EchoMode)
	}

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

	m.startConnectFlow(nil)
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Fatalf("EchoMode = %v, want EchoNormal at step 0", m.textInput.EchoMode)
	}
}

func TestEchoResetAfterFormClosed(t *testing.T) {
	m := newFormTestModel(t)
	m.executeCommand("/connect")
	m = pickConnectRow(t, m, connectAPIKeyRowIdx(t, "openai"))
	m.View()
	if m.textInput.EchoMode != textinput.EchoPassword {
		t.Fatalf("precondition: expected EchoPassword on step 2, got %v", m.textInput.EchoMode)
	}

	m = sendKeys(m, "\x1b")
	m.View()
	if m.modalMode != ModalNone {
		t.Fatalf("expected modal closed, got %v", m.modalMode)
	}
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Fatalf("EchoMode = %v, want EchoNormal after leaving form", m.textInput.EchoMode)
	}
}
