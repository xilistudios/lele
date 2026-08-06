package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// TestRenderConnectPicker verifies the type picker renders the preset list
// and the custom entry.
func TestRenderConnectPicker(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 40

	m.executeCommand("/connect")
	m.textInput.SetValue("my-provider")
	m = sendKeys(m, "\r")
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
	if !strings.Contains(out, "Custom") && !strings.Contains(out, "Personalizado") && !strings.Contains(out, "Personalizado") {
		t.Fatalf("picker missing custom entry:\n%s", out)
	}
	// The API base should be visible for presets.
	if !strings.Contains(out, "https://api.openai.com/v1") {
		t.Fatalf("picker missing openai api base:\n%s", out)
	}
}

// TestRenderConnectSuccess verifies the success screen shows the saved
// provider and model.
func TestRenderConnectSuccess(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 40

	m.executeCommand("/connect")
	m.textInput.SetValue("openai-test")
	m = sendKeys(m, "\r")
	m.providerTypePickerIdx = 0
	m = sendKeys(m, "\r")
	m.textInput.SetValue("sk-test-key")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r") // api base pre-filled
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
	m = sendKeys(m, "\r") // review → save → success

	if !m.connectSuccess {
		t.Fatalf("expected success screen, got step %d saved=%v", m.formStepIndex, m.providerSavedInFlow)
	}

	out := m.renderFormModal("Add Provider", m.formStepNames())
	if !strings.Contains(out, "configured successfully") && !strings.Contains(out, "configurados") && !strings.Contains(out, "configurado") {
		t.Fatalf("success screen missing confirmation:\n%s", out)
	}
	if !strings.Contains(out, "openai-test") {
		t.Fatalf("success screen missing provider name:\n%s", out)
	}
	if !strings.Contains(out, "gpt-4o") {
		t.Fatalf("success screen missing model alias:\n%s", out)
	}
}

// TestConnectFlow_SuccessScreenCloses verifies Enter on the success screen
// closes the modal.
func TestConnectFlow_SuccessScreenCloses(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	m.textInput.SetValue("openai-test")
	m = sendKeys(m, "\r")
	m.providerTypePickerIdx = 0
	m = sendKeys(m, "\r")
	m.textInput.SetValue("sk-test-key")
	m = sendKeys(m, "\r")
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
	m = sendKeys(m, "\r") // review → success

	if !m.connectSuccess {
		t.Fatalf("expected success screen")
	}
	// ESC also closes.
	m = sendKeys(m, tea.KeyEsc.String())
	if m.modalMode != ModalNone {
		t.Fatalf("expected modal closed after ESC on success, got %v", m.modalMode)
	}
	if m.connectSuccess {
		t.Fatal("connectSuccess should be cleared")
	}
}

// TestProvidersModal_ConnectAction verifies the /providers modal exposes a
// "+ Connect a provider" action that starts the /connect flow.
func TestProvidersModal_ConnectAction(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/providers")
	if m.modalMode != ModalProviders {
		t.Fatalf("expected ModalProviders, got %v", m.modalMode)
	}
	// No providers configured → item 0 is the empty message, then separator,
	// then the connect action.
	if len(m.modalItems) < 3 {
		t.Fatalf("expected at least 3 items, got %d: %v", len(m.modalItems), m.modalItems)
	}
	last := m.modalItems[len(m.modalItems)-1]
	if last != i18n.T("tui.connectAction") {
		t.Fatalf("expected connect action at end, got %q", last)
	}

	// Select the connect action and press Enter.
	m.modalSelectedIdx = len(m.modalItems) - 1
	m = sendKeys(m, "\r")
	if m.modalMode != ModalAddProvider {
		t.Fatalf("expected ModalAddProvider after connect action, got %v", m.modalMode)
	}
}

// TestProvidersModal_SelectProvider shows the detail view on Enter.
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
	// Item 0 is test-provider, then separator, then connect action.
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
