package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

const (
	providerEditTestName = "my-proxy"
	providerEditTestKey  = "sk-1234567890abcdef"
)

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

func TestProviderEditFormPreFillsTypeAndBaseButNotKey(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderEditForm(t, m)

	if m.providerSelectedName != providerEditTestName {
		t.Errorf("providerSelectedName = %q, want %q", m.providerSelectedName, providerEditTestName)
	}
	wantValues := []string{"openai", "", "https://api.example.com/v1", "http://proxy.internal:8080", "", "", "default", ""}
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

func TestProviderEditSavesInPlaceKeepingUntouchedFields(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderEditForm(t, m)

	m.textInput.SetValue("openrouter")
	m = sendKeys(m, "\r")
	if strings.TrimSpace(m.textInput.Value()) != "" {
		t.Fatalf("API-key step started with %q, want blank", m.textInput.Value())
	}
	m = sendKeys(m, "\r")
	if got, want := m.textInput.Value(), "https://api.example.com/v1"; got != want {
		t.Fatalf("API-base step started with %q, want the stored base %q", got, want)
	}
	m = walkProviderEditForm(t, m, map[int]string{
		editProviderAPIBaseStep: "https://new.example.com/v1",
	})

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

	if len(m.modalItems) == 0 || m.modalItems[0] != "Type: openrouter" {
		t.Errorf("detail list not rebuilt from the saved config: %q", m.modalItems)
	}

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

func TestProviderEditNewKeyReplacesStoredKey(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderEditForm(t, m)

	m = sendKeys(m, "\r")
	m.textInput.SetValue("sk-brand-new-key")
	m = walkProviderEditForm(t, m, nil)

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

func TestProviderEditEscKeepsStoredConfig(t *testing.T) {
	m := newProviderEditTestModel(t)
	m = openProviderEditForm(t, m)

	m.textInput.SetValue("anthropic")
	m = sendKeys(m, "\r")
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

func TestUpdateProviderErrors(t *testing.T) {
	m := &Model{cfg: &config.Config{}}
	if err := m.updateProvider("x", providerEditValues{Type: "openai", APIKey: "k", APIBase: "b"}); err == nil {
		t.Error("updateProvider with no providers config must fail")
	}

	m = newProviderEditTestModel(t)
	if err := m.updateProvider("ghost", providerEditValues{Type: "openai", APIKey: "k", APIBase: "b"}); err == nil {
		t.Error("updateProvider on a missing provider must fail")
	}
	if got := m.cfg.Providers.Named[providerEditTestName].APIKey; got != providerEditTestKey {
		t.Errorf("a failed update touched another provider: APIKey = %q", got)
	}
}

func TestProviderEditFormRenders(t *testing.T) {
	m := newProviderEditTestModel(t)
	m.width, m.height = 100, 40
	m = openProviderEditForm(t, m)
	m = sendKeys(m, "\r")

	frame := m.View()
	for _, want := range []string{
		i18n.T("tui.editProvider"),
		"Provider type",
		"API Key",
		"API Base URL",
		"Proxy",
		"Auth method",
		"Connect mode",
		"Web search (yes/no/default)",
		"Headers (JSON)",
		i18n.T("tui.editProviderKeepHint"),
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("provider edit form frame missing %q:\n%s", want, frame)
		}
	}
}

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

func TestProviderEditHintsIgnoreStaleConnectState(t *testing.T) {
	m := newProviderEditTestModel(t)
	m.width, m.height = 100, 40
	m = openProviderEditForm(t, m)
	m.providerSavedInFlow = true

	frame := m.View()
	if !strings.Contains(frame, i18n.T("tui.formEnter")) {
		t.Errorf("edit form frame missing the next/cancel hint %q:\n%s", i18n.T("tui.formEnter"), frame)
	}
	if strings.Contains(frame, i18n.T("tui.connectModelStepsHint")) {
		t.Errorf("edit form frame shows the /connect model-steps hint:\n%s", frame)
	}
}

func TestUpdateProviderRollsBackWhenSaveFails(t *testing.T) {
	m := newProviderHelpersTestModel(t)
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("preparing the blocked config dir: %v", err)
	}
	t.Setenv("LELE_CONFIG_DIR", filepath.Join(blocked, "cfg"))

	before, ok := m.cfg.Providers.Named["myprov"]
	if !ok {
		t.Fatal(`provider "myprov" missing from the fixture`)
	}
	typedBefore := m.cfg.Providers.OpenAI.ProviderConfig

	err := m.updateProvider("myprov", providerEditValues{
		Type:    "anthropic",
		APIKey:  "sk-brand-new-key",
		APIBase: "https://new.example/v1",
	})
	if err == nil {
		t.Fatal("updateProvider must fail when the config cannot be saved")
	}

	after := m.cfg.Providers.Named["myprov"]
	if !reflect.DeepEqual(after, before) {
		t.Errorf("named entry not rolled back:\n got %+v\nwant %+v", after, before)
	}
	if got := m.cfg.Providers.OpenAI.ProviderConfig; !reflect.DeepEqual(got, typedBefore) {
		t.Errorf("typed mirror not rolled back: got %+v, want %+v", got, typedBefore)
	}
}

func TestUpdateProviderMirrorPreservesTypedCredentials(t *testing.T) {
	t.Run("blank edit keeps the env-provided typed key", func(t *testing.T) {
		m := newProviderHelpersTestModel(t)
		m.cfg.Providers.Named["openai"] = config.NamedProviderConfig{
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIBase: "https://api.example.com/v1"},
		}
		m.cfg.Providers.OpenAI.ProviderConfig = config.ProviderConfig{APIKey: "sk-from-env"}

		if err := m.updateProvider("openai", providerEditValues{Type: "openai", APIBase: "https://new.example/v1"}); err != nil {
			t.Fatalf("updateProvider: %v", err)
		}

		if got, want := m.cfg.Providers.OpenAI.APIKey, "sk-from-env"; got != want {
			t.Errorf("typed APIKey = %q, want the env value %q preserved", got, want)
		}
		if got, want := m.cfg.Providers.OpenAI.APIBase, "https://new.example/v1"; got != want {
			t.Errorf("typed APIBase = %q, want the edited base %q", got, want)
		}
	})

	t.Run("named credentials still reach an empty typed copy", func(t *testing.T) {
		m := newProviderHelpersTestModel(t)
		m.cfg.Providers.Named["openai"] = config.NamedProviderConfig{
			Type: "openai",
			ProviderConfig: config.ProviderConfig{
				APIKey:  "sk-only-in-named",
				APIBase: "https://api.example.com/v1",
			},
		}

		if err := m.updateProvider("openai", providerEditValues{Type: "openai"}); err != nil {
			t.Fatalf("updateProvider: %v", err)
		}

		if got, want := m.cfg.Providers.OpenAI.APIKey, "sk-only-in-named"; got != want {
			t.Errorf("typed APIKey = %q, want it mirrored from the named entry (%q)", got, want)
		}
		if got, want := m.cfg.Providers.OpenAI.APIBase, "https://api.example.com/v1"; got != want {
			t.Errorf("typed APIBase = %q, want it mirrored from the named entry (%q)", got, want)
		}
	})

	t.Run("enter-through over a pre-filled value is not a change", func(t *testing.T) {
		m := newProviderHelpersTestModel(t)
		m.cfg.Providers.Named["openai"] = config.NamedProviderConfig{
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIBase: "https://api.example.com/v1"},
		}
		m.cfg.Providers.OpenAI.ProviderConfig = config.ProviderConfig{APIBase: "https://env.example/v1"}

		if err := m.updateProvider("openai", providerEditValues{Type: "openai", APIBase: "https://api.example.com/v1"}); err != nil {
			t.Fatalf("updateProvider: %v", err)
		}

		if got, want := m.cfg.Providers.OpenAI.APIBase, "https://env.example/v1"; got != want {
			t.Errorf("typed APIBase = %q, want the env value %q preserved", got, want)
		}
	})

	t.Run("an untouched field keeps a different typed value", func(t *testing.T) {
		m := newProviderHelpersTestModel(t)
		m.cfg.Providers.Named["openai"] = config.NamedProviderConfig{
			Type: "openai",
			ProviderConfig: config.ProviderConfig{
				APIKey:  "sk-from-json",
				APIBase: "https://api.example.com/v1",
			},
		}
		m.cfg.Providers.OpenAI.ProviderConfig = config.ProviderConfig{APIKey: "sk-from-env"}

		if err := m.updateProvider("openai", providerEditValues{Type: "openai", APIBase: "https://new.example/v1"}); err != nil {
			t.Fatalf("updateProvider: %v", err)
		}

		if got, want := m.cfg.Providers.OpenAI.APIKey, "sk-from-env"; got != want {
			t.Errorf("typed APIKey = %q, want the env value %q preserved", got, want)
		}
		if got, want := m.cfg.Providers.OpenAI.APIBase, "https://new.example/v1"; got != want {
			t.Errorf("typed APIBase = %q, want the edited base %q", got, want)
		}
	})

	t.Run("a typed key replaces the env one", func(t *testing.T) {
		m := newProviderHelpersTestModel(t)
		m.cfg.Providers.Named["openai"] = config.NamedProviderConfig{Type: "openai"}
		m.cfg.Providers.OpenAI.ProviderConfig = config.ProviderConfig{APIKey: "sk-from-env"}

		if err := m.updateProvider("openai", providerEditValues{Type: "openai", APIKey: "sk-typed-by-user"}); err != nil {
			t.Fatalf("updateProvider: %v", err)
		}

		if got, want := m.cfg.Providers.OpenAI.APIKey, "sk-typed-by-user"; got != want {
			t.Errorf("typed APIKey = %q, want the explicitly typed key %q", got, want)
		}
	})
}
