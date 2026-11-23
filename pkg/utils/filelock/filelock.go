package filelock

import (
	"fmt"
	"os"
	"time"
)

const (
	AcquireTimeout = 1 * time.Second
	AcquirePoll    = 2 * time.Millisecond
	StaleAfter     = 10 * time.Second
)

func Acquire(path string) (release func(), err error) {
	lockPath := path + ".lock"
	deadline := time.Now().Add(AcquireTimeout)
	for {
		f, openErr := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(openErr) {
			return func() {}, fmt.Errorf("create %s: %w", lockPath, openErr)
		}
		if isStale(lockPath) {
			if takeOver(lockPath) == nil {
				continue
			}
		}
		if time.Now().After(deadline) {
			return func() {}, fmt.Errorf("timed out after %s waiting for %s", AcquireTimeout, lockPath)
		}
		time.Sleep(AcquirePoll)
	}
}

func isStale(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > StaleAfter
}

func takeOver(path string) error {
	stalePath := path + ".stale"
	_ = os.Remove(stalePath)
	if err := os.Rename(path, stalePath); err != nil {
		return fmt.Errorf("reclaim %s: %w", path, err)
	}
	_ = os.Remove(stalePath)
	return nil
}
