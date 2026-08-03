package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"0.8.2", "0.9.0", true},
		{"v0.8.2", "v0.9.0", true},
		{"0.9.0", "0.9.0", false},
		{"0.9.1", "0.9.0", false},
		{"0.9", "0.9.1", true},
		{"0.10.0", "0.9.9", false},
		{"1.0.0", "0.99.99", false},
		{"dev", "1.0.0", false}, // dev builds never auto-update
		{"", "1.0.0", false},
		{"1.0.0", "", false},
		{"1.0.0-rc1", "1.0.0", false}, // same base version
		{"1.0.0", "1.0.1-beta", true}, // base 1.0.1 > 1.0.0
		{"2.0", "10.0", true},
	}
	for _, tt := range tests {
		if got := NewerVersion(tt.current, tt.latest); got != tt.want {
			t.Errorf("NewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	if compareSemver("1.2.3", "1.2.3") != 0 {
		t.Error("equal versions should compare as 0")
	}
	if compareSemver("1.2.4", "1.2.3") <= 0 {
		t.Error("1.2.4 should be greater than 1.2.3")
	}
	if compareSemver("1.2", "1.2.1") >= 0 {
		t.Error("1.2 should be less than 1.2.1")
	}
}

func TestReleaseVersion(t *testing.T) {
	r := &Release{Tag: "v1.2.3"}
	if r.Version() != "1.2.3" {
		t.Errorf("Version() = %q, want 1.2.3", r.Version())
	}
}

func TestCheckerLatest(t *testing.T) {
	rel := Release{
		Tag:    "v9.9.9",
		Body:   "changelog",
		Assets: []Asset{{Name: "lele_Linux_arm64.tar.gz", URL: "http://example/x.tar.gz"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/releases/latest") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(rel)
	}))
	defer srv.Close()

	c := NewChecker("owner/repo")
	// Point at test server by overriding fetch via URL injection:
	// simplest approach — use a custom checker that hits the test server.
	got, err := fetchFrom(c, srv.URL+"/repos/owner/repo/releases/latest")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Tag != "v9.9.9" {
		t.Errorf("Tag = %q, want v9.9.9", got.Tag)
	}
	if got.FindAsset("lele_Linux_arm64.tar.gz") == nil {
		t.Error("expected asset to be found")
	}
	if got.FindAsset("nope") != nil {
		t.Error("expected nil for missing asset")
	}
}

// fetchFrom exercises the decode path against an arbitrary URL.
func fetchFrom(c *Checker, url string) (*Release, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func TestIsDevBuild(t *testing.T) {
	for _, v := range []string{"", "dev", "dev-abc"} {
		if !IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) should be true", v)
		}
	}
	for _, v := range []string{"0.1.0", "v1.0.0"} {
		if IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) should be false", v)
		}
	}
}
