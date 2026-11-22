//go:build windows

package lockfile

import (
	"errors"
	"os"
)

func lockExclusive(f *os.File) error {
	pid, err := readPIDFile(f)
	if err != nil {
		return nil
	}
	if processAlive(pid) {
		return errors.New("pid file names a live process")
	}
	return nil
}

func unlockFile(f *os.File) error { return nil }

func processAlive(pid int) bool {
	return pid > 0
}
