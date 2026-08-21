package i18n

import (
	"os"
	"testing"

	"golang.org/x/text/language"
)

func TestInit(t *testing.T) {
	Init()
	if system == nil {
		t.Fatal("system should not be nil after Init()")
	}
}

func TestT(t *testing.T) {
	Init()

	tests := []struct {
		lang     string
		key      string
		expected string
	}{
		{"es", "tui.title", "Lele 🦞"},
		{"es", "tui.welcome", "¡Bienvenido a Lele!"},
		{"en", "tui.title", "Lele 🦞"},
		{"en", "tui.welcome", "Welcome to Lele!"},
		{"pt", "tui.title", "Lele 🦞"},
		{"pt", "tui.welcome", "Bem-vindo ao Lele!"},
	}

	for _, tt := range tests {
		SetLanguage(tt.lang)
		result := T(tt.key)
		if result != tt.expected {
			t.Errorf("T(%s) with lang %s = %s, want %s", tt.key, tt.lang, result, tt.expected)
		}
	}
}

func TestMissingKey(t *testing.T) {
	Init()
	SetLanguage("es")

	key := "nonexistent.key"
	result := T(key)
	if result != key {
		t.Errorf("T(%s) = %s, want %s (should return key itself for missing keys)", key, result, key)
	}
}

func TestSetLanguage(t *testing.T) {
	Init()

	// Test valid languages
	validLangs := []string{"es", "en", "pt"}
	for _, lang := range validLangs {
		SetLanguage(lang)
		if GetLanguage() != lang {
			t.Errorf("SetLanguage(%s) failed, GetLanguage() = %s", lang, GetLanguage())
		}
	}

	// Test invalid language falls back to Spanish
	SetLanguage("invalid")
	if GetLanguage() != "es" {
		t.Errorf("SetLanguage('invalid') should fallback to 'es', got %s", GetLanguage())
	}
}

func TestDetectLanguage(t *testing.T) {
	// Save original env vars
	origLang := os.Getenv("LANG")
	origLeleLang := os.Getenv("LELE_LANG")
	origLcAll := os.Getenv("LC_ALL")
	defer func() {
		os.Setenv("LANG", origLang)
		os.Setenv("LELE_LANG", origLeleLang)
		os.Setenv("LC_ALL", origLcAll)
	}()

	// Clear all env vars to test default behavior
	os.Unsetenv("LANG")
	os.Unsetenv("LELE_LANG")
	os.Unsetenv("LC_ALL")

	lang := detectLanguage()
	if lang != "es" {
		t.Errorf("detectLanguage() with no env vars = %s, want 'es'", lang)
	}

	// Test with LELE_LANG set
	os.Setenv("LELE_LANG", "pt")
	lang = detectLanguage()
	if lang != "pt" {
		t.Errorf("detectLanguage() with LELE_LANG=pt = %s, want 'pt'", lang)
	}

	// Test with LANG set
	os.Unsetenv("LELE_LANG")
	os.Setenv("LANG", "en_US.UTF-8")
	lang = detectLanguage()
	if lang != "en" {
		t.Errorf("detectLanguage() with LANG=en_US.UTF-8 = %s, want 'en'", lang)
	}
}

func TestAvailableLanguages(t *testing.T) {
	langs := AvailableLanguages()
	if len(langs) != 3 {
		t.Errorf("AvailableLanguages() returned %d languages, want 3", len(langs))
	}

	// Check all expected languages are present
	langMap := make(map[string]bool)
	for _, l := range langs {
		langMap[l] = true
	}

	for _, expected := range []string{"es", "en", "pt"} {
		if !langMap[expected] {
			t.Errorf("AvailableLanguages() missing %s", expected)
		}
	}
}

func TestGetLanguageTag(t *testing.T) {
	Init()
	SetLanguage("en")

	tag := GetLanguageTag()
	if tag != language.English {
		t.Errorf("GetLanguageTag() = %v, want %v", tag, language.English)
	}
}

func TestAllKeysExist(t *testing.T) {
	// Test that all required keys exist in all languages
	requiredKeys := []string{
		"tui.title",
		"tui.welcome",
		"tui.goodbye",
		"tui.newChat",
		"tui.send",
		"tui.help",
		"tui.quit",
		"tui.you",
		"tui.selectAgent",
		"tui.selectModel",
		"tui.selectChat",
		"tui.status.ready",
		"tui.status.thinking",
		"tui.status.sending",
		"tui.status.error",
		"tui.status.connected",
		"tui.status.disconnected",
		"tui.sidebar.agents",
		"tui.sidebar.models",
		"tui.sidebar.chats",
		"tui.command.help",
		"tui.command.new",
		"tui.command.clear",
		"tui.command.agent",
		"tui.command.model",
		"tui.command.theme",
		"tui.onboard.welcome",
		"tui.onboard.pressEnter",
		"tui.onboard.escSkip",
		"tui.onboard.skipConfirm",
		"tui.onboard.skipYes",
		"tui.onboard.skipNo",
		"tui.onboard.language",
		"tui.onboard.theme",
		"tui.onboard.themeHint",
		"tui.onboard.progress",
		"tui.onboard.pickProvider",
		"tui.onboard.pickProviderHint",
		"tui.onboard.otherCustom",
		"tui.onboard.skipForNow",
		"tui.onboard.noKeyNeeded",
		"tui.onboard.keyFormat",
		"tui.onboard.verifying",
		"tui.onboard.verifyFailed",
		"tui.onboard.done",
		"tui.onboard.doneProvider",
		"tui.onboard.doneModel",
		"tui.onboard.doneKey",
		"tui.onboard.tips",
		"tui.onboard.tipSend",
		"tui.onboard.tipModels",
		"tui.onboard.tipAgents",
		"tui.onboard.tipChats",
		"tui.onboard.tipConnect",
		"tui.onboard.pressEnterStart",
	}

	Init()

	for _, lang := range AvailableLanguages() {
		SetLanguage(lang)
		for _, key := range requiredKeys {
			result := T(key)
			if result == key {
				t.Errorf("Key %s missing in language %s", key, lang)
			}
		}
	}
}
