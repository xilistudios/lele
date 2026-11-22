package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func TestRenderConnectPicker(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 40

	m.executeCommand("/connect")
	if !m.providerTypePicker {
		t.Fatal("expected picker active")
	}

	out := m.renderFormModal("Add Provider", m.formStepNames())
	if !strings.Contains(out, "OpenAI") {
		t.Fatalf("picker missing OpenAI preset:\n%s", out)
	}
	if !strings.Contains(out, "Anthropic") {
		t.Fatalf("picker missing Anthropic preset:\n%s", out)
	}
	if !strings.Contains(out, "https://api.openai.com/v1") {
		t.Fatalf("picker missing openai api base:\n%s", out)
	}

	m.providerTypePickerIdx = len(connectRows()) - 1
	out = m.renderFormModal("Add Provider", m.formStepNames())
	if !strings.Contains(out, "Custom") && !strings.Contains(out, "Personalizado") {
		t.Fatalf("picker missing custom entry:\n%s", out)
	}
}

func TestRenderConnectPickerScroll(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.height = 30

	m.executeCommand("/connect")
	if !m.providerTypePicker {
		t.Fatal("expected picker active")
	}

	rows := connectRows()
	out := m.renderFormModal("Add Provider", m.formStepNames())
	if !strings.Contains(out, rows[0].label) {
		t.Fatalf("expected first row %q at top:\n%s", rows[0].label, out)
	}
	lastRow := rows[len(rows)-1]
	if strings.Contains(out, lastRow.label) {
		t.Fatalf("last row %q should be off-screen at top:\n%s", lastRow.label, out)
	}
	if !strings.Contains(out, i18n.T("tui.moreBelow")) {
		t.Fatalf("expected scroll-down indicator:\n%s", out)
	}

	m.providerTypePickerIdx = len(rows) - 1
	out = m.renderFormModal("Add Provider", m.formStepNames())
	if !strings.Contains(out, lastRow.label) {
		t.Fatalf("expected last row %q after scroll:\n%s", lastRow.label, out)
	}
	if strings.Contains(out, "> "+rows[0].label) || strings.Contains(out, "   "+rows[0].label) {
		t.Fatalf("first row %q should be off-screen after scrolling to end:\n%s", rows[0].label, out)
	}
	if !strings.Contains(out, i18n.T("tui.moreAbove")) {
		t.Fatalf("expected scroll-up indicator:\n%s", out)
	}

	m.providerTypePickerIdx = 0
	m.modalScrollOffset = 0
	for i := 0; i < 20; i++ {
		m = sendKeys(m, "down")
	}
	out = m.renderFormModal("Add Provider", m.formStepNames())
	if m.providerTypePickerIdx != 20 {
		t.Fatalf("expected idx 20, got %d", m.providerTypePickerIdx)
	}
	if m.modalScrollOffset <= 0 {
		t.Fatalf("expected positive scroll offset after moving past fold, got %d", m.modalScrollOffset)
	}
	selLabel := rows[m.providerTypePickerIdx].label
	if !strings.Contains(out, selLabel) {
		t.Fatalf("selected row %q not visible:\n%s", selLabel, out)
	}
}

func TestRenderConnectSuccess(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 40

	m.executeCommand("/connect")
	m = pickConnectRow(t, m, connectAPIKeyRowIdx(t, "openai"))
	m.textInput.SetValue("sk-test-key")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("gpt-4o")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("gpt-4o-2024-08-06")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("128000")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("4096")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("yes")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")

	if !m.connectSuccess {
		t.Fatalf("expected success screen, got step %d saved=%v", m.formStepIndex, m.providerSavedInFlow)
	}

	out := m.renderFormModal("Add Provider", m.formStepNames())
	if !strings.Contains(out, "configured successfully") && !strings.Contains(out, "configurados") && !strings.Contains(out, "configurado") {
		t.Fatalf("success screen missing confirmation:\n%s", out)
	}
	if !strings.Contains(out, "openai") {
		t.Fatalf("success screen missing provider name:\n%s", out)
	}
	if !strings.Contains(out, "gpt-4o") {
		t.Fatalf("success screen missing model alias:\n%s", out)
	}
}

func TestConnectFlow_SuccessScreenCloses(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	m = pickConnectRow(t, m, connectAPIKeyRowIdx(t, "openai"))
	m.textInput.SetValue("sk-test-key")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("gpt-4o")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("gpt-4o-2024-08-06")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("128000")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("4096")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("yes")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")

	if !m.connectSuccess {
		t.Fatalf("expected success screen")
	}
	m = sendKeys(m, tea.KeyEsc.String())
	if m.modalMode != ModalNone {
		t.Fatalf("expected modal closed after ESC on success, got %v", m.modalMode)
	}
	if m.connectSuccess {
		t.Fatal("connectSuccess should be cleared")
	}
}

func TestProvidersModal_ConnectAction(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/providers")
	if m.modalMode != ModalProviders {
		t.Fatalf("expected ModalProviders, got %v", m.modalMode)
	}
	if len(m.modalItems) < 3 {
		t.Fatalf("expected at least 3 items, got %d: %v", len(m.modalItems), m.modalItems)
	}
	last := m.modalItems[len(m.modalItems)-1]
	if last != i18n.T("tui.connectAction") {
		t.Fatalf("expected connect action at end, got %q", last)
	}

	m.modalSelectedIdx = len(m.modalItems) - 1
	m = sendKeys(m, "\r")
	if m.modalMode != ModalAddProvider {
		t.Fatalf("expected ModalAddProvider after connect action, got %v", m.modalMode)
	}
}

func TestProvidersModal_SelectProvider(t *testing.T) {
	m := newTestModel(t)
	if m.cfg.Providers.Named == nil {
		m.cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	m.cfg.Providers.Named["test-provider"] = config.NamedProviderConfig{
		Type: "openai",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "sk-secret-key-1234567890",
			APIBase: "https://api.openai.com/v1",
		},
		Models: map[string]config.ProviderModelConfig{
			"gpt-4o": {Model: "gpt-4o", ContextWindow: 128000},
		},
	}

	m.executeCommand("/providers")
	if m.modalMode != ModalProviders {
		t.Fatalf("expected ModalProviders, got %v", m.modalMode)
	}
	if m.modalSelectedIdx >= len(m.providerModalKeys) {
		t.Fatal("no provider keys")
	}

	m = sendKeys(m, "\r")
	if m.modalMode != ModalProviderDetail {
		t.Fatalf("expected ModalProviderDetail, got %v", m.modalMode)
	}
	foundModel := false
	for _, item := range m.modalItems {
		if item == "  gpt-4o" {
			foundModel = true
		}
	}
	if !foundModel {
		t.Fatalf("detail view missing model entry: %v", m.modalItems)
	}
}
