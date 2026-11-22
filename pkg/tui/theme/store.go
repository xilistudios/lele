package theme

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/xilistudios/lele/pkg/config"
)

type tuiConfigFile struct {
	Theme              string           `json:"theme"`
	CustomThemes       map[string]Theme `json:"custom_themes,omitempty"`
	InstalledCommunity []string         `json:"installed_community,omitempty"`
}

func DefaultPath() string {
	return filepath.Join(config.GetLeleDir(), "tui.json")
}

func Load(path string) (name string, custom map[string]Theme, installed []string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "dracula", nil, nil, nil
		}
		return "dracula", nil, nil, err
	}

	var cfg tuiConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "dracula", nil, nil, err
	}

	if cfg.Theme == "" {
		cfg.Theme = "dracula"
	}

	if len(cfg.CustomThemes) == 0 {
		cfg.CustomThemes = nil
	}

	if len(cfg.InstalledCommunity) == 0 {
		cfg.InstalledCommunity = nil
	}

	return cfg.Theme, cfg.CustomThemes, cfg.InstalledCommunity, nil
}

func Save(path, name string, custom map[string]Theme, installed []string) error {
	cfg := tuiConfigFile{
		Theme:              name,
		CustomThemes:       custom,
		InstalledCommunity: installed,
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

func IsInstalledCommunity(name string, installed []string) bool {
	for _, n := range installed {
		if n == name {
			return true
		}
	}
	return false
}

func AddInstalledCommunity(name string, installed []string) []string {
	if IsInstalledCommunity(name, installed) {
		return installed
	}
	out := make([]string, 0, len(installed)+1)
	out = append(out, installed...)
	return append(out, name)
}

func RemoveInstalledCommunity(name string, installed []string) []string {
	out := make([]string, 0, len(installed))
	for _, n := range installed {
		if n != name {
			out = append(out, n)
		}
	}
	return out
}
