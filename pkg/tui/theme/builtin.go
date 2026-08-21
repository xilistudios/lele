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
	"one-dark":        oneDarkTheme,
	"monokai":         monokaiTheme,
	"github-dark":     githubDarkTheme,
	"rose-pine":       rosePineTheme,
	"dracula-pro":     draculaProTheme,
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
var oneDarkTheme = Theme{
	Background:          "#282C34",
	InputBackground:     "#21252B",
	Primary:             "#E06C75",
	Secondary:           "#98C379",
	Accent:              "#61AFEF",
	Purple:              "#C678DD",
	Orange:              "#D19A66",
	Comment:             "#5C6370",
	Foreground:          "#ABB2BF",
	SelectionBackground: "#3E4451",
	Yellow:              "#E5C07B",
}

var monokaiTheme = Theme{
	Background:          "#2D2A2E",
	InputBackground:     "#221F22",
	Primary:             "#FF6188",
	Secondary:           "#A9DC76",
	Accent:              "#78DCE8",
	Purple:              "#AB9DF2",
	Orange:              "#FC9867",
	Comment:             "#727072",
	Foreground:          "#FCFCFA",
	SelectionBackground: "#403E41",
	Yellow:              "#FFD866",
}

var githubDarkTheme = Theme{
	Background:          "#0D1117",
	InputBackground:     "#161B22",
	Primary:             "#FF7B72",
	Secondary:           "#3FB950",
	Accent:              "#58A6FF",
	Purple:              "#BC8CFF",
	Orange:              "#F0883E",
	Comment:             "#8B949E",
	Foreground:          "#C9D1D9",
	SelectionBackground: "#1F6FEB",
	Yellow:              "#E3B341",
}

var rosePineTheme = Theme{
	Background:          "#191724",
	InputBackground:     "#1F1D2E",
	Primary:             "#EBBCBA",
	Secondary:           "#31748F",
	Accent:              "#9CCFD8",
	Purple:              "#C4A7E7",
	Orange:              "#EB6F92",
	Comment:             "#6E6A86",
	Foreground:          "#E0DEF4",
	SelectionBackground: "#26233A",
	Yellow:              "#F6C177",
}

var draculaProTheme = Theme{
	Background:          "#21222C",
	InputBackground:     "#282A36",
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
