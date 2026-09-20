//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// detachControllingTerminal sets up the child process to run in its own
// session (via setsid), detaching it from the parent's controlling terminal.
// This is critical for non-interactive execution: commands like git, sudo,
// ssh, and gh open /dev/tty for credential prompts or interactive pagers.
// Without setsid the child inherits the TUI's controlling terminal (bubbletea
// raw mode), writes its prompt over the TUI screen, and blocks forever waiting
// for input the TUI can never deliver. With setsid the child has no
// controlling terminal, so open("/dev/tty") fails with ENXIO and interactive
// prompts fail fast instead of hanging the whole agent.
func detachControllingTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Start a new session (detaches controlling tty)
	}
}

// killProcessTree kills the entire process group rooted at cmd. Because
// detachControllingTerminal sets Setsid: true, the child becomes a session
// leader whose pgid == pid. Sending SIGKILL to -pgid kills the leader and
// every descendant (e.g. git spawned by sh -c "git push"), which the default
// cmd.Process.Kill() cannot do — it only signals the direct child (the sh
// wrapper), orphaning grandchildren that keep running and may still hold the
// TUI terminal.
//
// If the group kill fails (e.g. ESRCH if the child is not a session leader
// for some reason, or on a non-setsid path), we fall back to the default
// single-process kill.
func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Kill the entire process group (negative pid = kill -pgid).
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		// Fallback: the child might not be a group/session leader (e.g.
		// Setsid wasn't used or the process already exited). Kill just
		// the direct process.
		_ = cmd.Process.Kill()
	}
}
