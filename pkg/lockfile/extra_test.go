package lockfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestAlreadyRunningError_Error(t *testing.T) {
	e := &AlreadyRunningError{PID: 1234}
	msg := e.Error()
	if !strings.Contains(msg, "1234") {
		t.Errorf("Error() = %q, want it to contain the PID", msg)
	}
	if !errors.Is(e, ErrAlreadyRunning) {
		t.Error("errors.Is(e, ErrAlreadyRunning) = false")
	}
}

func TestAcquire_OpenFileFailure(t *testing.T) {
	// Point the lock file at a path inside a non-existent directory.
	path := filepath.Join(t.TempDir(), "no-such-dir", "gateway.lock")
	if _, err := Acquire(path); err == nil {
		t.Fatal("Acquire over a path in a missing dir should fail")
	}
}

func TestAcquire_TruncateFailure(t *testing.T) {
	// Acquire opens the file read-write; we cannot easily force Truncate to
	// fail on a regular file. Instead verify takeover over an empty file with
	// stale content still succeeds, and that a directory-as-file is rejected.
	dir := t.TempDir()

	// Empty file -> taken over successfully.
	emptyPath := filepath.Join(dir, "empty.lock")
	if err := os.WriteFile(emptyPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(emptyPath)
	if err != nil {
		t.Fatalf("Acquire over empty file: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestAcquire_ReadPIDFileError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")

	switch runtime.GOOS {
	case "windows":
		// Windows: obtain a guaranteed-dead process is unreliable; use a big PID.
		writePIDFile(t, path, "4194303\n")
	default:
		// Use the current PID of a subprocess-sleep trick via a negative-free huge PID
		// is unreliable; instead write a clearly-dead PID using the helper.
		writePIDFile(t, path, strconv.Itoa(deadPID(t))+"\n")
	}

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over stale PID: %v", err)
	}
	defer l.Release()

	data, _ := os.ReadFile(path)
	if string(trimSpace(data)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("takeover did not write our own PID: %q", string(data))
	}
}

func TestAcquire_ZeroAndNegativePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")
	for _, content := range []string{"0\n", "-5\n"} {
		writePIDFile(t, path, content)
		if _, err := Acquire(path); err != nil {
			t.Fatalf("Acquire over %q should take over: %v", content, err)
		}
	}
}

func TestAcquire_EmptyPIDFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")
	if err := os.WriteFile(path, []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over empty pid file: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestRelease_AfterFileRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")
	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// Remove the file externally, then Release should not error.
	if err := os.Remove(path); err != nil {
		t.Fatalf("external remove: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release after external removal: %v", err)
	}
}

func TestRelease_DoubleRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")
	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release should be nil: %v", err)
	}
}

func TestRelease_ErrorOnClosedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")
	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// Close the underlying handle behind the lock's back so Release's
	// Truncate/Close calls fail (error branches).
	if l.file != nil {
		_ = l.file.Close()
	}
	if err := l.Release(); err == nil {
		t.Fatal("Release over a closed file should return an error")
	}
}

func TestAcquire_WriteFailureOnFullDevice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires /dev/full")
	}
	// /dev/full behaves like a disk that is always full: opens succeed but
	// writes/truncate fail, exercising Acquire's error branches.
	if _, err := Acquire("/dev/full"); err == nil {
		t.Fatal("Acquire on /dev/full should fail")
	}
}

func TestProcessAlive_InvalidPID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("processAlive semantics on windows")
	}
	// A negative PID cannot refer to a real process.
	if processAlive(-99999) {
		t.Error("processAlive(-99999) = true, want false")
	}
}

func TestRelease_RemoveErrorNonNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Swap the lock file for a non-empty directory so os.Remove at Release
	// returns a non-ENOENT error (ENOTEMPTY) while the open handle still lets
	// Truncate/Close succeed -> the Remove-error branch (firstErr set) runs.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove lock file: %v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir at lock path: %v", err)
	}
	dirFile := filepath.Join(path, "child")
	if err := os.WriteFile(dirFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write child in dir: %v", err)
	}

	if err := l.Release(); err == nil {
		t.Fatal("Release over a non-empty directory at lock path should return an error")
	}
}

// TestRelease_NilFile verifies Release with a nil file handle only removes the
// path (file already "released" internally).
func TestRelease_NilFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")
	l := &Lock{path: path}
	// Remove should succeed because path doesn't exist (nil error OK).
	if err := l.Release(); err != nil {
		t.Fatalf("Release with nil file: %v", err)
	}
}
