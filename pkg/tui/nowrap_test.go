package tui

import (
	"bytes"
	"testing"
)

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

	restore()
	if got, want := buf.String(), "\x1b[?7l\x1b[?7h"; got != want {
		t.Fatalf("second restore changed the output: %q, want %q", got, want)
	}
}

func TestDisableAutowrapNilWriterIsSafe(t *testing.T) {
	restore := DisableAutowrap(nil)
	if restore == nil {
		t.Fatal("DisableAutowrap(nil) returned a nil restore")
	}
	restore()
	restore()
}
