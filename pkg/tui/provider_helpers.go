package tui

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/catalog"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// saveConfigToDisk persists the current in-memory config to disk.
func (m *Model) saveConfigToDisk() error {
	if err := config.SaveConfig(config.DefaultConfigPath(), m.cfg); err != nil {
		return err
	}
	// Publish a fresh private copy to the agent loop so its goroutines never
	// read the pointer the TUI keeps mutating (audit C1 data race). Only on
	// success: a failed save must leave the loop on the last known-good config.
	if m.agentLoop != nil {
		m.agentLoop.UpdateConfigSnapshot(m.cfg.Snapshot())
	}
	return nil
}

// listProviders returns a sorted list of provider names from the config.
func (m *Model) listProviders() []string {
	if m.agentLoop == nil {
		return nil
	}
	snapshot := m.agentLoop.GetProvidable().GetConfigSnapshot()
	if snapshot == nil || snapshot.Providers == nil {
		return nil
	}
	named := snapshot.Providers.ListNamed()
	names := make([]string, 0, len(named))
	for name, cfg := range named {
		if len(cfg.Models) > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// listProviderModels returns a sorted list of model aliases for a given
// provider. Reads from configSource() (the agent-loop snapshot when
// available, m.cfg otherwise) so it also works with the nil agentLoop used
// by unit tests.
func (m *Model) listProviderModels(providerName string) []string {
	cfg := m.configSource()
	if cfg == nil || cfg.Providers == nil {
		return nil
	}
	provider, ok := cfg.Providers.GetNamed(providerName)
	if !ok || len(provider.Models) == 0 {
		return nil
	}
	models := make([]string, 0, len(provider.Models))
	for alias := range provider.Models {
		models = append(models, alias)
	}
	sort.Strings(models)
	return models
}

// addProvider adds a new provider to the config and persists to disk.
func (m *Model) addProvider(name, providerType, apiKey, apiBase string) error {
	if m.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if m.cfg.Providers == nil {
		m.cfg.Providers = &config.ProvidersConfig{}
	}
	if m.cfg.Providers.Named == nil {
		m.cfg.Providers.Named = make(map[string]config.NamedProviderConfig)
	}

	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return fmt.Errorf("provider name cannot be empty")
	}
	if _, exists := m.cfg.Providers.Named[key]; exists {
		return fmt.Errorf("provider %q already exists", key)
	}

	m.cfg.Providers.Named[key] = config.NamedProviderConfig{
		Type: providerType,
		ProviderConfig: config.ProviderConfig{
			APIKey:  apiKey,
			APIBase: apiBase,
		},
	}

	return m.saveConfigToDisk()
}

// providerEditValues carries the submitted values of the ModalEditProvider
// form — one field per step, in formValues order (see formStepNames). It is a
// struct rather than a positional string slice so a new step cannot silently
// shift an existing value into the wrong config field.
type providerEditValues struct {
	Type        string // provider type (required)
	APIKey      string // "" keeps the stored key
	APIBase     string // "" keeps the stored base URL
	Proxy       string // "" keeps, "-" clears
	AuthMethod  string // "" keeps, "-" clears
	ConnectMode string // "" keeps, "-" clears
	WebSearch   string // "" keeps, "yes"/"no" set, "default" unsets
	Headers     string // "" keeps, "{}" clears, else JSON object
}

// Steps of the ModalEditProvider form: indexes into formValues and
// formStepNames(). They are named because the secret-masking predicates
// (secret_fields.go) and the ENTER handler both key off them, and two magic
// numbers drifting apart would either unmask the headers step or mask a
// harmless one.
const (
	editProviderTypeStep = iota
	editProviderAPIKeyStep
	editProviderAPIBaseStep
	editProviderProxyStep
	editProviderAuthMethodStep
	editProviderConnectModeStep
	editProviderWebSearchStep
	editProviderHeadersStep
	// editProviderStepCount is the number of steps of the form: it is the
	// length of both formValues and formStepNames().
	editProviderStepCount
)

// Steps of the model forms. ModalAddModel collects one value per model field
// (alias → think system, see addModelStepCount); ModalEditModel shares those
// six steps and appends one step per config.ReasoningConfig field (see
// editModelStepCount) so the stored reasoning override of an existing model can
// be edited instead of merely being preserved.
//
// They are named because openModelEditFlow, formStepNames(), the shared ENTER
// handler and the tests all key off them: two magic numbers drifting apart
// would either save too early or read a value that was never collected.
const (
	addModelAliasStep = iota
	addModelModelNameStep
	addModelContextWindowStep
	addModelMaxTokensStep
	addModelVisionStep
	addModelThinkSystemStep
	// addModelStepCount is the number of steps of the ADD form: the length of
	// both formValues and formStepNames() for ModalAddModel, and the index
	// right after the last step (the think-system step saves).
	addModelStepCount
)

const (
	// The edit-model form continues where the add form stops: one step per
	// ReasoningConfig field, in struct order.
	editModelReasoningEnableStep = addModelStepCount + iota
	editModelReasoningEffortStep
	editModelReasoningMaxTokensStep
	editModelReasoningExcludeStep
	editModelReasoningSummaryStep
	// editModelStepCount is the number of steps of the EDIT form: the length of
	// both formValues and formStepNames() for ModalEditModel, and the index
	// right after the last step (the reasoning-summary step saves).
	editModelStepCount
)

// modelFormLastStep returns the index of the step that SAVES the model form:
// the think-system step for the add form, the reasoning-summary step for the
// edit form. Everything below it just advances.
func modelFormLastStep(mode modalType) int {
	if mode == ModalEditModel {
		return editModelStepCount - 1
	}
	return addModelStepCount - 1
}

// modelFormAllowsEmptyValue reports whether the given step of the model form
// may be submitted blank. The context-window and max-tokens steps are filled
// from the catalog when the model is known, the think-system step defaults to
// "auto", and the four optional reasoning steps mean "leave this override
// unset". Every other step carries a required answer (the reasoning-enabled
// step is always pre-filled with yes/no, so it never needs the escape hatch).
func modelFormAllowsEmptyValue(mode modalType, step int) bool {
	switch step {
	case addModelContextWindowStep, addModelMaxTokensStep, addModelThinkSystemStep:
		return true
	}
	// The reasoning steps exist only in the edit form; a stray index must not
	// loosen the add form.
	if mode != ModalEditModel {
		return false
	}
	switch step {
	case editModelReasoningEffortStep, editModelReasoningMaxTokensStep,
		editModelReasoningExcludeStep, editModelReasoningSummaryStep:
		return true
	}
	return false
}

// updateProvider edits an existing provider — every field of its config except
// the name and the model list — and persists to disk. It is the save point of
// the ModalEditProvider form (opened from the provider detail view with "e" or
// the "Edit provider" action row).
//
// Blank means "keep": the form is a plain text widget, so the stored secret is
// never rendered back into it (see secret_fields.go). An empty API key, API
// base URL, proxy, auth method, connect mode, web-search answer or header blob
// therefore leaves the stored value untouched instead of wiping it, and an
// empty type keeps the stored one — a provider can never lose its identity
// because the user pressed Enter through a step.
//
// The three optional middle fields (proxy, auth method, connect mode) take a
// lone "-" as "clear it", so an env-provided value the form cannot show can
// still be removed from the file; WebSearch takes "default" to go back to
// unset (nil), and Headers takes "{}" to remove every custom header.
//
// The entry is updated IN PLACE: Models, and every field the form does not ask
// about (or that this edit did not change), survive an edit exactly like
// ensureProvider does for a re-run of /connect — rebuilding the entry would
// drop the model list.
//
// Only the fields this edit actually CHANGED are mirrored into the typed field
// (transport selection is TYPE-FIRST, so ensureProvider mirrors them too): see
// syncEditedProviderCredentials. The one exception is the typed OpenAI
// web_search bool, which is not a ProviderConfig field and is projected
// explicitly below.
//
// A failed save rolls EVERY copy back (named entry, typed ProviderConfig and
// the typed OpenAI web_search bool). The form keeps the user on the error so
// the typed values can be retried, and ESC then returns to the detail without
// re-reading the file — leaving the rejected edit in memory would let any later
// save from another flow persist a change this one already reported as failed.
func (m *Model) updateProvider(name string, v providerEditValues) error {
	if m.cfg == nil || m.cfg.Providers == nil || m.cfg.Providers.Named == nil {
		return fmt.Errorf("no providers configured")
	}

	key := strings.ToLower(strings.TrimSpace(name))
	previous, ok := m.cfg.Providers.Named[key]
	if !ok {
		return fmt.Errorf("provider %q not found", key)
	}

	updated := previous
	if typ := strings.ToLower(strings.TrimSpace(v.Type)); typ != "" {
		updated.Type = typ
	}
	// Which credentials this edit actually CHANGED. Blank means "keep", and a
	// field submitted with the value already stored (Enter-through over the
	// pre-filled step) is not a change either — so pressing Enter through the
	// form cannot overwrite the typed copy, which may be the only carrier of an
	// env-provided credential (see syncEditedProviderCredentials).
	k := strings.TrimSpace(v.APIKey)
	base := strings.TrimSpace(v.APIBase)
	changes := providerEditChanges{
		APIKey:  k != "" && k != previous.APIKey,
		APIBase: base != "" && base != previous.APIBase,
	}
	if changes.APIKey {
		updated.APIKey = k
	}
	if changes.APIBase {
		updated.APIBase = base
	}

	updated.Proxy, changes.Proxy = resolveOptionalEditField(previous.Proxy, v.Proxy)
	updated.AuthMethod, changes.AuthMethod = resolveOptionalEditField(previous.AuthMethod, v.AuthMethod)
	updated.ConnectMode, changes.ConnectMode = resolveOptionalEditField(previous.ConnectMode, v.ConnectMode)

	webSearchToken, err := parseWebSearchToken(v.WebSearch)
	if err != nil {
		return err
	}
	updated.WebSearch, changes.WebSearch = resolveWebSearchEdit(previous.WebSearch, webSearchToken)

	// Headers are validated here as well as in the handler: updateProvider is
	// also called from tests and from any future caller that skips the form.
	storedHeaders, headersChanged, err := parseHeaderEditValue(previous.Headers, v.Headers)
	if err != nil {
		return err
	}
	updated.Headers = storedHeaders
	changes.Headers = headersChanged

	// Snapshot the typed mirrors so a failed save can put them back verbatim
	// (re-deriving them from `previous` would clobber an env-provided value).
	typedSlot := typedProviderSlot(m.cfg.Providers, key)
	var typedBefore config.ProviderConfig
	if typedSlot != nil {
		typedBefore = *typedSlot
	}
	// The typed OpenAI web_search bool lives outside ProviderConfig, so it needs
	// its own snapshot. Harmless for every other provider: nothing touches it.
	typedWebSearchBefore := m.cfg.Providers.OpenAI.WebSearch

	m.cfg.Providers.Named[key] = updated
	syncEditedProviderCredentials(m.cfg.Providers, key, updated, changes)
	// WebSearch is a *bool on the named entry but a plain bool on the typed
	// OpenAI slot, which is the copy the transports read (factory.go). Project
	// it only when this edit touched it, so the env/first-load default is not
	// silently overwritten by an untouched step.
	if key == "openai" && changes.WebSearch {
		m.cfg.Providers.OpenAI.WebSearch = updated.WebSearch != nil && *updated.WebSearch
	}

	if err := m.saveConfigToDisk(); err != nil {
		m.cfg.Providers.Named[key] = previous
		if typedSlot != nil {
			*typedSlot = typedBefore
		}
		m.cfg.Providers.OpenAI.WebSearch = typedWebSearchBefore
		return err
	}
	return nil
}

// resolveOptionalEditField resolves one of the "blank keeps / - clears" text
// fields of the edit form (proxy, auth method, connect mode). It returns the
// value to store and whether the edit changed it — a value re-submitted
// unchanged (Enter over the pre-filled step) is not a change, so it cannot
// overwrite a typed copy that carries an env-provided value.
func resolveOptionalEditField(previous, input string) (value string, changed bool) {
	trimmed := strings.TrimSpace(input)
	switch trimmed {
	case "":
		return previous, false
	case "-":
		// Blank means "keep", so "-" is the only way to ask for the value to be
		// removed from the file.
		return "", previous != ""
	default:
		return trimmed, trimmed != previous
	}
}

// parseWebSearchToken canonicalizes the web-search answer of the edit form.
// "" (keep), "yes", "no" and "default" are the accepted tokens; everything else
// is an error so the handler can keep the user on the step instead of saving a
// token the loader would ignore.
func parseWebSearchToken(input string) (string, error) {
	switch token := strings.ToLower(strings.TrimSpace(input)); token {
	case "", "yes", "no", "default":
		return token, nil
	default:
		return "", fmt.Errorf("invalid web search value %q: use yes, no or default", strings.TrimSpace(input))
	}
}

// resolveWebSearchEdit maps a canonical web-search token (see
// parseWebSearchToken) onto the *bool the config stores, returning the value to
// store and whether it differs from the previous one. nil means "unset": the
// transports then fall back to their own default.
func resolveWebSearchEdit(previous *bool, token string) (value *bool, changed bool) {
	switch token {
	case "":
		return previous, false
	case "default":
		return nil, previous != nil
	case "yes":
		on := true
		return &on, previous == nil || !*previous
	default: // "no" — parseWebSearchToken rejects anything else
		off := false
		return &off, previous == nil || *previous
	}
}

// parseHeaderEditValue resolves the Headers step of the edit form. "" keeps the
// stored headers, "{}" (and "null") clears them, and anything else must be a
// JSON object of string→string that passes
// protocoltypes.ValidateCustomHeaders — the same validation config load runs,
// so a value the loader would reject can never be saved from the TUI.
//
// The returned map is the freshly decoded one (never the previous map), so the
// caller can store it without cloning; a kept map is returned as-is.
func parseHeaderEditValue(previous map[string]string, input string) (map[string]string, bool, error) {
	trimmed := strings.TrimSpace(input)
	switch trimmed {
	case "":
		return previous, false, nil
	case "{}", "null":
		return nil, len(previous) > 0, nil
	}

	var parsed map[string]string
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, false, fmt.Errorf("headers must be a JSON object of string values, e.g. {\"X-Title\":\"lele\"}: %v", err)
	}
	if len(parsed) == 0 {
		return nil, len(previous) > 0, nil
	}
	if err := protocoltypes.ValidateCustomHeaders(parsed); err != nil {
		return nil, false, fmt.Errorf("invalid headers: %v", err)
	}
	return parsed, !maps.Equal(previous, parsed), nil
}

