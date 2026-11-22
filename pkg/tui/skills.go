package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

type skillsScanMsg struct {
	repo string
}

type skillsScanResultMsg struct {
	skills []channels.ScannedSkill
	repo   string
	err    error
}

type skillsInstallMsg struct {
	repo   string
	skills []string
}

type skillsInstallResultMsg struct {
	installed []string
	err       error
}

type skillToggleMsg struct {
	skillName string
	enabled   bool
}

type skillToggleResultMsg struct {
	skillName string
	enabled   bool
	err       error
}

type skillDeleteMsg struct {
	skillName string
}

type skillDeleteResultMsg struct {
	skillName string
	err       error
}

func (m *Model) skillsLoader() *skills.SkillsLoader {
	if m.agentLoop == nil {
		return nil
	}
	return m.agentLoop.SkillsLoader()
}

func (m *Model) skillInstaller() *skills.SkillInstaller {
	if m.agentLoop == nil {
		return nil
	}
	return m.agentLoop.SkillInstaller()
}

func (m *Model) loadSkillsList() {
	m.modalItems = nil
	m.skillsModalKeys = nil

	loader := m.skillsLoader()
	if loader == nil {
		m.modalItems = append(m.modalItems, i18n.T("tui.skillsUnavailable"))
		return
	}

	allSkills := loader.ListSkills()
	if len(allSkills) == 0 {
		m.modalItems = append(m.modalItems, i18n.T("tui.noSkillsInstalled"))
		m.skillsModalKeys = append(m.skillsModalKeys, "")
	} else {
		sort.Slice(allSkills, func(i, j int) bool {
			return strings.ToLower(allSkills[i].Name) < strings.ToLower(allSkills[j].Name)
		})
		for _, s := range allSkills {
			m.modalItems = append(m.modalItems, formatSkillItem(s.Name, s.Description, s.Enabled, s.Source))
			m.skillsModalKeys = append(m.skillsModalKeys, s.Name)
		}
	}

	m.modalItems = append(m.modalItems, i18n.T("tui.actionsSeparator"))
	m.skillsModalKeys = append(m.skillsModalKeys, "")
	m.modalItems = append(m.modalItems, i18n.T("tui.installFromGitHub"))
	m.skillsModalKeys = append(m.skillsModalKeys, "__install__")
	m.clampModalCursor()
}

func (m *Model) handleSkillsEnter() tea.Cmd {
	if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.skillsModalKeys) {
		return nil
	}

	selectedKey := m.skillsModalKeys[m.modalSelectedIdx]

	switch selectedKey {
	case "__install__":
		m.modalMode = ModalSkillInstall
		m.textInput.SetValue("")
		m.textInput.Placeholder = i18n.T("tui.skillRepoPlaceholder")
		m.formError = ""
		return m.tickCmd()
	case "":
		return nil
	default:
		return m.toggleSkillCmd(selectedKey)
	}
}

func (m *Model) toggleSkillCmd(skillName string) tea.Cmd {
	return func() tea.Msg {
		loader := m.skillsLoader()
		if loader == nil {
			return skillToggleResultMsg{skillName: skillName, err: fmt.Errorf("skills loader not available")}
		}

		configMgr := loader.GetConfigManager()
		if configMgr == nil {
			return skillToggleResultMsg{skillName: skillName, err: fmt.Errorf("workspace config not available")}
		}

		enabled, err := configMgr.Toggle(skillName)
		return skillToggleResultMsg{skillName: skillName, enabled: enabled, err: err}
	}
}

