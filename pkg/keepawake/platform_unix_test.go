//go:build linux || darwin

package keepawake

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

func TestStartCommandStopsChild(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30")
	release, err := startCommand(cmd)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	if cmd.Process == nil || cmd.Process.Pid <= 0 {
		t.Fatalf("child not started: Process=%v", cmd.Process)
	}
	pid := cmd.Process.Pid

	start := time.Now()
	release()

	// Poll until the process is gone (max 3 s).
	deadline := time.Now().Add(3 * time.Second)
	gone := false
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			gone = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !gone {
		t.Fatalf("child pid %d still alive after release", pid)
	}
	// The child is a plain `sleep` in its own process group, so release is
	// the normal (fast) case: SIGTERM lands, Wait returns, no timeout bound
	// is paid. Generous margin so slow CI cannot flake.
	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Fatalf("release took %v, want < 2s (normal case is ~ms)", elapsed)
	}

	// Idempotency: a second release must not panic or block.
	release()
}

func TestStartCommandStartError(t *testing.T) {
	cmd := exec.Command("/nonexistent/lele-keepawake-test")
	release, err := startCommand(cmd)
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
	if release != nil {
		t.Fatal("release must be nil on start error")
	}
}

// TestStartCommandReleaseKillsWholeGroup is the regression test for the
// 3-second release: the child mimics linuxInhibitCommand's shape (a shell
// that spawns a grandchild — `sh -c 'exec sleep 20'` — and then execs a
// process of its own, `sleep 20`). os/exec pipes cmd.Stderr (it is not an
// *os.File), so cmd.Wait — and therefore release — only returns once every
// write end of that pipe is closed. Before the fix, release signalled only
// the direct child: the shell died, the grandchild inherited the pipe, and
// release blocked for its full 2 s + 1 s bound (~3.0 s) in the NORMAL case.
// With Setpgid + kill(-pid) the whole group dies together, the pipe closes
// and release returns in ~1 ms. No systemd required, so it runs in CI.
func TestStartCommandReleaseKillsWholeGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", `sh -c 'exec sleep 20' & exec sleep 20`)
	release, err := startCommand(cmd)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	pid := cmd.Process.Pid

	start := time.Now()
	release()
	elapsed := time.Since(start)
	t.Logf("release() took %v", elapsed)

	if elapsed > 500*time.Millisecond {
		t.Errorf("release() took %v, want <= 500 ms (whole process group must die together)", elapsed)
	}

	// No grandchild may survive: kill(-pid, 0) reports ESRCH only once the
	// whole process group is gone. Poll briefly: SIGKILL is asynchronous.
	deadline := time.Now().Add(2 * time.Second)
	for err := syscall.Kill(-pid, 0); err == nil && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		err = syscall.Kill(-pid, 0)
		if err != nil {
			break
		}
	}
	if err := syscall.Kill(-pid, 0); err == nil {
		t.Errorf("process group %d (shell + grandchildren) still alive after release", pid)
	}
}

// TestLimitedWriterCapsStderr pins the bounded stderr capture: at most
// stderrCap bytes are kept (the rest is discarded, and Write always reports
// all bytes consumed so the os/exec copier never stalls or retries).
func TestLimitedWriterCapsStderr(t *testing.T) {
	w := &limitedWriter{}

	// Under the cap: everything is kept.
	if n, err := w.Write([]byte("first line\n")); n != 11 || err != nil {
		t.Fatalf("Write = (%d, %v), want (11, nil)", n, err)
	}
	// Over the cap: the buffer stops growing but Write still reports success.
	big := bytes.Repeat([]byte("a"), stderrCap*2)
	if n, err := w.Write(big); n != len(big) || err != nil {
		t.Fatalf("Write(big) = (%d, %v), want (%d, nil)", n, err, len(big))
	}
	if got := len(w.String()); got != stderrCap {
		t.Fatalf("captured %d bytes, want exactly the %d cap", got, stderrCap)
	}
	// The first line survives truncation (that is all we ever surface).
	if first := firstLineOrNoOutput(w.String()); first != "first line" {
		t.Fatalf("first line = %q, want %q", first, "first line")
	}
	// Writes after the cap are a cheap no-op that still report success.
	if n, err := w.Write([]byte("more")); n != 4 || err != nil {
		t.Fatalf("Write after cap = (%d, %v), want (4, nil)", n, err)
	}
	if got := len(w.String()); got != stderrCap {
		t.Fatalf("captured %d bytes after post-cap write, want the %d cap", got, stderrCap)
	}
}

