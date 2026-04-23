package main

import (
	"testing"
)

// parseCLIArgs tests the argument parsing logic extracted from agentCmd.
// This isolates the pure parsing logic from I/O and agent setup.

func TestParseCLIArgs_Default(t *testing.T) {
	// Simulate: lele agent
	msg, session, _ := parseCLIArgs([]string{})
	if msg != "" {
		t.Errorf("message = %q, want %q", msg, "")
	}
	if session != "cli:default" {
		t.Errorf("session = %q, want %q", session, "cli:default")
	}
}

func TestParseCLIArgs_MessageShort(t *testing.T) {
	msg, session, _ := parseCLIArgs([]string{"-m", "hello world"})
	if msg != "hello world" {
		t.Errorf("message = %q, want %q", msg, "hello world")
	}
	if session != "cli:default" {
		t.Errorf("session = %q, want %q", session, "cli:default")
	}
}

func TestParseCLIArgs_MessageLong(t *testing.T) {
	msg, session, _ := parseCLIArgs([]string{"--message", "test message"})
	if msg != "test message" {
		t.Errorf("message = %q, want %q", msg, "test message")
	}
	if session != "cli:default" {
		t.Errorf("session = %q, want %q", session, "cli:default")
	}
}

func TestParseCLIArgs_SessionShort(t *testing.T) {
	msg, session, _ := parseCLIArgs([]string{"-s", "my-session"})
	if msg != "" {
		t.Errorf("message = %q, want %q", msg, "")
	}
	if session != "my-session" {
		t.Errorf("session = %q, want %q", session, "my-session")
	}
}

func TestParseCLIArgs_SessionLong(t *testing.T) {
	msg, session, _ := parseCLIArgs([]string{"--session", "custom-session"})
	if msg != "" {
		t.Errorf("message = %q, want %q", msg, "")
	}
	if session != "custom-session" {
		t.Errorf("session = %q, want %q", session, "custom-session")
	}
}

func TestParseCLIArgs_DebugFlag(t *testing.T) {
	_, _, debug := parseCLIArgs([]string{"--debug"})
	if !debug {
		t.Error("debug = false, want true")
	}
}

func TestParseCLIArgs_DebugShort(t *testing.T) {
	_, _, debug := parseCLIArgs([]string{"-d"})
	if !debug {
		t.Error("debug = false, want true")
	}
}

func TestParseCLIArgs_AllFlags(t *testing.T) {
	msg, session, debug := parseCLIArgs([]string{
		"-d", "-m", "hello", "-s", "test-sess",
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
}

func TestParseCLIArgs_UnknownFlagIgnored(t *testing.T) {
	msg, session, debug := parseCLIArgs([]string{
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
}

func TestParseCLIArgs_MessageMissingValue(t *testing.T) {
	// When -m is last, there's no value to consume
	msg, _, _ := parseCLIArgs([]string{"-m"})
	if msg != "" {
		t.Errorf("message = %q, want empty (no value to consume)", msg)
	}
}

func TestParseCLIArgs_SessionMissingValue(t *testing.T) {
	_, session, _ := parseCLIArgs([]string{"-s"})
	if session != "cli:default" {
		t.Errorf("session = %q, want default (no value to consume)", session)
	}
}

func TestParseCLIArgs_MessageWithSession(t *testing.T) {
	msg, session, _ := parseCLIArgs([]string{
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
	msg, session, _ := parseCLIArgs([]string{
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
	_, _, debug := parseCLIArgs([]string{"--debug", "-d"})
	if !debug {
		t.Error("debug = false, want true")
	}
}

func TestParseCLIArgs_OnlyFlagsNoArgs(t *testing.T) {
	msg, session, debug := parseCLIArgs([]string{"-d", "-s", "cli:default"})
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
