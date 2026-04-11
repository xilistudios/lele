package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed locales/*.json
var localeFS embed.FS

var (
	bundle    *i18n.Bundle
	localizer *i18n.Localizer
	lang      string
)

func init() {
	bundle = i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	lang = detectLanguage()

	loadLocales()
	createLocalizer()
}

func SetLanguage(l string) {
	lang = l
	createLocalizer()
}

func GetLanguage() string {
	return lang
}

func detectLanguage() string {
	if overrideLang := os.Getenv("LELE_LANG"); overrideLang != "" {
		return normalizeLang(overrideLang)
	}

	langEnv := os.Getenv("LANG")
	if langEnv == "" {
		langEnv = os.Getenv("LC_ALL")
	}
	if langEnv == "" {
		langEnv = os.Getenv("LC_MESSAGES")
	}

	if langEnv != "" {
		return normalizeLang(langEnv)
	}

	return "en"
}

func normalizeLang(langStr string) string {
	langStr = strings.ToLower(langStr)

	langStr = strings.Split(langStr, ".")[0]
	langStr = strings.Split(langStr, "@")[0]

	parts := strings.Split(langStr, "_")
	if len(parts) >= 1 {
		baseLang := parts[0]
		switch baseLang {
		case "en", "es", "pt", "fr", "ja", "vi", "zh":
			return baseLang
		case "ptb", "ptbr":
			return "pt"
		case "zhcn", "zhsg", "zh hans":
			return "zh"
		case "zhtw", "zhht", "zh hant":
			return "zh"
		}
	}

	return "en"
}

func loadLocales() {
	locales := []string{"en", "es", "pt", "fr", "ja", "vi", "zh"}

	for _, l := range locales {
		path := fmt.Sprintf("locales/%s.json", l)
		if _, err := bundle.LoadMessageFileFS(localeFS, path); err != nil {
			continue
		}
	}
}

func createLocalizer() {
	langs := []string{lang}
	if lang != "en" {
		langs = append(langs, "en")
	}
	localizer = i18n.NewLocalizer(bundle, langs...)
}

func T(key string) string {
	msg, err := localizer.Localize(&i18n.LocalizeConfig{
		MessageID: key,
	})
	if err != nil {
		return key
	}
	return msg
}

func TWithData(key string, data map[string]interface{}) string {
	msg, err := localizer.Localize(&i18n.LocalizeConfig{
		MessageID:    key,
		TemplateData: data,
	})
	if err != nil {
		return key
	}
	return msg
}

func TPlural(key string, count int) string {
	msg, err := localizer.Localize(&i18n.LocalizeConfig{
		MessageID:   key,
		PluralCount: count,
	})
	if err != nil {
		return key
	}
	return msg
}

func TPrintf(key string, args ...interface{}) string {
	msg := T(key)
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

func AvailableLanguages() []string {
	return []string{"en", "es", "pt", "fr", "ja", "vi", "zh"}
}
