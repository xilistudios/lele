package skills

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractRepoName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sipeed/lele-skills", "lele-skills"},
		{"user/repo-name", "repo-name"},
		{"owner/name/sub", "sub"},
		{"singlename", "singlename"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractRepoName(tt.input)
			if result != tt.expected {
				t.Errorf("extractRepoName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractDescriptionFromSKILL(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			"with yaml frontmatter",
			`---
name: weather
description: Get current weather and forecasts
---

# Weather Skill

Some content here.`,
			"Get current weather and forecasts",
		},
		{
			"with yaml frontmatter quoted",
			`---
name: weather
description: "Get current weather and forecasts"
---

# Weather Skill`,
			"Get current weather and forecasts",
		},
		{
			"no frontmatter",
			`# Weather Skill

Get current weather and forecasts for any location.`,
			"Get current weather and forecasts for any location.",
		},
		{
			"frontmatter without description",
			`---
name: weather
---

# Weather Skill

This is the description.`,
			"This is the description.",
		},
		{
			"empty content",
			"",
			"",
		},
		{
			"only headers",
			`# Weather Skill

## Description

Some content`,
			"Some content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractDescriptionFromSKILL(tt.content)
			if result != tt.expected {
				t.Errorf("extractDescriptionFromSKILL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestScannedSkill_JSON(t *testing.T) {
	skill := ScannedSkill{
		Name:        "weather",
		Description: "Get weather forecasts",
		Path:        "skills/weather",
		HasSKILL:    true,
	}

	if skill.Name != "weather" {
		t.Errorf("expected name 'weather', got %q", skill.Name)
	}
	if skill.Path != "skills/weather" {
		t.Errorf("expected path 'skills/weather', got %q", skill.Path)
	}
	if !skill.HasSKILL {
		t.Error("expected HasSKILL to be true")
	}
}

func TestScanSkillsResponse(t *testing.T) {
	resp := ScanSkillsResponse{
		Skills: []ScannedSkill{
			{Name: "weather", Path: "weather", HasSKILL: true},
			{Name: "github", Path: "github", HasSKILL: true},
		},
		Repo: "sipeed/lele-skills",
	}

	if len(resp.Skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(resp.Skills))
	}
	if resp.Repo != "sipeed/lele-skills" {
		t.Errorf("expected repo 'sipeed/lele-skills', got %q", resp.Repo)
	}
}

// ---- Tests for scanner.go (HTTP mocked via httptest) ----

func TestScanGitHubRepo_SingleSkillRepo(t *testing.T) {
	// Repo itself is a skill: SKILL.md at root.
	mux := http.NewServeMux()
	mux.HandleFunc("/owner/single/main/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("---\ndescription: A single skill repo\n---\n# Single\nContent"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller("")
	skills, err := si.ScanGitHubRepo(context.Background(), "owner/single")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "single" {
		t.Errorf("expected name 'single', got %q", skills[0].Name)
	}
	if !skills[0].HasSKILL {
		t.Error("expected HasSKILL true")
	}
	if skills[0].Path != "" {
		t.Errorf("expected empty path, got %q", skills[0].Path)
	}
	if skills[0].Description != "A single skill repo" {
		t.Errorf("expected single repo description, got %q", skills[0].Description)
	}
}

func TestScanGitHubRepo_FlatLayout(t *testing.T) {
	// Flat: repo/skill-name/SKILL.md. No skills/ subdir.
	mux := http.NewServeMux()
	// Root listing.
	mux.HandleFunc("/repos/owner/flat/contents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"weather","type":"dir"},{"name":"github","type":"dir"},{"name":".hidden","type":"dir"},{"name":"node_modules","type":"dir"},{"name":"README.md","type":"file"}]`))
	})
	// HEAD checks for SKILL.md in each dir.
	mux.HandleFunc("/owner/flat/main/weather/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte("---\ndescription: Weather skill\n---\n# W"))
	})
	mux.HandleFunc("/owner/flat/main/github/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/owner/flat/main/.hidden/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/owner/flat/main/node_modules/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller("")
	skills, err := si.ScanGitHubRepo(context.Background(), "owner/flat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d: %+v", len(skills), skills)
	}
	if skills[0].Name != "weather" || skills[0].Path != "weather" {
		t.Errorf("unexpected skill: %+v", skills[0])
	}
	if skills[0].Description != "Weather skill" {
		t.Errorf("expected weather description, got %q", skills[0].Description)
	}
}

func TestScanGitHubRepo_NestedLayout(t *testing.T) {
	// Nested: repo/skills/skill-name/SKILL.md
	mux := http.NewServeMux()
	// Root listing: has skills/ dir.
	mux.HandleFunc("/repos/owner/nested/contents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"skills","type":"dir"},{"name":".git","type":"dir"}]`))
	})
	// skills/ listing.
	mux.HandleFunc("/repos/owner/nested/contents/skills", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"weather","type":"dir"}]`))
	})
	// SKILL.md checks.
	mux.HandleFunc("/owner/nested/main/skills/weather/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte("---\ndescription: nested weather\n---\n# W"))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller("")
	skills, err := si.ScanGitHubRepo(context.Background(), "owner/nested")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d: %+v", len(skills), skills)
	}
	if skills[0].Path != "skills/weather" {
		t.Errorf("expected nested path, got %q", skills[0].Path)
	}
}

func TestScanGitHubRepo_FetchContentsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/owner/bad/main/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	// API returns 500.
	mux.HandleFunc("/repos/owner/bad/contents", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller("")
	_, err := si.ScanGitHubRepo(context.Background(), "owner/bad")
	if err == nil {
		t.Error("expected error fetching repo contents")
	} else if !contains(err.Error(), "failed to list repo contents") {
		t.Errorf("expected list error, got %v", err)
	}
}

func TestScanGitHubRepo_EmptyListOK(t *testing.T) {
	// Root listing returns Not Found (404) -> treated as empty list.
	mux := http.NewServeMux()
	mux.HandleFunc("/owner/noskill/main/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/repos/owner/noskill/contents", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller("")
	skills, err := si.ScanGitHubRepo(context.Background(), "owner/noskill")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestInstallMultiple(t *testing.T) {
	ws := t.TempDir()

	mux := http.NewServeMux()
	// weather SKILL.md.
	mux.HandleFunc("/owner/repo/main/weather/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# Weather"))
	})
	// github SKILL.md.
	mux.HandleFunc("/owner/repo/main/github/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# Github"))
	})
	// missing skill -> 404.
	mux.HandleFunc("/owner/repo/main/missing/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller(ws)
	// Install weather and github and missing. Empty path is skipped (empty name).
	installed, err := si.InstallMultiple(context.Background(), "owner/repo", []string{"weather", "github", "missing", ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(installed) != 2 {
		t.Errorf("expected 2 installed, got %d: %v", len(installed), installed)
	}

	// Re-run: existing skills skipped.
	installed2, err := si.InstallMultiple(context.Background(), "owner/repo", []string{"weather"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(installed2) != 0 {
		t.Errorf("expected 0 installed on re-run, got %d", len(installed2))
	}

	// Files written.
	if _, err := os.Stat(filepath.Join(ws, "skills", "weather", "SKILL.md")); err != nil {
		t.Errorf("weather skill not installed: %v", err)
	}
}

// TestHasSkillFile covers hasSkillFile success/failure directly.
func TestHasSkillFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/owner/r/main/skills/weather/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/owner/r/main/skills/missing/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller("")
	client := &http.Client{Timeout: 15 * 1e9}

	if !si.hasSkillFile(context.Background(), client, "owner/r", "skills/weather/SKILL.md") {
		t.Error("expected hasSkillFile true")
	}
	if si.hasSkillFile(context.Background(), client, "owner/r", "skills/missing/SKILL.md") {
		t.Error("expected hasSkillFile false")
	}
}

// TestFetchGitHubContents covers the API listing function directly.
func TestFetchGitHubContents(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/api/contents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"weather","type":"dir"},{"name":"file.txt","type":"file"}]`))
	})
	mux.HandleFunc("/repos/owner/api/contents/skills", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	})
	mux.HandleFunc("/repos/owner/api/contents/notfound", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller("")
	client := &http.Client{}

	// Success.
	entries, err := si.fetchGitHubContents(context.Background(), client, "owner/api", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "weather" || entries[0].Type != "dir" {
		t.Errorf("unexpected entry: %+v", entries[0])
	}

	// 404 -> empty, no error.
	entries, err = si.fetchGitHubContents(context.Background(), client, "owner/api", "notfound")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for 404, got %d", len(entries))
	}

	// 500 -> error.
	_, err = si.fetchGitHubContents(context.Background(), client, "owner/api", "skills")
	if err == nil {
		t.Error("expected error for 500")
	} else if !contains(err.Error(), "GitHub API returned") {
		t.Errorf("expected API error, got %v", err)
	}
}

