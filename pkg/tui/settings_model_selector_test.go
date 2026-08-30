package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// newModelSelectorTestModel builds a Model that reproduces the user's
// real-world broken setup: stale default provider + model referencing a
// provider without a models map; agentLoop nil so configSource() falls back
// to m.cfg.
func newModelSelectorTestModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	temp0 := 0.7
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				Provider:  "nanogpt", // intentionally NOT in Providers.Named (stale)
				Model:     "openrouter:deepseek/deepseek-v4-flash",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "coder", Name: "Coder", Default: true, Workspace: "/tmp/cw", Temperature: &temp0,
					Model: &config.AgentModelConfig{Primary: "llmproxy:h3"}},
				{ID: "writer", Name: "Writer"},
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"antigravity": {Type: "openai", Models: map[string]config.ProviderModelConfig{
					"antigravity-gemini-3.6-flash": {},
				}},
				"llmproxy": {Type: "llmproxy", Models: map[string]config.ProviderModelConfig{
					"deepseek-flash": {}, "h3": {}, "luna-pro": {},
				}},
				"openrouter": {Type: "openrouter"}, // no models map
			},
		},
	}
	if err := config.SaveConfig(config.DefaultConfigPath(), cfg); err != nil {
		t.Fatalf("saving initial config: %v", err)
	}
	ti := textinput.New()
	ti.Focus()
	return &Model{cfg: cfg, textInput: ti}
}

// TestConfigSelectorOptionsPure tests the pure configSelectorOptions function.
func TestConfigSelectorOptionsPure(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		l, v := configSelectorOptions("", nil)
		if l != nil || v != nil {
			t.Errorf("expected nil, nil for empty input; got labels=%v values=%v", l, v)
		}
	})

	t.Run("current_not_in_list", func(t *testing.T) {
		labels, values := configSelectorOptions("a:b", []string{"x:y", "z:w"})
		wantLabels := []string{"(default)", "a:b", "x:y", "z:w", i18n.T("tui.settings.selectorCustom")}
		wantValues := []string{"", "a:b", "x:y", "z:w", modelCustomValue}
		if len(labels) != len(wantLabels) {
			t.Fatalf("label count mismatch: got %d want %d", len(labels), len(wantLabels))
		}
		for i, w := range wantLabels {
			if labels[i] != w {
				t.Errorf("labels[%d] = %q, want %q", i, labels[i], w)
			}
		}
		if len(values) != len(wantValues) {
			t.Fatalf("value count mismatch: got %d want %d", len(values), len(wantValues))
		}
		for i, w := range wantValues {
			if values[i] != w {
				t.Errorf("values[%d] = %q, want %q", i, values[i], w)
			}
		}
	})

	t.Run("current_in_list_no_duplicate", func(t *testing.T) {
		_, values := configSelectorOptions("x:y", []string{"x:y", "z:w"})
		// "x:y" should appear exactly once (in the configured list, not prepended)
		count := 0
		for _, v := range values {
			if v == "x:y" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected x:y exactly once, got %d times in values %v", count, values)
		}
		wantValues := []string{"", "x:y", "z:w", modelCustomValue}
		if len(values) != len(wantValues) {
			t.Fatalf("value count mismatch: got %d want %d", len(values), len(wantValues))
		}
		for i, w := range wantValues {
			if values[i] != w {
				t.Errorf("values[%d] = %q, want %q", i, values[i], w)
			}
		}
	})

	t.Run("no_current_value", func(t *testing.T) {
		labels, values := configSelectorOptions("", []string{"x:y"})
		wantValues := []string{"", "x:y", modelCustomValue}
		if len(values) != len(wantValues) {
			t.Fatalf("value count mismatch: got %d want %d", len(values), len(wantValues))
		}
		for i, w := range wantValues {
			if values[i] != w {
				t.Errorf("values[%d] = %q, want %q", i, values[i], w)
			}
		}
		// labels should have "(default)", "x:y", custom
		if len(labels) != 3 {
			t.Fatalf("label count mismatch: got %d want 3", len(labels))
		}
		if labels[0] != "(default)" {
			t.Errorf("labels[0] = %q, want (default)", labels[0])
		}
	})
}

