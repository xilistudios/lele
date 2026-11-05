package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Tests for the "edit provider" flow (ModalEditProvider): a provider's type,
// API key and API base URL can be changed from the provider detail view —
// without losing its models, proxy or any other field the form does not ask
// about. The flow is reachable both from the "Edit provider" action row and
// from the "e" shortcut, it is masked on the API-key step, and ESC must leave
// the stored config untouched.

const (
	providerEditTestName = "my-proxy"
	providerEditTestKey  = "sk-1234567890abcdef"
)

// newProviderEditTestModel builds a Model with one configured provider that
// carries credentials a form field could clobber: a Proxy, an API key and a
// model. It is based on newTestModel (real agent loop) because the /providers
// list is built from the loop's snapshot.
func newProviderEditTestModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	m.cfg.Providers.Named = map[string]config.NamedProviderConfig{
		providerEditTestName: {
			Type: "openai",
			ProviderConfig: config.ProviderConfig{
				APIKey:  providerEditTestKey,
				APIBase: "https://api.example.com/v1",
				Proxy:   "http://proxy.internal:8080",
			},
			Models: map[string]config.ProviderModelConfig{
				"alpha": {Model: "model-alpha-1", ContextWindow: 128000, MaxTokens: 4096},
			},
		},
	}
	return m
}

// openProviderEditForm lands on the provider detail view and activates the
// "Edit provider" action row with ENTER.
func openProviderEditForm(t *testing.T, m *Model) *Model {
	t.Helper()
	m = openProviderDetail(m)
	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail", m.modalMode)
	}
	if m.providerDetailEditIdx < 0 || m.providerDetailEditIdx >= len(m.modalItems) {
		t.Fatalf("edit action row index = %d, items = %q", m.providerDetailEditIdx, m.modalItems)
	}
	m.modalSelectedIdx = m.providerDetailEditIdx
	m = sendKeys(m, "\r")
	if m.modalMode != ModalEditProvider {
		t.Fatalf("modal = %v, want ModalEditProvider after ENTER on the edit row", m.modalMode)
	}
	return m
}

// TestProviderDetailHasEditActionRow verifies the detail view exposes the
// provider-level edit action, between the models separator and the add-model
// row, and that its index never collides with a model row.
func TestProviderDetailHasEditActionRow(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderDetail(m)

	if got := m.modalItems[m.providerDetailEditIdx]; got != i18n.T("tui.editProviderAction") {
		t.Errorf("items[editIdx] = %q, want %q", got, i18n.T("tui.editProviderAction"))
	}
	if _, ok := m.providerDetailRows[m.providerDetailEditIdx]; ok {
		t.Error("the edit action row must not be mapped as a model row")
	}
	if m.providerDetailEditIdx >= m.providerDetailAddIdx || m.providerDetailAddIdx >= m.providerDetailDelIdx {
		t.Errorf("action rows out of order: edit=%d add=%d del=%d",
			m.providerDetailEditIdx, m.providerDetailAddIdx, m.providerDetailDelIdx)
	}
}

// TestProviderEditFormPreFillsTypeAndBaseButNotKey covers the initial state of
// the form: type and API base URL are seeded from the stored provider so
// Enter-through keeps them, while the API key step stays BLANK and masked (the
// stored secret must never be rendered back into the widget).
func TestProviderEditFormPreFillsTypeAndBaseButNotKey(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderEditForm(t, m)

	if m.providerSelectedName != providerEditTestName {
		t.Errorf("providerSelectedName = %q, want %q", m.providerSelectedName, providerEditTestName)
	}
	wantValues := []string{"openai", "", "https://api.example.com/v1"}
	if len(m.formValues) != len(wantValues) {
		t.Fatalf("formValues = %q, want %d entries", m.formValues, len(wantValues))
	}
	for i, want := range wantValues {
		if m.formValues[i] != want {
			t.Errorf("formValues[%d] = %q, want %q", i, m.formValues[i], want)
		}
	}
	if got := m.textInput.Value(); got != "openai" {
		t.Errorf("textInput = %q, want the stored type %q", got, "openai")
	}
	for _, v := range append([]string{m.textInput.Value()}, m.formValues...) {
		if strings.Contains(v, providerEditTestKey) {
			t.Fatalf("edit form pre-filled the stored API key: %q", v)
		}
	}

	// Moving to the API-key step must mask it, and the rendered frame must not
	// leak the stored key anywhere.
	m = sendKeys(m, "\r")
	if m.formStepIndex != 1 {
		t.Fatalf("formStepIndex = %d, want 1 (API key)", m.formStepIndex)
	}
	if !m.isSecretInputStep() {
		t.Error("the API-key step of the provider edit form must be a secret input step")
	}
	if !m.isSecretFormValue(1) {
		t.Error("step 1 must be masked in the completed-step list")
	}
	if got := m.textInput.Value(); got != "" {
		t.Errorf("textInput = %q, want empty on the API-key step", got)
	}
	if frame := m.View(); strings.Contains(frame, providerEditTestKey) {
		t.Errorf("the rendered provider edit form leaks the stored API key:\n%s", frame)
	}
}

