package tui

import (
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func newEditModelTestModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	if m.cfg.Providers == nil {
		m.cfg.Providers = &config.ProvidersConfig{}
	}
	m.cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"test-provider": {
			Type: "openai",
			ProviderConfig: config.ProviderConfig{
				APIKey:  "sk-secret-key-1234567890",
				APIBase: "https://api.openai.com/v1",
			},
			Models: map[string]config.ProviderModelConfig{
				"gpt-4o": {
					Model:         "stored-gpt-4o-001",
					ContextWindow: 128000,
					MaxTokens:     4096,
					Vision:        false,
					Video:         true,
					Temperature:   ptrFloat(0.7),
					Reasoning:     fullyLoadedReasoning(),
				},
				"gpt-4o-mini": {
					Model:         "stored-mini-001",
					ContextWindow: 64000,
					MaxTokens:     2048,
					Vision:        true,
				},
			},
		},
	}
	return m
}

func fullyLoadedReasoning() *config.ReasoningConfig {
	effort := "high"
	maxTokens := 2048
	exclude := true
	summary := "detailed"
	return &config.ReasoningConfig{
		Enable:    true,
		Effort:    &effort,
		MaxTokens: &maxTokens,
		Exclude:   &exclude,
		Summary:   &summary,
	}
}

func ptrFloat(v float64) *float64 { return &v }

func assertFullyLoadedReasoning(t *testing.T, rc *config.ReasoningConfig) {
	t.Helper()
	if rc == nil {
		t.Fatal("Reasoning = nil, want the fully-loaded config")
	}
	if !rc.Enable {
		t.Error("Reasoning.Enable = false, want true")
	}
	if rc.Effort == nil || *rc.Effort != "high" {
		t.Errorf("Reasoning.Effort = %v, want high", rc.Effort)
	}
	if rc.MaxTokens == nil || *rc.MaxTokens != 2048 {
		t.Errorf("Reasoning.MaxTokens = %v, want 2048", rc.MaxTokens)
	}
	if rc.Exclude == nil || *rc.Exclude != true {
		t.Errorf("Reasoning.Exclude = %v, want true", rc.Exclude)
	}
	if rc.Summary == nil || *rc.Summary != "detailed" {
		t.Errorf("Reasoning.Summary = %v, want detailed", rc.Summary)
	}
}

func openProviderDetail(m *Model) *Model {
	m.executeCommand("/providers")
	return sendKeys(m, "\r")
}

func selectModelRow(t *testing.T, m *Model, alias string) {
	t.Helper()
	for idx, a := range m.providerDetailRows {
		if a == alias {
			m.modalSelectedIdx = idx
			return
		}
	}
	t.Fatalf("no detail row for alias %q (rows=%v)", alias, m.providerDetailRows)
}

func TestProviderDetailEnterOpensEditDoesNotDelete(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail", m.modalMode)
	}

	selectModelRow(t, m, "gpt-4o")
	m = sendKeys(m, "\r")

	if m.modalMode != ModalEditModel {
		t.Fatalf("modal = %v, want ModalEditModel after ENTER on a model row", m.modalMode)
	}

	models := m.cfg.Providers.Named["test-provider"].Models
	if len(models) != 2 {
		t.Fatalf("models count = %d, want 2 (ENTER must edit, not delete): %v", len(models), models)
	}
	if _, ok := models["gpt-4o"]; !ok {
		t.Fatal(`model "gpt-4o" was deleted by ENTER`)
	}
	if _, ok := models["gpt-4o-mini"]; !ok {
		t.Fatal(`model "gpt-4o-mini" was deleted by ENTER`)
	}

	wantValues := []string{
		"gpt-4o", "stored-gpt-4o-001", "128000", "4096", "no", "auto",
		"yes", "high", "2048", "yes", "detailed",
	}
	if len(m.formValues) != editModelStepCount {
		t.Fatalf("formValues len = %d, want %d: %v", len(m.formValues), editModelStepCount, m.formValues)
	}
	for i, want := range wantValues {
		if m.formValues[i] != want {
			t.Errorf("formValues[%d] = %q, want %q", i, m.formValues[i], want)
		}
	}
	if got := m.textInput.Value(); got != "gpt-4o" {
		t.Errorf("textInput = %q, want alias %q", got, "gpt-4o")
	}
	if m.modelEditAlias != "gpt-4o" {
		t.Errorf("modelEditAlias = %q, want gpt-4o", m.modelEditAlias)
	}
}

