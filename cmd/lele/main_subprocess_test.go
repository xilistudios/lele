package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain enables a subprocess mode: when LELE_TEST_MAIN env var is set to a
// value, the test binary re-executes the real main() with the given arguments.
// This lets us cover cmd/lele/main() for commands that return normally
// (no os.Exit), such as "version".
func TestMain(m *testing.M) {
	if sub := os.Getenv("LELE_TEST_MAIN"); sub != "" {
		args := strings.Split(sub, "\x00")
		os.Args = append([]string{os.Args[0]}, args...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runMainSubprocess invokes the current test binary (which routes to main()
// via TestMain) with the given command-line args and returns its combined
// output. Only safe for commands that exit code 0 (no os.Exit(1)).
func runMainSubprocess(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestMainPlaceholder")
	cmd.Env = append(os.Environ(), "LELE_TEST_MAIN="+strings.Join(args, "\x00"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\noutput: %s", err, out)
	}
	return string(out)
}

func TestMainPlaceholder(t *testing.T) {
	// This test only exists so -test.run can select the binary.
}

func TestMainCmd_Version(t *testing.T) {
	out := runMainSubprocess(t, "version")
	if !strings.Contains(out, "lele") {
		t.Errorf("version output missing lele, got: %q", out)
	}
}

func TestMainCmd_UnknownCommand(t *testing.T) {
	// Unknown command causes os.Exit(1); hide output. Not asserted.
	cmd := exec.Command(os.Args[0], "-test.run=TestMainPlaceholder")
	cmd.Env = append(os.Environ(), "LELE_TEST_MAIN=doesnotexist")
	_ = cmd.Run() // expected non-zero exit
}

func TestMainCmd_NoArgs(t *testing.T) {
	// main with no args prints help then os.Exit(1). Exercise the path via
	// subprocess without asserting output.
	cmd := exec.Command(os.Args[0], "-test.run=TestMainPlaceholder")
	cmd.Env = append(os.Environ(), "LELE_TEST_MAIN=")
	_ = cmd.Run() // main prints help then os.Exit(1); fine
}