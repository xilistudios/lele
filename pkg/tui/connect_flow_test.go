package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// newTestModel builds a TUI Model with a temp workspace and a real agent loop,
// ready to exercise modal flows like /connect.
func newTestModel(t *testing.T) *Model {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{},
	}
	// Save an initial config file so saveConfigToDisk works during the flow.
	if err := config.SaveConfig(filepath.Join(tmpDir, "config.json"), cfg); err != nil {
		t.Fatalf("saving initial config: %v", err)
	}
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	msgBus := bus.NewMessageBus()
	al := agent.NewAgentLoop(cfg, msgBus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go al.Run(ctx)

	sessionMgr := al.SessionManager()
	if sessionMgr == nil {
		t.Fatal("session manager not initialized")
	}

	return NewModel(cfg, al, sessionMgr)
}

// sendKeys delivers the given key presses to the model and returns the updated
// model, ignoring the returned command. Use "\r" for Enter and "\x1b" for Esc.
func sendKeys(m *Model, keys ...string) *Model {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "\r":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "\x1b":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		updated, _ := m.Update(msg)
		m = updated.(*Model)
	}
	return m
}

func TestConnectFlow_PresetPicker(t *testing.T) {
	m := newTestModel(t)
	if m.cfg == nil {
		t.Fatal("cfg is nil")
	}

	// Start /connect
	m.executeCommand("/connect")
	if m.modalMode != ModalAddProvider {
		t.Fatalf("expected ModalAddProvider, got %v", m.modalMode)
	}
	if m.formStepIndex != 0 {
		t.Fatalf("expected step 0, got %d", m.formStepIndex)
	}

	// Step 0: provider name
	m.textInput.SetValue("my-provider")
	m = sendKeys(m, "\r") // Enter
	if m.formStepIndex != 1 {
		t.Fatalf("after name, expected step 1, got %d", m.formStepIndex)
	}
	if !m.providerTypePicker {
		t.Fatal("expected provider type picker to be active")
	}
	if m.providerTypePickerMax != len(providerPresets)+1 {
		t.Fatalf("expected picker max %d, got %d", len(providerPresets)+1, m.providerTypePickerMax)
	}

	// Navigate down to pick "anthropic" (index 1) and choose it.
	m.providerTypePickerIdx = 1
	m = sendKeys(m, "\r")
	if m.providerTypePicker {
		t.Fatal("picker should be closed after selection")
	}
	if m.formValues[1] != "anthropic" {
		t.Fatalf("expected provider type anthropic, got %q", m.formValues[1])
	}
	if !m.providerTypeFromPreset {
		t.Fatal("expected providerTypeFromPreset true")
	}
	// API base pre-filled from preset.
	if m.formValues[3] != "https://api.anthropic.com/v1" {
		t.Fatalf("expected pre-filled api base, got %q", m.formValues[3])
	}
	if m.formStepIndex != 2 {
		t.Fatalf("after type pick, expected step 2 (API key), got %d", m.formStepIndex)
	}
}

