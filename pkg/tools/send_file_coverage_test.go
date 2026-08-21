package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSendFileTool_Execute_ContextFallback verifies channel/chatID resolution
// from the ToolContext when no defaults or args are provided.
func TestSendFileTool_Execute_ContextFallback(t *testing.T) {
	ctx := context.Background()
	ctx = WithToolContext(ctx, "ctx-channel", "ctx-chat")
	tool := NewSendFileTool()
	var sentChannel, sentChatID string
	tool.SetSendCallback(func(channel, chatID string, payload SendFilePayload) error {
		sentChannel = channel
		sentChatID = chatID
		return nil
	})
	res := tool.Execute(ctx, map[string]interface{}{"content": "hello"})
	if res.IsError {
		t.Fatalf("err: %s", res.ForLLM)
	}
	if sentChannel != "ctx-channel" || sentChatID != "ctx-chat" {
		t.Fatalf("sent = %s:%s, want ctx-channel:ctx-chat", sentChannel, sentChatID)
	}
}

// TestSendFileTool_Execute_NilCallback verifies the not-configured branch.
func TestSendFileTool_Execute_NilCallback(t *testing.T) {
	tool := NewSendFileTool()
	tool.SetContext("ch", "cid")
	res := tool.Execute(context.Background(), map[string]interface{}{"content": "hi"})
	if !res.IsError || !strings.Contains(res.ForLLM, "not configured") {
		t.Fatalf("res = %+v", res)
	}
}

// TestSendFileTool_Execute_NoContentFiles verifies the empty-content guard.
func TestSendFileTool_Execute_NoContentFiles(t *testing.T) {
	tool := NewSendFileTool()
	tool.SetContext("ch", "cid")
	res := tool.Execute(context.Background(), map[string]interface{}{})
	if !res.IsError || !strings.Contains(res.ForLLM, "content or file_paths") {
		t.Fatalf("res = %+v", res)
	}
}

// TestParseSendFileAttachments_Coverage exercises the parse error branches.
func TestParseSendFileAttachments_Coverage(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	os.WriteFile(file, []byte("x"), 0600)

	// nil => nil,nil
	a, err := parseSendFileAttachments(nil)
	if err != nil || a != nil {
		t.Fatalf("nil case: a=%v err=%v", a, err)
	}

	// []string path
	a, err = parseSendFileAttachments([]string{file})
	if err != nil || len(a) != 1 {
		t.Fatalf("[]string case: err=%v len=%d", err, len(a))
	}

	// []interface{} with non-string element
	_, err = parseSendFileAttachments([]interface{}{float64(123)})
	if err == nil || !strings.Contains(err.Error(), "only strings") {
		t.Fatalf("non-string element err=%v", err)
	}

	// default (not a slice)
	_, err = parseSendFileAttachments("not-a-slice")
	if err == nil || !strings.Contains(err.Error(), "array of strings") {
		t.Fatalf("default err=%v", err)
	}

	// nonexistent file
	_, err = parseSendFileAttachments([]interface{}{filepath.Join(dir, "missing.txt")})
	if err == nil || !strings.Contains(err.Error(), "cannot access") {
		t.Fatalf("missing file err=%v", err)
	}

	// directory path
	_, err = parseSendFileAttachments([]interface{}{dir})
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("directory err=%v", err)
	}
}

// TestSendFileTool_Execute_AttachmentParseError verifies the early-return on
// attachment parse failure before the content check.
func TestSendFileTool_Execute_AttachmentParseError(t *testing.T) {
	tool := NewSendFileTool()
	tool.SetContext("ch", "cid")
	res := tool.Execute(context.Background(), map[string]interface{}{
		"content":    "x",
		"file_paths": "not-an-array",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "array of strings") {
		t.Fatalf("res = %+v", res)
	}
}

// TestMimeTypeForPath covers the extension/no-extension/unknown branches.
func TestMimeTypeForPath(t *testing.T) {
	if got := mimeTypeForPath("/tmp/noext"); got != "application/octet-stream" {
		t.Fatalf("no-ext = %q", got)
	}
	if got := mimeTypeForPath("/tmp/file.txt"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("txt = %q", got)
	}
	// Uppercase extension is lowercased.
	if got := mimeTypeForPath("/tmp/FILE.PNG"); got == "" {
		t.Fatal("png ext should map")
	}
	// Unknown extension falls back.
	if got := mimeTypeForPath("/tmp/file.zzznotreal"); got != "application/octet-stream" {
		t.Fatalf("unknown ext = %q", got)
	}
}

// TestSendFileTool_Execute_FilePathsStringArray verifies the []string typed
// file_paths input.
func TestSendFileTool_Execute_FilePathsStringArray(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "r.md")
	os.WriteFile(file, []byte("# hi"), 0600)
	tool := NewSendFileTool()
	tool.SetContext("ch", "cid")
	var payload SendFilePayload
	tool.SetSendCallback(func(channel, chatID string, p SendFilePayload) error {
		payload = p
		return nil
	})
	res := tool.Execute(context.Background(), map[string]interface{}{"file_paths": []string{file}})
	if res.IsError {
		t.Fatalf("err: %s", res.ForLLM)
	}
	if len(payload.Attachments) != 1 {
		t.Fatalf("attachments = %d", len(payload.Attachments))
	}
	if payload.Attachments[0].Kind != "file" {
		t.Fatalf("kind = %q", payload.Attachments[0].Kind)
	}
}