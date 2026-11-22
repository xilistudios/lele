package tools

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func DetectEncoding(buffer []byte) (string, []byte) {
	if len(buffer) >= 4 {
		if buffer[0] == 0x00 && buffer[1] == 0x00 && buffer[2] == 0xFE && buffer[3] == 0xFF {
			return "UTF-32BE", buffer[4:]
		}
		if buffer[0] == 0xFF && buffer[1] == 0xFE && buffer[2] == 0x00 && buffer[3] == 0x00 {
			return "UTF-32LE", buffer[4:]
		}
	}

	if len(buffer) >= 2 {
		if buffer[0] == 0xFE && buffer[1] == 0xFF {
			return "UTF-16BE", buffer[2:]
		}
		if buffer[0] == 0xFF && buffer[1] == 0xFE {
			return "UTF-16LE", buffer[2:]
		}
	}

	if len(buffer) >= 3 {
		if buffer[0] == 0xEF && buffer[1] == 0xBB && buffer[2] == 0xBF {
			return "UTF-8-BOM", buffer[3:]
		}
	}

	return "UTF-8", buffer
}

func GetBOM(encoding string) []byte {
	switch strings.ToUpper(encoding) {
	case "UTF-8-BOM":
		return []byte{0xEF, 0xBB, 0xBF}
	case "UTF-16BE":
		return []byte{0xFE, 0xFF}
	case "UTF-16LE":
		return []byte{0xFF, 0xFE}
	case "UTF-32BE":
		return []byte{0x00, 0x00, 0xFE, 0xFF}
	case "UTF-32LE":
		return []byte{0xFF, 0xFE, 0x00, 0x00}
	}
	return nil
}

func ReadFileWithEncoding(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}

	encoding, content := DetectEncoding(data)
	return string(content), encoding, nil
}

func WriteFileWithEncoding(path, content, encoding string) error {
	bom := GetBOM(encoding)
	data := append(bom, []byte(content)...)
	return os.WriteFile(path, data, 0644)
}

type Match struct {
	Start int
	End   int
	Line  int
}

type MatchStrategy interface {
	FindMatches(content, pattern string) []Match
}

type ExactMatchStrategy struct{}

func (s *ExactMatchStrategy) FindMatches(content, oldStr string) []Match {
	var matches []Match

	if len(oldStr) == 0 {
		return matches
	}

	start := 0
	for {
		if start >= len(content) {
			break
		}
		idx := strings.Index(content[start:], oldStr)
		if idx == -1 {
			break
		}
		matchStart := start + idx
		matches = append(matches, Match{
			Start: matchStart,
			End:   matchStart + len(oldStr),
		})
		start = matchStart + len(oldStr)
	}
	return matches
}

type RegexMatchStrategy struct {
	Flags string
}

func (s *RegexMatchStrategy) FindMatches(content, pattern string) []Match {
	flags := s.Flags
	caseInsensitive := strings.Contains(flags, "i")
	global := strings.Contains(flags, "g")

	var re *regexp.Regexp
	var err error

	if caseInsensitive {
		re, err = regexp.Compile("(?i:" + pattern + ")")
	} else {
		re, err = regexp.Compile(pattern)
	}

	if err != nil {
		return nil
	}

	var matches []Match
	if global {
		allMatches := re.FindAllStringIndex(content, -1)
		for _, m := range allMatches {
			matches = append(matches, Match{
				Start: m[0],
				End:   m[1],
			})
		}
	} else {
		loc := re.FindStringIndex(content)
		if loc != nil {
			matches = append(matches, Match{
				Start: loc[0],
				End:   loc[1],
			})
		}
	}

	return matches
}

type ReplacementPair struct {
	Old string `json:"old"`
	New string `json:"new"`
}

