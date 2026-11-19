package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Tests for the full-field "edit provider" form (ModalEditProvider): besides
// the type, API key and API base URL it also edits proxy, auth method, connect
// mode, web search and custom headers. The rules under test:
//
//   - blank keeps the stored value (the form is a plain text widget and never
//     renders a stored secret back);
//   - "-" clears the three optional middle fields;
//   - web search takes yes / no / default (unset) and rejects anything else;
//   - headers take a JSON object of string→string, "{}" clears them, and
//     transport-owned headers are rejected by protocoltypes.ValidateCustomHeaders;
//   - the headers and API-key steps are both masked;
//   - a CHANGED field is mirrored into the typed provider copy, an untouched
//     one only fills an empty typed copy (env-carrier rule), and a failed save
//     rolls every copy back.

// walkProviderEditForm presses ENTER through every remaining step of the edit
// form, overriding the text input of the steps named in overrides (step index →
// value). The ENTER on the last step saves and closes the form. It fails the
// test if a step advances into an error, so a rejected value can never be
// mistaken for a silent save.
func walkProviderEditForm(t *testing.T, m *Model, overrides map[int]string) *Model {
	t.Helper()
	for step := m.formStepIndex; step < editProviderStepCount; step++ {
		if m.formStepIndex != step {
			t.Fatalf("form stuck on step %d, want %d (formError = %q)", m.formStepIndex, step, m.formError)
		}
		if v, ok := overrides[step]; ok {
			m.textInput.SetValue(v)
		}
		m = sendKeys(m, "\r")
	}
	return m
}

// newProviderEditFullModel builds a cfg-only Model (no agent loop, so
// configSource falls back to m.cfg) whose provider carries EVERY editable field
// plus a model, so a full Enter-through can prove nothing is lost.
func newProviderEditFullModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	webSearch := true
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: t.TempDir()},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"fullprov": {
					Type: "openai",
					ProviderConfig: config.ProviderConfig{
						APIKey:      "sk-1234567890abcdef",
						APIBase:     "https://api.example.com/v1",
						Proxy:       "http://proxy.internal:8080",
						AuthMethod:  "oauth",
						ConnectMode: "stdio",
						Headers:     map[string]string{"X-Title": "lele", "X-Trace": "on"},
					},
					WebSearch: &webSearch,
					Models: map[string]config.ProviderModelConfig{
						"alpha": {Model: "model-alpha-1", ContextWindow: 128000, MaxTokens: 4096},
					},
				},
			},
		},
	}
	if err := config.SaveConfig(config.DefaultConfigPath(), cfg); err != nil {
		t.Fatalf("saving initial config: %v", err)
	}
	m := &Model{cfg: cfg}
	// A Model built by NewModel gets a ready, focused text input; a hand-built
	// one has to do it explicitly, otherwise bubbles' textinput drops every
	// keystroke (its Update returns early when unfocused) and a cursor that was
	// never initialized panics on Focus.
	m.textInput = textinput.New()
	m.textInput.Focus()
	return m
}

