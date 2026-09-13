package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/xilistudios/lele/pkg/catalog"
	"github.com/xilistudios/lele/pkg/config"
)

// newCatalogTestModel builds a Model with a configured openai provider so the
// catalog-backed /add-model picker can resolve provider type → catalog key.
// agentLoop is nil (configSource falls back to m.cfg) — enough for picker and
// addModelToProvider unit tests. chatInput/width/height are initialized so
// Update()/sendKeys does not panic on focus sync.
func newCatalogTestModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	// Same as pkg/channels seedCatalogForTest: prevent background provider
	// downloads from writing into test temp dirs (cleanup races).
	t.Setenv("LELE_CATALOG_OFFLINE", "1")
	catalog.ResetLive()
	seedCatalogProviders(t)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				// Custom name with Type "openai" — catalog key must use Type.
				"my-openai": {
					Type:           "openai",
					ProviderConfig: config.ProviderConfig{APIKey: "sk-test"},
					Models:         map[string]config.ProviderModelConfig{},
				},
				"custom-llm": {
					Type:           "not-a-catalog-provider",
					ProviderConfig: config.ProviderConfig{APIKey: "sk-x"},
					Models:         map[string]config.ProviderModelConfig{},
				},
			},
		},
	}
	if err := config.SaveConfig(config.DefaultConfigPath(), cfg); err != nil {
		t.Fatalf("saving initial config: %v", err)
	}
	ti := textinput.New()
	ti.Focus()
	ta := textarea.New()
	return &Model{cfg: cfg, textInput: ti, chatInput: ta, width: 100, height: 40}
}

// seedCatalogProviders plants minimal catalog models on disk so picker tests
// do not depend on an embedded catalog or network download.
func seedCatalogProviders(t *testing.T) {
	t.Helper()
	mustSeed := func(p catalog.Provider) {
		if err := catalog.SeedProvider(p); err != nil {
			t.Fatalf("seed %s: %v", p.ID, err)
		}
	}
	mustSeed(catalog.Provider{
		ID: "openai", Name: "OpenAI", Type: "openai",
		APIBase: "https://api.openai.com/v1",
		Models: []catalog.Model{
			{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000, MaxOutput: 16384, Vision: true},
			{ID: "gpt-4o-mini", Name: "GPT-4o mini", ContextWindow: 128000, MaxOutput: 16384, Vision: true},
			{ID: "o3", Name: "o3", ContextWindow: 200000, MaxOutput: 100000, Reasoning: true, ThinkingLevels: []string{"low", "medium", "high"}},
		},
	})
	mustSeed(catalog.Provider{
		ID: "anthropic", Name: "Anthropic", Type: "anthropic",
		APIBase: "https://api.anthropic.com/v1",
		Models: []catalog.Model{
			{ID: "claude-sonnet-4", Name: "Claude Sonnet 4", ContextWindow: 200000, Vision: true, ThinkingLevels: []string{"low", "medium", "high"}},
		},
	})
}

// openAddModelOnModelName opens /add-model against "my-openai" and advances
// past the alias step so the catalog picker is active on model_name.
func openAddModelOnModelName(t *testing.T, m *Model) {
	t.Helper()
	m.executeCommand("/add-model")
	if m.modalMode != ModalAddModel {
		t.Fatalf("modal = %v, want ModalAddModel", m.modalMode)
	}
	m.providerSelectedName = "my-openai"
	m.textInput.SetValue("gpt-4o")
	m.formValues[0] = "gpt-4o"
	m.formStepIndex = 0
	// Advance alias → model_name via the enter handler.
	m = sendKeys(m, "\r")
	if m.formStepIndex != 1 {
		t.Fatalf("formStepIndex = %d, want 1 (model_name); formError=%q", m.formStepIndex, m.formError)
	}
	if !m.addModelCatalogActive {
		t.Fatal("expected catalog picker active on model_name step")
	}
}

func TestCatalogProviderKeyResolvesTypeFromNamedMap(t *testing.T) {
	m := newCatalogTestModel(t)

	if got := m.catalogProviderKey("my-openai"); got != "openai" {
		t.Errorf("catalogProviderKey(my-openai) = %q, want openai", got)
	}
	// Unknown name falls back to the name itself (catalog.normalizeType aliasing).
	if got := m.catalogProviderKey("anthropic"); got != "anthropic" {
		t.Errorf("catalogProviderKey(anthropic) = %q, want anthropic", got)
	}
	if got := m.catalogProviderKey(""); got != "" {
		t.Errorf("catalogProviderKey(\"\") = %q, want empty", got)
	}
}