func (m *Model) handleSkillInstallSubmit() tea.Cmd {
	repo := strings.TrimSpace(m.textInput.Value())
	if repo == "" {
		m.formError = "Repository is required"
		return nil
	}

	repo = cleanRepoURL(repo)
	if repo == "" {
		m.formError = "Invalid repository format. Use: owner/repo"
		return nil
	}

	m.skillsScanRepo = repo
	m.formError = ""
	m.skillsFeedback = ""

	return func() tea.Msg {
		installer := m.skillInstaller()
		if installer == nil {
			return skillsScanResultMsg{repo: repo, err: fmt.Errorf("skill installer not available")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		scanned, err := installer.ScanGitHubRepo(ctx, repo)
		if err != nil {
			return skillsScanResultMsg{repo: repo, err: err}
		}

		result := make([]channels.ScannedSkill, 0, len(scanned))
		for _, s := range scanned {
			result = append(result, channels.ScannedSkill{
				Name:        s.Name,
				Description: s.Description,
				Path:        s.Path,
				HasSKILL:    s.HasSKILL,
			})
		}
		return skillsScanResultMsg{skills: result, repo: repo}
	}
}

func (m *Model) handleSkillPickerEnter() tea.Cmd {
	if len(m.skillsScanResults) == 0 {
		return nil
	}

	selected := make([]string, 0)
	for idx, isSelected := range m.skillsSelectedMap {
		if isSelected && idx < len(m.skillsScanResults) {
			selected = append(selected, m.skillsScanResults[idx].Path)
		}
	}

	if len(selected) == 0 {
		m.formError = "No skills selected"
		return nil
	}

	repo := m.skillsScanRepo
	return func() tea.Msg {
		installer := m.skillInstaller()
		if installer == nil {
			return skillsInstallResultMsg{err: fmt.Errorf("skill installer not available")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		installed, err := installer.InstallMultiple(ctx, repo, selected)
		return skillsInstallResultMsg{installed: installed, err: err}
	}
}

func (m *Model) handleSkillPickerToggle() {
	if m.skillsSelectedMap == nil {
		m.skillsSelectedMap = make(map[int]bool)
	}
	m.skillsSelectedMap[m.modalSelectedIdx] = !m.skillsSelectedMap[m.modalSelectedIdx]
}

func (m *Model) deleteSkillCmd(skillName string) tea.Cmd {
	return func() tea.Msg {
		installer := m.skillInstaller()
		if installer == nil {
			return skillDeleteResultMsg{skillName: skillName, err: fmt.Errorf("skill installer not available")}
		}

		err := installer.Uninstall(skillName)
		return skillDeleteResultMsg{skillName: skillName, err: err}
	}
}

func (m *Model) handleSkillScanResult(msg skillsScanResultMsg) tea.Cmd {
	if msg.err != nil {
		m.formError = fmt.Sprintf("Scan failed: %v", msg.err)
		return m.tickCmd()
	}

	if len(msg.skills) == 0 {
		m.formError = "No skills found in this repository"
		return m.tickCmd()
	}

	m.skillsScanResults = msg.skills
	m.skillsScanRepo = msg.repo
	m.formError = ""

	if len(msg.skills) == 1 {
		return func() tea.Msg {
			installer := m.skillInstaller()
			if installer == nil {
				return skillsInstallResultMsg{err: fmt.Errorf("skill installer not available")}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			installed, err := installer.InstallMultiple(ctx, msg.repo, []string{msg.skills[0].Path})
			return skillsInstallResultMsg{installed: installed, err: err}
		}
	}

	m.skillsSelectedMap = make(map[int]bool)
	for i := range msg.skills {
		m.skillsSelectedMap[i] = true
	}
	m.modalMode = ModalSkillPicker
	m.modalSelectedIdx = 0
	m.modalScrollOffset = 0
	return m.tickCmd()
}

func (m *Model) handleSkillInstallResult(msg skillsInstallResultMsg) tea.Cmd {
	if msg.err != nil {
		m.formError = fmt.Sprintf("Install failed: %v", msg.err)
		return m.tickCmd()
	}

	if len(msg.installed) == 0 {
		m.skillsFeedback = i18n.T("tui.skillScanNoResults")
	} else {
		m.skillsFeedback = fmt.Sprintf("%s (%s)",
			fmt.Sprintf(i18n.T("tui.skillInstallSuccess"), len(msg.installed)),
			strings.Join(msg.installed, ", "))
	}

	m.skillsScanResults = nil
	m.skillsSelectedMap = nil
	m.modalMode = ModalSkills
	m.loadSkillsList()
	return m.tickCmd()
}

func (m *Model) handleSkillToggleResult(msg skillToggleResultMsg) tea.Cmd {
	if msg.err != nil {
		m.skillsFeedback = fmt.Sprintf("Toggle failed: %v", msg.err)
	} else {
		state := i18n.T("tui.skillEnabled")
		if !msg.enabled {
			state = i18n.T("tui.skillDisabled")
		}
		m.skillsFeedback = fmt.Sprintf(i18n.T("tui.skillToggleSuccess"), msg.skillName, state)
	}
	m.loadSkillsList()
	return m.tickCmd()
}

func (m *Model) handleSkillDeleteResult(msg skillDeleteResultMsg) tea.Cmd {
	if msg.err != nil {
		m.skillsFeedback = fmt.Sprintf("Delete failed: %v", msg.err)
	} else {
		m.skillsFeedback = fmt.Sprintf("Removed: %s", msg.skillName)
	}
	m.loadSkillsList()
	m.clampModalCursor()
	return m.tickCmd()
}

func formatSkillItem(name, description string, enabled bool, source string) string {
	status := "●"
	if !enabled {
		status = "○"
	}

	desc := truncateRightCells(description, 50)

	sourceTag := ""
	if source != "" {
		sourceTag = " [" + source + "]"
	}

	return fmt.Sprintf("%s %-15s — %s%s", status, name, desc, sourceTag)
}

func formatPickerItem(name, description string, selected bool) string {
	checkbox := "[ ]"
	if selected {
		checkbox = "[x]"
	}

	desc := truncateRightCells(description, 45)

	return fmt.Sprintf("%s %-15s — %s", checkbox, name, desc)
}

func cleanRepoURL(input string) string {
	input = strings.TrimSpace(input)
	input = strings.TrimRight(input, "/")

	prefixes := []string{
		"https://github.com/",
		"http://github.com/",
		"github.com/",
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(input, prefix) {
			input = strings.TrimPrefix(input, prefix)
			break
		}
	}

	parts := strings.Split(input, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}

	return ""
}

func sortSkillsByName(skills []channels.ScannedSkill) {
	sort.Slice(skills, func(i, j int) bool {
		return strings.ToLower(skills[i].Name) < strings.ToLower(skills[j].Name)
	})
}