// cloneHeaders deep-copies a header map. The named entry and its typed mirror
// are two copies of one provider, so they must never share the same map:
// mutating one later (an OAuth flow adding a header, for instance) would
// silently mutate the other.
func cloneHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for name, value := range headers {
		cloned[name] = value
	}
	return cloned
}

// headerNames returns the names of a header map, sorted, for display. Only the
// names: values can carry credentials (an Authorization override, a gateway
// token), so the detail view must never print them.
func headerNames(headers map[string]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// webSearchFormValue renders the stored *bool web_search as the edit form's
// token, so the step can be re-submitted unchanged: nil is "default" (unset),
// true is "yes", false is "no".
func webSearchFormValue(v *bool) string {
	switch {
	case v == nil:
		return "default"
	case *v:
		return "yes"
	default:
		return "no"
	}
}

// ensureProvider creates or updates the named provider for a row-driven
// /connect flow. It reports whether the provider was already usable (it had
// at least one model), so the caller can skip the model wizard.
//
// Rules:
//   - absent → create it (Type/APIKey/APIBase) and persist;
//   - present as an EMPTY PLACEHOLDER (see config.NamedProviderConfig.IsEmpty: no key, no
//     base, no proxy, no models, no auth method) → overwrite it wholesale (no
//     "already exists" error). A keyless entry that carries an endpoint
//     (APIBase/Proxy) is NOT a placeholder: /connect updates it in place.
//   - present with data → update the credentials IN PLACE (Models, Proxy,
//     WebSearch, ConnectMode and a custom APIBase are kept) and report
//     alreadyUsable=len(Models)>0, so re-running /connect for an
//     already-configured provider just refreshes the key and lands on the
//     success screen.
//
// baseIsPreset marks apiBase as the row's preset default rather than a value
// the user typed: the API-key rows skip the API-base step, so for them
// formValues[3] is the preset and a deliberate custom endpoint (a keyless
// local provider on another host, a proxied setup) must win. The custom rows
// do show the base step, so there the typed value is authoritative.
//
// The named entry is mirrored into its typed field (openai, anthropic, …)
// BEFORE saving because transport selection is TYPE-FIRST: pkg/providers
// reads Providers.<Name> before it consults Providers.Named (factory.go: the
// Gemini Code Assist pre-check, then the anthropic/gemini cases that gate on
// auth_method == "oauth"), so a credential that only reached the named copy
// would be persisted but invisible to the selector that has to use it.
// MarshalJSON is the mirror image — it writes the named copy and skips the
// typed one — which is why both copies are kept in sync rather than one of
// them being derived on read.
func (m *Model) ensureProvider(name, providerType, apiKey, apiBase string, baseIsPreset bool) (alreadyUsable bool, err error) {
	if m.cfg == nil {
		return false, fmt.Errorf("config is nil")
	}
	if m.cfg.Providers == nil {
		m.cfg.Providers = &config.ProvidersConfig{}
	}
	if m.cfg.Providers.Named == nil {
		m.cfg.Providers.Named = make(map[string]config.NamedProviderConfig)
	}

	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return false, fmt.Errorf("provider name cannot be empty")
	}

	existing, exists := m.cfg.Providers.Named[key]
	if !exists || existing.IsEmpty() {
		m.cfg.Providers.Named[key] = config.NamedProviderConfig{
			Type: providerType,
			ProviderConfig: config.ProviderConfig{
				APIKey:  apiKey,
				APIBase: apiBase,
			},
		}
		syncProviderToTypedField(m.cfg.Providers, key, m.cfg.Providers.Named[key])
		return false, m.saveConfigToDisk()
	}

	// In-place update. Never rebuild the entry here: a provider with Models is
	// configured, and replacing it would drop them (the user would lose the
	// model list by re-entering a key).
	alreadyUsable = len(existing.Models) > 0
	providerConfig := existing.ProviderConfig
	if k := strings.TrimSpace(apiKey); k != "" {
		providerConfig.APIKey = k
	}
	if b := strings.TrimSpace(apiBase); b != "" {
		// A preset base only seeds an empty endpoint; anything else would
		// silently move the provider off the host the user configured.
		if !baseIsPreset || strings.TrimSpace(providerConfig.APIBase) == "" {
			providerConfig.APIBase = b
		}
	}
	existing.ProviderConfig = providerConfig
	if strings.TrimSpace(existing.Type) == "" {
		existing.Type = providerType
	}
	m.cfg.Providers.Named[key] = existing
	syncProviderToTypedField(m.cfg.Providers, key, existing)
	return alreadyUsable, m.saveConfigToDisk()
}

