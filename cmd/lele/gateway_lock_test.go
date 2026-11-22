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

func shrinkHandoff(t *testing.T, timeout, poll time.Duration) {
	t.Helper()
	origTimeout, origPoll := instanceLockHandoffTimeout, instanceLockHandoffPoll
	instanceLockHandoffTimeout, instanceLockHandoffPoll = timeout, poll
	t.Cleanup(func() {
		instanceLockHandoffTimeout, instanceLockHandoffPoll = origTimeout, origPoll
	})
}

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

func TestAcquireInstanceLockRestartChildWaitsForHandoff(t *testing.T) {
	t.Setenv(update.RestartChildEnvKey, "1")
	shrinkHandoff(t, 5*time.Second, 10*time.Millisecond)

	path := filepath.Join(t.TempDir(), "gateway.lock")
	holder := liveFlockHolder(t, path)

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
