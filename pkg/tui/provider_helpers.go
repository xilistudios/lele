package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
)

// saveConfigToDisk persists the current in-memory config to disk.
func (m *Model) saveConfigToDisk() error {
	return config.SaveConfig(config.DefaultConfigPath(), m.cfg)
}

// listProviders returns a sorted list of provider names from the config.
func (m *Model) listProviders() []string {
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
