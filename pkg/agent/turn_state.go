// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"encoding/json"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
)

// TurnMarker checkpoints an in-flight inbound turn so a restart can resume it.
// Stored at KV key "sess:turn:<sessionKey>". Written only when BOTH
// session.durable_inbound and session.resume_enabled are true (the resume
// trigger is the durable inbound replay, so without durability there is no
// replay to resume from). Cleared when the turn completes; overwritten by the
// next inbound turn on the same session. Never expired.
type TurnMarker struct {
	SessionKey string `json:"session_key"`
	// DedupeID is the durable spool dedupe key (== message_id). It is the
	// resume match key: a replayed inbound carries the same DedupeID as the
	// turn that was checkpointed before the crash, which is how
	// processMessage distinguishes "resume this exact turn" from "a new
	// turn superseding an abandoned one".
	DedupeID  string    `json:"dedupe_id,omitempty"`
	SpoolID   int64     `json:"spool_id,omitempty"`
	Channel   string    `json:"channel,omitempty"`
	ChatID    string    `json:"chat_id,omitempty"`
	MsgID     string    `json:"msg_id,omitempty"` // reply target for the final answer
	AgentID   string    `json:"agent_id,omitempty"`
	Model     string    `json:"model,omitempty"` // resolved model for this turn ("" = session model)
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Iter      int       `json:"iter"`
	// Phase records how far the turn got before the process stopped:
	// inbound_start (accepted, no LLM request yet), llm_wait (a real provider
	// request was outstanding) or tools_running (tool calls were executing).
	Phase string `json:"phase"`
	// ResumeNoticeSent guards the "resuming…" user notice so a resume that
	// itself gets interrupted and replayed does not spam the channel.
	ResumeNoticeSent bool `json:"resume_notice_sent,omitempty"`
}

const (
	turnKeyPrefix     = "sess:turn:"
	turnPhaseInbound  = "inbound_start"
	turnPhaseLLMWait  = "llm_wait"
	turnPhaseToolsRun = "tools_running"
)

// resumeGate reports whether the resume feature is enabled in config. Read
// through al.cfg() so a config reload takes effect for new turns without a
// restart (same pattern as every other per-turn config read).
func (al *AgentLoop) resumeGate() bool {
	if al == nil {
		return false
	}
	return al.cfg().Session.ResumeEnabled()
}

// turnMarkerEnabled gates all turn-marker writes. The spec asks for
// resume_enabled && durable_inbound: without the durable spool there is no
// replay after a restart, and a replay is the only resume trigger in this
// design. The loop has no direct handle on the config boolean behind
// durability — the gateway wires durability in through
// SetInboundDurability exactly when the flag is on (and only when a store and
// a bus exist), so "inboundDurability != nil" is the faithful in-loop signal
// for durable_inbound. A marker written while durability is off would be
// harmless anyway (nothing reads it without a replay), but keeping the gate
// exact avoids dead KV rows.
func (al *AgentLoop) turnMarkerEnabled() bool {
	if al == nil {
		return false
	}
	return al.resumeGate() && al.inboundDurability != nil
}

