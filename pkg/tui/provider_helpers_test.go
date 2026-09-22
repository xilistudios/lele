package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// newProviderHelpersTestModel builds a Model with a provider holding two
// models (alpha carries Temperature/Reasoning so edit-survival can be
// asserted) and a provider with no models. agentLoop is nil so
// configSource() falls back to m.cfg, matching the settings selector tests.
func newProviderHelpersTestModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	temp := 0.7
	effort := "high"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"myprov": {
					Type: "openai",
					ProviderConfig: config.ProviderConfig{
						APIKey:  "sk-1234567890abcdef",
						APIBase: "https://api.example.com/v1",
					},
					Models: map[string]config.ProviderModelConfig{
						"alpha": {
							Model:         "model-alpha-1",
							ContextWindow: 128000,
							MaxTokens:     4096,
							Temperature:   &temp,
							Vision:        false,
							Reasoning:     &config.ReasoningConfig{Effort: &effort},
						},
						"beta": {
							Model:         "model-beta-1",
							ContextWindow: 32000,
							MaxTokens:     2048,
						},
					},
				},
				"emptyprov": {
					Type:   "openai",
					Models: map[string]config.ProviderModelConfig{},
				},
			},
		},
	}
	if err := config.SaveConfig(config.DefaultConfigPath(), cfg); err != nil {
		t.Fatalf("saving initial config: %v", err)
	}
	return &Model{cfg: cfg}
}

// TestProviderDetailListRowsAndModelRows verifies the detail list content is
// byte-identical to the previously inlined blocks and that modelRows maps
// exactly the model-row indices (no separators, no action rows).
func TestProviderDetailListRowsAndModelRows(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	items, modelRows, addIdx, delIdx := m.providerDetailList("myprov")

	want := []string{
		"Type: openai",
		"API Base: https://api.example.com/v1",
		"API Key: sk-1...cdef", // maskAPIKey keeps first/last 4
		"---",
		"  alpha",
		"  beta",
		"---",
		i18n.T("tui.addModelAction"),
		i18n.T("tui.deleteProviderAction"),
	}
	if len(items) != len(want) {
		t.Fatalf("items count = %d, want %d: %q", len(items), len(want), items)
	}
	for i, w := range want {
		if items[i] != w {
			t.Errorf("items[%d] = %q, want %q", i, items[i], w)
		}
	}

	// The action-row indices must point at the localized action labels.
	if addIdx != 7 || delIdx != 8 {
		t.Errorf("addIdx, delIdx = %d, %d; want 7, 8", addIdx, delIdx)
	}
	if addIdx >= 0 && items[addIdx] != i18n.T("tui.addModelAction") {
		t.Errorf("items[addIdx] = %q, want %q", items[addIdx], i18n.T("tui.addModelAction"))
	}
	if delIdx >= 0 && items[delIdx] != i18n.T("tui.deleteProviderAction") {
		t.Errorf("items[delIdx] = %q, want %q", items[delIdx], i18n.T("tui.deleteProviderAction"))
	}

	// modelRows: only indices 4 and 5 (the model rows), aliases trimmed.
	wantRows := map[int]string{4: "alpha", 5: "beta"}
	if len(modelRows) != len(wantRows) {
		t.Fatalf("modelRows = %v, want %v", modelRows, wantRows)
	}
	for idx, alias := range wantRows {
		got, ok := modelRows[idx]
		if !ok {
			t.Errorf("modelRows missing index %d", idx)
			continue
		}
		if got != alias {
			t.Errorf("modelRows[%d] = %q, want %q", idx, got, alias)
		}
	}
	// Info rows, separators and action rows must NOT be in the map.
	for _, idx := range []int{0, 1, 2, 3, 6, 7, 8} {
		if alias, ok := modelRows[idx]; ok {
			t.Errorf("modelRows unexpectedly maps index %d (%q) to %q", idx, items[idx], alias)
		}
	}
}

// TestProviderDetailListNoModels verifies the placeholder row yields an
// EMPTY modelRows map (no placeholder index leak) and the layout is intact.
func TestProviderDetailListNoModels(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	items, modelRows, addIdx, delIdx := m.providerDetailList("emptyprov")

	wantTail := []string{"---", "  (no models)", "---", i18n.T("tui.addModelAction"), i18n.T("tui.deleteProviderAction")}
	if len(items) != 3+len(wantTail) { // 3 info rows + tail
		t.Fatalf("items count = %d, want %d: %q", len(items), 3+len(wantTail), items)
	}
	for i, w := range wantTail {
		got := items[3+i]
		if got != w {
			t.Errorf("items[%d] = %q, want %q", 3+i, got, w)
		}
	}
	if addIdx != len(items)-2 || delIdx != len(items)-1 {
		t.Errorf("addIdx, delIdx = %d, %d; want %d, %d", addIdx, delIdx, len(items)-2, len(items)-1)
	}
	if items[0] != "Type: openai" {
		t.Errorf("items[0] = %q, want %q", items[0], "Type: openai")
	}
	if len(modelRows) != 0 {
		t.Errorf("modelRows must be empty for the placeholder, got %v", modelRows)
	}
	// No action row or separator may ever be mapped.
	for idx, alias := range modelRows {
		if strings.HasPrefix(alias, "+") || strings.HasPrefix(alias, "-") || alias == "---" {
			t.Errorf("modelRows[%d] = %q must not map action/separator rows", idx, alias)
		}
	}
}

