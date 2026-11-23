package filelock

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquire_BlocksCompetitorUntilRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	release, err := Acquire(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	if _, err := Acquire(path); err == nil {
		t.Fatal("second acquire succeeded while the lock was held, want a timeout error")
	}

	release()
	release2, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatal("sidecar must be removed after release")
	}
}

func TestAcquire_ReclaimsStaleSidecar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * StaleAfter)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	release, err := Acquire(path)
	if err != nil {
		t.Fatalf("stale sidecar was not reclaimed: %v", err)
	}
	release()
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatal("reclaimed sidecar must be removed after release")
	}
}

func TestAcquire_GivesUpWhenTheStaleSidecarCannotBeReclaimed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * StaleAfter)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := Acquire(path); err == nil {
		t.Fatal("Acquire succeeded on a directory it cannot write, want a timeout error")
	}
}
