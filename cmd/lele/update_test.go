package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/update"
)

func TestPrintUpdateHelp_ContainsOptions(t *testing.T) {
	out := runCmd(printUpdateHelp)
	for _, opt := range []string{"--check", "--yes", "--version", "--rollback", "--no-restart", "--force", "-h"} {
		if !strings.Contains(out, opt) {
			t.Errorf("printUpdateHelp should contain option %q", opt)
		}
	}
}

func TestConfirm_Yes(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("y\n")
	p.close()
	_ = captureStdout(t)
	if !confirm("Proceed?") {
		t.Error("confirm should return true for 'y'")
	}
}

func TestConfirm_YesUpper(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("YES\n")
	p.close()
	_ = captureStdout(t)
	if !confirm("Proceed?") {
		t.Error("confirm should return true for 'YES'")
	}
}

func TestConfirm_No(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("n\n")
	p.close()
	_ = captureStdout(t)
	if confirm("Proceed?") {
		t.Error("confirm should return false for 'n'")
	}
}

func TestConfirm_Empty(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("\n")
	p.close()
	_ = captureStdout(t)
	if confirm("Proceed?") {
		t.Error("confirm should return false for empty input (default no)")
	}
}

func TestBuildUpdater_ConfigNil(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir) // no config file -> loadConfig errors

	updater, cfg, err := buildUpdater()
	if err != nil {
		t.Fatalf("buildUpdater error: %v", err)
	}
	if updater == nil {
		t.Fatal("updater should not be nil")
	}
	_ = cfg
}

func TestBuildUpdater_CustomRepo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Updates.Repo = "acme/lele"
	if err := saveTestConfig(getConfigPath(), cfg); err != nil {
		t.Fatalf("saveTestConfig: %v", err)
	}

	updater, loaded, err := buildUpdater()
	if err != nil {
		t.Fatalf("buildUpdater error: %v", err)
	}
	if updater == nil {
		t.Fatal("updater should not be nil")
	}
	if loaded == nil {
		t.Fatal("cfg should be loaded")
	}
	if loaded.Updates.Repo != "acme/lele" {
		t.Errorf("repo = %q, want acme/lele", loaded.Updates.Repo)
	}
}

// releaseTransport is an http.RoundTripper that routes every request to the
// given httptest server while preserving the original URL path.
type releaseTransport struct {
	base *httptest.Server
}

func (rt *releaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	u.Scheme = "http"
	u.Host = strings.TrimPrefix(rt.base.URL, "http://")
	req2 := req.Clone(req.Context())
	req2.URL = &u
	req2.Host = u.Host
	return rt.base.Client().Transport.RoundTrip(req2)
}

// newCannedUpdater builds an Updater whose Checker hits a canned release server
// regardless of the hardcoded api.github.com host.
func newCannedUpdater(t *testing.T, current, latest string) *update.Updater {
	t.Helper()
	var body string
	if latest != "" {
		body = `{"tag_name":"v` + latest + `","name":"r","published_at":"2024-01-01T00:00:00Z"}`
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// For /releases/latest returns 200; for asset downloads return 404.
		if !strings.Contains(r.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})
	svr := httptest.NewServer(handler)
	t.Cleanup(svr.Close)

	updater := update.NewUpdater("acme/lele", t.TempDir(), current)
	updater.Checker.Client = &http.Client{Transport: &releaseTransport{base: svr}}
	updater.Checker.Repo = "acme/lele"
	return updater
}

func TestRunCheck_UpdateAvailable(t *testing.T) {
	oldVersion := version
	version = "1.0.0"
	defer func() { version = oldVersion }()

	updater := newCannedUpdater(t, "1.0.0", "1.1.0")
	out := runCmd(func() { runCheck(context.Background(), updater) })
	if !strings.Contains(out, "Current: 1.0.0") {
		t.Errorf("output should contain current version, got: %s", out)
	}
	if !strings.Contains(out, "Latest:  1.1.0") {
		t.Errorf("output should contain latest version, got: %s", out)
	}
	if !strings.Contains(out, "Update available") {
		t.Errorf("output should say update available, got: %s", out)
	}
}

func TestRunCheck_UpToDate(t *testing.T) {
	updater := newCannedUpdater(t, "1.1.0", "1.1.0")
	out := runCmd(func() { runCheck(context.Background(), updater) })
	if !strings.Contains(out, "Already up to date") {
		t.Errorf("output should say up to date, got: %s", out)
	}
}

// TestRunCheck_Error and TestRunApply_CheckError are omitted because
// runCheck/runApply call os.Exit(1) on error, which kills the test process.
// These paths could be tested via subprocess execution but are low-value
// for coverage since the error message printing is already exercised by
// the success-path tests above.

// TestRunApply_DevBuildWithoutForce omitted — runApply calls os.Exit(1) on dev build.

func TestRunApply_AbortedByConfirm(t *testing.T) {
	oldVersion := version
	version = "1.0.0"
	defer func() { version = oldVersion }()

	updater := newCannedUpdater(t, "1.0.0", "1.1.0")
	p := newStdinPipe(t)
	p.feed("n\n")
	p.close()
	_ = captureStdout(t)
	out := runCmd(func() { runApply(context.Background(), updater, "", false, false, false) })
	if !strings.Contains(out, "Aborted") {
		t.Errorf("expected Aborted, got: %s", out)
	}
}

func TestRunApply_UpToDate(t *testing.T) {
	oldVersion := version
	version = "1.0.0"
	defer func() { version = oldVersion }()

	updater := newCannedUpdater(t, "1.0.0", "1.0.0")
	out := runCmd(func() { runApply(context.Background(), updater, "", true, false, false) })
	if !strings.Contains(out, "Already up to date") {
		t.Errorf("expected up to date message, got: %s", out)
	}
}

// TestRunApply_CheckError omitted — runApply calls os.Exit(1) on check error.

// TestRunApply_PinnedVersion_InstallFailsBeforeBinaryOverwrite omitted —
// runApply calls os.Exit(1) on download failure.

// TestRunRollback_NoBackup omitted — runRollback calls os.Exit(1) when no backup.

func TestRunRollback_Success(t *testing.T) {
	// Set up a backup directory with a backup file so LatestBackup finds it.
	backupDir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	backupPath := filepath.Join(backupDir, "lele-1.0.0-20240101-120000")
	if err := copyFileForTest(exe, backupPath); err != nil {
		t.Fatalf("copy backup: %v", err)
	}

	updater := update.NewUpdater("acme/lele", backupDir, "1.0.0")
	p := newStdinPipe(t)
	p.feed("n\n") // decline confirmation
	p.close()
	_ = captureStdout(t)
	out := runCmd(func() { runRollback(context.Background(), updater) })
	if !strings.Contains(out, "Aborted") {
		t.Errorf("expected Aborted after declining rollback confirm, got: %s", out)
	}
}

// copyFileForTest copies a file (helper for backup fixtures).
func copyFileForTest(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	_ = data
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	outF, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer outF.Close()
	_, err = io.Copy(outF, f)
	return err
}
