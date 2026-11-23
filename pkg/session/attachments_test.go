package session

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func att(name, path string) providers.MessageAttachment {
	return providers.MessageAttachment{Name: name, Path: path, MIMEType: "text/plain", Kind: "file"}
}

func attachmentRows(hist []providers.Message) []providers.Message {
	rows := make([]providers.Message, 0, len(hist))
	for _, msg := range hist {
		if msg.Role == "tool" && strings.HasPrefix(msg.ToolCallID, "file:") {
			rows = append(rows, msg)
		}
	}
	return rows
}

func TestAddAttachmentMessage_AppendsToolRowAfterToolCallsAssistant(t *testing.T) {
	sm := NewSessionManager()
	sm.SetSessionRepo(newTestStore(t).Sessions())

	key := "native:attach-1"
	sm.AddMessage(key, "user", "send me the file")
	sm.AddFullMessage(key, providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{{
			ID:   "call_1",
			Type: "function",
			Name: "send_file",
		}},
	})

	sm.AddAttachmentMessage(key, "here you go", []providers.MessageAttachment{
		att("report.pdf", "/x/attachments/aa_report.pdf"),
	})

	hist := sm.GetHistory(key)
	if len(hist) != 3 {
		t.Fatalf("history len = %d, want 3", len(hist))
	}
	if len(hist[1].Attachments) != 0 {
		t.Errorf("assistant tool_calls row got %d attachments, want 0", len(hist[1].Attachments))
	}

	row := hist[2]
	if row.Role != "tool" {
		t.Fatalf("display row role = %q, want tool", row.Role)
	}
	if !strings.HasPrefix(row.ToolCallID, "file:") {
		t.Errorf("display row tool_call_id = %q, want file: prefix", row.ToolCallID)
	}
	if !row.ExcludeFromContext {
		t.Error("display row must be excluded from the model context")
	}
	if row.Content != "here you go" {
		t.Errorf("display row content = %q, want 'here you go'", row.Content)
	}
	if len(row.Attachments) != 1 {
		t.Fatalf("display row attachments = %d, want 1", len(row.Attachments))
	}
	if row.Attachments[0].Name != "report.pdf" || row.Attachments[0].Path != "/x/attachments/aa_report.pdf" {
		t.Errorf("display row attachment = %+v, want report.pdf", row.Attachments[0])
	}
}

func TestAddAttachmentMessage_PersistsAcrossReload(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "native:attach-2"
	sm.AddMessage(key, "user", "send it")
	sm.AddAttachmentMessage(key, "done", []providers.MessageAttachment{att("f.bin", "/x/f.bin")})

	reloaded := NewSessionManager()
	reloaded.SetSessionRepo(s.Sessions())
	hist := reloaded.GetHistory(key)
	if len(hist) != 2 {
		t.Fatalf("reloaded history len = %d, want 2", len(hist))
	}

	rows := attachmentRows(hist)
	if len(rows) != 1 {
		t.Fatalf("reloaded file: rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if len(row.Attachments) != 1 || row.Attachments[0].Path != "/x/f.bin" {
		t.Fatalf("reloaded attachments = %+v, want one with path /x/f.bin", row.Attachments)
	}
	if !row.ExcludeFromContext {
		t.Error("reloaded row lost exclude_from_context")
	}
	if row.ToolCallID != attachmentRows(hist)[0].ToolCallID || row.ToolCallID == "" {
		t.Errorf("reloaded row lost its tool_call_id: %q", row.ToolCallID)
	}
}

func TestAddAttachmentMessage_RedeliveryIsIdempotent(t *testing.T) {
	sm := NewSessionManager()
	sm.SetSessionRepo(newTestStore(t).Sessions())

	key := "native:attach-3"
	sm.AddMessage(key, "assistant", "ok")

	once := []providers.MessageAttachment{att("a.txt", "/x/a.txt")}
	sm.AddAttachmentMessage(key, "", once)
	sm.AddAttachmentMessage(key, "caption", once)

	hist := sm.GetHistory(key)
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2 (redelivery must not append a new row)", len(hist))
	}
	row := hist[1]
	if row.Content != "caption" {
		t.Errorf("row content = %q, want the redelivered caption", row.Content)
	}
	if len(row.Attachments) != 1 {
		t.Errorf("row attachments = %d, want 1 (deduped by path)", len(row.Attachments))
	}
}

