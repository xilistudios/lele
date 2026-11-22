//go:build windows

package tools

import "os/exec"

func detachControllingTerminal(cmd *exec.Cmd) {
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
