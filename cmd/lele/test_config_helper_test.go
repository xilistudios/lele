package main

import (
	"github.com/xilistudios/lele/pkg/config"
)

// defaultTestConfig returns a minimal default config suitable for CLI command
// tests, with a valid default agent model and temperature set so functions
// that dereference the temperature pointer do not panic.
func defaultTestConfig() (*config.Config, error) {
	cfg := config.DefaultConfig()
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = make(map[string]config.NamedProviderConfig)
	}
	temp := 0.7
	cfg.Agents.Defaults.Temperature = &temp
	if cfg.Agents.Defaults.Model == "" {
		cfg.Agents.Defaults.Model = "test:model"
	}
	return cfg, nil
}// saveTestConfig writes a config to a given path using the config package.
func saveTestConfig(path string, cfg *config.Config) error {
	return config.SaveConfig(path, cfg)
}