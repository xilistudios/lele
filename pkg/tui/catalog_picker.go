package tui

import (
	"strconv"
	"strings"

	"github.com/xilistudios/lele/pkg/catalog"
)

// maxCatalogSuggestions caps the filter-as-you-type list so the form modal
// stays readable on short terminals.
const maxCatalogSuggestions = 12

// catalogProviderKey resolves a configured provider name to the catalog key
// (provider type). Looks up Providers.Named[name].Type first so custom names
// like "my-openai" still hit the openai catalog; falls back to the name
// itself because catalog.normalizeType already understands aliases.
func (m *Model) catalogProviderKey(providerName string) string {
	name := strings.ToLower(strings.TrimSpace(providerName))
	if name == "" {
		return ""
	}
	if m.cfg != nil && m.cfg.Providers != nil && m.cfg.Providers.Named != nil {
		if p, ok := m.cfg.Providers.Named[name]; ok && strings.TrimSpace(p.Type) != "" {
			return strings.TrimSpace(p.Type)
		}
	}
	return name
}

// currentCatalogProviderKey returns the catalog provider key for the active
// add-model / connect flow. Prefers the selected provider's configured type;
// during the connect flow the type lives in formValues[1] once the provider
// step has been completed.
func (m *Model) currentCatalogProviderKey() string {
	if m.providerSelectedName != "" {
		if key := m.catalogProviderKey(m.providerSelectedName); key != "" {
			if len(catalog.ModelsForProvider(key)) > 0 {
				return key
			}
		}
	}
	if len(m.formValues) > 1 && strings.TrimSpace(m.formValues[1]) != "" {
		return strings.TrimSpace(m.formValues[1])
	}
	if m.providerSelectedName != "" {
		return m.providerSelectedName
	}
	return ""
}

// isModelNameFormStep reports whether the form cursor is on the "actual model
// name" field — the step that offers catalog autocomplete.
func (m *Model) isModelNameFormStep() bool {
	switch m.modalMode {
	case ModalAddModel:
		return m.formStepIndex == 1
	case ModalAddProvider:
		return m.providerSavedInFlow && m.formStepIndex == 5
	}
	return false
}

// startCatalogPickerIfNeeded activates (or tears down) the catalog suggestion
// list based on the current form step. Call after every formStepIndex
// transition so the picker never leaks onto the wrong field.
func (m *Model) startCatalogPickerIfNeeded() {
	if !m.isModelNameFormStep() {
		m.closeCatalogPicker()
		return
	}
	m.refreshCatalogSuggestions(m.textInput.Value())
}

// refreshCatalogSuggestions re-filters catalog models for the given query and
// rebuilds the parallel ID/label slices used by the picker.
func (m *Model) refreshCatalogSuggestions(query string) {
	key := m.currentCatalogProviderKey()
	// Unknown provider type (no catalog models at all) → no picker.
	if key == "" || len(catalog.ModelsForProvider(key)) == 0 {
		m.closeCatalogPicker()
		return
	}
	models := catalog.SearchModels(key, query)
	ids, labels := buildCatalogSuggestions(models)
	m.addModelCatalogActive = true
	m.addModelCatalogIDs = ids
	m.addModelCatalogLabels = labels
	if m.addModelCatalogIdx >= len(ids) {
		m.addModelCatalogIdx = 0
	}
}

// buildCatalogSuggestions maps catalog models to parallel ID and display-label
// slices, capped at maxCatalogSuggestions. Pure helper — unit-testable without
// a TUI Model.
func buildCatalogSuggestions(models []catalog.Model) (ids, labels []string) {
	if len(models) == 0 {
		return nil, nil
	}
	if len(models) > maxCatalogSuggestions {
		models = models[:maxCatalogSuggestions]
	}
	ids = make([]string, len(models))
	labels = make([]string, len(models))
	for i, cm := range models {
		ids[i] = cm.ID
		label := cm.ID
		if cm.Name != "" && !strings.EqualFold(cm.Name, cm.ID) {
			label += " · " + cm.Name
		}
		if len(cm.ThinkingLevels) > 0 {
			label += " · think " + strings.Join(cm.ThinkingLevels, "/")
		} else if cm.Reasoning {
			label += " · think"
		}
		labels[i] = label
	}
	return ids, labels
}

