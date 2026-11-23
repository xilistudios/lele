package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/skills"
)

const LoadSkillName = "load_skill"

type LoadSkillTool struct {
	loader  *skills.SkillsLoader
	usage   *skills.UsageStore
	agentID string
	filter  []string
	pin     func(sessionKey string, names []string) (pinned, already, rejected []string)
	unpin   func(sessionKey string, names []string) (removed, absent []string)
	pinned  func(sessionKey string) []string
}

var _ Tool = (*LoadSkillTool)(nil)

func NewLoadSkillTool(
	loader *skills.SkillsLoader,
	usage *skills.UsageStore,
	agentID string,
	filter []string,
	pin func(string, []string) ([]string, []string, []string),
	unpin func(string, []string) ([]string, []string),
	pinned func(string) []string,
) *LoadSkillTool {
	return &LoadSkillTool{
		loader:  loader,
		usage:   usage,
		agentID: agentID,
		filter:  filter,
		pin:     pin,
		unpin:   unpin,
		pinned:  pinned,
	}
}

func (t *LoadSkillTool) Name() string { return LoadSkillName }

func (t *LoadSkillTool) Description() string {
	return "Load the full instructions (SKILL.md) of one or more skills into the context. " +
		"Pass skill for a single name or skills for several. " +
		"Use search_skill first to discover skill names by keyword, and load only the skills you need. " +
		"Pins are per session and budgeted; pass unload:true to remove a pin."
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
			"unload": map[string]interface{}{
				"type":        "boolean",
				"description": "Set true to unload (unpin) the named skills from this session instead of loading them",
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

	_, sessionKey := AgentToolContextFromCtx(ctx)

	unload, _ := args["unload"].(bool)
	if unload {
		return t.executeUnload(sessionKey, names)
	}

	candidates, bodies, missing := t.collect(names)
	if len(candidates) == 0 {
		return ErrorResult(missingSkillsMessage(missing))
	}

	if SubagentScopeFromCtx(ctx) {
		for _, name := range candidates {
			t.usage.Inc(t.agentID, name)
		}
		return t.loadedResult(sessionKey, candidates, bodies, missing, nil, nil, true)
	}

	if t.pin == nil {
		return ErrorResult("load_skill is not configured (missing session pin owner)")
	}

	pinned, already, rejected := t.pin(sessionKey, candidates)
	for _, name := range pinned {
		t.usage.Inc(t.agentID, name)
	}

	result := t.loadedResult(sessionKey, pinned, bodies, missing, already, rejected, false)
	if len(pinned) == 0 && len(rejected) > 0 {
		return ErrorResult(result.ForLLM)
	}
	return result
}

func (t *LoadSkillTool) collect(names []string) (candidates []string, bodies map[string]string, missing []string) {
	visible := make(map[string]skills.SkillInfo)
	for _, s := range t.loader.VisibleSkills(t.filter) {
		visible[s.Name] = s
	}

	for _, name := range names {
		if _, ok := visible[name]; !ok {
			missing = append(missing, name)
			continue
		}
		body, ok := t.loader.LoadSkill(name)
		if !ok {
			missing = append(missing, name)
			continue
		}
		if bodies == nil {
			bodies = make(map[string]string, len(names))
		}
		bodies[name] = body
		candidates = append(candidates, name)
	}
	return candidates, bodies, missing
}

func (t *LoadSkillTool) loadedResult(sessionKey string, loaded []string, bodies map[string]string, missing, already, rejected []string, subagent bool) *ToolResult {
	var parts []string
	if len(loaded) > 1 {
		parts = append(parts, fmt.Sprintf("Loaded %d skill(s) into the context.", len(loaded)))
	}
	if len(loaded) > 0 {
		var blocks []string
		for _, name := range loaded {
			blocks = append(blocks, fmt.Sprintf("### Skill: %s\n\n%s", name, bodies[name]))
		}
		parts = append(parts, strings.Join(blocks, "\n\n---\n\n"))
	}
	if subagent {
		parts = append(parts, "Loaded for this subagent run only; not pinned into any session prompt.")
	}
	if len(already) > 0 {
		parts = append(parts, fmt.Sprintf("Already in context for this session: %s. No need to load again.", quoteNames(already)))
	}
	if len(rejected) > 0 {
		parts = append(parts, t.budgetMessage(sessionKey, rejected))
	}
	if len(missing) > 0 {
		parts = append(parts, fmt.Sprintf("Not found or not available: %s. Use search_skill to discover skill names.", quoteNames(missing)))
	}
	return NewToolResult(strings.Join(parts, "\n\n"))
}

func (t *LoadSkillTool) budgetMessage(sessionKey string, rejected []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Not loaded: %s. This session's pinned-skill budget is full (max %d skills / %d KiB of SKILL.md text).",
		quoteNames(rejected), skills.MaxPinnedSkillsPerSession, skills.MaxPinnedSkillBytesPerSession>>10)
	if t.pinned != nil {
		if current := t.pinned(sessionKey); len(current) > 0 {
			fmt.Fprintf(&b, " Currently pinned: %s.", quoteNames(current))
		}
	}
	b.WriteString(" Unload one with load_skill {\"skill\": \"<name>\", \"unload\": true} and retry.")
	return b.String()
}

func (t *LoadSkillTool) executeUnload(sessionKey string, names []string) *ToolResult {
	if t.unpin == nil {
		return ErrorResult("load_skill is not configured (missing session pin owner)")
	}
	removed, absent := t.unpin(sessionKey, names)

	var parts []string
	if len(removed) > 0 {
		parts = append(parts, fmt.Sprintf("Unloaded %s from this session.", quoteNames(removed)))
	}
	if len(absent) > 0 {
		parts = append(parts, fmt.Sprintf("Not pinned in this session: %s.", quoteNames(absent)))
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("Not pinned in this session: %s.", quoteNames(names)))
	}
	return NewToolResult(strings.Join(parts, " "))
}

func missingSkillsMessage(missing []string) string {
	return fmt.Sprintf("skill(s) %s not found or not available for this agent; use search_skill to discover skill names",
		quoteNames(missing))
}
