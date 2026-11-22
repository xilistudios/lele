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

func newTestModel(t *testing.T) *Model {
	t.Helper()
	return newTestModelWithConfig(t, testModelConfig(t), true)
}

func testModelConfig(t *testing.T) *config.Config {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	return &config.Config{
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
}

func newTestModelWithConfig(t *testing.T, cfg *config.Config, disableOnboarding bool) *Model {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	if err := config.SaveConfig(filepath.Join(tmpDir, "config.json"), cfg); err != nil {
		t.Fatalf("saving initial config: %v", err)
	}

	msgBus := bus.NewMessageBus()
	al := agent.NewAgentLoop(cfg, msgBus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go al.Run(ctx)

	sessionMgr := al.SessionManager()
	if sessionMgr == nil {
		t.Fatal("session manager not initialized")
	}

	m := NewModel(cfg, al, sessionMgr)
	if disableOnboarding {
		m.onboardingActive = false
		m.obSkipConfirm = false
	}
	return m
}

func newOnboardingTestModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModelWithConfig(t, testModelConfig(t), false)
	m.width = 80
	m.height = 40
	return m
}

func newConfiguredOnboardingTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := testModelConfig(t)
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "sk-xxx"},
			Models:         map[string]config.ProviderModelConfig{"gpt-4o": {Model: "gpt-4o"}},
		},
	}
	return newTestModelWithConfig(t, cfg, false)
}

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

func pickConnectRow(t *testing.T, m *Model, i int) *Model {
	t.Helper()
	if m.modalMode != ModalAddProvider || !m.providerTypePicker {
		t.Fatalf("expected the /connect picker to be open, got mode=%v picker=%v", m.modalMode, m.providerTypePicker)
	}
	m.providerTypePickerIdx = i
	return sendKeys(m, "\r")
}

func pickFreeTypeRow(t *testing.T, m *Model) *Model {
	t.Helper()
	return pickConnectRow(t, m, len(connectRows())-1)
}

func pickLegacyFreeTextName(t *testing.T, m *Model, name string) *Model {
	t.Helper()
	m.textInput.SetValue(name)
	return sendKeys(m, "\r")
}

func connectAPIKeyRowIdx(t *testing.T, typ string) int {
	t.Helper()
	for i, r := range connectRows() {
		if r.kind == connectRowAPIKey && r.typ == typ {
			return i
		}
	}
	t.Fatalf("no API-key row for %q", typ)
	return -1
}

