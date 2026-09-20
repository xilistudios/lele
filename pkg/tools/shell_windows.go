//go:build windows

package tools

import "os/exec"

// detachControllingTerminal is a no-op on Windows. The Windows code path
// already uses "powershell -NoProfile -NonInteractive" which prevents
// interactive prompts. Windows process group management is handled
// differently (CREATE_NEW_PROCESS_GROUP / job objects) and is not needed
// for the current shell execution model.
func detachControllingTerminal(cmd *exec.Cmd) {
	// No-op: Windows uses -NonInteractive for powershell; no TTY to detach.
}

// killProcessTree on Windows falls back to the default single-process kill.
// Proper tree kill on Windows requires job objects, which is beyond the scope
// of this fix (the Windows path already avoids interactive TTY issues via
// powershell -NonInteractive).
func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
