// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestSanitizeToolMessages_OrphanedToolResultRemoved(t *testing.T) {
	// A tool_result whose ToolCallID has no matching tool_use anywhere in history
	// should be removed.
	history := []providers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "tool", Content: "some result", ToolCallID: "orphan_id_123"},
		{Role: "user", Content: "Continue"},
	}

	result := sanitizeToolMessages(history)

	if len(result) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(result))
	}
	if result[0].Role != "user" || result[0].Content != "Hello" {
		t.Errorf("Expected first message to be user/Hello, got %s/%s", result[0].Role, result[0].Content)
	}
	if result[1].Role != "assistant" || result[1].Content != "Hi there" {
		t.Errorf("Expected second message to be assistant/Hi there, got %s/%s", result[1].Role, result[1].Content)
	}
	if result[2].Role != "user" || result[2].Content != "Continue" {
		t.Errorf("Expected third message to be user/Continue, got %s/%s", result[2].Role, result[2].Content)
	}
}

func TestSanitizeToolMessages_MissingToolResultsGetPlaceholders(t *testing.T) {
	// Assistant message with 2 tool_use blocks, but only 1 tool_result follows.
	// A placeholder should be added for the missing one.
	history := []providers.Message{
		{Role: "user", Content: "Do two things"},
		{
			Role:    "assistant",
			Content: "I'll do two things",
			ToolCalls: []providers.ToolCall{
				{ID: "tc_1", Name: "read_file"},
				{ID: "tc_2", Name: "write_file"},
			},
		},
		{Role: "tool", Content: "file contents", ToolCallID: "tc_1"},
		{Role: "user", Content: "Thanks"},
	}

	result := sanitizeToolMessages(history)

	// user, assistant, tool (tc_1), placeholder tool (tc_2), user = 5 messages
	if len(result) != 5 {
		t.Fatalf("Expected 5 messages, got %d", len(result))
	}
	// Check assistant message is preserved
	if result[1].Role != "assistant" {
		t.Errorf("Expected message[1] to be assistant, got %s", result[1].Role)
	}
	if len(result[1].ToolCalls) != 2 {
		t.Errorf("Expected 2 tool calls in assistant message, got %d", len(result[1].ToolCalls))
	}
	// Check existing tool result
	if result[2].Role != "tool" || result[2].ToolCallID != "tc_1" {
		t.Errorf("Expected message[2] to be tool/tc_1, got %s/%s", result[2].Role, result[2].ToolCallID)
	}
	// Check placeholder
	if result[3].Role != "tool" {
		t.Errorf("Expected message[3] to be tool (placeholder), got %s", result[3].Role)
	}
	if result[3].ToolCallID != "tc_2" {
		t.Errorf("Expected placeholder ToolCallID to be tc_2, got %s", result[3].ToolCallID)
	}
	if result[3].Content != "[Tool execution was cancelled]" {
		t.Errorf("Expected placeholder content to be '[Tool execution was cancelled]', got %s", result[3].Content)
	}
	// Check final user message
	if result[4].Role != "user" || result[4].Content != "Thanks" {
		t.Errorf("Expected message[4] to be user/Thanks, got %s/%s", result[4].Role, result[4].Content)
	}
}

func TestSanitizeToolMessages_AllToolResultsMissing_StripToolUse(t *testing.T) {
	// Assistant message with tool_use but NO tool_results follow.
	// Tool_use blocks should be stripped, but content preserved.
	history := []providers.Message{
		{Role: "user", Content: "Do something"},
		{
			Role:    "assistant",
			Content: "I'll do it",
			ToolCalls: []providers.ToolCall{
				{ID: "tc_miss_1", Name: "exec"},
			},
		},
		{Role: "user", Content: "Never mind"},
	}

	result := sanitizeToolMessages(history)

	// user, assistant (stripped), user = 3 messages
	if len(result) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(result))
	}
	// Assistant message should have no tool calls
	if len(result[1].ToolCalls) != 0 {
		t.Errorf("Expected 0 tool calls in assistant message, got %d", len(result[1].ToolCalls))
	}
	// But content should be preserved
	if result[1].Content != "I'll do it" {
		t.Errorf("Expected assistant content to be 'I'll do it', got %s", result[1].Content)
	}
}

