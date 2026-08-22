package update

import (
	"os"
	"testing"
)

func TestUnsupportedEnvironmentError(t *testing.T) {
	e := &UnsupportedEnvironmentError{Reason: "some reason"}
	if e.Error() != "some reason" {
		t.Errorf("Error() = %q, want %q", e.Error(), "some reason")
	}
	var _ error = e // must satisfy error interface
}

// TestIsDocker_False is the primary environment in which tests normally
// run (no docker markers). It asserts the non-docker path.
func TestIsDocker_False(t *testing.T) {
	// Ensure env var doesn't claim docker.
	t.Setenv("container", "")
	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("running inside docker; cannot assert non-docker path")
	}
	if isDocker() {
		t.Error("expected isDocker() == false in a non-container test env")
	}
}

// TestIsDocker_EnvContainer covers the container env var branch.
func TestIsDocker_EnvContainer(t *testing.T) {
	t.Setenv("container", "docker")
	if !isDocker() {
		t.Error("expected isDocker() == true with container=docker")
	}
	t.Setenv("container", "podman")
	if !isDocker() {
		t.Error("expected isDocker() == true with container=podman")
	}
}

// TestCheckEnvironment_EnvContainer covers CheckEnvironment's isDocker
// short-circuit without touching real filesystem markers.
func TestCheckEnvironment_EnvContainer(t *testing.T) {
	t.Setenv("container", "docker")
	err := CheckEnvironment()
	ue, ok := err.(*UnsupportedEnvironmentError)
	if !ok {
		t.Fatalf("expected *UnsupportedEnvironmentError, got %T: %v", err, err)
	}
	if ue.Reason == "" {
		t.Error("reason should not be empty")
	}
}

// TestIsDocker_ProcCgroup covers the /proc/1/cgroup branch by pointing
// HOME away but we can't easily substitute /proc — so we exercise the
// code path only when the host cgroup mentions docker/containerd. This
// test guards determinism: it must not panic regardless.
func TestIsDocker_ProcCgroupDeterministic(t *testing.T) {
	t.Setenv("container", "")
	_ = isDocker() // must not panic / must return a value
}

// TestCheckEnvironment_OK when clearly not docker (env unset and no markers).
func TestCheckEnvironment_OK(t *testing.T) {
	t.Setenv("container", "")
	if err := CheckEnvironment(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

// TestCheckEnvironment_DockerenvMarker covers the /.dockerenv branch via
// a temp dir is not possible (path is fixed), so this only asserts the
// function is safe to call in any environment.
func TestCheckEnvironment_DockerenvMarkerSafe(t *testing.T) {
	_ = CheckEnvironment()
}
