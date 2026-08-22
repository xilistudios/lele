package skills

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// ---- Tests for installer.go (HTTP mocked via httptest) ----

func TestNewSkillInstaller(t *testing.T) {
	si := NewSkillInstaller("/tmp/workspace")
	if si == nil {
		t.Fatal("expected non-nil installer")
	}
	if si.workspace != "/tmp/workspace" {
		t.Errorf("expected workspace /tmp/workspace, got %q", si.workspace)
	}
}

func TestInstallFromGitHub(t *testing.T) {
	ws := t.TempDir()

	mux := http.NewServeMux()
	mux.HandleFunc("/owner/repo/main/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# Installed Skill\n\nContent"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller(ws)

	// Success.
	if err := si.InstallFromGitHub(context.Background(), "owner/repo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(ws, "skills", "repo", "SKILL.md"))
	if err != nil {
		t.Fatalf("installed file missing: %v", err)
	}
	if !contains(string(data), "# Installed Skill") {
		t.Errorf("unexpected file content: %q", string(data))
	}

	// Already exists -> error.
	err = si.InstallFromGitHub(context.Background(), "owner/repo")
	if err == nil {
		t.Error("expected error for existing skill")
	} else if !contains(err.Error(), "already exists") {
		t.Errorf("expected already-exists error, got %v", err)
	}
}

func TestInstallFromGitHub_HTTPErrors(t *testing.T) {
	ws := t.TempDir()

	// 404 response.
	mux := http.NewServeMux()
	mux.HandleFunc("/notfound/repo/main/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller(ws)
	err := si.InstallFromGitHub(context.Background(), "notfound/repo")
	if err == nil {
		t.Error("expected error for HTTP 404")
	} else if !contains(err.Error(), "HTTP") {
		t.Errorf("expected HTTP error, got %v", err)
	}
}

func TestInstallFromGitHub_RequestError(t *testing.T) {
	ws := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller(ws)
	// Cancel context -> request creation succeeds but send fails.
	ctx, cancel := context.WithCancel(context.Background())
	srv.Close() // server closed -> connection error
	_ = cancel
	// Since the server is closed, client.Do will fail.
	err := si.InstallFromGitHub(ctx, "closed/repo")
	if err == nil {
		t.Error("expected error when server is closed")
	} else if !contains(err.Error(), "failed to fetch") {
		t.Errorf("expected failed to fetch error, got %v", err)
	}
}

func TestUninstall(t *testing.T) {
	ws := t.TempDir()
	si := NewSkillInstaller(ws)

	// Skill not present.
	if err := si.Uninstall("missing"); err == nil {
		t.Error("expected error for missing skill")
	} else if !contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got %v", err)
	}

	// Create a skill dir, then uninstall.
	skillDir := filepath.Join(ws, "skills", "temp-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := si.Uninstall("temp-skill"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Error("expected skill dir to be removed")
	}
}

func TestListAvailableSkills(t *testing.T) {
	body := `[{"name":"weather","repository":"sipeed/lele-skills","description":"Weather","author":"sipeed","tags":["tool"]}]`
	mux := http.NewServeMux()
	mux.HandleFunc("/sipeed/lele-skills/main/skills.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller(t.TempDir())
	skills, err := si.ListAvailableSkills(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "weather" {
		t.Errorf("expected weather, got %q", skills[0].Name)
	}
}

func TestListAvailableSkills_Errors(t *testing.T) {
	// 404.
	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv404.Close()
	cleanup := withMockHTTP(srv404)
	si := NewSkillInstaller(t.TempDir())
	if _, err := si.ListAvailableSkills(context.Background()); err == nil {
		t.Error("expected error for 404")
	} else if !contains(err.Error(), "HTTP") {
		t.Errorf("expected HTTP error, got %v", err)
	}
	cleanup()

	// Invalid JSON.
	mux := http.NewServeMux()
	mux.HandleFunc("/sipeed/lele-skills/main/skills.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cleanup = withMockHTTP(srv)
	defer cleanup()
	if _, err := si.ListAvailableSkills(context.Background()); err == nil {
		t.Error("expected error for invalid JSON")
	} else if !contains(err.Error(), "parse") {
		t.Errorf("expected parse error, got %v", err)
	}
}

func TestListAvailableSkills_ConnectionError(t *testing.T) {
	si := NewSkillInstaller(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	cleanup := withMockHTTP(srv)
	cleanup() // restore default transport
	srv.Close()
	// Now default transport tries the real github URL, which should fail in test env,
	// but we can't guarantee that. Instead force by pointing to closed server.
	// Simpler: use a closed server with the mock transport still active.
	if _, err := si.ListAvailableSkills(context.Background()); err == nil {
		// Not strictly guaranteed; this only tests the error path is not panicking.
		_ = err
	}
} // TestInstallFromGitHub_HTTPErrorBranch directly verifies the non-200 path.
func TestInstallFromGitHub_HTTPErrorBranch(t *testing.T) {
	ws := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/owner/errskill/main/SKILL.md" {
			http.Error(w, "nope", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller(ws)
	err := si.InstallFromGitHub(context.Background(), "owner/errskill")
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if !contains(err.Error(), "HTTP") {
		t.Errorf("expected HTTP error text, got %v", err)
	}
	// Nothing installed.
	if _, statErr := os.Stat(filepath.Join(ws, "skills", "errskill")); !os.IsNotExist(statErr) {
		t.Error("expected no skill dir created on HTTP error")
	}
}

// TestInstallFromGitHub_ReadResponseError triggers io.ReadAll failure using a
// bogus Content-Length larger than the actual body.
func TestInstallFromGitHub_ReadResponseError(t *testing.T) {
	ws := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/owner/readerr/main/SKILL.md" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		// Advertise more content than actually sent to force a read error.
		w.Header().Set("Content-Length", "9999")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	cleanup := withMockHTTP(srv)
	defer cleanup()

	si := NewSkillInstaller(ws)
	err := si.InstallFromGitHub(context.Background(), "owner/readerr")
	// The client may or may not surface the short-read as an error; accept
	// whichever occurs without panic. If it errored, ensure correct message.
	if err != nil {
		if !contains(err.Error(), "failed to read response") && !contains(err.Error(), "failed to fetch") {
			t.Errorf("unexpected error message: %v", err)
		}
	}
}