func TestSanitizeToolMessages_AllToolResultsMissing_EmptyContent_RemovesMessage(t *testing.T) {
	// Assistant message with tool_use, no tool_results, AND empty content/reasoning.
	// The entire message should be removed.
	history := []providers.Message{
		{Role: "user", Content: "Do something"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []providers.ToolCall{
				{ID: "tc_empty_1", Name: "exec"},
			},
		},
		{Role: "user", Content: "Next question"},
	}

	result := sanitizeToolMessages(history)

	// user, user = 2 messages (assistant removed entirely)
	if len(result) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "user" || result[0].Content != "Do something" {
		t.Errorf("Expected message[0] to be user/Do something, got %s/%s", result[0].Role, result[0].Content)
	}
	if result[1].Role != "user" || result[1].Content != "Next question" {
		t.Errorf("Expected message[1] to be user/Next question, got %s/%s", result[1].Role, result[1].Content)
	}
}

func TestSanitizeToolMessages_NormalConversationUnchanged(t *testing.T) {
	// A normal conversation with properly paired tool_use and tool_result
	// should pass through unchanged.
	history := []providers.Message{
		{Role: "user", Content: "Read a file"},
		{
			Role:    "assistant",
			Content: "Let me read it",
			ToolCalls: []providers.ToolCall{
				{ID: "tc_norm_1", Name: "read_file"},
			},
		},
		{Role: "tool", Content: "file contents here", ToolCallID: "tc_norm_1"},
		{
			Role:    "assistant",
			Content: "Here is the file content",
		},
		{Role: "user", Content: "Thanks"},
	}

	result := sanitizeToolMessages(history)

	if len(result) != len(history) {
		t.Fatalf("Expected %d messages (unchanged), got %d", len(history), len(result))
	}
	for i, msg := range result {
		if msg.Role != history[i].Role {
			t.Errorf("Message[%d]: expected role %s, got %s", i, history[i].Role, msg.Role)
		}
		if msg.Content != history[i].Content {
			t.Errorf("Message[%d]: expected content %q, got %q", i, history[i].Content, msg.Content)
		}
	}
}

func TestSanitizeToolMessages_MultipleAssistantTurnsWithTools(t *testing.T) {
	// Two independent assistant turns with tools:
	// Turn 1: 2 tool_use, 2 tool_results present → fully paired
	// Turn 2: 2 tool_use, only 1 tool_result → one placeholder added
	history := []providers.Message{
		{Role: "user", Content: "Do stuff"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{ID: "a1_t1", Name: "read_file"},
				{ID: "a1_t2", Name: "exec"},
			},
		},
		{Role: "tool", Content: "contents", ToolCallID: "a1_t1"},
		{Role: "tool", Content: "output", ToolCallID: "a1_t2"},
		{Role: "user", Content: "More stuff"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{ID: "a2_t1", Name: "read_file"},
				{ID: "a2_t2", Name: "write_file"},
			},
		},
		{Role: "tool", Content: "more contents", ToolCallID: "a2_t1"},
		{Role: "user", Content: "Done"},
	}

	result := sanitizeToolMessages(history)

	// Original: 8 messages. Turn 1 is fine (3 msgs: assistant + 2 tools).
	// Turn 2: assistant + 1 existing tool + 1 placeholder = 3 msgs.
	// Total: user + [assistant+2tools] + user + [assistant+1tool+1placeholder] + user = 9
	if len(result) != 9 {
		t.Fatalf("Expected 8 messages, got %d", len(result))
	}

	// Verify the placeholder for a2_t2 is at the right position
	// Position: user, assistant1, tool1, tool2, user, assistant2, tool_a2_t1, placeholder_a2_t2
	if result[6].Role != "tool" || result[6].ToolCallID != "a2_t1" {
		t.Errorf("Expected message[6] to be tool/a2_t1, got %s/%s", result[6].Role, result[6].ToolCallID)
	}
	if result[7].Role != "tool" || result[7].ToolCallID != "a2_t2" {
		t.Errorf("Expected message[7] to be placeholder tool/a2_t2, got %s/%s", result[7].Role, result[7].ToolCallID)
	}
	if result[7].Content != "[Tool execution was cancelled]" {
		t.Errorf("Expected placeholder content, got %s", result[7].Content)
	}
}

func TestSanitizeToolMessages_EmptyHistory(t *testing.T) {
	result := sanitizeToolMessages(nil)
	if len(result) != 0 {
		t.Errorf("Expected empty result for nil input, got %d messages", len(result))
	}

	result = sanitizeToolMessages([]providers.Message{})
	if len(result) != 0 {
		t.Errorf("Expected empty result for empty input, got %d messages", len(result))
	}
}

