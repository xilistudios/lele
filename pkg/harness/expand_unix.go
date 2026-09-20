//go:build !windows

package harness

import (
	"os/exec"
	"syscall"
)

// detachShellChild sets Setsid on the command so the child runs in its own
// session with no controlling terminal. This prevents commands that open
// /dev/tty (git credential prompts, pagers, sudo) from blocking on the
// parent's terminal. See pkg/tools/shell_unix.go for the full rationale.
func detachShellChild(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
