package skills

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
)

type SearchMatch struct {
	Skill SkillInfo
	Score float64
}

const (
	scoreNameExact       = 10.0
	scoreNameToken       = 6.0
	scoreNameSubstring   = 4.0
	scoreDescWord        = 2.0
	scoreDescSubstring   = 1.0
	scoreBodySubstring   = 0.5
	defaultSearchBodyCap = 64 * 1024
)

func Search(all []SkillInfo, query string, loadBody func(name string) (string, bool)) []SearchMatch {
	terms := tokenize(query)
	if len(terms) == 0 {
		return nil
	}

	matches := make([]SearchMatch, 0, len(all))
	for _, skill := range all {
		score := scoreSkill(skill, terms, loadBody)
		if score > 0 {
			matches = append(matches, SearchMatch{Skill: skill, Score: score})
		}
	}

	slices.SortStableFunc(matches, func(a, b SearchMatch) int {
		if diff := cmp.Compare(b.Score, a.Score); diff != 0 {
			return diff
		}
		return cmp.Compare(a.Skill.Name, b.Skill.Name)
	})
	return matches
}

func scoreSkill(skill SkillInfo, terms []string, loadBody func(name string) (string, bool)) float64 {
	name := strings.ToLower(skill.Name)
	nameTokens := tokenize(skill.Name)
	description := strings.ToLower(skill.Description)
	descWords := tokenize(skill.Description)

	var body string
	if loadBody != nil {
		if content, ok := loadBody(skill.Name); ok {
			if len(content) > defaultSearchBodyCap {
				content = content[:defaultSearchBodyCap]
			}
			body = strings.ToLower(content)
		}
	}

	var total float64
	matched := 0
	for _, term := range terms {
		best := scoreTerm(term, name, nameTokens, description, descWords, body)
		if best > 0 {
			matched++
			total += best
		}
	}
	if matched == 0 {
		return 0
	}
	return total * float64(matched) / float64(len(terms))
}

func scoreTerm(term, name string, nameTokens []string, description string, descWords []string, body string) float64 {
	switch {
	case name == term:
		return scoreNameExact
	case slices.Contains(nameTokens, term):
		return scoreNameToken
	case strings.Contains(name, term):
		return scoreNameSubstring
	case slices.Contains(descWords, term):
		return scoreDescWord
	case strings.Contains(description, term):
		return scoreDescSubstring
	case body != "" && strings.Contains(body, term):
		return scoreBodySubstring
	default:
		return 0
	}
}

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
