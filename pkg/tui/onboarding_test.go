package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"
)

func newOnboardingModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	m.onboardingActive = true
	m.onboardingStep = obWelcome
	m.obSkipConfirm = false
	m.showWelcome = true
	m.width = 80
	m.height = 40
	return m
}

func TestRenderWelcome_RendersContent(t *testing.T) {
	m := newOnboardingModel(t)
	progress := fmt.Sprintf(i18n.T("tui.onboard.progress"), 1, 6)

	out := m.renderOnboarding()

	if !strings.Contains(out, "|______|______|______|______|") {
		t.Fatalf("welcome missing logo:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.onboard.welcome")) {
		t.Fatalf("welcome missing welcome text:\n%s", out)
	}
	if !strings.Contains(out, progress) {
		t.Fatalf("welcome missing progress header %q:\n%s", progress, out)
	}
	if !strings.Contains(out, i18n.T("tui.onboard.pressEnter")) {
		t.Fatalf("welcome missing Enter hint:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.onboard.escSkip")) {
		t.Fatalf("welcome missing Esc hint:\n%s", out)
	}
}

func TestRenderWelcome_SkipConfirmOverlay(t *testing.T) {
	m := newOnboardingModel(t)
	m.obSkipConfirm = true

	out := m.renderObWelcome(m.width)

	if !strings.Contains(out, i18n.T("tui.onboard.skipConfirm")) {
		t.Fatalf("skip confirm missing prompt:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.onboard.skipYes")) {
		t.Fatalf("skip confirm missing 'Yes, skip' option:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.onboard.skipNo")) {
		t.Fatalf("skip confirm missing 'No, continue' option:\n%s", out)
	}
}

func TestRenderLang_RendersList(t *testing.T) {
	progress := fmt.Sprintf(i18n.T("tui.onboard.progress"), 2, 6)
	m := newOnboardingModel(t)
	m.onboardingStep = obLanguage
	m.modalSelectedIdx = 0

	out := m.renderOnboarding()

	if !strings.Contains(out, i18n.T("tui.onboard.language")) {
		t.Fatalf("lang missing title:\n%s", out)
	}
	if !strings.Contains(out, progress) {
		t.Fatalf("lang missing progress header %q:\n%s", progress, out)
	}
	for _, lang := range []string{"English", "Español", "Português"} {
		if !strings.Contains(out, lang) {
			t.Fatalf("lang missing %q:\n%s", lang, out)
		}
	}
}

func TestOnboard_EnterAdvancesToLanguage(t *testing.T) {
	m := newOnboardingModel(t)

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEnter})

	if updated.(*Model).onboardingStep != obLanguage {
		t.Fatalf("step = %v, want obLanguage", updated.(*Model).onboardingStep)
	}
}

func TestOnboard_EscShowsSkipConfirm(t *testing.T) {
	m := newOnboardingModel(t)

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEsc})

	mm := updated.(*Model)
	if !mm.obSkipConfirm {
		t.Fatal("obSkipConfirm not set after Esc")
	}
	if !mm.onboardingActive {
		t.Fatal("onboarding deactivated by Esc — should only show confirmation")
	}
	if mm.obSelectedPreset != 1 {
		t.Fatalf("obSelectedPreset = %d, want 1 (default No)", mm.obSelectedPreset)
	}
}

func TestOnboard_SkipConfirmYesExits(t *testing.T) {
	m := newOnboardingModel(t)
	m.obSkipConfirm = true
	m.obSelectedPreset = 0

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEnter})

	mm := updated.(*Model)
	if mm.onboardingActive {
		t.Fatal("onboarding still active after confirming skip")
	}
	if mm.obSkipConfirm {
		t.Fatal("obSkipConfirm not cleared after confirming skip")
	}
}

func TestOnboard_SkipConfirmNoDismisses(t *testing.T) {
	m := newOnboardingModel(t)
	m.obSkipConfirm = true
	m.obSelectedPreset = 1

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEnter})

	mm := updated.(*Model)
	if !mm.onboardingActive {
		t.Fatal("onboarding deactivated by 'No, continue'")
	}
	if mm.obSkipConfirm {
		t.Fatal("obSkipConfirm not cleared by 'No, continue'")
	}
}

func TestOnboard_SkipConfirmEscDismisses(t *testing.T) {
	m := newOnboardingModel(t)
	m.obSkipConfirm = true

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEsc})

	mm := updated.(*Model)
	if mm.obSkipConfirm {
		t.Fatal("obSkipConfirm not cleared by Esc")
	}
	if !mm.onboardingActive {
		t.Fatal("onboarding deactivated by Esc on confirmation")
	}
}