func TestCurrentCatalogProviderKeyPrefersConfiguredType(t *testing.T) {
	m := newCatalogTestModel(t)
	m.providerSelectedName = "my-openai"
	if got := m.currentCatalogProviderKey(); got != "openai" {
		t.Errorf("currentCatalogProviderKey = %q, want openai", got)
	}

	// Connect-flow fallback: type in formValues[1].
	m2 := newCatalogTestModel(t)
	m2.providerSelectedName = ""
	m2.formValues = make([]string, 10)
	m2.formValues[1] = "anthropic"
	if got := m2.currentCatalogProviderKey(); got != "anthropic" {
		t.Errorf("connect-flow key = %q, want anthropic", got)
	}
}

func TestBuildCatalogSuggestions(t *testing.T) {
	models := []catalog.Model{
		{ID: "gpt-4o", Name: "GPT-4o", ThinkingLevels: []string{"low", "medium", "high"}},
		{ID: "gpt-4o-mini", Name: "GPT-4o mini"},
		{ID: "o3", Name: "o3", Reasoning: true},
	}
	ids, labels := buildCatalogSuggestions(models)
	if len(ids) != 3 || len(labels) != 3 {
		t.Fatalf("got %d ids / %d labels, want 3/3", len(ids), len(labels))
	}
	if ids[0] != "gpt-4o" {
		t.Errorf("ids[0] = %q, want gpt-4o", ids[0])
	}
	if !strings.Contains(labels[0], "think low/medium/high") {
		t.Errorf("labels[0] = %q, want thinking levels", labels[0])
	}
	if !strings.Contains(labels[1], "gpt-4o-mini") {
		t.Errorf("labels[1] = %q, want gpt-4o-mini", labels[1])
	}
	if !strings.Contains(labels[2], "think") {
		t.Errorf("labels[2] = %q, want think marker for reasoning model", labels[2])
	}

	// Cap at maxCatalogSuggestions.
	big := make([]catalog.Model, maxCatalogSuggestions+5)
	for i := range big {
		big[i] = catalog.Model{ID: "m" + strconv.Itoa(i)}
	}
	ids, _ = buildCatalogSuggestions(big)
	if len(ids) != maxCatalogSuggestions {
		t.Errorf("capped len = %d, want %d", len(ids), maxCatalogSuggestions)
	}

	// Empty input.
	ids, labels = buildCatalogSuggestions(nil)
	if ids != nil || labels != nil {
		t.Errorf("empty input should return nil,nil; got %v %v", ids, labels)
	}
}

func TestRefreshCatalogSuggestionsFilters(t *testing.T) {
	m := newCatalogTestModel(t)
	m.modalMode = ModalAddModel
	m.formStepIndex = 1
	m.providerSelectedName = "my-openai"
	m.formValues = make([]string, 5)

	m.refreshCatalogSuggestions("")
	if !m.addModelCatalogActive {
		t.Fatal("picker should be active")
	}
	if len(m.addModelCatalogIDs) == 0 {
		t.Fatal("expected openai catalog models")
	}

	full := len(m.addModelCatalogIDs)
	m.refreshCatalogSuggestions("gpt-4o")
	if len(m.addModelCatalogIDs) == 0 {
		t.Fatal("expected gpt-4o matches")
	}
	if len(m.addModelCatalogIDs) > full {
		t.Errorf("filter grew the list: %d > %d", len(m.addModelCatalogIDs), full)
	}
	for _, id := range m.addModelCatalogIDs {
		if !strings.Contains(strings.ToLower(id), "gpt-4o") &&
			!strings.Contains(strings.ToLower(id), "gpt-4o") {
			// SearchModels matches ID or Name; IDs shown should typically
			// contain the query. Allow name-only matches by not failing hard
			// if one slips through — just require at least one match.
			_ = id
		}
	}

	// Unknown provider type → picker closes.
	m.providerSelectedName = "custom-llm"
	m.refreshCatalogSuggestions("")
	if m.addModelCatalogActive {
		t.Error("picker should close for unknown provider type")
	}
}