// TestModelSelectorOptionsStaleSetup verifies the stale-provider scenario:
// current value is prepended because openrouter has no models map.
func TestModelSelectorOptionsStaleSetup(t *testing.T) {
	m := newModelSelectorTestModel(t)
	labels, values := m.modelSelectorOptions("openrouter:deepseek/deepseek-v4-flash")

	if len(values) == 0 {
		t.Fatal("expected non-empty options")
	}
	// values[0] == "" (default)
	if values[0] != "" {
		t.Errorf("values[0] = %q, want empty (default)", values[0])
	}
	// values[1] == current value (prepended because not in configured list)
	if values[1] != "openrouter:deepseek/deepseek-v4-flash" {
		t.Errorf("values[1] = %q, want openrouter:deepseek/deepseek-v4-flash", values[1])
	}

	// Check all expected models are present
	wantPresent := []string{
		"antigravity:antigravity-gemini-3.6-flash",
		"llmproxy:deepseek-flash",
		"llmproxy:h3",
		"llmproxy:luna-pro",
	}
	for _, w := range wantPresent {
		found := false
		for _, v := range values {
			if v == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in values %v", w, values)
		}
	}

	// Check relative order: llmproxy models should be deepseek-flash, h3, luna-pro
	llmproxyIdx := make([]int, 0)
	for i, v := range values {
		if strings.HasPrefix(v, "llmproxy:") {
			llmproxyIdx = append(llmproxyIdx, i)
		}
	}
	if len(llmproxyIdx) != 3 {
		t.Fatalf("expected 3 llmproxy models, got %d", len(llmproxyIdx))
	}
	if values[llmproxyIdx[0]] != "llmproxy:deepseek-flash" ||
		values[llmproxyIdx[1]] != "llmproxy:h3" ||
		values[llmproxyIdx[2]] != "llmproxy:luna-pro" {
		t.Errorf("llmproxy models not in expected order: %v", []string{
			values[llmproxyIdx[0]], values[llmproxyIdx[1]], values[llmproxyIdx[2]],
		})
	}

	// Last value == modelCustomValue
	if values[len(values)-1] != modelCustomValue {
		t.Errorf("last value = %q, want modelCustomValue", values[len(values)-1])
	}

	// Total: "(default)", the prepended current value, 1 antigravity model,
	// 3 llmproxy models, and the custom sentinel = 7.
	if len(values) != 7 {
		t.Errorf("expected 7 values, got %d: %v", len(values), values)
	}

	// Verify labels match values in length
	if len(labels) != len(values) {
		t.Errorf("labels/values length mismatch: %d vs %d", len(labels), len(values))
	}
}

// TestModelSelectorOptionsNoDuplicateWhenCurrentInList verifies that when the
// current value IS in the configured list, it appears exactly once.
func TestModelSelectorOptionsNoDuplicateWhenCurrentInList(t *testing.T) {
	m := newModelSelectorTestModel(t)
	labels, values := m.modelSelectorOptions("llmproxy:h3")

	count := 0
	for _, v := range values {
		if v == "llmproxy:h3" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected llmproxy:h3 exactly once, got %d times in values %v", count, values)
	}

	// "", "antigravity:...", "llmproxy:deepseek-flash", "llmproxy:h3",
	// "llmproxy:luna-pro", modelCustomValue = 6
	if len(values) != 6 {
		t.Errorf("expected 6 values, got %d: %v", len(values), values)
	}
	if len(labels) != len(values) {
		t.Errorf("labels/values length mismatch: %d vs %d", len(labels), len(values))
	}
}