// TestProviderEditSavesInPlaceKeepingUntouchedFields walks the whole form —
// new type, blank key (keep), new API base — and asserts the provider is
// updated in place: models and proxy survive, the untouched key is preserved,
// the result is persisted to disk and the user lands back on the detail view.
func TestProviderEditSavesInPlaceKeepingUntouchedFields(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderEditForm(t, m)

	// Step 0 — type: replace the stored one.
	m.textInput.SetValue("openrouter")
	m = sendKeys(m, "\r")
	// Step 1 — API key: blank means "keep the stored key".
	if strings.TrimSpace(m.textInput.Value()) != "" {
		t.Fatalf("API-key step started with %q, want blank", m.textInput.Value())
	}
	m = sendKeys(m, "\r")
	// Step 2 — API base: replace it, then ENTER saves.
	if got, want := m.textInput.Value(), "https://api.example.com/v1"; got != want {
		t.Fatalf("API-base step started with %q, want the stored base %q", got, want)
	}
	m.textInput.SetValue("https://new.example.com/v1")
	m = sendKeys(m, "\r")

	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail after saving", m.modalMode)
	}
	if got, want := m.providerFeedback, i18n.T("tui.providerUpdated"); got != want {
		t.Errorf("providerFeedback = %q, want %q", got, want)
	}

	provider, ok := m.cfg.Providers.Named[providerEditTestName]
	if !ok {
		t.Fatalf("provider %q disappeared from the config", providerEditTestName)
	}
	if provider.Type != "openrouter" {
		t.Errorf("Type = %q, want openrouter", provider.Type)
	}
	if provider.APIKey != providerEditTestKey {
		t.Errorf("APIKey = %q, want the stored key %q (blank means keep)", provider.APIKey, providerEditTestKey)
	}
	if provider.APIBase != "https://new.example.com/v1" {
		t.Errorf("APIBase = %q, want https://new.example.com/v1", provider.APIBase)
	}
	if provider.Proxy != "http://proxy.internal:8080" {
		t.Errorf("Proxy = %q, want it preserved", provider.Proxy)
	}
	if len(provider.Models) != 1 || provider.Models["alpha"].Model != "model-alpha-1" {
		t.Errorf("Models = %v, want the original alpha model", provider.Models)
	}

	// The detail list was rebuilt from the saved config.
	if len(m.modalItems) == 0 || m.modalItems[0] != "Type: openrouter" {
		t.Errorf("detail list not rebuilt from the saved config: %q", m.modalItems)
	}

	// And the change reached the disk, not just the in-memory config.
	saved, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading the saved config: %v", err)
	}
	if saved.Providers == nil || saved.Providers.Named == nil {
		t.Fatal("saved config has no named providers")
	}
	onDisk := saved.Providers.Named[providerEditTestName]
	if onDisk.Type != "openrouter" || onDisk.APIBase != "https://new.example.com/v1" {
		t.Errorf("on disk: Type=%q APIBase=%q, want openrouter / https://new.example.com/v1", onDisk.Type, onDisk.APIBase)
	}
	if onDisk.APIKey != providerEditTestKey {
		t.Errorf("on disk: APIKey = %q, want the stored key (blank means keep)", onDisk.APIKey)
	}
}

// TestProviderEditNewKeyReplacesStoredKey covers the other half of the "blank
// means keep" rule: a key that IS typed must replace the stored one.
func TestProviderEditNewKeyReplacesStoredKey(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderEditForm(t, m)

	m = sendKeys(m, "\r") // keep the type
	m.textInput.SetValue("sk-brand-new-key")
	m = sendKeys(m, "\r") // save the key, move to the base step
	m = sendKeys(m, "\r") // keep the base

	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail after saving", m.modalMode)
	}
	provider := m.cfg.Providers.Named[providerEditTestName]
	if provider.APIKey != "sk-brand-new-key" {
		t.Errorf("APIKey = %q, want sk-brand-new-key", provider.APIKey)
	}
	if provider.APIBase != "https://api.example.com/v1" {
		t.Errorf("APIBase = %q, want it preserved", provider.APIBase)
	}
}

// TestProviderEditEscKeepsStoredConfig verifies ESC is a pure discard: no field
// changes and the user lands back on the provider detail.
func TestProviderEditEscKeepsStoredConfig(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderEditForm(t, m)

	m.textInput.SetValue("anthropic")
	m = sendKeys(m, "\r") // stage the type change
	m.textInput.SetValue("would-be-key")
	m = sendKeys(m, "\x1b")

	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail after ESC", m.modalMode)
	}
	provider := m.cfg.Providers.Named[providerEditTestName]
	if provider.Type != "openai" || provider.APIKey != providerEditTestKey {
		t.Errorf("ESC changed the provider: Type=%q APIKey=%q", provider.Type, provider.APIKey)
	}
	if m.formError != "" {
		t.Errorf("formError = %q, want it cleared on ESC", m.formError)
	}
}

