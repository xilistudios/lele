package update

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cannedCheckerClient returns a Checker whose interceptor always returns
// the given status/body.
func cannedCheckerClient(t *testing.T, status int, body string) *Checker {
	t.Helper()
	return &Checker{
		Repo: "o/r",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return cannedResponse(req, status, body), nil
		})},
	}
}

func TestNewUpdater(t *testing.T) {
	u := NewUpdater("o/r", "/tmp/bk", "1.0.0")
	if u.Checker == nil || u.Downloader == nil || u.Installer == nil || u.Restarter == nil {
		t.Fatal("expected all components wired")
	}
	if u.CurrentVersion != "1.0.0" {
		t.Errorf("CurrentVersion = %q", u.CurrentVersion)
	}
	if u.State().Phase != PhaseIdle {
		t.Errorf("initial phase = %q, want idle", u.State().Phase)
	}

	r := NewUpdater("", "", "")
	if r.Checker == nil || r.Checker.Repo != DefaultRepo {
		t.Errorf("default repo = %q, want %q", r.Checker.Repo, DefaultRepo)
	}
	if r.Installer.BackupDir != "" {
		t.Error("installer backup dir should be empty")
	}
}

func TestStateBusyEmit(t *testing.T) {
	u := NewUpdater("", "", "1.0.0")
	if u.Busy() {
		t.Error("expected not busy initially")
	}
	u.mu.Lock()
	u.busy = true
	u.mu.Unlock()
	if !u.Busy() {
		t.Error("expected busy")
	}
	u.mu.Lock()
	u.busy = false
	u.mu.Unlock()

	var got []State
	u.emit(Options{Progress: func(s State) { got = append(got, s) }}, State{Phase: PhaseChecking})
	if len(got) != 1 || got[0].Phase != PhaseChecking {
		t.Fatalf("emit callbacks: %+v", got)
	}
	if u.State().Phase != PhaseChecking {
		t.Errorf("phase = %q", u.State().Phase)
	}
}

func TestCheck_Success(t *testing.T) {
	rel := Release{Tag: "v9.9.9", Body: "notes"}
	rel.PublishedAt = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rel.HTMLURL = "http://x"
	u := NewUpdater("", "", "0.1.0")
	u.Checker = cannedCheckerClient(t, 200, marshalReleaseJSON(rel))
	info, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.Current != "0.1.0" || info.Latest != "9.9.9" {
		t.Errorf("info = %+v", info)
	}
	if !info.UpdateAvailable {
		t.Error("expected update available")
	}
	if info.Changelog != "notes" || info.HTMLURL != "http://x" {
		t.Errorf("info = %+v", info)
	}
}

func TestCheck_NoUpdate(t *testing.T) {
	rel := Release{Tag: "v0.1.0"}
	u := NewUpdater("", "", "0.1.0")
	u.Checker = cannedCheckerClient(t, 200, marshalReleaseJSON(rel))
	info, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.UpdateAvailable {
		t.Error("expected no update")
	}
}

func TestCheck_Error(t *testing.T) {
	u := NewUpdater("", "", "0.1.0")
	u.Checker = cannedCheckerClient(t, 404, "{}")
	if _, err := u.Check(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestApply_AlreadyBusy(t *testing.T) {
	u := NewUpdater("", t.TempDir(), "0.1.0")
	u.mu.Lock()
	u.busy = true
	u.mu.Unlock()
	if _, err := u.Apply(context.Background(), Options{}); err == nil {
		t.Fatal("expected busy error")
	}
}

func TestApply_CheckError(t *testing.T) {
	u := NewUpdater("", t.TempDir(), "0.1.0")
	u.Checker = cannedCheckerClient(t, 500, "{}")
	if _, err := u.Apply(context.Background(), Options{}); err == nil {
		t.Fatal("expected error")
	}
	if u.State().Phase != PhaseFailed {
		t.Errorf("phase = %q, want failed", u.State().Phase)
	}
	if u.State().Error == "" {
		t.Error("expected error message in state")
	}
	if u.Busy() {
		t.Error("busy should be false after failed apply")
	}
}

func TestApply_AlreadyUpToDate(t *testing.T) {
	rel := Release{Tag: "v1.0.0"}
	u := NewUpdater("", t.TempDir(), "1.0.0")
	u.Checker = cannedCheckerClient(t, 200, marshalReleaseJSON(rel))
	v, err := u.Apply(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if v != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", v)
	}
	if u.State().Phase != PhaseDone {
		t.Errorf("phase = %q, want done", u.State().Phase)
	}
	if u.Busy() {
		t.Error("busy should be false")
	}
}

func TestApply_DownloadFailure(t *testing.T) {
	rel := Release{Tag: "v9.9.9"} // no assets
	u := NewUpdater("", t.TempDir(), "0.1.0")
	u.Checker = cannedCheckerClient(t, 200, marshalReleaseJSON(rel))
	if _, err := u.Apply(context.Background(), Options{}); err == nil {
		t.Fatal("expected download failure")
	}
	if u.State().Phase != PhaseFailed {
		t.Errorf("phase = %q, want failed", u.State().Phase)
	}
}

func TestApply_ByTagResolvesToOlder(t *testing.T) {
	// Version set -> ByTag; also NewerVersion guard is skipped when
	// opts.Version != "" so an older pinned version still installs.
	// With no assets it fails at download, covering resolution + guard.
	rel := Release{Tag: "v0.0.1"}
	u := NewUpdater("", t.TempDir(), "1.0.0")
	u.Checker = cannedCheckerClient(t, 200, marshalReleaseJSON(rel))
	if _, err := u.Apply(context.Background(), Options{Version: "0.0.1"}); err == nil {
		t.Fatal("expected failure (no assets)")
	}
	if u.State().Phase != PhaseFailed {
		t.Errorf("phase = %q, want failed", u.State().Phase)
	}
}

func TestApply_ValidationFailure(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("tar.gz pipeline only")
	}
	// Fake binary reports 999.0.0 but release claims 9.9.9.
	fakeBin := buildFakeLele(t, "999.0.0")
	checker, srv := newUpdatePipeline(t, fakeBin, ArchiveName(platform), "v9.9.9", "9.9.9")
	defer srv.Close()
	u := NewUpdater("o/r", t.TempDir(), "0.1.0")
	u.Checker = checker
	if _, err := u.Apply(context.Background(), Options{}); err == nil {
		t.Fatal("expected validation failure")
	}
	if u.State().Phase != PhaseFailed {
		t.Errorf("phase = %q, want failed", u.State().Phase)
	}
}

// TestApply_FullSuccess runs the entire pipeline. On Unix it installs
// over the running test binary's path, which is safe because the running
// process keeps its already-mapped inode after rename.
func TestApply_FullSuccess(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("rename-over-running-exe unsupported")
	}
	fakeBin := buildFakeLele(t, "9.9.9")
	checker, srv := newUpdatePipeline(t, fakeBin, ArchiveName(platform), "v9.9.9", "9.9.9")
	defer srv.Close()

	u := NewUpdater("o/r", t.TempDir(), "0.1.0")
	u.Checker = checker

	var progressStates []Phase
	v, err := u.Apply(context.Background(), Options{
		Progress: func(s State) { progressStates = append(progressStates, s.Phase) },
	})
	if err != nil {
		t.Fatalf("Apply full: %v", err)
	}
	if v != "9.9.9" {
		t.Errorf("version = %q, want 9.9.9", v)
	}
	if u.State().Phase != PhaseDone {
		t.Errorf("phase = %q, want done", u.State().Phase)
	}
	if u.Busy() {
		t.Error("busy should be false")
	}
	if len(progressStates) < 5 {
		t.Errorf("expected several phases, got %v", progressStates)
	}
}

