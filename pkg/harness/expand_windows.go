//go:build windows

package harness

import "os/exec"

// detachShellChild is a no-op on Windows. See pkg/tools/shell_windows.go.
func detachShellChild(c *exec.Cmd) {
	// No-op: Windows uses -NonInteractive for powershell; no TTY to detach.
}
