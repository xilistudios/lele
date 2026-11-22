//go:build windows

package update

import (
	"os/exec"
	"syscall"
)

func setDetachFlags(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}
