package mcp

import (
	"fmt"
	"os"
	"time"
)

const (
	usageLockWait  = 100 * time.Millisecond
	usageLockPoll  = 2 * time.Millisecond
	usageLockStale = 10 * time.Second
)

func lockUsageFile(path string) (release func(), err error) {
	lockPath := path + ".lock"
	deadline := time.Now().Add(usageLockWait)
	for {
		f, openErr := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(openErr) {
			return func() {}, fmt.Errorf("create %s: %w", lockPath, openErr)
		}
		if usageLockIsStale(lockPath) {
			usageLockTakeOver(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return func() {}, fmt.Errorf("timed out after %s waiting for %s", usageLockWait, lockPath)
		}
		time.Sleep(usageLockPoll)
	}
}

func usageLockIsStale(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > usageLockStale
}

func usageLockTakeOver(path string) {
	stalePath := path + ".stale"
	_ = os.Remove(stalePath)
	if err := os.Rename(path, stalePath); err != nil {
		return
	}
	_ = os.Remove(stalePath)
}
