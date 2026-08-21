package theme

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

const (
	// CommunityRepo is the GitHub owner/repo for community themes.
	CommunityRepo = "xilistudios/awesome-lele"

	// CommunityBranch is the branch to fetch themes from.
	CommunityBranch = "main"

	// communityBaseURL is the raw.githubusercontent.com base URL for the
	// community themes repo.
	communityBaseURL = "https://raw.githubusercontent.com/" +
		CommunityRepo + "/" + CommunityBranch

	// communityTimeout is the HTTP client timeout used for all community
	// fetches.
	communityTimeout = 15 * time.Second
)

// CommunityThemeEntry represents one entry in the community themes index.json.
type CommunityThemeEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Homepage    string `json:"homepage"`
}

// CommunityIndex is the parsed index.json from the community themes repo.
// It is a slice of CommunityThemeEntry.
type CommunityIndex []CommunityThemeEntry

// communityNameRe matches theme names allowed in theme.json paths: only
// lowercase letters, digits, and hyphens. It guards against path
// traversal when building the fetch URL.
var communityNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// rawURL builds a raw.githubusercontent.com URL for a file path in the repo.
func rawURL(path string) string {
	return communityBaseURL + "/" + path
}

// FetchCommunityIndex downloads the themes/index.json from the awesome-lele
// repo via raw.githubusercontent.com. Returns the parsed index or an error.
// Uses a 15-second timeout. Never panics.
func FetchCommunityIndex() ([]CommunityThemeEntry, error) {
	resp, err := communityGet("themes/index.json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var idx CommunityIndex
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return nil, fmt.Errorf("community themes: %w", err)
	}
	return idx, nil
}

// FetchCommunityTheme downloads themes/<name>/theme.json from the
// awesome-lele repo. Returns the parsed Theme (already Normalize()d) or
// an error. Uses a 15-second timeout. Never panics.
func FetchCommunityTheme(name string) (Theme, error) {
	if err := validateCommunityName(name); err != nil {
		return Theme{}, err
	}

	resp, err := communityGet("themes/" + name + "/theme.json")
	if err != nil {
		return Theme{}, err
	}
	defer resp.Body.Close()

	var t Theme
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return Theme{}, fmt.Errorf("community themes: %w", err)
	}
	return *t.Normalize(), nil
}

// communityGet performs a GET request against the community themes repo
// with a 15-second timeout. It validates that the HTTP status code is 200.
func communityGet(path string) (*http.Response, error) {
	client := &http.Client{Timeout: communityTimeout}

	resp, err := client.Get(rawURL(path))
	if err != nil {
		return nil, fmt.Errorf("community themes: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf(
			"community themes: GET %s: unexpected status %s",
			path, resp.Status,
		)
	}

	return resp, nil
}

// validateCommunityName rejects empty or unsafe theme names. Only
// lowercase letters, digits, and hyphens are allowed, which prevents path
// traversal when building the fetch URL.
func validateCommunityName(name string) error {
	if name == "" || !communityNameRe.MatchString(name) {
		return fmt.Errorf(
			"community themes: invalid theme name %q (allowed: a-z, 0-9, '-')",
			name,
		)
	}
	return nil
}