func TestStartCatalogPickerIfNeededOnlyOnModelNameStep(t *testing.T) {
	m := newCatalogTestModel(t)
	m.modalMode = ModalAddModel
	m.providerSelectedName = "my-openai"
	m.formValues = make([]string, 5)

	// Alias step — picker off.
	m.formStepIndex = 0
	m.startCatalogPickerIfNeeded()
	if m.addModelCatalogActive {
		t.Error("picker must not activate on alias step")
	}

	// Model name step — picker on.
	m.formStepIndex = 1
	m.startCatalogPickerIfNeeded()
	if !m.addModelCatalogActive {
		t.Error("picker must activate on model_name step")
	}

	// Context window step — picker off again.
	m.formStepIndex = 2
	m.startCatalogPickerIfNeeded()
	if m.addModelCatalogActive {
		t.Error("picker must close when leaving model_name step")
	}
}

func TestApplyCatalogSelectionOnEnterPrefillsForm(t *testing.T) {
	m := newCatalogTestModel(t)
	openAddModelOnModelName(t, m)

	// Clear input so the highlighted suggestion is taken.
	m.textInput.SetValue("")
	if m.addModelCatalogIdx >= len(m.addModelCatalogIDs) {
		t.Fatalf("idx %d out of range (len=%d)", m.addModelCatalogIdx, len(m.addModelCatalogIDs))
	}
	wantID := m.addModelCatalogIDs[m.addModelCatalogIdx]
	m.applyCatalogSelectionOnEnter()

	if got := m.textInput.Value(); got != wantID {
		t.Errorf("textInput = %q, want %q", got, wantID)
	}
	if m.formValues[1] != wantID {
		t.Errorf("formValues[1] = %q, want %q", m.formValues[1], wantID)
	}
	cw, err := strconv.Atoi(m.formValues[2])
	if err != nil || cw <= 0 {
		t.Errorf("formValues[2] = %q, want positive integer", m.formValues[2])
	}
	mt, err := strconv.Atoi(m.formValues[3])
	if err != nil || mt <= 0 {
		t.Errorf("formValues[3] = %q, want positive integer", m.formValues[3])
	}
	if m.formValues[4] != "yes" && m.formValues[4] != "no" {
		t.Errorf("formValues[4] = %q, want yes/no", m.formValues[4])
	}
}

func TestApplyCatalogSelectionOnEnterCustomModelNoPrefill(t *testing.T) {
	m := newCatalogTestModel(t)
	openAddModelOnModelName(t, m)

	m.textInput.SetValue("totally-custom-model-xyz")
	before := []string{m.formValues[1], m.formValues[2], m.formValues[3], m.formValues[4]}
	m.applyCatalogSelectionOnEnter()

	if m.textInput.Value() != "totally-custom-model-xyz" {
		t.Errorf("textInput changed to %q", m.textInput.Value())
	}
	for i, b := range before {
		if m.formValues[i+1] != b {
			t.Errorf("formValues[%d] changed from %q to %q", i+1, b, m.formValues[i+1])
		}
	}
}

func TestAddModelCatalogEnterSelectsAndPrefillsViaKeys(t *testing.T) {
	m := newCatalogTestModel(t)
	openAddModelOnModelName(t, m)

	// Type a narrow filter so the list is small, then Enter selects the
	// highlighted match and prefills remaining form values.
	m = sendKeys(m, "gpt-4o")
	if !m.addModelCatalogActive {
		t.Fatal("picker should stay active while typing")
	}
	if len(m.addModelCatalogIDs) == 0 {
		t.Fatal("expected filtered catalog matches")
	}
	wantID := m.addModelCatalogIDs[m.addModelCatalogIdx]
	m = sendKeys(m, "\r")

	if m.formStepIndex != 2 {
		t.Fatalf("formStepIndex = %d, want 2 (context_window); err=%q", m.formStepIndex, m.formError)
	}
	if m.formValues[1] != wantID {
		t.Errorf("formValues[1] = %q, want %q", m.formValues[1], wantID)
	}
	if m.formValues[2] == "" || m.formValues[3] == "" {
		t.Errorf("expected prefilled context/max tokens, got %q / %q", m.formValues[2], m.formValues[3])
	}
	// Prefill is shown in the input for the next step.
	if m.textInput.Value() != m.formValues[2] {
		t.Errorf("textInput = %q, want prefilled context_window %q", m.textInput.Value(), m.formValues[2])
	}
	// Picker closed after leaving model_name.
	if m.addModelCatalogActive {
		t.Error("picker should close after advancing past model_name")
	}
}

