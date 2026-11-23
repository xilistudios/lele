package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/skills"
)

const LoadSkillName = "load_skill"

type LoadSkillTool struct {
	loader   *skills.SkillsLoader
	usage    *skills.UsageStore
	agentID  string
	filter   []string
	onLoaded func(names []string)
}

var _ Tool = (*LoadSkillTool)(nil)

func NewLoadSkillTool(loader *skills.SkillsLoader, usage *skills.UsageStore, agentID string, filter []string, onLoaded func([]string)) *LoadSkillTool {
	return &LoadSkillTool{loader: loader, usage: usage, agentID: agentID, filter: filter, onLoaded: onLoaded}
}

func (t *LoadSkillTool) Name() string { return LoadSkillName }

func (t *LoadSkillTool) Description() string {
	return "Load the full instructions (SKILL.md) of one or more skills into the context. " +
		"Pass skill for a single name or skills for several. " +
		"Use search_skill first to discover skill names by keyword, and load only the skills you need."
}

func (t *LoadSkillTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"skill": map[string]interface{}{
				"type":        "string",
				"description": "Single skill name (see the '# Skills' section of the context or search_skill results)",
			},
			"skills": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Skill names to load in one call. Duplicates are ignored.",
			},
		},
	}
}

func (t *LoadSkillTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.loader == nil {
		return ErrorResult("load_skill is not configured (missing skills loader)")
	}
	names := namesFromArgs(args, "skill", "skills")
	if len(names) == 0 {
		return ErrorResult(`missing required argument "skill" (or "skills"); use search_skill to discover skill names`)
	}

	visible := make(map[string]skills.SkillInfo)
	for _, s := range t.loader.VisibleSkills(t.filter) {
		visible[s.Name] = s
	}

	var blocks []string
	var loaded []string
	var missing []string
	for _, name := range names {
		if _, ok := visible[name]; !ok {
			missing = append(missing, name)
			continue
		}
		content, ok := t.loader.LoadSkill(name)
		if !ok {
			missing = append(missing, name)
			continue
		}
		t.usage.Inc(t.agentID, name)
		loaded = append(loaded, name)
		blocks = append(blocks, fmt.Sprintf("### Skill: %s\n\n%s", name, content))
	}

	if len(loaded) == 0 {
		return ErrorResult(fmt.Sprintf(
			"skill(s) %s not found or not available for this agent; use search_skill to discover skill names",
			quoteNames(missing)))
	}

	if t.onLoaded != nil {
		t.onLoaded(loaded)
	}

	var b strings.Builder
	if len(blocks) > 1 {
		fmt.Fprintf(&b, "Loaded %d skill(s) into the context.\n\n", len(blocks))
	}
	b.WriteString(strings.Join(blocks, "\n\n---\n\n"))
	if len(missing) > 0 {
		fmt.Fprintf(&b, "\n\nNot found or not available: %s. Use search_skill to discover skill names.", quoteNames(missing))
	}
	return NewToolResult(b.String())
}
