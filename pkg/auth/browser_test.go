package auth

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These tests drive startAndConfirm with `sh` rather than a platform launcher:
// the point of that split is precisely that the wait/exit logic can be proven
// without spawning a browser or depending on xdg-open/open being installed.
// Windows has no sh, and browserCommand is asserted per platform instead.

// requirePOSIX skips a test on a platform without a POSIX shell.
func requirePOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX shell on Windows")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh not available: %v", err)
	}
}

// TestStartAndConfirm_ExitsZero: a launcher that exits cleanly within the
// budget is a success.
func TestStartAndConfirm_ExitsZero(t *testing.T) {
	requirePOSIX(t)
	if err := startAndConfirm(exec.Command("sh", "-c", "exit 0"), toolWait); err != nil {
		t.Fatalf("startAndConfirm(exit 0) = %v, want nil", err)
	}
}

// TestStartAndConfirm_NonZeroExitIsAnError is the behaviour the caller needs:
// xdg-open starting is not the same as the URL opening, and its failure must
// reach the caller.
func TestStartAndConfirm_NonZeroExitIsAnError(t *testing.T) {
	requirePOSIX(t)
	err := startAndConfirm(exec.Command("sh", "-c", "exit 3"), toolWait)
	if err == nil {
		t.Fatal("startAndConfirm(exit 3) = nil, want an error")
	}
	if !strings.Contains(err.Error(), "status 3") {
		t.Errorf("error = %q, want it to name the exit status", err)
	}
	if strings.Contains(err.Error(), "-c") {
		t.Errorf("error = %q, want no command line (it carries the URL)", err)
	}
}

// TestStartAndConfirm_StillRunningIsASuccess: a launcher that has not exited
// after the budget is assumed to have handed the URL off (some launchers stay
// as the browser's parent). It must not block past the budget either.
func TestStartAndConfirm_StillRunningIsASuccess(t *testing.T) {
	requirePOSIX(t)
	start := time.Now()
	err := startAndConfirm(exec.Command("sh", "-c", "sleep 0.4"), 30*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("startAndConfirm(still running) = %v, want nil", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("startAndConfirm blocked for %v, want it bounded by the 30ms budget", elapsed)
	}
}

// TestStartAndConfirm_ZeroWaitDoesNotBlock: a non-positive budget means "start
// it and do not wait", the pre-existing OpenBrowser behaviour.
func TestStartAndConfirm_ZeroWaitDoesNotBlock(t *testing.T) {
	requirePOSIX(t)
	start := time.Now()
	if err := startAndConfirm(exec.Command("sh", "-c", "sleep 0.3"), 0); err != nil {
		t.Fatalf("startAndConfirm(0 wait) = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("startAndConfirm(0 wait) blocked for %v", elapsed)
	}
}

// TestStartAndConfirm_StartFailureIsReported covers the missing launcher: no
// xdg-open installed at all.
func TestStartAndConfirm_StartFailureIsReported(t *testing.T) {
	err := startAndConfirm(exec.Command("/nonexistent/lele-browser-launcher", "https://example.test/"), toolWait)
	if err == nil {
		t.Fatal("startAndConfirm(missing binary) = nil, want an error")
	}
}

// TestBrowserCommand_PerPlatform pins the one platform switch: the command each
// OS is opened with, and the URL as its last argument.
func TestBrowserCommand_PerPlatform(t *testing.T) {
	url := "https://auth.example.test/authorize?client_id=abc&state=s"
	cmd, err := browserCommand(url)
	if err != nil {
		t.Fatalf("browserCommand = %v, want a command", err)
	}
	args := cmd.Args
	if len(args) < 2 || args[len(args)-1] != url {
		t.Fatalf("browserCommand args = %v, want the url as the last argument", args)
	}

	var want string
	switch runtime.GOOS {
	case "darwin":
		want = "open"
	case "linux":
		want = "xdg-open"
	case "windows":
		want = "cmd"
	default:
		t.Skipf("unsupported platform %s", runtime.GOOS)
	}
	if args[0] != want {
		t.Errorf("launcher = %q, want %q on %s", args[0], want, runtime.GOOS)
	}
}

// TestLauncherExitError_NeverCarriesTheCommandLine guards the "no value in an
// error" rule this package follows for every OAuth failure.
func TestLauncherExitError_NeverCarriesTheCommandLine(t *testing.T) {
	requirePOSIX(t)
	cmd := exec.Command("sh", "-c", "exit 4")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected the command to fail")
	} else {
		got := launcherExitError(err)
		if strings.Contains(got.Error(), "sh") {
			t.Errorf("error = %q, want no command name/line", got)
		}
		if !strings.Contains(got.Error(), "status 4") {
			t.Errorf("error = %q, want the exit status", got)
		}
	}
	if err := launcherExitError(nil); err != nil {
		t.Errorf("launcherExitError(nil) = %v, want nil", err)
	}
}

// toolWait is the budget used by the tests above: long enough for a shell to
// start and exit on a loaded CI box, short enough to keep the suite fast.
const toolWait = 5 * time.Second
