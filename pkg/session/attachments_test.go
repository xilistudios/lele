package session

// Tests for AttachFilesToLastAssistant: persistence of send_file attachments
// on the last assistant message of a session (WebUI file download feature).

import (
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func att(name, path string) providers.MessageAttachment {
	return providers.MessageAttachment{Name: name, Path: path, MIMEType: "text/plain", Kind: "file"}
}

func TestAttachFilesToLastAssistant_AppendsToLastAssistant(t *testing.T) {
	sm := NewSessionManager()
	sm.SetStore(newTestStore(t))

	key := "native:attach-1"
	sm.AddMessage(key, "user", "hola")
	sm.AddMessage(key, "assistant", "aqui tienes")
	sm.AddMessage(key, "user", "gracias")
	sm.AddMessage(key, "assistant", "ok")

	sm.AttachFilesToLastAssistant(key, []providers.MessageAttachment{
		att("report.pdf", "/home/x/.lele/tmp/attachments/aa_report.pdf"),
	})

	hist := sm.GetHistory(key)
	if len(hist) != 4 {
		t.Fatalf("history len = %d, want 4", len(hist))
	}
	last := hist[3]
	if last.Role != "assistant" {
		t.Fatalf("last role = %q, want assistant", last.Role)
	}
	if len(last.Attachments) != 1 {
		t.Fatalf("last attachments = %d, want 1", len(last.Attachments))
	}
	if got := last.Attachments[0].Name; got != "report.pdf" {
		t.Errorf("attachment name = %q, want report.pdf", got)
	}
	// The FIRST assistant message must be untouched.
	if len(hist[1].Attachments) != 0 {
		t.Errorf("first assistant got %d attachments, want 0", len(hist[1].Attachments))
	}
}

func TestAttachFilesToLastAssistant_DedupesByPath(t *testing.T) {
	sm := NewSessionManager()
	sm.SetStore(newTestStore(t))

	key := "native:attach-2"
	sm.AddMessage(key, "assistant", "hola")

	once := []providers.MessageAttachment{att("a.txt", "/x/a.txt")}
	sm.AttachFilesToLastAssistant(key, once)
	sm.AttachFilesToLastAssistant(key, once) // repeated message.complete
	sm.AttachFilesToLastAssistant(key, []providers.MessageAttachment{
		att("a.txt", "/x/a.txt"), // dup path, different name — still skipped
		att("b.txt", "/x/b.txt"), // new
	})

	hist := sm.GetHistory(key)
	if len(hist) != 1 {
		t.Fatalf("history len = %d, want 1", len(hist))
	}
	if len(hist[0].Attachments) != 2 {
		t.Fatalf("attachments = %d, want 2 (deduped by path)", len(hist[0].Attachments))
	}
	if hist[0].Attachments[0].Name != "a.txt" || hist[0].Attachments[1].Name != "b.txt" {
		t.Errorf("unexpected attachment order/names: %+v", hist[0].Attachments)
	}
}

func TestAttachFilesToLastAssistant_NoAssistantIsNoop(t *testing.T) {
	sm := NewSessionManager()
	sm.SetStore(newTestStore(t))

	key := "native:attach-3"
	sm.AddMessage(key, "user", "solo usuario")

	sm.AttachFilesToLastAssistant(key, []providers.MessageAttachment{att("a.txt", "/x/a.txt")})

	hist := sm.GetHistory(key)
	if len(hist) != 1 || len(hist[0].Attachments) != 0 {
		t.Fatalf("user-only session must not gain attachments, got %+v", hist)
	}

	// Unknown session: must not panic nor create one.
	sm.AttachFilesToLastAssistant("native:does-not-exist", []providers.MessageAttachment{att("a.txt", "/x/a.txt")})
}

func TestAttachFilesToLastAssistant_PersistsAcrossReload(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "native:attach-4"
	sm.AddMessage(key, "assistant", "hola")
	sm.AttachFilesToLastAssistant(key, []providers.MessageAttachment{att("f.bin", "/x/f.bin")})

	// Fresh manager over the same store → loads from SQLite/JSON rows.
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	hist := sm2.GetHistory(key)
	if len(hist) != 1 {
		t.Fatalf("reloaded history len = %d, want 1", len(hist))
	}
	if len(hist[0].Attachments) != 1 || hist[0].Attachments[0].Path != "/x/f.bin" {
		t.Fatalf("reloaded attachments = %+v, want one with path /x/f.bin", hist[0].Attachments)
	}
}

func TestAttachFilesToLastAssistant_EmptyInputNoop(t *testing.T) {
	sm := NewSessionManager()
	sm.SetStore(newTestStore(t))
	key := "native:attach-5"
	sm.AddMessage(key, "assistant", "hola")

	sm.AttachFilesToLastAssistant(key, nil)
	sm.AttachFilesToLastAssistant(key, []providers.MessageAttachment{})

	hist := sm.GetHistory(key)
	if len(hist[0].Attachments) != 0 {
		t.Fatalf("empty attach call must not create attachments: %+v", hist[0].Attachments)
	}
}