// deleteProvider removes a provider from the config and persists to disk.
func (m *Model) deleteProvider(name string) error {
	if m.cfg == nil || m.cfg.Providers == nil || m.cfg.Providers.Named == nil {
		return fmt.Errorf("no providers configured")
	}

	key := strings.ToLower(strings.TrimSpace(name))
	if _, ok := m.cfg.Providers.Named[key]; !ok {
		return fmt.Errorf("provider %q not found", key)
	}

	delete(m.cfg.Providers.Named, key)
	return m.saveConfigToDisk()
}

// normalizeThinkSystemInput validates and canonicalizes the free-text think
// system entry of a model form step. Empty and "auto" both mean "leave unset"
// and return "". Invalid values return an error listing the accepted options.
func normalizeThinkSystemInput(val string) (string, error) {
	tt, ok := config.NormalizeThinkingType(strings.TrimSpace(val))
	if !ok {
		return "", fmt.Errorf("must be one of: auto, deepseek, openai, openrouter, qwen, none (Enter for auto)")
	}
	if tt == config.ThinkingTypeAuto {
		return "", nil
	}
	return tt, nil
}

// addModelToProvider adds a model alias to a provider's Models map.
// When the model is known to pkg/catalog and context_window / max_tokens were
// left empty (zero), the values are filled from catalog.DefaultsFor. Models
// that advertise thinking levels get a default Reasoning.Effort so reasoning
// works out of the box. thinkingType is the wire-level "think system"
// (see config.ThinkingType*); "" and "auto" leave the field unset.
func (m *Model) addModelToProvider(providerName, alias, modelName string, contextWindow, maxTokens int, vision bool, thinkingType string) error {
	if m.cfg == nil || m.cfg.Providers == nil || m.cfg.Providers.Named == nil {
		return fmt.Errorf("no providers configured")
	}

	key := strings.ToLower(strings.TrimSpace(providerName))
	provider, ok := m.cfg.Providers.Named[key]
	if !ok {
		return fmt.Errorf("provider %q not found", key)
	}

	if provider.Models == nil {
		provider.Models = make(map[string]config.ProviderModelConfig)
	}

	aliasKey := strings.ToLower(strings.TrimSpace(alias))
	if aliasKey == "" {
		return fmt.Errorf("model alias cannot be empty")
	}

	normalizedThink, err := normalizeThinkSystemInput(thinkingType)
	if err != nil {
		return err
	}

	var reasoning *config.ReasoningConfig
	if catKey := m.catalogProviderKey(key); catKey != "" {
		if d, ok := catalog.DefaultsFor(catKey, modelName); ok {
			if contextWindow == 0 {
				contextWindow = d.ContextWindow
			}
			if maxTokens == 0 {
				maxTokens = d.MaxTokens
			}
			if len(d.ThinkingLevels) > 0 || d.Reasoning {
				effort := defaultThinkingEffort(d.ThinkingLevels)
				reasoning = &config.ReasoningConfig{Effort: &effort}
			}
		}
	}

	provider.Models[aliasKey] = config.ProviderModelConfig{
		Model:         modelName,
		ContextWindow: contextWindow,
		MaxTokens:     maxTokens,
		Vision:        vision,
		Reasoning:     reasoning,
		ThinkingType:  normalizedThink,
	}
	m.cfg.Providers.Named[key] = provider

	return m.saveConfigToDisk()
}

