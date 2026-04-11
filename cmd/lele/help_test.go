package main

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"testing"

	"github.com/xilistudios/lele/pkg/i18n"
)

func captureOutput(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestPrintHelp_ContainsVersion(t *testing.T) {
	i18n.SetLanguage("en")
	version = "test-version"
	output := captureOutput(printHelp)

	if !regexp.MustCompile(`lele - Personal AI Assistant`).MatchString(output) {
		t.Errorf("printHelp should contain 'lele - Personal AI Assistant', got: %s", output)
	}
}

func TestPrintHelp_ContainsCommands(t *testing.T) {
	output := captureOutput(printHelp)

	expectedCommands := []string{
		"onboard",
		"agent",
		"auth",
		"gateway",
		"web",
		"status",
		"cron",
		"migrate",
		"skills",
		"client",
		"version",
	}

	for _, cmd := range expectedCommands {
		if !regexp.MustCompile(cmd).MatchString(output) {
			t.Errorf("printHelp should contain command '%s'", cmd)
		}
	}
}

func TestMigrateHelp_ContainsOptions(t *testing.T) {
	output := captureOutput(migrateHelp)

	expectedOptions := []string{
		"--dry-run",
		"--refresh",
		"--config-only",
		"--workspace-only",
		"--force",
		"--openclaw-home",
		"--lele-home",
	}

	for _, opt := range expectedOptions {
		if !regexp.MustCompile(opt).MatchString(output) {
			t.Errorf("migrateHelp should contain option '%s'", opt)
		}
	}
}

func TestMigrateHelp_ContainsExamples(t *testing.T) {
	output := captureOutput(migrateHelp)

	if !regexp.MustCompile(`lele migrate`).MatchString(output) {
		t.Error("migrateHelp should contain example commands")
	}
}

func TestWebHelp_ContainsCommands(t *testing.T) {
	output := captureOutput(webHelp)

	expectedCommands := []string{
		"start",
		"stop",
		"status",
	}

	for _, cmd := range expectedCommands {
		if !regexp.MustCompile(cmd).MatchString(output) {
			t.Errorf("webHelp should contain command '%s'", cmd)
		}
	}
}

func TestWebHelp_ContainsOptions(t *testing.T) {
	output := captureOutput(webHelp)

	if !regexp.MustCompile(`--host`).MatchString(output) {
		t.Error("webHelp should contain --host option")
	}
	if !regexp.MustCompile(`--port`).MatchString(output) {
		t.Error("webHelp should contain --port option")
	}
}

func TestCronHelp_ContainsCommands(t *testing.T) {
	output := captureOutput(cronHelp)

	expectedCommands := []string{
		"list",
		"add",
		"remove",
		"enable",
		"disable",
	}

	for _, cmd := range expectedCommands {
		if !regexp.MustCompile(cmd).MatchString(output) {
			t.Errorf("cronHelp should contain command '%s'", cmd)
		}
	}
}

func TestSkillsHelp_ContainsCommands(t *testing.T) {
	output := captureOutput(skillsHelp)

	expectedCommands := []string{
		"list",
		"install",
		"install-builtin",
		"list-builtin",
		"remove",
		"search",
		"show",
	}

	for _, cmd := range expectedCommands {
		if !regexp.MustCompile(cmd).MatchString(output) {
			t.Errorf("skillsHelp should contain command '%s'", cmd)
		}
	}
}

func TestClientHelp_ContainsCommands(t *testing.T) {
	output := captureOutput(clientHelp)

	expectedCommands := []string{
		"pin",
		"list",
		"pending",
		"remove",
		"status",
	}

	for _, cmd := range expectedCommands {
		if !regexp.MustCompile(cmd).MatchString(output) {
			t.Errorf("clientHelp should contain command '%s'", cmd)
		}
	}
}

func TestAuthHelp_ContainsCommands(t *testing.T) {
	output := captureOutput(authHelp)

	expectedCommands := []string{
		"login",
		"logout",
		"status",
	}

	for _, cmd := range expectedCommands {
		if !regexp.MustCompile(cmd).MatchString(output) {
			t.Errorf("authHelp should contain command '%s'", cmd)
		}
	}
}