func TestConnectFlow_PickerIsFirst(t *testing.T) {
	m := newTestModel(t)
	if m.cfg == nil {
		t.Fatal("cfg is nil")
	}

	m.executeCommand("/connect")
	if m.modalMode != ModalAddProvider {
		t.Fatalf("expected ModalAddProvider, got %v", m.modalMode)
	}
	if !m.providerTypePicker {
		t.Fatal("expected the provider picker on the first frame")
	}
	if m.formStepIndex != 0 {
		t.Fatalf("expected step 0 on the picker frame, got %d", m.formStepIndex)
	}
	if m.providerTypePickerMax != len(connectRows()) {
		t.Fatalf("expected picker max %d, got %d", len(connectRows()), m.providerTypePickerMax)
	}
	if m.connectRowIdx != -1 {
		t.Fatalf("row-first picker must not pre-pick a row, got connectRowIdx=%d", m.connectRowIdx)
	}
	if !m.connectRowPicker {
		t.Fatal("expected connectRowPicker on the row-first frame")
	}

	rows := connectRows()
	anthropicIdx := -1
	for i, r := range rows {
		if r.kind == connectRowAPIKey && r.typ == "anthropic" {
			anthropicIdx = i
			break
		}
	}
	if anthropicIdx < 0 {
		t.Fatal("no anthropic API-key row in the catalogue")
	}
	for i := 0; i < anthropicIdx; i++ {
		m = sendKeys(m, "down")
	}
	if m.providerTypePickerIdx != anthropicIdx {
		t.Fatalf("picker idx = %d, want %d", m.providerTypePickerIdx, anthropicIdx)
	}
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
	if m.formValues[3] != "https://api.anthropic.com/v1" {
		t.Fatalf("expected pre-filled api base, got %q", m.formValues[3])
	}
	if m.formStepIndex != 2 {
		t.Fatalf("after the API-key row, expected step 2 (API key), got %d", m.formStepIndex)
	}
	if m.connectSkipAPIBase != true {
		t.Fatal("expected connectSkipAPIBase for an API-key-only row")
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
	m.cfg.Providers.Named["existing"] = config.NamedProviderConfig{Type: "openai", ProviderConfig: config.ProviderConfig{APIKey: "sk-x"}}

	m.executeCommand("/connect")
	m = pickFreeTypeRow(t, m)
	m.textInput.SetValue("existing")
	m = sendKeys(m, "\r")

	if m.formStepIndex != 0 {
		t.Fatalf("expected to stay on step 0, got %d", m.formStepIndex)
	}
	if !strings.Contains(m.formError, "already exists") {
		t.Fatalf("expected duplicate error, got %q", m.formError)
	}
}

func TestConnectFlow_FreeTextStillRejectsDuplicate(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	m.cfg.Providers.Named["dup"] = config.NamedProviderConfig{Type: "openai", ProviderConfig: config.ProviderConfig{APIKey: "sk-x"}}

	m.executeCommand("/connect")
	m = pickFreeTypeRow(t, m)
	m.textInput.SetValue("dup")
	m = sendKeys(m, "\r")
	if m.formStepIndex != 0 {
		t.Fatalf("expected to stay on step 0, got %d", m.formStepIndex)
	}
	if !strings.Contains(m.formError, "already exists") {
		t.Fatalf("expected duplicate error, got %q", m.formError)
	}

	m.textInput.SetValue("dup2")
	m = sendKeys(m, "\r")
	if !m.providerTypePicker {
		t.Fatal("expected the legacy type picker to open after a valid name")
	}
	if m.providerTypePickerMax != len(providerPresets)+1 {
		t.Fatalf("legacy picker max = %d, want %d", m.providerTypePickerMax, len(providerPresets)+1)
	}
}

func TestConnectFlow_LegacyPickerIsNotRowFirst(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/connect")

	m.providerTypePickerIdx = len(connectRows()) - 1
	m = sendKeys(m, "\r")
	if m.providerTypePicker {
		t.Fatal("picker should close after choosing the free-type row")
	}
	if m.formStepIndex != 0 {
		t.Fatalf("free-type row should ask for the name first, got step %d", m.formStepIndex)
	}
	m.textInput.SetValue("my-proxy")
	m = sendKeys(m, "\r")
	if !m.providerTypePicker {
		t.Fatal("expected the legacy provider-type picker after the name")
	}
	if m.connectRowPicker {
		t.Fatal("the legacy picker must not be flagged row-first")
	}
	if m.providerTypePickerMax != len(providerPresets)+1 {
		t.Fatalf("legacy picker max = %d, want %d", m.providerTypePickerMax, len(providerPresets)+1)
	}
	if m.connectRowIdx != -1 {
		t.Fatalf("free-type name step must reset connectRowIdx to -1, got %d", m.connectRowIdx)
	}
}

func TestConnectFlow_CustomTypeEntry(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	m = pickFreeTypeRow(t, m)
	if m.formStepIndex != 0 {
		t.Fatalf("free-type row should start at the name step, got %d", m.formStepIndex)
	}

	m = pickLegacyFreeTextName(t, m, "my-provider")
	if !m.providerTypePicker {
		t.Fatal("expected type picker active")
	}
	if m.providerTypePickerMax != len(providerPresets)+1 {
		t.Fatalf("legacy picker max = %d, want %d", m.providerTypePickerMax, len(providerPresets)+1)
	}

	m.providerTypePickerIdx = len(providerPresets)
	m = sendKeys(m, "\r")
	if m.providerTypePicker {
		t.Fatal("picker should be closed after custom selection")
	}
	if m.formStepIndex != 1 {
		t.Fatalf("custom selection should stay on step 1 for free text, got %d", m.formStepIndex)
	}

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
	m = pickFreeTypeRow(t, m)
	m = pickLegacyFreeTextName(t, m, "local-provider")
	m.providerTypePickerIdx = 9
	m = sendKeys(m, "\r")
	if m.formValues[1] != "ollama" {
		t.Fatalf("expected ollama, got %q", m.formValues[1])
	}

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
	m = pickFreeTypeRow(t, m)
	m = pickLegacyFreeTextName(t, m, "openai-test")
	m.providerTypePickerIdx = 0
	m = sendKeys(m, "\r")
	if m.formValues[1] != "openai" {
		t.Fatalf("expected openai, got %q", m.formValues[1])
	}

	m.textInput.SetValue("sk-test-key")
	m = sendKeys(m, "\r")
	if m.formStepIndex != 3 {
		t.Fatalf("expected step 3 (api base), got %d", m.formStepIndex)
	}

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
		t.Fatalf("expected think-system step 9, got %d", m.formStepIndex)
	}

	m = sendKeys(m, "\r")
	if m.formStepIndex != 10 {
		t.Fatalf("expected review step 10, got %d", m.formStepIndex)
	}

	m = sendKeys(m, "\r")
	if !m.connectSuccess {
		t.Fatal("expected connectSuccess after saving model")
	}
	if m.modalMode != ModalAddProvider {
		t.Fatalf("expected still in ModalAddProvider for success screen, got %v", m.modalMode)
	}

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
	if mc, ok := p.Models["gpt-4o"]; !ok || mc.Model != "gpt-4o-2024-08-06" || mc.ContextWindow != 128000 || mc.MaxTokens != 4096 || !mc.Vision || mc.ThinkingType != "" {
		t.Fatalf("model config mismatch: %+v", mc)
	}

	m = sendKeys(m, "\r")
	if m.modalMode != ModalNone {
		t.Fatalf("expected modal closed after success, got %v", m.modalMode)
	}
	if m.connectSuccess {
		t.Fatal("connectSuccess should be cleared")
	}
}