func TestProviderDetailEditSaveUpdatesInPlace(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	selectModelRow(t, m, "gpt-4o")
	m = sendKeys(m, "\r")
	if m.modalMode != ModalEditModel {
		t.Fatalf("modal = %v, want ModalEditModel", m.modalMode)
	}

	m = sendKeys(m, "\r")
	if m.formStepIndex != 1 {
		t.Fatalf("step = %d, want 1 after alias step", m.formStepIndex)
	}
	m.textInput.SetValue("stored-gpt-4o-v2")
	m = sendKeys(m, "\r")
	if m.formStepIndex != 2 {
		t.Fatalf("step = %d, want 2 after model name step", m.formStepIndex)
	}
	m.textInput.SetValue("200000")
	m = sendKeys(m, "\r")
	if m.formStepIndex != 3 {
		t.Fatalf("step = %d, want 3 after context window step", m.formStepIndex)
	}
	m = sendKeys(m, "\r")
	if m.formStepIndex != 4 {
		t.Fatalf("step = %d, want 4 after max tokens step", m.formStepIndex)
	}
	m = sendKeys(m, "\r")
	if m.formStepIndex != 5 {
		t.Fatalf("step = %d, want 5 after vision step", m.formStepIndex)
	}
	m.textInput.SetValue("deepseek")
	m = sendKeys(m, "\r")
	if m.formStepIndex != editModelReasoningEnableStep {
		t.Fatalf("step = %d, want the reasoning-enabled step (%d)", m.formStepIndex, editModelReasoningEnableStep)
	}
	for step := editModelReasoningEnableStep; step < editModelStepCount; step++ {
		if m.formStepIndex != step {
			t.Fatalf("step = %d, want %d (formError = %q)", m.formStepIndex, step, m.formError)
		}
		m = sendKeys(m, "\r")
	}

	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail after save (formError = %q)", m.modalMode, m.formError)
	}

	models := m.cfg.Providers.Named["test-provider"].Models
	mc, ok := models["gpt-4o"]
	if !ok {
		t.Fatalf("alias gpt-4o must survive the edit, models = %v", keysOfModels(models))
	}
	if mc.Model != "stored-gpt-4o-v2" {
		t.Errorf("Model = %q, want stored-gpt-4o-v2", mc.Model)
	}
	if mc.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", mc.ContextWindow)
	}
	if mc.Vision {
		t.Error("Vision = true, want unchanged false")
	}
	if mc.Temperature == nil || *mc.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7 (preserved)", mc.Temperature)
	}
	assertFullyLoadedReasoning(t, mc.Reasoning)
	if mc.ThinkingType != "deepseek" {
		t.Errorf("ThinkingType = %q, want deepseek (edited)", mc.ThinkingType)
	}

	mini, ok := models["gpt-4o-mini"]
	if !ok {
		t.Fatalf("gpt-4o-mini missing, models = %v", keysOfModels(models))
	}
	if mini.Model != "stored-mini-001" || mini.ContextWindow != 64000 || !mini.Vision {
		t.Errorf("gpt-4o-mini changed: %+v", mini)
	}

	if alias, ok := m.providerDetailRows[m.modalSelectedIdx]; !ok || alias != "gpt-4o" {
		t.Errorf("detail cursor on idx %d (%q), want the edited row gpt-4o", m.modalSelectedIdx, alias)
	}
}

func TestProviderDetailEditRenameMovesAlias(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	selectModelRow(t, m, "gpt-4o")
	m = sendKeys(m, "\r")
	if m.modalMode != ModalEditModel {
		t.Fatalf("modal = %v, want ModalEditModel", m.modalMode)
	}

	m.textInput.SetValue("gpt-4o-latest")
	m = sendKeys(m, "\r")
	for step := addModelModelNameStep; step < editModelStepCount; step++ {
		if m.formStepIndex != step {
			t.Fatalf("step = %d, want %d (formError = %q)", m.formStepIndex, step, m.formError)
		}
		m = sendKeys(m, "\r")
	}

	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail after save", m.modalMode)
	}

	models := m.cfg.Providers.Named["test-provider"].Models
	if _, ok := models["gpt-4o"]; ok {
		t.Error(`old alias "gpt-4o" must be removed after rename`)
	}
	mc, ok := models["gpt-4o-latest"]
	if !ok {
		t.Fatalf("renamed alias missing, models = %v", keysOfModels(models))
	}
	if mc.Model != "stored-gpt-4o-001" || mc.ContextWindow != 128000 || mc.MaxTokens != 4096 || mc.Vision {
		t.Errorf("renamed entry = %+v, want stored values carried over", mc)
	}
	assertFullyLoadedReasoning(t, mc.Reasoning)
	if len(models) != 2 {
		t.Errorf("models count = %d, want 2 (rename, not add/delete)", len(models))
	}

	found := false
	for _, item := range m.modalItems {
		if item == "  gpt-4o-latest" {
			found = true
		}
	}
	if !found {
		t.Fatalf("detail view missing renamed model, items = %v", m.modalItems)
	}
	if alias, ok := m.providerDetailRows[m.modalSelectedIdx]; !ok || alias != "gpt-4o-latest" {
		t.Errorf("detail cursor on idx %d (%q), want gpt-4o-latest", m.modalSelectedIdx, alias)
	}
}

