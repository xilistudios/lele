package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// TestRenderConnectPicker verifies the row catalogue renders the custom rows,
// a known provider row with its API base, and the trailing free-type row.
func TestRenderConnectPicker(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 40

	// /connect opens the picker on the first frame — no name step first.
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
	// The API base should be visible for presets.
	if !strings.Contains(out, "https://api.openai.com/v1") {
		t.Fatalf("picker missing openai api base:\n%s", out)
	}

	// Jump to the last entry (free-type row) so it scrolls into view.
	m.providerTypePickerIdx = len(connectRows()) - 1
	out = m.renderFormModal("Add Provider", m.formStepNames())
	if !strings.Contains(out, "Custom") && !strings.Contains(out, "Personalizado") {
		t.Fatalf("picker missing custom entry:\n%s", out)
	}
}

// TestRenderConnectPickerScroll verifies the long preset list is windowed:
// earlier items scroll out of view when the selection moves past the
// visible page, and the custom entry becomes reachable at the end.
func TestRenderConnectPickerScroll(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.height = 30 // matches a typical compact TUI frame

	// The picker is open on the first frame.
	m.executeCommand("/connect")
	if !m.providerTypePicker {
		t.Fatal("expected picker active")
	}

	// At the top, the custom OpenAI row (the first row) is visible and the
	// last row is not. The last ROW's label is what must stay off-screen at
	// the top.
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

	// Move selection to the last row — it must scroll into view and the top
	// of the catalogue should scroll away. The check must look at the rows
	// in the WINDOW (not a raw substring): the free-type row's label also
	// contains "OpenAI" ("OpenAI compatible (custom)"), so a plain
	// strings.Contains would match it while the window is at the end.
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

	// Keyboard down past the fold updates the offset so selection stays visible.
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
	// The selected row must be in the rendered window.
	selLabel := rows[m.providerTypePickerIdx].label
	if !strings.Contains(out, selLabel) {
		t.Fatalf("selected row %q not visible:\n%s", selLabel, out)
	}
}

// TestRenderConnectSuccess verifies the success screen shows the saved
// provider and model.
func TestRenderConnectSuccess(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 40

	// The row catalogue is open on the first frame: pick the OpenAI API-key
	// row, then the flow asks only for the key (base is pre-filled and the
	// base step is skipped).
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
	m = sendKeys(m, "\r") // think system (auto) → review
	m = sendKeys(m, "\r") // review → save → success

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

// TestConnectFlow_SuccessScreenCloses verifies Enter on the success screen
// closes the modal.
func TestConnectFlow_SuccessScreenCloses(t *testing.T) {
	m := newTestModel(t)

	// /connect opens the picker; pick the OpenAI API-key row: key → (base
	// skipped) → model steps.
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
	m = sendKeys(m, "\r") // think system (auto) → review
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
