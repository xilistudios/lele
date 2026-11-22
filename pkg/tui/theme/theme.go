package theme

import (
	"regexp"
	"strings"
)

type Theme struct {
	Background          string `json:"background"`
	InputBackground     string `json:"input_background"`
	Primary             string `json:"primary"`
	Secondary           string `json:"secondary"`
	Accent              string `json:"accent"`
	Purple              string `json:"purple"`
	Orange              string `json:"orange"`
	Comment             string `json:"comment"`
	Foreground          string `json:"foreground"`
	SelectionBackground string `json:"selection_background"`
	Yellow              string `json:"yellow"`
}

var DraculaDefault = Theme{
	Background:          "#181824",
	InputBackground:     "#212130",
	Primary:             "#FF5555",
	Secondary:           "#50FA7B",
	Accent:              "#8BE9FD",
	Purple:              "#BD93F9",
	Orange:              "#FFB86C",
	Comment:             "#6272A4",
	Foreground:          "#F8F8F2",
	SelectionBackground: "#44475A",
	Yellow:              "#F1FA8C",
}

var (
	hexColorRe      = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	shortHexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{3}$`)
	ansiColorRe     = regexp.MustCompile(`^[0-9]{1,3}$`)
)

func isValidColor(s string) bool {
	return hexColorRe.MatchString(s) ||
		shortHexColorRe.MatchString(s) ||
		ansiColorRe.MatchString(s)
}

func (t *Theme) Normalize() *Theme {
	if t == nil {
		return t
	}
	t.Background = normalizeField(t.Background, DraculaDefault.Background)
	t.InputBackground = normalizeField(t.InputBackground, DraculaDefault.InputBackground)
	t.Primary = normalizeField(t.Primary, DraculaDefault.Primary)
	t.Secondary = normalizeField(t.Secondary, DraculaDefault.Secondary)
	t.Accent = normalizeField(t.Accent, DraculaDefault.Accent)
	t.Purple = normalizeField(t.Purple, DraculaDefault.Purple)
	t.Orange = normalizeField(t.Orange, DraculaDefault.Orange)
	t.Comment = normalizeField(t.Comment, DraculaDefault.Comment)
	t.Foreground = normalizeField(t.Foreground, DraculaDefault.Foreground)
	t.SelectionBackground = normalizeField(t.SelectionBackground, DraculaDefault.SelectionBackground)
	t.Yellow = normalizeField(t.Yellow, DraculaDefault.Yellow)
	return t
}

func normalizeField(v, def string) string {
	v = strings.TrimSpace(v)
	if v == "" || !isValidColor(v) {
		return def
	}
	return v
}

func Get(name string, custom map[string]Theme) Theme {
	var t Theme

	if custom != nil {
		if ct, ok := custom[name]; ok {
			t = ct
			return *t.Normalize()
		}
	}

	if builtin, ok := builtins[name]; ok {
		t = builtin
		return *t.Normalize()
	}

	t = DraculaDefault
	return *t.Normalize()
}
