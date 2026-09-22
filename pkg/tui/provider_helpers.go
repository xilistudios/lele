package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/catalog"
	"github.com/xilistudios/lele/pkg/config"
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

// updateProvider updates an existing provider's API key and base URL.
func (m *Model) updateProvider(name, apiKey, apiBase string) error {
	if m.cfg == nil || m.cfg.Providers == nil || m.cfg.Providers.Named == nil {
		return fmt.Errorf("no providers configured")
	}

	key := strings.ToLower(strings.TrimSpace(name))
	provider, ok := m.cfg.Providers.Named[key]
	if !ok {
		return fmt.Errorf("provider %q not found", key)
	}

	provider.APIKey = apiKey
	provider.APIBase = apiBase
	m.cfg.Providers.Named[key] = provider

	return m.saveConfigToDisk()
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

// addModelToProvider adds a model alias to a provider's Models map.
// When the model is known to pkg/catalog and context_window / max_tokens were
// left empty (zero), the values are filled from catalog.DefaultsFor. Models
// that advertise thinking levels get a default Reasoning.Effort so reasoning
// works out of the box.
func (m *Model) addModelToProvider(providerName, alias, modelName string, contextWindow, maxTokens int, vision bool) error {
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

// providerDetailList builds the item list for the ModalProviderDetail view:
// three provider info rows when the provider is found ("Type: %s",
// "API Base: %s", "API Key: <mask or (not set)>"), a "---" separator, one row
// per model alias prefixed with exactly two spaces (or the "  (no models)"
// placeholder when the provider has no models), another "---", and the
// localized tui.addModelAction / tui.deleteProviderAction action rows. The
// content is byte-identical to the previously inlined blocks in
// handlers_modal.go, except the two action rows are now localized.
//
// modelRows maps the item index of every model row to its (trimmed) model
// alias so callers can resolve a selected row back to a model without
// re-parsing the label; the placeholder, separators and action rows are not
// part of the map. addIdx / delIdx are the item indices of the two action
// rows (-1 when absent — they are always appended, so in practice always
// valid), so callers compare indexes instead of fragile label strings.
func (m *Model) providerDetailList(providerName string) (items []string, modelRows map[int]string, addIdx, delIdx int) {
	modelRows = make(map[int]string)
	addIdx, delIdx = -1, -1

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
		}
	}
	items = append(items, "---")

	// List models.
	models := m.listProviderModels(providerName)
	for _, alias := range models {
		modelRows[len(items)] = strings.TrimSpace(alias)
		items = append(items, fmt.Sprintf("  %s", alias))
	}
	if len(models) == 0 {
		items = append(items, "  (no models)")
	}

	items = append(items, "---")
	addIdx = len(items)
	items = append(items, i18n.T("tui.addModelAction"))
	delIdx = len(items)
	items = append(items, i18n.T("tui.deleteProviderAction"))
	return items, modelRows, addIdx, delIdx
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
// EXISTING entry, so fields the form does not ask about (Temperature,
// Reasoning) survive the edit: Reasoning is deliberately NOT re-derived from
// the catalog here — an existing model may carry user-tuned reasoning.
// contextWindow / maxTokens keep their current value when passed as 0; a
// value still zero afterwards is filled from catalog.DefaultsFor when the
// model is known there. Vision is always set explicitly (the form always
// answers yes/no). The alias may be renamed: the old key is removed when the
// (normalized) new alias differs.
func (m *Model) updateModelInProvider(providerName, origAlias, newAlias, modelName string, contextWindow, maxTokens int, vision bool) error {
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

	// Catalog fallback for values still zero after the edit. Deliberately no
	// Reasoning touch — a user-tuned config must survive the edit.
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
	m.modalMode = ModalProviderDetail
	m.providerSelectedName = providerName
	m.modalItems, m.providerDetailRows, m.providerDetailAddIdx, m.providerDetailDelIdx = m.providerDetailList(providerName)
	m.modalSelectedIdx = 0
	for idx, alias := range m.providerDetailRows {
		if alias == focusAlias {
			m.modalSelectedIdx = idx
			break
		}
	}
	m.modalScrollOffset = 0
}

// openModelEditFlow opens the ModalEditModel form pre-filled with the stored
// values of the given model alias. When the model cannot be found the form is
// NOT opened: an error is staged and the caller stays in the detail view.
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
	m.formValues = make([]string, 5)
	m.formError = ""
	m.formConfirmMode = false

	// Pre-fill: alias, model name, context window, max tokens, vision.
	m.formValues[0] = alias
	m.formValues[1] = mc.Model
	if mc.ContextWindow > 0 {
		m.formValues[2] = strconv.Itoa(mc.ContextWindow)
	}
	if mc.MaxTokens > 0 {
		m.formValues[3] = strconv.Itoa(mc.MaxTokens)
	}
	if mc.Vision {
		m.formValues[4] = "yes"
	} else {
		m.formValues[4] = "no"
	}
	m.modelEditAlias = alias
	m.modelEditOrigModel = mc.Model

	m.textInput.SetValue(m.formValues[0])
	m.textInput.Placeholder = "Model alias (e.g. gpt-4o)"
	m.syncTextInputEcho()
}
