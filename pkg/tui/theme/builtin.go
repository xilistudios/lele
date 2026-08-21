package theme

import "sort"

// builtins is the registry of built-in themes keyed by name.
var builtins = map[string]Theme{
	"dracula":         DraculaDefault,
	"nord":            nordTheme,
	"catppuccin":      catppuccinTheme,
	"gruvbox":         gruvboxTheme,
	"tokyo-night":     tokyoNightTheme,
	"solarized-light": solarizedLightTheme,
}

// Builtins returns the sorted list of built-in theme names.
func Builtins() []string {
	names := make([]string, 0, len(builtins))
	for name := range builtins {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var nordTheme = Theme{
	Background:          "#2E3440",
	InputBackground:     "#3B4252",
	Primary:             "#BF616A",
	Secondary:           "#A3BE8C",
	Accent:              "#88C0D0",
	Purple:              "#B48EAD",
	Orange:              "#D08770",
	Comment:             "#4C566A",
	Foreground:          "#ECEFF4",
	SelectionBackground: "#434C5E",
	Yellow:              "#EBCB8B",
}

var catppuccinTheme = Theme{
	Background:          "#1E1E2E",
	InputBackground:     "#313244",
	Primary:             "#F38BA8",
	Secondary:           "#A6E3A1",
	Accent:              "#89DCEB",
	Purple:              "#CBA6F7",
	Orange:              "#FAB387",
	Comment:             "#6C7086",
	Foreground:          "#CDD6F4",
	SelectionBackground: "#45475A",
	Yellow:              "#F9E2AF",
}

var gruvboxTheme = Theme{
	Background:          "#282828",
	InputBackground:     "#3C3836",
	Primary:             "#FB494B",
	Secondary:           "#B8BB26",
	Accent:              "#83A598",
	Purple:              "#D3869B",
	Orange:              "#FE8019",
	Comment:             "#928374",
	Foreground:          "#EBDBB2",
	SelectionBackground: "#504945",
	Yellow:              "#FABD2F",
}

var tokyoNightTheme = Theme{
	Background:          "#1A1B26",
	InputBackground:     "#24283B",
	Primary:             "#F7768E",
	Secondary:           "#9ECE6A",
	Accent:              "#7DCFFF",
	Purple:              "#BB9AF7",
	Orange:              "#FF9E64",
	Comment:             "#565F89",
	Foreground:          "#C0CAF5",
	SelectionBackground: "#33467C",
	Yellow:              "#E0AF68",
}

var solarizedLightTheme = Theme{
	Background:          "#FDF6E3",
	InputBackground:     "#EEE8D5",
	Primary:             "#DC322F",
	Secondary:           "#859900",
	Accent:              "#268BD2",
	Purple:              "#6C71C4",
	Orange:              "#CB4B16",
	Comment:             "#93A1A1",
	Foreground:          "#657B83",
	SelectionBackground: "#EEE8D5",
	Yellow:              "#B58900",
}