// TestFetchSkillDescription covers fetchSkillDescription success/error.
func TestFetchSkillDescription(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/owner/d/main/skills/weather/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte("---\ndescription: The weather description\n---\n# W"))
		}
	})
	mux.HandleFunc("/owner/d/main/skills/missing/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller("")
	client := &http.Client{}

	desc := si.fetchSkillDescription(context.Background(), client, "owner/d", "skills/weather/SKILL.md")
	if desc != "The weather description" {
		t.Errorf("expected weather description, got %q", desc)
	}
	if got := si.fetchSkillDescription(context.Background(), client, "owner/d", "skills/missing/SKILL.md"); got != "" {
		t.Errorf("expected empty description, got %q", got)
	}
}

func TestScanDirectories_SkipsNonSkills(t *testing.T) {
	// Directory-level test for scanDirectories: entries include files, hidden dirs.
	mux := http.NewServeMux()
	mux.HandleFunc("/owner/sc/main/skills/weather/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller("")
	entries := []githubContent{
		{Name: "weather", Type: "dir"},
		{Name: "README.md", Type: "file"},
		{Name: ".hidden", Type: "dir"},
		{Name: "node_modules", Type: "dir"},
	}
	client := &http.Client{}
	skills := si.scanDirectories(context.Background(), client, "owner/sc", entries, "skills")
	if len(skills) != 1 || skills[0].Name != "weather" {
		t.Errorf("expected only weather, got %+v", skills)
	}
	if skills[0].Path != "skills/weather" {
		t.Errorf("expected prefixed path, got %q", skills[0].Path)
	}
} // ---- Error/edge paths for scanner + installer ----

// testClient returns an http.Client and mock transport pointing at the server.
func testClient(srv *httptest.Server) *http.Client {
	return &http.Client{Transport: &mockTransport{serverURL: srv.URL, originalRT: http.DefaultTransport}, Timeout: 5 * 1e9}
}

func TestHasSkillFile_RequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	// Closed server -> connection refused.
	srv.Close()

	si := &SkillInstaller{}
	client := testClient(srv)
	if si.hasSkillFile(context.Background(), client, "owner/repo", "x/SKILL.md") {
		t.Error("expected hasSkillFile false on connection error")
	}
}

