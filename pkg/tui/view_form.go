package tui

import (
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) renderFormModal(title string, steps []string) string {
	return m.paintFrame(m.renderFormModalContent(title, steps))
}

func (m *Model) renderFormModalContent(title string, steps []string) string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(title) + "\n\n")

	if m.connectSuccess {
		sb.WriteString(m.renderFormSuccess())
		return ModalContainer.Render(sb.String())
	}

	if m.modalMode == ModalAddProvider && m.providerTypePicker {
		sb.WriteString(m.renderProviderTypePicker())
		return ModalContainer.Render(sb.String())
	}

	isReviewStep := m.modalMode == ModalAddProvider && m.formStepIndex == 10 && m.providerSavedInFlow

	sb.WriteString(m.renderFormSteps(steps, isReviewStep))
	sb.WriteString("\n")

	if m.formError != "" {
		sb.WriteString(lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n\n")
	}

	if m.connectOAuthFeedback != "" {
		sb.WriteString(SuccessStyle.Render("  ✓ "+m.connectOAuthFeedback) + "\n\n")
	}

	if !isReviewStep {
		m.textInput.Width = 40
		sb.WriteString(InputBarContainer.Width(44).Render(m.textInputView()) + "\n\n")
	}

	if m.addModelCatalogActive && m.isModelNameFormStep() && !isReviewStep {
		sb.WriteString(renderCatalogSuggestions(m.addModelCatalogLabels, m.addModelCatalogIdx, m.maxModalVisible()))
	}

	sb.WriteString(m.renderFormStepHint(isReviewStep))

	sb.WriteString(m.renderFormHints(isReviewStep))

	modalView := ModalContainer.Render(sb.String())
	return modalView
}

func (m *Model) renderFormSuccess() string {
	var sb strings.Builder
	sb.WriteString(SuccessStyle.Render("  "+i18n.T("tui.connectModelSaved")) + "\n\n")

	providerName := ""
	providerType := ""
	modelAlias := ""
	if len(m.formValues) > 0 {
		providerName = m.formValues[0]
	}
	if len(m.formValues) > 1 {
		providerType = m.formValues[1]
	}
	if len(m.formValues) > 4 {
		modelAlias = m.formValues[4]
	}
	sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  %s: %s", i18n.T("tui.connectReviewProvider"), providerName)) + "\n")
	if providerType != "" {
		sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  Type: %s", providerType)) + "\n")
	}
	if modelAlias != "" {
		sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  %s: %s", i18n.T("tui.connectReviewModel"), modelAlias)) + "\n")
	}
	sb.WriteString("\n")
	sb.WriteString(HelpStyle.Render("  " + i18n.T("tui.connectSuccessHint")))
	return sb.String()
}

func (m *Model) renderProviderTypePicker() string {
	var sb strings.Builder
	sb.WriteString(ModalItemInactive.Render("  "+i18n.T("tui.connectPickProvider")) + "\n\n")

	max := m.providerTypePickerMax
	if max <= 0 {
		max = len(connectRows())
	}

	maxVisible := m.height - 14
	if maxVisible < 5 {
		maxVisible = 5
	}
	if maxVisible > max {
		maxVisible = max
	}
	if max > maxVisible && maxVisible > 1 {
		maxVisible--
	}

	if m.providerTypePickerIdx < m.modalScrollOffset {
		m.modalScrollOffset = m.providerTypePickerIdx
	}
	if m.providerTypePickerIdx >= m.modalScrollOffset+maxVisible {
		m.modalScrollOffset = m.providerTypePickerIdx - maxVisible + 1
	}
	if m.modalScrollOffset < 0 {
		m.modalScrollOffset = 0
	}
	if maxOffset := max - maxVisible; m.modalScrollOffset > maxOffset {
		m.modalScrollOffset = maxOffset
	}

	if m.modalScrollOffset > 0 {
		sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.moreAbove")) + "\n")
	}

	endIdx := m.modalScrollOffset + maxVisible
	if endIdx > max {
		endIdx = max
	}
	var rowRows []connectRow
	if m.connectRowPicker {
		rowRows = connectRows()
	}
	for i := m.modalScrollOffset; i < endIdx; i++ {
		var label string
		if !m.connectRowPicker {
			if i < len(providerPresets) {
				p := providerPresets[i]
				label = p.label
				if p.apiBase != "" {
					label += "  ·  " + p.apiBase
				}
			} else {
				label = i18n.T("tui.connectCustomType")
			}
		} else if i >= 0 && i < len(rowRows) {
			row := rowRows[i]
			label = row.label
			if row.apiBase != "" {
				label += "  ·  " + row.apiBase
			}
		} else {
			continue
		}
		if i == m.providerTypePickerIdx {
			sb.WriteString(ModalItemActive.Render("  > "+label) + "\n")
		} else {
			sb.WriteString(ModalItemInactive.Render("    "+label) + "\n")
		}
	}
	if endIdx < max {
		sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.moreBelow")) + "\n")
	}
	sb.WriteString("\n")
	sb.WriteString(HelpStyle.Render("  " + i18n.T("tui.connectPickerHint")))
	return sb.String()
}