// TestProviderSelectorOptionsIncludesStaleCurrent verifies that the provider
// selector includes the current value even when it's not in the configured
// providers.
func TestProviderSelectorOptionsIncludesStaleCurrent(t *testing.T) {
	m := newModelSelectorTestModel(t)
	labels, values := m.providerSelectorOptions("nanogpt")

	// values[0] == "" (default)
	if values[0] != "" {
		t.Errorf("values[0] = %q, want empty (default)", values[0])
	}
	// values[1] == "nanogpt" (stale current prepended)
	if values[1] != "nanogpt" {
		t.Errorf("values[1] = %q, want nanogpt", values[1])
	}

	// All three provider names should be present
	wantPresent := []string{"antigravity", "llmproxy", "openrouter"}
	for _, w := range wantPresent {
		found := false
		for _, v := range values {
			if v == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in values %v", w, values)
		}
	}

	// Last == modelCustomValue
	if values[len(values)-1] != modelCustomValue {
		t.Errorf("last value = %q, want modelCustomValue", values[len(values)-1])
	}

	if len(labels) != len(values) {
		t.Errorf("labels/values length mismatch: %d vs %d", len(labels), len(values))
	}
}

// TestDefaultsProviderEnterOpensSelector verifies that pressing Enter on the
// Provider field in the defaults view opens the selector with the stale
// current value pre-selected.
func TestDefaultsProviderEnterOpensSelector(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 0 // Provider
	m.handleDefaultsEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected selector active")
	}
	if m.settingsSelectorField != "defaultProvider" {
		t.Errorf("field = %q, want defaultProvider", m.settingsSelectorField)
	}

	// Pre-selected index should point to "nanogpt"
	nanogptIdx := -1
	for i, v := range m.settingsSelectorValues {
		if v == "nanogpt" {
			nanogptIdx = i
			break
		}
	}
	if nanogptIdx == -1 {
		t.Fatal("nanogpt not found in selector values")
	}
	if m.settingsSelectorIdx != nanogptIdx {
		t.Errorf("selectorIdx = %d, want %d (nanogpt)", m.settingsSelectorIdx, nanogptIdx)
	}
}

// TestDefaultsModelEnterOpensSelector verifies that pressing Enter on the
// Model field in the defaults view opens the selector with the current model
// pre-selected.
func TestDefaultsModelEnterOpensSelector(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 1 // Model
	m.handleDefaultsEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected selector active")
	}
	if m.settingsSelectorField != "defaultModel" {
		t.Errorf("field = %q, want defaultModel", m.settingsSelectorField)
	}

	// Pre-selected index should point to "openrouter:deepseek/deepseek-v4-flash"
	modelIdx := -1
	for i, v := range m.settingsSelectorValues {
		if v == "openrouter:deepseek/deepseek-v4-flash" {
			modelIdx = i
			break
		}
	}
	if modelIdx == -1 {
		t.Fatal("openrouter:deepseek/deepseek-v4-flash not found in selector values")
	}
	if m.settingsSelectorIdx != modelIdx {
		t.Errorf("selectorIdx = %d, want %d (openrouter:deepseek/deepseek-v4-flash)", m.settingsSelectorIdx, modelIdx)
	}
}

// TestAgentModelEnterOpensSelector verifies that pressing Enter on the Model
// field for a specific agent opens the selector with the agent's model
// pre-selected.
func TestAgentModelEnterOpensSelector(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")
	m.modalSelectedIdx = agentFieldModel // 4
	m.handleAgentEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected selector active")
	}
	if m.settingsSelectorField != "agentModel" {
		t.Errorf("field = %q, want agentModel", m.settingsSelectorField)
	}

	// Pre-selected index should point to "llmproxy:h3"
	modelIdx := -1
	for i, v := range m.settingsSelectorValues {
		if v == "llmproxy:h3" {
			modelIdx = i
			break
		}
	}
	if modelIdx == -1 {
		t.Fatal("llmproxy:h3 not found in selector values")
	}
	if m.settingsSelectorIdx != modelIdx {
		t.Errorf("selectorIdx = %d, want %d (llmproxy:h3)", m.settingsSelectorIdx, modelIdx)
	}
}

