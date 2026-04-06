package main

import (
	"testing"
)

func TestFormatVersion_NoGitCommit(t *testing.T) {
	version = "1.0.0"
	gitCommit = ""

	result := formatVersion()
	if result != "1.0.0" {
		t.Errorf("formatVersion() = %q, want %q", result, "1.0.0")
	}
}

func TestFormatVersion_WithGitCommit(t *testing.T) {
	version = "1.0.0"
	gitCommit = "abc123"

	result := formatVersion()
	expected := "1.0.0 (git: abc123)"
	if result != expected {
		t.Errorf("formatVersion() = %q, want %q", result, expected)
	}
}

func TestFormatBuildInfo_NoBuildTime(t *testing.T) {
	buildTime = ""
	goVersion = ""

	build, goVer := formatBuildInfo()
	if build != "" {
		t.Errorf("build = %q, want empty", build)
	}
	if goVer == "" {
		t.Error("goVer should not be empty when goVersion var is empty (should use runtime.Version())")
	}
}

func TestFormatBuildInfo_WithBuildTime(t *testing.T) {
	buildTime = "2024-01-01T00:00:00Z"
	goVersion = "go1.21.0"

	build, goVer := formatBuildInfo()
	if build != buildTime {
		t.Errorf("build = %q, want %q", build, buildTime)
	}
	if goVer != goVersion {
		t.Errorf("goVer = %q, want %q", goVer, goVersion)
	}
}

func TestFormatBuildInfo_UsesRuntimeVersion(t *testing.T) {
	goVersion = ""

	_, goVer := formatBuildInfo()
	if goVer == "" {
		t.Error("goVer should use runtime.Version() when goVersion is empty")
	}
}
