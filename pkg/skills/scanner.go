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

type ScannedSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	HasSKILL    bool   `json:"has_skill"`
}

type ScanSkillsResponse struct {
	Skills []ScannedSkill `json:"skills"`
	Repo   string         `json:"repo"`
}

type githubContent struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

func (si *SkillInstaller) ScanGitHubRepo(ctx context.Context, repo string) ([]ScannedSkill, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	if si.hasSkillFile(ctx, client, repo, "SKILL.md") {
		return []ScannedSkill{{
			Name:        extractRepoName(repo),
			Description: si.fetchSkillDescription(ctx, client, repo, "SKILL.md"),
			Path:        "",
			HasSKILL:    true,
		}}, nil
	}

	skills := make([]ScannedSkill, 0)

	entries, err := si.fetchGitHubContents(ctx, client, repo, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list repo contents: %w", err)
	}

	var skillsSubdirEntries []githubContent
	for _, entry := range entries {
		if entry.Type == "dir" && entry.Name == "skills" {
			skillsSubdirEntries, _ = si.fetchGitHubContents(ctx, client, repo, "skills")
			break
		}
	}

	skills = append(skills, si.scanDirectories(ctx, client, repo, entries, "")...)

	if len(skillsSubdirEntries) > 0 {
		skills = append(skills, si.scanDirectories(ctx, client, repo, skillsSubdirEntries, "skills")...)
	}

	return skills, nil
}

func (si *SkillInstaller) scanDirectories(ctx context.Context, client *http.Client, repo string, entries []githubContent, prefix string) []ScannedSkill {
	skills := make([]ScannedSkill, 0)

	for _, entry := range entries {
		if entry.Type != "dir" {
			continue
		}

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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if err != nil {
		return ""
	}

	return extractDescriptionFromSKILL(string(body))
}

func extractDescriptionFromSKILL(content string) string {
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var contents []githubContent
	if err := json.Unmarshal(body, &contents); err != nil {
		return nil, err
	}

	return contents, nil
}

func extractRepoName(repo string) string {
	parts := strings.Split(repo, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-1]
	}
	return repo
}

func (si *SkillInstaller) InstallMultiple(ctx context.Context, repo string, skillPaths []string) ([]string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	installed := make([]string, 0, len(skillPaths))

	for _, skillPath := range skillPaths {
		skillName := extractRepoName(skillPath)
		if skillName == "" {
			continue
		}

		skillDir := filepath.Join(si.workspace, "skills", skillName)
		if _, err := os.Stat(skillDir); err == nil {
			continue
		}

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

		body, err := readLimitedBody(resp)
		if err != nil {
			continue
		}

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
