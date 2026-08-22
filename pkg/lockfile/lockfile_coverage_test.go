package lockfile

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// TestAcquire_TruncateError covers the error path in Acquire where Truncate(0)
// fails after a successful open.
//
// Opening /dev/zero with O_CREATE|O_RDWR succeeds, Read returns a buffer of
// NUL bytes (corrupt PID, so the liveness check is skipped non-blockingly),
// but Truncate on a character device returns EINVAL, so the Truncate error
// branch is exercised. This device is available on Linux and many Unices.
func TestAcquire_TruncateError(t *testing.T) {
	if _, err := os.Stat("/dev/zero"); err != nil {
		t.Skip("no /dev/zero on this platform")
	}
	_, err := Acquire("/dev/zero")
	if err == nil {
		t.Fatal("Acquire on /dev/zero succeeded, want Truncate error")
	}
}

// TestAcquire_TruncateFullDevice exercises the write/truncate failure path via
// /dev/full which behaves like an always-full disk: the open succeeds but
// writes and syncs fail, exercising Acquire's error branches on Linux.
func TestAcquire_TruncateFullDevice(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full on this platform")
	}
	_, err := Acquire("/dev/full")
	if err == nil {
		t.Fatal("Acquire on /dev/full succeeded, want error")
	}
}

// TestRelease_CloseError covers the error path in Release where the stored file
// handle is already closed, forcing l.file.Close to return os.ErrClosed.
// This complements extra_test.go's TestRelease_ErrorOnClosedFile by also
// asserting that the returned error is non-nil from a fresh lock.
func TestRelease_CloseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.lock")
	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := l.file.Close(); err != nil {
		t.Fatalf("Close handle: %v", err)
	}
	if err := l.Release(); err == nil {
		t.Fatal("Release() = nil, want error from already-closed file handle")
	}
}

// TestAcquire_FprintfWriteFailure covers the error branch where the Fprintf of
// our PID fails after a successful Truncate and Seek. It temporarily lowers the
// RLIMIT_FSIZE file-size resource limit to zero: Open, Truncate and Seek all
// succeed, but the subsequent write of the PID exceeds the zero-byte limit and
// fails with EFBIG. This is the only reliable way to reach that branch without
// a fault-injecting filesystem.
func TestAcquire_FprintfWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("RLIMIT_FSIZE not available on windows")
	}

	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Skipf("RLIMIT_FSIZE unavailable: %v", err)
	}
	if old.Max == 0 {
		t.Skip("max RLIMIT_FSIZE is 0; cannot restore")
	}
	zero := syscall.Rlimit{Cur: 0, Max: old.Max}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &zero); err != nil {
		t.Skipf("cannot lower FSIZE: %v", err)
	}
	defer func() { _ = syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old) }()

	path := filepath.Join(t.TempDir(), "gateway.lock")
	_, err := Acquire(path)
	if err == nil {
		t.Fatal("Acquire with zero file-size limit succeeded, want fprintf error")
	}
}