func (m *Model) renderFormSteps(steps []string, isReviewStep bool) string {
	var sb strings.Builder

	for i, step := range steps {
		if isReviewStep && i < 10 {
			val := ""
			if i < len(m.formValues) {
				val = m.formValues[i]
			}
			if m.isSecretFormValue(i) {
				val = maskSecretDisplay(val)
			}
			if i == 4 {
				sb.WriteString("\n" + SidebarHeader.Render(i18n.T("tui.connectReviewModel")) + "\n")
			}
			sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  ✓ %s: %s", step, val)) + "\n")
			continue
		}

		if i < m.formStepIndex {
			val := ""
			if i < len(m.formValues) {
				val = m.formValues[i]
			}
			if m.isSecretFormValue(i) {
				val = maskSecretDisplay(val)
			}
			sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  ✓ %s: %s", step, val)) + "\n")
		} else if i == m.formStepIndex {
			if isReviewStep {
				sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  ▶ %s", step)) + "\n")
			} else {
				val := m.textInput.Value()
				if m.isSecretInputStep() {
					val = maskSecretDisplay(val)
				}
				if val == "" {
					val = "…"
				}
				sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  ▶ %s: [%s]", step, val)) + "\n")
			}
		} else {
			sb.WriteString(CommentColorStyle.Render(fmt.Sprintf("  ○ %s", step)) + "\n")
		}
	}

	return sb.String()
}

func (m *Model) renderFormStepHint(isReviewStep bool) string {
	if m.modalMode == ModalEditProvider {
		switch m.formStepIndex {
		case editProviderAPIKeyStep, editProviderAPIBaseStep:
			return CommentColorStyle.Render("  "+i18n.T("tui.editProviderKeepHint")) + "\n\n"
		case editProviderProxyStep, editProviderAuthMethodStep, editProviderConnectModeStep:
			return CommentColorStyle.Render("  "+i18n.T("tui.editProviderOptionalHint")) + "\n\n"
		case editProviderWebSearchStep:
			return CommentColorStyle.Render("  "+i18n.T("tui.editProviderWebSearchHint")) + "\n\n"
		case editProviderHeadersStep:
			return CommentColorStyle.Render("  "+i18n.T("tui.editProviderHeadersHint")) + "\n\n"
		}
		return ""
	}
	if m.modalMode == ModalEditModel {
		switch m.formStepIndex {
		case editModelReasoningEffortStep, editModelReasoningExcludeStep, editModelReasoningSummaryStep:
			return CommentColorStyle.Render("  "+i18n.T("tui.editModelReasoningDefaultHint")) + "\n\n"
		case editModelReasoningMaxTokensStep:
			return CommentColorStyle.Render("  "+i18n.T("tui.editModelReasoningTokensHint")) + "\n\n"
		}
		return ""
	}
	if m.modalMode != ModalAddProvider || m.providerSavedInFlow || m.providerTypePicker || m.connectSuccess || isReviewStep {
		return ""
	}

	switch m.formStepIndex {
	case 2:
		return CommentColorStyle.Render("  "+i18n.T("tui.connectAPIKeyOptional")) + "\n\n"
	case 3:
		if m.providerTypeFromPreset {
			return CommentColorStyle.Render("  "+i18n.T("tui.connectAPIBasePrefilled")) + "\n\n"
		}
		return CommentColorStyle.Render("  "+i18n.T("tui.connectAPIBaseRequired")) + "\n\n"
	}
	return ""
}

