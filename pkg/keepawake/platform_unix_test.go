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
	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Fatalf("release took %v, want < 2s (normal case is ~ms)", elapsed)
	}

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

func TestLimitedWriterCapsStderr(t *testing.T) {
	w := &limitedWriter{}

	if n, err := w.Write([]byte("first line\n")); n != 11 || err != nil {
		t.Fatalf("Write = (%d, %v), want (11, nil)", n, err)
	}
	big := bytes.Repeat([]byte("a"), stderrCap*2)
	if n, err := w.Write(big); n != len(big) || err != nil {
		t.Fatalf("Write(big) = (%d, %v), want (%d, nil)", n, err, len(big))
	}
	if got := len(w.String()); got != stderrCap {
		t.Fatalf("captured %d bytes, want exactly the %d cap", got, stderrCap)
	}
	if first := firstLineOrNoOutput(w.String()); first != "first line" {
		t.Fatalf("first line = %q, want %q", first, "first line")
	}
	if n, err := w.Write([]byte("more")); n != 4 || err != nil {
		t.Fatalf("Write after cap = (%d, %v), want (4, nil)", n, err)
	}
	if got := len(w.String()); got != stderrCap {
		t.Fatalf("captured %d bytes after post-cap write, want the %d cap", got, stderrCap)
	}
}

func TestStartCommandExitsImmediately(t *testing.T) {
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
	if elapsed >= 250*time.Millisecond {
		t.Errorf("failed after %v, want < the 250 ms readiness window (child dies early)", elapsed)
	}
}

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

	long := strings.Repeat("B", 4*maxReasonLen)
	got := firstLineOrNoOutput(long)
	if n := len([]rune(got)); n > maxReasonLen+1 {
		t.Fatalf("oversized line not capped: %d runes, want <= %d", n, maxReasonLen+1)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("capped line must end with an ellipsis, got %q", got[len(got)-8:])
	}
	multi := firstLineOrNoOutput(strings.Repeat("ñ", 4*maxReasonLen))
	if !utf8.ValidString(multi) {
		t.Errorf("capped line is not valid UTF-8: %q", multi)
	}
}

func TestStartCommandReleaseIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("release of a TERM-ignoring child takes ~3 s; skipped in -short mode")
	}
	const script = `trap 'printf x >> "$1"' TERM
while :; do sleep 0.05; done`
	termFile := filepath.Join(t.TempDir(), "terms")
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
	close(startBarrier)
	wg.Wait()

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