// deleteModelFromProvider removes a model alias from a provider's Models map.
func (m *Model) deleteModelFromProvider(providerName, alias string) error {
	if m.cfg == nil || m.cfg.Providers == nil || m.cfg.Providers.Named == nil {
		return fmt.Errorf("no providers configured")
	}

	key := strings.ToLower(strings.TrimSpace(providerName))
	provider, ok := m.cfg.Providers.Named[key]
	if !ok {
		return fmt.Errorf("provider %q not found", key)
	}

	aliasKey := strings.ToLower(strings.TrimSpace(alias))
	if provider.Models == nil {
		return fmt.Errorf("model %q not found in provider %q", aliasKey, key)
	}
	if _, ok := provider.Models[aliasKey]; !ok {
		return fmt.Errorf("model %q not found in provider %q", aliasKey, key)
	}

	delete(provider.Models, aliasKey)
	m.cfg.Providers.Named[key] = provider

	return m.saveConfigToDisk()
}

// providerDeleteConfirmWindow is how long the "press d again" confirmation
// stays armed after the first press when deleting a model from the provider
// detail view. Deliberately a separate constant from commandsDeleteConfirmWindow:
// the two flows must be able to evolve independently.
const providerDeleteConfirmWindow = 5 * time.Second

// providerDetailLayout is the rendered provider detail view (ModalProviderDetail)
// plus the row→action mapping its key handlers need: ModelRows resolves a
// selected model row back to its alias, and the three indices are the action
// rows (edit provider / add model / delete provider), -1 when absent. The row
// labels are localized, so callers must compare INDEXES, never label strings.
type providerDetailLayout struct {
	Items     []string
	ModelRows map[int]string
	EditIdx   int
	AddIdx    int
	DelIdx    int
}