// TestProviderEditEnterThroughEightStepsChangesNothing is the safety net of the
// wider form: pressing ENTER through all eight pre-filled steps must not change
// a single stored field (type, key, base, proxy, auth, connect, web search,
// headers and models), and must not clobber the typed mirror either.
func TestProviderEditEnterThroughEightStepsChangesNothing(t *testing.T) {
	m := newProviderEditFullModel(t)
	before := m.cfg.Providers.Named["fullprov"]
	typedBefore := m.cfg.Providers.OpenAI.ProviderConfig
	typedWebSearchBefore := m.cfg.Providers.OpenAI.WebSearch

	m.providerSelectedName = "fullprov"
	if !m.openProviderEditFlow() {
		t.Fatalf("openProviderEditFlow() = false: %s", m.providerFeedback)
	}
	if got := len(m.formValues); got != editProviderStepCount {
		t.Fatalf("formValues has %d entries, want %d: %q", got, editProviderStepCount, m.formValues)
	}
	// Secrets are never pre-filled: the key and the headers blob stay blank.
	if m.formValues[editProviderAPIKeyStep] != "" {
		t.Errorf("API-key step pre-filled: %q", m.formValues[editProviderAPIKeyStep])
	}
	if m.formValues[editProviderHeadersStep] != "" {
		t.Errorf("headers step pre-filled: %q", m.formValues[editProviderHeadersStep])
	}

	m = walkProviderEditForm(t, m, nil)

	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail after saving", m.modalMode)
	}
	if m.formError != "" {
		t.Fatalf("formError = %q, want none", m.formError)
	}
	if after := m.cfg.Providers.Named["fullprov"]; !reflect.DeepEqual(after, before) {
		t.Errorf("Enter-through changed the named entry:\n got %+v\nwant %+v", after, before)
	}
	if got := m.cfg.Providers.OpenAI.ProviderConfig; !reflect.DeepEqual(got, typedBefore) {
		t.Errorf("Enter-through changed the typed mirror: got %+v, want %+v", got, typedBefore)
	}
	if got := m.cfg.Providers.OpenAI.WebSearch; got != typedWebSearchBefore {
		t.Errorf("typed OpenAI.WebSearch = %v, want %v", got, typedWebSearchBefore)
	}
} // TestProviderEditNewFieldsPersist covers one edit per new field: the value
// reaches the named entry, survives the reload from disk and nothing else moves.
func TestProviderEditNewFieldsPersist(t *testing.T) {
	cases := []struct {
		label  string
		step   int
		input  string
		assert func(t *testing.T, p config.NamedProviderConfig)
	}{
		{
			label: "proxy",
			step:  editProviderProxyStep,
			input: "http://new-proxy.internal:3128",
			assert: func(t *testing.T, p config.NamedProviderConfig) {
				if p.Proxy != "http://new-proxy.internal:3128" {
					t.Errorf("Proxy = %q, want the typed proxy", p.Proxy)
				}
			},
		},
		{
			label: "proxy cleared with a dash",
			step:  editProviderProxyStep,
			input: "-",
			assert: func(t *testing.T, p config.NamedProviderConfig) {
				if p.Proxy != "" {
					t.Errorf("Proxy = %q, want it cleared", p.Proxy)
				}
			},
		},
		{
			label: "auth method",
			step:  editProviderAuthMethodStep,
			input: "codex-cli",
			assert: func(t *testing.T, p config.NamedProviderConfig) {
				if p.AuthMethod != "codex-cli" {
					t.Errorf("AuthMethod = %q, want codex-cli", p.AuthMethod)
				}
			},
		},
		{
			label: "auth method cleared with a dash",
			step:  editProviderAuthMethodStep,
			input: "-",
			assert: func(t *testing.T, p config.NamedProviderConfig) {
				if p.AuthMethod != "" {
					t.Errorf("AuthMethod = %q, want it cleared", p.AuthMethod)
				}
			},
		},
		{
			label: "connect mode",
			step:  editProviderConnectModeStep,
			input: "grpc",
			assert: func(t *testing.T, p config.NamedProviderConfig) {
				if p.ConnectMode != "grpc" {
					t.Errorf("ConnectMode = %q, want grpc", p.ConnectMode)
				}
			},
		},
		{
			label: "connect mode cleared with a dash",
			step:  editProviderConnectModeStep,
			input: "-",
			assert: func(t *testing.T, p config.NamedProviderConfig) {
				if p.ConnectMode != "" {
					t.Errorf("ConnectMode = %q, want it cleared", p.ConnectMode)
				}
			},
		},
		{
			label: "web search no",
			step:  editProviderWebSearchStep,
			input: "no",
			assert: func(t *testing.T, p config.NamedProviderConfig) {
				if p.WebSearch == nil || *p.WebSearch {
					t.Errorf("WebSearch = %v, want an explicit false", p.WebSearch)
				}
			},
		},
		{
			label: "web search yes, uppercase",
			step:  editProviderWebSearchStep,
			input: "YES",
			assert: func(t *testing.T, p config.NamedProviderConfig) {
				if p.WebSearch == nil || !*p.WebSearch {
					t.Errorf("WebSearch = %v, want an explicit true", p.WebSearch)
				}
			},
		},
		{
			label: "web search default unsets it",
			step:  editProviderWebSearchStep,
			input: "default",
			assert: func(t *testing.T, p config.NamedProviderConfig) {
				if p.WebSearch != nil {
					t.Errorf("WebSearch = %v, want nil (unset)", *p.WebSearch)
				}
			},
		},
		{
			label: "headers replaced",
			step:  editProviderHeadersStep,
			input: `{"X-Title":"other","X-Team":"core"}`,
			assert: func(t *testing.T, p config.NamedProviderConfig) {
				want := map[string]string{"X-Title": "other", "X-Team": "core"}
				if !reflect.DeepEqual(p.Headers, want) {
					t.Errorf("Headers = %v, want %v", p.Headers, want)
				}
			},
		},
		{
			label: "headers cleared with an empty object",
			step:  editProviderHeadersStep,
			input: "{}",
			assert: func(t *testing.T, p config.NamedProviderConfig) {
				if p.Headers != nil {
					t.Errorf("Headers = %v, want nil", p.Headers)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			m := newProviderEditFullModel(t)
			m.providerSelectedName = "fullprov"
			if !m.openProviderEditFlow() {
				t.Fatalf("openProviderEditFlow() = false: %s", m.providerFeedback)
			}
			m = walkProviderEditForm(t, m, map[int]string{tc.step: tc.input})
			if m.modalMode != ModalProviderDetail {
				t.Fatalf("modal = %v (formError = %q), want ModalProviderDetail", m.modalMode, m.formError)
			}

			provider := m.cfg.Providers.Named["fullprov"]
			tc.assert(t, provider)
			// Nothing else moved: the model list, the key and the base URL are
			// the fields a rebuild of the entry would drop.
			if len(provider.Models) != 1 || provider.Models["alpha"].Model != "model-alpha-1" {
				t.Errorf("Models = %v, want the original alpha model", provider.Models)
			}
			if provider.APIKey != "sk-1234567890abcdef" {
				t.Errorf("APIKey = %q, want it preserved", provider.APIKey)
			}
			if provider.APIBase != "https://api.example.com/v1" {
				t.Errorf("APIBase = %q, want it preserved", provider.APIBase)
			}

			// And the change really reached the file, not just memory.
			saved, err := config.LoadConfig(config.DefaultConfigPath())
			if err != nil {
				t.Fatalf("reloading the saved config: %v", err)
			}
			tc.assert(t, saved.Providers.Named["fullprov"])
		})
	}
} // TestProviderEditHeadersValidation covers the headers step's rejection cases
// (invalid JSON, a non-object, a non-string value, a transport-owned header): a
// rejected value must leave the user on the step with a helpful error and must
// never reach the config.
func TestProviderEditHeadersValidation(t *testing.T) {
	cases := []struct {
		label    string
		input    string
		wantHint string
	}{
		{label: "invalid JSON", input: `{"X-Title":`, wantHint: "json object"},
		{label: "array instead of object", input: `["X-Title"]`, wantHint: "json object"},
		{label: "non-string value", input: `{"X-Title":1}`, wantHint: "json object"},
		{label: "transport-owned header", input: `{"Host":"example.com"}`, wantHint: "transport"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			m := newProviderEditFullModel(t)
			before := m.cfg.Providers.Named["fullprov"]
			m.providerSelectedName = "fullprov"
			if !m.openProviderEditFlow() {
				t.Fatalf("openProviderEditFlow() = false: %s", m.providerFeedback)
			}
			// Walk to the headers step (the last one).
			for m.formStepIndex < editProviderHeadersStep {
				m = sendKeys(m, "\r")
			}
			if m.formStepIndex != editProviderHeadersStep {
				t.Fatalf("formStepIndex = %d, want the headers step", m.formStepIndex)
			}
			// The headers step must be masked while typing.
			if !m.isSecretInputStep() {
				t.Error("the headers step must be a secret input step")
			}
			if got := m.textInput.Value(); got != "" {
				t.Fatalf("headers step started with %q, want blank", got)
			}

			m.textInput.SetValue(tc.input)
			m = sendKeys(m, "\r")

			if m.modalMode != ModalEditProvider {
				t.Fatalf("modal = %v, want the form to stay open", m.modalMode)
			}
			if !strings.Contains(strings.ToLower(m.formError), tc.wantHint) {
				t.Errorf("formError = %q, want it to mention %q", m.formError, tc.wantHint)
			}
			if after := m.cfg.Providers.Named["fullprov"]; !reflect.DeepEqual(after, before) {
				t.Errorf("a rejected header edit changed the config:\n got %+v\nwant %+v", after, before)
			}
		})
	}
}

