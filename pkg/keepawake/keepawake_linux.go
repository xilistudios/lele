//go:build linux

package keepawake

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// platformStart inhibits sleep through systemd-inhibit (see linuxInhibitCommand
// for the exact mechanism). It fails with a clear wrapped error when
// systemd-inhibit is not installed or the session has no systemd (WSL,
// non-systemd distros).
func platformStart() (func(), error) {
	if _, err := exec.LookPath("systemd-inhibit"); err != nil {
		return nil, fmt.Errorf("keep-awake: systemd-inhibit not available: %w", err)
	}
	return startCommand(linuxInhibitCommand())
}

// linuxInhibitCommand builds the systemd-inhibit invocation.
//
//	/usr/bin/systemd-inhibit --what=idle:sleep --who=lele --why=lele is running \
//	  --mode=block sh -c 'while kill -0 <pid> 2>/dev/null; do sleep 5; done'
//
// Rationale:
//
//   - idle:sleep inhibits idle- and sleep-triggered suspend but deliberately
//     does NOT include handle-lid-switch: closing the lid is an explicit user
//     action, so we never fight it.
//
//   - The `kill -0 <pid>` loop makes the inhibition self-release even if lele
//     is SIGKILLed (the guard notices the pid is gone within 5 s and exits,
//     dropping the inhibitor), and it costs one short-lived `sleep` process
//     per 5 s.
//
//   - Stop() releases by signalling the child's whole process group
//     (startCommand runs it with Setpgid), so the shell AND the `sleep 5` of
//     a loop iteration already in flight die together: no orphan is left
//     behind, and the stderr pipe closes immediately (which is what lets
//     cmd.Wait — and therefore Stop — return in ~1 ms in the normal case;
//     the 2 s + 1 s bounds in startCommand only apply if the group ignores
//     SIGTERM).
//
//   - If the lele PID were recycled, the recycled process would keep the
//     inhibition alive (the guard would see "its" pid as still running).
//     Practically impossible, but documented for completeness.
//
// Only os/exec is used — no shell interpolation happens on our side (the
// single `sh -c` argument is passed verbatim as one argv entry).
func linuxInhibitCommand() *exec.Cmd {
	return exec.Command("systemd-inhibit",
		"--what=idle:sleep",
		"--who=lele",
		"--why=lele is running",
		"--mode=block",
		"sh",
		"-c",
		"while kill -0 "+strconv.Itoa(os.Getpid())+" 2>/dev/null; do sleep 5; done",
	)
}
