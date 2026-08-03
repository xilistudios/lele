package channels

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/update"
)

func TestSystemVersionRequiresAuth(t *testing.T) {
	ts := newNativeTestServer(t)

	resp, err := http.Get(ts.server.URL + "/api/v1/system/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}
}

func TestSystemVersionReturnsBuildInfo(t *testing.T) {
	ts := newNativeTestServer(t)
	SetBuildInfo("1.2.3", "abc1234", "2026-01-01")

	req, _ := http.NewRequest("GET", ts.server.URL+"/api/v1/system/version", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["version"] != "1.2.3" {
		t.Errorf("version = %v, want 1.2.3", body["version"])
	}
	if body["git_commit"] != "abc1234" {
		t.Errorf("git_commit = %v, want abc1234", body["git_commit"])
	}
	if _, ok := body["supervisor"]; !ok {
		t.Error("expected supervisor field")
	}
}

func TestUpdatesCheckWithoutServiceReturns503(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest("GET", ts.server.URL+"/api/v1/system/updates/check", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 without update service, got %d", resp.StatusCode)
	}
}

func TestUpdatesStatusReturnsIdleState(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.SetUpdateService(update.NewUpdater("", t.TempDir(), "0.1.0"))

	req, _ := http.NewRequest("GET", ts.server.URL+"/api/v1/system/updates/status", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var state update.State
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Phase != update.PhaseIdle {
		t.Errorf("phase = %v, want idle", state.Phase)
	}
}

func TestUpdatesApplyWithoutServiceReturns503(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest("POST", ts.server.URL+"/api/v1/system/updates/apply", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 without update service, got %d", resp.StatusCode)
	}
}

func TestUpdatesRollbackWithoutBackupFails(t *testing.T) {
	ts := newNativeTestServer(t)
	// Empty backup dir → rollback should fail with 500.
	ts.channel.SetUpdateService(update.NewUpdater("", filepath.Join(t.TempDir(), "no-backups"), "0.1.0"))

	req, _ := http.NewRequest("POST", ts.server.URL+"/api/v1/system/updates/rollback", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 with no backup, got %d", resp.StatusCode)
	}
}

func TestUpdatesDisabledReturns403(t *testing.T) {
	// Write a config with updates disabled.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfgContent := `{"updates": {"enabled": false}}`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	ts := newNativeTestServerWithConfigPath(t, cfgPath)
	ts.channel.SetUpdateService(update.NewUpdater("", t.TempDir(), "0.1.0"))

	req, _ := http.NewRequest("GET", ts.server.URL+"/api/v1/system/updates/check", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 when updates disabled, got %d", resp.StatusCode)
	}
}