func (m *Model) renderFormHints(isReviewStep bool) string {
	if m.modalMode == ModalEditProvider {
		return HelpStyle.Render("  " + i18n.T("tui.formEnter"))
	}
	if m.addModelCatalogActive && m.isModelNameFormStep() && !isReviewStep {
		return HelpStyle.Render("  " + i18n.T("tui.catalogPickerHint"))
	}
	if isReviewStep {
		return HelpStyle.Render("  " + i18n.T("tui.connectReviewHint"))
	}
	if m.providerSavedInFlow {
		return HelpStyle.Render("  " + i18n.T("tui.connectModelStepsHint"))
	}
	return HelpStyle.Render("  " + i18n.T("tui.formEnter"))
}

func renderCatalogSuggestions(labels []string, idx, maxVisible int) string {
	if len(labels) == 0 {
		return CommentColorStyle.Render("  "+i18n.T("tui.catalogNoMatches")) + "\n\n"
	}
	if maxVisible < 3 {
		maxVisible = 3
	}
	if maxVisible > 8 {
		maxVisible = 8
	}
	start := 0
	if idx >= maxVisible {
		start = idx - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(labels) {
		end = len(labels)
	}
	var sb strings.Builder
	sb.WriteString(SidebarHeader.Render("  "+i18n.T("tui.catalogSuggestions")) + "\n")
	for i := start; i < end; i++ {
		if i == idx {
			sb.WriteString(ModalItemActive.Render("  › "+labels[i]) + "\n")
		} else {
			sb.WriteString(ModalItemInactive.Render("    "+labels[i]) + "\n")
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

func (m *Model) modelFormStepNames() []string {
	nameLabel := "Model name"
	if m.addModelCatalogThink != "" {
		nameLabel += " (thinking: " + m.addModelCatalogThink + ")"
	}
	steps := []string{
		"Model alias",
		nameLabel,
		"Context window",
		"Max tokens",
		"Vision (yes/no)",
		"Think system",
	}
	if m.modalMode != ModalEditModel {
		return steps
	}
	return append(steps,
		"Reasoning (yes/no)",
		"Reasoning effort",
		"Reasoning max tokens",
		"Reasoning exclude (yes/no/default)",
		"Reasoning summary",
	)
}

func (m *Model) formStepNames() []string {
	switch m.modalMode {
	case ModalAddProvider:
		steps := []string{
			"Provider name", "Provider type", "API Key", "API Base URL",
			"Model alias", "Model name", "Context window", "Max tokens", "Vision (yes/no)",
			"Think system", i18n.T("tui.connectReview"),
		}
		if m.addModelCatalogThink != "" && len(steps) > 5 {
			steps[5] = "Model name (thinking: " + m.addModelCatalogThink + ")"
		}
		return steps
	case ModalAddModel, ModalEditModel:
		return m.modelFormStepNames()
	case ModalEditProvider:
		return []string{
			"Provider type",
			"API Key",
			"API Base URL",
			"Proxy",
			"Auth method",
			"Connect mode",
			"Web search (yes/no/default)",
			"Headers (JSON)",
		}
	case ModalAddCommand:
		return []string{
			i18n.T("tui.commands.fieldName"),
			i18n.T("tui.commands.fieldDescription"),
			i18n.T("tui.commands.fieldAgent"),
			i18n.T("tui.commands.fieldModel"),
			i18n.T("tui.commands.fieldAllowShell"),
			i18n.T("tui.commands.fieldAllowAbsFiles"),
			i18n.T("tui.commands.fieldTemplate"),
		}
	case ModalAddSecret:
		return []string{
			i18n.T("tui.secretName"),
			i18n.T("tui.secretValue"),
			i18n.T("tui.secretDescription"),
			i18n.T("tui.secretTags"),
			i18n.T("tui.secretScope"),
		}
	case ModalMCPAdd:
		return m.mcpAddStepNames()
	default:
		return nil
	}
}
