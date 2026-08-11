package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ScannedSkill represents a skill found in a GitHub repo.
type ScannedSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"` // e.g. "weather" or "skills/weather"
	HasSKILL    bool   `json:"has_skill"`
}

// ScanSkillsResponse is the response from scanning a repo.
type ScanSkillsResponse struct {
	Skills []ScannedSkill `json:"skills"`
	Repo   string         `json:"repo"`
}

// githubContent represents a GitHub API directory entry.
type githubContent struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"` // "file" or "dir"
	SHA  string `json:"sha"`
}

// ScanGitHubRepo discovers skills in a GitHub repo by checking for
// SKILL.md files in subdirectories. Supports two layouts:
//   - Flat: repo/skill-name/SKILL.md
//   - Nested: repo/skills/skill-name/SKILL.md
//
// Also detects single-skill repos where SKILL.md is at the root.
func (si *SkillInstaller) ScanGitHubRepo(ctx context.Context, repo string) ([]ScannedSkill, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	// First check if the repo itself is a single skill (SKILL.md at root)
	if si.hasSkillFile(ctx, client, repo, "SKILL.md") {
		return []ScannedSkill{{
			Name:        extractRepoName(repo),
			Description: si.fetchSkillDescription(ctx, client, repo, "SKILL.md"),
			Path:        "",
			HasSKILL:    true,
		}}, nil
	}

	// Scan for skills in subdirectories
	skills := make([]ScannedSkill, 0)

	// Try to get directory listing via GitHub API
	entries, err := si.fetchGitHubContents(ctx, client, repo, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list repo contents: %w", err)
	}

	// Check for skills/ subdirectory (nested layout)
	var skillsSubdirEntries []githubContent
	for _, entry := range entries {
		if entry.Type == "dir" && entry.Name == "skills" {
			skillsSubdirEntries, _ = si.fetchGitHubContents(ctx, client, repo, "skills")
			break
		}
	}

	// Scan top-level directories for SKILL.md
	skills = append(skills, si.scanDirectories(ctx, client, repo, entries, "")...)

	// Scan skills/ subdirectory for SKILL.md
	if len(skillsSubdirEntries) > 0 {
		skills = append(skills, si.scanDirectories(ctx, client, repo, skillsSubdirEntries, "skills")...)
	}

	return skills, nil
}

// scanDirectories checks each directory entry for a SKILL.md file.
func (si *SkillInstaller) scanDirectories(ctx context.Context, client *http.Client, repo string, entries []githubContent, prefix string) []ScannedSkill {
	skills := make([]ScannedSkill, 0)

	for _, entry := range entries {
		if entry.Type != "dir" {
			continue
		}

		// Skip hidden directories and common non-skill dirs
		if strings.HasPrefix(entry.Name, ".") || entry.Name == "node_modules" || entry.Name == ".git" {
			continue
		}

		skillPath := entry.Name
		if prefix != "" {
			skillPath = prefix + "/" + entry.Name
		}

		skillFilePath := skillPath + "/SKILL.md"
		if si.hasSkillFile(ctx, client, repo, skillFilePath) {
			skills = append(skills, ScannedSkill{
				Name:        entry.Name,
				Description: si.fetchSkillDescription(ctx, client, repo, skillFilePath),
				Path:        skillPath,
				HasSKILL:    true,
			})
		}
	}

	return skills
}

// hasSkillFile checks if a SKILL.md file exists at the given path in the repo.
func (si *SkillInstaller) hasSkillFile(ctx context.Context, client *http.Client, repo, path string) bool {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/main/%s", repo, path)

	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// fetchSkillDescription reads the first few lines of SKILL.md to extract description.
func (si *SkillInstaller) fetchSkillDescription(ctx context.Context, client *http.Client, repo, path string) string {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/main/%s", repo, path)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return ""
	}

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	// Read first 2KB to extract description from frontmatter
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if err != nil {
		return ""
	}

	return extractDescriptionFromSKILL(string(body))
}

// extractDescriptionFromSKILL parses SKILL.md content to extract description.
// Looks for YAML frontmatter or first paragraph.
func extractDescriptionFromSKILL(content string) string {
	// Try YAML frontmatter
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	frontmatterEnded := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			// End of frontmatter
			frontmatterEnded = true
			inFrontmatter = false
			continue
		}

		if inFrontmatter && strings.HasPrefix(trimmed, "description:") {
			desc := strings.TrimPrefix(trimmed, "description:")
			desc = strings.TrimSpace(desc)
			desc = strings.Trim(desc, "\"'")
			if desc != "" {
				return desc
			}
		}
	}

	// Fallback: use first non-empty, non-header line after frontmatter
	inContent := false
	inFrontmatter = false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			inContent = true
			inFrontmatter = false
			continue
		}

		if !inFrontmatter && !inContent {
			if frontmatterEnded {
				inContent = true
			} else if trimmed != "" {
				// No frontmatter - return first non-header line
				if !strings.HasPrefix(trimmed, "#") {
					return trimmed
				}
			}
		}

		if inContent && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return trimmed
		}
	}

	return ""
}

// fetchGitHubContents lists directory contents via GitHub API.
func (si *SkillInstaller) fetchGitHubContents(ctx context.Context, client *http.Client, repo, path string) ([]githubContent, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repo, path)
	url = strings.TrimRight(url, "/")

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return []githubContent{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, err
	}

	var contents []githubContent
	if err := json.Unmarshal(body, &contents); err != nil {
		return nil, err
	}

	return contents, nil
}

// extractRepoName returns the repo name from "owner/repo" format.
func extractRepoName(repo string) string {
	parts := strings.Split(repo, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-1]
	}
	return repo
}

// InstallMultiple installs selected skills from a scanned repo.
// skillPaths are the relative paths within the repo (e.g. ["weather", "github"]).
// Returns list of successfully installed skill names.
func (si *SkillInstaller) InstallMultiple(ctx context.Context, repo string, skillPaths []string) ([]string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	installed := make([]string, 0, len(skillPaths))

	for _, skillPath := range skillPaths {
		skillName := extractRepoName(skillPath)
		if skillName == "" {
			continue
		}

		// Check if already exists
		skillDir := filepath.Join(si.workspace, "skills", skillName)
		if _, err := os.Stat(skillDir); err == nil {
			continue // Skip existing
		}

		// Fetch SKILL.md
		skillFilePath := skillPath + "/SKILL.md"
		if skillPath == "" {
			skillFilePath = "SKILL.md"
		}

		url := fmt.Sprintf("https://raw.githubusercontent.com/%s/main/%s", repo, skillFilePath)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			continue
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			continue
		}

		// Create directory and write SKILL.md
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			continue
		}

		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), body, 0644); err != nil {
			continue
		}

		installed = append(installed, skillName)
	}

	return installed, nil
}
