package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGitignore_UploadTestResidue pins the repo-hygiene rule that keeps the
// upload fixtures a test run leaves behind out of a commit. Some channel tests
// root uploads in the process working directory, so a run drops its files under
// pkg/channels/tmp/uploads/ in the SOURCE tree; their intended home is
// <LeleDir>/tmp/uploads, which pkg/channels/native_lele_dir_test.go asserts. The
// ignore rule is the only thing that stops a `git add -A` from committing that
// residue, so it is asserted here rather than left to inspection.
//
// The check asks the REAL tool over the REAL file — `git check-ignore` — instead
// of parsing .gitignore, so any rule that covers the path counts (a broader
// `tmp/` rule would do just as well). Where git or a work tree is unavailable
// (a packaged copy of the sources, for instance) the test skips instead of
// failing on the environment.
func TestGitignore_UploadTestResidue(t *testing.T) {
	const residue = "pkg/channels/tmp/uploads/test.txt"

	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is unavailable (%v): cannot ask which paths are ignored", err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	cmd := exec.Command("git", "check-ignore", "-q", residue)
	cmd.Dir = root
	runErr := cmd.Run()
	if runErr == nil {
		return // ignored: `git add -A` cannot pick the residue up
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) && exit.ExitCode() == 1 {
		t.Fatalf("%s is not ignored: a `git add -A` would commit the test residue "+
			"(add the `pkg/channels/tmp/` directory to .gitignore)", residue)
	}
	t.Skipf("cannot ask git which paths are ignored (check-ignore: %v)", runErr)
}
