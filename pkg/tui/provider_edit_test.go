package tui

import (
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Tests for the "edit model" flow (ModalEditModel): pressing ENTER on a model
// row in the provider detail must OPEN a pre-filled edit form — never delete
// the model —, the form must save in place via updateModelInProvider (keeping
// fields the form does not ask about), ESC must return to the detail view, and
// the shared ENTER branch must not regress the add-model flow.
//
// The stored Model names are deliberately NOT in the embedded catalog so the
// catalog picker cannot prefill/clobber form values while walking the steps.

// newEditModelTestModel builds a Model whose "test-provider" provider holds
// two models; "gpt-4o" carries Temperature/Reasoning so edit-survival of
// fields outside the form can be asserted.
func newEditModelTestModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	if m.cfg.Providers == nil {
		m.cfg.Providers = &config.ProvidersConfig{}
	}
	temp := 0.7
	effort := "high"
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
					Temperature:   &temp,
					Reasoning:     &config.ReasoningConfig{Effort: &effort},
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

// openProviderDetail runs /providers and presses ENTER to land on the detail.
func openProviderDetail(m *Model) *Model {
	m.executeCommand("/providers")
	return sendKeys(m, "\r")
}

// selectModelRow positions the provider-detail cursor on the given alias row.
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

// TestProviderDetailEnterOpensEditDoesNotDelete is the regression test for
// the reported bug: ENTER on a model row used to DELETE the model. It must
// open the edit form pre-filled with the stored values instead.
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

	// Regression: BOTH models must still exist after ENTER.
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

	// The form is pre-filled with the stored values.
	wantValues := []string{"gpt-4o", "stored-gpt-4o-001", "128000", "4096", "no"}
	if len(m.formValues) != 5 {
		t.Fatalf("formValues len = %d, want 5: %v", len(m.formValues), m.formValues)
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

// TestProviderDetailEditSaveUpdatesInPlace walks the 5 steps (changing the
// context window along the way) and asserts the model is updated in place:
// the alias survives, Model/ContextWindow change, Vision stays, and the
// Temperature/Reasoning fields the form never asks about are preserved.
func TestProviderDetailEditSaveUpdatesInPlace(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	selectModelRow(t, m, "gpt-4o")
	m = sendKeys(m, "\r") // open edit form
	if m.modalMode != ModalEditModel {
		t.Fatalf("modal = %v, want ModalEditModel", m.modalMode)
	}

	// Step 0: alias unchanged.
	m = sendKeys(m, "\r")
	if m.formStepIndex != 1 {
		t.Fatalf("step = %d, want 1 after alias step", m.formStepIndex)
	}
	// Step 1: change the model name.
	m.textInput.SetValue("stored-gpt-4o-v2")
	m = sendKeys(m, "\r")
	if m.formStepIndex != 2 {
		t.Fatalf("step = %d, want 2 after model name step", m.formStepIndex)
	}
	// Step 2: change the context window.
	m.textInput.SetValue("200000")
	m = sendKeys(m, "\r")
	if m.formStepIndex != 3 {
		t.Fatalf("step = %d, want 3 after context window step", m.formStepIndex)
	}
	// Step 3: accept the prefilled max tokens.
	m = sendKeys(m, "\r")
	if m.formStepIndex != 4 {
		t.Fatalf("step = %d, want 4 after max tokens step", m.formStepIndex)
	}
	// Step 4: accept the prefilled vision answer → save.
	m = sendKeys(m, "\r")

	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail after save", m.modalMode)
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
	if mc.Reasoning == nil || mc.Reasoning.Effort == nil || *mc.Reasoning.Effort != "high" {
		t.Errorf("Reasoning = %+v, want effort high (preserved)", mc.Reasoning)
	}

	// The sibling model must be untouched.
	mini, ok := models["gpt-4o-mini"]
	if !ok {
		t.Fatalf("gpt-4o-mini missing, models = %v", keysOfModels(models))
	}
	if mini.Model != "stored-mini-001" || mini.ContextWindow != 64000 || !mini.Vision {
		t.Errorf("gpt-4o-mini changed: %+v", mini)
	}

	// The detail list cursor sits on the edited row.
	if alias, ok := m.providerDetailRows[m.modalSelectedIdx]; !ok || alias != "gpt-4o" {
		t.Errorf("detail cursor on idx %d (%q), want the edited row gpt-4o", m.modalSelectedIdx, alias)
	}
}

// TestProviderDetailEditRenameMovesAlias renames the alias on step 0 and
// verifies the old key is gone, the new key carries the updated values, and
// the detail view re-shows the renamed model.
func TestProviderDetailEditRenameMovesAlias(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	selectModelRow(t, m, "gpt-4o")
	m = sendKeys(m, "\r") // open edit form
	if m.modalMode != ModalEditModel {
		t.Fatalf("modal = %v, want ModalEditModel", m.modalMode)
	}

	// Step 0: rename the alias.
	m.textInput.SetValue("gpt-4o-latest")
	m = sendKeys(m, "\r")
	// Steps 1-4: accept the prefilled values (last Enter saves).
	for i := 0; i < 4; i++ {
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
	if len(models) != 2 {
		t.Errorf("models count = %d, want 2 (rename, not add/delete)", len(models))
	}

	// The detail view re-shows the renamed model.
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

// TestProviderDetailEditEscReturnsToDetailWithoutChanges verifies ESC leaves
// the edit form back to the provider detail with nothing modified.
func TestProviderDetailEditEscReturnsToDetailWithoutChanges(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	selectModelRow(t, m, "gpt-4o")
	m = sendKeys(m, "\r") // open edit form
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
	// Landed back on the row we came from.
	if alias, ok := m.providerDetailRows[m.modalSelectedIdx]; !ok || alias != "gpt-4o" {
		t.Errorf("detail cursor on idx %d (%q), want gpt-4o", m.modalSelectedIdx, alias)
	}
}

// TestAddModelFlowStillAdds guards the shared ENTER branch: from the detail
// view, the add-model action row (matched by providerDetailAddIdx, not by a
// hardcoded label) must still ADD a new model and close the modal.
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
	m = sendKeys(m, "\r") // step 0: alias
	m.textInput.SetValue("brand-new-001")
	m = sendKeys(m, "\r") // step 1: model name
	m.textInput.SetValue("50000")
	m = sendKeys(m, "\r") // step 2: context window
	m.textInput.SetValue("8192")
	m = sendKeys(m, "\r") // step 3: max tokens
	m.textInput.SetValue("yes")
	m = sendKeys(m, "\r") // step 4: vision → save

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
	// Existing models were not edited by the add flow.
	if got := models["gpt-4o"]; got.Model != "stored-gpt-4o-001" || got.ContextWindow != 128000 {
		t.Errorf("existing model changed by add flow: %+v", got)
	}
}

// TestProviderDetailEditInvalidNumberStaysInForm verifies a non-numeric
// context window keeps the user in the form with an error and never touches
// the config.
func TestProviderDetailEditInvalidNumberStaysInForm(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	selectModelRow(t, m, "gpt-4o")
	m = sendKeys(m, "\r") // open edit form
	m = sendKeys(m, "\r") // step 0 → 1
	m = sendKeys(m, "\r") // step 1 → 2 (context window)
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

	// Config untouched.
	models := m.cfg.Providers.Named["test-provider"].Models
	if len(models) != 2 {
		t.Fatalf("models count = %d, want 2 (unchanged)", len(models))
	}
	mc := models["gpt-4o"]
	if mc.Model != "stored-gpt-4o-001" || mc.ContextWindow != 128000 || mc.MaxTokens != 4096 || mc.Vision {
		t.Errorf("stored values changed by a rejected step: %+v", mc)
	}
}

// TestEditModelTitleIsLocalized verifies tui.editModel resolves to a real
// translation (not the raw key) in all three embedded locales and that the
// modal title uses it.
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

// keysOfModels returns the sorted-ish alias list for failure messages.
func keysOfModels(models map[string]config.ProviderModelConfig) []string {
	out := make([]string, 0, len(models))
	for k := range models {
		out = append(out, k)
	}
	return out
}
