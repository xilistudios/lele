package tools

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/xilistudios/lele/pkg/skills"
)

const SearchSkillName = "search_skill"

const (
	defaultSearchSkillLimit = 10
	maxSearchSkillLimit     = 50
)

type SearchSkillTool struct {
	loader  *skills.SkillsLoader
	usage   *skills.UsageStore
	agentID string
	filter  []string
}

var _ Tool = (*SearchSkillTool)(nil)

func NewSearchSkillTool(loader *skills.SkillsLoader, usage *skills.UsageStore, agentID string, filter []string) *SearchSkillTool {
	return &SearchSkillTool{loader: loader, usage: usage, agentID: agentID, filter: filter}
}

func (t *SearchSkillTool) Name() string { return SearchSkillName }

func (t *SearchSkillTool) Description() string {
	return "Search the skill catalog by keywords, ranked by relevance (name matches weigh more than " +
		"description, then SKILL.md content). Returns names, descriptions and locations only. " +
		"Use load_skill to load one or more skills' full instructions into the context."
}

func (t *SearchSkillTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Keywords to search for (case-insensitive, e.g. \"weather forecast\" or \"github pr\")",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     maxSearchSkillLimit,
				"description": "maximum results to return (default 10, max 50)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *SearchSkillTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.loader == nil {
		return ErrorResult("search_skill is not configured (missing skills loader)")
	}
	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return ErrorResult(`missing required argument "query"; pass keywords like {"query": "weather"}`)
	}

	matches := t.loader.SearchSkills(query, t.filter)
	if len(matches) == 0 {
		return NewToolResult(fmt.Sprintf("No skills match %q. Try broader or different keywords.", query))
	}

	counts := t.usage.CountsFor(t.agentID)
	slices.SortStableFunc(matches, func(a, b skills.SearchMatch) int {
		if diff := cmp.Compare(b.Score, a.Score); diff != 0 {
			return diff
		}
		if diff := cmp.Compare(counts[b.Skill.Name], counts[a.Skill.Name]); diff != 0 {
			return diff
		}
		return cmp.Compare(a.Skill.Name, b.Skill.Name)
	})

	limit := positiveIntArg(args, "limit", defaultSearchSkillLimit, maxSearchSkillLimit)
	total := len(matches)
	if len(matches) > limit {
		matches = matches[:limit]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d skill(s) matching %q, showing %d by relevance:\n", total, query, len(matches))
	for _, m := range matches {
		fmt.Fprintf(&b, "\n- %s (%s)", m.Skill.Name, m.Skill.Source)
		if desc := shortDescription(m.Skill.Description); desc != "" {
			fmt.Fprintf(&b, " — %s", desc)
		}
		b.WriteString(usageSuffix(counts[m.Skill.Name]))
		fmt.Fprintf(&b, "\n  location: %s", m.Skill.Path)
	}
	b.WriteString("\n\nCall load_skill with {\"skill\": \"<name>\"} — or \"skills\": [\"<a>\", \"<b>\"] for several — to load full instructions into the context.\n")
	return NewToolResult(b.String())
}