// closeCatalogPicker deactivates the suggestion list and clears its state.
func (m *Model) closeCatalogPicker() {
	m.addModelCatalogActive = false
	m.addModelCatalogIDs = nil
	m.addModelCatalogLabels = nil
	m.addModelCatalogIdx = 0
	m.addModelCatalogThink = ""
}

// applyCatalogSelectionOnEnter runs before the ModalAddModel / ModalAddProvider
// enter handlers. When the typed value is empty and a suggestion is
// highlighted, the suggestion is copied into the text input. When the typed
// value matches a catalog model, the ID is canonicalized and context_window /
// max_tokens / vision are prefilled into formValues so the remaining steps can
// be accepted with Enter.
func (m *Model) applyCatalogSelectionOnEnter() {
	if !m.isModelNameFormStep() {
		return
	}
	typed := strings.TrimSpace(m.textInput.Value())
	if typed == "" && m.addModelCatalogIdx < len(m.addModelCatalogIDs) {
		typed = m.addModelCatalogIDs[m.addModelCatalogIdx]
		m.textInput.SetValue(typed)
	}
	if typed == "" {
		return
	}
	key := m.currentCatalogProviderKey()
	if key == "" {
		return
	}
	d, ok := catalog.DefaultsFor(key, typed)
	if !ok {
		return
	}
	if cm, found := catalog.FindModel(key, typed); found && cm.ID != "" {
		typed = cm.ID
		m.textInput.SetValue(typed)
	}
	m.prefillModelFormFromDefaults(typed, d)
}

// prefillModelFormFromDefaults writes catalog defaults into the form value
// slots for the active flow. ModalAddModel uses indices 1-4 (model_name,
// context_window, max_tokens, vision); the connect flow uses 5-8.
func (m *Model) prefillModelFormFromDefaults(modelID string, d catalog.ModelDefaults) {
	vision := "no"
	if d.Vision {
		vision = "yes"
	}
	switch m.modalMode {
	case ModalAddModel:
		if len(m.formValues) >= 5 {
			m.formValues[1] = modelID
			m.formValues[2] = strconv.Itoa(d.ContextWindow)
			m.formValues[3] = strconv.Itoa(d.MaxTokens)
			m.formValues[4] = vision
		}
	case ModalAddProvider:
		if len(m.formValues) >= 9 {
			m.formValues[5] = modelID
			m.formValues[6] = strconv.Itoa(d.ContextWindow)
			m.formValues[7] = strconv.Itoa(d.MaxTokens)
			m.formValues[8] = vision
		}
	}
	m.addModelCatalogThink = catalogThinkingHint(d.ThinkingLevels)
}

// defaultThinkingEffort picks a Reasoning.Effort from catalog thinking levels.
// Prefers "medium" when advertised, otherwise the first low/medium/high entry,
// falling back to "medium".
func defaultThinkingEffort(levels []string) string {
	for _, l := range levels {
		if strings.EqualFold(strings.TrimSpace(l), "medium") {
			return "medium"
		}
	}
	for _, l := range levels {
		switch strings.ToLower(strings.TrimSpace(l)) {
		case "low", "high":
			return strings.ToLower(strings.TrimSpace(l))
		}
	}
	return "medium"
}

// catalogThinkingHint returns a short "thinking: low/medium/high" fragment for
// form labels, or "" when the model has no advertised levels.
func catalogThinkingHint(levels []string) string {
	if len(levels) == 0 {
		return ""
	}
	return strings.Join(levels, "/")
}