func TestOnboard_LanguageSelectSetsLang(t *testing.T) {
	i18n.InitWithLanguage("en")

	m := newOnboardingModel(t)
	m.onboardingStep = obLanguage
	m.modalSelectedIdx = 1

	before := i18n.GetLanguage()

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEnter})
	mm := updated.(*Model)

	if i18n.GetLanguage() != "es" {
		t.Fatalf("i18n language = %q, want es (from %q)", i18n.GetLanguage(), before)
	}
	if mm.cfg.Language != "es" {
		t.Fatalf("cfg.Language = %q, want es", mm.cfg.Language)
	}
	if mm.onboardingStep != obTheme {
		t.Fatalf("step = %v, want obTheme", mm.onboardingStep)
	}
}

func TestOnboard_LanguageEscGoesBack(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obLanguage

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEsc})
	mm := updated.(*Model)

	if mm.onboardingStep != obWelcome {
		t.Fatalf("step = %v, want obWelcome", mm.onboardingStep)
	}
	if mm.obSkipConfirm {
		t.Fatal("obSkipConfirm should be cleared going back to welcome")
	}
}

func TestOnboard_LanguageNavClamps(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obLanguage

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("up")})
	mm := updated.(*Model)
	if mm.modalSelectedIdx != 0 {
		t.Fatalf("idx after up at 0 = %d, want 0", mm.modalSelectedIdx)
	}

	mm = sendKeys(mm, "j", "j", "j")
	if mm.modalSelectedIdx != 2 {
		t.Fatalf("idx after 3 downs = %d, want 2", mm.modalSelectedIdx)
	}
	mm = sendKeys(mm, "j")
	if mm.modalSelectedIdx != 2 {
		t.Fatalf("idx after down past end = %d, want 2", mm.modalSelectedIdx)
	}
}

func TestRenderTheme_RendersList(t *testing.T) {
	progress := fmt.Sprintf(i18n.T("tui.onboard.progress"), 3, 6)
	m := newOnboardingModel(t)
	m.onboardingStep = obTheme
	m.modalSelectedIdx = 0

	out := m.renderOnboarding()

	if !strings.Contains(out, i18n.T("tui.onboard.theme")) {
		t.Fatalf("theme missing title:\n%s", out)
	}
	if !strings.Contains(out, progress) {
		t.Fatalf("theme missing progress header %q:\n%s", progress, out)
	}
	for _, name := range theme.Builtins() {
		if !strings.Contains(out, name) {
			t.Fatalf("theme missing built-in %q:\n%s", name, out)
		}
	}
	if !strings.Contains(out, i18n.T("tui.onboard.themeHint")) {
		t.Fatalf("theme missing nav hint line:\n%s", out)
	}
}

func TestOnboard_ThemeSelectAdvancesToProvider(t *testing.T) {
	m := newOnboardingModel(t)
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")
	if m.onboardingStep != obTheme {
		t.Fatalf("step = %v, want obTheme", m.onboardingStep)
	}
	m = sendKeys(m, "\r")
	if m.onboardingStep != obProviderPicker {
		t.Fatalf("theme Enter: step = %v, want obProviderPicker", m.onboardingStep)
	}
}

func TestOnboard_ThemeEscGoesBackToLanguage(t *testing.T) {
	m := newOnboardingModel(t)
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "down")
	m = sendKeys(m, "esc")
	if m.onboardingStep != obLanguage {
		t.Fatalf("theme Esc: step = %v, want obLanguage", m.onboardingStep)
	}
	if m.currentThemeName != "dracula" {
		t.Fatalf("theme should revert to dracula on Esc, got %q", m.currentThemeName)
	}
}

func TestOnboard_ThemeNavClamps(t *testing.T) {
	m := newOnboardingModel(t)
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")
	names := theme.Builtins()
	for i := 0; i < len(names)+2; i++ {
		m = sendKeys(m, "down")
	}
	m = sendKeys(m, "up")
	if m.onboardingStep != obTheme {
		t.Fatalf("step = %v, want obTheme", m.onboardingStep)
	}
}

func TestOnboard_ThemeAppliesAndPersists(t *testing.T) {
	m := newOnboardingModel(t)
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")
	names := theme.Builtins()
	nordIdx := 0
	for i, n := range names {
		if n == "nord" {
			nordIdx = i
			break
		}
	}
	for m.modalSelectedIdx < nordIdx {
		m = sendKeys(m, "down")
	}
	m = sendKeys(m, "\r")
	if m.currentThemeName != "nord" {
		t.Fatalf("currentThemeName = %q, want nord", m.currentThemeName)
	}
	if m.onboardingStep != obProviderPicker {
		t.Fatalf("step = %v, want obProviderPicker", m.onboardingStep)
	}
}

func TestOnboard_ThemePreviewAppliesLive(t *testing.T) {
	m := newOnboardingModel(t)
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "down")
	names := theme.Builtins()
	previewedName := names[m.modalSelectedIdx]
	if m.currentThemeName != "dracula" {
		t.Fatalf("preview should not update currentThemeName; got %q, want dracula", m.currentThemeName)
	}
	if m.themePreviewName != "dracula" {
		t.Fatalf("themePreviewName = %q, want dracula", m.themePreviewName)
	}
	_ = previewedName
	if previewedName == "dracula" {
		t.Log("note: down navigated onto dracula? check item index")
	}
	m = sendKeys(m, "esc")
	if m.onboardingStep != obLanguage {
		t.Fatalf("expected obLanguage after Esc, got %v", m.onboardingStep)
	}
	if m.currentThemeName != "dracula" {
		t.Fatalf("expected theme reverted to dracula, got %q", m.currentThemeName)
	}
}

