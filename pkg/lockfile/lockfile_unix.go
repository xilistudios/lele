//go:build !windows

package lockfile

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether the process with the given PID currently exists.
//
// It sends signal 0 (a no-op whose sole purpose is liveness probing) to the
// process. A nil error means the process exists; syscall.EPERM also means the
// process exists but is owned by another user; any other error (normally
// syscall.ESRCH) means the process does not exist.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM: process exists but we are not allowed to signal it.
	return errors.Is(err, syscall.EPERM)
}
