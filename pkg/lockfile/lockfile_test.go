package lockfile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// writePIDFile writes contents to the lock file path, returning the open file
// handle so the test can close it afterwards.
func writePIDFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
}

func TestAcquireRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if l.Path() != path {
		t.Errorf("Path() = %q, want %q", l.Path(), path)
	}

	// The file must exist and contain our own PID.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got, err := strconv.Atoi(string(trimSpace(data)))
	if err != nil {
		t.Fatalf("lock file does not contain an integer PID: %q => %v", string(data), err)
	}
	if got != os.Getpid() {
		t.Errorf("lock file PID = %d, want %d", got, os.Getpid())
	}

	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after Release: %v", err)
	}
}

func TestAcquire_AlreadyRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")

	l1, err := Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer l1.Release()

	_, err = Acquire(path)
	if err == nil {
		t.Fatal("second Acquire succeeded, want ErrAlreadyRunning")
	}

	var already *AlreadyRunningError
	if !errors.As(err, &already) {
		t.Fatalf("error type = %T, want *AlreadyRunningError", err)
	}
	if already.PID != os.Getpid() {
		t.Errorf("AlreadyRunningError.PID = %d, want %d", already.PID, os.Getpid())
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("errors.Is(err, ErrAlreadyRunning) = false, want true")
	}
}

// deadPID returns the PID of a process that is guaranteed (as far as possible)
// to be dead. On Windows it falls back to a hardcoded large PID.
func deadPID(t *testing.T) int {
	t.Helper()
	if runtime.GOOS == "windows" {
		return 4194303 // 2^22-1, almost certainly unassigned
	}

	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing sleep (pid %d): %v", pid, err)
	}
	if err := cmd.Wait(); err != nil {
		// Expected: the process was killed. Ignore the error.
		_ = err
	}
	return pid
}

func TestAcquire_StalePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")
	writePIDFile(t, path, strconv.Itoa(deadPID(t))+"\n")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over stale PID: %v", err)
	}
	defer l.Release()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got, err := strconv.Atoi(string(trimSpace(data)))
	if err != nil {
		t.Fatalf("lock file does not contain an integer PID after takeover: %q => %v", string(data), err)
	}
	if got != os.Getpid() {
		t.Errorf("PID after takeover = %d, want %d", got, os.Getpid())
	}
}

func TestAcquire_CorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")
	writePIDFile(t, path, "not-a-pid\n")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over corrupt file: %v", err)
	}
	defer l.Release()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got, err := strconv.Atoi(string(trimSpace(data)))
	if err != nil {
		t.Fatalf("lock file does not contain an integer PID after takeover of corrupt file: %q => %v", string(data), err)
	}
	if got != os.Getpid() {
		t.Errorf("PID after takeover = %d, want %d", got, os.Getpid())
	}
}

func TestReadPID(t *testing.T) {
	dir := t.TempDir()

	// Valid PID.
	validPath := filepath.Join(dir, "valid.lock")
	writePIDFile(t, validPath, "12345\n")
	pid, err := ReadPID(validPath)
	if err != nil {
		t.Fatalf("ReadPID(valid): %v", err)
	}
	if pid != 12345 {
		t.Errorf("ReadPID = %d, want 12345", pid)
	}

	// Corrupt content.
	corruptPath := filepath.Join(dir, "corrupt.lock")
	writePIDFile(t, corruptPath, "not-a-pid\n")
	if _, err := ReadPID(corruptPath); err == nil {
		t.Error("ReadPID(corrupt) = nil error, want error")
	}

	// Missing file.
	missingPath := filepath.Join(dir, "missing.lock")
	if _, err := ReadPID(missingPath); err == nil {
		t.Error("ReadPID(missing) = nil error, want error")
	}
}

func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == '\n' || b[start] == '\r' || b[start] == ' ') {
		start++
	}
	for end > start && (b[end-1] == '\n' || b[end-1] == '\r' || b[end-1] == ' ') {
		end--
	}
	return b[start:end]
}

// TestAcquire_LivePIDWithoutHolder_Takeover verifies the new flock-first
// semantics: a PID file naming a live process is no longer sufficient to
// refuse acquisition, because the kernel lock — not the file — decides
// ownership. A holder that crashed while leaving its PID behind (flock is
// auto-released on process exit) must not wedge the lock forever.
//
// Windows keeps the conservative PID-only guard, so this test is Unix-only.
func TestAcquire_LivePIDWithoutHolder_Takeover(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses the conservative PID-file guard, not flock")
	}
	path := filepath.Join(t.TempDir(), "gateway.lock")

	// Simulate a crashed holder: a live PID (ours) in the file, but no
	// process actually holding the flock.
	writePIDFile(t, path, strconv.Itoa(os.Getpid())+"\n")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over live PID with no flock holder: %v", err)
	}
	defer l.Release()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got, err := strconv.Atoi(string(trimSpace(data))); err != nil || got != os.Getpid() {
		t.Errorf("PID after takeover = %d (err %v), want %d", got, err, os.Getpid())
	}
}

// TestAcquire_AfterRelease verifies that Release fully relinquishes the lock
// (explicit unlock plus close), so a subsequent Acquire on the same path
// succeeds. This guards against regressions where the kernel lock or the
// open handle would be left behind.
func TestAcquire_AfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")

	l1, err := Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}

	l2, err := Acquire(path)
	if err != nil {
		t.Fatalf("second Acquire after Release: %v", err)
	}
	defer l2.Release()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got, err := strconv.Atoi(string(trimSpace(data))); err != nil || got != os.Getpid() {
		t.Errorf("PID after reacquire = %d (err %v), want %d", got, err, os.Getpid())
	}
}
