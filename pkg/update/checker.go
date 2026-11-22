package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRepo      = "xilistudios/lele"
	DefaultUserAgent = "lele-self-update"
)

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type Release struct {
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []Asset   `json:"assets"`
}

func (r *Release) Version() string {
	return strings.TrimPrefix(r.Tag, "v")
}

func (r *Release) FindAsset(name string) *Asset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

type Checker struct {
	Repo   string
	Client *http.Client
}

func NewChecker(repo string) *Checker {
	if repo == "" {
		repo = DefaultRepo
	}
	return &Checker{
		Repo:   repo,
		Client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Checker) Latest(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.Repo)
	return c.fetch(ctx, url)
}

func (c *Checker) ByTag(ctx context.Context, tag string) (*Release, error) {
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", c.Repo, tag)
	return c.fetch(ctx, url)
}

func (c *Checker) fetch(ctx context.Context, url string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", DefaultUserAgent)
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("release not found (404)")
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == 429 {
		return nil, fmt.Errorf("github API rate limit exceeded (HTTP %d); set GITHUB_TOKEN or try later", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned HTTP %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}
	return &rel, nil
}

func NewerVersion(current, latest string) bool {
	cur := normalizeVersion(current)
	lat := normalizeVersion(latest)
	if cur == "" || lat == "" {
		return false
	}
	return compareSemver(lat, cur) > 0
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" || v == "dev" {
		return ""
	}
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	return v
}

func compareSemver(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		var an, bn int
		if i < len(ap) {
			an, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			bn, _ = strconv.Atoi(bp[i])
		}
		if an != bn {
			return an - bn
		}
	}
	return 0
}
