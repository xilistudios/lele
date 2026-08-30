package tui

import (
	"fmt"
	"sort"
	"strings"

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

// listProviderModels returns a sorted list of model aliases for a given provider.
func (m *Model) listProviderModels(providerName string) []string {
	if m.agentLoop == nil {
		return nil
	}
	snapshot := m.agentLoop.GetProvidable().GetConfigSnapshot()
	if snapshot == nil || snapshot.Providers == nil {
		return nil
	}
	provider, ok := snapshot.Providers.GetNamed(providerName)
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

	provider.Models[aliasKey] = config.ProviderModelConfig{
		Model:         modelName,
		ContextWindow: contextWindow,
		MaxTokens:     maxTokens,
		Vision:        vision,
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
