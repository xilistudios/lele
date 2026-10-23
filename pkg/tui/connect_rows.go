package tui

import (
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// connectRowKind classifies what a /connect picker row does when the user
// presses ENTER on it. The kind — not the label — is what the dispatch in
// handlers_modal.go switches on, so the row catalogue stays the single source
// of truth for the flow shape.
type connectRowKind int

const (
	// connectRowCustomOpenAI / connectRowCustomAnthropic are the two
	// "bring your own compatible endpoint" rows. They run the FULL flow:
	// name → API key → API base → model steps (the provider type is known,
	// so the free-text "provider type" step is skipped).
	connectRowCustomOpenAI connectRowKind = iota
	connectRowCustomAnthropic
	// connectRowAPIKey is a known provider that only needs an API key: the
	// name is derived from the provider type, the API base comes from the
	// preset, and BOTH the type and base steps are skipped — the user lands
	// on the API-key step and, on Enter, on the model steps.
	connectRowAPIKey
	// connectRowOAuth is a native provider OAuth login: it runs pkg/auth's
	// non-blocking ProviderFlow, stores the resulting credential under the
	// canonical provider name and writes the provider auth method needed to
	// select the subscription/Claude/Gemini Code Assist/Codex transport.
	// oauthName holds the canonical pkg/auth preset name for that row.
	connectRowOAuth
	// connectRowFreeType is the trailing "type it yourself" row: it
	// reproduces the legacy flow exactly (name → free-text type → API key →
	// API base → model steps).
	connectRowFreeType
)

// connectRow is one entry of the /connect provider picker.
//
// Design note (row-first flow): a row fully describes the entry the user
// picks — the kind decides which steps are skipped, and the preset-derived
// fields (typ/apiBase/keyHint/modelHint/defaultModel/defaultModelAlias) are
// copied so the row itself needs no lookup later. Step NUMBERS never change:
// API-key masking is still step 2, the API base is still step 3 and the model
// steps still start at 4 — a row only decides which of them the user is sent
// to, so every existing render/mask rule keeps working unmodified.
type connectRow struct {
	kind    connectRowKind
	label   string // already i18n-resolved
	typ     string // NamedProviderConfig.Type ("" for the free-type row)
	name    string // config key; "" means the user types it
	apiBase string

	keyHint           string // hint shown next to the API Key step
	modelHint         string // hint shown next to the Model alias step
	defaultModel      string // default actual model name to pre-fill
	defaultModelAlias string // default model alias to pre-fill

	// oauthName is the canonical pkg/auth registry name an OAuth row logs in
	// to ("" for every non-OAuth row). connectOAuthStartFromRow resolves the
	// provider config from THIS field, so the row stays the single source of
	// truth for what a picker entry does — the catalog lookup is only needed
	// afterwards, to persist the provider the transport expects.
	oauthName string
}

// connectOAuthRowMeta describes a native provider OAuth row. The registry
// remains the source of truth for endpoints and canonical names; this table
// only decides catalogue order, the localized row label and how the selected
// provider is persisted after the credential is stored.
type connectOAuthRowMeta struct {
	// provider is the canonical pkg/auth registry/credential-store name.
	provider string
	// label is the localized picker label.
	label string
	// configType is NamedProviderConfig.Type for the /connect route.
	configType string
	// configName is the named provider key /models route uses.
	configName string
	// authMethod is the value written to providers.<name>.auth_method.
	authMethod string

	// requiresEmptyAPIKey declares whether the OAuth transport this entry
	// selects needs providers.<name>.api_key to be EMPTY. It is true ONLY for
	// gemini: pkg/providers/factory.go requires auth_method == "oauth" AND an
	// empty API key for the Google Code Assist transport (both the typed
	// Providers.Gemini pre-check and the Named-entry case), so a stale key
	// MUST be cleared there. For openai (Codex) and anthropic (Claude
	// subscription) the transport is selected from auth_method alone, with no
	// api-key condition, so clearing a developer key would be pure data loss.
	requiresEmptyAPIKey bool

	// modelHint/defaultModel/defaultModelAlias optionally OVERRIDE the
	// providerPreset-derived model metadata for the OAuth route. They are
	// needed when the OAuth transport's model space differs from the
	// developer-API one: an openai login selects the Codex transport, whose
	// default model is gpt-5.2 (pkg/providers/codex_provider.go), while the
	// preset's gpt-4o is a developer-API model. Empty means "keep the preset
	// value" (anthropic/gemini keep their presets byte-identically).
	modelHint         string
	defaultModel      string
	defaultModelAlias string
}

// connectOAuthCatalog maps a known providerPreset type to the native OAuth
// registry entry. The order is deliberately the providerPresets order:
// connectRows emits OAuth immediately before the matching API-key row.
//
// No "google-antigravity" entry (there used to be one): it was unreachable
// (providerPresets has no antigravity entry, so connectRows never emitted an
// OAuth row for it) AND pkg/providers has no selection case for it — factory.go
// does not know the type and NewAntigravityProvider() has zero non-test
// callers — so a login would store a credential nothing reads and leave the
// user with an unusable provider. Antigravity OAuth exists in pkg/auth
// (GoogleAntigravityOAuthConfig) but has no pkg/providers selection path yet;
// follow-up: wire the transport, then add the row + a preset here.
var connectOAuthCatalog = map[string]connectOAuthRowMeta{
	"openai": {
		provider:   "openai",
		label:      "tui.connectRowOAuthOpenAI",
		configType: "openai",
		configName: "openai",
		authMethod: "oauth",
		// The Codex transport's model space: gpt-4o (the preset default) is a
		// developer-API model the ChatGPT backend rejects, so the OAuth route
		// overrides it with the transport default (codexDefaultModel).
		modelHint:         "gpt-5.2 (Codex)...",
		defaultModel:      "gpt-5.2",
		defaultModelAlias: "gpt-5.2",
	},
	"anthropic": {
		provider:   "anthropic",
		label:      "tui.connectRowOAuthAnthropic",
		configType: "anthropic",
		configName: "anthropic",
		authMethod: "oauth",
		// Claude subscription accepts the developer-API model names too, so
		// the preset values are kept (empty override).
	},
	"gemini": {
		provider:   "gemini",
		label:      "tui.connectRowOAuthGemini",
		configType: "gemini",
		configName: "gemini",
		authMethod: "oauth",
		// Code Assist needs the key cleared (see requiresEmptyAPIKey).
		requiresEmptyAPIKey: true,
	},
}

// connectRows builds the /connect picker catalogue: the two custom rows
// (full flow), one row per native OAuth preset and known API-key preset
// (key-only flow, in the exact providerPresets order), and the trailing
// free-type row (legacy flow).
//
// It is called on every picker frame, so it must stay cheap: it only copies
// preset strings (no I/O, no allocation beyond the returned slice) and does
// not resolve i18n lazily — the label keys are resolved here once per call.
// Deliberately NOT memoised: providerPresets is static, but i18n.T() is
// language-dependent and a stale memo would keep rendering the previous
// language after the user switches it in settings.
func connectRows() []connectRow {
	rows := make([]connectRow, 0, len(providerPresets)+3)
	rows = append(rows,
		connectRow{
			kind:  connectRowCustomOpenAI,
			label: i18n.T("tui.connectRowCustomOpenAI"),
			typ:   "openai",
		},
		connectRow{
			kind:  connectRowCustomAnthropic,
			label: i18n.T("tui.connectRowCustomAnthropic"),
			typ:   "anthropic",
		},
	)
	for i := range providerPresets {
		p := providerPresets[i]
		// OAuth-capable providers (the presets registered in pkg/auth's
		// native-flow registry) get an OAuth row immediately before their
		// API-key row. That preserves the catalogue invariant every existing
		// index-based test relies on — API-key rows still start at index 2
		// and remain in providerPresets order — while offering the
		// credential-native sign-in first. Both routes may coexist on purpose:
		// an OAuth row configures a Codex/Code Assist/Claude subscription or
		// Cloud Code transport, while the API-key row is the developer-key
		// alternative.
		if meta, ok := connectOAuthCatalog[p.typ]; ok {
			// The catalog entry may override the preset-derived model
			// metadata (OAuth transports whose model space differs from the
			// developer-API one, e.g. openai → Codex). Empty override keeps
			// the preset value, so anthropic/gemini are byte-identical to
			// their API-key rows.
			modelHint, defaultModel, defaultModelAlias := p.modelHint, p.defaultModel, p.defaultModelAlias
			if meta.modelHint != "" {
				modelHint = meta.modelHint
			}
			if meta.defaultModel != "" {
				defaultModel = meta.defaultModel
			}
			if meta.defaultModelAlias != "" {
				defaultModelAlias = meta.defaultModelAlias
			}
			rows = append(rows, connectRow{
				kind: connectRowOAuth,
				// connectOAuthRowMeta.label is an i18n KEY (not a rendered
				// string): resolve it here, exactly like the other rows, so a
				// language switch re-renders the row in the new language.
				label:             i18n.T(meta.label),
				typ:               p.typ,
				name:              meta.provider,
				apiBase:           p.apiBase,
				modelHint:         modelHint,
				defaultModel:      defaultModel,
				defaultModelAlias: defaultModelAlias,
				oauthName:         meta.provider,
			})
		}
		rows = append(rows, connectRow{
			kind:              connectRowAPIKey,
			label:             p.label,
			typ:               p.typ,
			name:              p.typ,
			apiBase:           p.apiBase,
			keyHint:           p.keyHint,
			modelHint:         p.modelHint,
			defaultModel:      p.defaultModel,
			defaultModelAlias: p.defaultModelAlias,
		})
	}
	rows = append(rows, connectRow{
		kind:  connectRowFreeType,
		label: i18n.T("tui.connectCustomType"),
	})
	return rows
}

// connectRowAt returns the picker row at index i, or nil when out of range.
// The returned row points into a freshly built catalogue (connectRows is not
// memoised — see its doc), so it must not be stored across calls.
func connectRowAt(i int) *connectRow {
	if i < 0 {
		return nil
	}
	rows := connectRows()
	if i >= len(rows) {
		return nil
	}
	return &rows[i]
}

// connectRowByIndex returns the picker row at index i. The second return
// value is false when i is out of range (defensive: the picker bounds are
// clamped by providerTypePickerMax, which is len(connectRows()) on the
// row-driven path).
func connectRowByIndex(i int) (connectRow, bool) {
	if i < 0 {
		return connectRow{}, false
	}
	rows := connectRows()
	if i >= len(rows) {
		return connectRow{}, false
	}
	return rows[i], true
}
