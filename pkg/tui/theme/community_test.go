package theme

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRawURL(t *testing.T) {
	got := rawURL("themes/index.json")
	want := "https://raw.githubusercontent.com/" + CommunityRepo + "/" +
		CommunityBranch + "/themes/index.json"
	if got != want {
		t.Errorf("rawURL(\"themes/index.json\") = %q, want %q", got, want)
	}
}

func TestFetchCommunityThemeInvalidName(t *testing.T) {
	badNames := []string{
		"",
		"../evil",
		"a/b",
		"a.b",
		"has space",
		"Ümlaut",
		"UPPER",
		"a\\b",
	}

	for _, name := range badNames {
		_, err := FetchCommunityTheme(name)
		if err == nil {
			t.Errorf("FetchCommunityTheme(%q) error = nil, want non-nil", name)
			continue
		}
		if !strings.Contains(err.Error(), "invalid theme name") {
			t.Errorf("FetchCommunityTheme(%q) error = %q, want \"invalid theme name\"", name, err)
		}
	}
}

// TestFetchCommunityIndex fetches the community themes index from GitHub.
// It is skipped when there is no network access.
//
//nolint:errcheck
func TestFetchCommunityIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network integration test in short mode")
	}

	if !hasNetwork() {
		t.Skip("no network; skipping network integration test")
	}

	idx, err := FetchCommunityIndex()
	if err != nil {
		t.Fatalf("FetchCommunityIndex: %v", err)
	}

	if len(idx) < 1 {
		t.Errorf("community index has %d entries, want at least 1", len(idx))
	}
	if len(idx) > 0 && idx[0].Name == "" {
		t.Error("first community index entry has an empty Name")
	}
}

// hasNetwork does a quick best-effort probe for internet connectivity with
// a short timeout so the integration test fails fast (and skips) rather
// than hanging for the full 15-second client timeout.
func hasNetwork() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("https://github.com")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}