// TestProviderEditHeadersNeverPreFilled pins the secret rule: even when the
// provider HAS headers, step 7 starts empty — the values may be credentials (an
// Authorization override, a gateway token) and must not be rendered back.
func TestProviderEditHeadersNeverPreFilled(t *testing.T) {
	m := newProviderEditFullModel(t)
	m.width, m.height = 120, 40
	m.providerSelectedName = "fullprov"
	if !m.openProviderEditFlow() {
		t.Fatalf("openProviderEditFlow() = false: %s", m.providerFeedback)
	}
	if got := m.formValues[editProviderHeadersStep]; got != "" {
		t.Fatalf("formValues[headers] = %q, want empty", got)
	}

	for m.formStepIndex < editProviderHeadersStep {
		m = sendKeys(m, "\r")
	}
	if got := m.textInput.Value(); got != "" {
		t.Fatalf("headers input = %q, want empty", got)
	}

	// Typing a secret-bearing blob must not reach the frame in clear text.
	const blob = `{"Authorization":"Bearer super-secret-token"}`
	m = sendKeys(m, blob)
	if m.textInput.Value() != blob {
		t.Fatalf("forwarding broke input: %q", m.textInput.Value())
	}
	// renderActiveModal is the part of View() that paints the form; the rest of
	// the frame needs a live agent loop, which this cfg-only fixture has not got.
	frame := m.renderActiveModal()
	if strings.Contains(frame, "super-secret-token") {
		t.Errorf("the rendered headers step leaks the value:\n%s", frame)
	}
	if !strings.Contains(frame, "•") {
		t.Errorf("the rendered headers step shows no mask:\n%s", frame)
	}
}

