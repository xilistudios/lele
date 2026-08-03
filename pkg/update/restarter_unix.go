//go:build !windows

package update

import (
	"os/exec"
	"syscall"
)

// setDetachFlags configures cmd to spawn a detached process on Unix
// systems: the child gets its own session so it survives the parent.
func setDetachFlags(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
