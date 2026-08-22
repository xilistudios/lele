// Lele - coverage tests for the io.Copy error branch in Transcribe.
// When the audio "file" is actually a directory, os.Open and Stat succeed but
// io.Copy fails reading directory contents, exercising the copy-error path.
// License: MIT

package voice

import (
	"context"
	"strings"
	"testing"
)

// TestTranscribe_CopyError_Directory exercises the io.Copy error branch by
// passing a directory as the audio file. On Linux, os.Open(dir) and
// dir.Stat() succeed, but io.Copy fails with EISDIR when reading the
// directory's contents via CreateFormFile's returned writer.
func TestTranscribe_CopyError_Directory(t *testing.T) {
	tr := NewGroqTranscriber("sk-test")

	_, err := tr.Transcribe(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error copying from a directory as audio file")
	}
	if !strings.Contains(err.Error(), "failed to copy file content") {
		t.Errorf("expected 'failed to copy file content' error, got: %v", err)
	}
}