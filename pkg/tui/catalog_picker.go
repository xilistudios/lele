package tui

import (
	"strconv"
	"strings"

	"github.com/xilistudios/lele/pkg/catalog"
)

const maxCatalogSuggestions = 12

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

func (m *Model) isModelNameFormStep() bool {
	switch m.modalMode {
	case ModalAddModel, ModalEditModel:
		return m.formStepIndex == 1
	case ModalAddProvider:
		return m.providerSavedInFlow && m.formStepIndex == 5
	}
	return false
}

func (m *Model) startCatalogPickerIfNeeded() {
	if !m.isModelNameFormStep() {
		m.closeCatalogPicker()
		return
	}
	m.refreshCatalogSuggestions(m.textInput.Value())
}

func (m *Model) refreshCatalogSuggestions(query string) {
	key := m.currentCatalogProviderKey()
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

func (m *Model) closeCatalogPicker() {
	m.addModelCatalogActive = false
	m.addModelCatalogIDs = nil
	m.addModelCatalogLabels = nil
	m.addModelCatalogIdx = 0
	m.addModelCatalogThink = ""
}

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

func (m *Model) prefillModelFormFromDefaults(modelID string, d catalog.ModelDefaults) {
	vision := "no"
	if d.Vision {
		vision = "yes"
	}
	switch m.modalMode {
	case ModalAddModel, ModalEditModel:
		if len(m.formValues) >= 5 {
			m.formValues[1] = modelID
			sameStoredModel := m.modalMode == ModalEditModel &&
				m.modelEditOrigModel != "" && modelID == m.modelEditOrigModel
			if !sameStoredModel {
				m.formValues[2] = strconv.Itoa(d.ContextWindow)
				m.formValues[3] = strconv.Itoa(d.MaxTokens)
				m.formValues[4] = vision
			}
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

func catalogThinkingHint(levels []string) string {
	if len(levels) == 0 {
		return ""
	}
	return strings.Join(levels, "/")
}
