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

// newOnboardingModel returns a test model in the onboarding welcome step,
// mimicking what NewModel does on a true first run.
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

// TestRenderWelcome_RendersContent verifies the welcome step draws the logo,
// the localized welcome text, progress dots (step 1 of 6) and the hints.
func TestRenderWelcome_RendersContent(t *testing.T) {
	m := newOnboardingModel(t)
	progress := fmt.Sprintf(i18n.T("tui.onboard.progress"), 1, 6)

	out := m.renderOnboarding()

	// Logo block (a distinctive line of the ASCII art).
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

// TestRenderWelcome_SkipConfirmOverlay verifies the skip confirmation overlays
// the welcome screen when obSkipConfirm is set.
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

// TestRenderLang_RendersList verifies the language step shows the title,
// progress step 2/6 and all three languages.
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

// TestOnboard_EnterAdvancesToLanguage verifies Enter on the welcome step
// transitions to the language picker.
func TestOnboard_EnterAdvancesToLanguage(t *testing.T) {
	m := newOnboardingModel(t)

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEnter})

	if updated.(*Model).onboardingStep != obLanguage {
		t.Fatalf("step = %v, want obLanguage", updated.(*Model).onboardingStep)
	}
}

// TestOnboard_EscShowsSkipConfirm verifies Esc on the welcome step opens the
// skip confirmation (and does NOT quit / deactivate onboarding).
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
	// Default selection is "No, continue" (1).
	if mm.obSelectedPreset != 1 {
		t.Fatalf("obSelectedPreset = %d, want 1 (default No)", mm.obSelectedPreset)
	}
}

// TestOnboard_SkipConfirmYesExits verifies choosing "Yes, skip" deactivates
// onboarding.
func TestOnboard_SkipConfirmYesExits(t *testing.T) {
	m := newOnboardingModel(t)
	m.obSkipConfirm = true
	m.obSelectedPreset = 0 // Yes, skip

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEnter})

	mm := updated.(*Model)
	if mm.onboardingActive {
		t.Fatal("onboarding still active after confirming skip")
	}
	if mm.obSkipConfirm {
		t.Fatal("obSkipConfirm not cleared after confirming skip")
	}
}

// TestOnboard_SkipConfirmNoDismisses verifies choosing "No, continue" just
// dismisses the confirmation and stays in onboarding.
func TestOnboard_SkipConfirmNoDismisses(t *testing.T) {
	m := newOnboardingModel(t)
	m.obSkipConfirm = true
	m.obSelectedPreset = 1 // No, continue

	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyEnter})

	mm := updated.(*Model)
	if !mm.onboardingActive {
		t.Fatal("onboarding deactivated by 'No, continue'")
	}
	if mm.obSkipConfirm {
		t.Fatal("obSkipConfirm not cleared by 'No, continue'")
	}
}

// TestOnboard_SkipConfirmEscDismisses verifies Esc on the confirmation dismisses
// it without skipping.
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

// TestOnboard_LanguageSelectSetsLang verifies Enter on a language calls
// i18n.SetLanguage, persists it to cfg.Language and advances to theme picker.
func TestOnboard_LanguageSelectSetsLang(t *testing.T) {
	// Ensure a known initial language to detect a real change below.
	i18n.InitWithLanguage("en")

	m := newOnboardingModel(t)
	m.onboardingStep = obLanguage
	m.modalSelectedIdx = 1 // Español (es)

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

// TestOnboard_LanguageEscGoesBack verifies Esc on the language step returns to
// the welcome step.
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

// TestOnboard_LanguageNavClamps verifies up/down navigation clamps to the
// bounds of the language list.
func TestOnboard_LanguageNavClamps(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obLanguage

	// At 0, pressing up stays at 0.
	updated, _ := m.handleOnboardingKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("up")})
	// key message type: use runes; handleOnboardingKey switches on String()
	mm := updated.(*Model)
	if mm.modalSelectedIdx != 0 {
		t.Fatalf("idx after up at 0 = %d, want 0", mm.modalSelectedIdx)
	}

	// Move down, down, down → clamps at 2 (last).
	mm = sendKeys(mm, "j", "j", "j")
	if mm.modalSelectedIdx != 2 {
		t.Fatalf("idx after 3 downs = %d, want 2", mm.modalSelectedIdx)
	}
	mm = sendKeys(mm, "j")
	if mm.modalSelectedIdx != 2 {
		t.Fatalf("idx after down past end = %d, want 2", mm.modalSelectedIdx)
	}
}

