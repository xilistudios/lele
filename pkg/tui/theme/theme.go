// Package theme defines TUI color themes as plain color strings.
//
// It is a pure package with no TUI dependencies (no lipgloss, no
// bubbletea). It only deals with color strings and files.
package theme

import (
	"regexp"
	"strings"
)

// Theme holds the color palette of a TUI theme as raw color strings.
// Colors may be hex like "#181824", short hex like "#ff0", or numeric
// ANSI-256 values like "39" or "240".
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

// DraculaDefault is the default Dracula palette used as a fallback for
// every missing or invalid field.
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

// isValidColor reports whether s is a valid color value: full hex,
// short hex, or a numeric ANSI-256 value.
func isValidColor(s string) bool {
	return hexColorRe.MatchString(s) ||
		shortHexColorRe.MatchString(s) ||
		ansiColorRe.MatchString(s)
}

// Normalize fills any empty field of t with the corresponding Dracula
// default and replaces any invalid color value with the Dracula default
// for that field. It mutates and returns the receiver.
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

// normalizeField returns v trimmed of whitespace; if that yields an
// empty or invalid color it falls back to def.
func normalizeField(v, def string) string {
	v = strings.TrimSpace(v)
	if v == "" || !isValidColor(v) {
		return def
	}
	return v
}

// Get returns the theme named name. Resolution order:
//
//  1. if custom[name] exists, start from it;
//  2. else if name is a built-in theme, start from it;
//  3. else fall back to Dracula.
//
// The result is always Normalize()d before being returned.
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
