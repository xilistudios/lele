package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO5WkR0AAAAASUVORK5CYII="

func writeTinyPNG(t *testing.T, dir string) string {
	t.Helper()

	data, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}

	path := filepath.Join(dir, "tiny.png")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write png: %v", err)
	}

	return path
}

func TestReadImageTool_Execute_Success(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := writeTinyPNG(t, tmpDir)

	tool := NewReadImageTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":   imagePath,
		"prompt": "Describe this image",
		"detail": "high",
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	if !result.Silent {
		t.Fatalf("expected silent result")
	}
	if len(result.ContextMessages) != 1 {
		t.Fatalf("ContextMessages len = %d, want 1", len(result.ContextMessages))
	}

	msg := result.ContextMessages[0]
	if msg.Role != "user" {
		t.Fatalf("role = %q, want user", msg.Role)
	}
	if len(msg.ContentParts) != 2 {
		t.Fatalf("ContentParts len = %d, want 2", len(msg.ContentParts))
	}
	if msg.ContentParts[0].Text != "Describe this image" {
		t.Fatalf("text part = %q, want Describe this image", msg.ContentParts[0].Text)
	}
	if msg.ContentParts[1].ImageURL == nil {
		t.Fatal("expected image_url part")
	}
	if !strings.HasPrefix(msg.ContentParts[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("image url prefix = %q", msg.ContentParts[1].ImageURL.URL[:32])
	}
	if msg.ContentParts[1].ImageURL.Detail != "high" {
		t.Fatalf("detail = %q, want high", msg.ContentParts[1].ImageURL.Detail)
	}
}

func TestReadImageTool_Execute_UnsupportedType(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := NewReadImageTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": path})
	if !result.IsError {
		t.Fatal("expected error for unsupported type")
	}
	if !strings.Contains(result.ForLLM, "unsupported image type") {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
}
