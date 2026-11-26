package lockfile

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

var ErrAlreadyRunning = errors.New("already running")

type AlreadyRunningError struct {
	PID int
}

func (e *AlreadyRunningError) Error() string {
	return fmt.Sprintf("gateway already running (pid %d)", e.PID)
}

func (e *AlreadyRunningError) Unwrap() error { return ErrAlreadyRunning }

type Lock struct {
	path string
	file *os.File
}

func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	if err := lockExclusive(f); err != nil {
		holder := os.Getpid()
		if _, seekErr := f.Seek(0, 0); seekErr == nil {
			if pid, parseErr := readPIDFile(f); parseErr == nil && pid > 0 {
				holder = pid
			}
		}
		f.Close()
		return nil, &AlreadyRunningError{PID: holder}
	}

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

// Release drops the advisory lock. The lock file is deliberately NOT unlinked:
// unlinking a lock file races with another process that already opened the same
// path — that process can take the lock on the now-orphaned inode while a third
// creates a fresh file at the same path and takes "the same" lock there, leaving
// two instances convinced they are exclusive. The file stays on disk with the
// last PID; only a held flock means anything.
func (l *Lock) Release() error {
	var firstErr error

	if l.file != nil {
		if err := unlockFile(l.file); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := l.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		l.file = nil
	}

	return firstErr
}

func (l *Lock) Path() string {
	return l.path
}

func ReadPID(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return readPIDFile(f)
}

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