// providerDetailList builds the item list for the ModalProviderDetail view:
// the provider info rows when the provider is found ("Type: %s",
// "API Base: %s", "API Key: <mask or (not set)>", plus one row per optional
// field that is actually set — proxy, auth method, connect mode, web search
// and the custom header NAMES), a "---" separator, one row per model alias
// prefixed with exactly two spaces (or the "  (no models)" placeholder when the
// provider has no models), another "---", and the localized
// editProviderAction / addModelAction / deleteProviderAction action rows.
//
// The optional rows are conditional on purpose: a three-row block that always
// listed five extra empty fields would bury the model list, and this is a
// scrolling picker. Their labels are hardcoded English like "Type: %s" — this
// block predates the localized action rows and stays one style.
//
// The header row lists the header NAMES only, sorted and joined with ", ":
// header values can carry credentials (an Authorization override, a gateway
// token), so they must never reach a screen that is not masked — and the row
// would be one unreadable line anyway.
//
// ModelRows maps the item index of every model row to its (trimmed) model
// alias so callers can resolve a selected row back to a model without
// re-parsing the label; the placeholder, separators and action rows are not
// part of the map. The Edit/Add/Del indices are the item indices of the three
// action rows (-1 when absent — they are always appended, so in practice
// always valid), so callers compare indexes instead of fragile label strings.
// All of them are computed from the item count as it grows, which is what keeps
// them correct when the optional info rows appear or disappear.
func (m *Model) providerDetailList(providerName string) providerDetailLayout {
	layout := providerDetailLayout{
		ModelRows: make(map[int]string),
		EditIdx:   -1,
		AddIdx:    -1,
		DelIdx:    -1,
	}
	var items []string

	// Show provider info (skipped when the provider is not found).
	if cfg := m.configSource(); cfg != nil && cfg.Providers != nil {
		if p, ok := cfg.Providers.GetNamed(providerName); ok {
			items = append(items, fmt.Sprintf("Type: %s", p.Type))
			items = append(items, fmt.Sprintf("API Base: %s", p.APIBase))
			// Never print raw key material — even short keys.
			keyDisplay := maskAPIKey(p.APIKey)
			if keyDisplay == "" {
				keyDisplay = "(not set)"
			}
			items = append(items, fmt.Sprintf("API Key: %s", keyDisplay))
			if proxy := strings.TrimSpace(p.Proxy); proxy != "" {
				items = append(items, fmt.Sprintf("Proxy: %s", proxy))
			}
			if authMethod := strings.TrimSpace(p.AuthMethod); authMethod != "" {
				items = append(items, fmt.Sprintf("Auth method: %s", authMethod))
			}
			if connectMode := strings.TrimSpace(p.ConnectMode); connectMode != "" {
				items = append(items, fmt.Sprintf("Connect mode: %s", connectMode))
			}
			if p.WebSearch != nil {
				answer := "no"
				if *p.WebSearch {
					answer = "yes"
				}
				items = append(items, fmt.Sprintf("Web search: %s", answer))
			}
			if len(p.Headers) > 0 {
				items = append(items, fmt.Sprintf("Headers: %s", strings.Join(headerNames(p.Headers), ", ")))
			}
		}
	}
	items = append(items, "---")

	// List models.
	models := m.listProviderModels(providerName)
	for _, alias := range models {
		layout.ModelRows[len(items)] = strings.TrimSpace(alias)
		items = append(items, fmt.Sprintf("  %s", alias))
	}
	if len(models) == 0 {
		items = append(items, "  (no models)")
	}

	items = append(items, "---")
	layout.EditIdx = len(items)
	items = append(items, i18n.T("tui.editProviderAction"))
	layout.AddIdx = len(items)
	items = append(items, i18n.T("tui.addModelAction"))
	layout.DelIdx = len(items)
	items = append(items, i18n.T("tui.deleteProviderAction"))
	layout.Items = items
	return layout
}

