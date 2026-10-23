//go:build linux || darwin

package keepawake

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// stderrCap bounds how much child stderr is captured for the "exited
// immediately" error message (only the first line is ever surfaced). Keeping
// it small means a chatty child cannot make the copy goroutine buffer
// unbounded memory.
const stderrCap = 4096

// startCommand starts cmd and returns a release func that terminates it.
//
// On success exactly one goroutine reaps the child with cmd.Wait() and
// signals the done channel, so no zombie process is left behind.
//
// Startup verification: cmd.Stderr is captured in a bounded buffer (instead
// of being sent to /dev/null) and, after cmd.Start() returns, startCommand
// waits a readiness window of 250 ms before reporting success. If the child
// dies within that window the mechanism never really existed — the classic
// case being `systemd-inhibit` present on PATH but unable to acquire the
// lock (Docker/WSL2 without systemd, chroot), which prints "Failed to
// inhibit: …" on stderr and exits 1 while cmd.Start() itself succeeds. Cost:
// every Start blocks for 250 ms, which is acceptable because it only happens
// on a toggle.
//
// The child runs in its own process group (Setpgid) so release can signal the
// WHOLE group. This is required, not an optimisation: os/exec copies a
// non-*os.File stderr through a pipe, so cmd.Wait() does not return until
// every write end of that pipe is closed. The real child is
// `systemd-inhibit … sh -c 'while kill -0 <pid>; do sleep 5; done'`, so
// signalling only the direct child leaves the shell and its `sleep 5`
// grandchild alive with the pipe still open, and cmd.Wait() (and therefore
// release) would block for its full 2 s + 1 s bound in the NORMAL case.
// Signalling the group kills the shell and the grandchild together, closing
// the pipe; the normal release then costs ~1 ms (SIGTERM + Wait).
//
// The release closure is idempotent (sync.Once): it sends SIGTERM to the
// process group, waits up to 2 s for cmd.Wait to return, then SIGKILLs the
// group and waits again — for at most 1 s more, so a process stuck in
// uninterruptible sleep (state D) can never hang Close()/Stop() forever. The
// 2 s + 1 s bound is only paid when a child IGNORES SIGTERM (the pathological
// case); the normal case (systemd-inhibit/caffeinate + shell loop) is a
// couple of milliseconds. Signal errors are ignored (the group may have exited
// on its own).
func startCommand(cmd *exec.Cmd) (func(), error) {
	buf := &limitedWriter{}
	cmd.Stderr = buf
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Own process group: release signals -pid so the shell loop and its
	// `sleep` grandchild die together with systemd-inhibit (see doc above).
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("keep-awake: starting %s: %w", cmd.Path, err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done: // died immediately => the inhibition never existed
		return nil, fmt.Errorf("keep-awake: %s exited immediately: %s",
			filepath.Base(cmd.Path), firstLineOrNoOutput(buf.String()))
	case <-time.After(250 * time.Millisecond):
	}
	pid := cmd.Process.Pid
	var once sync.Once
	return func() {
		once.Do(func() {
			// The child (group) may already be gone — a mechanism that died on
			// its own. Signalling a dead group would be harmless *unless* the
			// pid was recycled while we were not looking, so bail out: once the
			// kernel has released the pgid, -pid may name somebody else's group.
			select {
			case <-done:
				return
			default:
			}
			// Signal the whole process group (negative pid): the direct child
			// may be systemd-inhibit, whose `sh`/`sleep` descendants also hold
			// the stderr pipe open — cmd.Wait() would otherwise never return.
			// The child (group) may have exited already; ignore the error.
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
			// SIGKILL the group as well: same reasoning as above, and a
			// grandchild left behind would also keep the pipe open.
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			// Bounded wait: a process stuck in state D would otherwise hang
			// the caller forever. If this times out the group is orphaned.
			select {
			case <-done:
			case <-time.After(1 * time.Second):
			}
		})
	}, nil
}

// limitedWriter keeps at most stderrCap bytes and silently discards the rest.
// It exists so a chatty child (e.g. a systemd-inhibit variant that spews
// warnings) cannot make the os/exec stderr copy goroutine buffer without
// bound: cmd.Stderr is not an *os.File, so os/exec pipes the child's stderr
// and a background goroutine copies every byte into this writer until the
// write end closes (which is also why cmd.Wait, and therefore release, only
// returns once the whole child process group is dead). Only the first line is
// ever read (firstLineOrNoOutput), and that line is itself capped by
// maxReasonLen, so truncating here is loss-free for our use (a pathological
// single line longer than the cap is ellipsised further down).
// Write never blocks and always reports success so the child is never stalled
// or killed by a full pipe.
type limitedWriter struct {
	mu  sync.Mutex
	buf []byte
}

// Write appends p, keeping at most stderrCap bytes in total; any overflow is
// discarded. It is safe for concurrent use (defence in depth; os/exec uses a
// single copier goroutine).
func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if room := stderrCap - len(w.buf); room > 0 {
		if len(p) > room {
			w.buf = append(w.buf, p[:room]...)
		} else {
			w.buf = append(w.buf, p...)
		}
	}
	return len(p), nil // report all consumed: never make the copier retry
}

// String returns the captured (possibly truncated) stderr.
func (w *limitedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}

// firstLineOrNoOutput returns the first non-empty line of s with surrounding
// whitespace trimmed, or "no output" when there is none (so an error message
// built from it is never empty).
func firstLineOrNoOutput(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return truncateReason(trimmed)
		}
	}
	return "no output"
}

// maxReasonLen caps the single line carried into the start error (and from
// there into the TUI/log). It only matters for a child that writes one huge
// line before dying: without the cap the error would carry the full 4 KiB
// stderr capture, which the modal then has to truncate anyway and which reads
// terribly in a log.
const maxReasonLen = 512

// truncateReason shortens s to maxReasonLen runes, marking the cut with an
// ellipsis. It is rune-aware so a multibyte character is never split.
func truncateReason(s string) string {
	if len(s) <= maxReasonLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxReasonLen {
		return s
	}
	return string(runes[:maxReasonLen]) + "…"
}