// TestProviderDetailListProviderNotFound verifies the three info rows are
// skipped when the provider does not exist, while separators/models/actions
// are still emitted.
func TestProviderDetailListProviderNotFound(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	items, modelRows, addIdx, delIdx := m.providerDetailList("missingprov")

	want := []string{"---", "  (no models)", "---", i18n.T("tui.addModelAction"), i18n.T("tui.deleteProviderAction")}
	if len(items) != len(want) {
		t.Fatalf("items count = %d, want %d: %q", len(items), len(want), items)
	}
	for i, w := range want {
		if items[i] != w {
			t.Errorf("items[%d] = %q, want %q", i, items[i], w)
		}
	}
	if addIdx != len(items)-2 || delIdx != len(items)-1 {
		t.Errorf("addIdx, delIdx = %d, %d; want %d, %d", addIdx, delIdx, len(items)-2, len(items)-1)
	}
	if len(modelRows) != 0 {
		t.Errorf("modelRows must be empty, got %v", modelRows)
	}
}

// TestProviderModelConfigLookup covers found / not-found / case-insensitive
// (and whitespace-trimmed) lookups for both keys.
func TestProviderModelConfigLookup(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	t.Run("found", func(t *testing.T) {
		mc, ok := m.providerModelConfig("myprov", "alpha")
		if !ok {
			t.Fatal("expected model found")
		}
		if mc.Model != "model-alpha-1" {
			t.Errorf("Model = %q, want model-alpha-1", mc.Model)
		}
		if mc.ContextWindow != 128000 || mc.MaxTokens != 4096 {
			t.Errorf("ContextWindow/MaxTokens = %d/%d, want 128000/4096", mc.ContextWindow, mc.MaxTokens)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		if _, ok := m.providerModelConfig("myprov", "gamma"); ok {
			t.Error("expected model not found")
		}
		if _, ok := m.providerModelConfig("otherprov", "alpha"); ok {
			t.Error("expected provider not found")
		}
	})

	t.Run("case_insensitive", func(t *testing.T) {
		mc, ok := m.providerModelConfig("MYPROV", "ALPHA")
		if !ok {
			t.Fatal("expected case-insensitive lookup to succeed")
		}
		if mc.Model != "model-alpha-1" {
			t.Errorf("Model = %q, want model-alpha-1", mc.Model)
		}
		if _, ok := m.providerModelConfig("  MyProv  ", "  Alpha  "); !ok {
			t.Error("expected trimmed lookup to succeed")
		}
	})
}

// TestUpdateModelInProviderInPlaceEditPersists verifies an in-place edit
// updates model/context/max/vision and round-trips to disk.
func TestUpdateModelInProviderInPlaceEditPersists(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	if err := m.updateModelInProvider("myprov", "alpha", "alpha", "model-alpha-2", 111000, 3333, true); err != nil {
		t.Fatalf("updateModelInProvider: %v", err)
	}

	got := m.cfg.Providers.Named["myprov"].Models["alpha"]
	if got.Model != "model-alpha-2" {
		t.Errorf("Model = %q, want model-alpha-2", got.Model)
	}
	if got.ContextWindow != 111000 {
		t.Errorf("ContextWindow = %d, want 111000", got.ContextWindow)
	}
	if got.MaxTokens != 3333 {
		t.Errorf("MaxTokens = %d, want 3333", got.MaxTokens)
	}
	if !got.Vision {
		t.Error("Vision = false, want true")
	}

	// Persisted to disk?
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	disk := reloaded.Providers.Named["myprov"].Models["alpha"]
	if disk.Model != "model-alpha-2" || disk.ContextWindow != 111000 ||
		disk.MaxTokens != 3333 || !disk.Vision {
		t.Errorf("disk state = %+v, want model-alpha-2/111000/3333/vision", disk)
	}
}

// TestUpdateModelInProviderPreservesTemperatureAndReasoning verifies fields
// not covered by the edit survive (Reasoning is NOT re-derived from catalog).
func TestUpdateModelInProviderPreservesTemperatureAndReasoning(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	if err := m.updateModelInProvider("myprov", "alpha", "alpha", "model-alpha-2", 111000, 3333, true); err != nil {
		t.Fatalf("updateModelInProvider: %v", err)
	}

	got := m.cfg.Providers.Named["myprov"].Models["alpha"]
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7 (preserved)", got.Temperature)
	}
	if got.Reasoning == nil || got.Reasoning.Effort == nil {
		t.Fatal("Reasoning/Effort must survive the edit")
	}
	if *got.Reasoning.Effort != "high" {
		t.Errorf("Effort = %q, want high (preserved)", *got.Reasoning.Effort)
	}
}