func TestConnectFlow_EscCancelsFlowFromPicker(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	if !m.providerTypePicker {
		t.Fatal("expected picker active on the first frame")
	}

	m = sendKeys(m, "\x1b")
	if m.providerTypePicker {
		t.Fatal("expected picker closed after ESC")
	}
	if m.modalMode != ModalNone {
		t.Fatalf("expected the modal closed after ESC on the picker, got %v", m.modalMode)
	}
	if m.formStepIndex != 0 {
		t.Fatalf("expected step reset to 0, got %d", m.formStepIndex)
	}
}

func TestConnectFlow_LegacyEscLeavesPickerToFreeForm(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	m = pickFreeTypeRow(t, m)
	m = pickLegacyFreeTextName(t, m, "my-provider")
	if !m.providerTypePicker {
		t.Fatal("expected picker active")
	}

	m = sendKeys(m, "\x1b")
	if m.providerTypePicker {
		t.Fatal("expected picker closed after ESC")
	}
	if m.modalMode != ModalAddProvider {
		t.Fatalf("expected still in the modal (back on the type step), got %v", m.modalMode)
	}
	if m.formStepIndex != 1 {
		t.Fatalf("expected step 1, got %d", m.formStepIndex)
	}
}

func TestConnectFlow_QDoesNotCancelPicker(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	m = pickFreeTypeRow(t, m)
	m = pickLegacyFreeTextName(t, m, "my-provider")
	if !m.providerTypePicker {
		t.Fatal("expected picker active")
	}

	m = sendKeys(m, "q")
	if !m.providerTypePicker {
		t.Fatal("expected picker still active after 'q'")
	}
	if m.formStepIndex != 1 {
		t.Fatalf("expected still step 1, got %d", m.formStepIndex)
	}

	m = sendKeys(m, "\x1b")
	if m.providerTypePicker {
		t.Fatal("expected picker closed after ESC")
	}
}