// TestRenderTheme_RendersList verifies the theme step shows the title, the
// progress header (step 3/6), every built-in theme name, and the nav hint.
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

// TestOnboard_ThemeSelectAdvancesToProvider verifies Enter on the theme step
// applies the selected theme and advances to the provider picker.
func TestOnboard_ThemeSelectAdvancesToProvider(t *testing.T) {
	m := newOnboardingModel(t)
	m = sendKeys(m, "\r") // welcome → language
	m = sendKeys(m, "\r") // language → theme
	if m.onboardingStep != obTheme {
		t.Fatalf("step = %v, want obTheme", m.onboardingStep)
	}
	m = sendKeys(m, "\r") // theme → provider picker
	if m.onboardingStep != obProviderPicker {
		t.Fatalf("theme Enter: step = %v, want obProviderPicker", m.onboardingStep)
	}
}

// TestOnboard_ThemeEscGoesBackToLanguage verifies Esc on the theme step returns
// to the language step.
func TestOnboard_ThemeEscGoesBackToLanguage(t *testing.T) {
	m := newOnboardingModel(t)
	m = sendKeys(m, "\r") // welcome → language
	m = sendKeys(m, "\r") // language → theme
	m = sendKeys(m, "esc") // theme → language
	if m.onboardingStep != obLanguage {
		t.Fatalf("theme Esc: step = %v, want obLanguage", m.onboardingStep)
	}
}

// TestOnboard_ThemeNavClamps verifies up/down navigation on the theme step
// clamps to the bounds of the built-in theme list.
func TestOnboard_ThemeNavClamps(t *testing.T) {
	m := newOnboardingModel(t)
	m = sendKeys(m, "\r") // welcome → language
	m = sendKeys(m, "\r") // language → theme
	// Try to go past the last item
	names := theme.Builtins()
	for i := 0; i < len(names)+2; i++ {
		m = sendKeys(m, "down")
	}
	// Should be clamped at last item
	m = sendKeys(m, "up")
	// No crash, step unchanged
	if m.onboardingStep != obTheme {
		t.Fatalf("step = %v, want obTheme", m.onboardingStep)
	}
}

// TestOnboard_ThemeAppliesAndPersists verifies selecting a non-default theme
// applies it (updating currentThemeName) and advances to the provider picker.
func TestOnboard_ThemeAppliesAndPersists(t *testing.T) {
	m := newOnboardingModel(t)
	m = sendKeys(m, "\r") // welcome → language
	m = sendKeys(m, "\r") // language → theme
	// Find nord index in sorted builtins
	names := theme.Builtins()
	nordIdx := 0
	for i, n := range names {
		if n == "nord" {
			nordIdx = i
			break
		}
	}
	// Navigate to nord from the current position (pre-selected to dracula)
	for m.modalSelectedIdx < nordIdx {
		m = sendKeys(m, "down")
	}
	m = sendKeys(m, "\r") // select nord
	if m.currentThemeName != "nord" {
		t.Fatalf("currentThemeName = %q, want nord", m.currentThemeName)
	}
	if m.onboardingStep != obProviderPicker {
		t.Fatalf("step = %v, want obProviderPicker", m.onboardingStep)
	}
}

