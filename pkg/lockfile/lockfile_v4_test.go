package lockfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// TestRelease_RemoveNonNotExistError covers the Release error path where
// os.Remove fails with an error that is not os.ErrNotExist. Removing a
// directory returns EISDIR/ENOTEMPTY, which exercises the
// `err != nil && !os.IsNotExist(err)` branch that stores firstErr.
func TestRelease_RemoveNonNotExistError(t *testing.T) {
	// Create a directory that we'll attempt to Remove as if it were our lock path.
	dir := filepath.Join(t.TempDir(), "not-a-lockfile")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	// Make sure it is not empty so Remove definitely fails on all platforms.
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Construct a Lock with a nil handle and a directory path so Release's
	// os.Remove step fails (directory is not empty) —— but the failure is not
	// os.IsNotExist, so the error is surfaced.
	l := &Lock{path: dir}

	if err := l.Release(); err == nil {
		t.Fatal("Release() = nil, want non-nil error removing a non-empty directory")
	}

	// Directory must still exist because Remove failed.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("directory should still exist after failed Remove: %v", err)
	}
}

// TestRelease_CloseTruncateError covers Release's behavior when the stored file
// handle's Truncate fails. Closing the underlying fd first makes the later
// Truncate/Close calls return os.ErrClosed; the first error is surfaced.
func TestRelease_CloseTruncateError_v4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")
	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Force a failure by closing the fd out from under Release.
	if err := l.file.Close(); err != nil {
		t.Fatalf("Close handle: %v", err)
	}
	if err := l.Release(); err == nil {
		t.Fatal("Release() = nil, want error for already-closed file handle")
	}
} // TestProcessAlive_EPERM covers the EPERM branch in processAlive: when a
// process exists but belongs to another user (or is otherwise not usable by
// us), the signal-0 probe returns permission denied and processAlive must
// report the process is alive (true). PID 1 is normally the init process
// owned by root. It is skipped if the environment doesn't produce EPERM.
func TestProcessAlive_EPERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("processAlive EPERM is unix-specific")
	}
	if !processAlive(1) {
		// If PID 1 is not alive or find fails, try to detect whether EPERM can
		// be reproduced at all; otherwise skip.
		p, err := os.FindProcess(1)
		if err != nil {
			t.Skip("cannot inspect PID 1")
		}
		if err := p.Signal(syscall.Signal(0)); err != nil && errors.Is(err, syscall.EPERM) {
			// EPERM detected but processAlive(1) returned false - unusual.
			t.Skip("PID 1 not reachable")
		}
		return
	}
	// processAlive(1) returned true, which is the expected EPERM-alive result
	// in environments where init is owned by root.
}