// TestProviderEditWebSearchInvalidValueStaysInForm verifies the yes/no/default
// validation happens on advance, not on save: a typo keeps the user on the step
// and leaves the config untouched.
func TestProviderEditWebSearchInvalidValueStaysInForm(t *testing.T) {
	m := newProviderEditFullModel(t)
	before := m.cfg.Providers.Named["fullprov"]
	m.providerSelectedName = "fullprov"
	if !m.openProviderEditFlow() {
		t.Fatalf("openProviderEditFlow() = false: %s", m.providerFeedback)
	}
	for m.formStepIndex < editProviderWebSearchStep {
		m = sendKeys(m, "\r")
	}
	if m.formStepIndex != editProviderWebSearchStep {
		t.Fatalf("formStepIndex = %d, want the web-search step", m.formStepIndex)
	}
	if got, want := m.textInput.Value(), "yes"; got != want {
		t.Errorf("web-search step pre-filled with %q, want %q", got, want)
	}

	m.textInput.SetValue("maybe")
	m = sendKeys(m, "\r")

	if m.modalMode != ModalEditProvider || m.formStepIndex != editProviderWebSearchStep {
		t.Fatalf("modal/step = %v/%d, want to stay on the web-search step", m.modalMode, m.formStepIndex)
	}
	if m.formError == "" {
		t.Error("an invalid web-search value must set formError")
	}
	if after := m.cfg.Providers.Named["fullprov"]; !reflect.DeepEqual(after, before) {
		t.Errorf("a rejected web-search edit changed the config:\n got %+v\nwant %+v", after, before)
	}
} // TestUpdateProviderNewFieldSemantics drives the save point directly (no form)
// for the rules the handler cannot express alone: validation errors, the
// "-"/"{}" clear syntax and the "keep" default.
func TestUpdateProviderNewFieldSemantics(t *testing.T) {
	t.Run("invalid web search is rejected", func(t *testing.T) {
		m := newProviderEditFullModel(t)
		err := m.updateProvider("fullprov", providerEditValues{WebSearch: "maybe"})
		if err == nil {
			t.Fatal("updateProvider must reject an invalid web_search value")
		}
		if got := m.cfg.Providers.Named["fullprov"].WebSearch; got == nil || !*got {
			t.Errorf("WebSearch = %v, want the stored true untouched", got)
		}
	})

	t.Run("transport-owned header is rejected", func(t *testing.T) {
		m := newProviderEditFullModel(t)
		err := m.updateProvider("fullprov", providerEditValues{Headers: `{"Content-Type":"text/plain"}`})
		if err == nil {
			t.Fatal("updateProvider must reject a transport-owned header")
		}
		if got := m.cfg.Providers.Named["fullprov"].Headers; len(got) != 2 {
			t.Errorf("Headers = %v, want the stored headers untouched", got)
		}
	})

	t.Run("dash clears an env-provided field", func(t *testing.T) {
		m := newProviderHelpersTestModel(t)
		m.cfg.Providers.Named["myprov"] = config.NamedProviderConfig{
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{AuthMethod: "oauth", Proxy: "http://p:8080"},
		}
		if err := m.updateProvider("myprov", providerEditValues{AuthMethod: "-", Proxy: "-"}); err != nil {
			t.Fatalf("updateProvider: %v", err)
		}
		got := m.cfg.Providers.Named["myprov"]
		if got.AuthMethod != "" || got.Proxy != "" {
			t.Errorf("AuthMethod/Proxy = %q/%q, want both cleared", got.AuthMethod, got.Proxy)
		}
	})

	t.Run("empty headers object clears the map", func(t *testing.T) {
		m := newProviderEditFullModel(t)
		if err := m.updateProvider("fullprov", providerEditValues{Headers: "{}"}); err != nil {
			t.Fatalf("updateProvider: %v", err)
		}
		if got := m.cfg.Providers.Named["fullprov"].Headers; got != nil {
			t.Errorf("Headers = %v, want nil", got)
		}
	})
}

