package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func topNames(list []SkillInfo) []string {
	names := make([]string, len(list))
	for i, s := range list {
		names[i] = s.Name
	}
	return names
}

func TestTopSkills_OrdersByUsageAndCapsAtLimit(t *testing.T) {
	sl := newSummaryLoader(t, "alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta")
	counts := map[string]uint64{"zeta": 9, "gamma": 4}

	top := sl.TopSkills(nil, counts, 5)
	got := topNames(top)
	if len(got) != 5 {
		t.Fatalf("top = %v, want exactly the limit of 5", got)
	}
	if got[0] != "zeta" || got[1] != "gamma" {
		t.Fatalf("top = %v, want the most-used skills first", got)
	}
	for _, name := range got[2:] {
		if name == "zeta" || name == "gamma" {
			t.Fatalf("top = %v, contains duplicates", got)
		}
	}
}

func TestTopSkills_NoUsageFallsBackToAlphabetical(t *testing.T) {
	sl := newSummaryLoader(t, "gamma", "alpha", "beta")
	got := topNames(sl.TopSkills(nil, nil, 2))
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("top = %v, want the first skills alphabetically when nothing was used", got)
	}
}

func TestTopSkills_RespectsFilterAndDisabled(t *testing.T) {
	sl := newSummaryLoader(t, "alpha", "beta", "gamma")
	if err := sl.GetConfigManager().SetDisabled("beta"); err != nil {
		t.Fatalf("disable beta: %v", err)
	}

	got := topNames(sl.TopSkills([]string{"beta", "gamma"}, map[string]uint64{"beta": 100}, 5))
	if len(got) != 1 || got[0] != "gamma" {
		t.Fatalf("top = %v, want only gamma (beta is disabled despite its usage and being filtered in)", got)
	}
}

func TestBuildTopSkillsSummary_RendersOnlyTheTopWindow(t *testing.T) {
	sl := newSummaryLoader(t, "alpha", "beta", "gamma")
	summary := sl.BuildTopSkillsSummary(nil, map[string]uint64{"gamma": 3}, 2)
	got := summaryNames(t, summary)
	if len(got) != 2 || got[0] != "gamma" {
		t.Fatalf("summary names = %v, want gamma first and only 2 entries", got)
	}
}

func TestSearchSkills_SearchesBodyAndHidesDisabled(t *testing.T) {
	sl := newSummaryLoader(t, "alpha", "beta")
	bodyPath := filepath.Join(sl.WorkspaceDir(), "skills", "beta", "SKILL.md")
	content, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bodyPath, append(content, []byte("\nsecret zebra knowledge")...), 0o644); err != nil {
		t.Fatal(err)
	}

	matches := sl.SearchSkills("zebra", nil)
	if len(matches) != 1 || matches[0].Skill.Name != "beta" {
		t.Fatalf("matches = %v, want beta found through its SKILL.md body", matchNames(matches))
	}

	if err := sl.GetConfigManager().SetDisabled("beta"); err != nil {
		t.Fatal(err)
	}
	if got := sl.SearchSkills("zebra", nil); len(got) != 0 {
		t.Fatalf("matches = %v, want disabled skills excluded from search", matchNames(got))
	}
}
