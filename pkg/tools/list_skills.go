package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/skills"
)

const ListSkillsName = "list_skills"

const (
	defaultSkillsPageSize = 20
	maxSkillsPageSize     = 100
)

type ListSkillsTool struct {
	loader    *skills.SkillsLoader
	usage     *skills.UsageStore
	agentID   string
	filter    []string
	inContext func(sessionKey string) []string
}

var _ Tool = (*ListSkillsTool)(nil)

func NewListSkillsTool(loader *skills.SkillsLoader, usage *skills.UsageStore, agentID string, filter []string, inContext func(sessionKey string) []string) *ListSkillsTool {
	return &ListSkillsTool{loader: loader, usage: usage, agentID: agentID, filter: filter, inContext: inContext}
}

func (t *ListSkillsTool) Name() string { return ListSkillsName }

func (t *ListSkillsTool) Description() string {
	return "List the skill catalog, paginated and ordered by usage frequency (most used first), " +
		"excluding skills already in this session's context. Returns names and short descriptions only. " +
		"Use search_skill to find skills by keyword, and load_skill to load full instructions into the context."
}

func (t *ListSkillsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"page": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"description": "1-based page number (default 1)",
			},
			"page_size": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     maxSkillsPageSize,
				"description": "skills per page (default 20, max 100)",
			},
		},
	}
}

func (t *ListSkillsTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.loader == nil {
		return ErrorResult("list_skills is not configured (missing skills loader)")
	}

	counts := t.usage.CountsFor(t.agentID)
	ordered := t.loader.TopSkills(t.filter, counts, 0)

	excluded := make(map[string]struct{})
	if t.inContext != nil {
		_, sessionKey := AgentToolContextFromCtx(ctx)
		for _, name := range t.inContext(sessionKey) {
			excluded[name] = struct{}{}
		}
	}
	remaining := make([]skills.SkillInfo, 0, len(ordered))
	for _, s := range ordered {
		if _, skip := excluded[s.Name]; !skip {
			remaining = append(remaining, s)
		}
	}
	if len(remaining) == 0 {
		return NewToolResult("No additional skills to list: every available skill is already in the context (or the catalog is empty).")
	}

	pageSize := positiveIntArg(args, "page_size", defaultSkillsPageSize, maxSkillsPageSize)
	totalPages := (len(remaining) + pageSize - 1) / pageSize
	page := positiveIntArg(args, "page", 1, 0)
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if end > len(remaining) {
		end = len(remaining)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Skill catalog: %d skill(s) not yet in context, page %d/%d (page size %d). Ordered by usage frequency.\n",
		len(remaining), page, totalPages, pageSize)
	for _, s := range remaining[start:end] {
		fmt.Fprintf(&b, "\n- %s (%s)", s.Name, s.Source)
		if desc := shortDescription(s.Description); desc != "" {
			fmt.Fprintf(&b, " — %s", desc)
		}
		b.WriteString(usageSuffix(counts[s.Name]))
		fmt.Fprintf(&b, "\n  location: %s", s.Path)
	}
	b.WriteString("\n\nCall load_skill with {\"skill\": \"<name>\"} — or \"skills\": [\"<a>\", \"<b>\"] for several — to load full instructions into the context.\n")
	return NewToolResult(b.String())
}
