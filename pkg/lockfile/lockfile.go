// Package lockfile provides a PID-file based single-instance lock.
package lockfile

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ErrAlreadyRunning is returned as the wrapped error from Acquire when another
// live process already holds the lock. Use errors.As to inspect the
// *AlreadyRunningError value and recover the holding PID.
var ErrAlreadyRunning = errors.New("already running")

// AlreadyRunningError reports that a live process already holds the lock.
type AlreadyRunningError struct {
	// PID is the process identifier that currently holds the lock.
	PID int
}

func (e *AlreadyRunningError) Error() string {
	return fmt.Sprintf("gateway already running (pid %d)", e.PID)
}

// Unwrap makes AlreadyRunningError match errors.Is(err, ErrAlreadyRunning).
func (e *AlreadyRunningError) Unwrap() error { return ErrAlreadyRunning }

// Lock is a single-instance lock backed by a PID file.
//
// The PID file is intentionally kept open for the lifetime of the Lock so that
// Release can truncate it; keeping the handle open also prevents some edge
// cases where the file could otherwise be unlinked out from under us.
type Lock struct {
	path string
	file *os.File
}

// Acquire acquires the lock described by the PID file at path.
//
// The authoritative guard is an advisory kernel lock (flock, LOCK_EX|LOCK_NB)
// taken on the open file descriptor. Because the kernel holds that lock for us
// and releases it automatically on process exit, it closes the check-then-write
// race in the old PID-only scheme, where two processes could both pass the
// liveness probe and both write their PID (TOCTOU). The PID file is kept —
// first as a fast-path hint, then for humans and diagnostics — but the flock
// result decides ownership.
//
// Semantics per outcome:
//   - flock acquired over a file naming a live PID: that holder crashed
//     without releasing (flock is auto-released on exit), so this is a
//     legitimate takeover of a stale lock.
//   - flock acquired over an empty/corrupt/dead-PID file: plain takeover.
//   - flock fails (EWOULDBLOCK): a live holder exists — either the PID in the
//     file (the common case) or an unknown one (PID reuse or a foreign
//     writer). We report AlreadyRunning using the file's PID when it parses,
//     falling back to our own PID as "unknown holder" sentinel.
//
// The returned Lock must be released with Release when done.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	// Authoritative single-instance check: take the non-blocking exclusive
	// kernel lock before trusting anything written in the file.
	if err := lockExclusive(f); err != nil {
		holder := os.Getpid()
		// Rewind before re-reading: lockExclusive may have left the file
		// position anywhere (the Windows fallback reads the PID file itself).
		if _, seekErr := f.Seek(0, 0); seekErr == nil {
			if pid, parseErr := readPIDFile(f); parseErr == nil && pid > 0 {
				holder = pid
			}
		}
		f.Close()
		return nil, &AlreadyRunningError{PID: holder}
	}

	// We own the flock. The PID file is now only an informational record
	// (fast-path staleness hint and human-readable holder ID): even if it
	// names a live PID, that holder exited without releasing the lock or the
	// PID was reused — the kernel lock, not the file, guarantees exclusivity.

	// Take over: truncate, write our own PID, flush to disk.
	if err := f.Truncate(0); err != nil {
		unlockFile(f)
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		unlockFile(f)
		f.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		unlockFile(f)
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		unlockFile(f)
		f.Close()
		return nil, err
	}

	return &Lock{path: path, file: f}, nil
}

// Release hands the lock back. The PID file is truncated (so stale detection
// sees an empty file), the handle is closed and the file is removed. A missing
// file is not an error.
func (l *Lock) Release() error {
	var firstErr error

	if l.file != nil {
		// Truncate first (while still holding the lock) so that even if the
		// subsequent Remove fails the file is left in a clearly-stale (empty)
		// state.
		if err := l.file.Truncate(0); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := unlockFile(l.file); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := l.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		l.file = nil
	}

	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) && firstErr == nil {
		firstErr = err
	}

	return firstErr
}

// Path returns the path of the PID file backing this lock.
func (l *Lock) Path() string {
	return l.path
}

// ReadPID reads and parses the PID from an existing lock file at path. It
// returns an error if the file is missing, empty or corrupt (non-integer
// content). It is useful for reporting which PID currently holds the lock.
func ReadPID(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return readPIDFile(f)
}

// readPIDFile reads and parses the PID from the open file f. It returns an
// error if the file is empty, corrupt (non-integer content) or cannot be read.
// The file position is left wherever it ends up; callers should Seek(0,0)
// before writing to it.
func readPIDFile(f *os.File) (int, error) {
	data := make([]byte, 64)
	n, err := f.Read(data)
	if err != nil && n == 0 {
		return 0, err
	}
	content := strings.TrimSpace(string(data[:n]))
	if content == "" {
		return 0, errors.New("empty pid file")
	}
	pid, err := strconv.Atoi(content)
	if err != nil || pid <= 0 {
		return 0, errors.New("corrupt pid file")
	}
	return pid, nil
}