func TestRenderProviderPicker_RendersList(t *testing.T) {
	m := newOnboardingModel(t)
	i18n.InitWithLanguage("en")
	progress := fmt.Sprintf(i18n.T("tui.onboard.progress"), 4, 6)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = 0

	out := m.renderOnboarding()

	if !strings.Contains(out, i18n.T("tui.onboard.pickProvider")) {
		t.Fatalf("provider picker missing title:\n%s", out)
	}
	if !strings.Contains(out, progress) {
		t.Fatalf("provider picker missing progress header %q:\n%s", progress, out)
	}
	for _, preset := range providerPresets {
		if !strings.Contains(out, preset.label) {
			t.Fatalf("provider picker missing preset %q:\n%s", preset.label, out)
		}
	}
	if !strings.Contains(out, i18n.T("tui.onboard.otherCustom")) {
		t.Fatalf("provider picker missing 'other / custom' entry:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.onboard.skipForNow")) {
		t.Fatalf("provider picker missing 'skip for now' entry:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.onboard.pickProviderHint")) {
		t.Fatalf("provider picker missing hint line:\n%s", out)
	}
}

func TestRenderProviderPicker_ShowsHints(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = 1

	out := m.renderObProviderPicker(m.width)

	anthropicHint := fmt.Sprintf(i18n.T("tui.onboard.keyFormat"), "sk-ant-...")
	if !strings.Contains(out, anthropicHint) {
		t.Fatalf("provider picker missing key-format hint %q:\n%s", anthropicHint, out)
	}
	if !strings.Contains(out, i18n.T("tui.onboard.noKeyNeeded")) {
		t.Fatalf("provider picker missing 'no API key needed' hint for Ollama:\n%s", out)
	}
}

func TestRenderProviderPicker_HighlightActive(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = 0

	out := m.renderObProviderPicker(m.width)

	if !strings.Contains(out, ModalItemActive.Render("> OpenAI")) {
		t.Fatalf("provider picker active row not highlighted:\n%s", out)
	}
}

func TestOnboard_ProviderPickerNavClamps(t *testing.T) {
	total := len(providerPresets) + 2
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker

	mm := sendKeys(m, "up")
	if mm.modalSelectedIdx != 0 {
		t.Fatalf("idx after up at 0 = %d, want 0", mm.modalSelectedIdx)
	}

	mm = m
	for i := 0; i < total; i++ {
		mm = sendKeys(mm, "j")
	}
	if mm.modalSelectedIdx != total-1 {
		t.Fatalf("idx after (total) downs = %d, want %d", mm.modalSelectedIdx, total-1)
	}
	mm = sendKeys(mm, "j")
	if mm.modalSelectedIdx != total-1 {
		t.Fatalf("idx after down past end = %d, want %d", mm.modalSelectedIdx, total-1)
	}
}

func TestOnboard_ProviderPickerSelectPreset(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = 2

	mm := sendKeys(m, "\r")
	if mm.onboardingStep != obConnect {
		t.Fatalf("step = %v, want obConnect", mm.onboardingStep)
	}
	if mm.obSelectedPreset != 2 {
		t.Fatalf("obSelectedPreset = %d, want 2", mm.obSelectedPreset)
	}
	if mm.modalSelectedIdx != 0 {
		t.Fatalf("modalSelectedIdx = %d, want reset to 0", mm.modalSelectedIdx)
	}
}

func TestOnboard_ProviderPickerSelectCustom(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = len(providerPresets)

	mm := sendKeys(m, "\r")
	if mm.onboardingStep != obConnect {
		t.Fatalf("step = %v, want obConnect", mm.onboardingStep)
	}
	if mm.obSelectedPreset != -1 {
		t.Fatalf("obSelectedPreset = %d, want -1 (custom)", mm.obSelectedPreset)
	}
}

func TestOnboard_ProviderPickerSkip(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = len(providerPresets) + 1

	mm := sendKeys(m, "\r")
	if mm.onboardingActive {
		t.Fatal("onboarding still active after 'Skip for now'")
	}
}

func TestOnboard_ProviderPickerEscGoesBack(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = 3

	mm := sendKeys(m, "\x1b")
	if mm.onboardingStep != obTheme {
		t.Fatalf("step = %v, want obTheme", mm.onboardingStep)
	}
	expected := 0
	names := theme.Builtins()
	for i, n := range names {
		if n == mm.currentThemeName {
			expected = i
			break
		}
	}
	if mm.modalSelectedIdx != expected {
		t.Fatalf("modalSelectedIdx = %d, want restored to %d", mm.modalSelectedIdx, expected)
	}
}