// TestCompactionModelEnterOpensSelector verifies that pressing Enter on the
// Compaction model field opens the selector with the current compaction model
// pre-selected.
func TestCompactionModelEnterOpensSelector(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.cfg.Session.CompactionModel = "llmproxy:luna-pro"
	m.settingsSection = "sys_0"
	m.modalSelectedIdx = 3
	m.handleSessionEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected selector active")
	}
	if m.settingsSelectorField != "compactionModel" {
		t.Errorf("field = %q, want compactionModel", m.settingsSelectorField)
	}

	// Pre-selected index should point to "llmproxy:luna-pro"
	modelIdx := -1
	for i, v := range m.settingsSelectorValues {
		if v == "llmproxy:luna-pro" {
			modelIdx = i
			break
		}
	}
	if modelIdx == -1 {
		t.Fatal("llmproxy:luna-pro not found in selector values")
	}
	if m.settingsSelectorIdx != modelIdx {
		t.Errorf("selectorIdx = %d, want %d (llmproxy:luna-pro)", m.settingsSelectorIdx, modelIdx)
	}
}

// TestSelectorConfirmSavesModel verifies that confirming a model selection
// in the selector saves it to config and persists to disk.
func TestSelectorConfirmSavesModel(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.modalMode = ModalSettingsAgentEdit
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 1 // Model
	m.handleDefaultsEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected selector active")
	}

	// Find the index of "llmproxy:luna-pro" in the selector values
	lunaIdx := -1
	for i, v := range m.settingsSelectorValues {
		if v == "llmproxy:luna-pro" {
			lunaIdx = i
			break
		}
	}
	if lunaIdx == -1 {
		t.Fatal("llmproxy:luna-pro not found in selector values")
	}
	m.settingsSelectorIdx = lunaIdx

	m.handleSelectorConfirm()

	if m.cfg.Agents.Defaults.Model != "llmproxy:luna-pro" {
		t.Errorf("model = %q, want llmproxy:luna-pro", m.cfg.Agents.Defaults.Model)
	}
	if m.settingsSelectorActive {
		t.Error("expected selector closed after confirm")
	}
	if m.settingsEditField != "" {
		t.Errorf("editField = %q, want empty", m.settingsEditField)
	}

	// Verify persistence
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Agents.Defaults.Model != "llmproxy:luna-pro" {
		t.Errorf("persisted model = %q, want llmproxy:luna-pro", reloaded.Agents.Defaults.Model)
	}
}

// TestSelectorConfirmCustomOpensTextInput verifies that selecting "(custom…)"
// opens the free-text input pre-filled with the original value.
func TestSelectorConfirmCustomOpensTextInput(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.modalMode = ModalSettingsAgentEdit
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 1 // Model
	m.handleDefaultsEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected selector active")
	}

	// Find the index of modelCustomValue
	customIdx := -1
	for i, v := range m.settingsSelectorValues {
		if v == modelCustomValue {
			customIdx = i
			break
		}
	}
	if customIdx == -1 {
		t.Fatal("modelCustomValue not found in selector values")
	}
	m.settingsSelectorIdx = customIdx

	m.handleSelectorConfirm()

	if m.settingsSelectorActive {
		t.Error("expected selector closed after custom select")
	}
	if m.settingsEditField != "defaultModel" {
		t.Errorf("editField = %q, want defaultModel", m.settingsEditField)
	}
	if !m.textInput.Focused() {
		t.Error("expected text input focused")
	}
	if m.textInput.Value() != "openrouter:deepseek/deepseek-v4-flash" {
		t.Errorf("textInput value = %q, want openrouter:deepseek/deepseek-v4-flash", m.textInput.Value())
	}
}

