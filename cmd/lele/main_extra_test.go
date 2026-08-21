package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseConfigDirFlag(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name          string
		args          []string
		wantConfigDir string
		wantRemaining []string
	}{
		{name: "no flag", args: []string{"agent", "-m", "hi"}, wantConfigDir: "", wantRemaining: []string{"agent", "-m", "hi"}},
		{name: "long equals", args: []string{"--config-dir=" + dir, "agent"}, wantConfigDir: dir, wantRemaining: []string{"agent"}},
		{name: "short equals", args: []string{"-c=" + dir, "status"}, wantConfigDir: dir, wantRemaining: []string{"status"}},
		{name: "long space", args: []string{"--config-dir", dir, "tui"}, wantConfigDir: dir, wantRemaining: []string{"tui"}},
		{name: "short space", args: []string{"-c", dir, "onboard"}, wantConfigDir: dir, wantRemaining: []string{"onboard"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, remaining := parseConfigDirFlag(tt.args)
			if got != tt.wantConfigDir {
				t.Errorf("configDir = %q, want %q", got, tt.wantConfigDir)
			}
			if !reflect.DeepEqual(remaining, tt.wantRemaining) {
				t.Errorf("remaining = %v, want %v", remaining, tt.wantRemaining)
			}
		})
	}
}

// TestParseConfigDirFlag_MissingValue exercises the branch where the flag is
// the last argument with no following value.
func TestParseConfigDirFlag_MissingValue(t *testing.T) {
	got, remaining := parseConfigDirFlag([]string{"--config-dir"})
	if got != "" {
		t.Errorf("configDir = %q, want empty", got)
	}
	if len(remaining) != 1 || remaining[0] != "--config-dir" {
		t.Errorf("remaining = %v, want [--config-dir]", remaining)
	}
}

// validateConfigDir calls os.Exit on failure paths, which we must not test.
// Instead we test the success path only via a valid existing directory.
func TestValidateConfigDir_Valid(t *testing.T) {
	dir := t.TempDir()
	// Must not exit for a valid existing directory.
	validateConfigDir(dir) // no panic/exit means success
}

// TestParseSessionFlag_Additional helpers already in main_test.go.
func TestPrintVersion_WithVersion(t *testing.T) {
	oldVersion, oldBuild, oldGo := version, buildTime, goVersion
	version = "9.9.9"
	buildTime = "2024-05-05"
	goVersion = ""
	defer func() { version, buildTime, goVersion = oldVersion, oldBuild, oldGo }()

	out := runCmd(printVersion)
	if !strings.Contains(out, "9.9.9") {
		t.Errorf("printVersion should contain version, got: %s", out)
	}
	if !strings.Contains(out, "2024-05-05") {
		t.Errorf("printVersion should contain build time, got: %s", out)
	}
	if !strings.Contains(out, "Go:") {
		t.Errorf("printVersion should contain Go version, got: %s", out)
	}
}

func TestPrintVersion_NoBuild(t *testing.T) {
	oldVersion, oldBuild, oldGo := version, buildTime, goVersion
	version = "1.0.0"
	buildTime = ""
	goVersion = ""
	defer func() { version, buildTime, goVersion = oldVersion, oldBuild, oldGo }()

	out := runCmd(printVersion)
	if !strings.Contains(out, "1.0.0") {
		t.Errorf("printVersion should contain version, got: %s", out)
	}
	if strings.Contains(out, "Build:") {
		t.Errorf("printVersion should not contain Build when empty, got: %s", out)
	}
}

func TestCopyEmbeddedToTarget(t *testing.T) {
	target := t.TempDir()
	if err := copyEmbeddedToTarget(target); err != nil {
		t.Fatalf("copyEmbeddedToTarget: %v", err)
	}
	// The workspace exports should have been written.
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected embedded files copied to target")
	}
}

func TestCopyEmbeddedToTarget_BadTarget(t *testing.T) {
	// A path that prevents MkdirAll should return an error.
	err := copyEmbeddedToTarget("/proc/definitely/not/creatable/xyz")
	if err == nil {
		t.Error("expected error for uncreatable target")
	}
}

func TestCreateWorkspaceTemplatesCmd(t *testing.T) {
	target := filepath.Join(t.TempDir(), "ws")
	_ = captureStdout(t) // discard
	createWorkspaceTemplates(target)
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		t.Errorf("workspace templates dir should exist, err=%v", err)
	}
}

func TestRegisterKeyringResolver_Disabled(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Keyring.Enabled = false
	// Must not panic.
	registerKeyringResolver(cfg)
}

func TestRegisterKeyringResolver_Nil(t *testing.T) {
	registerKeyringResolver(nil) // must not panic
}