func onProviderPicker(t *testing.T, idx int) *Model {
	t.Helper()
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = idx
	return m
}

func TestConnect_PresetPrefill(t *testing.T) {
	preset := &providerPresets[0]
	m := onProviderPicker(t, 0)
	mm := sendKeys(m, "\r")

	if mm.onboardingStep != obConnect {
		t.Fatalf("step = %v, want obConnect", mm.onboardingStep)
	}
	if mm.modalMode != ModalAddProvider {
		t.Fatalf("modal = %v, want ModalAddProvider", mm.modalMode)
	}
	if mm.formStepIndex != 2 {
		t.Fatalf("formStepIndex = %d, want 2 (API Key step)", mm.formStepIndex)
	}
	if mm.formValues[0] != preset.label {
		t.Fatalf("provider name = %q, want %q", mm.formValues[0], preset.label)
	}
	if mm.formValues[1] != preset.typ {
		t.Fatalf("provider type = %q, want %q", mm.formValues[1], preset.typ)
	}
	if mm.formValues[3] != preset.apiBase {
		t.Fatalf("API base = %q, want %q", mm.formValues[3], preset.apiBase)
	}
	if !mm.providerTypeFromPreset {
		t.Fatal("providerTypeFromPreset should be true")
	}
	if mm.providerSavedInFlow {
		t.Fatal("providerSavedInFlow should be false at start")
	}
}

func TestConnect_OllamaSkipsAPIKey(t *testing.T) {
	idx := -1
	for i := range providerPresets {
		if providerPresets[i].typ == "ollama" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("ollama preset not found")
	}
	m := onProviderPicker(t, idx)
	mm := sendKeys(m, "\r")

	if mm.formStepIndex != 3 {
		t.Fatalf("formStepIndex = %d, want 3 (skip API Key for ollama)", mm.formStepIndex)
	}
	if mm.formValues[1] != "ollama" {
		t.Fatalf("type = %q, want ollama", mm.formValues[1])
	}
	if !strings.HasPrefix(mm.textInput.Value(), "http://localhost:11434") {
		t.Fatalf("api base not pre-filled for ollama: %q", mm.textInput.Value())
	}
}

func TestConnect_CustomPathNoPrefill(t *testing.T) {
	m := onProviderPicker(t, len(providerPresets))
	mm := sendKeys(m, "\r")

	if mm.obSelectedPreset != -1 {
		t.Fatalf("obSelectedPreset = %d, want -1 (custom)", mm.obSelectedPreset)
	}
	if mm.formStepIndex != 0 {
		t.Fatalf("formStepIndex = %d, want 0 for custom path", mm.formStepIndex)
	}
	if mm.formValues[0] != "" {
		t.Fatalf("provider name should be empty for custom, got %q", mm.formValues[0])
	}
}

func TestConnect_PresetModelPrefillAndCompleteToVerify(t *testing.T) {
	m := onProviderPicker(t, 0)
	mm := sendKeys(m, "\r")
	if mm.formStepIndex != 2 {
		t.Fatalf("start step = %d, want 2", mm.formStepIndex)
	}

	mm.textInput.SetValue("sk-test")
	mm = sendKeys(mm, "\r")
	if mm.formStepIndex != 3 {
		t.Fatalf("after key, step = %d, want 3", mm.formStepIndex)
	}
	mm = sendKeys(mm, "\r")
	if mm.formStepIndex != 4 {
		t.Fatalf("after base, step = %d, want 4", mm.formStepIndex)
	}
	if mm.formValues[0] != providerPresets[0].label {
		t.Fatalf("provider name = %q, want %q", mm.formValues[0], providerPresets[0].label)
	}

	if mm.formValues[4] != providerPresets[0].defaultModelAlias {
		t.Fatalf("model alias prefill = %q, want %q", mm.formValues[4], providerPresets[0].defaultModelAlias)
	}
	if mm.formValues[5] != providerPresets[0].defaultModel {
		t.Fatalf("model name prefill = %q, want %q", mm.formValues[5], providerPresets[0].defaultModel)
	}

	for i := 0; i < 7; i++ {
		mm = sendKeys(mm, "\r")
	}

	if mm.onboardingStep != obVerify {
		t.Fatalf("after complete, step = %v, want obVerify", mm.onboardingStep)
	}
	if !mm.onboardingActive {
		t.Fatal("onboarding should still be active after connect, routed to verify")
	}
	if mm.modalMode != ModalNone {
		t.Fatalf("modal should be closed after onboarding connect, got %v", mm.modalMode)
	}
	if mm.connectSuccess {
		t.Fatal("connectSuccess should not be shown in onboarding (routes to verify)")
	}
	if mm.providerSavedInFlow {
		t.Fatal("providerSavedInFlow should be reset after routing to verify")
	}

	key := strings.ToLower(providerPresets[0].label)
	if mm.cfg == nil || mm.cfg.Providers == nil || mm.cfg.Providers.Named == nil {
		t.Fatal("providers config nil after save")
	}
	p, ok := mm.cfg.Providers.Named[key]
	if !ok {
		t.Fatalf("provider %q not persisted", key)
	}
	if p.Type != providerPresets[0].typ {
		t.Fatalf("type = %q, want %q", p.Type, providerPresets[0].typ)
	}
	if len(p.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(p.Models))
	}
	if mc, ok := p.Models[providerPresets[0].defaultModelAlias]; !ok || mc.Model != providerPresets[0].defaultModel {
		t.Fatalf("model config mismatch: %+v (alias %q)", mc, providerPresets[0].defaultModelAlias)
	}
}