// TestSelectorConfirmCustomThenSave verifies the full flow: select custom,
// type a new value, and save.
func TestSelectorConfirmCustomThenSave(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.modalMode = ModalSettingsAgentEdit
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 1 // Model
	m.handleDefaultsEditEnter()

	// Select custom
	customIdx := -1
	for i, v := range m.settingsSelectorValues {
		if v == modelCustomValue {
			customIdx = i
			break
		}
	}
	if customIdx == -1 {
		t.Fatal("modelCustomValue not found")
	}
	m.settingsSelectorIdx = customIdx
	m.handleSelectorConfirm()

	// Now simulate the user typing a new value and submitting
	m.textInput.SetValue("openrouter:anthropic/claude-x")
	m.handleAgentSettingsInput("openrouter:anthropic/claude-x")

	if m.cfg.Agents.Defaults.Model != "openrouter:anthropic/claude-x" {
		t.Errorf("model = %q, want openrouter:anthropic/claude-x", m.cfg.Agents.Defaults.Model)
	}
}

// TestModelEnterNoProvidersWithCurrentValueOpensSelector verifies that when
// there are no configured providers but there IS a current value, the
// selector still opens (showing the current value + custom), because
// configSelectorOptions returns non-nil when currentValue is non-empty.
func TestModelEnterNoProvidersWithCurrentValueOpensSelector(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				Model:     "some-model",
				Provider:  "",
			},
		},
		Providers: &config.ProvidersConfig{},
	}
	if err := config.SaveConfig(config.DefaultConfigPath(), cfg); err != nil {
		t.Fatalf("saving initial config: %v", err)
	}
	ti := textinput.New()
	ti.Focus()
	m := &Model{cfg: cfg, textInput: ti}

	m.modalSelectedIdx = 1 // Model
	m.handleDefaultsEditEnter()

	// With a non-empty current value and no configured providers, the selector
	// still opens showing "(default)", the current value, and "(custom…)".
	if !m.settingsSelectorActive {
		t.Error("expected selector active (current value present)")
	}
	if m.settingsSelectorField != "defaultModel" {
		t.Errorf("field = %q, want defaultModel", m.settingsSelectorField)
	}
	// The current value should be in the selector values
	found := false
	for _, v := range m.settingsSelectorValues {
		if v == "some-model" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected some-model in selector values %v", m.settingsSelectorValues)
	}
}

// TestModelEnterNoProvidersNoCurrentValueShowsMinimalSelector verifies that when
// there are no configured providers AND no current value, the selector still
// opens with a minimal "(default)" + "(custom...)" set instead of falling back
// to text input. This ensures a consistent selector experience for model fields.
func TestModelEnterNoProvidersNoCurrentValueShowsMinimalSelector(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				Model:     "",
				Provider:  "",
			},
		},
		Providers: &config.ProvidersConfig{},
	}
	if err := config.SaveConfig(config.DefaultConfigPath(), cfg); err != nil {
		t.Fatalf("saving initial config: %v", err)
	}
	ti := textinput.New()
	ti.Focus()
	m := &Model{cfg: cfg, textInput: ti}

	m.modalSelectedIdx = 1 // Model
	m.handleDefaultsEditEnter()

	// No providers AND no current value → minimal selector with (default) + (custom...)
	if !m.settingsSelectorActive {
		t.Error("expected selector active with minimal options")
	}
	if m.settingsSelectorField != "defaultModel" {
		t.Errorf("selectorField = %q, want defaultModel", m.settingsSelectorField)
	}
	if len(m.settingsSelectorValues) != 2 {
		t.Fatalf("expected 2 selector values, got %d: %v", len(m.settingsSelectorValues), m.settingsSelectorValues)
	}
	if m.settingsSelectorValues[0] != "" || m.settingsSelectorValues[1] != "__custom__" {
		t.Errorf("unexpected selector values: %v", m.settingsSelectorValues)
	}
}
