// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/session"
)

// In-flight tool bookkeeping.
//
// The live "running tool" row a UI paints while a long call is in flight
// (sleep, wait_for_subagent, exec, ...) only travels in the tool.executing
// event stream; the call itself never reaches the session history. Any chat
// reload — TUI session switch/resume, WebUI page reload or re-subscribe —
// therefore lost the row while the turn kept running.
//
// The loop mirrors every native tool lifecycle event into inProgressTools
// (sessionKey -> *session.InProgressTool) so both UIs can restore the row from
// the loop itself instead of from a replay of past events. The map is
// in-memory only and is cleared when the turn ends, so it can never resurrect
// a tool that is no longer running.

// publishToolLifecycle publishes a tool lifecycle event after updating the
// session's in-flight tool record. Every tool.executing / tool.result publish
// on the native channel goes through here so the record and the wire always
// agree — a divergent bookkeeping path would silently reintroduce the missing
// row on whichever reload route forgot to update it.
//
// Events of other channels are published untouched: only the UI-bearing native
// channel (TUI + WebUI) needs the record, and their tool events are gated the
// same way at every call site.
func (al *AgentLoop) publishToolLifecycle(msg bus.OutboundMessage) {
	if msg.Channel == channels.ChannelName {
		al.trackInProgressTool(msg)
	}
	al.bus.PublishOutbound(msg)
}

// trackInProgressTool mirrors one tool lifecycle event into the session's
// in-flight tool record.
func (al *AgentLoop) trackInProgressTool(msg bus.OutboundMessage) {
	key := al.ResolveSessionKey(msg.ChatID)
	if key == "" {
		return
	}

	switch msg.Event {
	case "tool.executing":
		tool := msg.Metadata["tool"]
		if tool == "" {
			return
		}
		al.inProgressTools.Store(key, &session.InProgressTool{
			Tool:               tool,
			Action:             msg.Metadata["action"],
			Arguments:          msg.Metadata["arguments"],
			ToolCallID:         msg.Metadata["tool_call_id"],
			SubagentSessionKey: msg.Metadata["subagent_session_key"],
			StartedAt:          time.Now(),
		})
	case "tool.result":
		raw, ok := al.inProgressTools.Load(key)
		if !ok {
			return
		}
		inFlight, _ := raw.(*session.InProgressTool)
		if inFlight == nil {
			return
		}
		// A result may only clear the call it belongs to. Results carry the
		// tool_call_id when the executor published one; the id-less special
		// cases (compact, goal) fall back to the tool name. Anything else is
		// stale (out-of-order delivery, a nested call) and must not erase a
		// newer tool.
		if id := msg.Metadata["tool_call_id"]; id != "" && inFlight.ToolCallID != "" && id != inFlight.ToolCallID {
			return
		}
		if name := msg.Metadata["tool"]; name != "" && name != inFlight.Tool {
			return
		}
		al.inProgressTools.Delete(key)
	}
}

// GetInProgressTool returns the tool the session is currently executing, or nil
// when there is none.
//
// The returned record is a copy. Callers MUST gate on IsSessionProcessing
// before displaying it: it is in-memory turn state, so it is only meaningful
// while the session is actually processing (a cancelled or crashed turn may
// have left a record behind until the next cleanup).
func (al *AgentLoop) GetInProgressTool(sessionKey string) *session.InProgressTool {
	key := al.ResolveSessionKey(sessionKey)
	if key == "" {
		return nil
	}
	raw, ok := al.inProgressTools.Load(key)
	if !ok {
		return nil
	}
	inFlight, _ := raw.(*session.InProgressTool)
	return inFlight.Clone()
}

// clearInProgressTool drops the session's in-flight tool record. Called when a
// turn ends or is cancelled: from that instant on the recorded tool is stale
// (its tool.result may even have been published after cancellation), and a
// later chat reload must not restore a row for a turn that is no longer
// running.
func (al *AgentLoop) clearInProgressTool(sessionKey string) {
	if sessionKey == "" {
		return
	}
	al.inProgressTools.Delete(al.ResolveSessionKey(sessionKey))
}