func TestProviderDetailEditEscReturnsToDetailWithoutChanges(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	selectModelRow(t, m, "gpt-4o")
	m = sendKeys(m, "\r")
	if m.modalMode != ModalEditModel {
		t.Fatalf("modal = %v, want ModalEditModel", m.modalMode)
	}

	m = sendKeys(m, "\x1b")
	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail after ESC", m.modalMode)
	}

	models := m.cfg.Providers.Named["test-provider"].Models
	if len(models) != 2 {
		t.Fatalf("models count = %d, want 2 (unchanged)", len(models))
	}
	mc := models["gpt-4o"]
	if mc.Model != "stored-gpt-4o-001" || mc.ContextWindow != 128000 || mc.MaxTokens != 4096 || mc.Vision {
		t.Errorf("stored values changed by open+ESC: %+v", mc)
	}
	if alias, ok := m.providerDetailRows[m.modalSelectedIdx]; !ok || alias != "gpt-4o" {
		t.Errorf("detail cursor on idx %d (%q), want gpt-4o", m.modalSelectedIdx, alias)
	}
}

func TestAddModelFlowStillAdds(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail", m.modalMode)
	}

	idx := m.providerDetailAddIdx
	if idx < 0 || idx >= len(m.modalItems) {
		t.Fatalf("providerDetailAddIdx = %d out of range for items %v", idx, m.modalItems)
	}
	if got := m.modalItems[idx]; got != i18n.T("tui.addModelAction") {
		t.Fatalf(`items[providerDetailAddIdx] = %q, want %q`, got, i18n.T("tui.addModelAction"))
	}
	m.modalSelectedIdx = idx
	m = sendKeys(m, "\r")
	if m.modalMode != ModalAddModel {
		t.Fatalf("modal = %v, want ModalAddModel after + Add model", m.modalMode)
	}

	m.textInput.SetValue("new-model")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("brand-new-001")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("50000")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("8192")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("yes")
	m = sendKeys(m, "\r")
	m.textInput.SetValue("deepseek")
	m = sendKeys(m, "\r")

	if m.modalMode != ModalNone {
		t.Fatalf("modal = %v, want ModalNone (add flow closes the modal)", m.modalMode)
	}

	models := m.cfg.Providers.Named["test-provider"].Models
	if len(models) != 3 {
		t.Fatalf("models count = %d, want 3 (2 existing + 1 added): %v", len(models), keysOfModels(models))
	}
	mc, ok := models["new-model"]
	if !ok {
		t.Fatalf(`added model "new-model" missing, models = %v`, keysOfModels(models))
	}
	if mc.Model != "brand-new-001" || mc.ContextWindow != 50000 || mc.MaxTokens != 8192 || !mc.Vision {
		t.Errorf("added entry = %+v, want brand-new-001/50000/8192/vision", mc)
	}
	if mc.ThinkingType != "deepseek" {
		t.Errorf("ThinkingType = %q, want deepseek", mc.ThinkingType)
	}
	if got := models["gpt-4o"]; got.Model != "stored-gpt-4o-001" || got.ContextWindow != 128000 {
		t.Errorf("existing model changed by add flow: %+v", got)
	}
}

func TestProviderDetailEditInvalidNumberStaysInForm(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	selectModelRow(t, m, "gpt-4o")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")
	if m.formStepIndex != 2 {
		t.Fatalf("step = %d, want 2 (context window)", m.formStepIndex)
	}

	m.textInput.SetValue("abc")
	m = sendKeys(m, "\r")

	if m.modalMode != ModalEditModel {
		t.Fatalf("modal = %v, want ModalEditModel after invalid number", m.modalMode)
	}
	if m.formError == "" {
		t.Fatal("formError must be set for a non-numeric context window")
	}

	models := m.cfg.Providers.Named["test-provider"].Models
	if len(models) != 2 {
		t.Fatalf("models count = %d, want 2 (unchanged)", len(models))
	}
	mc := models["gpt-4o"]
	if mc.Model != "stored-gpt-4o-001" || mc.ContextWindow != 128000 || mc.MaxTokens != 4096 || mc.Vision {
		t.Errorf("stored values changed by a rejected step: %+v", mc)
	}
}

func TestEditModelTitleIsLocalized(t *testing.T) {
	m := &Model{}
	prev := i18n.GetLanguage()
	defer i18n.SetLanguage(prev)

	for _, lang := range []string{"en", "es", "pt"} {
		i18n.SetLanguage(lang)
		if got := i18n.T("tui.editModel"); got == "tui.editModel" || got == "" {
			t.Errorf("locale %q: tui.editModel unresolved, got %q", lang, got)
		}
		got := m.modalTitleFor(ModalEditModel)
		if got == "" {
			t.Errorf("locale %q: ModalEditModel title is empty", lang)
		}
		if got == "tui.editModel" {
			t.Errorf("locale %q: title is the raw key %q", lang, got)
		}
		if want := i18n.T("tui.editModel"); got != want {
			t.Errorf("locale %q: title = %q, want %q", lang, got, want)
		}
	}
}

func keysOfModels(models map[string]config.ProviderModelConfig) []string {
	out := make([]string, 0, len(models))
	for k := range models {
		out = append(out, k)
	}
	return out
}