func TestConnect_EscDuringConnectReturnsToPicker(t *testing.T) {
	m := onProviderPicker(t, 0)
	mm := sendKeys(m, "\r")
	if mm.onboardingStep != obConnect {
		t.Fatalf("step = %v, want obConnect", mm.onboardingStep)
	}

	mm = sendKeys(mm, "\x1b")
	if mm.onboardingStep != obProviderPicker {
		t.Fatalf("after Esc step = %v, want obProviderPicker", mm.onboardingStep)
	}
	if !mm.onboardingActive {
		t.Fatal("onboarding deactivated by Esc on connect")
	}
	if mm.modalMode != ModalNone {
		t.Fatalf("modal = %v, want ModalNone after Esc", mm.modalMode)
	}
}

func TestRenderObConnect_RendersProgressAndForm(t *testing.T) {
	m := onProviderPicker(t, 0)
	mm := sendKeys(m, "\r")
	progress := fmt.Sprintf(i18n.T("tui.onboard.progress"), 5, 6)

	out := mm.renderOnboarding()
	if !strings.Contains(out, progress) {
		t.Fatalf("obConnect missing progress header %q:\n%s", progress, out)
	}
	if strings.Contains(out, "Coming soon...") {
		t.Fatalf("obConnect still renders placeholder:\n%s", out)
	}
	if !strings.Contains(out, "OpenAI") {
		t.Fatalf("obConnect missing pre-filled provider name:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.connectReview")) {
		t.Fatalf("obConnect missing form steps:\n%s", out)
	}
}

func obFinalizedModel(t *testing.T) *Model {
	t.Helper()
	m := newOnboardingModel(t)
	m.onboardingStep = obVerify
	m.onboardingActive = true
	m.cfg.Agents.Defaults.Provider = ""
	m.cfg.Agents.Defaults.Model = ""
	prov := config.NamedProviderConfig{
		Type: "openai",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "sk-test-key-1234",
			APIBase: "https://api.openai.com/v1",
		},
		Models: map[string]config.ProviderModelConfig{
			"gpt-4o": {Model: "gpt-4o-2024-08-06"},
		},
	}
	if m.cfg.Providers == nil {
		m.cfg.Providers = &config.ProvidersConfig{}
	}
	m.cfg.Providers.Named["openai"] = prov
	return m
}

func TestOnboard_DefaultsOverrideSeededPlaceholders(t *testing.T) {
	m := obFinalizedModel(t)
	m.cfg.Agents.Defaults.Provider = "openrouter"
	m.cfg.Agents.Defaults.Model = "deepseek-v4-pro"

	m.obFinalizeSetup()

	if m.cfg.Agents.Defaults.Provider != "openai" {
		t.Fatalf("defaults.Provider = %q, want openai (override seeded placeholder)", m.cfg.Agents.Defaults.Provider)
	}
	if m.cfg.Agents.Defaults.Model != "gpt-4o" {
		t.Fatalf("defaults.Model = %q, want gpt-4o", m.cfg.Agents.Defaults.Model)
	}
	if !m.cfg.HasUsableProvider() {
		t.Fatal("config should have a usable provider after finalize")
	}
}

func TestOnboard_DefaultsPreservedWhenUsable(t *testing.T) {
	m := obFinalizedModel(t)
	m.cfg.Providers.Named["ollama"] = config.NamedProviderConfig{
		Type: "ollama",
		ProviderConfig: config.ProviderConfig{
			APIBase: "http://localhost:11434",
		},
		Models: map[string]config.ProviderModelConfig{
			"llama3": {Model: "llama3.2"},
		},
	}
	m.cfg.Agents.Defaults.Provider = "ollama"
	m.cfg.Agents.Defaults.Model = "llama3"
	m.providerSelectedName = "openai"

	m.obFinalizeSetup()

	if m.cfg.Agents.Defaults.Provider != "ollama" {
		t.Fatalf("defaults.Provider = %q, want ollama preserved (usable default)", m.cfg.Agents.Defaults.Provider)
	}
	if m.cfg.Agents.Defaults.Model != "llama3" {
		t.Fatalf("defaults.Model = %q, want llama3 preserved", m.cfg.Agents.Defaults.Model)
	}
	if m.obProviderName != "openai" {
		t.Fatalf("obProviderName = %q, want openai", m.obProviderName)
	}
}

