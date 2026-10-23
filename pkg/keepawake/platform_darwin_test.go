//go:build darwin

package keepawake

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDarwinCaffeinateCommandShape pins the exact argv of the caffeinate
// invocation: `-dims` as a SINGLE token, then `-w`, then our own pid, in that
// order, with no extra flags. Mirror of TestLinuxInhibitCommandShape.
// This test cannot run on Linux CI (darwin-only build tag); it is verified by
// `GOOS=darwin go vet ./pkg/keepawake` and by running the suite on macOS.
func TestDarwinCaffeinateCommandShape(t *testing.T) {
	cmd := darwinCaffeinateCommand()

	// cmd.Path is what exec.Command resolved through LookPath; basename must
	// be caffeinate.
	if base := filepath.Base(cmd.Path); base != "caffeinate" {
		t.Errorf("cmd.Path = %q, want basename caffeinate", cmd.Path)
	}

	// Exact argv: program name, -dims (one token), -w, own pid.
	want := []string{"caffeinate", "-dims", "-w", strconv.Itoa(os.Getpid())}
	if len(cmd.Args) != len(want) {
		t.Fatalf("args = %q, want exactly %q", cmd.Args, want)
	}
	for i := range want {
		if !strings.Contains(cmd.Args[i], want[i]) {
			t.Errorf("args[%d] = %q, want %q", i, cmd.Args[i], want[i])
		}
	}

	// -dims must be a single token: no separate -d/-i/-m/-s entries.
	for _, a := range cmd.Args[1:] {
		if a == "-d" || a == "-i" || a == "-m" || a == "-s" {
			t.Errorf("argv contains split flag %q; want a single -dims token", a)
		}
	}
}
