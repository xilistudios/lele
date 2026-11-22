//go:build linux

package keepawake

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

func platformStart() (func(), error) {
	if _, err := exec.LookPath("systemd-inhibit"); err != nil {
		return nil, fmt.Errorf("keep-awake: systemd-inhibit not available: %w", err)
	}
	return startCommand(linuxInhibitCommand())
}

func linuxInhibitCommand() *exec.Cmd {
	return exec.Command("systemd-inhibit",
		"--what=idle:sleep",
		"--who=lele",
		"--why=lele is running",
		"--mode=block",
		"sh",
		"-c",
		"while kill -0 "+strconv.Itoa(os.Getpid())+" 2>/dev/null; do sleep 5; done",
	)
}
