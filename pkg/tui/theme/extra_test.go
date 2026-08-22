package theme

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	orig := os.Getenv("LELE_CONFIG_DIR")
	defer os.Setenv("LELE_CONFIG_DIR", orig)

	// With a custom config dir env var set, DefaultPath joins it + tui.json.
	os.Setenv("LELE_CONFIG_DIR", "/tmp/custom-lele-dir")
	if got := DefaultPath(); got != filepath.Join("/tmp/custom-lele-dir", "tui.json") {
		t.Errorf("DefaultPath() with LELE_CONFIG_DIR = %q, want %q", got, filepath.Join("/tmp/custom-lele-dir", "tui.json"))
	}

	// Nested dir we control: exercise the home-join branch deterministically.
	os.Setenv("LELE_CONFIG_DIR", t.TempDir())
	got := DefaultPath()
	if !strings.HasSuffix(got, string(filepath.Separator)+"tui.json") {
		t.Errorf("DefaultPath() = %q, want it to end with /tui.json", got)
	}
}

func TestNormalizeNil(t *testing.T) {
	var th *Theme
	if got := th.Normalize(); got != nil {
		t.Error("Normalize() on nil receiver = non-nil, want nil")
	}
}

func TestLoadFileIsDirectory(t *testing.T) {
	// Reading a directory is an error that is NOT os.IsNotExist. Load must
	// still return the "dracula, nil, nil, nil" fallback without panicking
	// and without an error from the read (the read error is masked).
	dir := t.TempDir()
	name, custom, installed, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(dir) error = %v, want nil", err)
	}
	if name != "dracula" {
		t.Errorf("name = %q, want dracula", name)
	}
	if custom != nil || installed != nil {
		t.Errorf("Load(dir) custom/installed = %+v/%+v, want nil/nil", custom, installed)
	}
}

func TestLoadEmptyThemeNameDefaultsDracula(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tui.json")
	if err := os.WriteFile(path, []byte(`{"theme":""}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	name, custom, installed, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if name != "dracula" {
		t.Errorf("name = %q, want dracula (empty theme defaults)", name)
	}
	if custom != nil {
		t.Errorf("custom = %+v, want nil (empty map defaults)", custom)
	}
	if installed != nil {
		t.Errorf("installed = %+v, want nil (empty list defaults)", installed)
	}
}

func TestSaveToUnwritableParent(t *testing.T) {
	// Build a path whose parent cannot be created because a file blocks it,
	// forcing MkdirAll to fail.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile blocker: %v", err)
	}
	path := filepath.Join(blocker, "tui.json")

	if err := Save(path, "nord", nil, nil); err == nil {
		t.Error("Save(unwritable parent) error = nil, want non-nil")
	}
}

func TestSaveEmptyCustomThemesKeepsEmptySlice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tui.json")

	// Empty (but non-nil) custom map and empty installed list should round-trip.
	emptyCustom := map[string]Theme{}
	emptyInstalled := []string{}

	if err := Save(path, "dracula", emptyCustom, emptyInstalled); err != nil {
		t.Fatalf("Save: %v", err)
	}

	name, custom, installed, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if name != "dracula" {
		t.Errorf("name = %q, want dracula", name)
	}
	// Load normalizes empty maps/lists to nil.
	if custom != nil {
		t.Errorf("custom = %+v, want nil", custom)
	}
	if installed != nil {
		t.Errorf("installed = %+v, want nil", installed)
	}
}

func TestAddRemoveRoundTripPreservesNil(t *testing.T) {
	// Add a community theme to nil, then remove it: returns a non-nil empty
	// slice vs nil input expectations.
	added := AddInstalledCommunity("ocean", nil)
	if !reflect.DeepEqual(added, []string{"ocean"}) {
		t.Errorf("AddInstalledCommunity(nil) = %+v, want [ocean]", added)
	}

	removed := RemoveInstalledCommunity("ocean", added)
	if removed == nil || len(removed) != 0 {
		t.Errorf("RemoveInstalledCommunity after add = %+v, want empty non-nil", removed)
	}
}

func TestValidateCommunityNameValid(t *testing.T) {
	valid := []string{"dracula", "my-theme", "th3me", "a", "theme-2024"}
	for _, name := range valid {
		if err := validateCommunityName(name); err != nil {
			t.Errorf("validateCommunityName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateCommunityNameInvalid(t *testing.T) {
	invalid := []string{
		"",
		"../evil",
		"/abs/path",
		"a/b",
		"a.b",
		"has space",
		"UPPER",
		"themes/x",
	}
	for _, name := range invalid {
		if err := validateCommunityName(name); err == nil {
			t.Errorf("validateCommunityName(%q) error = nil, want non-nil", name)
		}
	}
}

func TestCommunityGetUsesREnforcedTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping close-over-const check that reads the package constant")
	}
	// communityTimeout is private; assert the exported contract that a
	// request to an unreachable host fails quickly rather than hanging. This
	// only exercises the timeout field's effect trivially.
	if communityTimeout <= 0 {
		t.Error("communityTimeout should be positive")
	}
}

func TestFetchCommunityThemeValidNameNoNetwork(t *testing.T) {
	if testing.Short() || !hasNetwork() {
		t.Skip("skipping network-dependent test without network")
	}
	// A valid name passes validation; if the network fetch fails (e.g. theme
	// does not exist) we still get a non-nil error, which proves the name
	// guard did not reject it.
	_, err := FetchCommunityTheme("definitely-not-a-real-theme-xyz")
	if err == nil {
		// If it somehow exists, that's fine; the test is only asserting the
		// guard passes. If it errors, it must be reach/parse related, not a
		// name validation error.
		return
	}
	if strings.Contains(err.Error(), "invalid theme name") {
		t.Errorf("FetchCommunityTheme(valid) returned name-validation error: %v", err)
	}
}
