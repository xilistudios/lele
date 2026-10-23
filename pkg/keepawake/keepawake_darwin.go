//go:build darwin

package keepawake

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// platformStart inhibits sleep through caffeinate (see darwinCaffeinateCommand
// for the exact mechanism). caffeinate lives in /usr/bin, so LookPath only
// fails in exotic environments — the error is wrapped regardless.
func platformStart() (func(), error) {
	if _, err := exec.LookPath("caffeinate"); err != nil {
		return nil, fmt.Errorf("keep-awake: caffeinate not available: %w", err)
	}
	return startCommand(darwinCaffeinateCommand())
}

// darwinCaffeinateCommand builds the caffeinate invocation:
//
//	/usr/bin/caffeinate -dims -w <pid>
//
//	- -d  prevent display sleep
//	- -i  prevent system idle sleep
//	- -m  prevent disk idle sleep
//	- -s  prevent system sleep (only honoured on AC power)
//	- -w <pid> wait for that pid, so the assertion drops by itself when lele
//	  exits (no orphan assertion even on SIGKILL).
func darwinCaffeinateCommand() *exec.Cmd {
	return exec.Command("caffeinate",
		"-dims",
		"-w",
		strconv.Itoa(os.Getpid()),
	)
}