// TestProviderEditEShortcutOpensForm covers the "e" shortcut of the detail
// view, which must open the same form as the action row.
func TestProviderEditEShortcutOpensForm(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderDetail(m)

	m = sendKeys(m, "e")
	if m.modalMode != ModalEditProvider {
		t.Fatalf("modal = %v, want ModalEditProvider after \"e\"", m.modalMode)
	}
	if m.providerSelectedName != providerEditTestName {
		t.Errorf("providerSelectedName = %q, want %q", m.providerSelectedName, providerEditTestName)
	}
}

// TestProviderEditRequiresAType verifies the only required step: a blank type
// is rejected and the form stays open (an existing provider must never be
// saved with an empty identity).
func TestProviderEditRequiresAType(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderEditForm(t, m)

	m.textInput.SetValue("")
	m.formValues[0] = ""
	m = sendKeys(m, "\r")

	if m.modalMode != ModalEditProvider {
		t.Fatalf("modal = %v, want the form to stay open", m.modalMode)
	}
	if m.formError == "" {
		t.Error("a blank provider type must set formError")
	}
	if got := m.cfg.Providers.Named[providerEditTestName].Type; got != "openai" {
		t.Errorf("Type = %q, want the stored type untouched", got)
	}
}

// TestOpenProviderEditFlowUnknownProvider verifies a stale/nonexistent
// selection refuses to open the form and reports why instead of opening an
// empty editor.
func TestOpenProviderEditFlowUnknownProvider(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderDetail(m)
	m.providerSelectedName = "ghost"

	if m.openProviderEditFlow() {
		t.Fatal("openProviderEditFlow() = true for an unknown provider")
	}
	if m.modalMode != ModalProviderDetail {
		t.Errorf("modal = %v, want ModalProviderDetail", m.modalMode)
	}
	if !strings.Contains(m.providerFeedback, "ghost") {
		t.Errorf("providerFeedback = %q, want it to name the missing provider", m.providerFeedback)
	}
}

// TestUpdateProviderErrors covers the guard clauses of the single save point.
func TestUpdateProviderErrors(t *testing.T) {
	m := &Model{cfg: &config.Config{}}
	if err := m.updateProvider("x", "openai", "k", "b"); err == nil {
		t.Error("updateProvider with no providers config must fail")
	}

	m = newProviderEditTestModel(t)
	if err := m.updateProvider("ghost", "openai", "k", "b"); err == nil {
		t.Error("updateProvider on a missing provider must fail")
	}
	if got := m.cfg.Providers.Named[providerEditTestName].APIKey; got != providerEditTestKey {
		t.Errorf("a failed update touched another provider: APIKey = %q", got)
	}
}

// TestProviderEditFormRenders covers the painted surface: the modal title, the
// three steps and the keep-blank hint must reach the frame, so a user can see
// that a blank API key keeps the stored one.
func TestProviderEditFormRenders(t *testing.T) {
	m := newProviderEditTestModel(t)
	m.width, m.height = 100, 40
	m = openProviderEditForm(t, m)
	m = sendKeys(m, "\r") // land on the API-key step

	frame := m.View()
	for _, want := range []string{
		i18n.T("tui.editProvider"),
		"Provider type",
		"API Key",
		"API Base URL",
		i18n.T("tui.editProviderKeepHint"),
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("provider edit form frame missing %q:\n%s", want, frame)
		}
	}
}

// TestProviderDetailRendersEditActionRow verifies the action row is actually
// painted in the detail view (not just present in modalItems).
func TestProviderDetailRendersEditActionRow(t *testing.T) {
	m := newProviderEditTestModel(t)
	m.width, m.height = 100, 40
	m = openProviderDetail(m)

	frame := m.View()
	for _, want := range []string{
		i18n.T("tui.editProviderAction"),
		i18n.T("tui.providerDetailHints"),
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("provider detail frame missing %q:\n%s", want, frame)
		}
	}
}

// TestProviderEditHintsIgnoreStaleConnectState verifies the edit form does not
// inherit the /connect wording: a stale providerSavedInFlow flag from an
// earlier /connect run must not replace the plain next/cancel hint with the
// "ESC: skip model" one (the edit form has no model steps).
func TestProviderEditHintsIgnoreStaleConnectState(t *testing.T) {
	m := newProviderEditTestModel(t)
	m.width, m.height = 100, 40
	m = openProviderEditForm(t, m)
	m.providerSavedInFlow = true // stale state a previous /connect left behind

	frame := m.View()
	if !strings.Contains(frame, i18n.T("tui.formEnter")) {
		t.Errorf("edit form frame missing the next/cancel hint %q:\n%s", i18n.T("tui.formEnter"), frame)
	}
	if strings.Contains(frame, i18n.T("tui.connectModelStepsHint")) {
		t.Errorf("edit form frame shows the /connect model-steps hint:\n%s", frame)
	}
}