func ApplyReplacements(content string, pairs []ReplacementPair, strategy MatchStrategy) (string, error) {
	type replacementMatch struct {
		Pair  ReplacementPair
		Match Match
	}

	var allMatches []replacementMatch

	for _, pair := range pairs {
		matches := strategy.FindMatches(content, pair.Old)
		if len(matches) == 0 {
			return "", fmt.Errorf("old_text not found: %s", pair.Old)
		}
		if len(matches) > 1 {
			return "", fmt.Errorf("old_text appears %d times: %s", len(matches), pair.Old)
		}
		allMatches = append(allMatches, replacementMatch{
			Pair:  pair,
			Match: matches[0],
		})
	}

	for i := 0; i < len(allMatches); i++ {
		for j := i + 1; j < len(allMatches); j++ {
			if matchesOverlap(allMatches[i].Match, allMatches[j].Match) {
				return "", fmt.Errorf("replacements overlap: '%s' and '%s'",
					allMatches[i].Pair.Old, allMatches[j].Pair.Old)
			}
		}
	}

	for i := 0; i < len(allMatches); i++ {
		for j := i + 1; j < len(allMatches); j++ {
			if allMatches[i].Match.Start < allMatches[j].Match.Start {
				allMatches[i], allMatches[j] = allMatches[j], allMatches[i]
			}
		}
	}

	result := content
	for _, rm := range allMatches {
		result = ReplaceRange(result, rm.Match, rm.Pair.New)
	}

	return result, nil
}

func ReplaceRange(content string, match Match, newText string) string {
	var buf bytes.Buffer
	buf.WriteString(content[:match.Start])
	buf.WriteString(newText)
	buf.WriteString(content[match.End:])
	return buf.String()
}

func DetectOverlaps(matches []Match) bool {
	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matchesOverlap(matches[i], matches[j]) {
				return true
			}
		}
	}
	return false
}

func matchesOverlap(a, b Match) bool {
	return (a.Start < b.End && a.End > b.Start)
}

type WhitespaceTolerantStrategy struct{}

func (s *WhitespaceTolerantStrategy) FindMatches(content, oldStr string) []Match {
	normalizedContent := normalizeWhitespace(content)
	normalizedOld := normalizeWhitespace(oldStr)

	var matches []Match
	start := 0
	for {
		idx := strings.Index(normalizedContent[start:], normalizedOld)
		if idx == -1 {
			break
		}
		matchStart := start + idx

		origStart := mapNormalizedToOriginal(content, matchStart)
		origEnd := mapNormalizedToOriginal(content, matchStart+len(normalizedOld))

		matches = append(matches, Match{
			Start: origStart,
			End:   origEnd,
		})
		start = matchStart + 1
	}
	return matches
}

func normalizeWhitespace(s string) string {
	re := regexp.MustCompile(`\s+`)
	return re.ReplaceAllString(s, " ")
}

func mapNormalizedToOriginal(original string, normalizedPos int) int {
	if normalizedPos <= 0 {
		return 0
	}

	whitespacePattern := regexp.MustCompile(`\s+`)
	normalizedIndex := 0
	originalIndex := 0

	for originalIndex < len(original) && normalizedIndex < normalizedPos {
		loc := whitespacePattern.FindStringIndex(original[originalIndex:])
		if loc != nil && loc[0] == 0 {
			normalizedIndex++
			originalIndex += loc[1]
		} else {
			normalizedIndex++
			originalIndex++
		}

		if normalizedIndex >= normalizedPos {
			return originalIndex
		}
	}

	return originalIndex
}

func ReadLines(content string, from, to int) ([]struct {
	Number int
	Text   string
}, error) {
	lines := strings.Split(content, "\n")

	if from < 1 {
		from = 1
	}
	if to > len(lines) {
		to = len(lines)
	}
	if from > to {
		return nil, fmt.Errorf("from (%d) must be <= to (%d)", from, to)
	}

	var result []struct {
		Number int
		Text   string
	}
	for i := from - 1; i < to && i < len(lines); i++ {
		result = append(result, struct {
			Number int
			Text   string
		}{
			Number: i + 1,
			Text:   lines[i],
		})
	}
	return result, nil
}
