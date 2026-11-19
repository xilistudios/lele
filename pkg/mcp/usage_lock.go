// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package mcp

import (
	"fmt"
	"os"
	"time"
)

// Cross-process serialisation of the usage-file read/merge/replace section.
//
// usageStore.mu only serialises ONE store. Two managers sharing a LeleDir (an
// agent's manager plus a probe, or two lele processes) each have their own
// store, their own mu and their own snapshot, so both can read the file, merge
// their own deltas and rename the result over it: the last rename wins and the
// other manager's increment disappears without a word. The pending-delta merge
// (see saveLocked) removes the SEQUENTIAL version of that loss; only a lock on
// the FILE closes the concurrent one.
//
// The lock is a sidecar (<file>.lock) created with O_CREATE|O_EXCL, which is
// atomic on every platform Go supports — no flock, so no per-OS build-tagged
// file and no behaviour difference on Windows. The protected section is a few
// hundred bytes of JSON, so contention is measured in microseconds.
//
// Best-effort by contract, like the rest of the store: a lock that cannot be
// taken within usageLockWait is reported and the write proceeds unlocked
// (losing one increment beats failing a tool call), and a sidecar left behind
// by a crash is taken over once it is older than usageLockStale.
const (
	// usageLockWait bounds how long one save waits for another writer. The
	// section it guards is tiny, so anything longer means an abnormal holder.
	usageLockWait = 100 * time.Millisecond
	// usageLockPoll is the retry interval while the lock is held.
	usageLockPoll = 2 * time.Millisecond
	// usageLockStale is how old a sidecar must be before it is assumed to
	// belong to a process that died while holding it. Far above the real
	// critical section (sub-millisecond) and far below a human-scale pause.
	usageLockStale = 10 * time.Second
)

// lockUsageFile serialises writers of the counter file at path. It returns the
// release func to call when the merge is done (never nil, so callers can defer
// it unconditionally) and an error when the lock could not be taken in time —
// in that case the release func is a no-op and the caller proceeds unlocked.
//
// A nil-safe empty path is not expected here (memory-only stores never save),
// but an error is returned rather than a panic for robustness.
func lockUsageFile(path string) (release func(), err error) {
	lockPath := path + ".lock"
	deadline := time.Now().Add(usageLockWait)
	for {
		f, openErr := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr == nil {
			// The sidecar only has to EXIST: its content carries no meaning.
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(openErr) {
			return func() {}, fmt.Errorf("create %s: %w", lockPath, openErr)
		}
		if usageLockIsStale(lockPath) {
			// Only one waiter can win this rename, so a crashed holder is
			// reclaimed exactly once and no waiter can steal a lock that was
			// just created by another one.
			usageLockTakeOver(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return func() {}, fmt.Errorf("timed out after %s waiting for %s", usageLockWait, lockPath)
		}
		time.Sleep(usageLockPoll)
	}
}

// usageLockIsStale reports whether the sidecar at path is old enough to belong
// to a dead holder. A missing file is not stale: the caller loops and retries
// the exclusive create, which is the fast path.
func usageLockIsStale(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > usageLockStale
}

// usageLockTakeOver removes a stale sidecar by renaming it away first: rename
// is atomic and only one process can rename a given file, which is what keeps
// two waiters from both "reclaiming" the same lock and then both holding it.
// A leftover .stale file from a previous takeover is removed on the next one.
func usageLockTakeOver(path string) {
	stalePath := path + ".stale"
	_ = os.Remove(stalePath)
	if err := os.Rename(path, stalePath); err != nil {
		return // another waiter won the takeover, or the holder released it
	}
	_ = os.Remove(stalePath)
}
