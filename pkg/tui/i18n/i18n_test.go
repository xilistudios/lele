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
	// Isolate from any downloaded packs on the host machine.
	SetPackDir(t.TempDir())
	langs := AvailableLanguages()
	if len(langs) != 3 {
		t.Errorf("AvailableLanguages() returned %d languages, want 3 (builtins only)", len(langs))
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

func TestInstalledLanguagesIncludesExternal(t *testing.T) {
	dir := t.TempDir()
	SetPackDir(dir)
	// Write a fake downloaded pack.
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
	// Reset for other tests.
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

// TestMCPAuthKeysExistInAllBuiltinLanguages covers the /mcp auth surface: the
// OAuth modal (ModalMCPAuth, "a" in the server detail), the visibility markers
// and sign-out lines of the list/detail, and the paste-refusal messages.
// TestAllKeysExist only checks its own hardcoded list, so it would NOT catch a
// key added to one locale and forgotten in the other two — which is exactly
// the mistake this table of new keys is here to catch. Every key must be
// present VERBATIM in each builtin locale's JSON (T() alone cannot prove that:
// a missing key silently falls back to the default locale), and the format
// verbs must be identical across locales (a %v dropped in one translation
// would render a raw argument, or a %s added would consume a missing one).
func TestMCPAuthKeysExistInAllBuiltinLanguages(t *testing.T) {
	keys := []string{
		"tui.mcpAuth",
		"tui.mcpAuthStarting",
		"tui.mcpAuthURLLabel",
		"tui.mcpAuthInstructions",
		"tui.mcpAuthPlaceholder",
		"tui.mcpAuthHintEnter",
		"tui.mcpAuthHintCopy",
		"tui.mcpAuthHintClose",
		// The close hint is split by state: the cancel wording while the flow
		// is live, the mere-close wording once it is over.
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
		// The transport/probe sentinel: an unreachable server. Its engine text
		// is English, so it MUST have its own key or es/pt would show it.
		"tui.mcpAuthErrProbeFailed",
		// The paste parser refuses a denial redirect and an unreadable query
		// with messages of its own; the outcomes themselves come from the
		// engine (mcp.ParseAuthPaste), not from a TUI copy of the rule.
		"tui.mcpAuthRefused",
		"tui.mcpAuthRefusedUnknown",
		// Auth visibility in the list and the detail, and the sign out.
		"tui.mcpAuthRequired",
		"tui.mcpAuthOK",
		"tui.mcpAuthLogoutDone",
		"tui.mcpAuthLogoutNone",
		"tui.mcpAuthLogoutFailed",
		"tui.mcpAuthUnavailable",
		"tui.mcpDetailAuth",
		"tui.mcpDetailAuthState",
		"tui.mcpDetailNone",
		// The detail hint was extended to advertise the new key, and so was
		// the LIST hint ("D: sign out" between the toggle and the filter), so
		// both footers must carry it in every locale.
		"tui.mcpDetailHints",
		"tui.mcpListHints",
		"tui.mcpLogoutHint",
	}

	Init()
	langs := BuiltinLanguages()
	if len(langs) == 0 {
		t.Fatal("BuiltinLanguages() is empty")
	}
	// The three builtins must really be there, or this test would pass on an
	// empty iteration.
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

	// The raw per-locale maps: a key deleted from exactly one builtin file
	// would still resolve through the default-locale fallback, so presence is
	// asserted against the file itself, not against T().
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

// TestConnectPickerKeysExistInAllBuiltinLanguages covers the row-first
// /connect picker surface: the picker title, the two custom (bring-your-own
// endpoint) rows and the provider-name placeholder. Modelled on
// TestMCPAuthKeysExistInAllBuiltinLanguages — TestAllKeysExist only checks
// its own hardcoded list and would miss a key added to one locale and
// formatVerbs extracts the printf verbs of a translated string ("%s", "%v",
// …) in order, ignoring the literal text around them. Two locales agree for a
// key when their verb sequences match: a translation that reorders or drops a
// verb would panic (or print %!v(MISSING)) at render time.
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

// forgotten in the other two.
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

// TestConnectOAuthKeysExistInAllBuiltinLanguages covers the native /connect
// OAuth login surface (ModalConnectOAuth, rendered by connect_oauth.go): the
// three OAuth row labels and every inline/modal string the flow can render,
// from the authorization-URL instructions through each terminal sentinel
// error. Modelled on TestConnectPickerKeysExistInAllBuiltinLanguages so a key
// added to one locale and forgotten in the other two is caught here.
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
		// Reasons substituted into the tui.connectOAuthStartFailed template
		// when a picked row cannot be started at all (catalog/registry drift).
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

// TestMCPAddKeysExistInAllBuiltinLanguages covers the /mcp add wizard's
// surface: the wizard title, the per-slot labels and placeholders, the
// per-slot validation errors, the review warnings and hints, and the
// result-handler feedback lines. TestAllKeysExist only checks its own
// hardcoded list, so it would NOT catch a key added to one locale and
// forgotten in the other two — which is exactly the mistake this table of
// new keys is here to catch. Every key must be present VERBATIM in each
// builtin locale's JSON (T() alone cannot prove that: a missing key
// silently falls back to the default locale), and the format verbs must be
// identical across locales (a %s dropped in one translation would render a
// raw argument, or a %v added would consume a missing one). The two
// MODIFIED keys (mcpListHints, mcpNoServers) are listed too so a fragment
// dropped from one translation is caught per-file.
func TestMCPAddKeysExistInAllBuiltinLanguages(t *testing.T) {
	keys := []string{
		"tui.mcpAdd",
		"tui.mcpAddHint",
		// Slot labels.
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
		// Placeholders.
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
		// Per-slot validation errors.
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
		// Review warnings and hints.
		"tui.mcpAddReviewWarnReplace",
		"tui.mcpAddReviewWarnShadowed",
		"tui.mcpAddReviewNoteShadows",
		"tui.mcpAddReviewAuthHint",
		"tui.mcpAddHints",
		"tui.mcpAddReviewHints",
		// Result-handler feedback lines.
		"tui.mcpAddSuccess",
		"tui.mcpAddReplaced",
		"tui.mcpAddNoChange",
		"tui.mcpAddShadowSuffix",
		"tui.mcpAddFailed",
		"tui.mcpAddLayerNoPath",
		"tui.mcpAddPathNotAllowed",
		// The two MODIFIED keys: the "N" fragment and the empty-state row
		// advertise the wizard, so both must carry it in every locale.
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
