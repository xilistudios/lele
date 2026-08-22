package channels

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"root", "/", "/"},
		{"single slash suffix", "/api/v1/", "/api/v1"},
		{"multiple trailing slashes strips one", "/api//", "/api/"},
		{"no trailing slash", "/api/v1", "/api/v1"},
		{"one char", "a/", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizePath(tt.in); got != tt.want {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExpandHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain path unchanged", "/etc/foo", "/etc/foo"},
		{"tilde alone", "~", home},
		{"tilde slash", "~/config", home + "/config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandHomePath(tt.in); got != tt.want {
				t.Errorf("expandHomePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsAllowedWorkspacePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()

	tests := []struct {
		name    string
		path    string
		allowed bool
	}{
		{"under home", filepath.Join(home, "lele", "ws"), true},
		{"home itself", home, true},
		{"tmp", "/tmp/lele-ws", true},
		{"tmp direct without slash", "/tmpx", filepath.Join(home, "tmpx") != "" && home != ""},
		{"cwd subpath", filepath.Join(cwd, "somewhere"), cwd != ""},
		{"unrelated", "/etc", home != "" && filepath.Join("/etc", "x") == ""},
	}
	// Simplify: use explicit expectations that don't depend on runtime.
	_ = tests

	if !isAllowedWorkspacePath(filepath.Join(home, "lele", "ws")) {
		t.Error("expected path under home to be allowed")
	}
	if isAllowedWorkspacePath("/this/totally/unrelated/path") {
		t.Error("expected unrelated path to be denied")
	}
	if !isAllowedWorkspacePath("/tmp/lele-ws") {
		t.Error("expected /tmp/ path to be allowed")
	}
	if home != "" {
		if !isAllowedWorkspacePath(home) {
			t.Error("home itself should be allowed")
		}
	}
	if cwd != "" {
		if !isAllowedWorkspacePath(filepath.Join(cwd, "testdata")) {
			t.Error("path under cwd should be allowed")
		}
	}
}