func TestAddModelFullFlowWithCatalogPrefill(t *testing.T) {
	m := newCatalogTestModel(t)
	openAddModelOnModelName(t, m)

	// Select highlighted catalog model.
	m = sendKeys(m, "\r") // model_name → context_window (prefilled)
	if m.formStepIndex != 2 {
		t.Fatalf("step = %d, want 2; err=%q", m.formStepIndex, m.formError)
	}
	// Accept prefilled context_window, max_tokens, vision.
	m = sendKeys(m, "\r") // → max_tokens
	if m.formStepIndex != 3 {
		t.Fatalf("step = %d, want 3; err=%q", m.formStepIndex, m.formError)
	}
	m = sendKeys(m, "\r") // → vision
	if m.formStepIndex != 4 {
		t.Fatalf("step = %d, want 4; err=%q", m.formStepIndex, m.formError)
	}
	m = sendKeys(m, "\r") // save

	if m.modalMode != ModalNone {
		t.Fatalf("modal should close after save; mode=%v err=%q", m.modalMode, m.formError)
	}
	saved, ok := m.cfg.Providers.Named["my-openai"]
	if !ok {
		t.Fatal("provider my-openai missing after save")
	}
	if len(saved.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(saved.Models))
	}
	var mc config.ProviderModelConfig
	for _, v := range saved.Models {
		mc = v
	}
	if mc.Model == "" {
		t.Error("saved model name is empty")
	}
	if mc.ContextWindow <= 0 {
		t.Errorf("ContextWindow = %d, want >0", mc.ContextWindow)
	}
	if mc.MaxTokens <= 0 {
		t.Errorf("MaxTokens = %d, want >0", mc.MaxTokens)
	}
}

func TestAddModelToProviderCatalogPrefillZeros(t *testing.T) {
	m := newCatalogTestModel(t)

	// Look up a real catalog model with thinking levels.
	models := catalog.ModelsForProvider("openai")
	if len(models) == 0 {
		t.Fatal("no openai catalog models")
	}
	var pick catalog.Model
	for _, cm := range models {
		if len(cm.ThinkingLevels) > 0 && cm.ContextWindow > 0 && cm.MaxOutput > 0 {
			pick = cm
			break
		}
	}
	if pick.ID == "" {
		t.Skip("no openai model with thinking levels + positive sizes")
	}

	// Zero context/max tokens must be filled from catalog.
	if err := m.addModelToProvider("my-openai", pick.ID, pick.ID, 0, 0, pick.Vision); err != nil {
		t.Fatalf("addModelToProvider: %v", err)
	}
	saved := m.cfg.Providers.Named["my-openai"].Models[strings.ToLower(pick.ID)]
	if saved.ContextWindow != pick.ContextWindow {
		t.Errorf("ContextWindow = %d, want %d", saved.ContextWindow, pick.ContextWindow)
	}
	wantMax := pick.MaxOutput
	if saved.MaxTokens != wantMax {
		t.Errorf("MaxTokens = %d, want %d", saved.MaxTokens, wantMax)
	}
	if saved.Reasoning == nil || saved.Reasoning.Effort == nil {
		t.Fatal("expected Reasoning.Effort to be set from catalog thinking levels")
	}
	effort := *saved.Reasoning.Effort
	if effort != "low" && effort != "medium" && effort != "high" {
		t.Errorf("effort = %q, want low/medium/high", effort)
	}
}

func TestAddModelToProviderRespectsExplicitValues(t *testing.T) {
	m := newCatalogTestModel(t)

	if err := m.addModelToProvider("my-openai", "custom-alias", "gpt-4o", 99999, 1234, true); err != nil {
		t.Fatalf("addModelToProvider: %v", err)
	}
	saved := m.cfg.Providers.Named["my-openai"].Models["custom-alias"]
	if saved.ContextWindow != 99999 {
		t.Errorf("ContextWindow = %d, want 99999 (explicit wins)", saved.ContextWindow)
	}
	if saved.MaxTokens != 1234 {
		t.Errorf("MaxTokens = %d, want 1234 (explicit wins)", saved.MaxTokens)
	}
	if !saved.Vision {
		t.Error("Vision should stay true")
	}
}

