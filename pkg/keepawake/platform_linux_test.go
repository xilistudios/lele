//go:build linux

package keepawake

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestLinuxInhibitCommandShape(t *testing.T) {
	cmd := linuxInhibitCommand()

	// cmd.Path is what exec.Command resolved; basename must be systemd-inhibit.
	if base := filepath.Base(cmd.Path); base != "systemd-inhibit" {
		// exec.Command resolves through LookPath; accept a full resolved path
		// as long as the basename matches.
		t.Errorf("cmd.Path = %q, want basename systemd-inhibit", cmd.Path)
	}

	args := cmd.Args
	if len(args) < 8 {
		t.Fatalf("args too short: %q", args)
	}
	// args[0] is the program name as passed ("systemd-inhibit").
	if !strings.Contains(args[0], "systemd-inhibit") {
		t.Errorf("args[0] = %q, want systemd-inhibit", args[0])
	}

	wantPrefixes := []string{
		"--what=idle:sleep",
		"--who=lele",
		"--mode=block",
	}
	for _, w := range wantPrefixes {
		if !containsArg(args, w) {
			t.Errorf("argv missing %q (args=%q)", w, args)
		}
	}

	// `sh`, `-c` and the loop must be present, in that order, with the pid.
	shIdx := indexOfArg(args, "sh")
	cIdx := indexOfArg(args, "-c")
	if shIdx < 0 || cIdx < 0 || cIdx != shIdx+1 {
		t.Fatalf("expected sh -c adjacent in argv, got args=%q", args)
	}
	last := args[len(args)-1]
	if !strings.Contains(last, strconv.Itoa(os.Getpid())) {
		t.Errorf("last arg %q missing pid %d", last, os.Getpid())
	}
	if !strings.Contains(last, "kill -0") {
		t.Errorf("last arg %q missing kill -0", last)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func indexOfArg(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

func TestPlatformStartWithoutSystemdInhibit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only test")
	}
	// Empty PATH => LookPath("systemd-inhibit") fails deterministically.
	t.Setenv("PATH", t.TempDir())

	release, err := platformStart()
	if err == nil {
		if release != nil {
			release()
		}
		t.Fatal("expected error when systemd-inhibit is not in PATH")
	}
	if !strings.Contains(err.Error(), "systemd-inhibit") {
		t.Fatalf("error %q must name systemd-inhibit", err)
	}
	if !strings.Contains(err.Error(), "keep-awake:") {
		t.Fatalf("error %q must carry the keep-awake prefix", err)
	}
}
