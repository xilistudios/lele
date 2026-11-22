package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

func (m *Model) isSecretInputStep() bool {
	switch m.modalMode {
	case ModalAddProvider:
		return m.formStepIndex == 2
	case ModalEditProvider:
		return m.formStepIndex == editProviderAPIKeyStep || m.formStepIndex == editProviderHeadersStep
	case ModalAddSecret:
		return m.formStepIndex == 1
	case ModalMCPAdd:
		return m.formStepIndex == mcpAddStepEnv || m.formStepIndex == mcpAddStepHeaders
	}
	return false
}

func (m *Model) isSecretFormValue(stepIdx int) bool {
	switch m.modalMode {
	case ModalAddProvider:
		return stepIdx == 2
	case ModalEditProvider:
		return stepIdx == editProviderAPIKeyStep || stepIdx == editProviderHeadersStep
	case ModalAddSecret:
		return stepIdx == 1
	}
	return false
}

func maskSecretDisplay(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 8 {
		return strings.Repeat("•", 8)
	}
	return maskSecretValue(v)
}

func (m *Model) syncTextInputEcho() {
	if m.isSecretInputStep() {
		m.textInput.EchoMode = textinput.EchoPassword
		m.textInput.EchoCharacter = '•'
	} else {
		m.textInput.EchoMode = textinput.EchoNormal
	}
}

func (m *Model) textInputView() string {
	m.syncTextInputEcho()
	return m.textInput.View()
}
