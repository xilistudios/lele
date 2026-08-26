package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tui.json")

	custom := map[string]Theme{
		"ocean": {
			Background: "#000040",
			Primary:    "#00FFFF",
			Yellow:     "#FFFF00",
		},
	}

	if err := Save(path, "ocean", custom, nil); err != nil {
		t.Fatalf("Save: %v", err)
	}

	name, gotCustom, installed, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if name != "ocean" {
		t.Errorf("name = %q, want %q", name, "ocean")
	}
	if !reflect.DeepEqual(gotCustom, custom) {
		t.Errorf("custom = %+v, want %+v", gotCustom, custom)
	}
	if installed != nil {
		t.Errorf("installed = %+v, want nil", installed)
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	name, custom, installed, err := Load(path)
	if err != nil {
		t.Fatalf("Load(missing) error = %v, want nil", err)
	}
	if name != "dracula" {
		t.Errorf("name = %q, want %q", name, "dracula")
	}
	if custom != nil {
		t.Errorf("custom = %+v, want nil", custom)
	}
	if installed != nil {
		t.Errorf("installed = %+v, want nil", installed)
	}
}

func TestLoadMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tui.json")
	if err := os.WriteFile(path, []byte("{ not valid json !!"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	name, _, _, err := Load(path)
	if err == nil {
		t.Error("Load(malformed) error = nil, want non-nil")
	}
	if name != "dracula" {
		t.Errorf("name = %q, want %q", name, "dracula")
	}
}

func TestSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "tui.json") // parent dir must be created

	if err := Save(path, "nord", nil, nil); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Must be valid JSON...
	var cfg tuiConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
	if cfg.Theme != "nord" {
		t.Errorf("theme = %q, want %q", cfg.Theme, "nord")
	}

	// ...and 2-space indented.
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 1 {
		indent := ""
		for _, line := range lines[1:] {
			for _, r := range line {
				if r == ' ' {
					indent += " "
				} else {
					break
				}
			}
			break
		}
		if indent != "  " {
			t.Errorf("indent = %q, want exactly two spaces", indent)
		}
	}

	// No temp file should be left behind.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file %q still exists", path+".tmp")
	}
}

func TestSaveCustomThemes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tui.json")

	custom := map[string]Theme{
		"beach": {
			Background: "#A0C4FF",
			Primary:    "#FDCA40",
		},
	}

	if err := Save(path, "beach", custom, nil); err != nil {
		t.Fatalf("Save: %v", err)
	}

	name, gotCustom, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if name != "beach" {
		t.Errorf("name = %q, want %q", name, "beach")
	}
	if !reflect.DeepEqual(gotCustom, custom) {
		t.Errorf("custom = %+v, want %+v", gotCustom, custom)
	}
}
func TestSaveLoadWithInstalled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tui.json")

	custom := map[string]Theme{
		"cyberpunk": {Background: "#000000", Primary: "#00FF00"},
	}
	installed := []string{"cyberpunk", "forest"}

	if err := Save(path, "cyberpunk", custom, installed); err != nil {
		t.Fatalf("Save: %v", err)
	}

	name, gotCustom, gotInstalled, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if name != "cyberpunk" {
		t.Errorf("name = %q, want %q", name, "cyberpunk")
	}
	if !reflect.DeepEqual(gotCustom, custom) {
		t.Errorf("custom = %+v, want %+v", gotCustom, custom)
	}
	if !reflect.DeepEqual(gotInstalled, installed) {
		t.Errorf("installed = %+v, want %+v", gotInstalled, installed)
	}
}

func TestIsInstalledCommunity(t *testing.T) {
	installed := []string{"cyberpunk", "forest"}
	cases := []struct {
		name string
		want bool
	}{
		{"cyberpunk", true},
		{"forest", true},
		{"ocean", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsInstalledCommunity(tc.name, installed); got != tc.want {
			t.Errorf("IsInstalledCommunity(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
	if IsInstalledCommunity("cyberpunk", nil) {
		t.Error("IsInstalledCommunity on nil list = true, want false")
	}
}

func TestAddInstalledCommunity(t *testing.T) {
	got := AddInstalledCommunity("cyberpunk", []string{"forest"})
	want := []string{"forest", "cyberpunk"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AddInstalledCommunity = %+v, want %+v", got, want)
	}

	// No duplicates — adding an existing name is a no-op.
	got = AddInstalledCommunity("forest", []string{"forest", "cyberpunk"})
	want = []string{"forest", "cyberpunk"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AddInstalledCommunity duplicate = %+v, want %+v", got, want)
	}

	// Adding to nil input.
	got = AddInstalledCommunity("ocean", nil)
	want = []string{"ocean"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AddInstalledCommunity nil = %+v, want %+v", got, want)
	}

	// Input must not be mutated.
	src := []string{"forest"}
	got = AddInstalledCommunity("ocean", src)
	if !reflect.DeepEqual(src, []string{"forest"}) {
		t.Errorf("input mutated: %+v", src)
	}
	_ = got
}

func TestRemoveInstalledCommunity(t *testing.T) {
	got := RemoveInstalledCommunity("forest", []string{"cyberpunk", "forest"})
	want := []string{"cyberpunk"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RemoveInstalledCommunity = %+v, want %+v", got, want)
	}

	// Non-existent name is a no-op.
	got = RemoveInstalledCommunity("ocean", []string{"cyberpunk", "forest"})
	want = []string{"cyberpunk", "forest"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RemoveInstalledCommunity no-op = %+v, want %+v", got, want)
	}

	// Input must not be mutated.
	src := []string{"cyberpunk", "forest"}
	RemoveInstalledCommunity("cyberpunk", src)
	if !reflect.DeepEqual(src, []string{"cyberpunk", "forest"}) {
		t.Errorf("input mutated: %+v", src)
	}

	// Removing the last element yields an empty, non-nil slice.
	got = RemoveInstalledCommunity("cyberpunk", []string{"cyberpunk"})
	if got == nil || len(got) != 0 {
		t.Errorf("RemoveInstalledCommunity last = %+v, want empty non-nil", got)
	}
}
