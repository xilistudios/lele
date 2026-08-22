package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// renderFormHelper builds a model with a form modal and invokes
// renderFormModalContent.
func renderFormHelper(t *testing.T, mode modalType, step int, saved bool) *Model {
	t.Helper()
	m := welcomeViewModel(t)
	m.modalMode = mode
	m.formStepIndex = step
	m.providerSavedInFlow = saved
	m.formValues = make([]string, 10)
	return m
}

// TestFormModal_ReviewStep covers the review rendering of the add-provider
// form (formStepIndex 9, providerSavedInFlow true).
func TestFormModal_ReviewStep(t *testing.T) {
	m := renderFormHelper(t, ModalAddProvider, 9, true)
	m.formValues[0] = "openai"
	m.formValues[1] = "openai"
	m.formValues[4] = "gpt-4o"
	out := m.renderFormModalContent(i18n.T("tui.addProvider"), m.formStepNames())
	if !strings.Contains(out, i18n.T("tui.connectReviewHint")) {
		t.Errorf("expected review hint, got:\n%s", out)
	}
}

// TestFormModal_SuccessScreen covers connectSuccess rendering.
func TestFormModal_SuccessScreen(t *testing.T) {
	m := renderFormHelper(t, ModalAddProvider, 10, false)
	m.connectSuccess = true
	m.formValues[0] = "openai"
	m.formValues[1] = "openai"
	m.formValues[4] = "gpt-4o"
	// Long API key to cover masking.
	m.formValues[2] = "0123456789abcdef"
	out := m.renderFormModalContent(i18n.T("tui.addProvider"), m.formStepNames())
	if !strings.Contains(out, i18n.T("tui.connectModelSaved")) {
		t.Errorf("expected success label, got:\n%s", out)
	}
}

// TestFormModal_ProviderTypePicker covers the provider-type picker rendering.
func TestFormModal_ProviderTypePicker(t *testing.T) {
	m := renderFormHelper(t, ModalAddProvider, 1, false)
	m.providerTypePicker = true
	m.providerTypePickerMax = len(providerPresets) + 1
	m.providerTypePickerIdx = 0
	out := m.renderFormModalContent(i18n.T("tui.addProvider"), m.formStepNames())
	if !strings.Contains(out, i18n.T("tui.connectPickType")) {
		t.Errorf("expected pick-type label, got:\n%s", out)
	}
}

// TestFormModal_ErrorAndAPIKeyHint covers the error display and the API-key
// optional hint rendering.
func TestFormModal_ErrorAndAPIKeyHint(t *testing.T) {
	m := renderFormHelper(t, ModalAddProvider, 2, false)
	m.formError = "something bad"
	out := m.renderFormModalContent("t", m.formStepNames())
	if !strings.Contains(out, "something bad") {
		t.Errorf("expected error rendered, got:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.connectAPIKeyOptional")) {
		t.Errorf("expected API key optional hint, got:\n%s", out)
	}
}

// TestFormModal_APIBasePrefilled covers the prefilled api-base hint.
func TestFormModal_APIBasePrefilled(t *testing.T) {
	m := renderFormHelper(t, ModalAddProvider, 3, false)
	m.providerTypeFromPreset = true
	out := m.renderFormModalContent("t", m.formStepNames())
	if !strings.Contains(out, i18n.T("tui.connectAPIBasePrefilled")) {
		t.Errorf("expected prefilled hint, got:\n%s", out)
	}
}

// TestFormModal_APIBaseRequired covers the required api-base hint (non-preset).
func TestFormModal_APIBaseRequired(t *testing.T) {
	m := renderFormHelper(t, ModalAddProvider, 3, false)
	m.providerTypeFromPreset = false
	out := m.renderFormModalContent("t", m.formStepNames())
	if !strings.Contains(out, i18n.T("tui.connectAPIBaseRequired")) {
		t.Errorf("expected required hint, got:\n%s", out)
	}
}

// TestFormModal_ModelStepsHint covers providerSavedInFlow non-review hint.
func TestFormModal_ModelStepsHint(t *testing.T) {
	m := renderFormHelper(t, ModalAddProvider, 4, true)
	out := m.renderFormModalContent("t", m.formStepNames())
	if !strings.Contains(out, i18n.T("tui.connectModelStepsHint")) {
		t.Errorf("expected model-steps hint, got:\n%s", out)
	}
}
