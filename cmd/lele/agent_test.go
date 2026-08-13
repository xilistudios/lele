package main

import (
	"testing"
)

// parseCLIArgs tests the argument parsing logic extracted from agentCmd.
// This isolates the pure parsing logic from I/O and agent setup.

func TestParseCLIArgs_Default(t *testing.T) {
	// Simulate: lele agent
	msg, session, _, verbose := parseCLIArgs([]string{})
	if msg != "" {
		t.Errorf("message = %q, want %q", msg, "")
	}
	if session != "cli:default" {
		t.Errorf("session = %q, want %q", session, "cli:default")
	}
	if verbose {
		t.Error("verbose = true, want false")
	}
}

func TestParseCLIArgs_MessageShort(t *testing.T) {
	msg, session, _, _ := parseCLIArgs([]string{"-m", "hello world"})
	if msg != "hello world" {
		t.Errorf("message = %q, want %q", msg, "hello world")
	}
	if session != "cli:default" {
		t.Errorf("session = %q, want %q", session, "cli:default")
	}
}

func TestParseCLIArgs_MessageLong(t *testing.T) {
	msg, session, _, _ := parseCLIArgs([]string{"--message", "test message"})
	if msg != "test message" {
		t.Errorf("message = %q, want %q", msg, "test message")
	}
	if session != "cli:default" {
		t.Errorf("session = %q, want %q", session, "cli:default")
	}
}

func TestParseCLIArgs_SessionShort(t *testing.T) {
	msg, session, _, _ := parseCLIArgs([]string{"-s", "my-session"})
	if msg != "" {
		t.Errorf("message = %q, want %q", msg, "")
	}
	if session != "my-session" {
		t.Errorf("session = %q, want %q", session, "my-session")
	}
}

func TestParseCLIArgs_SessionLong(t *testing.T) {
	msg, session, _, _ := parseCLIArgs([]string{"--session", "custom-session"})
	if msg != "" {
		t.Errorf("message = %q, want %q", msg, "")
	}
	if session != "custom-session" {
		t.Errorf("session = %q, want %q", session, "custom-session")
	}
}

func TestParseCLIArgs_DebugFlag(t *testing.T) {
	_, _, debug, _ := parseCLIArgs([]string{"--debug"})
	if !debug {
		t.Error("debug = false, want true")
	}
}

func TestParseCLIArgs_DebugShort(t *testing.T) {
	_, _, debug, _ := parseCLIArgs([]string{"-d"})
	if !debug {
		t.Error("debug = false, want true")
	}
}

func TestParseCLIArgs_DebugAndVerboseCombined(t *testing.T) {
	// When both --debug and --verbose are passed, both should be true
	// The precedence logic is in agentCmd (debug wins), but parseCLIArgs should return both as true
	_, _, debug, verbose := parseCLIArgs([]string{"--debug", "--verbose"})
	if !debug {
		t.Error("debug = false, want true")
	}
	if !verbose {
		t.Error("verbose = false, want true")
	}
}

func TestParseCLIArgs_VerboseAndDebugCombined(t *testing.T) {
	// Order shouldn't matter - test reverse order
	_, _, debug, verbose := parseCLIArgs([]string{"-v", "-d"})
	if !debug {
		t.Error("debug = false, want true")
	}
	if !verbose {
		t.Error("verbose = false, want true")
	}
}

func TestParseCLIArgs_VerboseFlag(t *testing.T) {
	_, _, _, verbose := parseCLIArgs([]string{"--verbose"})
	if !verbose {
		t.Error("verbose = false, want true")
	}
}

func TestParseCLIArgs_VerboseShort(t *testing.T) {
	_, _, _, verbose := parseCLIArgs([]string{"-v"})
	if !verbose {
		t.Error("verbose = false, want true")
	}
}

func TestParseCLIArgs_AllFlags(t *testing.T) {
	msg, session, debug, verbose := parseCLIArgs([]string{
		"-d", "-v", "-m", "hello", "-s", "test-sess",
	})
	if msg != "hello" {
		t.Errorf("message = %q, want %q", msg, "hello")
	}
	if session != "test-sess" {
		t.Errorf("session = %q, want %q", session, "test-sess")
	}
	if !debug {
		t.Error("debug = false, want true")
	}
	if !verbose {
		t.Error("verbose = false, want true")
	}
}

func TestParseCLIArgs_UnknownFlagIgnored(t *testing.T) {
	msg, session, debug, verbose := parseCLIArgs([]string{
		"--unknown", "value", "-m", "test",
	})
	if msg != "test" {
		t.Errorf("message = %q, want %q", msg, "test")
	}
	if session != "cli:default" {
		t.Errorf("session = %q, want %q", session, "cli:default")
	}
	if debug {
		t.Error("debug = true, want false")
	}
	if verbose {
		t.Error("verbose = true, want false")
	}
}

func TestParseCLIArgs_MessageMissingValue(t *testing.T) {
	// When -m is last, there's no value to consume
	msg, _, _, _ := parseCLIArgs([]string{"-m"})
	if msg != "" {
		t.Errorf("message = %q, want empty (no value to consume)", msg)
	}
}

func TestParseCLIArgs_SessionMissingValue(t *testing.T) {
	_, session, _, _ := parseCLIArgs([]string{"-s"})
	if session != "cli:default" {
		t.Errorf("session = %q, want default (no value to consume)", session)
	}
}

func TestParseCLIArgs_MessageWithSession(t *testing.T) {
	msg, session, _, _ := parseCLIArgs([]string{
		"-m", "query", "-s", "work-session",
	})
	if msg != "query" {
		t.Errorf("message = %q, want %q", msg, "query")
	}
	if session != "work-session" {
		t.Errorf("session = %q, want %q", session, "work-session")
	}
}

func TestParseCLIArgs_SessionWithMessage(t *testing.T) {
	msg, session, _, _ := parseCLIArgs([]string{
		"-s", "work-session", "-m", "query",
	})
	if msg != "query" {
		t.Errorf("message = %q, want %q", msg, "query")
	}
	if session != "work-session" {
		t.Errorf("session = %q, want %q", session, "work-session")
	}
}

func TestParseCLIArgs_DebugFlagMultipleTimes(t *testing.T) {
	_, _, debug, _ := parseCLIArgs([]string{"--debug", "-d"})
	if !debug {
		t.Error("debug = false, want true")
	}
}

func TestParseCLIArgs_OnlyFlagsNoArgs(t *testing.T) {
	msg, session, debug, _ := parseCLIArgs([]string{"-d", "-s", "cli:default"})
	if msg != "" {
		t.Errorf("message = %q, want %q", msg, "")
	}
	if session != "cli:default" {
		t.Errorf("session = %q, want %q", session, "cli:default")
	}
	if !debug {
		t.Error("debug = false, want true")
	}
}

func TestParseCLIArgs_EqualsSyntax(t *testing.T) {
	msg, session, _, _ := parseCLIArgs([]string{"--session=574f2fc5-3e50-4415-9e7d-aa70e4d4ab36", "--message=hello"})
	if session != "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36" {
		t.Errorf("session = %q, want %q", session, "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36")
	}
	if msg != "hello" {
		t.Errorf("msg = %q, want %q", msg, "hello")
	}

	msg2, session2, _, _ := parseCLIArgs([]string{"-s=test-session", "-m=world"})
	if session2 != "test-session" {
		t.Errorf("session = %q, want %q", session2, "test-session")
	}
	if msg2 != "world" {
		t.Errorf("msg = %q, want %q", msg2, "world")
	}
}