func TestConnectFlow_ProviderPersistsToDisk(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/connect")
	m = pickFreeTypeRow(t, m)
	m = pickLegacyFreeTextName(t, m, "disk-provider")
	m.providerTypePickerIdx = 0
	m = sendKeys(m, "\r")
	m.textInput.SetValue("sk-disk")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")

	if _, ok := m.cfg.Providers.Named["disk-provider"]; !ok {
		t.Fatalf("provider not saved in memory: %+v", m.cfg.Providers)
	}

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

func TestConnectFlow_APIKeyRowSkipsBaseStep(t *testing.T) {
	m := newTestModel(t)
	if m.cfg == nil || m.cfg.Providers == nil {
		t.Fatal("providers config nil")
	}

	m.executeCommand("/connect")
	idx := -1
	for i, r := range connectRows() {
		if r.kind == connectRowAPIKey && r.typ == "anthropic" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("no anthropic API-key row")
	}
	m = pickConnectRow(t, m, idx)
	if m.formStepIndex != 2 {
		t.Fatalf("expected API-key step 2, got %d", m.formStepIndex)
	}
	if m.formValues[0] != "anthropic" {
		t.Fatalf("expected derived name \"anthropic\", got %q", m.formValues[0])
	}
	if m.formValues[3] != "https://api.anthropic.com/v1" {
		t.Fatalf("expected preset api base, got %q", m.formValues[3])
	}

	m.textInput.SetValue("sk-ant-test-key")
	m = sendKeys(m, "\r")
	if m.formStepIndex != 4 {
		t.Fatalf("expected step 4 (model alias) after the key, got %d (base step must be skipped)", m.formStepIndex)
	}
	if !m.providerSavedInFlow {
		t.Fatal("expected providerSavedInFlow after the API-key save")
	}
	if m.connectSkipAPIBase != true {
		t.Fatal("connectSkipAPIBase should stay set for the row-driven flow")
	}
	p, ok := m.cfg.Providers.Named["anthropic"]
	if !ok {
		t.Fatalf("anthropic not saved: %+v", m.cfg.Providers.Named)
	}
	if p.APIKey != "sk-ant-test-key" {
		t.Fatalf("persisted API key = %q, want sk-ant-test-key", p.APIKey)
	}
	if p.APIBase != "https://api.anthropic.com/v1" {
		t.Fatalf("persisted API base = %q", p.APIBase)
	}
	if p.Type != "anthropic" {
		t.Fatalf("persisted type = %q, want anthropic", p.Type)
	}
	if m.formValues[4] != "claude-sonnet" || m.formValues[5] != "claude-sonnet-4-20250514" {
		t.Fatalf("model prefill = alias %q / model %q", m.formValues[4], m.formValues[5])
	}
}

func TestConnectFlow_ExistingProviderUpdatesKeyInPlace(t *testing.T) {
	m := newTestModel(t)
	if m.cfg.Providers.Named == nil {
		m.cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	m.cfg.Providers.Named["anthropic"] = config.NamedProviderConfig{
		Type:           "anthropic",
		ProviderConfig: config.ProviderConfig{APIKey: "sk-old", APIBase: "https://api.anthropic.com/v1"},
		Models:         map[string]config.ProviderModelConfig{"claude-sonnet": {Model: "claude-sonnet-4-20250514"}},
	}

	m.executeCommand("/connect")
	idx := -1
	for i, r := range connectRows() {
		if r.kind == connectRowAPIKey && r.typ == "anthropic" {
			idx = i
			break
		}
	}
	m = pickConnectRow(t, m, idx)
	m.textInput.SetValue("sk-new-key")
	m = sendKeys(m, "\r")

	if m.formError != "" {
		t.Fatalf("unexpected error updating an existing provider: %q", m.formError)
	}
	if !m.connectSuccess {
		t.Fatalf("expected the success screen for an already-usable provider, got step %d saved=%v", m.formStepIndex, m.providerSavedInFlow)
	}
	if m.providerSavedInFlow {
		t.Fatal("providerSavedInFlow should be false on the success screen")
	}
	p := m.cfg.Providers.Named["anthropic"]
	if p.APIKey != "sk-new-key" {
		t.Fatalf("API key = %q, want sk-new-key", p.APIKey)
	}
	if len(p.Models) != 1 {
		t.Fatalf("models must be kept, got %d", len(p.Models))
	}
	if _, ok := p.Models["claude-sonnet"]; !ok {
		t.Fatalf("claude-sonnet model lost: %+v", p.Models)
	}
}

func TestConnectFlow_ExistingPlaceholderReplaced(t *testing.T) {
	m := newTestModel(t)
	if m.cfg.Providers.Named == nil {
		m.cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	m.cfg.Providers.Named["anthropic"] = config.NamedProviderConfig{Type: "anthropic"}

	m.executeCommand("/connect")
	idx := -1
	for i, r := range connectRows() {
		if r.kind == connectRowAPIKey && r.typ == "anthropic" {
			idx = i
			break
		}
	}
	m = pickConnectRow(t, m, idx)
	m.textInput.SetValue("sk-placeholder-replace")
	m = sendKeys(m, "\r")

	if m.formError != "" {
		t.Fatalf("placeholder must be replaced silently, got error %q", m.formError)
	}
	if m.formStepIndex != 4 {
		t.Fatalf("expected the model steps after the save, got %d", m.formStepIndex)
	}
	p := m.cfg.Providers.Named["anthropic"]
	if p.APIKey != "sk-placeholder-replace" {
		t.Fatalf("placeholder not replaced: %+v", p)
	}
	if len(p.Models) != 0 {
		t.Fatalf("expected no models yet, got %d", len(p.Models))
	}
}
