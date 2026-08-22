package i18n

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/text/language"
)

// resetSystem nils out the global system so tests can exercise the
// auto-Init paths.
func resetSystem() {
	system = nil
}

func TestInitWithLanguage(t *testing.T) {
	resetSystem()
	InitWithLanguage("pt")
	if system == nil {
		t.Fatal("system should be non-nil after InitWithLanguage")
	}
	if GetLanguage() != "pt" {
		t.Errorf("GetLanguage() = %q after InitWithLanguage(pt), want pt", GetLanguage())
	}
}

func TestAutoInitWhenNil(t *testing.T) {
	// Control the ambient environment so auto-Init detection is deterministic.
	origLang := os.Getenv("LANG")
	origLeleLang := os.Getenv("LELE_LANG")
	origLcAll := os.Getenv("LC_ALL")
	defer func() {
		os.Setenv("LANG", origLang)
		os.Setenv("LELE_LANG", origLeleLang)
		os.Setenv("LC_ALL", origLcAll)
	}()
	os.Unsetenv("LANG")
	os.Unsetenv("LELE_LANG")
	os.Unsetenv("LC_ALL")

	// Every package-level entry point must auto-Init when system is nil.
	if !strings.HasPrefix(T("tui.title"), "Lele") {
		t.Error("T should auto-Init when system is nil")
	}

	resetSystem()
	SetLanguage("en")
	if GetLanguage() != "en" {
		t.Errorf("SetLanguage should auto-Init; GetLanguage = %q, want en", GetLanguage())
	}

	resetSystem()
	if GetLanguage() != "es" {
		t.Errorf("GetLanguage should auto-Init; got %q, want es", GetLanguage())
	}

	resetSystem()
	if GetLanguageTag() != language.Spanish {
		t.Errorf("GetLanguageTag should auto-Init; got %v, want es", GetLanguageTag())
	}
}

func TestSystemTFallback(t *testing.T) {
	Init()
	// Force a current language with no translations to exercise the
	// fallback path in the receiver's T method.
	s := &i18nSystem{
		currentLang:  language.French,
		translations: map[language.Tag]localeMap{language.French: {}, language.Spanish: {"tui.title": "Lele 🦞"}},
		fallback:     language.Spanish,
	}
	system = s

	// Current lang (French) has no key -> falls back to Spanish.
	if got := T("tui.title"); got != "Lele 🦞" {
		t.Errorf("T fallback = %q, want %q", got, "Lele 🦞")
	}
}

func TestSetLanguageMappings(t *testing.T) {
	Init()

	// Long-form / colloquial names map to supported languages even though
	// language.Parse fails on them (exercises the hand-rolled switch).
	longForms := map[string]string{
		"español":    "es",
		"spanish":    "es",
		"english":    "en",
		"português":  "pt",
		"portuguese": "pt",
	}
	for input, want := range longForms {
		SetLanguage(input)
		if got := GetLanguage(); got != want {
			t.Errorf("SetLanguage(%q) -> GetLanguage() = %q, want %q", input, got, want)
		}
	}

	// Exact short codes.
	for _, code := range []string{"es", "en", "pt"} {
		SetLanguage(code)
		if GetLanguage() != code {
			t.Errorf("SetLanguage(%q) -> %q", code, GetLanguage())
		}
	}
}

func TestSetLanguageBaseMatch(t *testing.T) {
	Init()

	// es-MX parses fine but is not a registered key; base-language matching
	// should resolve it to Spanish.
	SetLanguage("es-MX")
	if GetLanguage() != "es" {
		t.Errorf("SetLanguage(es-MX) -> GetLanguage() = %q, want es", GetLanguage())
	}

	// es_ES also matches the Spanish base.
	SetLanguage("es_ES")
	if GetLanguage() != "es" {
		t.Errorf("SetLanguage(es_ES) -> GetLanguage() = %q, want es", GetLanguage())
	}
}