func TestAddAttachmentMessage_SecondDispatchGetsItsOwnRow(t *testing.T) {
	sm := NewSessionManager()
	sm.SetSessionRepo(newTestStore(t).Sessions())

	key := "native:attach-4"
	sm.AddMessage(key, "assistant", "ok")

	sm.AddAttachmentMessage(key, "first", []providers.MessageAttachment{att("a.txt", "/x/a.txt")})
	sm.AddAttachmentMessage(key, "second", []providers.MessageAttachment{
		att("a.txt", "/x/a.txt"),
		att("b.txt", "/x/b.txt"),
	})

	hist := sm.GetHistory(key)
	rows := attachmentRows(hist)
	if len(rows) != 2 {
		t.Fatalf("file: rows = %d, want 2 (one per dispatch)", len(rows))
	}
	if rows[0].Content != "first" || rows[1].Content != "second" {
		t.Errorf("row contents = %q, %q, want first, second", rows[0].Content, rows[1].Content)
	}
	if rows[0].ToolCallID == rows[1].ToolCallID {
		t.Errorf("distinct dispatches share tool_call_id %q", rows[0].ToolCallID)
	}
	if len(rows[1].Attachments) != 2 {
		t.Errorf("second row attachments = %d, want 2", len(rows[1].Attachments))
	}
}

func TestAddAttachmentMessage_InsertsBeforeStreamingDraft(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "native:attach-5"
	sm.AddMessage(key, "user", "draw it")
	sm.AppendAssistantChunk(key, "working on it")

	sm.AddAttachmentMessage(key, "chart", []providers.MessageAttachment{att("chart.png", "/x/chart.png")})

	hist := sm.GetHistory(key)
	if len(hist) != 3 {
		t.Fatalf("history len = %d, want 3", len(hist))
	}
	row := hist[1]
	if row.Role != "tool" || !strings.HasPrefix(row.ToolCallID, "file:") {
		t.Fatalf("row before the draft = %+v, want a file: tool row", row)
	}
	tail := hist[2]
	if tail.Role != "assistant" || !tail.Streaming {
		t.Fatalf("tail = %+v, want the streaming draft", tail)
	}
	if tail.Content != "working on it" {
		t.Errorf("draft content = %q, want 'working on it'", tail.Content)
	}

	sm.SealStreamingTail(key)
	sealed := sm.GetHistory(key)
	if tail := sealed[len(sealed)-1]; tail.Streaming {
		t.Error("draft is no longer sealable after the insert")
	}
	if len(sealed) != 3 {
		t.Errorf("sealing changed the history len to %d, want 3", len(sealed))
	}

	reloaded := NewSessionManager()
	reloaded.SetSessionRepo(s.Sessions())
	stored := reloaded.GetHistory(key)
	if len(stored) != 3 {
		t.Fatalf("reloaded history len = %d, want 3: %+v", len(stored), stored)
	}
	if stored[1].Role != "tool" || !strings.HasPrefix(stored[1].ToolCallID, "file:") {
		t.Fatalf("reloaded row order = %+v, want the file row before the assistant reply", stored)
	}
	if stored[2].Role != "assistant" {
		t.Fatalf("reloaded tail = %+v, want the assistant reply", stored[2])
	}
}

func TestAddAttachmentMessage_EmptyInputNoop(t *testing.T) {
	sm := NewSessionManager()
	sm.SetSessionRepo(newTestStore(t).Sessions())

	key := "native:attach-6"
	sm.AddMessage(key, "assistant", "hola")

	sm.AddAttachmentMessage(key, "nothing", nil)
	sm.AddAttachmentMessage(key, "nothing", []providers.MessageAttachment{})

	hist := sm.GetHistory(key)
	if len(hist) != 1 {
		t.Fatalf("history len = %d, want 1", len(hist))
	}
	if len(hist[0].Attachments) != 0 {
		t.Fatalf("empty call must not create attachments: %+v", hist[0].Attachments)
	}
}

func TestAddAttachmentMessage_UnknownSessionNoop(t *testing.T) {
	sm := NewSessionManager()
	sm.SetSessionRepo(newTestStore(t).Sessions())

	sm.AddAttachmentMessage("native:does-not-exist", "file", []providers.MessageAttachment{att("a.txt", "/x/a.txt")})

	if hist := sm.GetHistory("native:does-not-exist"); len(hist) != 0 {
		t.Fatalf("unknown session gained %d messages", len(hist))
	}
}