func TestFetchSkillDescription_NonOK(t *testing.T) {
	// Read error: handler closes body after writing header, client gets EOF mid-read.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		// hijack and close to cause read error
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer srv.Close()

	si := &SkillInstaller{}
	desc := si.fetchSkillDescription(context.Background(), testClient(srv), "owner/repo", "x/SKILL.md")
	// Either empty or a description; must not panic.
	_ = desc
}

func TestFetchSkillDescription_RequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	si := &SkillInstaller{}
	if got := si.fetchSkillDescription(context.Background(), testClient(srv), "owner/repo", "x"); got != "" {
		t.Errorf("expected empty description on error, got %q", got)
	}
}

func TestScanGitHubRepo_HideDoubleRemove(t *testing.T) {
	// Covers fetchGitHubContents read error (body read fails -> error returned).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/single/main/SKILL.md" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// API listing returns a body that is not JSON -> unmarshal error.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("this is not json at all"))
	}))
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := &SkillInstaller{}
	_, err := si.ScanGitHubRepo(context.Background(), "single")
	if err == nil {
		t.Error("expected error for invalid JSON listing")
	} else if !contains(err.Error(), "failed to list repo contents") {
		t.Errorf("expected list error, got %v", err)
	}
}

func TestFetchGitHubContents_ReadErrorAndRequestError(t *testing.T) {
	// Request error: closed server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	si := &SkillInstaller{}
	if _, err := si.fetchGitHubContents(context.Background(), testClient(srv), "o/r", ""); err == nil {
		t.Error("expected error on closed server")
	}
}

func TestFetchGitHubContents_CannotConnect(t *testing.T) {
	si := &SkillInstaller{}
	client := &http.Client{Timeout: 5 * 1e9}
	// Point at a port almost certainly closed.
	client.Transport = &mockTransport{serverURL: "http://127.0.0.1:1", originalRT: http.DefaultTransport}
	_, err := si.fetchGitHubContents(context.Background(), client, "o/r", "")
	if err == nil {
		t.Error("expected error when host is unreachable")
	}
}

func TestInstallMultiple_WriteError(t *testing.T) {
	// Make skills dir uncreatable by placing a file where `<ws>/skills` should go.
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "skills"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("# content"))
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller(ws)
	installed, err := si.InstallMultiple(context.Background(), "owner/repo", []string{"skillname"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// MkdirAll will fail because a file occupies the dir path -> skill not installed.
	if len(installed) != 0 {
		t.Errorf("expected 0 installed due to mkdir failure, got %d", len(installed))
	}
}

func TestInstallMultiple_RequestAndReadErrors(t *testing.T) {
	ws := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller(ws)
	installed, err := si.InstallMultiple(context.Background(), "owner/repo", []string{"weather"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Connection error -> skill skipped.
	if len(installed) != 0 {
		t.Errorf("expected 0 installed on connection error, got %d", len(installed))
	}
}