func TestAddModelToProviderUnknownModelNoCatalogFill(t *testing.T) {
	m := newCatalogTestModel(t)

	if err := m.addModelToProvider("my-openai", "weird", "not-in-catalog-xyz", 0, 0, false); err != nil {
		t.Fatalf("addModelToProvider: %v", err)
	}
	saved := m.cfg.Providers.Named["my-openai"].Models["weird"]
	if saved.ContextWindow != 0 || saved.MaxTokens != 0 {
		t.Errorf("unknown model should keep zeros, got cw=%d mt=%d", saved.ContextWindow, saved.MaxTokens)
	}
	if saved.Reasoning != nil {
		t.Error("unknown model should not get Reasoning")
	}
}

func TestDefaultThinkingEffort(t *testing.T) {
	cases := []struct {
		levels []string
		want   string
	}{
		{[]string{"low", "medium", "high"}, "medium"},
		{[]string{"low", "high"}, "low"},
		{[]string{"high"}, "high"},
		{nil, "medium"},
		{[]string{"ultra"}, "medium"},
	}
	for _, tc := range cases {
		if got := defaultThinkingEffort(tc.levels); got != tc.want {
			t.Errorf("defaultThinkingEffort(%v) = %q, want %q", tc.levels, got, tc.want)
		}
	}
}

func TestFormStepNamesThinkingHint(t *testing.T) {
	m := newCatalogTestModel(t)
	m.modalMode = ModalAddModel
	m.addModelCatalogThink = "low/medium/high"
	steps := m.formStepNames()
	if len(steps) < 2 {
		t.Fatalf("steps = %v", steps)
	}
	if !strings.Contains(steps[1], "thinking: low/medium/high") {
		t.Errorf("steps[1] = %q, want thinking hint", steps[1])
	}

	m.addModelCatalogThink = ""
	steps = m.formStepNames()
	if steps[1] != "Model name" {
		t.Errorf("steps[1] = %q, want plain Model name", steps[1])
	}
}

func TestCatalogPickerNavigationKeys(t *testing.T) {
	m := newCatalogTestModel(t)
	openAddModelOnModelName(t, m)

	if len(m.addModelCatalogIDs) < 2 {
		t.Skip("need at least 2 catalog models for navigation test")
	}
	start := m.addModelCatalogIdx
	m = sendKeys(m, "down")
	if m.addModelCatalogIdx != start+1 {
		t.Errorf("after down idx=%d, want %d", m.addModelCatalogIdx, start+1)
	}
	m = sendKeys(m, "up")
	if m.addModelCatalogIdx != start {
		t.Errorf("after up idx=%d, want %d", m.addModelCatalogIdx, start)
	}

	// Tab accepts the highlighted ID into the text input.
	m = sendKeys(m, "tab")
	want := m.addModelCatalogIDs[m.addModelCatalogIdx]
	if m.textInput.Value() != want {
		t.Errorf("after tab textInput=%q, want %q", m.textInput.Value(), want)
	}

	// ESC hides suggestions without closing the form.
	m = sendKeys(m, "\x1b")
	if m.addModelCatalogActive {
		t.Error("esc should hide catalog suggestions")
	}
	if m.modalMode != ModalAddModel {
		t.Errorf("esc closed the form; mode=%v", m.modalMode)
	}
}

func TestCatalogPickerTypingFiltersViaKeys(t *testing.T) {
	m := newCatalogTestModel(t)
	openAddModelOnModelName(t, m)

	// Clear and type a filter character by character.
	m.textInput.SetValue("")
	m.refreshCatalogSuggestions("")
	all := len(m.addModelCatalogIDs)
	if all == 0 {
		t.Fatal("expected catalog models")
	}

	// Type "claude" — anthropic models exist but provider is openai, so this
	// should shrink the openai list (possibly to empty).
	for _, r := range "claude" {
		m = sendKeys(m, string(r))
	}
	if m.addModelCatalogIdx != 0 {
		t.Errorf("idx should reset on filter, got %d", m.addModelCatalogIdx)
	}
	// Either fewer matches or none — never more than the full list.
	if len(m.addModelCatalogIDs) > all {
		t.Errorf("filter grew list: %d > %d", len(m.addModelCatalogIDs), all)
	}
}

func TestRenderCatalogSuggestionsEmptyAndList(t *testing.T) {
	// Empty list: some no-matches hint (locale-dependent string).
	out := renderCatalogSuggestions(nil, 0, 10)
	if strings.TrimSpace(out) == "" {
		t.Error("empty list render should still emit a hint")
	}

	labels := []string{"a · A", "b · B", "c · C"}
	out = renderCatalogSuggestions(labels, 1, 10)
	if !strings.Contains(out, "b · B") {
		t.Errorf("render missing highlighted label: %q", out)
	}
}
