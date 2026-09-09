package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
	Enabled     bool   `json:"enabled"` // Whether skill is enabled in workspace config
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
	workspaceSkills string // workspace skills (项目级别)
	globalSkills    string // 全局 skills (~/.lele/skills)
	builtinSkills   string // 内置 skills
	configMgr       *WorkspaceConfigManager
}

func NewSkillsLoader(workspace string, globalSkills string, builtinSkills string) *SkillsLoader {
	loader := &SkillsLoader{
		workspace:       workspace,
		workspaceSkills: filepath.Join(workspace, "skills"),
		globalSkills:    globalSkills, // ~/.lele/skills
		builtinSkills:   builtinSkills,
	}

	// Try to load workspace config
	if workspace != "" {
		if mgr, err := NewWorkspaceConfigManager(workspace); err == nil {
			loader.configMgr = mgr
		} else {
			slog.Warn("failed to load workspace skills config", "error", err)
		}
	}

	return loader
}

// SetConfigManager sets the workspace config manager (for testing or external injection).
func (sl *SkillsLoader) SetConfigManager(mgr *WorkspaceConfigManager) {
	sl.configMgr = mgr
}

// GetConfigManager returns the workspace config manager.
func (sl *SkillsLoader) GetConfigManager() *WorkspaceConfigManager {
	return sl.configMgr
}

func (sl *SkillsLoader) ListSkills() []SkillInfo {
	skills := make([]SkillInfo, 0)

	if sl.workspaceSkills != "" {
		if dirs, err := os.ReadDir(sl.workspaceSkills); err == nil {
			for _, dir := range dirs {
				if dir.IsDir() {
					skillFile := filepath.Join(sl.workspaceSkills, dir.Name(), "SKILL.md")
					if _, err := os.Stat(skillFile); err == nil {
						info := SkillInfo{
							Name:   dir.Name(),
							Path:   skillFile,
							Source: "workspace",
						}
						metadata := sl.getSkillMetadata(skillFile)
						if metadata != nil {
							info.Description = metadata.Description
							info.Name = metadata.Name
						}
						if err := info.validate(); err != nil {
							slog.Warn("invalid skill from workspace", "name", info.Name, "error", err)
							continue
						}
						skills = append(skills, info)
					}
				}
			}
		}
	}

	// 全局 skills (~/.lele/skills) - 被 workspace skills 覆盖
	if sl.globalSkills != "" {
		if dirs, err := os.ReadDir(sl.globalSkills); err == nil {
			for _, dir := range dirs {
				if dir.IsDir() {
					skillFile := filepath.Join(sl.globalSkills, dir.Name(), "SKILL.md")
					if _, err := os.Stat(skillFile); err == nil {
						// 检查是否已被 workspace skills 覆盖
						exists := false
						for _, s := range skills {
							if s.Name == dir.Name() && s.Source == "workspace" {
								exists = true
								break
							}
						}
						if exists {
							continue
						}

						info := SkillInfo{
							Name:   dir.Name(),
							Path:   skillFile,
							Source: "global",
						}
						metadata := sl.getSkillMetadata(skillFile)
						if metadata != nil {
							info.Description = metadata.Description
							info.Name = metadata.Name
						}
						if err := info.validate(); err != nil {
							slog.Warn("invalid skill from global", "name", info.Name, "error", err)
							continue
						}
						skills = append(skills, info)
					}
				}
			}
		}
	}

	if sl.builtinSkills != "" {
		if dirs, err := os.ReadDir(sl.builtinSkills); err == nil {
			for _, dir := range dirs {
				if dir.IsDir() {
					skillFile := filepath.Join(sl.builtinSkills, dir.Name(), "SKILL.md")
					if _, err := os.Stat(skillFile); err == nil {
						// 检查是否已被 workspace 或 global skills 覆盖
						exists := false
						for _, s := range skills {
							if s.Name == dir.Name() && (s.Source == "workspace" || s.Source == "global") {
								exists = true
								break
							}
						}
						if exists {
							continue
						}

						info := SkillInfo{
							Name:   dir.Name(),
							Path:   skillFile,
							Source: "builtin",
						}
						metadata := sl.getSkillMetadata(skillFile)
						if metadata != nil {
							info.Description = metadata.Description
							info.Name = metadata.Name
						}
						if err := info.validate(); err != nil {
							slog.Warn("invalid skill from builtin", "name", info.Name, "error", err)
							continue
						}
						skills = append(skills, info)
					}
				}
			}
		}
	}

	// Set Enabled field based on workspace config
	for i := range skills {
		skills[i].Enabled = sl.isSkillEnabled(skills[i].Name)
	}

	return skills
}

// isSkillEnabled checks if a skill is enabled based on workspace config.
// If no config manager is set, all skills are enabled by default.
func (sl *SkillsLoader) isSkillEnabled(name string) bool {
	if sl.configMgr == nil {
		return true
	}
	return sl.configMgr.IsEnabled(name)
}

