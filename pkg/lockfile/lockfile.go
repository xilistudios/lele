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
// If the file already contains a valid PID of a process that is still alive,
// it returns an *AlreadyRunningError wrapping ErrAlreadyRunning. Otherwise the
// lock is considered stale (empty file, corrupt content, or a dead PID) and is
// taken over: the file is truncated, our own PID is written, flushed to disk
// and the handle is kept open in the returned Lock.
//
// The returned Lock must be released with Release when done.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	// Best effort liveness check of any existing PID.
	if pid, parseErr := readPIDFile(f); parseErr == nil && processAlive(pid) {
		f.Close()
		return nil, &AlreadyRunningError{PID: pid}
	}

	// Take over: truncate, write our own PID, flush to disk.
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
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
		// Truncate first so that even if the subsequent Remove fails the file
		// is left in a clearly-stale (empty) state.
		if err := l.file.Truncate(0); err != nil && firstErr == nil {
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
