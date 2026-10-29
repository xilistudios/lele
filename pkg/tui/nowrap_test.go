package tui

import (
	"bytes"
	"testing"
)

// The two sequences must be written exactly and only once each: a duplicated
// restore is harmless but a missing one leaves the user's shell without
// autowrap, which looks like a broken terminal.
func TestDisableAutowrapWritesAndRestoresTheMode(t *testing.T) {
	var buf bytes.Buffer
	restore := DisableAutowrap(&buf)
	if got := buf.String(); got != "\x1b[?7l" {
		t.Fatalf("after DisableAutowrap = %q, want %q", got, "\x1b[?7l")
	}

	restore()
	if got, want := buf.String(), "\x1b[?7l\x1b[?7h"; got != want {
		t.Fatalf("after restore = %q, want %q", got, want)
	}

	// Idempotent: exit paths (ctrl+c, /quit, render error) may all restore.
	restore()
	if got, want := buf.String(), "\x1b[?7l\x1b[?7h"; got != want {
		t.Fatalf("second restore changed the output: %q, want %q", got, want)
	}
}

// A nil writer must not panic: the TUI can be started with the output already
// gone (closed pipe), and the restore path still runs from defers.
func TestDisableAutowrapNilWriterIsSafe(t *testing.T) {
	restore := DisableAutowrap(nil)
	if restore == nil {
		t.Fatal("DisableAutowrap(nil) returned a nil restore")
	}
	restore()
	restore()
}
