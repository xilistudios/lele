//go:build windows

package update

import (
	"os/exec"
	"syscall"
)

// setDetachFlags configures cmd to spawn a detached process on Windows:
// the child starts in its own process group so it is not affected by
// console events (Ctrl+C) delivered to the parent.
func setDetachFlags(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}
