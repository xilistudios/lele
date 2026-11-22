package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

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

func TestProviderDetailListRowsAndModelRows(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	layout := m.providerDetailList("myprov")
	items, modelRows, editIdx, addIdx, delIdx := layout.Items, layout.ModelRows, layout.EditIdx, layout.AddIdx, layout.DelIdx

	want := []string{
		"Type: openai",
		"API Base: https://api.example.com/v1",
		"API Key: sk-1...cdef",
		"---",
		"  alpha",
		"  beta",
		"---",
		i18n.T("tui.editProviderAction"),
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

	if editIdx != 7 || addIdx != 8 || delIdx != 9 {
		t.Errorf("editIdx, addIdx, delIdx = %d, %d, %d; want 7, 8, 9", editIdx, addIdx, delIdx)
	}
	if editIdx >= 0 && items[editIdx] != i18n.T("tui.editProviderAction") {
		t.Errorf("items[editIdx] = %q, want %q", items[editIdx], i18n.T("tui.editProviderAction"))
	}
	if addIdx >= 0 && items[addIdx] != i18n.T("tui.addModelAction") {
		t.Errorf("items[addIdx] = %q, want %q", items[addIdx], i18n.T("tui.addModelAction"))
	}
	if delIdx >= 0 && items[delIdx] != i18n.T("tui.deleteProviderAction") {
		t.Errorf("items[delIdx] = %q, want %q", items[delIdx], i18n.T("tui.deleteProviderAction"))
	}

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
	for _, idx := range []int{0, 1, 2, 3, 6, 7, 8, 9} {
		if alias, ok := modelRows[idx]; ok {
			t.Errorf("modelRows unexpectedly maps index %d (%q) to %q", idx, items[idx], alias)
		}
	}
}

func TestProviderDetailListNoModels(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	layout := m.providerDetailList("emptyprov")
	items, modelRows, addIdx, delIdx := layout.Items, layout.ModelRows, layout.AddIdx, layout.DelIdx

	wantTail := []string{"---", "  (no models)", "---", i18n.T("tui.editProviderAction"), i18n.T("tui.addModelAction"), i18n.T("tui.deleteProviderAction")}
	if len(items) != 3+len(wantTail) {
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
	for idx, alias := range modelRows {
		if strings.HasPrefix(alias, "+") || strings.HasPrefix(alias, "-") || alias == "---" {
			t.Errorf("modelRows[%d] = %q must not map action/separator rows", idx, alias)
		}
	}
}

func TestProviderDetailListProviderNotFound(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	layout := m.providerDetailList("missingprov")
	items, modelRows, addIdx, delIdx := layout.Items, layout.ModelRows, layout.AddIdx, layout.DelIdx

	want := []string{"---", "  (no models)", "---", i18n.T("tui.editProviderAction"), i18n.T("tui.addModelAction"), i18n.T("tui.deleteProviderAction")}
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

func TestUpdateModelInProviderInPlaceEditPersists(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	if err := m.updateModelInProvider("myprov", "alpha", "alpha", "model-alpha-2", 111000, 3333, true, "deepseek", nil); err != nil {
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
	if got.ThinkingType != "deepseek" {
		t.Errorf("ThinkingType = %q, want deepseek", got.ThinkingType)
	}

	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	disk := reloaded.Providers.Named["myprov"].Models["alpha"]
	if disk.Model != "model-alpha-2" || disk.ContextWindow != 111000 ||
		disk.MaxTokens != 3333 || !disk.Vision || disk.ThinkingType != "deepseek" {
		t.Errorf("disk state = %+v, want model-alpha-2/111000/3333/vision/deepseek", disk)
	}
}

func TestUpdateModelInProviderPreservesTemperatureAndStoresReasoning(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	stored := m.cfg.Providers.Named["myprov"].Models["alpha"].Reasoning
	if err := m.updateModelInProvider("myprov", "alpha", "alpha", "model-alpha-2", 111000, 3333, true, "", stored); err != nil {
		t.Fatalf("updateModelInProvider: %v", err)
	}

	got := m.cfg.Providers.Named["myprov"].Models["alpha"]
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7 (preserved)", got.Temperature)
	}
	if got.Reasoning == nil || got.Reasoning.Effort == nil {
		t.Fatal("Reasoning/Effort must be stored from the form answer")
	}
	if *got.Reasoning.Effort != "high" {
		t.Errorf("Effort = %q, want high (round-tripped)", *got.Reasoning.Effort)
	}

	if err := m.updateModelInProvider("myprov", "alpha", "alpha", "model-alpha-2", 111000, 3333, true, "", nil); err != nil {
		t.Fatalf("updateModelInProvider(nil reasoning): %v", err)
	}
	if got := m.cfg.Providers.Named["myprov"].Models["alpha"]; got.Reasoning != nil {
		t.Errorf("Reasoning = %+v, want nil after a nil answer", got.Reasoning)
	}
}

func TestUpdateModelInProviderZeroContextKeepsExisting(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	if err := m.updateModelInProvider("myprov", "alpha", "alpha", "model-alpha-2", 0, 0, false, "", nil); err != nil {
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

func TestUpdateModelInProviderThinkingType(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	if err := m.updateModelInProvider("myprov", "alpha", "alpha", "model-alpha-1", 0, 0, false, "qwen", nil); err != nil {
		t.Fatalf("updateModelInProvider(qwen): %v", err)
	}
	if got := m.cfg.Providers.Named["myprov"].Models["alpha"].ThinkingType; got != "qwen" {
		t.Errorf("ThinkingType = %q, want qwen", got)
	}

	if err := m.updateModelInProvider("myprov", "alpha", "alpha", "model-alpha-1", 0, 0, false, "auto", nil); err != nil {
		t.Fatalf("updateModelInProvider(auto): %v", err)
	}
	if got := m.cfg.Providers.Named["myprov"].Models["alpha"].ThinkingType; got != "" {
		t.Errorf("ThinkingType = %q, want empty after auto", got)
	}

	err := m.updateModelInProvider("myprov", "alpha", "alpha", "model-alpha-1", 0, 0, false, "bogus", nil)
	if err == nil {
		t.Fatal("expected error for invalid thinking type")
	}
	if got := m.cfg.Providers.Named["myprov"].Models["alpha"].Model; got != "model-alpha-1" {
		t.Errorf("Model = %q, want model-alpha-1 (unchanged after error)", got)
	}
}

func TestUpdateModelInProviderRename(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	if err := m.updateModelInProvider("myprov", "alpha", "Alpha2", "model-alpha-2", 111000, 3333, true, "", nil); err != nil {
		t.Fatalf("updateModelInProvider: %v", err)
	}

	models := m.cfg.Providers.Named["myprov"].Models
	if _, ok := models["alpha"]; ok {
		t.Error("old key \"alpha\" must be removed after rename")
	}
	got, ok := models["alpha2"]
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

func TestUpdateModelInProviderRenameCollisionLeavesConfigUnchanged(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	err := m.updateModelInProvider("myprov", "alpha", "beta", "changed", 1, 1, true, "", nil)
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

func TestUpdateModelInProviderUnknownOrigAlias(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	err := m.updateModelInProvider("myprov", "nope", "nope", "model-x", 1, 1, false, "", nil)
	if err == nil {
		t.Fatal("expected error for unknown origAlias")
	}
	want := `model "nope" not found in provider "myprov"`
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestUpdateModelInProviderEmptyNewAlias(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	err := m.updateModelInProvider("myprov", "alpha", "   ", "model-x", 1, 1, false, "", nil)
	if err == nil {
		t.Fatal("expected error for empty new alias")
	}
	if err.Error() != "model alias cannot be empty" {
		t.Errorf("err = %q, want %q", err.Error(), "model alias cannot be empty")
	}
	if got := m.cfg.Providers.Named["myprov"].Models["alpha"]; got.Model != "model-alpha-1" {
		t.Errorf("alpha entry changed: %+v", got)
	}
}

func TestUpdateModelInProviderEmptyModelName(t *testing.T) {
	m := newProviderHelpersTestModel(t)

	err := m.updateModelInProvider("myprov", "alpha", "alpha", "  ", 1, 1, false, "", nil)
	if err == nil {
		t.Fatal("expected error for empty model name")
	}
	if err.Error() != "model name cannot be empty" {
		t.Errorf("err = %q, want %q", err.Error(), "model name cannot be empty")
	}
	if got := m.cfg.Providers.Named["myprov"].Models["alpha"]; got.Model != "model-alpha-1" {
		t.Errorf("alpha entry changed: %+v", got)
	}
}

func TestEnsureProviderKeepsKeylessEndpoint(t *testing.T) {
	m := newProviderHelpersTestModel(t)
	m.cfg.Providers.Named["ollama"] = config.NamedProviderConfig{
		Type: "ollama",
		ProviderConfig: config.ProviderConfig{
			APIBase: "http://192.168.1.50:11434/v1",
			Proxy:   "http://proxy.lan:3128",
		},
	}

	alreadyUsable, err := m.ensureProvider("ollama", "ollama", "", "http://localhost:11434/v1", true)
	if err != nil {
		t.Fatalf("ensureProvider: %v", err)
	}
	if alreadyUsable {
		t.Error("alreadyUsable = true, want false (no models were configured)")
	}

	p, ok := m.cfg.Providers.Named["ollama"]
	if !ok {
		t.Fatal("named provider entry disappeared")
	}
	if p.APIBase != "http://192.168.1.50:11434/v1" {
		t.Errorf("APIBase = %q, want the pre-configured endpoint kept", p.APIBase)
	}
	if p.Proxy != "http://proxy.lan:3128" {
		t.Errorf("Proxy = %q, want the pre-configured proxy kept", p.Proxy)
	}
	if p.APIKey != "" {
		t.Errorf("APIKey = %q, want empty (none was supplied)", p.APIKey)
	}
	if len(p.Models) != 0 {
		t.Errorf("Models = %v, want empty", p.Models)
	}
}

func TestEnsureProviderStillReplacesTrulyEmptyPlaceholder(t *testing.T) {
	m := newProviderHelpersTestModel(t)
	m.cfg.Providers.Named["ollama"] = config.NamedProviderConfig{Type: "ollama"}

	alreadyUsable, err := m.ensureProvider("ollama", "ollama", "sk-new", "http://localhost:11434/v1", true)
	if err != nil {
		t.Fatalf("ensureProvider: %v", err)
	}
	if alreadyUsable {
		t.Error("alreadyUsable = true, want false")
	}

	p := m.cfg.Providers.Named["ollama"]
	if p.APIKey != "sk-new" {
		t.Errorf("APIKey = %q, want sk-new (placeholder replaced)", p.APIKey)
	}
	if p.APIBase != "http://localhost:11434/v1" {
		t.Errorf("APIBase = %q, want the new base", p.APIBase)
	}
	if p.Proxy != "" {
		t.Errorf("Proxy = %q, want empty", p.Proxy)
	}
}

func TestNamedProviderConfigIsEmptyFieldMatrix(t *testing.T) {
	base := config.NamedProviderConfig{Type: "ollama"}
	if !base.IsEmpty() {
		t.Fatal("a bare Type-only entry must still be a placeholder")
	}

	cases := []struct {
		name  string
		entry config.NamedProviderConfig
		want  bool
	}{
		{"api_key", withProviderConfig(base, config.ProviderConfig{APIKey: "sk-x"}), false},
		{"api_base", withProviderConfig(base, config.ProviderConfig{APIBase: "http://x"}), false},
		{"proxy", withProviderConfig(base, config.ProviderConfig{Proxy: "http://p"}), false},
		{"connect_mode", withProviderConfig(base, config.ProviderConfig{ConnectMode: "stdio"}), false},
		{"auth_method", withProviderConfig(base, config.ProviderConfig{AuthMethod: "oauth"}), false},
		{"models", withModels(base), false},
		{"type_only", base, true},
		{"web_search_only", withWebSearch(base), true},
	}
	for _, tc := range cases {
		if got := tc.entry.IsEmpty(); got != tc.want {
			t.Errorf("%s: IsEmpty() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func withProviderConfig(p config.NamedProviderConfig, pc config.ProviderConfig) config.NamedProviderConfig {
	p.ProviderConfig = pc
	return p
}

func withModels(p config.NamedProviderConfig) config.NamedProviderConfig {
	p.Models = map[string]config.ProviderModelConfig{"m": {Model: "m"}}
	return p
}

func withWebSearch(p config.NamedProviderConfig) config.NamedProviderConfig {
	ws := true
	p.WebSearch = &ws
	return p
}

func TestEnsureProviderMirrorsToTypedField(t *testing.T) {
	m := newProviderHelpersTestModel(t)
	m.cfg.Providers.OpenAI.ProviderConfig = config.ProviderConfig{
		APIKey:  "sk-old-key",
		APIBase: "https://api.openai.com/v1",
	}
	m.cfg.Providers.Named["openai"] = config.NamedProviderConfig{
		Type: "openai",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "sk-old-key",
			APIBase: "https://api.openai.com/v1",
		},
		Models: map[string]config.ProviderModelConfig{
			"gpt-4o": {Model: "gpt-4o"},
		},
	}

	alreadyUsable, err := m.ensureProvider("openai", "openai", "sk-new-key",
		"https://api.openai.com/v1", true)
	if err != nil {
		t.Fatalf("ensureProvider: %v", err)
	}
	if !alreadyUsable {
		t.Error("alreadyUsable = false, want true (the provider already had a model)")
	}

	named := m.cfg.Providers.Named["openai"]
	if named.APIKey != "sk-new-key" {
		t.Errorf("named api_key = %q, want the freshly entered key", named.APIKey)
	}
	if len(named.Models) != 1 {
		t.Errorf("named models = %v, want the existing model list kept", named.Models)
	}
	if got := m.cfg.Providers.OpenAI.APIKey; got != "sk-new-key" {
		t.Errorf("typed Providers.OpenAI.APIKey = %q, want %q (mirror missing)", got, "sk-new-key")
	}
}
