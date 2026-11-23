package skills

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/xilistudios/lele/pkg/logger"
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)

const (
	MaxNameLength        = 64
	MaxDescriptionLength = 1024
)

type SkillMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type SkillInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Source      string `json:"source"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

func (info SkillInfo) validate() error {
	var errs error
	if info.Name == "" {
		errs = errors.Join(errs, errors.New("name is required"))
	} else {
		if len(info.Name) > MaxNameLength {
			errs = errors.Join(errs, fmt.Errorf("name exceeds %d characters", MaxNameLength))
		}
		if !namePattern.MatchString(info.Name) {
			errs = errors.Join(errs, errors.New("name must be alphanumeric with hyphens"))
		}
	}

	if info.Description == "" {
		errs = errors.Join(errs, errors.New("description is required"))
	} else if len(info.Description) > MaxDescriptionLength {
		errs = errors.Join(errs, fmt.Errorf("description exceeds %d character", MaxDescriptionLength))
	}
	return errs
}

type SkillsLoader struct {
	workspace       string
	workspaceSkills string
	globalSkills    string
	builtinSkills   string
	directorySkills string
	configMgr       *WorkspaceConfigManager
}

func NewSkillsLoader(workspace string, globalSkills string, builtinSkills string, directorySkills string) *SkillsLoader {
	loader := &SkillsLoader{
		workspace:       workspace,
		workspaceSkills: filepath.Join(workspace, "skills"),
		globalSkills:    globalSkills,
		builtinSkills:   builtinSkills,
		directorySkills: directorySkills,
	}

	if workspace != "" {
		if mgr, err := NewWorkspaceConfigManager(workspace); err == nil {
			loader.configMgr = mgr
		} else {
			slog.Warn("failed to load workspace skills config", "error", err)
		}
	}

	return loader
}

func (sl *SkillsLoader) SetConfigManager(mgr *WorkspaceConfigManager) {
	sl.configMgr = mgr
}

func (sl *SkillsLoader) GetConfigManager() *WorkspaceConfigManager {
	return sl.configMgr
}

func (sl *SkillsLoader) WorkspaceDir() string {
	return sl.workspace
}

func (sl *SkillsLoader) GlobalSkillsDir() string {
	return sl.globalSkills
}

type skillRoot struct {
	path   string
	source string
}

func (sl *SkillsLoader) skillRoots() []skillRoot {
	roots := []skillRoot{
		{path: sl.directorySkills, source: "directory"},
		{path: sl.workspaceSkills, source: "workspace"},
		{path: sl.globalSkills, source: "global"},
		{path: sl.builtinSkills, source: "builtin"},
	}
	present := make([]skillRoot, 0, len(roots))
	for _, root := range roots {
		if root.path != "" {
			present = append(present, root)
		}
	}
	return present
}

func (sl *SkillsLoader) ListSkills() []SkillInfo {
	skills := make([]SkillInfo, 0)
	claimed := make(map[string]struct{})

	for _, root := range sl.skillRoots() {
		dirs, err := os.ReadDir(root.path)
		if err != nil {
			continue
		}
		for _, dir := range dirs {
			if !dir.IsDir() {
				continue
			}
			skillFile := filepath.Join(root.path, dir.Name(), "SKILL.md")
			if _, err := os.Stat(skillFile); err != nil {
				continue
			}
			info := SkillInfo{
				Name:   dir.Name(),
				Path:   skillFile,
				Source: root.source,
			}
			if metadata := sl.getSkillMetadata(skillFile); metadata != nil {
				info.Description = metadata.Description
				info.Name = metadata.Name
			}
			if err := info.validate(); err != nil {
				slog.Warn("invalid skill from "+root.source, "name", info.Name, "error", err)
				continue
			}
			claim := skillClaimKey(info.Name)
			if _, shadowed := claimed[claim]; shadowed {
				continue
			}
			claimed[claim] = struct{}{}
			skills = append(skills, info)
		}
	}

	for i := range skills {
		skills[i].Enabled = sl.isSkillEnabled(skills[i].Name)
	}

	return skills
}

func skillClaimKey(name string) string {
	return strings.ToLower(normalizeSkillName(name))
}

func (sl *SkillsLoader) isSkillEnabled(name string) bool {
	if sl.configMgr == nil {
		return true
	}
	return sl.configMgr.IsEnabled(name)
}

func (sl *SkillsLoader) LoadSkill(name string) (string, bool) {
	if !isLoadableSkillName(name) {
		return "", false
	}

	for i, root := range sl.skillRoots() {
		skillFile := filepath.Join(root.path, name, "SKILL.md")
		content, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}
		body := string(content)
		declared := sl.metadataFromContent(body, name).Name
		if i == 0 && (declared == "" || strings.EqualFold(declared, name)) {
			return sl.stripFrontmatter(body), true
		}
		return sl.loadSkillByCatalogName(name)
	}

	return sl.loadSkillByCatalogName(name)
}

func (sl *SkillsLoader) loadSkillByCatalogName(name string) (string, bool) {
	catalog := sl.ListSkills()
	matches := []func(SkillInfo) bool{
		func(s SkillInfo) bool { return strings.EqualFold(s.Name, name) },
		func(s SkillInfo) bool { return strings.EqualFold(filepath.Base(filepath.Dir(s.Path)), name) },
	}
	for _, match := range matches {
		for _, s := range catalog {
			if !match(s) {
				continue
			}
			if content, ok := sl.skillBody(s); ok {
				return content, true
			}
		}
	}
	return "", false
}

func isLoadableSkillName(name string) bool {
	if name == "" || len(name) > MaxNameLength {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return false
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (sl *SkillsLoader) LoadSkillsForContext(skillNames []string) string {
	if len(skillNames) == 0 {
		return ""
	}

	var parts []string
	for _, name := range skillNames {
		if !sl.isSkillEnabled(name) {
			continue
		}

		content, ok := sl.LoadSkill(name)
		if ok {
			parts = append(parts, fmt.Sprintf("### Skill: %s\n\n%s", name, content))
		}
	}

	return strings.Join(parts, "\n\n---\n\n")
}

func (sl *SkillsLoader) BuildSkillsSummary(filter []string) string {
	return renderSkillsSummary(sl.VisibleSkills(filter))
}

func (sl *SkillsLoader) VisibleSkills(filter []string) []SkillInfo {
	allSkills := sl.ListSkills()
	allowed := skillNameSet(filter)

	visible := make([]SkillInfo, 0, len(allSkills))
	for _, s := range allSkills {
		if !s.Enabled {
			continue
		}
		if allowed != nil && !allowed[normalizeSkillName(s.Name)] {
			continue
		}
		visible = append(visible, s)
	}
	return visible
}

func (sl *SkillsLoader) TopSkills(filter []string, counts map[string]uint64, limit int) []SkillInfo {
	visible := sl.VisibleSkills(filter)
	slices.SortStableFunc(visible, func(a, b SkillInfo) int {
		if diff := cmp.Compare(counts[b.Name], counts[a.Name]); diff != 0 {
			return diff
		}
		return cmp.Compare(a.Name, b.Name)
	})
	if limit > 0 && len(visible) > limit {
		visible = visible[:limit]
	}
	return visible
}

func (sl *SkillsLoader) BuildTopSkillsSummary(filter []string, counts map[string]uint64, limit int) string {
	return renderSkillsSummary(sl.TopSkills(filter, counts, limit))
}

func (sl *SkillsLoader) SearchSkills(query string, filter []string) []SearchMatch {
	return Search(sl.VisibleSkills(filter), query, sl.skillBody)
}

func (sl *SkillsLoader) skillBody(s SkillInfo) (string, bool) {
	content, err := os.ReadFile(s.Path)
	if err != nil {
		return "", false
	}
	return sl.stripFrontmatter(string(content)), true
}

func renderSkillsSummary(list []SkillInfo) string {
	if len(list) == 0 {
		return ""
	}

	var lines []string
	for _, s := range list {
		escapedName := escapeXML(s.Name)
		escapedDesc := escapeXML(s.Description)
		escapedPath := escapeXML(s.Path)

		lines = append(lines, "  <skill>")
		lines = append(lines, fmt.Sprintf("    <name>%s</name>", escapedName))
		lines = append(lines, fmt.Sprintf("    <description>%s</description>", escapedDesc))
		lines = append(lines, fmt.Sprintf("    <location>%s</location>", escapedPath))
		lines = append(lines, fmt.Sprintf("    <source>%s</source>", s.Source))
		lines = append(lines, "  </skill>")
	}

	lines = append([]string{"<skills>"}, lines...)
	lines = append(lines, "</skills>")

	return strings.Join(lines, "\n")
}

func skillNameSet(filter []string) map[string]bool {
	if len(filter) == 0 {
		return nil
	}
	set := make(map[string]bool, len(filter))
	for _, name := range filter {
		name = normalizeSkillName(name)
		if name == "" {
			continue
		}
		set[name] = true
	}
	return set
}

func normalizeSkillName(name string) string {
	return strings.TrimSpace(name)
}

func SkillAllowed(filter []string, name string) bool {
	if len(filter) == 0 {
		return true
	}
	name = normalizeSkillName(name)
	for _, f := range filter {
		if normalizeSkillName(f) == name {
			return true
		}
	}
	return false
}

func (sl *SkillsLoader) SkillVisible(filter []string, name string) bool {
	return SkillAllowed(filter, name) && sl.isSkillEnabled(name)
}

func (sl *SkillsLoader) SkillResolvable(filter []string, name string) bool {
	if !sl.SkillVisible(filter, name) {
		return false
	}
	return sl.skillExists(name)
}

func (sl *SkillsLoader) skillExists(name string) bool {
	if !isLoadableSkillName(name) {
		return false
	}
	for _, root := range sl.skillRoots() {
		if _, err := os.Stat(filepath.Join(root.path, name, "SKILL.md")); err == nil {
			return true
		}
	}
	for _, s := range sl.ListSkills() {
		if strings.EqualFold(s.Name, name) {
			return true
		}
	}
	return false
}

func (sl *SkillsLoader) getSkillMetadata(skillPath string) *SkillMetadata {
	content, err := os.ReadFile(skillPath)
	if err != nil {
		logger.WarnCF("skills", "Failed to read skill metadata",
			map[string]interface{}{
				"skill_path": skillPath,
				"error":      err.Error(),
			})
		return nil
	}

	return sl.metadataFromContent(string(content), filepath.Base(filepath.Dir(skillPath)))
}

func (sl *SkillsLoader) metadataFromContent(content, fallbackName string) *SkillMetadata {
	frontmatter := sl.extractFrontmatter(content)
	if frontmatter == "" {
		return &SkillMetadata{Name: fallbackName}
	}

	var jsonMeta struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(frontmatter), &jsonMeta); err == nil {
		return &SkillMetadata{
			Name:        jsonMeta.Name,
			Description: jsonMeta.Description,
		}
	}

	yamlMeta := sl.parseSimpleYAML(frontmatter)
	return &SkillMetadata{
		Name:        yamlMeta["name"],
		Description: yamlMeta["description"],
	}
}

func (sl *SkillsLoader) parseSimpleYAML(content string) map[string]string {
	result := make(map[string]string)

	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			value = strings.Trim(value, "\"'")
			result[key] = value
		}
	}

	return result
}

func (sl *SkillsLoader) extractFrontmatter(content string) string {
	re := regexp.MustCompile(`(?s)^---(?:\r\n|\n|\r)(.*?)(?:\r\n|\n|\r)---`)
	match := re.FindStringSubmatch(content)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func (sl *SkillsLoader) stripFrontmatter(content string) string {
	re := regexp.MustCompile(`(?s)^---(?:\r\n|\n|\r)(.*?)(?:\r\n|\n|\r)---(?:\r\n|\n|\r)*`)
	return re.ReplaceAllString(content, "")
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
