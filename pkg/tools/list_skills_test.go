package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/skills"
)

func listSkillNames(count int) []string {
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("skill-%02d", i+1)
	}
	return names
}

func TestListSkillsTool_PaginatesTwentyPerPage(t *testing.T) {
	loader := newSkillToolLoader(t, listSkillNames(25)...)
	tool := NewListSkillsTool(loader, nil, "coder", nil, nil)

	first := tool.Execute(context.Background(), map[string]interface{}{})
	if first.IsError {
		t.Fatalf("list failed: %s", first.ForLLM)
	}
	if got := strings.Count(first.ForLLM, "\n- "); got != 20 {
		t.Fatalf("page 1 returned %d entries, want the default page size of 20", got)
	}
	if !strings.Contains(first.ForLLM, "page 1/2") || !strings.Contains(first.ForLLM, "25 skill(s)") {
		t.Fatalf("header must report the full catalog and page count: %s", first.ForLLM)
	}

	second := tool.Execute(context.Background(), map[string]interface{}{"page": 2})
	if got := strings.Count(second.ForLLM, "\n- "); got != 5 {
		t.Fatalf("page 2 returned %d entries, want the remaining 5", got)
	}
	if strings.Contains(second.ForLLM, "- skill-01 ") {
		t.Fatalf("page 2 repeats page 1 content: %s", second.ForLLM)
	}
}

func TestListSkillsTool_OrdersByUsageMostUsedFirst(t *testing.T) {
	loader := newSkillToolLoader(t, "alpha", "beta", "gamma")
	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	usage.Inc("coder", "gamma")
	usage.Inc("coder", "gamma")
	usage.Inc("coder", "beta")
	tool := NewListSkillsTool(loader, usage, "coder", nil, nil)

	res := tool.Execute(context.Background(), map[string]interface{}{})
	gamma := strings.Index(res.ForLLM, "- gamma")
	beta := strings.Index(res.ForLLM, "- beta")
	alpha := strings.Index(res.ForLLM, "- alpha")
	if gamma < 0 || beta < 0 || alpha < 0 || !(gamma < beta && beta < alpha) {
		t.Fatalf("entries not ordered by usage frequency: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "(used 2 times)") {
		t.Fatalf("result should report usage counts: %s", res.ForLLM)
	}
}

func TestListSkillsTool_ExcludesSkillsAlreadyInContext(t *testing.T) {
	loader := newSkillToolLoader(t, "alpha", "beta", "gamma")
	inContext := func(string) []string { return []string{"alpha", "gamma"} }
	tool := NewListSkillsTool(loader, nil, "coder", nil, inContext)

	res := tool.Execute(context.Background(), map[string]interface{}{})
	if strings.Contains(res.ForLLM, "- alpha") || strings.Contains(res.ForLLM, "- gamma") {
		t.Fatalf("in-context skills leaked into the listing: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- beta") || !strings.Contains(res.ForLLM, "1 skill(s)") {
		t.Fatalf("listing must contain only the remaining skill: %s", res.ForLLM)
	}
}

func TestListSkillsTool_AllInContextReportsNothingToList(t *testing.T) {
	loader := newSkillToolLoader(t, "alpha")
	tool := NewListSkillsTool(loader, nil, "coder", nil, func(string) []string { return []string{"alpha"} })

	res := tool.Execute(context.Background(), map[string]interface{}{})
	if res.IsError || !strings.Contains(res.ForLLM, "already in the context") {
		t.Fatalf("expected a friendly empty-catalog message, got: %s", res.ForLLM)
	}
}

func TestListSkillsTool_PageBeyondLastClampsToLast(t *testing.T) {
	loader := newSkillToolLoader(t, listSkillNames(25)...)
	tool := NewListSkillsTool(loader, nil, "coder", nil, nil)

	res := tool.Execute(context.Background(), map[string]interface{}{"page": 99})
	if !strings.Contains(res.ForLLM, "page 2/2") {
		t.Fatalf("out-of-range page must clamp to the last page: %s", res.ForLLM)
	}
	if got := strings.Count(res.ForLLM, "\n- "); got != 5 {
		t.Fatalf("clamped page returned %d entries, want the last 5", got)
	}
}

func TestListSkillsTool_RespectsAgentFilter(t *testing.T) {
	loader := newSkillToolLoader(t, "alpha", "beta")
	tool := NewListSkillsTool(loader, nil, "coder", []string{"beta"}, nil)

	res := tool.Execute(context.Background(), map[string]interface{}{})
	if strings.Contains(res.ForLLM, "- alpha") || !strings.Contains(res.ForLLM, "- beta") {
		t.Fatalf("filtered catalog must only offer beta: %s", res.ForLLM)
	}
}
func TestListSkillsTool_ExclusionUsesContextSessionKey(t *testing.T) {
	loader := newSkillToolLoader(t, "alpha", "beta")
	var keys []string
	inContext := func(sessionKey string) []string {
		keys = append(keys, sessionKey)
		if sessionKey == "chat-b" {
			return []string{"alpha"}
		}
		return nil
	}
	tool := NewListSkillsTool(loader, nil, "coder", nil, inContext)

	res := tool.Execute(WithAgentToolContext(context.Background(), "coder", "chat-b"), map[string]interface{}{})
	if res.IsError {
		t.Fatalf("list failed: %s", res.ForLLM)
	}
	if len(keys) != 1 || keys[0] != "chat-b" {
		t.Fatalf("inContext saw session keys %v, want [chat-b]", keys)
	}
	if strings.Contains(res.ForLLM, "- alpha") || !strings.Contains(res.ForLLM, "- beta") {
		t.Fatalf("exclusion must follow the calling session: %s", res.ForLLM)
	}

	bare := tool.Execute(context.Background(), map[string]interface{}{})
	if !strings.Contains(bare.ForLLM, "- alpha") {
		t.Fatalf("a bare context has no pins, alpha must be listed: %s", bare.ForLLM)
	}
}
