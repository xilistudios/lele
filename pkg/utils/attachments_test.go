package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
)

func TestPersistAttachmentsToWorkspace_MovesTemporaryFile(t *testing.T) {
	workspace := t.TempDir()
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "input.txt")
	if err := os.WriteFile(sourcePath, []byte("secret-content"), 0600); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	attachments, err := PersistAttachmentsToWorkspace(workspace, []bus.FileAttachment{{
		Name:      "input.txt",
		Path:      sourcePath,
		Kind:      "file",
		Temporary: true,
	}})
	if err != nil {
		t.Fatalf("PersistAttachmentsToWorkspace returned error: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].Temporary {
		t.Fatalf("expected persisted attachment to be non-temporary")
	}
	if !strings.HasPrefix(attachments[0].Path, filepath.Join(workspace, "attachments")) {
		t.Fatalf("expected attachment path inside workspace, got %q", attachments[0].Path)
	}
	if _, err := os.Stat(attachments[0].Path); err != nil {
		t.Fatalf("expected persisted file to exist: %v", err)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("expected original temp file to be moved away, stat err=%v", err)
	}

	contextText := BuildAttachmentContext(attachments)
	if !strings.Contains(contextText, attachments[0].Path) {
		t.Fatalf("expected attachment context to include stored path, got %q", contextText)
	}
	if strings.Contains(contextText, "secret-content") {
		t.Fatalf("attachment context should not inline file contents")
	}
}