// requestProviderModelDelete arms the double-press confirmation for deleting
// the model under the cursor of the provider detail view. The first "d" only
// shows a warning; a second "d" on the same row within
// providerDeleteConfirmWindow actually removes it. Mirrors requestCommandDelete:
// a stray keystroke must never destroy saved config.
func (m *Model) requestProviderModelDelete(providerName, alias string) tea.Cmd {
	if m.providerDeleteKey == alias && time.Since(m.providerDeleteArmed) < providerDeleteConfirmWindow {
		m.clearProviderDeleteConfirm()
		if err := m.deleteModelFromProvider(providerName, alias); err != nil {
			m.providerFeedback = err.Error()
			return m.tickCmd()
		}
		m.providerFeedback = fmt.Sprintf("%s: %s", i18n.T("tui.modelDeleted"), alias)
		// Rebuild the list so the removed row disappears. The cursor stays on
		// the row the deleted model occupied — after the rebuild that row is
		// the next model (or the last one), which is far less jarring than
		// jumping back to the top of the list. clampModalCursor keeps it
		// inside the shortened list.
		row := m.modalSelectedIdx
		m.enterProviderDetail(providerName, "")
		if row < len(m.modalItems) {
			m.modalSelectedIdx = row
		}
		m.clampModalCursor()
		return m.tickCmd()
	}
	m.providerDeleteKey = alias
	m.providerDeleteArmed = time.Now()
	m.providerFeedback = fmt.Sprintf(i18n.T("tui.modelDeleteConfirm"), alias)
	return m.tickCmd()
}

// clearProviderDeleteConfirm disarms a pending model delete. Called from every
// path that moves the cursor or changes the view so an armed delete can never
// follow its user to a different row.
func (m *Model) clearProviderDeleteConfirm() {
	m.providerDeleteKey = ""
	m.providerDeleteArmed = time.Time{}
}

// providerModelConfig returns the stored config for a model alias within a
// provider, reading from configSource() (the agent-loop snapshot when
// available, m.cfg otherwise). Both keys are lowercased/trimmed like the
// other helpers. The second return value reports whether the model was found.
func (m *Model) providerModelConfig(providerName, alias string) (config.ProviderModelConfig, bool) {
	var zero config.ProviderModelConfig
	cfg := m.configSource()
	if cfg == nil || cfg.Providers == nil || cfg.Providers.Named == nil {
		return zero, false
	}
	key := strings.ToLower(strings.TrimSpace(providerName))
	aliasKey := strings.ToLower(strings.TrimSpace(alias))
	provider, ok := cfg.Providers.Named[key]
	if !ok {
		return zero, false
	}
	mc, ok := provider.Models[aliasKey]
	if !ok {
		return zero, false
	}
	return mc, true
}

// updateModelInProvider edits an existing model alias in a provider's Models
// map and persists to disk. Unlike addModelToProvider it starts from the
// EXISTING entry, so fields the form does not ask about (Temperature, Video)
// survive the edit.
//
// Reasoning IS form-driven (the edit form's five reasoning steps, see
// reasoningFromFormValues), so it is always set explicitly: a nil value means
// "no reasoning override" and clears whatever the model carried, while a
// non-nil value is exactly what the user answered. The value is validated here
// as well as in the form helper (defense in depth for callers that skip the
// form), and validation happens before anything is written so a rejected
// config leaves the stored model untouched.
//
// contextWindow / maxTokens keep their current value when passed as 0; a
// value still zero afterwards is filled from catalog.DefaultsFor when the
// model is known there. Vision is always set explicitly (the form always
// answers yes/no). thinkingType is the think-system step answer (see
// config.ThinkingType*); "" and "auto" clear the field back to the default
// heuristics. The alias may be renamed: the old key is removed when the
// (normalized) new alias differs.
func (m *Model) updateModelInProvider(providerName, origAlias, newAlias, modelName string, contextWindow, maxTokens int, vision bool, thinkingType string, reasoning *config.ReasoningConfig) error {
	if m.cfg == nil || m.cfg.Providers == nil || m.cfg.Providers.Named == nil {
		return fmt.Errorf("no providers configured")
	}

	key := strings.ToLower(strings.TrimSpace(providerName))
	provider, ok := m.cfg.Providers.Named[key]
	if !ok {
		return fmt.Errorf("provider %q not found", key)
	}

	origKey := strings.ToLower(strings.TrimSpace(origAlias))
	existing, ok := provider.Models[origKey]
	if !ok {
		return fmt.Errorf("model %q not found in provider %q", origKey, key)
	}

	newKey := strings.ToLower(strings.TrimSpace(newAlias))
	if newKey == "" {
		return fmt.Errorf("model alias cannot be empty")
	}
	if newKey != origKey {
		if _, exists := provider.Models[newKey]; exists {
			return fmt.Errorf("model %q already exists", newKey)
		}
	}
	if strings.TrimSpace(modelName) == "" {
		return fmt.Errorf("model name cannot be empty")
	}

	// Start from the existing entry so Temperature/Reasoning (and any other
	// field not covered by the form) are preserved.
	updated := existing
	updated.Model = modelName
	if contextWindow > 0 {
		updated.ContextWindow = contextWindow
	}
	if maxTokens > 0 {
		updated.MaxTokens = maxTokens
	}
	updated.Vision = vision
	// Thinking type IS asked by the form (think-system step), so it is set
	// explicitly: "" / "auto" clear the field back to the default heuristics.
	normalizedThink, err := normalizeThinkSystemInput(thinkingType)
	if err != nil {
		return err
	}
	updated.ThinkingType = normalizedThink
	// Reasoning is form-driven too (the five reasoning steps). Validate before
	// touching the stored entry so a rejected answer cannot half-save the edit.
	if err := reasoning.Validate(); err != nil {
		return err
	}
	updated.Reasoning = reasoning

	// Catalog fallback for values still zero after the edit. Temperature, Video
	// and every other field the form does not ask about were copied over from
	// the existing entry above and keep their stored values.
	if (contextWindow == 0 && updated.ContextWindow == 0) || (maxTokens == 0 && updated.MaxTokens == 0) {
		if catKey := m.catalogProviderKey(key); catKey != "" {
			if d, ok := catalog.DefaultsFor(catKey, modelName); ok {
				if contextWindow == 0 && updated.ContextWindow == 0 && d.ContextWindow != 0 {
					updated.ContextWindow = d.ContextWindow
				}
				if maxTokens == 0 && updated.MaxTokens == 0 && d.MaxTokens != 0 {
					updated.MaxTokens = d.MaxTokens
				}
			}
		}
	}

	if newKey != origKey {
		delete(provider.Models, origKey)
	}
	provider.Models[newKey] = updated
	// Re-assign: Models edits above mutate the shared map, but the struct
	// copy is what the map holds (Go map value semantics).
	m.cfg.Providers.Named[key] = provider

	return m.saveConfigToDisk()
}