func TestSetLanguageNoMatchFallsBack(t *testing.T) {
	Init()

	// French parses fine but has no matching base -> fallback (Spanish).
	SetLanguage("fr")
	if GetLanguage() != "es" {
		t.Errorf("SetLanguage(fr) -> GetLanguage() = %q, want es", GetLanguage())
	}

	// Unknown parseable value also falls back.
	SetLanguage("xx")
	if GetLanguage() != "es" {
		t.Errorf("SetLanguage(xx) -> GetLanguage() = %q, want es", GetLanguage())
	}

	// Invalid unparseable value falls back.
	SetLanguage("not a language")
	if GetLanguage() != "es" {
		t.Errorf("SetLanguage(not a language) -> GetLanguage() = %q, want es", GetLanguage())
	}
}

func TestSetLanguageCaseAndSpace(t *testing.T) {
	Init()
	SetLanguage("  EN  ")
	if GetLanguage() != "en" {
		t.Errorf("SetLanguage(  EN  ) -> GetLanguage() = %q, want en", GetLanguage())
	}
}

func TestDetectLanguageLCALL(t *testing.T) {
	origLang := os.Getenv("LANG")
	origLeleLang := os.Getenv("LELE_LANG")
	origLcAll := os.Getenv("LC_ALL")
	defer func() {
		os.Setenv("LANG", origLang)
		os.Setenv("LELE_LANG", origLeleLang)
		os.Setenv("LC_ALL", origLcAll)
	}()

	os.Unsetenv("LELE_LANG")
	os.Unsetenv("LANG")
	os.Setenv("LC_ALL", "pt_BR.UTF-8")
	if got := detectLanguage(); got != "pt" {
		t.Errorf("detectLanguage() with LC_ALL=pt_BR.UTF-8 = %q, want pt", got)
	}

	// LANG without a dotted encoding suffix.
	os.Unsetenv("LC_ALL")
	os.Setenv("LANG", "de_DE")
	if got := detectLanguage(); got != "de" {
		t.Errorf("detectLanguage() with LANG=de_DE = %q, want de", got)
	}

	// LANG with no encoding and no region.
	os.Setenv("LANG", "fr")
	if got := detectLanguage(); got != "fr" {
		t.Errorf("detectLanguage() with LANG=fr = %q, want fr", got)
	}
}

func TestLoadLocaleMissingFile(t *testing.T) {
	// A non-existent embedded file must produce an error.
	if _, err := loadLocale("locales/does-not-exist.json"); err == nil {
		t.Error("loadLocale(missing file) error = nil, want non-nil")
	}
}

func TestDetectLanguageLANGNoSuffix(t *testing.T) {
	origLang := os.Getenv("LANG")
	origLeleLang := os.Getenv("LELE_LANG")
	origLcAll := os.Getenv("LC_ALL")
	defer func() {
		os.Setenv("LANG", origLang)
		os.Setenv("LELE_LANG", origLeleLang)
		os.Setenv("LC_ALL", origLcAll)
	}()

	// LANG set but LELE_LANG takes precedence.
	os.Setenv("LELE_LANG", "pt")
	os.Setenv("LANG", "en_US.UTF-8")
	if got := detectLanguage(); got != "pt" {
		t.Errorf("detectLanguage() with LELE_LANG=pt, LANG=en_US.UTF-8 = %q, want pt", got)
	}

	// LANG with a region separator (underscore) and encoding fully split.
	os.Unsetenv("LELE_LANG")
	os.Setenv("LANG", "en_US.UTF-8")
	if got := detectLanguage(); got != "en" {
		t.Errorf("detectLanguage() with LANG=en_US.UTF-8 = %q, want en", got)
	}

	// LANG with an unusual double-dot form.
	os.Setenv("LANG", "ja_JP.eucJP")
	if got := detectLanguage(); got != "ja" {
		t.Errorf("detectLanguage() with LANG=ja_JP.eucJP = %q, want ja", got)
	}
}
