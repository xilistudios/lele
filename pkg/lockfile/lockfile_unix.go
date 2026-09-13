//go:build !windows

package lockfile

import (
	"os"
	"syscall"
)

// lockExclusive takes an advisory kernel lock (flock) on the open file, in
// non-blocking mode. It is the authoritative single-instance guard: the kernel
// releases it automatically when the owning process (or the last fd referring
// to the open file description) exits, so it can never go stale.
//
// Returns an error (typically EWOULDBLOCK/EAGAIN) when another process — or
// another open file description in the same process — already holds the lock.
func lockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// unlockFile releases a lock taken by lockExclusive. Closing the file also
// releases it, so this is a best-effort explicit hand-back.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