func TestOnboard_VerifySuccessSetsDoneAndDefaults(t *testing.T) {
	m := obFinalizedModel(t)
	m.obVerifying = true

	updated, _ := m.Update(obVerifyResultMsg{success: true, providerName: "openai"})
	mm := updated.(*Model)

	if mm.onboardingStep != obDone {
		t.Fatalf("step = %v, want obDone", mm.onboardingStep)
	}
	if mm.obVerifying {
		t.Fatal("obVerifying should be false after result")
	}
	if mm.obVerifyFailed {
		t.Fatal("obVerifyFailed should be false on success")
	}
	if mm.cfg.Agents.Defaults.Provider != "openai" {
		t.Fatalf("defaults.Provider = %q, want openai", mm.cfg.Agents.Defaults.Provider)
	}
	if mm.cfg.Agents.Defaults.Model != "gpt-4o" {
		t.Fatalf("defaults.Model = %q, want gpt-4o", mm.cfg.Agents.Defaults.Model)
	}
	if mm.obProviderName != "openai" {
		t.Fatalf("obProviderName = %q, want openai", mm.obProviderName)
	}
	if mm.obModelName != "gpt-4o" {
		t.Fatalf("obModelName = %q, want gpt-4o", mm.obModelName)
	}
	if mm.obMaskedKey != "sk-t...1234" {
		t.Fatalf("obMaskedKey = %q, want sk-t...1234", mm.obMaskedKey)
	}
}

func TestOnboard_VerifyFailureShowsWarning(t *testing.T) {
	m := obFinalizedModel(t)
	m.obVerifying = true

	updated, _ := m.Update(obVerifyResultMsg{success: false, providerName: "openai"})
	mm := updated.(*Model)

	if mm.onboardingStep != obDone {
		t.Fatalf("step = %v, want obDone", mm.onboardingStep)
	}
	if !mm.obVerifyFailed {
		t.Fatal("obVerifyFailed should be true on failed verification")
	}
	if mm.onboardingActive != true {
		t.Fatal("onboarding should remain active on verify failure (non-blocking)")
	}
	if mm.cfg.Agents.Defaults.Provider != "openai" {
		t.Fatalf("defaults.Provider = %q, want openai despite failure", mm.cfg.Agents.Defaults.Provider)
	}
	out := mm.renderObDone(mm.width)
	if !strings.Contains(out, i18n.T("tui.onboard.verifyFailed")) {
		t.Fatalf("done screen missing verify warning:\n%s", out)
	}
}

func TestOnboard_VerifyEscSkipsAhead(t *testing.T) {
	m := obFinalizedModel(t)
	m.obVerifying = true

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEsc})
	mm := updated.(*Model)

	if mm.onboardingStep != obDone {
		t.Fatalf("step = %v, want obDone", mm.onboardingStep)
	}
	if mm.obVerifying {
		t.Fatal("obVerifying should be false after Esc skip")
	}
	if mm.cfg.Agents.Defaults.Provider != "openai" {
		t.Fatalf("defaults.Provider = %q, want openai after Esc skip", mm.cfg.Agents.Defaults.Provider)
	}
	out := mm.renderObVerify(mm.width)
	if strings.Contains(out, i18n.T("tui.onboard.verifyFailed")) {
		t.Fatalf("skipped verify should not show failure warning:\n%s", out)
	}
}

func TestOnboard_DoneEnterClearsOnboarding(t *testing.T) {
	m := obFinalizedModel(t)
	m.onboardingStep = obDone
	m.showWelcome = true

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEnter})
	mm := updated.(*Model)

	if mm.onboardingActive {
		t.Fatal("onboarding should be cleared after Enter on done screen")
	}
	if !mm.showWelcome {
		t.Fatal("showWelcome should be true after Enter on done screen (return to welcome)")
	}
	if !mm.chatInput.Focused() {
		t.Fatal("chat input should be focused after Enter on done screen")
	}
}

func TestOnboard_DoneRendersSuccessSummary(t *testing.T) {
	m := obFinalizedModel(t)
	m.onboardingStep = obDone
	m.obFinalizeSetup()

	out := m.renderObDone(m.width)

	if !strings.Contains(out, i18n.T("tui.onboard.done")) {
		t.Fatalf("done screen missing done message:\n%s", out)
	}
	if !strings.Contains(out, "openai") {
		t.Fatalf("done screen missing provider name:\n%s", out)
	}
	if !strings.Contains(out, "gpt-4o") {
		t.Fatalf("done screen missing model alias:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.onboard.tipSend")) {
		t.Fatalf("done screen missing tip line:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.onboard.pressEnterStart")) {
		t.Fatalf("done screen missing press-enter-start hint:\n%s", out)
	}
	progress := fmt.Sprintf(i18n.T("tui.onboard.progress"), 6, 6)
	if !strings.Contains(out, progress) {
		t.Fatalf("done screen missing progress header %q:\n%s", progress, out)
	}
}