// TestStartCommandExitsImmediately pins the readiness window: a child that
// dies right after Start() (the `systemd-inhibit` "Failed to inhibit" case:
// Start() succeeds, the process exits 1) must NOT be reported as success.
func TestStartCommandExitsImmediately(t *testing.T) {
	// Writes to stderr and exits 1, mirroring systemd-inhibit without a lock.
	cmd := exec.Command("sh", "-c", `echo "Failed to inhibit: Access denied" >&2; exit 1`)
	start := time.Now()
	release, err := startCommand(cmd)
	elapsed := time.Since(start)

	if err == nil {
		if release != nil {
			release()
		}
		t.Fatal("expected error for a child that exits immediately")
	}
	if release != nil {
		t.Fatal("release must be nil when startup verification fails")
	}
	if !strings.Contains(err.Error(), "keep-awake:") {
		t.Errorf("error %q must carry the keep-awake prefix", err)
	}
	if !strings.Contains(err.Error(), "exited immediately") {
		t.Errorf("error %q must say the child exited immediately", err)
	}
	if !strings.Contains(err.Error(), "Failed to inhibit: Access denied") {
		t.Errorf("error %q must surface the child's first stderr line", err)
	}
	// The child dies well before the 250 ms readiness window, so we return
	// as soon as we detect it (not after the full window).
	if elapsed >= 250*time.Millisecond {
		t.Errorf("failed after %v, want < the 250 ms readiness window (child dies early)", elapsed)
	}
}

// TestFirstLineOrNoOutput covers the stderr summariser used in the
// "exited immediately" error.
func TestFirstLineOrNoOutput(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "no output"},
		{"\n \n", "no output"},
		{"  Failed to inhibit: Access denied \nsecond line\n", "Failed to inhibit: Access denied"},
		{"\n\nboom\n", "boom"},
		{"  \n  trimmed first  \nrest", "trimmed first"},
	}
	for _, c := range cases {
		if got := firstLineOrNoOutput(c.in); got != c.want {
			t.Errorf("firstLineOrNoOutput(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// A single pathological line longer than the cap is ellipsised: the error
	// carries a readable reason, not the whole 4 KiB stderr capture.
	long := strings.Repeat("B", 4*maxReasonLen)
	got := firstLineOrNoOutput(long)
	if n := len([]rune(got)); n > maxReasonLen+1 {
		t.Fatalf("oversized line not capped: %d runes, want <= %d", n, maxReasonLen+1)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("capped line must end with an ellipsis, got %q", got[len(got)-8:])
	}
	// Multibyte input must not be split in the middle of a rune.
	multi := firstLineOrNoOutput(strings.Repeat("ñ", 4*maxReasonLen))
	if !utf8.ValidString(multi) {
		t.Errorf("capped line is not valid UTF-8: %q", multi)
	}
}

// TestStartCommandReleaseIsIdempotent pins the observable behaviour of the
// release closure under concurrency: two goroutines call release() at the
// same time behind a barrier and the child — which IGNORES SIGTERM — must see
// exactly ONE SIGTERM (one appended byte). Calling release twice is otherwise
// unobservable (the second signal over an already-dead child is a no-op), so
// this is the honest claim: 2 concurrent releases => 1 SIGTERM counted by the
// child. The sync.Once in startCommand is defence in depth for this; the
// production invariant it protects is at the Inhibitor level, where the mutex
// plus `i.release = nil` guarantee each release closure is handed out (and
// thus executed) at most once — see TestStartStopDeliversEachReleaseOnce.
//
// Skipped with -short because the child ignores TERM, so release takes
// SIGTERM + 2 s timeout + SIGKILL + up to 1 s kill timeout.
func TestStartCommandReleaseIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("release of a TERM-ignoring child takes ~3 s; skipped in -short mode")
	}
	const script = `trap 'printf x >> "$1"' TERM
while :; do sleep 0.05; done`
	termFile := filepath.Join(t.TempDir(), "terms")
	// NOTE: the trap writes to "$1" (a real argument), never to $0, so the
	// script does not depend on how exec passes argv[0].
	cmd := exec.Command("sh", "-c", script, "sh", termFile)
	release, err := startCommand(cmd)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	pid := cmd.Process.Pid

	const callers = 2
	var (
		startBarrier = make(chan struct{})
		wg           sync.WaitGroup
	)
	for g := 0; g < callers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startBarrier
			release()
		}()
	}
	close(startBarrier) // release both goroutines at the same time
	wg.Wait()

	// The child must be gone (TERM ignored => KILLed by release).
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("child pid %d still alive after release", pid)
	}

	data, err := os.ReadFile(termFile)
	if err != nil {
		t.Fatalf("reading term log: %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("release ran %d times, want exactly 1 (byte log %q)", len(data), data)
	}
}
