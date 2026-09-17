package tui

import (
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/tui/i18n"

	"github.com/charmbracelet/lipgloss"
)

// renderFormModal renders a multi-step form modal with step indicators,
// an input field for the current step, and optional error display. It frames
// the modal content with paintFrame.
func (m *Model) renderFormModal(title string, steps []string) string {
	return m.paintFrame(m.renderFormModalContent(title, steps))
}

// renderFormModalContent builds the ModalContainer-wrapped (but unframed)
// content for a form modal. renderFormModal paints it into a full frame; the
// onboarding wizard renders it inline inside its own frame via renderObConnect.
func (m *Model) renderFormModalContent(title string, steps []string) string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(title) + "\n\n")

	// ── Success screen: show what was saved and how to continue ──
	if m.connectSuccess {
		sb.WriteString(m.renderFormSuccess())
		return ModalContainer.Render(sb.String())
	}

	// ── Provider-type picker: list of known presets ──
	if m.modalMode == ModalAddProvider && m.providerTypePicker {
		sb.WriteString(m.renderProviderTypePicker())
		return ModalContainer.Render(sb.String())
	}

	isReviewStep := m.modalMode == ModalAddProvider && m.formStepIndex == 9 && m.providerSavedInFlow

	// ── Step list ──
	sb.WriteString(m.renderFormSteps(steps, isReviewStep))
	sb.WriteString("\n")

	// Error display
	if m.formError != "" {
		sb.WriteString(lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n\n")
	}

	// Text input field (hidden on review step)
	if !isReviewStep {
		m.textInput.Width = 40
		sb.WriteString(InputBarContainer.Width(44).Render(m.textInputView()) + "\n\n")
	}

	// Catalog model suggestions (filter-as-you-type) on the model-name step
	if m.addModelCatalogActive && m.isModelNameFormStep() && !isReviewStep {
		sb.WriteString(renderCatalogSuggestions(m.addModelCatalogLabels, m.addModelCatalogIdx, m.maxModalVisible()))
	}

	// Contextual step hint (optional fields)
	sb.WriteString(m.renderFormStepHint(isReviewStep))

	// Hints
	sb.WriteString(m.renderFormHints(isReviewStep))

	modalView := ModalContainer.Render(sb.String())
	return modalView
}

// renderFormSuccess renders the success screen after a form is submitted.
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

// renderProviderTypePicker renders the provider type selection list within
// the add-provider form modal.
func (m *Model) renderProviderTypePicker() string {
	var sb strings.Builder
	sb.WriteString(ModalItemInactive.Render("  "+i18n.T("tui.connectPickType")) + "\n\n")

	max := m.providerTypePickerMax
	if max <= 0 {
		max = len(providerPresets) + 1
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

	// Keep highlighted preset inside visible window
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
	for i := m.modalScrollOffset; i < endIdx; i++ {
		var label string
		if i < len(providerPresets) {
			p := providerPresets[i]
			label = p.label
			if p.apiBase != "" {
				label += "  ·  " + p.apiBase
			}
		} else {
			label = i18n.T("tui.connectCustomType")
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

// renderFormSteps renders the list of form steps with completion indicators.
func (m *Model) renderFormSteps(steps []string, isReviewStep bool) string {
	var sb strings.Builder

	for i, step := range steps {
		// Review mode: show completed steps
		if isReviewStep && i < 9 {
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
			// Completed step
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

// renderFormStepHint renders contextual hints for the current form step.
func (m *Model) renderFormStepHint(isReviewStep bool) string {
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

// renderFormHints renders the bottom hints for the form modal.
func (m *Model) renderFormHints(isReviewStep bool) string {
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

// renderCatalogSuggestions paints the filterable catalog model list shown
// under the form's text input on the model-name step.
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

// formStepNames returns the step names for the current form modal mode.
func (m *Model) formStepNames() []string {
	switch m.modalMode {
	case ModalAddProvider:
		steps := []string{
			"Provider name", "Provider type", "API Key", "API Base URL",
			"Model alias", "Model name", "Context window", "Max tokens", "Vision (yes/no)",
			i18n.T("tui.connectReview"),
		}
		if m.addModelCatalogThink != "" && len(steps) > 5 {
			steps[5] = "Model name (thinking: " + m.addModelCatalogThink + ")"
		}
		return steps
	case ModalAddModel:
		nameLabel := "Model name"
		if m.addModelCatalogThink != "" {
			nameLabel += " (thinking: " + m.addModelCatalogThink + ")"
		}
		return []string{"Model alias", nameLabel, "Context window", "Max tokens", "Vision (yes/no)"}
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
	default:
		return nil
	}
}
