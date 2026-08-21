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

	if err := Save(path, "ocean", custom); err != nil {
		t.Fatalf("Save: %v", err)
	}

	name, gotCustom, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if name != "ocean" {
		t.Errorf("name = %q, want %q", name, "ocean")
	}
	if !reflect.DeepEqual(gotCustom, custom) {
		t.Errorf("custom = %+v, want %+v", gotCustom, custom)
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	name, custom, err := Load(path)
	if err != nil {
		t.Fatalf("Load(missing) error = %v, want nil", err)
	}
	if name != "dracula" {
		t.Errorf("name = %q, want %q", name, "dracula")
	}
	if custom != nil {
		t.Errorf("custom = %+v, want nil", custom)
	}
}

func TestLoadMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tui.json")
	if err := os.WriteFile(path, []byte("{ not valid json !!"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	name, _, err := Load(path)
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

	if err := Save(path, "nord", nil); err != nil {
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

	if err := Save(path, "beach", custom); err != nil {
		t.Fatalf("Save: %v", err)
	}

	name, gotCustom, err := Load(path)
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
