//go:build !windows

package harness

import (
	"os/exec"
	"syscall"
)

func detachShellChild(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
