package session

import (
	"encoding/json"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestSQLite_StreamingFinalization_ToolCallsPersisted(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "webui:toolcalls-1"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "run a command")
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	sm.AppendAssistantChunk(key, "Let me run that.")
	sm.FinalizeAssistantMessage(key)

	final := providers.Message{
		Role:    "assistant",
		Content: "Let me run that.",
		ToolCalls: []providers.ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: &providers.FunctionCall{
				Name:      "exec",
				Arguments: `{"command":"ls"}`,
			},
		}},
	}
	sm.AddFullMessage(key, final)

	sm.AddFullMessage(key, providers.Message{
		Role:       "tool",
		ToolCallID: "call_1",
		Content:    "file1\nfile2",
	})

	if err := sm.Save(key); err != nil {
		t.Fatalf("save after finalization: %v", err)
	}

	repo := s.Sessions()
	msgJSONs, err := repo.LoadMessages(key)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(msgJSONs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgJSONs))
	}

	var assistant providers.Message
	if err := json.Unmarshal([]byte(msgJSONs[1]), &assistant); err != nil {
		t.Fatalf("unmarshal assistant message: %v", err)
	}
	if assistant.Streaming {
		t.Error("persisted assistant message still marked streaming=true")
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("persisted assistant message lost tool_calls: got %d, want 1", len(assistant.ToolCalls))
	}
	if assistant.ToolCalls[0].Function == nil || assistant.ToolCalls[0].Function.Name != "exec" {
		t.Errorf("tool call name not persisted correctly: %+v", assistant.ToolCalls[0])
	}
}

func TestSQLite_StreamingFinalization_CompactionSameSave(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "webui:toolcalls-2"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "task")
	for i := 0; i < 8; i++ {
		sm.AddMessage(key, "assistant", "filler")
		sm.AddMessage(key, "user", "more")
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	sm.AppendAssistantChunk(key, "partial")
	sm.AddFullMessage(key, providers.Message{
		Role:    "assistant",
		Content: "partial",
		ToolCalls: []providers.ToolCall{{
			ID:       "call_9",
			Function: &providers.FunctionCall{Name: "read_file", Arguments: "{}"},
		}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", ToolCallID: "call_9", Content: "ok"})

	sm.ExcludeOldMessagesFromContext(key, 6)

	if err := sm.Save(key); err != nil {
		t.Fatalf("save with compaction: %v", err)
	}

	repo := s.Sessions()
	msgJSONs, err := repo.LoadMessages(key)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}

	found := false
	for _, raw := range msgJSONs {
		var m providers.Message
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			found = true
			if m.Streaming {
				t.Error("assistant with tool_calls still streaming=true")
			}
			if m.ToolCalls[0].Function == nil || m.ToolCalls[0].Function.Name != "read_file" {
				t.Errorf("wrong tool name persisted: %+v", m.ToolCalls[0])
			}
		}
	}
	if !found {
		t.Fatal("no assistant message with tool_calls found in SQLite — update was dropped")
	}
}