func TestOnboard_VerifyRendersSpinner(t *testing.T) {
	m := obFinalizedModel(t)
	m.obVerifying = true

	out := m.renderObVerify(m.width)
	if !strings.Contains(out, i18n.T("tui.onboard.verifying")) {
		t.Fatalf("verify screen missing verifying message:\n%s", out)
	}
	if !strings.Contains(out, "[") || !strings.Contains(out, "]") {
		t.Fatalf("verify screen missing spinner:\n%s", out)
	}
}
func TestOnboard_VerifyToDoneIntegration(t *testing.T) {
	m := onProviderPicker(t, 0)
	mm := sendKeys(m, "\r")
	if mm.onboardingStep != obConnect {
		t.Fatalf("step = %v, want obConnect", mm.onboardingStep)
	}

	mm.textInput.SetValue("sk-test-abcdef")
	mm = sendKeys(mm, "\r")
	mm = sendKeys(mm, "\r")
	for i := 0; i < 7; i++ {
		mm = sendKeys(mm, "\r")
	}

	if mm.onboardingStep != obVerify {
		t.Fatalf("step = %v, want obVerify", mm.onboardingStep)
	}
	if !mm.obVerifying {
		t.Fatal("obVerifying should be true on entering verify step")
	}
	if mm.cfg.Agents.Defaults.Provider == "" {
		t.Fatal("defaults.Provider not set entering verify")
	}
	if mm.cfg.Agents.Defaults.Model == "" {
		t.Fatal("defaults.Model not set entering verify")
	}

	if !strings.Contains(mm.renderObVerify(mm.width), i18n.T("tui.onboard.verifying")) {
		t.Fatal("verify screen missing verifying message")
	}

	updated, _ := mm.Update(obVerifyResultMsg{success: true, providerName: mm.cfg.Agents.Defaults.Provider})
	out := updated.(*Model)

	if out.onboardingStep != obDone {
		t.Fatalf("step = %v, want obDone after verify result", out.onboardingStep)
	}
	if out.obVerifying {
		t.Fatal("obVerifying should be false after result")
	}
	if !strings.Contains(out.renderObDone(out.width), i18n.T("tui.onboard.pressEnterStart")) {
		t.Fatal("done screen missing press-enter-start hint")
	}

	done := sendKeys(out, "\r")
	if done.onboardingActive {
		t.Fatal("onboarding still active after Enter on done screen")
	}
	if !done.showWelcome {
		t.Fatal("showWelcome should be true after Enter on done screen (return to welcome)")
	}
	if !done.chatInput.Focused() {
		t.Fatal("chat input should be focused after Enter on done screen")
	}
}

func TestOnboard_NarrowWidths(t *testing.T) {
	for _, w := range []int{80, 50, 40, 30, 20} {
		m := newOnboardingModel(t)
		m.width = w
		m.height = 40

		steps := map[string]func(){
			"renderObWelcome":        func() { m.renderObWelcome(w) },
			"renderObLanguage":       func() { m.renderObLanguage(w) },
			"renderObProviderPicker": func() { m.renderObProviderPicker(w) },
			"renderObVerify":         func() { m.renderObVerify(w) },
			"renderObDone":           func() { m.renderObDone(w) },
		}
		for name, fn := range steps {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("width %d: %s panicked: %v", w, name, r)
					}
				}()
				fn()
			}()
		}
	}
}

func TestOnboarding_FreshConfigTriggersOnboarding(t *testing.T) {
	m := newOnboardingTestModel(t)
	if !m.onboardingActive {
		t.Fatal("expected onboardingActive for fresh config")
	}
	if m.onboardingStep != obWelcome {
		t.Fatalf("expected obWelcome, got %d", m.onboardingStep)
	}
}

func TestOnboarding_ConfigWithProvidersNoOnboarding(t *testing.T) {
	m := newConfiguredOnboardingTestModel(t)
	if m.onboardingActive {
		t.Fatal("expected onboarding NOT active for configured provider")
	}
}