func TestApply_RestartFailedNotFatal(t *testing.T) {
	// Restart with a Restarter that errors. Install already succeeded, so
	// Apply should report Done with an Error note instead of failing.
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("rename-over-running-exe unsupported")
	}
	fakeBin := buildFakeLele(t, "9.9.9")
	checker, srv := newUpdatePipeline(t, fakeBin, ArchiveName(platform), "v9.9.9", "9.9.9")
	defer srv.Close()
	u := NewUpdater("o/r", t.TempDir(), "0.1.0")
	u.Checker = checker
	// Force Restart failure by making selfExec fail: Configuring the
	// restarter with a UnitNames won't help (no supervisor). Instead make
	// CurrentBinaryPath fail via an impossible cwd? Simpler: give Restarter
	// a UnitName and set INVOCATION env so systemd path fails through fake
	// systemctl returning error. But systemd scope on this host is active,
	// so set env to trigger systemd-user supervisor.
	t.Setenv("INVOCATION_ID", "x")
	t.Setenv("FAKE_INVOCATION_USER", "x")
	t.Setenv("PATH", "/usr/bin")
	// Without our fake systemctl on PATH, real systemctl runs; user scope
	// show for lele.service returns empty (not "x"), findUnit fails, fall
	// back to `systemctl --user restart lele.service` which errors because
	// there's no user bus / unit. This yields an error from Restart.
	v, err := u.Apply(context.Background(), Options{Restart: true})
	if err != nil {
		t.Fatalf("Apply with restart: %v", err)
	}
	if v != "9.9.9" {
		t.Errorf("version = %q, want 9.9.9", v)
	}
	if u.State().Phase != PhaseDone {
		t.Errorf("phase = %q, want done", u.State().Phase)
	}
	if u.State().Error == "" {
		t.Error("expected an Error note about restart failure")
	}
}

func TestRollback_NoBackup(t *testing.T) {
	u := NewUpdater("", t.TempDir(), "1.0.0")
	if _, err := u.Rollback(context.Background()); err == nil {
		t.Fatal("expected error with no backups")
	}
}

func TestRollback_Success(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(backupDir, "lele-0.1.0-20250101-010101")
	if err := os.WriteFile(backup, []byte("backup-binary"), 0755); err != nil {
		t.Fatal(err)
	}

	u := NewUpdater("o/r", backupDir, "0.1.0")
	// Installer writes to CurrentBinaryPath of this test process. On Unix
	// that's safe. We assert it returns the backup path on success.
	p, err := u.Rollback(context.Background())
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if p != backup {
		t.Errorf("rollback = %q, want %q", p, backup)
	}
}

func TestValidateBinary_Unreachable(t *testing.T) {
	if err := validateBinary("/nonexistent/path", "1.0.0"); err == nil {
		t.Fatal("expected error running missing binary")
	}
}

func TestValidateBinary_VersionMismatch(t *testing.T) {
	fakeBin := buildFakeLele(t, "1.2.3")
	if err := validateBinary(fakeBin, "9.9.9"); err == nil {
		t.Fatal("expected version mismatch error")
	}
}

func TestValidateBinary_Success(t *testing.T) {
	fakeBin := buildFakeLele(t, "9.9.9")
	if err := validateBinary(fakeBin, "9.9.9"); err != nil {
		t.Fatalf("validateBinary: %v", err)
	}
}