//go:build !windows

package update

import (
	"os/exec"
	"syscall"
)

func setDetachFlags(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