// TestUpdateProviderMirrorsNewFieldsIntoTheTypedSlot covers the mirror rules for
// the new fields on a provider that HAS a typed slot (anthropic): a changed
// field wins outright, an untouched one does not clobber a non-empty typed copy
// (the env-carrier rule), and headers are deep-copied rather than aliased.
func TestUpdateProviderMirrorsNewFieldsIntoTheTypedSlot(t *testing.T) {
	t.Run("changed fields reach the typed slot", func(t *testing.T) {
		m := newProviderHelpersTestModel(t)
		m.cfg.Providers.Named["anthropic"] = config.NamedProviderConfig{Type: "anthropic"}

		err := m.updateProvider("anthropic", providerEditValues{
			AuthMethod:  "oauth",
			Proxy:       "http://proxy.example:8080",
			ConnectMode: "grpc",
			Headers:     `{"X-Title":"lele"}`,
		})
		if err != nil {
			t.Fatalf("updateProvider: %v", err)
		}

		typed := m.cfg.Providers.Anthropic
		if typed.AuthMethod != "oauth" {
			t.Errorf("typed AuthMethod = %q, want oauth", typed.AuthMethod)
		}
		if typed.Proxy != "http://proxy.example:8080" {
			t.Errorf("typed Proxy = %q, want the edited proxy", typed.Proxy)
		}
		if typed.ConnectMode != "grpc" {
			t.Errorf("typed ConnectMode = %q, want grpc", typed.ConnectMode)
		}
		if typed.Headers["X-Title"] != "lele" {
			t.Errorf("typed Headers = %v, want X-Title: lele", typed.Headers)
		}
		// Two copies of one provider must not share the map: mutating one must
		// not be visible in the other.
		typed.Headers["X-Title"] = "mutated"
		if got := m.cfg.Providers.Named["anthropic"].Headers["X-Title"]; got != "lele" {
			t.Errorf("named Headers aliases the typed map: X-Title = %q, want lele", got)
		}
	})

	t.Run("untouched fields keep the env-provided typed value", func(t *testing.T) {
		m := newProviderHelpersTestModel(t)
		m.cfg.Providers.Named["anthropic"] = config.NamedProviderConfig{
			Type: "anthropic",
			ProviderConfig: config.ProviderConfig{
				AuthMethod: "token",
				Proxy:      "http://from-json:8080",
				Headers:    map[string]string{"X-From": "json"},
			},
		}
		// Simulates LELE_PROVIDERS_ANTHROPIC_AUTH_METHOD / _PROXY, applied by
		// env.Parse after the JSON is decoded.
		m.cfg.Providers.Anthropic = config.ProviderConfig{
			AuthMethod: "oauth",
			Proxy:      "http://from-env:8080",
			Headers:    map[string]string{"X-From": "env"},
		}

		if err := m.updateProvider("anthropic", providerEditValues{APIBase: "https://new.example/v1"}); err != nil {
			t.Fatalf("updateProvider: %v", err)
		}

		typed := m.cfg.Providers.Anthropic
		if typed.AuthMethod != "oauth" {
			t.Errorf("typed AuthMethod = %q, want the env value oauth preserved", typed.AuthMethod)
		}
		if typed.Proxy != "http://from-env:8080" {
			t.Errorf("typed Proxy = %q, want the env proxy preserved", typed.Proxy)
		}
		if got := typed.Headers["X-From"]; got != "env" {
			t.Errorf("typed Headers[X-From] = %q, want the env value preserved", got)
		}
	})

	t.Run("clearing a field clears the typed copy", func(t *testing.T) {
		m := newProviderHelpersTestModel(t)
		m.cfg.Providers.Named["anthropic"] = config.NamedProviderConfig{
			Type:           "anthropic",
			ProviderConfig: config.ProviderConfig{AuthMethod: "oauth"},
		}
		if err := m.updateProvider("anthropic", providerEditValues{AuthMethod: "-"}); err != nil {
			t.Fatalf("updateProvider: %v", err)
		}
		if got := m.cfg.Providers.Anthropic.AuthMethod; got != "" {
			t.Errorf("typed AuthMethod = %q, want it cleared", got)
		}
	})
} // TestUpdateProviderMirrorsOpenAIWebSearch covers the one field that is not a
// ProviderConfig member: the typed OpenAI slot keeps web_search as a plain bool
// and the transports read that copy, so a changed answer has to be projected
// onto it in BOTH directions.
func TestUpdateProviderMirrorsOpenAIWebSearch(t *testing.T) {
	cases := []struct {
		label   string
		edited  string
		want    bool
		wantNil bool
	}{
		{label: "yes sets the typed bool", edited: "yes", want: true},
		{label: "no clears the typed bool", edited: "no", want: false},
		{label: "default unsets both", edited: "default", want: false, wantNil: true},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			m := newProviderHelpersTestModel(t)
			on := true
			m.cfg.Providers.Named["openai"] = config.NamedProviderConfig{Type: "openai", WebSearch: &on}
			m.cfg.Providers.OpenAI.WebSearch = true

			if err := m.updateProvider("openai", providerEditValues{WebSearch: tc.edited}); err != nil {
				t.Fatalf("updateProvider: %v", err)
			}

			named := m.cfg.Providers.Named["openai"]
			if tc.wantNil {
				if named.WebSearch != nil {
					t.Errorf("named WebSearch = %v, want nil", *named.WebSearch)
				}
			} else if named.WebSearch == nil || *named.WebSearch != tc.want {
				t.Errorf("named WebSearch = %v, want %v", named.WebSearch, tc.want)
			}
			if got := m.cfg.Providers.OpenAI.WebSearch; got != tc.want {
				t.Errorf("typed OpenAI.WebSearch = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("an untouched answer leaves the typed bool alone", func(t *testing.T) {
		m := newProviderHelpersTestModel(t)
		m.cfg.Providers.Named["openai"] = config.NamedProviderConfig{Type: "openai"}
		// The typed copy is the carrier (an env override, or the JSON under
		// providers.openai); pressing Enter over "default" must not clear it.
		m.cfg.Providers.OpenAI.WebSearch = true

		if err := m.updateProvider("openai", providerEditValues{WebSearch: "default"}); err != nil {
			t.Fatalf("updateProvider: %v", err)
		}
		if got := m.cfg.Providers.OpenAI.WebSearch; !got {
			t.Error("typed OpenAI.WebSearch = false, want the stored true preserved")
		}
	})
}

// TestUpdateProviderRollsBackEveryCopyWhenSaveFails extends the rollback
// contract to the new fields: a failed save must restore the named entry, the
// typed ProviderConfig AND the typed OpenAI web_search bool.
func TestUpdateProviderRollsBackEveryCopyWhenSaveFails(t *testing.T) {
	m := newProviderHelpersTestModel(t)
	on := true
	m.cfg.Providers.Named["openai"] = config.NamedProviderConfig{
		Type: "openai",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "sk-original",
			Proxy:   "http://proxy:8080",
			Headers: map[string]string{"X-Title": "lele"},
		},
		WebSearch: &on,
	}
	m.cfg.Providers.OpenAI.WebSearch = true

	// A regular file where the config directory should be: MkdirAll fails, so
	// SaveConfig cannot write and updateProvider must roll back.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("preparing the blocked config dir: %v", err)
	}
	t.Setenv("LELE_CONFIG_DIR", filepath.Join(blocked, "cfg"))

	before := m.cfg.Providers.Named["openai"]
	typedBefore := m.cfg.Providers.OpenAI.ProviderConfig

	err := m.updateProvider("openai", providerEditValues{
		Type:        "anthropic",
		Proxy:       "http://new-proxy:9090",
		AuthMethod:  "oauth",
		ConnectMode: "grpc",
		WebSearch:   "no",
		Headers:     `{"X-Title":"other"}`,
	})
	if err == nil {
		t.Fatal("updateProvider must fail when the config cannot be saved")
	}

	if after := m.cfg.Providers.Named["openai"]; !reflect.DeepEqual(after, before) {
		t.Errorf("named entry not rolled back:\n got %+v\nwant %+v", after, before)
	}
	if got := m.cfg.Providers.OpenAI.ProviderConfig; !reflect.DeepEqual(got, typedBefore) {
		t.Errorf("typed mirror not rolled back: got %+v, want %+v", got, typedBefore)
	}
	if got := m.cfg.Providers.OpenAI.WebSearch; !got {
		t.Error("typed OpenAI.WebSearch not rolled back: got false, want true")
	}
} // TestProviderDetailShowsOptionalInfoRows covers the detail view's extra rows:
// they appear only for fields that are actually set, the header row lists NAMES
// (never values), and the model/action-index maps still resolve with the
// additional rows in place.
func TestProviderDetailShowsOptionalInfoRows(t *testing.T) {
	m := newProviderEditFullModel(t)
	layout := m.providerDetailList("fullprov")

	wantInfo := []string{
		"Type: openai",
		"API Base: https://api.example.com/v1",
		"API Key: sk-1...cdef",
		"Proxy: http://proxy.internal:8080",
		"Auth method: oauth",
		"Connect mode: stdio",
		"Web search: yes",
		"Headers: X-Title, X-Trace", // sorted names, never values
	}
	if len(layout.Items) < len(wantInfo) {
		t.Fatalf("items = %q, want at least %d info rows", layout.Items, len(wantInfo))
	}
	for i, want := range wantInfo {
		if layout.Items[i] != want {
			t.Errorf("items[%d] = %q, want %q", i, layout.Items[i], want)
		}
	}

	// The maps are computed from the growing list, so they must still resolve:
	// the model row sits right after the separator that follows the info rows.
	if alias, ok := layout.ModelRows[len(wantInfo)+1]; !ok || alias != "alpha" {
		t.Errorf("ModelRows[%d] = %q, %v; want alpha", len(wantInfo)+1, alias, ok)
	}
	if got, want := layout.Items[layout.EditIdx], i18n.T("tui.editProviderAction"); got != want {
		t.Errorf("items[EditIdx] = %q, want %q", got, want)
	}
	if layout.EditIdx >= layout.AddIdx || layout.AddIdx >= layout.DelIdx {
		t.Errorf("action rows out of order: edit=%d add=%d del=%d", layout.EditIdx, layout.AddIdx, layout.DelIdx)
	}
	for idx, alias := range layout.ModelRows {
		if strings.HasPrefix(alias, "X-") || strings.Contains(alias, "Proxy") {
			t.Errorf("ModelRows[%d] = %q must not map an info row", idx, alias)
		}
	}
}