// TestRenderProviderPicker_RendersList verifies the provider picker step shows
// the title, progress step 4/6, every preset label, the "other / custom" entry,
// the "Skip for now" entry and the navigation hint.
func TestRenderProviderPicker_RendersList(t *testing.T) {
	m := newOnboardingModel(t)
	i18n.InitWithLanguage("en") // reset to English (NewModel may have set it to the config default)
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

// TestRenderProviderPicker_ShowsHints verifies presets show a key-format hint
// and the Ollama local preset shows the "no API key needed" hint instead.
func TestRenderProviderPicker_ShowsHints(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = 1 // highlight Anthropic

	out := m.renderObProviderPicker(m.width)

	// Anthropic row should include its key format hint.
	anthropicHint := fmt.Sprintf(i18n.T("tui.onboard.keyFormat"), "sk-ant-...")
	if !strings.Contains(out, anthropicHint) {
		t.Fatalf("provider picker missing key-format hint %q:\n%s", anthropicHint, out)
	}
	// Ollama (local) should show the no-key hint instead of a key format.
	if !strings.Contains(out, i18n.T("tui.onboard.noKeyNeeded")) {
		t.Fatalf("provider picker missing 'no API key needed' hint for Ollama:\n%s", out)
	}
}

// TestRenderProviderPicker_HighlightActive verifies the rendered active row is
// wrapped in ModalItemActive style.
func TestRenderProviderPicker_HighlightActive(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = 0 // highlight OpenAI (first preset)

	out := m.renderObProviderPicker(m.width)

	if !strings.Contains(out, ModalItemActive.Render("> OpenAI")) {
		t.Fatalf("provider picker active row not highlighted:\n%s", out)
	}
}

// TestOnboard_ProviderPickerNavClamps verifies up/down navigation clamps to
// the bounds of the provider list (presets + other + skip).
func TestOnboard_ProviderPickerNavClamps(t *testing.T) {
	total := len(providerPresets) + 2
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker

	// At 0, pressing up stays at 0.
	mm := sendKeys(m, "up")
	if mm.modalSelectedIdx != 0 {
		t.Fatalf("idx after up at 0 = %d, want 0", mm.modalSelectedIdx)
	}

	// Move down to the last item.
	mm = m
	for i := 0; i < total; i++ {
		mm = sendKeys(mm, "j")
	}
	if mm.modalSelectedIdx != total-1 {
		t.Fatalf("idx after (total) downs = %d, want %d", mm.modalSelectedIdx, total-1)
	}
	// Pressing down past the end stays clamped.
	mm = sendKeys(mm, "j")
	if mm.modalSelectedIdx != total-1 {
		t.Fatalf("idx after down past end = %d, want %d", mm.modalSelectedIdx, total-1)
	}
}

// TestOnboard_ProviderPickerSelectPreset verifies Enter on a preset advances to
// the connect step and records the preset index.
func TestOnboard_ProviderPickerSelectPreset(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = 2 // e.g. OpenRouter

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

// TestOnboard_ProviderPickerSelectCustom verifies selecting the "Other /
// custom" entry records the -1 sentinel and advances to connect.
func TestOnboard_ProviderPickerSelectCustom(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = len(providerPresets) // other/custom entry

	mm := sendKeys(m, "\r")
	if mm.onboardingStep != obConnect {
		t.Fatalf("step = %v, want obConnect", mm.onboardingStep)
	}
	if mm.obSelectedPreset != -1 {
		t.Fatalf("obSelectedPreset = %d, want -1 (custom)", mm.obSelectedPreset)
	}
}

// TestOnboard_ProviderPickerSkip verifies selecting "Skip for now" exits
// onboarding cleanly.
func TestOnboard_ProviderPickerSkip(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = len(providerPresets) + 1 // skip entry

	mm := sendKeys(m, "\r")
	if mm.onboardingActive {
		t.Fatal("onboarding still active after 'Skip for now'")
	}
}

// TestOnboard_ProviderPickerEscGoesBack verifies Esc on the provider picker
// returns to the theme step.
func TestOnboard_ProviderPickerEscGoesBack(t *testing.T) {
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = 3

	mm := sendKeys(m, "\x1b")
	if mm.onboardingStep != obTheme {
		t.Fatalf("step = %v, want obTheme", mm.onboardingStep)
	}
	// modalSelectedIdx should be restored to the current theme's index (not 0).
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
} // ── Phase 4: Guided Connect ──────────────────────────────────────────────────

// helper to place an onboarding model on the provider-picker step at a preset.
func onProviderPicker(t *testing.T, idx int) *Model {
	t.Helper()
	m := newOnboardingModel(t)
	m.onboardingStep = obProviderPicker
	m.modalSelectedIdx = idx
	return m
}

// TestConnect_PresetPrefill verifies that choosing a preset pre-fills the
// provider name, type, API base and skips straight to the API Key step (step
// 2), with the provider saved name derived from the preset label.
func TestConnect_PresetPrefill(t *testing.T) {
	// OpenAI is preset index 0.
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

// TestConnect_OllamaSkipsAPIKey verifies the Ollama local preset skips the
// API Key step (lands on step 3, API Base) because it requires no key.
func TestConnect_OllamaSkipsAPIKey(t *testing.T) {
	// Find the ollama preset index.
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
	// API base pre-filled.
	if !strings.HasPrefix(mm.textInput.Value(), "http://localhost:11434") {
		t.Fatalf("api base not pre-filled for ollama: %q", mm.textInput.Value())
	}
}

// TestConnect_CustomPathNoPrefill verifies the "Other / custom" entry calls
// startConnectFlow(nil), starting at step 0 with no pre-filled values.
func TestConnect_CustomPathNoPrefill(t *testing.T) {
	m := onProviderPicker(t, len(providerPresets)) // other/custom entry
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

// TestConnect_PresetModelPrefillAndCompleteToVerify runs a full guided
// connect for a preset: the model alias/name/int steps are pre-filled from the
// preset, so pressing Enter through completes and routes to obVerify (instead
// of closing the modal). This mirrors a first-run: paste key + 3× Enter.
func TestConnect_PresetModelPrefillAndCompleteToVerify(t *testing.T) {
	// Interaction order: preset → API key → (base, pre-filled) → then model
	// steps are pre-filled; Enter through each. Provider picker idx = OpenAI.
	m := onProviderPicker(t, 0)
	mm := sendKeys(m, "\r")
	if mm.formStepIndex != 2 {
		t.Fatalf("start step = %d, want 2", mm.formStepIndex)
	}

	// API Key (API base already pre-filled in formValues[3]).
	mm.textInput.SetValue("sk-test")
	mm = sendKeys(mm, "\r")
	if mm.formStepIndex != 3 {
		t.Fatalf("after key, step = %d, want 3", mm.formStepIndex)
	}
	mm = sendKeys(mm, "\r") // API Base (pre-filled)
	if mm.formStepIndex != 4 {
		t.Fatalf("after base, step = %d, want 4", mm.formStepIndex)
	}
	if mm.formValues[0] != providerPresets[0].label {
		t.Fatalf("provider name = %q, want %q", mm.formValues[0], providerPresets[0].label)
	}

	// Model alias (pre-filled) — pressing Enter uses the default.
	if mm.formValues[4] != providerPresets[0].defaultModelAlias {
		t.Fatalf("model alias prefill = %q, want %q", mm.formValues[4], providerPresets[0].defaultModelAlias)
	}
	if mm.formValues[5] != providerPresets[0].defaultModel {
		t.Fatalf("model name prefill = %q, want %q", mm.formValues[5], providerPresets[0].defaultModel)
	}

	// Enter through model steps. Steps 4→5→6→7→8 advance (5 presses), then the
	// 6th Enter confirms the review step (9) which saves the model.
	for i := 0; i < 6; i++ {
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

	// Provider + model persisted.
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

// TestConnect_EscDuringConnectReturnsToPicker verifies that during onboarding,
// Esc on the connect modal returns to the provider picker (never exits the
// wizard), and that onboarding stays active.
func TestConnect_EscDuringConnectReturnsToPicker(t *testing.T) {
	m := onProviderPicker(t, 0)
	mm := sendKeys(m, "\r") // enter connect
	if mm.onboardingStep != obConnect {
		t.Fatalf("step = %v, want obConnect", mm.onboardingStep)
	}

	mm = sendKeys(mm, "\x1b") // Esc
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

// TestRenderObConnect_RendersProgressAndForm verifies the obConnect step renders
// progress dots (step 5/6) and the form modal content (via the shared
// renderFormModalContent path), not "Coming soon...".
func TestRenderObConnect_RendersProgressAndForm(t *testing.T) {
	m := onProviderPicker(t, 0)
	mm := sendKeys(m, "\r") // enter connect, pre-fills openai
	progress := fmt.Sprintf(i18n.T("tui.onboard.progress"), 5, 6)

	out := mm.renderOnboarding()
	if !strings.Contains(out, progress) {
		t.Fatalf("obConnect missing progress header %q:\n%s", progress, out)
	}
	if strings.Contains(out, "Coming soon...") {
		t.Fatalf("obConnect still renders placeholder:\n%s", out)
	}
	// Form content present: review/step list rendered with provider name.
	if !strings.Contains(out, "OpenAI") {
		t.Fatalf("obConnect missing pre-filled provider name:\n%s", out)
	}
	// The step list includes the review step label (indicating real form render).
	if !strings.Contains(out, i18n.T("tui.connectReview")) {
		t.Fatalf("obConnect missing form steps:\n%s", out)
	}
}

// ── Phase 5: Verify, Set Defaults & Success Screen ──────────────────────────

// obFinalizedModel places the model on the obVerify step with a configured
// provider/model in the config, mimicking the end of a guided connect flow.
func obFinalizedModel(t *testing.T) *Model {
	t.Helper()
	m := newOnboardingModel(t)
	m.onboardingStep = obVerify
	m.onboardingActive = true
	// A real first run starts with no defaults set (newTestModel pre-sets one).
	m.cfg.Agents.Defaults.Provider = ""
	m.cfg.Agents.Defaults.Model = ""
	// Add a configured provider + model as /connect would have.
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

// TestOnboard_DefaultsOverrideSeededPlaceholders verifies that on a true
// first-run obFinalizeSetup overrides the DefaultConfig placeholder defaults
// (provider=openrouter, model=deepseek-v4-pro) with the freshly configured
// provider. A real first-run config seeds these unusable defaults, but the
// chosen provider must become the chat default (regression for Phase 5).
func TestOnboard_DefaultsOverrideSeededPlaceholders(t *testing.T) {
	m := obFinalizedModel(t)
	// Mimic DefaultConfig seeded defaults that have no backing provider/key.
	m.cfg.Agents.Defaults.Provider = "openrouter"
	m.cfg.Agents.Defaults.Model = "deepseek-v4-pro"

	m.obFinalizeSetup()

	if m.cfg.Agents.Defaults.Provider != "openai" {
		t.Fatalf("defaults.Provider = %q, want openai (override seeded placeholder)", m.cfg.Agents.Defaults.Provider)
	}
	if m.cfg.Agents.Defaults.Model != "gpt-4o" {
		t.Fatalf("defaults.Model = %q, want gpt-4o", m.cfg.Agents.Defaults.Model)
	}
	// After finalize, the config must have a usable default provider.
	if !m.cfg.HasUsableProvider() {
		t.Fatal("config should have a usable provider after finalize")
	}
}

// TestOnboard_DefaultsPreservedWhenUsable verifies obFinalizeSetup preserves an
// existing default that already points to a usable provider (user adjusted
// defaults during setup rather than relying on seeding).
func TestOnboard_DefaultsPreservedWhenUsable(t *testing.T) {
	m := obFinalizedModel(t)
	// Simulate: user already set a usable default (e.g. a local ollama) before
	// finalizing — it must not be clobbered by the chalked provider.
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
	// The connect flow sets providerSelectedName to the just-connected provider.
	m.providerSelectedName = "openai"

	m.obFinalizeSetup()

	if m.cfg.Agents.Defaults.Provider != "ollama" {
		t.Fatalf("defaults.Provider = %q, want ollama preserved (usable default)", m.cfg.Agents.Defaults.Provider)
	}
	if m.cfg.Agents.Defaults.Model != "llama3" {
		t.Fatalf("defaults.Model = %q, want llama3 preserved", m.cfg.Agents.Defaults.Model)
	}
	// The success screen still reports the freshly connected provider.
	if m.obProviderName != "openai" {
		t.Fatalf("obProviderName = %q, want openai", m.obProviderName)
	}
}

// TestOnboard_VerifySuccessSetsDoneAndDefaults verifies that an obVerifyResultMsg
// with success transitions to obDone, sets agent defaults, and captures the
// provider/model/masked-key for the success screen.
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
	// Agent defaults were set.
	if mm.cfg.Agents.Defaults.Provider != "openai" {
		t.Fatalf("defaults.Provider = %q, want openai", mm.cfg.Agents.Defaults.Provider)
	}
	if mm.cfg.Agents.Defaults.Model != "gpt-4o" {
		t.Fatalf("defaults.Model = %q, want gpt-4o", mm.cfg.Agents.Defaults.Model)
	}
	// Success screen fields populated.
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

// TestOnboard_VerifyFailureShowsWarning verifies that a failed verification is
// a WARNING (not a blocker): it transitions to obDone with obVerifyFailed set,
// still persists/sets defaults.
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
	// Defaults still set even though verification failed.
	if mm.cfg.Agents.Defaults.Provider != "openai" {
		t.Fatalf("defaults.Provider = %q, want openai despite failure", mm.cfg.Agents.Defaults.Provider)
	}
	// Warning renders on the done screen.
	out := mm.renderObDone(mm.width)
	if !strings.Contains(out, i18n.T("tui.onboard.verifyFailed")) {
		t.Fatalf("done screen missing verify warning:\n%s", out)
	}
}

// TestOnboard_VerifyEscSkipsAhead verifies that Esc during verification skips
// to the done screen and finalizes setup.
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
	// Finalize still ran (defaults set).
	if mm.cfg.Agents.Defaults.Provider != "openai" {
		t.Fatalf("defaults.Provider = %q, want openai after Esc skip", mm.cfg.Agents.Defaults.Provider)
	}
	// Esc-skips renders explainer only (not the warning).
	out := mm.renderObVerify(mm.width)
	if strings.Contains(out, i18n.T("tui.onboard.verifyFailed")) {
		t.Fatalf("skipped verify should not show failure warning:\n%s", out)
	}
}

// TestOnboard_DoneEnterClearsOnboarding verifies Enter on the done screen
// clears onboarding, hides the welcome screen, and focuses the chat input.
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

// TestOnboard_DoneRendersSuccessSummary verifies the done screen renders the
// checkmark, provider/model/key summary and the tips.
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
	// Progress header (step 6 of 6) must render on the done screen too.
	progress := fmt.Sprintf(i18n.T("tui.onboard.progress"), 6, 6)
	if !strings.Contains(out, progress) {
		t.Fatalf("done screen missing progress header %q:\n%s", progress, out)
	}
}

// TestOnboard_VerifyRendersSpinner verifies the verify step renders the
// spinner + "verifying" message while obVerifying is true.
func TestOnboard_VerifyRendersSpinner(t *testing.T) {
	m := obFinalizedModel(t)
	m.obVerifying = true

	out := m.renderObVerify(m.width)
	if !strings.Contains(out, i18n.T("tui.onboard.verifying")) {
		t.Fatalf("verify screen missing verifying message:\n%s", out)
	}
	// The bouncing-dot spinner frames "[...]".
	if !strings.Contains(out, "[") || !strings.Contains(out, "]") {
		t.Fatalf("verify screen missing spinner:\n%s", out)
	}
} // TestOnboard_VerifyToDoneIntegration verifies the full end-to-end routing:
// a completed guided connect routes to obVerify with obVerifying + the verify
// cmd, and an async result transitions to obDone with defaults set and the
// success screen rendered.
func TestOnboard_VerifyToDoneIntegration(t *testing.T) {
	m := onProviderPicker(t, 0)
	mm := sendKeys(m, "\r") // enter connect (OpenAI preset)
	if mm.onboardingStep != obConnect {
		t.Fatalf("step = %v, want obConnect", mm.onboardingStep)
	}

	// API key → API base (pre-filled) → model prefill → review.
	mm.textInput.SetValue("sk-test-abcdef")
	mm = sendKeys(mm, "\r")  // API key
	mm = sendKeys(mm, "\r")  // API base (pre-filled)
	for i := 0; i < 6; i++ { // model steps + review
		mm = sendKeys(mm, "\r")
	}

	// Routes to verify, with verification already running.
	if mm.onboardingStep != obVerify {
		t.Fatalf("step = %v, want obVerify", mm.onboardingStep)
	}
	if !mm.obVerifying {
		t.Fatal("obVerifying should be true on entering verify step")
	}
	// Defaults + workspace were persisted just before starting verification.
	if mm.cfg.Agents.Defaults.Provider == "" {
		t.Fatal("defaults.Provider not set entering verify")
	}
	if mm.cfg.Agents.Defaults.Model == "" {
		t.Fatal("defaults.Model not set entering verify")
	}

	// Render the spinner while verifying.
	if !strings.Contains(mm.renderObVerify(mm.width), i18n.T("tui.onboard.verifying")) {
		t.Fatal("verify screen missing verifying message")
	}

	// Fire the async result (success).
	updated, _ := mm.Update(obVerifyResultMsg{success: true, providerName: mm.cfg.Agents.Defaults.Provider})
	out := updated.(*Model)

	if out.onboardingStep != obDone {
		t.Fatalf("step = %v, want obDone after verify result", out.onboardingStep)
	}
	if out.obVerifying {
		t.Fatal("obVerifying should be false after result")
	}
	// Done screen shows summary.
	if !strings.Contains(out.renderObDone(out.width), i18n.T("tui.onboard.pressEnterStart")) {
		t.Fatal("done screen missing press-enter-start hint")
	}

	// Enter clears onboarding and focuses chat input.
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

// TestOnboard_NarrowWidths verifies every onboarding step renders without
// panicking at widths below the preferred 60-col box (narrow terminal).
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
} // ── Phase 7: First-run trigger & full transition coverage ──────────────────

// TestOnboarding_FreshConfigTriggersOnboarding verifies that a brand-new
// config (no usable provider) activates the onboarding wizard at the welcome
// step.
func TestOnboarding_FreshConfigTriggersOnboarding(t *testing.T) {
	m := newOnboardingTestModel(t)
	if !m.onboardingActive {
		t.Fatal("expected onboardingActive for fresh config")
	}
	if m.onboardingStep != obWelcome {
		t.Fatalf("expected obWelcome, got %d", m.onboardingStep)
	}
}

// TestOnboarding_ConfigWithProvidersNoOnboarding verifies that a config with a
// usable provider does NOT trigger the wizard.
func TestOnboarding_ConfigWithProvidersNoOnboarding(t *testing.T) {
	m := newConfiguredOnboardingTestModel(t)
	if m.onboardingActive {
		t.Fatal("expected onboarding NOT active for configured provider")
	}
}

// TestOnboarding_FullHappyPath drives the wizard end-to-end from the welcome
// screen through language → provider preset → connect → verify → done → chat.
func TestOnboarding_FullHappyPath(t *testing.T) {
	m := newOnboardingTestModel(t)

	// welcome → language
	m = sendKeys(m, "\r")
	if m.onboardingStep != obLanguage {
		t.Fatalf("welcome Enter: step = %v, want obLanguage", m.onboardingStep)
	}
	// language (English, default) → theme picker
	m = sendKeys(m, "\r")
	if m.onboardingStep != obTheme {
		t.Fatalf("lang Enter: step = %v, want obTheme", m.onboardingStep)
	}
	// theme (default selection) → provider picker
	m = sendKeys(m, "\r")
	if m.onboardingStep != obProviderPicker {
		t.Fatalf("theme Enter: step = %v, want obProviderPicker", m.onboardingStep)
	}
	// provider picker: select preset 0 (OpenAI) → connect
	m = sendKeys(m, "\r")
	if m.onboardingStep != obConnect {
		t.Fatalf("picker Enter: step = %v, want obConnect", m.onboardingStep)
	}
	if m.formStepIndex != 2 {
		t.Fatalf("expect connect at API Key step, got %d", m.formStepIndex)
	}

	// Type an API key, then Enter through API base + model steps + review.
	m.textInput.SetValue("sk-test-abcdef")
	m = sendKeys(m, "\r") // API key
	if m.formStepIndex != 3 {
		t.Fatalf("after key step = %d, want 3", m.formStepIndex)
	}
	m = sendKeys(m, "\r") // API base (pre-filled)
	for i := 0; i < 6; i++ {
		m = sendKeys(m, "\r")
	}

	// Routes to verify, verification running.
	if m.onboardingStep != obVerify {
		t.Fatalf("step = %v, want obVerify", m.onboardingStep)
	}
	if !m.obVerifying {
		t.Fatal("obVerifying should be true entering verify")
	}

	// Fire a successful async result → done screen.
	out, _ := m.Update(obVerifyResultMsg{success: true, providerName: m.cfg.Agents.Defaults.Provider})
	m = out.(*Model)
	if m.onboardingStep != obDone {
		t.Fatalf("step = %v, want obDone", m.onboardingStep)
	}
	if m.obVerifying {
		t.Fatal("obVerifying should be false after result")
	}

	// Enter on done → chat, onboarding cleared.
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

// TestOnboarding_SkipPath verifies Esc → "Yes, skip" exits the wizard.
func TestOnboarding_SkipPath(t *testing.T) {
	m := newOnboardingTestModel(t)

	m = sendKeys(m, "esc") // welcome → skip confirm
	if !m.obSkipConfirm {
		t.Fatal("obSkipConfirm should be set after Esc")
	}
	// Default selection is "No, continue" (1). Navigate up to "Yes, skip".
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

// TestOnboarding_LanguageChangePersists verifies selecting Spanish updates
// both cfg.Language and the global i18n language.
func TestOnboarding_LanguageChangePersists(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := newOnboardingTestModel(t)

	m = sendKeys(m, "\r")   // welcome → language
	m = sendKeys(m, "down") // select Español
	m = sendKeys(m, "\r")   // confirm
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

// TestOnboarding_ProviderSkipExits verifies choosing "Skip for now" in the
// provider picker exits onboarding cleanly (no connect modal).
func TestOnboarding_ProviderSkipExits(t *testing.T) {
	m := newOnboardingTestModel(t)

	m = sendKeys(m, "\r") // welcome → language
	m = sendKeys(m, "\r") // language → theme
	m = sendKeys(m, "\r") // theme → provider picker
	// Scroll to the "Skip for now" entry (last item).
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

// TestOnboarding_VerifyFailureAllowsProceeding verifies a failed verification
// is a WARNING (non-blocking): it still advances to done with the warning flag
// and allows proceeding to chat on Enter.
func TestOnboarding_VerifyFailureAllowsProceeding(t *testing.T) {
	m := newOnboardingTestModel(t)
	m.onboardingStep = obVerify
	m.obVerifying = true
	// A failed result should be a warning, not a blocker.
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
	// Warning renders on the done screen.
	if !strings.Contains(mm.renderObDone(mm.width), i18n.T("tui.onboard.verifyFailed")) {
		t.Fatal("done screen missing verify warning")
	}
	// User can still proceed to chat.
	mm = sendKeys(mm, "\r")
	if mm.onboardingActive {
		t.Fatal("onboarding should clear on Enter despite failed verification")
	}
}

// TestOnboarding_DoneEnterClearsOnboarding verifies Enter on the done screen
// clears onboarding and the welcome screen.
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