// getTurnMarker returns the checkpointed marker for a session, if any. A
// corrupt payload is deleted (it can never be resumed) and reported as absent.
func (al *AgentLoop) getTurnMarker(sessionKey string) (TurnMarker, bool) {
	var zero TurnMarker
	if al == nil || sessionKey == "" {
		return zero, false
	}
	repo := al.sessionStateKV()
	if repo == nil {
		return zero, false
	}
	raw, ok, err := repo.Get(turnKeyPrefix + sessionKey)
	if err != nil || !ok || raw == "" {
		return zero, false
	}
	var m TurnMarker
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		logger.WarnCF("session", "resume: corrupt turn marker, dropping", map[string]any{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
		_ = repo.Delete(turnKeyPrefix + sessionKey)
		return zero, false
	}
	return m, true
}

// writeTurnMarker advances (or seeds) the checkpoint for a session.
//
// Update-only for every phase except inbound_start: llm_wait and
// tools_running are mid-turn states, and a marker for them without a
// preceding inbound_start means the turn was started through a path the
// resume design does not cover (ProcessDirect, goals, cron). Creating markers
// there would resume work the spool never held, so those calls are no-ops.
//
// Read-modify-write keeps the fields the inbound turn seeded (DedupeID,
// SpoolID, channel info, StartedAt, ResumeNoticeSent) and only advances
// Phase/Iter/UpdatedAt. Fail-closed: every error is logged, never returned —
// a marker that cannot be written only loses crash coverage, it must not
// fail the live turn.
func (al *AgentLoop) writeTurnMarker(sessionKey, phase string, iter int, seed func(*TurnMarker)) {
	if al == nil || sessionKey == "" || !al.turnMarkerEnabled() {
		return
	}
	repo := al.sessionStateKV()
	if repo == nil {
		return
	}
	key := turnKeyPrefix + sessionKey

	var m TurnMarker
	existing, ok, err := repo.Get(key)
	if err != nil {
		logger.WarnCF("session", "resume: failed to read turn marker", map[string]any{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
		return
	}
	if ok && existing != "" {
		if err := json.Unmarshal([]byte(existing), &m); err != nil {
			// Unreadable old marker: start a fresh one over it. The inbound
			// turn that is writing now replaces whatever it described.
			m = TurnMarker{}
		}
	}
	if !ok || m.SessionKey == "" {
		if phase != turnPhaseInbound {
			// Update-only: no inbound_start to advance from.
			return
		}
		m = TurnMarker{SessionKey: sessionKey, StartedAt: time.Now().UTC()}
	}

	m.Phase = phase
	m.Iter = iter
	m.UpdatedAt = time.Now().UTC()
	if seed != nil {
		seed(&m)
	}

	data, err := json.Marshal(m)
	if err != nil {
		logger.WarnCF("session", "resume: failed to encode turn marker", map[string]any{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
		return
	}
	if err := repo.Set(key, string(data)); err != nil {
		logger.WarnCF("session", "resume: failed to persist turn marker", map[string]any{
			"session_key": sessionKey,
			"phase":       phase,
			"error":       err.Error(),
		})
	}
}

// clearTurnMarker removes the checkpoint. A non-empty dedupeID makes the
// delete conditional: the caller is announcing "MY turn ended", and a marker
// belonging to a different (newer) turn must survive. dedupeID == "" is the
// unconditional reset used before seeding a fresh inbound turn.
func (al *AgentLoop) clearTurnMarker(sessionKey, dedupeID string) {
	if al == nil || sessionKey == "" {
		return
	}
	repo := al.sessionStateKV()
	if repo == nil {
		return
	}
	key := turnKeyPrefix + sessionKey
	if dedupeID != "" {
		m, ok := al.getTurnMarker(sessionKey)
		if !ok {
			return
		}
		if m.DedupeID != "" && m.DedupeID != dedupeID {
			// The marker describes a different turn; leave it alone.
			return
		}
	}
	if err := repo.Delete(key); err != nil {
		logger.WarnCF("session", "resume: failed to clear turn marker", map[string]any{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
	}
}

// setResumeNoticeSent persists the one-shot flag for the resume notice
// BEFORE the notice is published: a crash between the write and the publish
// loses at most the notice, while the reverse order would duplicate it on
// every replay.
func (al *AgentLoop) setResumeNoticeSent(sessionKey string) {
	if al == nil || sessionKey == "" || !al.turnMarkerEnabled() {
		return
	}
	repo := al.sessionStateKV()
	if repo == nil {
		return
	}
	m, ok := al.getTurnMarker(sessionKey)
	if !ok {
		return
	}
	m.ResumeNoticeSent = true
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := repo.Set(turnKeyPrefix+sessionKey, string(data)); err != nil {
		logger.WarnCF("session", "resume: failed to persist resume notice flag", map[string]any{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
	}
}
