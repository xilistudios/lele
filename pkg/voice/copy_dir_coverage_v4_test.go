package voice

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestTranscribe_CopyDirError exercises the io.Copy error branch in Transcribe.
// On Linux, reading from an open directory fd returns EISDIR, which makes
// io.Copy fail after the multipart part is created — driving the
// "failed to copy file content" error path.
func TestTranscribe_CopyDirError(t *testing.T) {
	tr := NewGroqTranscriber("sk-test")
	dir := t.TempDir()

	// The directory itself opens and stats fine; only reading its content
	// fails (EISDIR on Linux). We set an unreachable base so that if io.Copy
	// unexpectedly succeeds (non-Linux), the following HTTP call returns an
	// error too, keeping the test deterministic.
	tr.apiBase = "http://127.0.0.1:1"

	_, err := tr.Transcribe(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error when copying a directory as audio")
	}
	// The failure must come from the copy step (on Linux) or the HTTP dial.
	msg := err.Error()
	if !strings.Contains(msg, "failed to copy file content") && !strings.Contains(msg, "failed to send request") {
		t.Errorf("unexpected error: %v", msg)
	}
	_ = os.Remove(dir)
}