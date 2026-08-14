package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// keyringSvc returns the keyring service from the agent loop, or nil if the
// module is disabled / unavailable.
func (m *Model) keyringSvc() *keyring.Service {
	if m.agentLoop == nil {
		return nil
	}
	return m.agentLoop.KeyringService()
}

// loadSecrets refreshes the secrets modal list from the keyring service.
func (m *Model) loadSecrets() {
	m.modalItems = nil
	m.secretsModalKeys = nil
	m.secretsDetailMode = false
	m.secretsDetailName = ""
	m.secretsReveal = false

	svc := m.keyringSvc()
	if svc == nil {
		m.modalItems = append(m.modalItems, i18n.T("tui.secretsUnavailable"))
		return
	}

	secrets, err := svc.ListAll()
	if err != nil || len(secrets) == 0 {
		m.modalItems = append(m.modalItems, i18n.T("tui.noSecrets"))
		return
	}

	for _, s := range secrets {
		m.modalItems = append(m.modalItems, formatSecretLine(s))
		m.secretsModalKeys = append(m.secretsModalKeys, s.Name)
	}
}

// formatSecretLine renders a single secret as a compact list line.
func formatSecretLine(s keyring.SecretMeta) string {
	name := s.Name
	if len(name) > 36 {
		name = name[:33] + "..."
	}

	var parts []string
	if len(s.Tags) > 0 {
		parts = append(parts, "["+strings.Join(s.Tags, ", ")+"]")
	}
	if len(s.Scope) > 0 {
		parts = append(parts, CommentColorStyle.Render("(scoped)"))
	}

	line := fmt.Sprintf("🔐 %s", name)
	if len(parts) > 0 {
		line += "  " + strings.Join(parts, " ")
	}
	if s.Description != "" {
		desc := s.Description
		if len(desc) > 30 {
			desc = desc[:27] + "..."
		}
		line += "  " + CommentColorStyle.Render(desc)
	}
	return line
}

// secretsHeader builds the modal title with a backend indicator.
func (m *Model) secretsHeader() string {
	svc := m.keyringSvc()
	backend := "—"
	count := 0
	if svc != nil {
		backend = svc.Backend()
		count = svc.Count()
	}
	title := fmt.Sprintf("%s (%d)", i18n.T("tui.secrets"), count)
	return title + "  " + CommentColorStyle.Render("["+i18n.T("tui.secretsBackend")+": "+backend+"]")
}

// renderSecretDetail renders the detail view for the currently selected secret.
func (m *Model) renderSecretDetail() string {
	svc := m.keyringSvc()
	if svc == nil {
		m.secretsDetailMode = false
		m.loadSecrets()
		return m.renderModal(m.secretsHeader())
	}

	name := m.secretsDetailName
	meta, found := m.findSecretMeta(svc, name)
	if !found {
		m.secretsDetailMode = false
		m.loadSecrets()
		return m.renderModal(m.secretsHeader())
	}

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render("🔐 "+name) + "\n\n")

	labelStyle := lipgloss.NewStyle().Foreground(AccentColor)
	addField := func(label, value string) {
		sb.WriteString(labelStyle.Render(label+": ") + value + "\n")
	}

	if meta.Description != "" {
		addField(i18n.T("tui.secretDescription"), meta.Description)
	}
	if len(meta.Tags) > 0 {
		addField(i18n.T("tui.secretTags"), strings.Join(meta.Tags, ", "))
	}
	if len(meta.Scope) > 0 {
		addField(i18n.T("tui.secretScope"), strings.Join(meta.Scope, ", "))
	} else {
		addField(i18n.T("tui.secretScope"), CommentColorStyle.Render("(all agents)"))
	}

	// Value: masked by default, revealed on demand.
	if m.secretsReveal {
		val, err := svc.GetRaw(name)
		if err != nil {
			addField(i18n.T("tui.secretValue"), lipgloss.NewStyle().Foreground(PrimaryColor).Render(err.Error()))
		} else {
			addField(i18n.T("tui.secretValue"), val+"  "+CommentColorStyle.Render("["+i18n.T("tui.secretRevealed")+"]"))
		}
	} else {
		addField(i18n.T("tui.secretValue"), CommentColorStyle.Render("••••••••  ["+i18n.T("tui.secretMasked")+"]"))
	}

	if meta.CreatedBy != "" {
		addField(i18n.T("tui.secretCreatedBy"), meta.CreatedBy)
	}
	if !meta.CreatedAt.IsZero() {
		addField(i18n.T("tui.secretCreatedAt"), meta.CreatedAt.Format("2006-01-02 15:04"))
	}
	if !meta.UpdatedAt.IsZero() {
		addField(i18n.T("tui.secretUpdatedAt"), meta.UpdatedAt.Format("2006-01-02 15:04"))
	}

	sb.WriteString("\n")
	sb.WriteString(CommentColorStyle.Render(i18n.T("tui.secretsDetailHints")))

	box := ModalContainer.Width(m.width - 10).Render(sb.String())
	return m.paintFrame(box)
}

