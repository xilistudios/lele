package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func newModelSelectorTestModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	temp0 := 0.7
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				Provider:  "nanogpt",
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
				"openrouter": {Type: "openrouter"},
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
		if len(labels) != 3 {
			t.Fatalf("label count mismatch: got %d want 3", len(labels))
		}
		if labels[0] != "(default)" {
			t.Errorf("labels[0] = %q, want (default)", labels[0])
		}
	})
}

func TestModelSelectorOptionsStaleSetup(t *testing.T) {
	m := newModelSelectorTestModel(t)
	labels, values := m.modelSelectorOptions("openrouter:deepseek/deepseek-v4-flash")

	if len(values) == 0 {
		t.Fatal("expected non-empty options")
	}
	if values[0] != "" {
		t.Errorf("values[0] = %q, want empty (default)", values[0])
	}
	if values[1] != "openrouter:deepseek/deepseek-v4-flash" {
		t.Errorf("values[1] = %q, want openrouter:deepseek/deepseek-v4-flash", values[1])
	}

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

	if values[len(values)-1] != modelCustomValue {
		t.Errorf("last value = %q, want modelCustomValue", values[len(values)-1])
	}

	if len(values) != 7 {
		t.Errorf("expected 7 values, got %d: %v", len(values), values)
	}

	if len(labels) != len(values) {
		t.Errorf("labels/values length mismatch: %d vs %d", len(labels), len(values))
	}
}

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

	if len(values) != 6 {
		t.Errorf("expected 6 values, got %d: %v", len(values), values)
	}
	if len(labels) != len(values) {
		t.Errorf("labels/values length mismatch: %d vs %d", len(labels), len(values))
	}
}

func TestProviderSelectorOptionsIncludesStaleCurrent(t *testing.T) {
	m := newModelSelectorTestModel(t)
	labels, values := m.providerSelectorOptions("nanogpt")

	if values[0] != "" {
		t.Errorf("values[0] = %q, want empty (default)", values[0])
	}
	if values[1] != "nanogpt" {
		t.Errorf("values[1] = %q, want nanogpt", values[1])
	}

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

	if values[len(values)-1] != modelCustomValue {
		t.Errorf("last value = %q, want modelCustomValue", values[len(values)-1])
	}

	if len(labels) != len(values) {
		t.Errorf("labels/values length mismatch: %d vs %d", len(labels), len(values))
	}
}

func TestDefaultsProviderEnterOpensSelector(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 0
	m.handleDefaultsEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected selector active")
	}
	if m.settingsSelectorField != "defaultProvider" {
		t.Errorf("field = %q, want defaultProvider", m.settingsSelectorField)
	}

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

func TestDefaultsModelEnterOpensSelector(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 1
	m.handleDefaultsEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected selector active")
	}
	if m.settingsSelectorField != "defaultModel" {
		t.Errorf("field = %q, want defaultModel", m.settingsSelectorField)
	}

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

func TestAgentModelEnterOpensSelector(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")
	m.modalSelectedIdx = agentFieldModel
	m.handleAgentEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected selector active")
	}
	if m.settingsSelectorField != "agentModel" {
		t.Errorf("field = %q, want agentModel", m.settingsSelectorField)
	}

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

func TestSelectorConfirmSavesModel(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.modalMode = ModalSettingsAgentEdit
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 1
	m.handleDefaultsEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected selector active")
	}

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

	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Agents.Defaults.Model != "llmproxy:luna-pro" {
		t.Errorf("persisted model = %q, want llmproxy:luna-pro", reloaded.Agents.Defaults.Model)
	}
}

func TestSelectorConfirmCustomOpensTextInput(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.modalMode = ModalSettingsAgentEdit
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 1
	m.handleDefaultsEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected selector active")
	}

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

func TestSelectorConfirmCustomThenSave(t *testing.T) {
	m := newModelSelectorTestModel(t)
	m.modalMode = ModalSettingsAgentEdit
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 1
	m.handleDefaultsEditEnter()

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

	m.textInput.SetValue("openrouter:anthropic/claude-x")
	m.handleAgentSettingsInput("openrouter:anthropic/claude-x")

	if m.cfg.Agents.Defaults.Model != "openrouter:anthropic/claude-x" {
		t.Errorf("model = %q, want openrouter:anthropic/claude-x", m.cfg.Agents.Defaults.Model)
	}
}

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

	m.modalSelectedIdx = 1
	m.handleDefaultsEditEnter()

	if !m.settingsSelectorActive {
		t.Error("expected selector active (current value present)")
	}
	if m.settingsSelectorField != "defaultModel" {
		t.Errorf("field = %q, want defaultModel", m.settingsSelectorField)
	}
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

	m.modalSelectedIdx = 1
	m.handleDefaultsEditEnter()

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
