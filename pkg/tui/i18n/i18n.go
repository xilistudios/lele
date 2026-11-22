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

var localeFiles = map[language.Tag]string{
	language.Spanish:    "locales/es.json",
	language.English:    "locales/en.json",
	language.Portuguese: "locales/pt.json",
}

type Localizer interface {
	T(key string) string
}

type localeMap map[string]string

type i18nSystem struct {
	currentLang  language.Tag
	translations map[language.Tag]localeMap
	fallback     language.Tag
}

var system *i18nSystem

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

func Init() {
	system = &i18nSystem{
		translations: make(map[language.Tag]localeMap),
		fallback:     language.Spanish,
	}

	for tag, filename := range localeFiles {
		translations, err := loadLocale(filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "i18n: warning: %v\n", err)
			continue
		}
		system.translations[tag] = translations
	}

	ensureExternalLoaded()
	externalMu.RLock()
	for code, translations := range externalLangs {
		if tag, err := language.Parse(code); err == nil {
			system.translations[tag] = translations
		}
	}
	externalMu.RUnlock()

	lang := detectLanguage()
	system.setLanguage(lang)
}

func InitWithLanguage(lang string) {
	Init()
	SetLanguage(lang)
}

func T(key string) string {
	if system == nil {
		Init()
	}
	return system.T(key)
}

func SetLanguage(lang string) {
	if system == nil {
		Init()
	}
	system.setLanguage(lang)
}

func GetLanguage() string {
	if system == nil {
		Init()
	}
	return system.currentLang.String()
}

func GetLanguageTag() language.Tag {
	if system == nil {
		Init()
	}
	return system.currentLang
}

func (s *i18nSystem) T(key string) string {
	if translations, ok := s.translations[s.currentLang]; ok {
		if val, exists := translations[key]; exists {
			return val
		}
	}

	if translations, ok := s.translations[s.fallback]; ok {
		if val, exists := translations[key]; exists {
			return val
		}
	}

	return key
}

func (s *i18nSystem) setLanguage(lang string) {
	lang = strings.ToLower(strings.TrimSpace(lang))

	tag, err := language.Parse(lang)
	if err != nil {
		switch lang {
		case "es", "español", "spanish":
			tag = language.Spanish
		case "en", "english":
			tag = language.English
		case "pt", "português", "portuguese":
			tag = language.Portuguese
		default:
			if _, ok := externalLangs[lang]; ok {
				if t, perr := language.Parse(lang); perr == nil {
					tag = t
				} else {
					tag = language.Make(lang)
				}
			} else {
				tag = s.fallback
			}
		}
	}

	if _, ok := s.translations[tag]; !ok {
		externalMu.RLock()
		if pack, ok2 := externalLangs[lang]; ok2 {
			s.translations[tag] = pack
		} else if pack, ok2 := externalLangs[tag.String()]; ok2 {
			s.translations[tag] = pack
		}
		externalMu.RUnlock()
	}

	if _, ok := s.translations[tag]; ok {
		s.currentLang = tag
		return
	}
	for registeredTag := range s.translations {
		regBase, _ := registeredTag.Base()
		tagBase, _ := tag.Base()
		if regBase == tagBase {
			s.currentLang = registeredTag
			return
		}
	}
	s.currentLang = s.fallback
}

func detectLanguage() string {
	if lang := os.Getenv("LELE_LANG"); lang != "" {
		return lang
	}

	if lang := os.Getenv("LANG"); lang != "" {
		parts := strings.Split(lang, ".")
		if len(parts) > 0 {
			langCode := strings.Split(parts[0], "_")[0]
			return langCode
		}
		return lang
	}

	if lang := os.Getenv("LC_ALL"); lang != "" {
		parts := strings.Split(lang, ".")
		if len(parts) > 0 {
			langCode := strings.Split(parts[0], "_")[0]
			return langCode
		}
		return lang
	}

	return "es"
}