// findSecretMeta looks up a secret's metadata by name.
func (m *Model) findSecretMeta(svc *keyring.Service, name string) (keyring.SecretMeta, bool) {
	all, err := svc.ListAll()
	if err != nil {
		return keyring.SecretMeta{}, false
	}
	for _, s := range all {
		if s.Name == name {
			return s, true
		}
	}
	return keyring.SecretMeta{}, false
}

// selectedSecretName returns the name of the currently selected secret.
func (m *Model) selectedSecretName() string {
	if m.secretsDetailMode && m.secretsDetailName != "" {
		return m.secretsDetailName
	}
	if m.modalSelectedIdx < len(m.secretsModalKeys) {
		return m.secretsModalKeys[m.modalSelectedIdx]
	}
	return ""
}

// reselectSecret restores the selection cursor to the secret with the given
// name after the list has been reloaded.
func (m *Model) reselectSecret(name string) {
	for i, n := range m.secretsModalKeys {
		if n == name {
			m.modalSelectedIdx = i
			if m.secretsDetailMode {
				m.secretsDetailName = name
			}
			return
		}
	}
	if m.modalSelectedIdx >= len(m.secretsModalKeys) && len(m.secretsModalKeys) > 0 {
		m.modalSelectedIdx = len(m.secretsModalKeys) - 1
	}
}

// startAddSecret initializes the multi-step add-secret form.
func (m *Model) startAddSecret() {
	m.modalMode = ModalAddSecret
	m.modalItems = nil
	m.formStepIndex = 0
	m.formValues = make([]string, 5) // name, value, description, tags, scope
	m.formError = ""
	m.formConfirmMode = false
	m.textInput.SetValue("")
	m.textInput.Placeholder = "Secret name (e.g. openai.api_key)"
}

// maskSecretValue renders a partially masked secret value for display.
func maskSecretValue(v string) string {
	if len(v) <= 8 {
		return "••••"
	}
	return v[:4] + strings.Repeat("•", 8) + v[len(v)-4:]
}

// renderSecretsList renders the secrets list modal with action hints.
func (m *Model) renderSecretsList(modalTitle string) string {
	maxVisible := m.maxModalVisible()

	if m.modalSelectedIdx < m.modalScrollOffset {
		m.modalScrollOffset = m.modalSelectedIdx
	}
	if m.modalSelectedIdx >= m.modalScrollOffset+maxVisible {
		m.modalScrollOffset = m.modalSelectedIdx - maxVisible + 1
	}
	if m.modalScrollOffset < 0 {
		m.modalScrollOffset = 0
	}

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(modalTitle) + "\n")

	if m.modalScrollOffset > 0 {
		sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.moreAbove")) + "\n")
	} else {
		sb.WriteString("\n")
	}

	endIdx := m.modalScrollOffset + maxVisible
	if endIdx > len(m.modalItems) {
		endIdx = len(m.modalItems)
	}
	for i := m.modalScrollOffset; i < endIdx; i++ {
		item := m.modalItems[i]
		if i == m.modalSelectedIdx {
			sb.WriteString(ModalItemActive.Render("> "+item) + "\n")
		} else {
			sb.WriteString(ModalItemInactive.Render("  "+item) + "\n")
		}
	}

	if endIdx < len(m.modalItems) {
		sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.moreBelow")) + "\n")
	}

	sb.WriteString("\n")
	sb.WriteString(HelpStyle.Render("  " + i18n.T("tui.secretsListHints")))

	modalView := ModalContainer.Render(sb.String())
	return m.paintFrame(modalView)
}

// splitCSV splits a comma-separated string into trimmed, non-empty fields.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
