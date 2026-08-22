package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadImageTool_Execute_defaultPrompt verifies default prompt and detail
// when args omit them.
func TestReadImageTool_Execute_defaultPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := writeTinyPNG(t, tmpDir)

	tool := NewReadImageTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": imagePath,
	})

	if result.IsError {
		t.Fatalf("expected success, got: %s", result.ForLLM)
	}
	if len(result.ContextMessages) != 1 {
		t.Fatalf("ContextMessages len = %d, want 1", len(result.ContextMessages))
	}
	msg := result.ContextMessages[0]
	if !strings.Contains(msg.ContentParts[0].Text, "Analyze the image at") {
		t.Fatalf("default prompt = %q", msg.ContentParts[0].Text)
	}
	if msg.ContentParts[1].ImageURL.Detail != "auto" {
		t.Fatalf("default detail = %q, want auto", msg.ContentParts[1].ImageURL.Detail)
	}
}

// TestReadImageTool_Execute_invalidDetail verifies invalid detail errors.
func TestReadImageTool_Execute_invalidDetail(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := writeTinyPNG(t, tmpDir)

	tool := NewReadImageTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":   imagePath,
		"detail": "ultra",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for invalid detail")
	}
	if !strings.Contains(result.ForLLM, "detail must be one of") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestReadImageTool_Execute_missingPath verifies missing path errors.
func TestReadImageTool_Execute_missingPath(t *testing.T) {
	tool := NewReadImageTool("", false)
	result := tool.Execute(context.Background(), map[string]interface{}{})
	if result == nil || !result.IsError {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(result.ForLLM, "path is required") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestReadImageTool_Execute_emptyPath verifies whitespace path errors.
func TestReadImageTool_Execute_emptyPath(t *testing.T) {
	tool := NewReadImageTool("", false)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": "   "})
	if result == nil || !result.IsError {
		t.Fatal("expected error for whitespace path")
	}
}

// TestReadImageTool_Execute_isDirectory verifies directory rejection.
func TestReadImageTool_Execute_isDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewReadImageTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": tmpDir})
	if result == nil || !result.IsError {
		t.Fatal("expected error for directory path")
	}
	if !strings.Contains(result.ForLLM, "not a directory") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestReadImageTool_Execute_nonexistent verifies stat failure path.
func TestReadImageTool_Execute_nonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewReadImageTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": filepath.Join(tmpDir, "missing.png"),
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(result.ForLLM, "failed to stat image") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestReadImageTool_Execute_outsideWorkspace verifies path restriction.
func TestReadImageTool_Execute_outsideWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	otherDir := t.TempDir()
	p := filepath.Join(otherDir, "x.png")
	os.WriteFile(p, []byte("not an image"), 0644)

	tool := NewReadImageTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": p})
	if result == nil || !result.IsError {
		t.Fatal("expected error for outside workspace")
	}
}

// TestDetectImageMIME verifies detection via content sniffing.
func TestDetectImageMIME(t *testing.T) {
	tests := []struct {
		name string
		path string
		data []byte
		want string
	}{
		{"png sniff", "x", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, "image/png"},
		{"jpeg sniff", "x", []byte{0xFF, 0xD8, 0xFF}, "image/jpeg"},
		{"empty data ext png", "photo.png", nil, "image/png"},
		{"empty data unknown ext", "file.xyz", nil, "application/octet-stream"},
		{"unsupported content text", "x", []byte("plain text here"), "application/octet-stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectImageMIME(tt.path, tt.data)
			if got != tt.want {
				t.Fatalf("detectImageMIME = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIsSupportedImageMIME table test.
func TestIsSupportedImageMIME(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"image/png", true},
		{"image/jpeg", true},
		{"image/gif", true},
		{"image/webp", true},
		{"Image/PNG", true},
		{"application/pdf", false},
		{"text/plain", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isSupportedImageMIME(tt.in); got != tt.want {
			t.Errorf("isSupportedImageMIME(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestAsString verifies asString casts safely.
func TestAsString(t *testing.T) {
	if got := asString("hello"); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if got := asString(123); got != "" {
		t.Fatalf("expected empty for non-string, got %q", got)
	}
	if got := asString(nil); got != "" {
		t.Fatalf("expected empty for nil, got %q", got)
	}
}

// TestReadImageTool_Execute_largeImage verifies the size limit path with a
// synthetic huge file using the 20MB constant indirectly. We can't easily
// create a 20MB valid image, but a file with any content is fine since size is
// checked before MIME validation.
func TestReadImageTool_Execute_tooLarge(t *testing.T) {
	tmpDir := t.TempDir()
	big := filepath.Join(tmpDir, "big.png")
	if err := os.WriteFile(big, make([]byte, maxImageReadSize+1), 0644); err != nil {
		t.Fatalf("write big file: %v", err)
	}
	tool := NewReadImageTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": big})
	if result == nil || !result.IsError {
		t.Fatal("expected error for oversized image")
	}
	if !strings.Contains(result.ForLLM, "too large") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestReadImageTool_Execute_promptTypeAsString verifies non-string prompt
// falls back to default (prompt passed as int).
func TestReadImageTool_Execute_promptNonString(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := writeTinyPNG(t, tmpDir)

	tool := NewReadImageTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":   imagePath,
		"prompt": 42,
	})
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.ForLLM)
	}
}
