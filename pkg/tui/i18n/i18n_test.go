package i18n

import (
	"os"
	"strings"
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

	validLangs := []string{"es", "en", "pt"}
	for _, lang := range validLangs {
		SetLanguage(lang)
		if GetLanguage() != lang {
			t.Errorf("SetLanguage(%s) failed, GetLanguage() = %s", lang, GetLanguage())
		}
	}

	SetLanguage("invalid")
	if GetLanguage() != "es" {
		t.Errorf("SetLanguage('invalid') should fallback to 'es', got %s", GetLanguage())
	}
}

func TestDetectLanguage(t *testing.T) {
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

	lang := detectLanguage()
	if lang != "es" {
		t.Errorf("detectLanguage() with no env vars = %s, want 'es'", lang)
	}

	os.Setenv("LELE_LANG", "pt")
	lang = detectLanguage()
	if lang != "pt" {
		t.Errorf("detectLanguage() with LELE_LANG=pt = %s, want 'pt'", lang)
	}

	os.Unsetenv("LELE_LANG")
	os.Setenv("LANG", "en_US.UTF-8")
	lang = detectLanguage()
	if lang != "en" {
		t.Errorf("detectLanguage() with LANG=en_US.UTF-8 = %s, want 'en'", lang)
	}
}

func TestAvailableLanguages(t *testing.T) {
	SetPackDir(t.TempDir())
	langs := AvailableLanguages()
	if len(langs) != 3 {
		t.Errorf("AvailableLanguages() returned %d languages, want 3 (builtins only)", len(langs))
	}

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

func TestInstalledLanguagesIncludesExternal(t *testing.T) {
	dir := t.TempDir()
	SetPackDir(dir)
	if err := os.MkdirAll(dir+"/tui", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/tui/fr.json", []byte(`{"tui.title":"Lele"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	langs := InstalledLanguages()
	found := false
	for _, l := range langs {
		if l == "fr" {
			found = true
		}
	}
	if !found {
		t.Fatalf("InstalledLanguages() = %v, want fr included", langs)
	}
	SetPackDir("")
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
		"tui.queue.status",
		"tui.queue.removeHint",
		"tui.queue.flushHint",
		"tui.queue.full",
		"tui.queue.removed",
		"tui.queue.dropped",
		"tui.queue.empty",
		"tui.queue.flushConfirm",
		"tui.editModel",
		"tui.providerDetailHints",
		"tui.modelDeleteConfirm",
		"tui.modelDeleteNoRow",
		"tui.subagentsRunning",
		"tui.settings.keepAwake",
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

func TestMCPAuthKeysExistInAllBuiltinLanguages(t *testing.T) {
	keys := []string{
		"tui.mcpAuth",
		"tui.mcpAuthStarting",
		"tui.mcpAuthBrowserOpened",
		"tui.mcpAuthBrowserFailed",
		"tui.mcpAuthURLLabel",
		"tui.mcpAuthInstructions",
		"tui.mcpAuthPlaceholder",
		"tui.mcpAuthHintEnter",
		"tui.mcpAuthHintCopy",
		"tui.mcpAuthHintClose",
		"tui.mcpAuthHintClosed",
		"tui.mcpAuthCopied",
		"tui.mcpAuthCopyFailed",
		"tui.mcpAuthEmptyCode",
		"tui.mcpAuthBadInput",
		"tui.mcpAuthSuccess",
		"tui.mcpAuthFailed",
		"tui.mcpAuthStartFailed",
		"tui.mcpAuthSubmitFailed",
		"tui.mcpAuthServerUnknown",
		"tui.mcpAuthNotApplicable",
		"tui.mcpAuthErrNotSupported",
		"tui.mcpAuthErrNotRequired",
		"tui.mcpAuthErrStoreDisabled",
		"tui.mcpAuthErrCanceled",
		"tui.mcpAuthErrTimedOut",
		"tui.mcpAuthErrProbeFailed",
		"tui.mcpAuthRefused",
		"tui.mcpAuthRefusedUnknown",
		"tui.mcpAuthRequired",
		"tui.mcpAuthOK",
		"tui.mcpAuthLogoutDone",
		"tui.mcpAuthLogoutNone",
		"tui.mcpAuthLogoutFailed",
		"tui.mcpAuthUnavailable",
		"tui.mcpDetailAuth",
		"tui.mcpDetailAuthState",
		"tui.mcpDetailNone",
		"tui.mcpDetailHints",
		"tui.mcpListHints",
		"tui.mcpLogoutHint",
	}

	Init()
	langs := BuiltinLanguages()
	if len(langs) == 0 {
		t.Fatal("BuiltinLanguages() is empty")
	}
	for _, want := range []string{"en", "es", "pt"} {
		found := false
		for _, l := range langs {
			if l == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("BuiltinLanguages() = %v, want %q included", langs, want)
		}
	}

	prev := GetLanguage()
	t.Cleanup(func() { SetLanguage(prev) })

	rawByLang := map[string]localeMap{}
	for _, lang := range []string{"en", "es", "pt"} {
		tag, err := language.Parse(lang)
		if err != nil {
			t.Fatalf("language.Parse(%q): %v", lang, err)
		}
		m, err := loadLocale(localeFiles[tag])
		if err != nil {
			t.Fatalf("loadLocale(%s): %v", lang, err)
		}
		rawByLang[lang] = m
	}

	reference := map[string]string{}
	SetLanguage("en")
	for _, key := range keys {
		if v := T(key); v != key {
			reference[key] = formatVerbs(v)
		}
	}
	for _, lang := range langs {
		SetLanguage(lang)
		for _, key := range keys {
			if m, ok := rawByLang[lang]; ok {
				if _, exists := m[key]; !exists {
					t.Errorf("key %s missing from %s.json (T() would mask it via the default-locale fallback)", key, lang)
				}
			}
			got := T(key)
			if got == key {
				t.Errorf("key %s missing in language %s", key, lang)
				continue
			}
			if got == "" {
				t.Errorf("key %s is empty in language %s", key, lang)
			}
			if ref, ok := reference[key]; ok && formatVerbs(got) != ref {
				t.Errorf("key %s: format verbs %q in %s, want %q (the en source)",
					key, formatVerbs(got), lang, ref)
			}
		}
	}
}

func formatVerbs(s string) string {
	var out []rune
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+1 < len(s) {
			out = append(out, rune(s[i+1]))
			i++
		}
	}
	return string(out)
}

func TestConnectPickerKeysExistInAllBuiltinLanguages(t *testing.T) {
	keys := []string{
		"tui.connectPickProvider",
		"tui.connectRowCustomOpenAI",
		"tui.connectRowCustomAnthropic",
		"tui.connectProviderNamePlaceholder",
	}

	Init()
	langs := BuiltinLanguages()
	if len(langs) == 0 {
		t.Fatal("BuiltinLanguages() is empty")
	}

	prev := GetLanguage()
	t.Cleanup(func() { SetLanguage(prev) })

	rawByLang := map[string]localeMap{}
	for _, lang := range []string{"en", "es", "pt"} {
		tag, err := language.Parse(lang)
		if err != nil {
			t.Fatalf("language.Parse(%q): %v", lang, err)
		}
		m, err := loadLocale(localeFiles[tag])
		if err != nil {
			t.Fatalf("loadLocale(%s): %v", lang, err)
		}
		rawByLang[lang] = m
	}

	reference := map[string]string{}
	SetLanguage("en")
	for _, key := range keys {
		if v := T(key); v != key {
			reference[key] = formatVerbs(v)
		}
	}
	for _, lang := range langs {
		SetLanguage(lang)
		for _, key := range keys {
			if m, ok := rawByLang[lang]; ok {
				if _, exists := m[key]; !exists {
					t.Errorf("key %s missing from %s.json (T() would mask it via the default-locale fallback)", key, lang)
				}
			}
			got := T(key)
			if got == key {
				t.Errorf("key %s missing in language %s", key, lang)
				continue
			}
			if got == "" {
				t.Errorf("key %s is empty in language %s", key, lang)
			}
			if ref, ok := reference[key]; ok && formatVerbs(got) != ref {
				t.Errorf("key %s: format verbs %q in %s, want %q (the en source)",
					key, formatVerbs(got), lang, ref)
			}
		}
	}
}

func TestConnectOAuthKeysExistInAllBuiltinLanguages(t *testing.T) {
	keys := []string{
		"tui.connectRowOAuthOpenAI",
		"tui.connectRowOAuthAnthropic",
		"tui.connectRowOAuthGemini",
		"tui.connectOAuthTitle",
		"tui.connectOAuthStarting",
		"tui.connectOAuthURLLabel",
		"tui.connectOAuthURLTruncated",
		"tui.connectOAuthInstructions",
		"tui.connectOAuthPlaceholder",
		"tui.connectOAuthHintEnter",
		"tui.connectOAuthHintCopy",
		"tui.connectOAuthHintClose",
		"tui.connectOAuthHintClosed",
		"tui.connectOAuthCopied",
		"tui.connectOAuthCopyFailed",
		"tui.connectOAuthEmptyInput",
		"tui.connectOAuthSuccess",
		"tui.connectOAuthFailed",
		"tui.connectOAuthSaveFailed",
		"tui.connectOAuthStartFailed",
		"tui.connectOAuthSubmitFailed",
		"tui.connectOAuthErrCanceled",
		"tui.connectOAuthErrTimedOut",
		"tui.connectOAuthErrClosed",
		"tui.connectOAuthErrNoManualRedirect",
		"tui.connectOAuthErrStateMismatch",
		"tui.connectOAuthErrUnknownProvider",
		"tui.connectOAuthErrUnregisteredProvider",
		"tui.connectOAuthErrInconsistentRow",
	}

	Init()
	langs := BuiltinLanguages()
	if len(langs) == 0 {
		t.Fatal("BuiltinLanguages() is empty")
	}

	prev := GetLanguage()
	t.Cleanup(func() { SetLanguage(prev) })

	rawByLang := map[string]localeMap{}
	for _, lang := range []string{"en", "es", "pt"} {
		tag, err := language.Parse(lang)
		if err != nil {
			t.Fatalf("language.Parse(%q): %v", lang, err)
		}
		m, err := loadLocale(localeFiles[tag])
		if err != nil {
			t.Fatalf("loadLocale(%s): %v", lang, err)
		}
		rawByLang[lang] = m
	}

	reference := map[string]string{}
	SetLanguage("en")
	for _, key := range keys {
		if v := T(key); v != key {
			reference[key] = formatVerbs(v)
		}
	}
	for _, lang := range langs {
		SetLanguage(lang)
		for _, key := range keys {
			if m, ok := rawByLang[lang]; ok {
				if _, exists := m[key]; !exists {
					t.Errorf("key %s missing from %s.json (T() would mask it via the default-locale fallback)", key, lang)
				}
			}
			got := T(key)
			if got == key {
				t.Errorf("key %s missing in language %s", key, lang)
				continue
			}
			if got == "" {
				t.Errorf("key %s is empty in language %s", key, lang)
			}
			if ref, ok := reference[key]; ok && formatVerbs(got) != ref {
				t.Errorf("key %s: format verbs %q in %s, want %q (the en source)",
					key, formatVerbs(got), lang, ref)
			}
		}
	}
}

func TestMCPAddKeysExistInAllBuiltinLanguages(t *testing.T) {
	keys := []string{
		"tui.mcpAdd",
		"tui.mcpAddHint",
		"tui.mcpAddStepName",
		"tui.mcpAddStepKind",
		"tui.mcpAddStepCommand",
		"tui.mcpAddStepURL",
		"tui.mcpAddStepArgs",
		"tui.mcpAddStepType",
		"tui.mcpAddStepEnv",
		"tui.mcpAddStepHeaders",
		"tui.mcpAddStepDescription",
		"tui.mcpAddStepLayer",
		"tui.mcpAddStepReview",
		"tui.mcpAddNamePlaceholder",
		"tui.mcpAddKindPlaceholder",
		"tui.mcpAddCommandPlaceholder",
		"tui.mcpAddURLPlaceholder",
		"tui.mcpAddArgsPlaceholder",
		"tui.mcpAddTypePlaceholder",
		"tui.mcpAddEnvPlaceholder",
		"tui.mcpAddHeadersPlaceholder",
		"tui.mcpAddDescriptionPlaceholder",
		"tui.mcpAddLayerPlaceholder",
		"tui.mcpAddConfirmPlaceholder",
		"tui.mcpAddErrNameRequired",
		"tui.mcpAddErrNameSlash",
		"tui.mcpAddErrKind",
		"tui.mcpAddErrCommand",
		"tui.mcpAddErrURL",
		"tui.mcpAddErrType",
		"tui.mcpAddErrPairNoEquals",
		"tui.mcpAddErrPairNoKey",
		"tui.mcpAddErrLayer",
		"tui.mcpAddErrLayerNoPath",
		"tui.mcpAddErrPathNotAllowed",
		"tui.mcpAddErrConfirm",
		"tui.mcpAddReviewWarnReplace",
		"tui.mcpAddReviewWarnShadowed",
		"tui.mcpAddReviewNoteShadows",
		"tui.mcpAddReviewAuthHint",
		"tui.mcpAddHints",
		"tui.mcpAddReviewHints",
		"tui.mcpAddSuccess",
		"tui.mcpAddReplaced",
		"tui.mcpAddNoChange",
		"tui.mcpAddShadowSuffix",
		"tui.mcpAddFailed",
		"tui.mcpAddLayerNoPath",
		"tui.mcpAddPathNotAllowed",
		"tui.mcpListHints",
		"tui.mcpNoServers",
	}

	Init()
	langs := BuiltinLanguages()
	if len(langs) == 0 {
		t.Fatal("BuiltinLanguages() is empty")
	}
	for _, want := range []string{"en", "es", "pt"} {
		found := false
		for _, l := range langs {
			if l == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("BuiltinLanguages() = %v, want %q included", langs, want)
		}
	}

	prev := GetLanguage()
	t.Cleanup(func() { SetLanguage(prev) })

	rawByLang := map[string]localeMap{}
	for _, lang := range []string{"en", "es", "pt"} {
		tag, err := language.Parse(lang)
		if err != nil {
			t.Fatalf("language.Parse(%q): %v", lang, err)
		}
		m, err := loadLocale(localeFiles[tag])
		if err != nil {
			t.Fatalf("loadLocale(%s): %v", lang, err)
		}
		rawByLang[lang] = m
	}

	verbs := func(s string) string {
		var out []rune
		for i := 0; i < len(s); i++ {
			if s[i] == '%' && i+1 < len(s) {
				out = append(out, rune(s[i+1]))
				i++
			}
		}
		return string(out)
	}

	reference := map[string]string{}
	SetLanguage("en")
	for _, key := range keys {
		if v := T(key); v != key {
			reference[key] = verbs(v)
		}
	}
	for _, lang := range langs {
		SetLanguage(lang)
		for _, key := range keys {
			if m, ok := rawByLang[lang]; ok {
				if _, exists := m[key]; !exists {
					t.Errorf("key %s missing from %s.json (T() would mask it via the default-locale fallback)", key, lang)
				}
			}
			got := T(key)
			if got == key {
				t.Errorf("key %s missing in language %s", key, lang)
				continue
			}
			if got == "" {
				t.Errorf("key %s is empty in language %s", key, lang)
			}
			if ref, ok := reference[key]; ok && verbs(got) != ref {
				t.Errorf("key %s: format verbs %q in %s, want %q (the en source)",
					key, verbs(got), lang, ref)
			}
		}
	}
}

func TestKeepAwakeKeysExistInAllBuiltinLanguages(t *testing.T) {
	keys := []string{
		"tui.settings.keepAwake",
		"tui.settings.keepAwakeCancelHint",
	}

	Init()
	langs := []string{"en", "es", "pt"}
	prev := GetLanguage()
	t.Cleanup(func() { SetLanguage(prev) })

	for _, lang := range langs {
		tag, err := language.Parse(lang)
		if err != nil {
			t.Fatalf("language.Parse(%q): %v", lang, err)
		}
		raw, err := loadLocale(localeFiles[tag])
		if err != nil {
			t.Fatalf("loadLocale(%s): %v", lang, err)
		}
		for _, key := range keys {
			value, exists := raw[key]
			if !exists {
				t.Errorf("key %s missing from %s.json (T() would mask it via the default-locale fallback)", key, lang)
				continue
			}
			if value == "" {
				t.Errorf("key %s is empty in %s.json", key, lang)
			}
		}
	}
}

func TestProviderEditKeysExistInAllBuiltinLanguages(t *testing.T) {
	keys := []string{
		"tui.editProvider",
		"tui.editProviderAction",
		"tui.editProviderKeepHint",
		"tui.editProviderOptionalHint",
		"tui.editProviderWebSearchHint",
		"tui.editProviderHeadersHint",
		"tui.providerUpdated",
		"tui.providerDetailHints",
	}

	Init()
	langs := []string{"en", "es", "pt"}
	prev := GetLanguage()
	t.Cleanup(func() { SetLanguage(prev) })

	for _, lang := range langs {
		tag, err := language.Parse(lang)
		if err != nil {
			t.Fatalf("language.Parse(%q): %v", lang, err)
		}
		raw, err := loadLocale(localeFiles[tag])
		if err != nil {
			t.Fatalf("loadLocale(%s): %v", lang, err)
		}
		for _, key := range keys {
			value, exists := raw[key]
			if !exists {
				t.Errorf("key %s missing from %s.json (T() would mask it via the default-locale fallback)", key, lang)
				continue
			}
			if value == "" {
				t.Errorf("key %s is empty in %s.json", key, lang)
			}
		}
		if hints := raw["tui.providerDetailHints"]; !strings.Contains(hints, "E:") {
			t.Errorf("tui.providerDetailHints in %s does not advertise the edit shortcut: %q", lang, hints)
		}
	}
}

func TestEditModelReasoningKeysExistInAllBuiltinLanguages(t *testing.T) {
	keys := []string{
		"tui.editModelReasoningDefaultHint",
		"tui.editModelReasoningTokensHint",
	}

	Init()
	prev := GetLanguage()
	t.Cleanup(func() { SetLanguage(prev) })

	for _, lang := range []string{"en", "es", "pt"} {
		tag, err := language.Parse(lang)
		if err != nil {
			t.Fatalf("language.Parse(%q): %v", lang, err)
		}
		raw, err := loadLocale(localeFiles[tag])
		if err != nil {
			t.Fatalf("loadLocale(%s): %v", lang, err)
		}
		for _, key := range keys {
			value, exists := raw[key]
			if !exists {
				t.Errorf("key %s missing from %s.json (T() would mask it via the default-locale fallback)", key, lang)
				continue
			}
			if value == "" {
				t.Errorf("key %s is empty in %s.json", key, lang)
			}
		}
	}
}
