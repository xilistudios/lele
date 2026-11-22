package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

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
		return
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) && exit.ExitCode() == 1 {
		t.Fatalf("%s is not ignored: a `git add -A` would commit the test residue "+
			"(add the `pkg/channels/tmp/` directory to .gitignore)", residue)
	}
	t.Skipf("cannot ask git which paths are ignored (check-ignore: %v)", runErr)
}
