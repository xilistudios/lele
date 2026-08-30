package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// Secret-input masking (audit M2). API keys and secret values collected in
// the /connect (ModalAddProvider) and add-secret (ModalAddSecret) forms must
// never render in clear text. The predicates below are data-driven from
// modalMode+formStepIndex so the ~24 formStepIndex transition sites need no
// per-transition bookkeeping — syncTextInputEcho is the single choke point
// that re-derives the widget state on every keystroke.

// isSecretInputStep reports whether the CURRENT form step collects a secret
// (API key / secret value). ModalAddProvider step 2 is "API Key" and
// ModalAddSecret step 1 is "Secret value"; the onboarding connect flow
// (onboardingStep==obConnect) reuses ModalAddProvider with identical step
// numbering, so it is covered by the same rule.
func (m *Model) isSecretInputStep() bool {
	switch m.modalMode {
	case ModalAddProvider:
		return m.formStepIndex == 2
	case ModalAddSecret:
		return m.formStepIndex == 1
	}
	return false
}

// isSecretFormValue reports whether the stored value for a completed form
// step is a secret and must be masked in step-list / review displays.
func (m *Model) isSecretFormValue(stepIdx int) bool {
	switch m.modalMode {
	case ModalAddProvider:
		return stepIdx == 2
	case ModalAddSecret:
		return stepIdx == 1
	}
	return false
}

// maskSecretDisplay renders a secret value for display without leaking it.
// Empty stays empty (callers keep their own "…" placeholder); short values
// are fully hidden (maskSecretValue would echo 4+4 of a ≤8-char secret,
// which is the whole secret); long values reuse the exact 4+8+4 policy of
// maskSecretValue in secrets.go.
func maskSecretDisplay(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 8 {
		return strings.Repeat("•", 8)
	}
	return maskSecretValue(v)
}

// syncTextInputEcho re-derives the shared textinput's echo mode from the
// current modal/step state. Called before every keystroke forwarded to the
// widget and whenever a form modal is entered, so the mode self-heals across
// all transitions and a stale password echo can never leak into the chat
// input (which uses a separate textarea widget, but the reset keeps the
// shared textinput clean for non-form renderers).
func (m *Model) syncTextInputEcho() {
	if m.isSecretInputStep() {
		m.textInput.EchoMode = textinput.EchoPassword
		m.textInput.EchoCharacter = '•'
	} else {
		m.textInput.EchoMode = textinput.EchoNormal
	}
}

// textInputView is the only safe way to paint the shared textinput widget:
// it re-derives the echo mode from the current modal/step first, so no
// renderer can show a secret in clear text (or hide a non-secret behind
// stale bullets) regardless of which transition last touched the widget.
func (m *Model) textInputView() string {
	m.syncTextInputEcho()
	return m.textInput.View()
}