func TestConnectFlow_DuplicateNameRejected(t *testing.T) {
	m := newTestModel(t)
	if m.cfg == nil || m.cfg.Providers == nil {
		t.Fatal("providers config nil")
	}
	if m.cfg.Providers.Named == nil {
		m.cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	m.cfg.Providers.Named["existing"] = config.NamedProviderConfig{Type: "openai"}

	m.executeCommand("/connect")
	m.textInput.SetValue("existing")
	m = sendKeys(m, "\r")

	if m.formStepIndex != 0 {
		t.Fatalf("expected to stay on step 0, got %d", m.formStepIndex)
	}
	if !strings.Contains(m.formError, "already exists") {
		t.Fatalf("expected duplicate error, got %q", m.formError)
	}
}

func TestConnectFlow_CustomTypeEntry(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	m.textInput.SetValue("my-provider")
	m = sendKeys(m, "\r")
	if !m.providerTypePicker {
		t.Fatal("expected type picker active")
	}

	// Choose the "custom" entry (last index).
	m.providerTypePickerIdx = len(providerPresets)
	m = sendKeys(m, "\r")
	if m.providerTypePicker {
		t.Fatal("picker should be closed after custom selection")
	}
	if m.formStepIndex != 1 {
		t.Fatalf("custom selection should stay on step 1 for free text, got %d", m.formStepIndex)
	}

	// Type a free-form type.
	m.textInput.SetValue("my-custom-type")
	m = sendKeys(m, "\r")
	if m.formValues[1] != "my-custom-type" {
		t.Fatalf("expected custom type, got %q", m.formValues[1])
	}
	if m.formStepIndex != 2 {
		t.Fatalf("expected step 2 after custom type, got %d", m.formStepIndex)
	}
	if m.providerTypeFromPreset {
		t.Fatal("custom type should not be from preset")
	}
}

func TestConnectFlow_APIKeyOptional(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	m.textInput.SetValue("local-provider")
	m = sendKeys(m, "\r")
	// Pick ollama preset (index 9).
	m.providerTypePickerIdx = 9
	m = sendKeys(m, "\r")
	if m.formValues[1] != "ollama" {
		t.Fatalf("expected ollama, got %q", m.formValues[1])
	}

	// API key can be empty for local providers.
	m.textInput.SetValue("")
	m = sendKeys(m, "\r")
	if m.formStepIndex != 3 {
		t.Fatalf("expected step 3 (api base), got %d (api key should be optional)", m.formStepIndex)
	}
	if m.formValues[2] != "" {
		t.Fatalf("expected empty api key, got %q", m.formValues[2])
	}
}

func TestConnectFlow_FullSaveAndSuccess(t *testing.T) {
	m := newTestModel(t)
	if m.cfg == nil {
		t.Fatal("cfg is nil")
	}

	m.executeCommand("/connect")
	m.textInput.SetValue("openai-test")
	m = sendKeys(m, "\r")
	m.providerTypePickerIdx = 0 // openai
	m = sendKeys(m, "\r")
	if m.formValues[1] != "openai" {
		t.Fatalf("expected openai, got %q", m.formValues[1])
	}

	// API key
	m.textInput.SetValue("sk-test-key")
	m = sendKeys(m, "\r")
	if m.formStepIndex != 3 {
		t.Fatalf("expected step 3 (api base), got %d", m.formStepIndex)
	}

	// API base pre-filled
	if !strings.HasPrefix(m.textInput.Value(), "https://api.openai.com") {
		t.Fatalf("expected api base pre-filled, got %q", m.textInput.Value())
	}
	m = sendKeys(m, "\r")
	if m.formStepIndex != 4 {
		t.Fatalf("expected step 4 (model alias), got %d", m.formStepIndex)
	}
	if !m.providerSavedInFlow {
		t.Fatal("expected providerSavedInFlow after provider save")
	}

	// Model steps
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
	if m.formStepIndex != 9 {
		t.Fatalf("expected review step 9, got %d", m.formStepIndex)
	}

	// Review — save model
	m = sendKeys(m, "\r")
	if !m.connectSuccess {
		t.Fatal("expected connectSuccess after saving model")
	}
	if m.modalMode != ModalAddProvider {
		t.Fatalf("expected still in ModalAddProvider for success screen, got %v", m.modalMode)
	}

	// Verify provider persisted
	key := "openai-test"
	p, ok := m.cfg.Providers.Named[key]
	if !ok {
		t.Fatalf("provider %q not saved", key)
	}
	if p.Type != "openai" {
		t.Fatalf("expected type openai, got %q", p.Type)
	}
	if p.APIKey != "sk-test-key" {
		t.Fatalf("expected api key, got %q", p.APIKey)
	}
	if len(p.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(p.Models))
	}
	if mc, ok := p.Models["gpt-4o"]; !ok || mc.Model != "gpt-4o-2024-08-06" || mc.ContextWindow != 128000 || mc.MaxTokens != 4096 || !mc.Vision {
		t.Fatalf("model config mismatch: %+v", mc)
	}

	// Success screen closes on Enter.
	m = sendKeys(m, "\r")
	if m.modalMode != ModalNone {
		t.Fatalf("expected modal closed after success, got %v", m.modalMode)
	}
	if m.connectSuccess {
		t.Fatal("connectSuccess should be cleared")
	}
}

func TestConnectFlow_EscLeavesPickerToFreeForm(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	m.textInput.SetValue("my-provider")
	m = sendKeys(m, "\r")
	if !m.providerTypePicker {
		t.Fatal("expected picker active")
	}

	// ESC returns to free-form type input.
	m = sendKeys(m, "\x1b")
	if m.providerTypePicker {
		t.Fatal("expected picker closed after ESC")
	}
	if m.formStepIndex != 1 {
		t.Fatalf("expected step 1, got %d", m.formStepIndex)
	}
}

// TestConnectFlow_QDoesNotCancelPicker verifies the "q" key does NOT exit the
// picker — only ESC does. Typing "q" should be forwarded to the text input
// (free-form type entry) without closing the picker.
func TestConnectFlow_QDoesNotCancelPicker(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	m.textInput.SetValue("my-provider")
	m = sendKeys(m, "\r")
	if !m.providerTypePicker {
		t.Fatal("expected picker active")
	}

	// "q" must not cancel the picker.
	m = sendKeys(m, "q")
	if !m.providerTypePicker {
		t.Fatal("expected picker still active after 'q'")
	}
	if m.formStepIndex != 1 {
		t.Fatalf("expected still step 1, got %d", m.formStepIndex)
	}

	// ESC still cancels.
	m = sendKeys(m, "\x1b")
	if m.providerTypePicker {
		t.Fatal("expected picker closed after ESC")
	}
}

// TestConnectFlow_LoadPersisted ensures the config loads the saved provider
// from disk (the save path writes through config.SaveConfig).
func TestConnectFlow_ProviderPersistsToDisk(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	m.textInput.SetValue("disk-provider")
	m = sendKeys(m, "\r")
	m.providerTypePickerIdx = 0
	m = sendKeys(m, "\r")
	m.textInput.SetValue("sk-disk")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r") // api base (pre-filled)

	if _, ok := m.cfg.Providers.Named["disk-provider"]; !ok {
		t.Fatalf("provider not saved in memory: %+v", m.cfg.Providers)
	}

	// Reload from disk.
	loaded, err := config.LoadConfig(filepath.Join(os.Getenv("LELE_CONFIG_DIR"), "config.json"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if loaded.Providers == nil || loaded.Providers.Named == nil {
		t.Fatalf("loaded providers nil")
	}
	if _, ok := loaded.Providers.Named["disk-provider"]; !ok {
		t.Fatalf("provider not found after reload")
	}
	_ = os.Remove(filepath.Join(os.Getenv("LELE_CONFIG_DIR"), "config.json"))
}
