package i18n

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/text/language"
)

// externalMu guards external pack registration and the pack directory.
var (
	externalMu     sync.RWMutex
	packDir        string
	externalLangs  = map[string]localeMap{}
	externalLoaded bool
)

// BuiltinLanguages are the locales compiled into the binary.
func BuiltinLanguages() []string {
	return []string{"es", "en", "pt"}
}

// SetPackDir sets the directory that holds downloaded language packs
// (<dir>/tui/<code>.json). Call before Init/InitWithLanguage so the
// configured language can load a downloaded pack. Empty clears it.
func SetPackDir(dir string) {
	externalMu.Lock()
	defer externalMu.Unlock()
	packDir = dir
	externalLoaded = false
	externalLangs = map[string]localeMap{}
}

// PackDir returns the configured pack directory (may be empty).
func PackDir() string {
	externalMu.RLock()
	defer externalMu.RUnlock()
	return packDir
}

// DefaultPackDir returns ~/.lele/locales (honors LELE_CONFIG_DIR).
// Kept free of pkg/config imports to avoid a dependency cycle.
func DefaultPackDir() string {
	if envDir := os.Getenv("LELE_CONFIG_DIR"); envDir != "" {
		if strings.HasPrefix(envDir, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				return filepath.Join(home, strings.TrimPrefix(envDir, "~"))
			}
		}
		return filepath.Join(envDir, "locales")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".lele", "locales")
	}
	return filepath.Join(home, ".lele", "locales")
}

// LoadExternalPacks scans packDir/tui/*.json and registers each language.
// Missing directory is not an error. Returns the codes that loaded.
func LoadExternalPacks() []string {
	externalMu.Lock()
	dir := packDir
	externalMu.Unlock()
	if dir == "" {
		dir = DefaultPackDir()
		externalMu.Lock()
		packDir = dir
		externalMu.Unlock()
	}

	tuiDir := filepath.Join(dir, "tui")
	entries, err := os.ReadDir(tuiDir)
	if err != nil {
		externalMu.Lock()
		externalLoaded = true
		externalMu.Unlock()
		return nil
	}

	var loaded []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		code := strings.ToLower(strings.TrimSuffix(e.Name(), ".json"))
		if isBuiltinCode(code) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(tuiDir, e.Name()))
		if err != nil {
			continue
		}
		var m localeMap
		if err := json.Unmarshal(data, &m); err != nil || len(m) == 0 {
			continue
		}
		externalMu.Lock()
		externalLangs[code] = m
		externalMu.Unlock()
		loaded = append(loaded, code)
	}

	sort.Strings(loaded)
	externalMu.Lock()
	externalLoaded = true
	externalMu.Unlock()
	return loaded
}

// RegisterExternal installs an in-memory translation map for code (tests
// and post-download activation without a re-scan).
func RegisterExternal(code string, translations map[string]string) {
	code = normalizeLangCode(code)
	if code == "" || len(translations) == 0 {
		return
	}
	externalMu.Lock()
	externalLangs[code] = translations
	externalMu.Unlock()

	if system != nil {
		tag, err := language.Parse(code)
		if err != nil {
			return
		}
		system.translations[tag] = translations
	}
}

// EnsureLoadedPacks loads external packs once if not already done.
func ensureExternalLoaded() {
	externalMu.RLock()
	done := externalLoaded
	externalMu.RUnlock()
	if !done {
		LoadExternalPacks()
	}
}

func isBuiltinCode(code string) bool {
	for _, b := range BuiltinLanguages() {
		if b == code {
			return true
		}
	}
	return false
}

func normalizeLangCode(lang string) string {
	return strings.ToLower(strings.TrimSpace(lang))
}

// InstalledLanguages returns builtin + downloaded language codes.
func InstalledLanguages() []string {
	ensureExternalLoaded()
	externalMu.RLock()
	defer externalMu.RUnlock()

	out := append([]string{}, BuiltinLanguages()...)
	for code := range externalLangs {
		out = append(out, code)
	}
	sort.Strings(out)
	// Keep builtins first in a stable preferred order, extras alphabetical.
	builtins := BuiltinLanguages()
	extras := make([]string, 0, len(out))
	for _, c := range out {
		if !isBuiltinCode(c) {
			extras = append(extras, c)
		}
	}
	sort.Strings(extras)
	return append(append([]string{}, builtins...), extras...)
}

// AvailableLanguages returns builtin + installed language codes.
// Prefer this over the historical hard-coded three-language list.
func AvailableLanguages() []string {
	return InstalledLanguages()
}

// LanguageDisplayNames maps codes to native display names for pickers.
// Non-Latin names use unicode escapes to satisfy gosmopolitan.
var LanguageDisplayNames = map[string]string{
	"es": "Español",
	"en": "English",
	"pt": "Português",
	"fr": "Français",
	"de": "Deutsch",
	"it": "Italiano",
	"ja": "\u65e5\u672c\u8a9e",
	"ko": "\ud55c\uad6d\uc5b4",
	"zh": "\u4e2d\u6587",
	"ru": "\u0420\u0443\u0441\u0441\u043a\u0438\u0439",
	"vi": "Ti\u1ebfng Vi\u1ec7t",
	"pl": "Polski",
	"nl": "Nederlands",
	"tr": "T\u00fcrk\u00e7e",
	"ar": "\u0627\u0644\u0639\u0631\u0628\u064a\u0629",
	"hi": "\u0939\u093f\u0928\u094d\u0926\u0940",
	"th": "\u0e44\u0e17\u0e22",
	"id": "Bahasa Indonesia",
	"sv": "Svenska",
	"uk": "\u0423\u043a\u0440\u0430\u0457\u043d\u0441\u044c\u043a\u0430",
}

// DisplayName returns the native name for a language code.
func DisplayName(code string) string {
	code = normalizeLangCode(code)
	if n, ok := LanguageDisplayNames[code]; ok {
		return n
	}
	return strings.ToUpper(code)
}

// FormatLanguageOption renders "NativeName (code)" with a status suffix.
func FormatLanguageOption(code, statusSuffix string) string {
	base := fmt.Sprintf("%s (%s)", DisplayName(code), code)
	if statusSuffix == "" {
		return base
	}
	return base + " " + statusSuffix
}