func TestSanitizeToolMessages_AllToolResultsMissingEmptyContentWithReasoning(t *testing.T) {
	// Assistant message with tool_use, no tool_results, empty Content but
	// non-empty ReasoningContent → should keep the message (reasoning preserved).
	history := []providers.Message{
		{Role: "user", Content: "Think about it"},
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "I was thinking...",
			ToolCalls: []providers.ToolCall{
				{ID: "tc_reason_1", Name: "exec"},
			},
		},
		{Role: "user", Content: "What did you think?"},
	}

	result := sanitizeToolMessages(history)

	if len(result) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(result))
	}
	if len(result[1].ToolCalls) != 0 {
		t.Errorf("Expected 0 tool calls, got %d", len(result[1].ToolCalls))
	}
	if result[1].ReasoningContent != "I was thinking..." {
		t.Errorf("Expected reasoning content preserved, got %q", result[1].ReasoningContent)
	}
}
func TestSanitizeToolMessages_NamelessToolCallStripped(t *testing.T) {
	// An assistant message with a tool_call missing a function name (both
	// top-level Name and Function.Name empty) causes HTTP 400 on OpenAI-compatible
	// APIs. It must be stripped while valid tool_calls are preserved.
	history := []providers.Message{
		{Role: "user", Content: "Do things"},
		{
			Role:    "assistant",
			Content: "Calling tools",
			ToolCalls: []providers.ToolCall{
				{ID: "tc_valid", Name: "read_file"},
				{ID: "tc_nameless"}, // no Name, no Function
			},
		},
		{Role: "tool", Content: "file contents", ToolCallID: "tc_valid"},
		{Role: "user", Content: "Thanks"},
	}

	result := sanitizeToolMessages(history)

	// user, assistant, tool(tc_valid), user = 4 messages
	if len(result) != 4 {
		t.Fatalf("Expected 4 messages, got %d", len(result))
	}
	if len(result[1].ToolCalls) != 1 {
		t.Fatalf("Expected 1 tool_call after stripping, got %d", len(result[1].ToolCalls))
	}
	if result[1].ToolCalls[0].ID != "tc_valid" {
		t.Errorf("Expected tc_valid to remain, got %q", result[1].ToolCalls[0].ID)
	}
}

func TestSanitizeToolMessages_NamelessToolCallWithEmptyFunctionStripped(t *testing.T) {
	// A tool_call with a present-but-empty Function struct must also be stripped.
	history := []providers.Message{
		{
			Role:    "assistant",
			Content: "tools",
			ToolCalls: []providers.ToolCall{
				{ID: "tc_bad", Function: &providers.FunctionCall{Name: ""}},
				{ID: "tc_good", Function: &providers.FunctionCall{Name: "web_search"}},
			},
		},
		{Role: "tool", Content: "results", ToolCallID: "tc_good"},
	}

	result := sanitizeToolMessages(history)

	if len(result) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(result))
	}
	if len(result[0].ToolCalls) != 1 {
		t.Fatalf("Expected 1 tool_call after stripping, got %d", len(result[0].ToolCalls))
	}
	if len(result[0].ToolCalls) > 0 && result[0].ToolCalls[0].ID != "tc_good" {
		t.Errorf("Expected tc_good to remain, got %q", result[0].ToolCalls[0].ID)
	}
}

func TestSanitizeToolMessages_AllNamelessToolCallsStrippedKeepsContent(t *testing.T) {
	// If ALL tool_calls are nameless, strip them all but keep the message content.
	history := []providers.Message{
		{Role: "user", Content: "hi"},
		{
			Role:    "assistant",
			Content: "I tried but couldn't",
			ToolCalls: []providers.ToolCall{
				{ID: "tc_1"},
				{ID: "tc_2"},
			},
		},
		{Role: "user", Content: "ok"},
	}

	result := sanitizeToolMessages(history)

	if len(result) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(result))
	}
	if len(result[1].ToolCalls) != 0 {
		t.Errorf("Expected 0 tool_calls, got %d", len(result[1].ToolCalls))
	}
	if result[1].Content != "I tried but couldn't" {
		t.Errorf("Expected content preserved, got %q", result[1].Content)
	}
}

func TestStripNamelessToolCalls(t *testing.T) {
	calls := []providers.ToolCall{
		{ID: "a", Name: "exec"},
		{ID: "b"}, // stripped
		{ID: "c", Function: &providers.FunctionCall{Name: "read_file"}},
		{ID: "d", Function: &providers.FunctionCall{Name: "  "}}, // stripped (whitespace)
		{ID: "e", Name: "write_file", Function: &providers.FunctionCall{Name: "other"}},
	}

	cleaned := stripNamelessToolCalls(calls)

	if len(cleaned) != 3 {
		t.Fatalf("Expected 3 tool_calls, got %d", len(cleaned))
	}
	if cleaned[0].ID != "a" || cleaned[1].ID != "c" || cleaned[2].ID != "e" {
		t.Errorf("Unexpected cleaned order: %+v", cleaned)
	}
}