// TestProviderDetailHidesUnsetOptionalRows is the other half: a provider with no
// optional fields keeps exactly the three original info rows, so the extra rows
// never turn into a row of empty labels.
func TestProviderDetailHidesUnsetOptionalRows(t *testing.T) {
	m := newProviderHelpersTestModel(t)
	layout := m.providerDetailList("myprov")

	wantInfo := []string{
		"Type: openai",
		"API Base: https://api.example.com/v1",
		"API Key: sk-1...cdef",
	}
	for i, want := range wantInfo {
		if layout.Items[i] != want {
			t.Errorf("items[%d] = %q, want %q", i, layout.Items[i], want)
		}
	}
	if got := layout.Items[len(wantInfo)]; got != "---" {
		t.Errorf("items[%d] = %q, want the models separator (no empty optional rows)", len(wantInfo), got)
	}
}

// TestProviderDetailShowsWebSearchNo covers the *bool rendering: an explicit
// "no" is a set value and must be shown (only nil means "unset").
func TestProviderDetailShowsWebSearchNo(t *testing.T) {
	m := newProviderHelpersTestModel(t)
	// Same shape as the withWebSearch test helper, but an explicit "no".
	off := false
	empty := m.cfg.Providers.Named["emptyprov"]
	empty.WebSearch = &off
	m.cfg.Providers.Named["emptyprov"] = empty

	layout := m.providerDetailList("emptyprov")
	found := false
	for _, item := range layout.Items {
		if item == "Web search: no" {
			found = true
		}
	}
	if !found {
		t.Errorf("items = %q, want a \"Web search: no\" row", layout.Items)
	}
}

