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

func (m *Model) saveConfigToDisk() error {
	if err := config.SaveConfig(config.DefaultConfigPath(), m.cfg); err != nil {
		return err
	}
	if m.agentLoop != nil {
		m.agentLoop.UpdateConfigSnapshot(m.cfg.Snapshot())
	}
	return nil
}

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

type providerEditValues struct {
	Type        string
	APIKey      string
	APIBase     string
	Proxy       string
	AuthMethod  string
	ConnectMode string
	WebSearch   string
	Headers     string
}

const (
	editProviderTypeStep = iota
	editProviderAPIKeyStep
	editProviderAPIBaseStep
	editProviderProxyStep
	editProviderAuthMethodStep
	editProviderConnectModeStep
	editProviderWebSearchStep
	editProviderHeadersStep
	editProviderStepCount
)

const (
	addModelAliasStep = iota
	addModelModelNameStep
	addModelContextWindowStep
	addModelMaxTokensStep
	addModelVisionStep
	addModelThinkSystemStep
	addModelStepCount
)

const (
	editModelReasoningEnableStep = addModelStepCount + iota
	editModelReasoningEffortStep
	editModelReasoningMaxTokensStep
	editModelReasoningExcludeStep
	editModelReasoningSummaryStep
	editModelStepCount
)

func modelFormLastStep(mode modalType) int {
	if mode == ModalEditModel {
		return editModelStepCount - 1
	}
	return addModelStepCount - 1
}

func modelFormAllowsEmptyValue(mode modalType, step int) bool {
	switch step {
	case addModelContextWindowStep, addModelMaxTokensStep, addModelThinkSystemStep:
		return true
	}
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

	storedHeaders, headersChanged, err := parseHeaderEditValue(previous.Headers, v.Headers)
	if err != nil {
		return err
	}
	updated.Headers = storedHeaders
	changes.Headers = headersChanged

	typedSlot := typedProviderSlot(m.cfg.Providers, key)
	var typedBefore config.ProviderConfig
	if typedSlot != nil {
		typedBefore = *typedSlot
	}
	typedWebSearchBefore := m.cfg.Providers.OpenAI.WebSearch

	m.cfg.Providers.Named[key] = updated
	syncEditedProviderCredentials(m.cfg.Providers, key, updated, changes)
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

func resolveOptionalEditField(previous, input string) (value string, changed bool) {
	trimmed := strings.TrimSpace(input)
	switch trimmed {
	case "":
		return previous, false
	case "-":
		return "", previous != ""
	default:
		return trimmed, trimmed != previous
	}
}

func parseWebSearchToken(input string) (string, error) {
	switch token := strings.ToLower(strings.TrimSpace(input)); token {
	case "", "yes", "no", "default":
		return token, nil
	default:
		return "", fmt.Errorf("invalid web search value %q: use yes, no or default", strings.TrimSpace(input))
	}
}

func resolveWebSearchEdit(previous *bool, token string) (value *bool, changed bool) {
	switch token {
	case "":
		return previous, false
	case "default":
		return nil, previous != nil
	case "yes":
		on := true
		return &on, previous == nil || !*previous
	default:
		off := false
		return &off, previous == nil || *previous
	}
}

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

func headerNames(headers map[string]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

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

	alreadyUsable = len(existing.Models) > 0
	providerConfig := existing.ProviderConfig
	if k := strings.TrimSpace(apiKey); k != "" {
		providerConfig.APIKey = k
	}
	if b := strings.TrimSpace(apiBase); b != "" {
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

const providerDeleteConfirmWindow = 5 * time.Second

type providerDetailLayout struct {
	Items     []string
	ModelRows map[int]string
	EditIdx   int
	AddIdx    int
	DelIdx    int
}

func (m *Model) providerDetailList(providerName string) providerDetailLayout {
	layout := providerDetailLayout{
		ModelRows: make(map[int]string),
		EditIdx:   -1,
		AddIdx:    -1,
		DelIdx:    -1,
	}
	var items []string

	if cfg := m.configSource(); cfg != nil && cfg.Providers != nil {
		if p, ok := cfg.Providers.GetNamed(providerName); ok {
			items = append(items, fmt.Sprintf("Type: %s", p.Type))
			items = append(items, fmt.Sprintf("API Base: %s", p.APIBase))
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

func (m *Model) requestProviderModelDelete(providerName, alias string) tea.Cmd {
	if m.providerDeleteKey == alias && time.Since(m.providerDeleteArmed) < providerDeleteConfirmWindow {
		m.clearProviderDeleteConfirm()
		if err := m.deleteModelFromProvider(providerName, alias); err != nil {
			m.providerFeedback = err.Error()
			return m.tickCmd()
		}
		m.providerFeedback = fmt.Sprintf("%s: %s", i18n.T("tui.modelDeleted"), alias)
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

func (m *Model) clearProviderDeleteConfirm() {
	m.providerDeleteKey = ""
	m.providerDeleteArmed = time.Time{}
}

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

	updated := existing
	updated.Model = modelName
	if contextWindow > 0 {
		updated.ContextWindow = contextWindow
	}
	if maxTokens > 0 {
		updated.MaxTokens = maxTokens
	}
	updated.Vision = vision
	normalizedThink, err := normalizeThinkSystemInput(thinkingType)
	if err != nil {
		return err
	}
	updated.ThinkingType = normalizedThink
	if err := reasoning.Validate(); err != nil {
		return err
	}
	updated.Reasoning = reasoning

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
	m.cfg.Providers.Named[key] = provider

	return m.saveConfigToDisk()
}

const modelCustomValue = "\x00__custom__"

func (m *Model) configSource() *config.Config {
	if m.agentLoop != nil {
		if snap := m.agentLoop.GetProvidable().GetConfigSnapshot(); snap != nil {
			return snap
		}
	}
	return m.cfg
}

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
	m.providerSelectedName = provider
	m.formStepIndex = 0
	m.formValues = []string{
		named.Type,
		"",
		named.APIBase,
		named.Proxy,
		named.AuthMethod,
		named.ConnectMode,
		webSearchFormValue(named.WebSearch),
		"",
	}
	m.formError = ""
	m.formConfirmMode = false
	m.providerSavedInFlow = false

	m.textInput.SetValue(named.Type)
	m.textInput.Placeholder = "Provider type (e.g. openai, anthropic, openrouter)"
	m.syncTextInputEcho()
	return true
}

func (m *Model) openModelEditFlow(alias string) {
	provider := m.providerSelectedName
	mc, ok := m.providerModelConfig(provider, alias)
	if !ok {
		m.formError = fmt.Sprintf("model %q not found in provider %q", alias, provider)
		return
	}

	m.resetModal(ModalEditModel)
	m.providerSelectedName = provider
	m.formStepIndex = 0
	m.formValues = make([]string, editModelStepCount)
	m.formError = ""
	m.formConfirmMode = false

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
