package session

import (
	"encoding/json"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// TestSQLite_StreamingFinalization_ToolCallsPersisted is a regression test for
// the WebUI "Action" label bug: when a streamed assistant message is replaced
// in-place with its final version (carrying tool_calls) and tool results are
// appended in the same Save, the in-place update used to be dropped because
// saveIncrementalUnlocked only re-persisted the LAST message. The stale row
// (streaming=true, no tool_calls) stayed in SQLite and the WebUI rendered the
// generic "Action" label instead of the tool name.
func TestSQLite_StreamingFinalization_ToolCallsPersisted(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "webui:toolcalls-1"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "run a command")
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	// Simulate streaming: partial assistant content persisted mid-stream.
	sm.AppendAssistantChunk(key, "Let me run that.")
	sm.FinalizeAssistantMessage(key)

	// Now the provider returns the final message WITH tool_calls; AddFullMessage
	// replaces the streaming message in-place.
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

	// Tool result appended in the SAME save window — this is what used to mask
	// the in-place update (modified msg no longer the last one).
	sm.AddFullMessage(key, providers.Message{
		Role:       "tool",
		ToolCallID: "call_1",
		Content:    "file1\nfile2",
	})

	if err := sm.Save(key); err != nil {
		t.Fatalf("save after finalization: %v", err)
	}

	// Verify the persisted assistant row carries tool_calls and streaming=false.
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

// TestSQLite_StreamingFinalization_CompactionSameSave verifies the second loss
// path: compaction (ExcludeOldMessagesFromContext) running in the same Save
// call as a pending streaming finalization must not drop the tool_calls update.
func TestSQLite_StreamingFinalization_CompactionSameSave(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "webui:toolcalls-2"
	sm.GetOrCreate(key)
	// Seed enough messages so ExcludeOldMessagesFromContext has something to mark.
	sm.AddMessage(key, "user", "task")
	for i := 0; i < 8; i++ {
		sm.AddMessage(key, "assistant", "filler")
		sm.AddMessage(key, "user", "more")
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	// Stream + finalize with tool_calls.
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

	// Compaction in the same save window.
	sm.ExcludeOldMessagesFromContext(key, 6)

	if err := sm.Save(key); err != nil {
		t.Fatalf("save with compaction: %v", err)
	}

	repo := s.Sessions()
	msgJSONs, err := repo.LoadMessages(key)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}

	// Find the assistant message with tool_calls — it must be persisted.
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
