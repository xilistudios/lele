//go:build darwin

package keepawake

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDarwinCaffeinateCommandShape(t *testing.T) {
	cmd := darwinCaffeinateCommand()

	if base := filepath.Base(cmd.Path); base != "caffeinate" {
		t.Errorf("cmd.Path = %q, want basename caffeinate", cmd.Path)
	}

	want := []string{"caffeinate", "-dims", "-w", strconv.Itoa(os.Getpid())}
	if len(cmd.Args) != len(want) {
		t.Fatalf("args = %q, want exactly %q", cmd.Args, want)
	}
	for i := range want {
		if !strings.Contains(cmd.Args[i], want[i]) {
			t.Errorf("args[%d] = %q, want %q", i, cmd.Args[i], want[i])
		}
	}

	for _, a := range cmd.Args[1:] {
		if a == "-d" || a == "-i" || a == "-m" || a == "-s" {
			t.Errorf("argv contains split flag %q; want a single -dims token", a)
		}
	}
}
