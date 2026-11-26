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
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file must survive Release (unlinking it races a concurrent Acquire): %v", err)
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

func deadPID(t *testing.T) int {
	t.Helper()
	if runtime.GOOS == "windows" {
		return 4194303
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

	validPath := filepath.Join(dir, "valid.lock")
	writePIDFile(t, validPath, "12345\n")
	pid, err := ReadPID(validPath)
	if err != nil {
		t.Fatalf("ReadPID(valid): %v", err)
	}
	if pid != 12345 {
		t.Errorf("ReadPID = %d, want 12345", pid)
	}

	corruptPath := filepath.Join(dir, "corrupt.lock")
	writePIDFile(t, corruptPath, "not-a-pid\n")
	if _, err := ReadPID(corruptPath); err == nil {
		t.Error("ReadPID(corrupt) = nil error, want error")
	}

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

func TestAcquire_LivePIDWithoutHolder_Takeover(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses the conservative PID-file guard, not flock")
	}
	path := filepath.Join(t.TempDir(), "gateway.lock")

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

func TestLockfile_ReleaseNoUnlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Release removed the lock file: %v", err)
	}

	again, err := Acquire(path)
	if err != nil {
		t.Fatalf("the lock was not released, or the surviving file cannot be re-locked: %v", err)
	}
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
	if err := again.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the lock file did not survive the second Release: %v", err)
	}
}

func TestLockfile_ConcurrentAcquireAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")

	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	defer second.Release()

	if _, err := Acquire(path); err == nil {
		t.Fatal("a second lock on a live holder succeeded")
	} else if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("error = %v, want ErrAlreadyRunning", err)
	}
}