// modelCustomValue is the sentinel value of the "(custom…)" selector option.
// It cannot collide with a real provider/model reference (a NUL byte cannot be
// typed in the terminal text input) and signals "open free-text input".
const modelCustomValue = "\x00__custom__"

// configSource returns the config to read selector options from: the agent
// loop's live snapshot when available, otherwise m.cfg (unit tests run with
// a nil agentLoop, which must not panic).
func (m *Model) configSource() *config.Config {
	if m.agentLoop != nil {
		if snap := m.agentLoop.GetProvidable().GetConfigSnapshot(); snap != nil {
			return snap
		}
	}
	return m.cfg
}

// configSelectorOptions builds selector option lists from configured values:
// "(default)" (empty value) first, then the current value when it is not
// among the configured values (so a stale reference always stays selectable
// and marked with ✓), then the configured values (in the order provided),
// and finally a "(custom…)" entry (value modelCustomValue) that opens
// free-text input. Returns nil, nil when there is nothing to offer (no
// configured values and no current value) so callers can fall back to plain
// text input.
func configSelectorOptions(currentValue string, configured []string) (labels, values []string) {
	currentValue = strings.TrimSpace(currentValue)
	if len(configured) == 0 && currentValue == "" {
		return nil, nil
	}
	labels = make([]string, 0, len(configured)+3)
	values = make([]string, 0, len(configured)+3)
	labels = append(labels, "(default)")
	values = append(values, "")
	if currentValue != "" {
		found := false
		for _, c := range configured {
			if c == currentValue {
				found = true
				break
			}
		}
		if !found {
			labels = append(labels, currentValue)
			values = append(values, currentValue)
		}
	}
	for _, c := range configured {
		labels = append(labels, c)
		values = append(values, c)
	}
	labels = append(labels, i18n.T("tui.settings.selectorCustom"))
	values = append(values, modelCustomValue)
	return labels, values
}

// providerSelectorOptions returns selector options for a provider field: all
// configured provider names (sorted), plus the current value when it is not
// among them (e.g. a provider removed from the config), plus "(custom…)".
func (m *Model) providerSelectorOptions(currentValue string) (labels, values []string) {
	cfg := m.configSource()
	if cfg == nil || cfg.Providers == nil || cfg.Providers.Named == nil {
		return configSelectorOptions(currentValue, nil)
	}
	names := make([]string, 0, len(cfg.Providers.Named))
	for name := range cfg.Providers.Named {
		names = append(names, name)
	}
	sort.Strings(names)
	return configSelectorOptions(currentValue, names)
}

