//go:build darwin

package keepawake

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

func platformStart() (func(), error) {
	if _, err := exec.LookPath("caffeinate"); err != nil {
		return nil, fmt.Errorf("keep-awake: caffeinate not available: %w", err)
	}
	return startCommand(darwinCaffeinateCommand())
}

func darwinCaffeinateCommand() *exec.Cmd {
	return exec.Command("caffeinate",
		"-dims",
		"-w",
		strconv.Itoa(os.Getpid()),
	)
}
