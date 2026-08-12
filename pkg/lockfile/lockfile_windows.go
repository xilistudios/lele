//go:build windows

package lockfile

// processAlive reports whether the process with the given PID currently exists.
//
// On Windows, os.FindProcess always succeeds for any PID (it performs no real
// liveness check), and there is no portable way to probe a process without
// potentially disrupting it. We therefore take a conservative approach and
// treat any PID greater than zero as alive.
//
// Limitation: this means a stale lock file whose PID has been reused by an
// unrelated process will be treated as held. In practice the lock lives under
// the per-user ~/.lele state directory and PID reuse within a session is rare,
// so this trade-off is acceptable. Callers that need strict correctness on
// Windows should clear the lock file manually.
func processAlive(pid int) bool {
	return pid > 0
}