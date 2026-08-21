package theme

import (
	"reflect"
	"testing"
)

func TestBuiltinsSorted(t *testing.T) {
	names := Builtins()

	want := []string{"blood-moon", "catppuccin", "dracula", "dracula-pro", "github-dark", "gruvbox", "monokai", "nord", "one-dark", "rose-pine", "solarized-light", "tokyo-night"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Builtins() = %v, want %v", names, want)
	}
}

func TestBuiltinThemesComplete(t *testing.T) {
	for name, th := range builtins {
		for field, v := range map[string]string{
			"Background": th.Background, "InputBackground": th.InputBackground,
			"Primary": th.Primary, "Secondary": th.Secondary, "Accent": th.Accent,
			"Purple": th.Purple, "Orange": th.Orange, "Comment": th.Comment,
			"Foreground": th.Foreground, "SelectionBackground": th.SelectionBackground,
			"Yellow": th.Yellow,
		} {
			if v == "" {
				t.Errorf("builtin theme %q field %s is empty", name, field)
			}
		}
	}
}
