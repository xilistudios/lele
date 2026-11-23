package skills

import (
	"strings"
	"testing"
)

func searchSkillInfo(name, description string) SkillInfo {
	return SkillInfo{Name: name, Description: description, Enabled: true}
}

func matchNames(matches []SearchMatch) []string {
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.Skill.Name
	}
	return names
}

func TestSearch_NameOutranksDescriptionAndBody(t *testing.T) {
	all := []SkillInfo{
		searchSkillInfo("body-only", "unrelated words"),
		searchSkillInfo("desc-only", "mentions weather somewhere"),
		searchSkillInfo("weather", "unrelated words"),
	}
	bodies := map[string]string{"body-only": "secret weather notes"}
	matches := Search(all, "weather", func(name string) (string, bool) {
		b, ok := bodies[name]
		return b, ok
	})

	got := matchNames(matches)
	want := []string{"weather", "desc-only", "body-only"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ranking = %v, want name > description > body (%v)", got, want)
	}
}

func TestSearch_FullQueryCoverageOutranksPartial(t *testing.T) {
	all := []SkillInfo{
		searchSkillInfo("github-only", "issues and more"),
		searchSkillInfo("github-pr", "review pull requests on github"),
	}
	matches := Search(all, "github pull requests", nil)
	got := matchNames(matches)
	if len(got) != 2 || got[0] != "github-pr" {
		t.Fatalf("ranking = %v, want the skill covering every query term first (OR semantics keeps the partial match)", got)
	}
	if matches[0].Score <= matches[1].Score {
		t.Fatalf("scores = %+v, want full coverage strictly above partial", matches)
	}
}

func TestSearch_CaseInsensitiveAndNoMatches(t *testing.T) {
	all := []SkillInfo{searchSkillInfo("Weather", "Forecast data")}
	if got := matchNames(Search(all, "WEATHER", nil)); len(got) != 1 {
		t.Fatalf("case-insensitive search found %v, want the Weather skill", got)
	}
	if got := Search(all, "nonexistent", nil); len(got) != 0 {
		t.Fatalf("unrelated query matched %v, want none", matchNames(got))
	}
	if got := Search(all, "  ", nil); got != nil {
		t.Fatalf("blank query matched %v, want nil", matchNames(got))
	}
}

func TestSearch_SkillMatchingNoTermIsExcluded(t *testing.T) {
	all := []SkillInfo{
		searchSkillInfo("weather", "forecast"),
		searchSkillInfo("cooking", "recipes"),
	}
	got := matchNames(Search(all, "weather forecast", nil))
	if len(got) != 1 || got[0] != "weather" {
		t.Fatalf("results = %v, want only the skill matching at least one term", got)
	}
}

func TestSearchScores_DescendWithRank(t *testing.T) {
	all := []SkillInfo{
		searchSkillInfo("weather", "forecast"),
		searchSkillInfo("other", "weather forecast"),
	}
	matches := Search(all, "weather", nil)
	if len(matches) != 2 || matches[0].Score <= matches[1].Score {
		t.Fatalf("scores = %+v, want the name match strictly above the description match", matches)
	}
}
