// Package i18n provides internationalization support for the Lele TUI.
//
// This package implements a simple but effective i18n system that:
// - Loads translations from embedded JSON files (no external dependencies at runtime)
// - Supports multiple languages (Spanish, English, Portuguese)
// - Falls back to Spanish (default) when a translation is missing
// - Uses golang.org/x/text/language for proper language handling
//
// Language Precedence (highest to lowest):
//  1. Config.Language (from config file)
//  2. LELE_LANG environment variable
//  3. LANG environment variable
//  4. LC_ALL environment variable
//  5. Default: "es" (Spanish)
//
// Usage:
//
//	// Initialize with default language (Spanish)
//	i18n.Init()
//
//	// Or set a specific language
//	i18n.SetLanguage("en")
//
//	// Get translations
//	title := i18n.T("tui.title") // Returns "Lele 🦞"
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"golang.org/x/text/language"
)

//go:embed locales/*.json
var localeFS embed.FS

// localeFiles maps language tags to their embedded JSON file names.
var localeFiles = map[language.Tag]string{
	language.Spanish:    "locales/es.json",
	language.English:    "locales/en.json",
	language.Portuguese: "locales/pt.json",
}

// Localizer defines the interface for localization.
// Implementations must provide a T method that translates a key to the current language.
type Localizer interface {
	// T returns the translation for the given key.
	// If the key is not found, it returns the key itself.
	T(key string) string
}

// localeMap stores translations for a single language.
type localeMap map[string]string

// i18nSystem implements the Localizer interface.
type i18nSystem struct {
	currentLang  language.Tag
	translations map[language.Tag]localeMap
	fallback     language.Tag
}

// Global instance
var system *i18nSystem

// loadLocale reads a JSON file from the embedded filesystem and returns a localeMap.
func loadLocale(filename string) (localeMap, error) {
	data, err := localeFS.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded locale %s: %w", filename, err)
	}

	var m localeMap
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse locale %s: %w", filename, err)
	}

	return m, nil
}

// Init initializes the i18n system with the default language (Spanish).
// It loads all available translations from embedded JSON files and detects the language from environment.
func Init() {
	system = &i18nSystem{
		translations: make(map[language.Tag]localeMap),
		fallback:     language.Spanish,
	}

	// Load all locales from embedded JSON files
	for tag, filename := range localeFiles {
		translations, err := loadLocale(filename)
		if err != nil {
			// Log error but continue - fallback will handle missing translations
			fmt.Fprintf(os.Stderr, "i18n: warning: %v\n", err)
			continue
		}
		system.translations[tag] = translations
	}

	// Detect language from environment or config
	lang := detectLanguage()
	system.setLanguage(lang)
}

// InitWithLanguage initializes the i18n system with a specific language.
func InitWithLanguage(lang string) {
	Init()
	SetLanguage(lang)
}

// T returns the translation for the given key in the current language.
// If the key is not found, it returns the key itself.
func T(key string) string {
	if system == nil {
		Init()
	}
	return system.T(key)
}

// SetLanguage changes the current language.
// Supported values: "es", "en", "pt"
func SetLanguage(lang string) {
	if system == nil {
		Init()
	}
	system.setLanguage(lang)
}

// GetLanguage returns the current language code (e.g., "es", "en", "pt").
func GetLanguage() string {
	if system == nil {
		Init()
	}
	return system.currentLang.String()
}

// GetLanguageTag returns the current language tag.
func GetLanguageTag() language.Tag {
	if system == nil {
		Init()
	}
	return system.currentLang
}

// AvailableLanguages returns a list of supported language codes.
func AvailableLanguages() []string {
	return []string{"es", "en", "pt"}
}

// T implements the Localizer interface.
func (s *i18nSystem) T(key string) string {
	// Try current language
	if translations, ok := s.translations[s.currentLang]; ok {
		if val, exists := translations[key]; exists {
			return val
		}
	}

	// Fallback to default language
	if translations, ok := s.translations[s.fallback]; ok {
		if val, exists := translations[key]; exists {
			return val
		}
	}

	// Key not found, return the key itself
	return key
}

// setLanguage sets the current language from a string code.
func (s *i18nSystem) setLanguage(lang string) {
	// Normalize language code
	lang = strings.ToLower(strings.TrimSpace(lang))

	// Parse language tag
	tag, err := language.Parse(lang)
	if err != nil {
		// If parsing fails, try common mappings
		switch lang {
		case "es", "español", "spanish":
			tag = language.Spanish
		case "en", "english":
			tag = language.English
		case "pt", "português", "portuguese":
			tag = language.Portuguese
		default:
			tag = s.fallback
		}
	}

	// Check if we have translations for this language
	if _, ok := s.translations[tag]; ok {
		s.currentLang = tag
	} else {
		// Try to find a matching base language
		for registeredTag := range s.translations {
			regBase, _ := registeredTag.Base()
			tagBase, _ := tag.Base()
			if regBase == tagBase {
				s.currentLang = registeredTag
				return
			}
		}
		// Use fallback
		s.currentLang = s.fallback
	}
}

// detectLanguage detects the language from environment variables.
// Checks LELE_LANG, LANG, and LC_ALL in that order.
func detectLanguage() string {
	// Check LELE_LANG first (specific to our app)
	if lang := os.Getenv("LELE_LANG"); lang != "" {
		return lang
	}

	// Check LANG environment variable
	if lang := os.Getenv("LANG"); lang != "" {
		// LANG is usually in format like "en_US.UTF-8"
		// Extract just the language part
		parts := strings.Split(lang, ".")
		if len(parts) > 0 {
			langCode := strings.Split(parts[0], "_")[0]
			return langCode
		}
		return lang
	}

	// Check LC_ALL
	if lang := os.Getenv("LC_ALL"); lang != "" {
		parts := strings.Split(lang, ".")
		if len(parts) > 0 {
			langCode := strings.Split(parts[0], "_")[0]
			return langCode
		}
		return lang
	}

	// Default to Spanish
	return "es"
}
