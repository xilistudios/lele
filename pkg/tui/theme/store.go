package theme

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/xilistudios/lele/pkg/config"
)

// tuiConfigFile is the on-disk representation of the TUI theme config.
type tuiConfigFile struct {
	Theme        string           `json:"theme"`
	CustomThemes map[string]Theme `json:"custom_themes,omitempty"`
}

// DefaultPath returns the default path for the TUI config file.
func DefaultPath() string {
	return filepath.Join(config.GetLeleDir(), "tui.json")
}

// Load reads the TUI config file at path. If the file does not exist it
// returns "dracula", nil, nil. If the file is malformed it returns
// "dracula", nil, err so the caller can log it without crashing. On
// success it returns the theme name (defaulting to "dracula" if empty)
// and the custom themes map (nil if empty).
func Load(path string) (name string, custom map[string]Theme, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "dracula", nil, nil
		}
		return "dracula", nil, nil
	}

	var cfg tuiConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "dracula", nil, err
	}

	if cfg.Theme == "" {
		cfg.Theme = "dracula"
	}

	if len(cfg.CustomThemes) == 0 {
		return cfg.Theme, nil, nil
	}

	return cfg.Theme, cfg.CustomThemes, nil
}

// Save writes the theme name and custom themes to path as pretty-printed
// JSON. The write is atomic: it writes to a temp file (path+".tmp") and
// renames it into place. Parent directories are created if needed.
func Save(path, name string, custom map[string]Theme) error {
	cfg := tuiConfigFile{
		Theme:        name,
		CustomThemes: custom,
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
