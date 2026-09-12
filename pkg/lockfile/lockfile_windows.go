//go:build windows

package lockfile

import (
	"errors"
	"os"
)

// lockExclusive is the Windows fallback guard: Go's syscall package does not
// expose flock, so the conservative PID-file liveness check remains the
// single-instance guard on this platform (behavior unchanged from the
// pre-flock implementation).
//
// It returns a non-nil error when the PID file names a live process. The
// check-then-write TOCTOU window that flock closes on Unix still exists here;
// in practice the lock lives under the per-user ~/.lele state directory and
// concurrent startup races are rare, so this trade-off is acceptable. Callers
// that need strict correctness on Windows should clear the lock file manually.
func lockExclusive(f *os.File) error {
	pid, err := readPIDFile(f)
	if err != nil {
		// Missing, empty or corrupt PID file: stale lock, take over.
		return nil
	}
	if processAlive(pid) {
		return errors.New("pid file names a live process")
	}
	return nil
}

// unlockFile is a no-op stub on Windows (there is no flock to release; the
// PID file is truncated and removed by Release).
func unlockFile(f *os.File) error { return nil }

// processAlive reports whether the process with the given PID currently
// exists.
//
// On Windows, os.FindProcess always succeeds for any PID (it performs no real
// liveness check), and there is no portable way to probe a process without
// potentially disrupting it. We therefore take a conservative approach and
// treat any PID greater than zero as alive.
//
// Limitation: this means a stale lock file whose PID has been reused by an
// unrelated process will be treated as held. In practice the lock lives under
// the per-user ~/.lele state directory and PID reuse within a session is rare,
// so this trade-off is acceptable.
func processAlive(pid int) bool {
	return pid > 0
}