// modelSelectorOptions returns selector options for a model field: every
// configured provider's models as "provider:alias" (sorted by provider name,
// then alias), plus the current value when it is not among them, plus
// "(custom…)".
func (m *Model) modelSelectorOptions(currentValue string) (labels, values []string) {
	cfg := m.configSource()
	if cfg == nil || cfg.Providers == nil || cfg.Providers.Named == nil {
		return configSelectorOptions(currentValue, nil)
	}
	providers := make([]string, 0, len(cfg.Providers.Named))
	for name := range cfg.Providers.Named {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	refs := make([]string, 0, 8)
	for _, name := range providers {
		provider := cfg.Providers.Named[name]
		aliases := make([]string, 0, len(provider.Models))
		for alias := range provider.Models {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		for _, alias := range aliases {
			refs = append(refs, name+":"+alias)
		}
	}
	return configSelectorOptions(currentValue, refs)
}

// enterProviderDetail switches to the provider detail view for providerName,
// rebuilds the item list (and the item-index → model-alias map used to resolve
// ENTER on a model row) and positions the cursor on focusAlias when it maps to
// a row (falling back to the first row). Used by the /providers list, by the
// edit-model save/ESC paths so the user always lands back on the detail with
// the edited row highlighted, and by any future navigation into the detail.
func (m *Model) enterProviderDetail(providerName, focusAlias string) {
	layout := m.providerDetailList(providerName)
	m.modalMode = ModalProviderDetail
	m.providerSelectedName = providerName
	m.modalItems = layout.Items
	m.providerDetailRows = layout.ModelRows
	m.providerDetailEditIdx = layout.EditIdx
	m.providerDetailAddIdx = layout.AddIdx
	m.providerDetailDelIdx = layout.DelIdx
	m.modalSelectedIdx = 0
	for idx, alias := range m.providerDetailRows {
		if alias == focusAlias {
			m.modalSelectedIdx = idx
			break
		}
	}
	m.modalScrollOffset = 0
}

// openProviderEditFlow opens the ModalEditProvider form for the provider
// currently selected in the detail view (m.providerSelectedName). The form has
// one step per editable provider config field — type, API key, API base URL,
// proxy, auth method, connect mode, web search and headers (see
// editProviderStepCount) — and starts with every stored value pre-filled so
// Enter-through keeps them. The provider NAME is deliberately not editable: an
// agent's model reference ("provider:model") would have to be re-pointed, so the
// name is shown in the detail view and models keep their own flows.
//
// Two steps are deliberately left BLANK: the API key and the headers blob. The
// form is a plain text widget and a stored secret must never be rendered back
// into it (see secret_fields.go), so blank means "keep the stored value" — the
// same rule updateProvider applies. When the provider cannot be resolved the
// form is NOT opened: a message is staged on providerFeedback (rendered under
// the detail list) and the caller stays where it is. It reports whether the
// form opened.
func (m *Model) openProviderEditFlow() bool {
	provider := m.providerSelectedName
	cfg := m.configSource()
	if cfg == nil || cfg.Providers == nil || strings.TrimSpace(provider) == "" {
		m.providerFeedback = fmt.Sprintf("provider %q not found", provider)
		return false
	}
	named, ok := cfg.Providers.GetNamed(provider)
	if !ok {
		m.providerFeedback = fmt.Sprintf("provider %q not found", strings.ToLower(strings.TrimSpace(provider)))
		return false
	}

	m.resetModal(ModalEditProvider)
	// resetModal clears providerSelectedName (and nils providerDetailRows — the
	// list is rebuilt on the way back via enterProviderDetail); restore the
	// provider this edit flow operates on.
	m.providerSelectedName = provider
	m.formStepIndex = 0
	// One entry per step, in editProvider*Step order. The API key and the
	// headers blob stay empty on purpose: they may carry secrets, and a stored
	// secret is never rendered back into the form.
	m.formValues = []string{
		named.Type,
		"", // API key
		named.APIBase,
		named.Proxy,
		named.AuthMethod,
		named.ConnectMode,
		webSearchFormValue(named.WebSearch),
		"", // headers
	}
	m.formError = ""
	m.formConfirmMode = false
	// The edit form reuses none of the /connect bookkeeping: clear the flag a
	// previous connect run may have left behind so the hints stay honest.
	m.providerSavedInFlow = false

	m.textInput.SetValue(named.Type)
	m.textInput.Placeholder = "Provider type (e.g. openai, anthropic, openrouter)"
	m.syncTextInputEcho()
	return true
}

// openModelEditFlow opens the ModalEditModel form pre-filled with the stored
// values of the given model alias: the six fields of the add form plus the five
// reasoning overrides (see editModelStepCount), so Enter-through leaves the
// model exactly as it was. When the model cannot be found the form is NOT
// opened: an error is staged and the caller stays in the detail view.
func (m *Model) openModelEditFlow(alias string) {
	provider := m.providerSelectedName
	mc, ok := m.providerModelConfig(provider, alias)
	if !ok {
		m.formError = fmt.Sprintf("model %q not found in provider %q", alias, provider)
		return
	}

	m.resetModal(ModalEditModel)
	// resetModal clears providerSelectedName (and nils providerDetailRows —
	// the list is rebuilt on the way back via enterProviderDetail); restore
	// the provider the edit flow operates on.
	m.providerSelectedName = provider
	m.formStepIndex = 0
	m.formValues = make([]string, editModelStepCount)
	m.formError = ""
	m.formConfirmMode = false

	// Pre-fill: alias, model name, context window, max tokens, vision,
	// think system.
	m.formValues[addModelAliasStep] = alias
	m.formValues[addModelModelNameStep] = mc.Model
	if mc.ContextWindow > 0 {
		m.formValues[addModelContextWindowStep] = strconv.Itoa(mc.ContextWindow)
	}
	if mc.MaxTokens > 0 {
		m.formValues[addModelMaxTokensStep] = strconv.Itoa(mc.MaxTokens)
	}
	if mc.Vision {
		m.formValues[addModelVisionStep] = "yes"
	} else {
		m.formValues[addModelVisionStep] = "no"
	}
	if mc.ThinkingType != "" {
		m.formValues[addModelThinkSystemStep] = mc.ThinkingType
	} else {
		m.formValues[addModelThinkSystemStep] = config.ThinkingTypeAuto
	}

	// Pre-fill the reasoning override. A model without a ReasoningConfig shows
	// the neutral answers (disabled + every optional override unset), so
	// Enter-through round-trips nil back to nil.
	r := mc.Reasoning
	if r != nil && r.Enable {
		m.formValues[editModelReasoningEnableStep] = "yes"
	} else {
		m.formValues[editModelReasoningEnableStep] = "no"
	}
	if r != nil && r.Effort != nil && strings.TrimSpace(*r.Effort) != "" {
		m.formValues[editModelReasoningEffortStep] = strings.TrimSpace(*r.Effort)
	} else {
		m.formValues[editModelReasoningEffortStep] = reasoningFormDefault
	}
	if r != nil && r.MaxTokens != nil && *r.MaxTokens > 0 {
		m.formValues[editModelReasoningMaxTokensStep] = strconv.Itoa(*r.MaxTokens)
	}
	if r != nil && r.Exclude != nil {
		if *r.Exclude {
			m.formValues[editModelReasoningExcludeStep] = "yes"
		} else {
			m.formValues[editModelReasoningExcludeStep] = "no"
		}
	} else {
		m.formValues[editModelReasoningExcludeStep] = reasoningFormDefault
	}
	if r != nil && r.Summary != nil && strings.TrimSpace(*r.Summary) != "" {
		m.formValues[editModelReasoningSummaryStep] = strings.TrimSpace(*r.Summary)
	} else {
		m.formValues[editModelReasoningSummaryStep] = reasoningFormDefault
	}

	m.modelEditAlias = alias
	m.modelEditOrigModel = mc.Model

	m.textInput.SetValue(m.formValues[0])
	m.textInput.Placeholder = "Model alias (e.g. gpt-4o)"
	m.syncTextInputEcho()
}
