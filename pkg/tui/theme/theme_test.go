package theme

import "testing"

func TestNormalizeFillsEmpty(t *testing.T) {
	got := Theme{
		Background:      "#FF0000",
		Primary:         "#00FF00",
		Yellow:          "#0000FF",
		InputBackground: "some-accidental-value",
	}
	got.Normalize()

	want := DraculaDefault
	// Explicitly set fields survive (they are valid colors).
	want.Background = "#FF0000"
	want.Primary = "#00FF00"
	want.Yellow = "#0000FF"

	if got.Background != want.Background {
		t.Errorf("Background = %q, want %q", got.Background, want.Background)
	}
	if got.InputBackground != want.InputBackground {
		t.Errorf("InputBackground = %q, want %q", got.InputBackground, want.InputBackground)
	}
	if got.Primary != want.Primary {
		t.Errorf("Primary = %q, want %q", got.Primary, want.Primary)
	}
	if got.Secondary != want.Secondary {
		t.Errorf("Secondary = %q, want %q", got.Secondary, want.Secondary)
	}
	if got.Accent != want.Accent {
		t.Errorf("Accent = %q, want %q", got.Accent, want.Accent)
	}
	if got.Purple != want.Purple {
		t.Errorf("Purple = %q, want %q", got.Purple, want.Purple)
	}
	if got.Orange != want.Orange {
		t.Errorf("Orange = %q, want %q", got.Orange, want.Orange)
	}
	if got.Comment != want.Comment {
		t.Errorf("Comment = %q, want %q", got.Comment, want.Comment)
	}
	if got.Foreground != want.Foreground {
		t.Errorf("Foreground = %q, want %q", got.Foreground, want.Foreground)
	}
	if got.SelectionBackground != want.SelectionBackground {
		t.Errorf("SelectionBackground = %q, want %q", got.SelectionBackground, want.SelectionBackground)
	}
	if got.Yellow != want.Yellow {
		t.Errorf("Yellow = %q, want %q", got.Yellow, want.Yellow)
	}
}

func TestNormalizeInvalidColor(t *testing.T) {
	got := Theme{
		Background:          "not-a-color",
		Primary:             "#GGGGGG", // invalid hex
		Secondary:           "39",      // valid ANSI 256
		InputBackground:     "x",       // invalid numeric
		SelectionBackground: "#fff",    // valid short hex
	}
	got.Normalize()

	want := DraculaDefault
	want.Secondary = "39"
	want.SelectionBackground = "#fff"

	if got.Background != want.Background {
		t.Errorf("Background = %q, want %q", got.Background, want.Background)
	}
	if got.Primary != want.Primary {
		t.Errorf("Primary = %q, want %q", got.Primary, want.Primary)
	}
	if got.Secondary != want.Secondary {
		t.Errorf("Secondary = %q, want %q", got.Secondary, want.Secondary)
	}
	if got.InputBackground != want.InputBackground {
		t.Errorf("InputBackground = %q, want %q", got.InputBackground, want.InputBackground)
	}
	if got.SelectionBackground != want.SelectionBackground {
		t.Errorf("SelectionBackground = %q, want %q", got.SelectionBackground, want.SelectionBackground)
	}
}

func TestGetBuiltin(t *testing.T) {
	got := Get("nord", nil)

	want := nordTheme
	want.Normalize()
	if got != want {
		t.Errorf("Get(\"nord\", nil) = %+v, want %+v", got, want)
	}
	// Ensure no empty fields survive.
	for name, v := range map[string]string{
		"Background": got.Background, "InputBackground": got.InputBackground,
		"Primary": got.Primary, "Secondary": got.Secondary, "Accent": got.Accent,
		"Purple": got.Purple, "Orange": got.Orange, "Comment": got.Comment,
		"Foreground": got.Foreground, "SelectionBackground": got.SelectionBackground,
		"Yellow": got.Yellow,
	} {
		if v == "" {
			t.Errorf("nord field %s is empty", name)
		}
	}
}

func TestGetCustom(t *testing.T) {
	custom := map[string]Theme{
		"ocean": {
			Background: "#000040",
			Primary:    "#00FFFF",
		},
	}

	got := Get("ocean", custom)

	if got.Background != "#000040" {
		t.Errorf("Background = %q, want #000040", got.Background)
	}
	if got.Primary != "#00FFFF" {
		t.Errorf("Primary = %q, want #00FFFF", got.Primary)
	}
	// Unset custom fields fall back to Dracula.
	if got.Secondary != DraculaDefault.Secondary {
		t.Errorf("Secondary = %q, want %q", got.Secondary, DraculaDefault.Secondary)
	}
	if got.Accent != DraculaDefault.Accent {
		t.Errorf("Accent = %q, want %q", got.Accent, DraculaDefault.Accent)
	}
	if got.Yellow != DraculaDefault.Yellow {
		t.Errorf("Yellow = %q, want %q", got.Yellow, DraculaDefault.Yellow)
	}
}

func TestGetBadName(t *testing.T) {
	got := Get("nonexistent", nil)

	want := DraculaDefault
	if got != want {
		t.Errorf("Get(\"nonexistent\", nil) = %+v, want %+v", got, want)
	}
}