// TestProviderEditFormShapeMatchesConstants guards the one invariant the whole
// edit flow rests on: formValues, formStepNames() and editProviderStepCount must
// describe the same eight steps. If they drift, the ENTER handler either saves
// too early or reads a value that was never collected.
func TestProviderEditFormShapeMatchesConstants(t *testing.T) {
	m := newProviderEditFullModel(t)
	m.providerSelectedName = "fullprov"
	if !m.openProviderEditFlow() {
		t.Fatalf("openProviderEditFlow() = false: %s", m.providerFeedback)
	}

	steps := m.formStepNames()
	if len(steps) != editProviderStepCount {
		t.Fatalf("formStepNames() has %d steps, want %d: %q", len(steps), editProviderStepCount, steps)
	}
	if len(m.formValues) != editProviderStepCount {
		t.Fatalf("formValues has %d entries, want %d: %q", len(m.formValues), editProviderStepCount, m.formValues)
	}

	// Every step must be reachable by pressing ENTER, and the last one saves.
	for step := 0; step < editProviderStepCount-1; step++ {
		if m.formStepIndex != step {
			t.Fatalf("at step %d, want %d (formError = %q)", m.formStepIndex, step, m.formError)
		}
		m = sendKeys(m, "\r")
		if m.modalMode != ModalEditProvider {
			t.Fatalf("ENTER on step %d closed the form (formError = %q)", step, m.formError)
		}
	}
	if m.formStepIndex != editProviderStepCount-1 {
		t.Fatalf("formStepIndex = %d, want the last step (%d)", m.formStepIndex, editProviderStepCount-1)
	}
	m = sendKeys(m, "\r")
	if m.modalMode != ModalProviderDetail {
		t.Fatalf("the last ENTER did not save: modal = %v (formError = %q)", m.modalMode, m.formError)
	}
}
