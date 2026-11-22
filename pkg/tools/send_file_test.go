package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSendFileTool_AbsolutePathBlockedWhenRestricted(t *testing.T) {
	workspace := t.TempDir()

	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewSendFileTool()
	tool.SetWorkspaceRestrictions(workspace, true)
	tool.SetSendCallback(func(channel, chatID string, payload SendFilePayload) error {
		return nil
	})

	result := tool.Execute(context.Background(), map[string]interface{}{
		"file_paths": []interface{}{outsideFile},
		"channel":    "test",
		"chat_id":    "1",
	})

	if !result.IsError {
		t.Errorf("Expected absolute path outside workspace to be blocked")
	}
	if !strings.Contains(result.ForLLM, "access denied") {
		t.Errorf("Expected 'access denied' message, got: %s", result.ForLLM)
	}
}

func TestSendFileTool_EtcPasswdBlockedWhenRestricted(t *testing.T) {
	tool := NewSendFileTool()
	tool.SetWorkspaceRestrictions(t.TempDir(), true)
	tool.SetSendCallback(func(channel, chatID string, payload SendFilePayload) error {
		return nil
	})

	result := tool.Execute(context.Background(), map[string]interface{}{
		"file_paths": []interface{}{"/etc/passwd"},
		"channel":    "test",
		"chat_id":    "1",
	})

	if !result.IsError {
		t.Errorf("Expected /etc/passwd to be blocked when restrict=true")
	}
}

func TestSendFileTool_PathInsideWorkspaceAllowed(t *testing.T) {
	workspace := t.TempDir()
	okFile := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(okFile, []byte("report"), 0644); err != nil {
		t.Fatal(err)
	}

	var sent []string
	tool := NewSendFileTool()
	tool.SetWorkspaceRestrictions(workspace, true)
	tool.SetSendCallback(func(channel, chatID string, payload SendFilePayload) error {
		for _, a := range payload.Attachments {
			sent = append(sent, a.Path)
		}
		return nil
	})

	result := tool.Execute(context.Background(), map[string]interface{}{
		"file_paths": []interface{}{okFile},
		"channel":    "test",
		"chat_id":    "1",
	})

	if result.IsError {
		t.Errorf("Expected workspace file to be allowed, got error: %s", result.ForLLM)
	}
	if len(sent) != 1 || sent[0] != okFile {
		t.Errorf("Expected attachment %q, got %v", okFile, sent)
	}
}

func TestSendFileTool_NoRestrictionAllowsOutsidePaths(t *testing.T) {
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "file.txt")
	if err := os.WriteFile(outsideFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewSendFileTool()
	tool.SetSendCallback(func(channel, chatID string, payload SendFilePayload) error {
		return nil
	})

	result := tool.Execute(context.Background(), map[string]interface{}{
		"file_paths": []interface{}{outsideFile},
		"channel":    "test",
		"chat_id":    "1",
	})

	if result.IsError {
		t.Errorf("Expected unrestricted tool to allow outside path, got error: %s", result.ForLLM)
	}
}

func TestSendFileTool_RelativePathTraversalBlocked(t *testing.T) {
	workspace := t.TempDir()

	tool := NewSendFileTool()
	tool.SetWorkspaceRestrictions(workspace, true)
	tool.SetSendCallback(func(channel, chatID string, payload SendFilePayload) error {
		return nil
	})

	result := tool.Execute(context.Background(), map[string]interface{}{
		"file_paths": []interface{}{"../../../etc/passwd"},
		"channel":    "test",
		"chat_id":    "1",
	})

	if !result.IsError {
		t.Errorf("Expected relative traversal to be blocked when restrict=true")
	}
}