// TestUpdateModelInProviderZeroContextKeepsExisting verifies contextWindow=0
// (and maxTokens=0) keep the stored values instead of zeroing them.
func TestUpdateModelInProviderZeroContextKeepsExisting(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	if err := m.updateModelInProvider("myprov", "alpha", "alpha", "model-alpha-2", 0, 0, false); err != nil {
		t.Fatalf("updateModelInProvider: %v", err)
	}

	got := m.cfg.Providers.Named["myprov"].Models["alpha"]
	if got.ContextWindow != 128000 {
		t.Errorf("ContextWindow = %d, want 128000 (kept)", got.ContextWindow)
	}
	if got.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096 (kept)", got.MaxTokens)
	}
}

// TestUpdateModelInProviderRename verifies a rename moves the map key,
// removes the old one and carries the updated entry over.
func TestUpdateModelInProviderRename(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	if err := m.updateModelInProvider("myprov", "alpha", "Alpha2", "model-alpha-2", 111000, 3333, true); err != nil {
		t.Fatalf("updateModelInProvider: %v", err)
	}

	models := m.cfg.Providers.Named["myprov"].Models
	if _, ok := models["alpha"]; ok {
		t.Error("old key \"alpha\" must be removed after rename")
	}
	got, ok := models["alpha2"] // normalized: lowercased + trimmed
	if !ok {
		t.Fatalf("renamed key \"alpha2\" missing, models = %v", models)
	}
	if got.Model != "model-alpha-2" || got.ContextWindow != 111000 || got.MaxTokens != 3333 || !got.Vision {
		t.Errorf("renamed entry = %+v, want updated values", got)
	}
	if len(models) != 2 {
		t.Errorf("models count = %d, want 2 (alpha renamed, beta untouched)", len(models))
	}
}

// TestUpdateModelInProviderRenameCollisionLeavesConfigUnchanged verifies
// renaming onto an existing alias errors and mutates nothing.
func TestUpdateModelInProviderRenameCollisionLeavesConfigUnchanged(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	err := m.updateModelInProvider("myprov", "alpha", "beta", "changed", 1, 1, true)
	if err == nil {
		t.Fatal("expected collision error")
	}
	if err.Error() != `model "beta" already exists` {
		t.Errorf("err = %q, want %q", err.Error(), `model "beta" already exists`)
	}

	models := m.cfg.Providers.Named["myprov"].Models
	if len(models) != 2 {
		t.Fatalf("models count = %d, want 2 (unchanged)", len(models))
	}
	alpha := models["alpha"]
	if alpha.Model != "model-alpha-1" || alpha.ContextWindow != 128000 || alpha.MaxTokens != 4096 || alpha.Vision {
		t.Errorf("alpha entry changed: %+v", alpha)
	}
	beta := models["beta"]
	if beta.Model != "model-beta-1" || beta.ContextWindow != 32000 || beta.MaxTokens != 2048 {
		t.Errorf("beta entry changed: %+v", beta)
	}
}

// TestUpdateModelInProviderUnknownOrigAlias verifies the descriptive error
// for an alias that does not exist in the provider.
func TestUpdateModelInProviderUnknownOrigAlias(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	err := m.updateModelInProvider("myprov", "nope", "nope", "model-x", 1, 1, false)
	if err == nil {
		t.Fatal("expected error for unknown origAlias")
	}
	want := `model "nope" not found in provider "myprov"`
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

// TestUpdateModelInProviderEmptyNewAlias verifies an empty (after
// normalize) new alias is rejected.
func TestUpdateModelInProviderEmptyNewAlias(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	err := m.updateModelInProvider("myprov", "alpha", "   ", "model-x", 1, 1, false)
	if err == nil {
		t.Fatal("expected error for empty new alias")
	}
	if err.Error() != "model alias cannot be empty" {
		t.Errorf("err = %q, want %q", err.Error(), "model alias cannot be empty")
	}
	// Config untouched.
	if got := m.cfg.Providers.Named["myprov"].Models["alpha"]; got.Model != "model-alpha-1" {
		t.Errorf("alpha entry changed: %+v", got)
	}
}

// TestUpdateModelInProviderEmptyModelName verifies an empty model name is
// rejected.
func TestUpdateModelInProviderEmptyModelName(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	err := m.updateModelInProvider("myprov", "alpha", "alpha", "  ", 1, 1, false)
	if err == nil {
		t.Fatal("expected error for empty model name")
	}
	if err.Error() != "model name cannot be empty" {
		t.Errorf("err = %q, want %q", err.Error(), "model name cannot be empty")
	}
	// Config untouched.
	if got := m.cfg.Providers.Named["myprov"].Models["alpha"]; got.Model != "model-alpha-1" {
		t.Errorf("alpha entry changed: %+v", got)
	}
}
