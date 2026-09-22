// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package session

import "time"

// InProgressTool is the tool call a session is currently executing.
//
// It is turn-scoped, in-memory-only state: it is never persisted, because a
// restarted process has no running tool and replaying a stale one would leave
// every UI showing a command that is not there. Consumers must gate on the
// session actually processing (IsSessionProcessing) before displaying it.
//
// Why the record exists: the "running tool" row a UI paints while a long call
// (sleep, wait_for_subagent, exec, ...) is in flight only ever travels in the
// live event stream — the in-flight tool call never reaches the session
// history. Any reload of the chat (TUI session switch, WebUI page reload or
// re-subscribe) therefore lost the row. This record keeps the last
// tool.executing for the session so the UI can restore it from the same source
// of truth the live events come from.
type InProgressTool struct {
	// Tool is the tool name ("exec", "sleep", "wait_for_subagent", ...).
	Tool string `json:"tool"`
	// Action is the human-readable "tool: arguments" line carried by the
	// tool.executing event, already formatted by the backend.
	Action string `json:"action,omitempty"`
	// Arguments is the raw JSON object of the call arguments exactly as
	// published in the event metadata. Empty when the call had none.
	Arguments string `json:"arguments,omitempty"`
	// ToolCallID pairs this call with its tool.result, so a late result of a
	// previous call cannot clear a newer one.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// SubagentSessionKey is set for spawn calls (the subagent's session key).
	SubagentSessionKey string `json:"subagent_session_key,omitempty"`
	// StartedAt is when the tool started executing (local clock).
	StartedAt time.Time `json:"started_at"`
}

// Clone returns a copy of the record so readers on other goroutines (UI
// handlers, HTTP/WS payload builders) never race a concurrent event that
// replaces the stored pointer.
func (t *InProgressTool) Clone() *InProgressTool {
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}
