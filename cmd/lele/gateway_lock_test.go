package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/lockfile"
	"github.com/xilistudios/lele/pkg/update"
)

// shrinkHandoff makes the restart-child wait loop testable: production uses a
// 20s timeout with 100ms polls, which no test can wait through.
func shrinkHandoff(t *testing.T, timeout, poll time.Duration) {
	t.Helper()
	origTimeout, origPoll := instanceLockHandoffTimeout, instanceLockHandoffPoll
	instanceLockHandoffTimeout, instanceLockHandoffPoll = timeout, poll
	t.Cleanup(func() {
		instanceLockHandoffTimeout, instanceLockHandoffPoll = origTimeout, origPoll
	})
}

// liveFlockHolder mimics a running gateway instance: it takes the real
// kernel flock on the lock file (the authoritative guard since CHT-03 — a
// bare PID file no longer blocks acquisition) and writes its PID. The
// returned function releases the flock and closes the file, like a previous
// instance finishing its shutdown.
func liveFlockHolder(t *testing.T, path string) func() {
	t.Helper()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open holder lock file: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		t.Skipf("cannot take flock: %v", err)
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		t.Fatalf("truncate: %v", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		f.Close()
		t.Fatalf("write pid: %v", err)
	}

	released := false
	return func() {
		if released {
			return
		}
		released = true
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}

// TestAcquireInstanceLockPlainStartSucceeds covers the normal case: no lock file
// yet, so the lock is taken immediately.
func TestAcquireInstanceLockPlainStartSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")

	lock, err := acquireInstanceLock(path)
	if err != nil {
		t.Fatalf("acquireInstanceLock() = %v, want success", err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("Release() = %v", err)
	}
}

// TestAcquireInstanceLockSecondInstanceFailsFast is the whole point of the lock:
// without the restart marker, a second gateway must report already_running
// immediately instead of waiting.
func TestAcquireInstanceLockSecondInstanceFailsFast(t *testing.T) {
	t.Setenv(update.RestartChildEnvKey, "")
	path := filepath.Join(t.TempDir(), "gateway.lock")

	holder := liveFlockHolder(t, path)
	t.Cleanup(holder)

	started := time.Now()
	_, err := acquireInstanceLock(path)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Errorf("plain start waited %v, want to fail immediately", elapsed)
	}
	var arErr *lockfile.AlreadyRunningError
	if !errors.As(err, &arErr) {
		t.Fatalf("acquireInstanceLock() = %v, want *lockfile.AlreadyRunningError", err)
	}
}

// TestAcquireInstanceLockRestartChildWaitsForHandoff is the self-exec fix: the
// replacement process must tolerate the parent still holding the lock while it
// drains, and take over as soon as it is released.
func TestAcquireInstanceLockRestartChildWaitsForHandoff(t *testing.T) {
	t.Setenv(update.RestartChildEnvKey, "1")
	shrinkHandoff(t, 5*time.Second, 10*time.Millisecond)

	path := filepath.Join(t.TempDir(), "gateway.lock")
	holder := liveFlockHolder(t, path)

	// Release the holder shortly after the child starts waiting.
	go func() {
		time.Sleep(150 * time.Millisecond)
		holder()
	}()

	lock, err := acquireInstanceLock(path)
	if err != nil {
		t.Fatalf("acquireInstanceLock() = %v, want the child to take over after the handoff", err)
	}
	defer lock.Release()

	pid, err := lockfile.ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID() = %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("lock holder pid = %d, want %d (the restart child)", pid, os.Getpid())
	}
}

// TestAcquireInstanceLockRestartChildGivesUp bounds the wait: if the previous
// instance never releases, the child must fail with a clear error rather than
// hang forever holding a half-started gateway.
func TestAcquireInstanceLockRestartChildGivesUp(t *testing.T) {
	t.Setenv(update.RestartChildEnvKey, "1")
	shrinkHandoff(t, 200*time.Millisecond, 10*time.Millisecond)

	path := filepath.Join(t.TempDir(), "gateway.lock")
	holder := liveFlockHolder(t, path)
	t.Cleanup(holder)

	_, err := acquireInstanceLock(path)
	if err == nil {
		t.Fatal("acquireInstanceLock() = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "did not release") {
		t.Errorf("error = %v, want it to name the lock that was never released", err)
	}
}