func TestOnboarding_FullHappyPath(t *testing.T) {
	m := newOnboardingTestModel(t)

	m = sendKeys(m, "\r")
	if m.onboardingStep != obLanguage {
		t.Fatalf("welcome Enter: step = %v, want obLanguage", m.onboardingStep)
	}
	m = sendKeys(m, "\r")
	if m.onboardingStep != obTheme {
		t.Fatalf("lang Enter: step = %v, want obTheme", m.onboardingStep)
	}
	m = sendKeys(m, "\r")
	if m.onboardingStep != obProviderPicker {
		t.Fatalf("theme Enter: step = %v, want obProviderPicker", m.onboardingStep)
	}
	m = sendKeys(m, "\r")
	if m.onboardingStep != obConnect {
		t.Fatalf("picker Enter: step = %v, want obConnect", m.onboardingStep)
	}
	if m.formStepIndex != 2 {
		t.Fatalf("expect connect at API Key step, got %d", m.formStepIndex)
	}

	m.textInput.SetValue("sk-test-abcdef")
	m = sendKeys(m, "\r")
	if m.formStepIndex != 3 {
		t.Fatalf("after key step = %d, want 3", m.formStepIndex)
	}
	m = sendKeys(m, "\r")
	for i := 0; i < 7; i++ {
		m = sendKeys(m, "\r")
	}

	if m.onboardingStep != obVerify {
		t.Fatalf("step = %v, want obVerify", m.onboardingStep)
	}
	if !m.obVerifying {
		t.Fatal("obVerifying should be true entering verify")
	}

	out, _ := m.Update(obVerifyResultMsg{success: true, providerName: m.cfg.Agents.Defaults.Provider})
	m = out.(*Model)
	if m.onboardingStep != obDone {
		t.Fatalf("step = %v, want obDone", m.onboardingStep)
	}
	if m.obVerifying {
		t.Fatal("obVerifying should be false after result")
	}

	m = sendKeys(m, "\r")
	if m.onboardingActive {
		t.Fatal("onboarding should be inactive after done Enter")
	}
	if !m.showWelcome {
		t.Fatal("showWelcome should be true after done Enter (return to welcome)")
	}
	if !m.chatInput.Focused() {
		t.Fatal("chat input should be focused after done Enter")
	}
}

func TestOnboarding_SkipPath(t *testing.T) {
	m := newOnboardingTestModel(t)

	m = sendKeys(m, "esc")
	if !m.obSkipConfirm {
		t.Fatal("obSkipConfirm should be set after Esc")
	}
	m = sendKeys(m, "up")
	if m.obSelectedPreset != 0 {
		t.Fatalf("obSelectedPreset = %d, want 0 (Yes, skip)", m.obSelectedPreset)
	}
	m = sendKeys(m, "enter")
	if m.onboardingActive {
		t.Fatal("onboarding should be inactive after confirming skip")
	}
	if m.obSkipConfirm {
		t.Fatal("obSkipConfirm should be cleared after confirming skip")
	}
}

func TestOnboarding_LanguageChangePersists(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := newOnboardingTestModel(t)

	m = sendKeys(m, "\r")
	m = sendKeys(m, "down")
	m = sendKeys(m, "\r")
	if m.cfg.Language != "es" {
		t.Fatalf("expected language 'es', got '%s'", m.cfg.Language)
	}
	if i18n.GetLanguage() != "es" {
		t.Fatalf("i18n language = %q, want es", i18n.GetLanguage())
	}
	if m.onboardingStep != obTheme {
		t.Fatalf("after lang confirm step = %v, want obTheme", m.onboardingStep)
	}
}

func TestOnboarding_ProviderSkipExits(t *testing.T) {
	m := newOnboardingTestModel(t)

	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")
	m = sendKeys(m, "\r")
	total := len(providerPresets) + 2
	for i := 0; i < total; i++ {
		m = sendKeys(m, "down")
	}
	m = sendKeys(m, "\r")
	if m.onboardingActive {
		t.Fatal("expected onboarding to be inactive after 'Skip for now'")
	}
	if m.modalMode != ModalNone {
		t.Fatalf("modal = %v, want ModalNone after skip", m.modalMode)
	}
}

func TestOnboarding_VerifyFailureAllowsProceeding(t *testing.T) {
	m := newOnboardingTestModel(t)
	m.onboardingStep = obVerify
	m.obVerifying = true
	m.cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			ProviderConfig: config.ProviderConfig{
				APIKey:  "sk-test-abcdef",
				APIBase: "https://api.openai.com/v1",
			},
			Models: map[string]config.ProviderModelConfig{
				"gpt-4o": {Model: "gpt-4o"},
			},
		},
	}

	updated, _ := m.Update(obVerifyResultMsg{success: false, providerName: "openai"})
	mm := updated.(*Model)

	if mm.onboardingStep != obDone {
		t.Fatalf("step = %v, want obDone after failed verify", mm.onboardingStep)
	}
	if !mm.obVerifyFailed {
		t.Fatal("obVerifyFailed should be true on failed verification")
	}
	if !mm.onboardingActive {
		t.Fatal("onboarding should remain active on verify failure (non-blocking)")
	}
	if !strings.Contains(mm.renderObDone(mm.width), i18n.T("tui.onboard.verifyFailed")) {
		t.Fatal("done screen missing verify warning")
	}
	mm = sendKeys(mm, "\r")
	if mm.onboardingActive {
		t.Fatal("onboarding should clear on Enter despite failed verification")
	}
}

func TestOnboarding_DoneEnterClearsOnboarding(t *testing.T) {
	m := newOnboardingTestModel(t)
	m.onboardingStep = obDone
	m.showWelcome = true

	m = sendKeys(m, "\r")
	if m.onboardingActive {
		t.Fatal("onboardingActive should be false after Enter on done")
	}
	if !m.showWelcome {
		t.Fatal("showWelcome should be true after Enter on done (return to welcome)")
	}
}
