package filelock

import (
	"fmt"
	"os"
	"time"
)

const (
	Wait       = 100 * time.Millisecond
	Poll       = 2 * time.Millisecond
	StaleAfter = 10 * time.Second
)

func Acquire(path string) (release func(), err error) {
	lockPath := path + ".lock"
	deadline := time.Now().Add(Wait)
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
			takeOver(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return func() {}, fmt.Errorf("timed out after %s waiting for %s", Wait, lockPath)
		}
		time.Sleep(Poll)
	}
}

func isStale(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > StaleAfter
}

func takeOver(path string) {
	stalePath := path + ".stale"
	_ = os.Remove(stalePath)
	if err := os.Rename(path, stalePath); err != nil {
		return
	}
	_ = os.Remove(stalePath)
}
