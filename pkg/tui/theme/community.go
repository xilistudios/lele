package theme

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

const (
	CommunityRepo = "xilistudios/awesome-lele"

	CommunityBranch = "main"

	communityBaseURL = "https://raw.githubusercontent.com/" +
		CommunityRepo + "/" + CommunityBranch

	communityTimeout = 15 * time.Second
)

type CommunityThemeEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Homepage    string `json:"homepage"`
}

type CommunityIndex []CommunityThemeEntry

var communityNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

func rawURL(path string) string {
	return communityBaseURL + "/" + path
}

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

func validateCommunityName(name string) error {
	if name == "" || !communityNameRe.MatchString(name) {
		return fmt.Errorf(
			"community themes: invalid theme name %q (allowed: a-z, 0-9, '-')",
			name,
		)
	}
	return nil
}