func (sl *SkillsLoader) LoadSkill(name string) (string, bool) {
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return "", false
	}

	// 1. 优先从 workspace skills 加载（项目级别）
	if sl.workspaceSkills != "" {
		skillFile := filepath.Join(sl.workspaceSkills, name, "SKILL.md")
		if content, err := os.ReadFile(skillFile); err == nil {
			return sl.stripFrontmatter(string(content)), true
		}
	}

	// 2. 其次从全局 skills 加载 (~/.lele/skills)
	if sl.globalSkills != "" {
		skillFile := filepath.Join(sl.globalSkills, name, "SKILL.md")
		if content, err := os.ReadFile(skillFile); err == nil {
			return sl.stripFrontmatter(string(content)), true
		}
	}

	// 3. 最后从内置 skills 加载
	if sl.builtinSkills != "" {
		skillFile := filepath.Join(sl.builtinSkills, name, "SKILL.md")
		if content, err := os.ReadFile(skillFile); err == nil {
			return sl.stripFrontmatter(string(content)), true
		}
	}

	return "", false
}

func (sl *SkillsLoader) LoadSkillsForContext(skillNames []string) string {
	if len(skillNames) == 0 {
		return ""
	}

	var parts []string
	for _, name := range skillNames {
		// Skip disabled skills
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

// BuildSkillsSummary renders the <skills> block injected into the system
// prompt: one <skill> entry per enabled installed skill (name, description,
// SKILL.md path, source).
//
// filter limits the block to an allowlist of skill names and backs the
// per-agent `skills:` config (AgentInstance.SkillsFilter):
//   - nil / empty → every enabled skill is listed (the pre-filter behaviour,
//     so callers without a per-agent allowlist pass nil);
//   - non-empty → only skills whose name matches an entry, case-sensitively,
//     compared after trimming surrounding whitespace on both sides. Names in
//     the filter that are not installed are simply absent; that is not an
//     error (a skill may be uninstalled elsewhere).
//
// Returns "" when there is nothing to list, so the caller drops the whole
// "# Skills" section instead of emitting an empty block.
func (sl *SkillsLoader) BuildSkillsSummary(filter []string) string {
	allSkills := sl.ListSkills()
	if len(allSkills) == 0 {
		return ""
	}

	allowed := skillNameSet(filter)

	var lines []string
	for _, s := range allSkills {
		// Skip disabled skills in summary
		if !s.Enabled {
			continue
		}
		// Skip skills outside the per-agent allowlist (nil set = no filtering)
		if allowed != nil && !allowed[normalizeSkillName(s.Name)] {
			continue
		}

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
	if len(lines) == 0 {
		return ""
	}

	lines = append([]string{"<skills>"}, lines...)
	lines = append(lines, "</skills>")

	return strings.Join(lines, "\n")
}

// skillNameSet turns an allowlist of skill names into a lookup set. It returns
// nil for a nil/empty allowlist, which callers read as "no filtering", keeping
// the "all skills" vs. "only these" distinction in one place. Entries that are
// empty after trimming are dropped.
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

// normalizeSkillName canonicalises a skill name for allowlist comparison.
// Names come from SKILL.md frontmatter or from user-written config, so stray
// whitespace must not silently break a match.
func normalizeSkillName(name string) string {
	return strings.TrimSpace(name)
}

// SkillAllowed reports whether name passes the per-agent allowlist filter.
// It is the exported, single source of truth for the matching semantics used
// by BuildSkillsSummary, so other renderers (e.g. the agent context builder's
// skill-content path) stay consistent with the system prompt.
//
// An empty filter allows everything (back-compat); otherwise the comparison is
// case-sensitive and whitespace-trimmed on both sides.
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

	frontmatter := sl.extractFrontmatter(string(content))
	if frontmatter == "" {
		return &SkillMetadata{
			Name: filepath.Base(filepath.Dir(skillPath)),
		}
	}

	// Try JSON first (for backward compatibility)
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

	// Fall back to simple YAML parsing
	yamlMeta := sl.parseSimpleYAML(frontmatter)
	return &SkillMetadata{
		Name:        yamlMeta["name"],
		Description: yamlMeta["description"],
	}
}

// parseSimpleYAML parses simple key: value YAML format
// Example: name: github\n description: "..."
// Normalizes line endings to handle \n (Unix), \r\n (Windows), and \r (classic Mac)
func (sl *SkillsLoader) parseSimpleYAML(content string) map[string]string {
	result := make(map[string]string)

	// Normalize line endings: convert \r\n and \r to \n
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
			// Remove quotes if present
			value = strings.Trim(value, "\"'")
			result[key] = value
		}
	}

	return result
}

func (sl *SkillsLoader) extractFrontmatter(content string) string {
	// Support \n (Unix), \r\n (Windows), and \r (classic Mac) line endings for frontmatter blocks
	// (?s) enables DOTALL so . matches newlines;
	// ^--- at start, then ... --- at start of line, honoring all three line ending types
	re := regexp.MustCompile(`(?s)^---(?:\r\n|\n|\r)(.*?)(?:\r\n|\n|\r)---`)
	match := re.FindStringSubmatch(content)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func (sl *SkillsLoader) stripFrontmatter(content string) string {
	// Support \n (Unix), \r\n (Windows), and \r (classic Mac) line endings for frontmatter blocks
	// (?s) enables DOTALL so . matches newlines;
	// ^--- at start, then ... --- at start of line, honoring all three line ending types
	// Match zero or more trailing line endings after closing --- (handles both with and without blank lines)
	re := regexp.MustCompile(`(?s)^---(?:\r\n|\n|\r)(.*?)(?:\r\n|\n|\r)---(?:\r\n|\n|\r)*`)
	return re.ReplaceAllString(content, "")
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
