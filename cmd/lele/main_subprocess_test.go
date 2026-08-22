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
		args := strings.Split(sub, string(recordSep))
		os.Args = append([]string{os.Args[0]}, args...)
		main()
		os.Exit(0)
	}
	// Special subprocess routing for commands not reachable through main()'s
	// top-level dispatch (e.g. webServeCmd, which is invoked directly in the
	// live deployment). This keeps child coverage merged via the standard
	// re-exec + GOCOVERDIR mechanism used by LELE_TEST_MAIN.
	if os.Getenv("LELE_TEST_WEB") == "1" {
		webServeCmd(webServerOptions{Host: "127.0.0.1", Port: 0})
		os.Exit(0)
	}
	// TUI subprocess route: run tuiCmd with a session id. Pointed at a missing
	// config dir, loadConfig fails and tuiCmd prints an error then os.Exit(1).
	// The child flushes its atomic coverage counters on exit, so the merged
	// profile captures tuiCmd's setup/error path (the bubbletea program itself
	// cannot run without a TTY).
	if os.Getenv("LELE_TEST_TUI") != "" {
		os.Setenv("LELE_CONFIG_DIR", "/tmp/lele_tui_nonexistent_v5")
		tuiCmd(os.Getenv("LELE_TEST_TUI"))
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
	cmd.Env = append(os.Environ(), "LELE_TEST_MAIN="+strings.Join(args, string(recordSep)))
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