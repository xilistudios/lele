package agent

import (
	"encoding/json"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/tools"
)

type TurnMarker struct {
	SessionKey       string    `json:"session_key"`
	DedupeID         string    `json:"dedupe_id,omitempty"`
	SpoolID          int64     `json:"spool_id,omitempty"`
	Channel          string    `json:"channel,omitempty"`
	ChatID           string    `json:"chat_id,omitempty"`
	MsgID            string    `json:"msg_id,omitempty"`
	AgentID          string    `json:"agent_id,omitempty"`
	Model            string    `json:"model,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	Iter             int       `json:"iter"`
	Phase            string    `json:"phase"`
	ResumeNoticeSent bool      `json:"resume_notice_sent,omitempty"`
}

const (
	turnKeyPrefix     = "sess:turn:"
	turnPhaseInbound  = "inbound_start"
	turnPhaseLLMWait  = "llm_wait"
	turnPhaseToolsRun = "tools_running"
)

func (al *AgentLoop) resumeGate() bool {
	if al == nil {
		return false
	}
	return al.cfg().Session.ResumeEnabled()
}

func (al *AgentLoop) turnMarkerEnabled() bool {
	if al == nil {
		return false
	}
	return al.resumeGate() && al.inboundDurability != nil
}

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
			m = TurnMarker{}
		}
	}
	if !ok || m.SessionKey == "" {
		if phase != turnPhaseInbound {
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

const turnSubsKeyPrefix = "sess:turnsubs:"

const maxSnapshottedSubagents = 10

const maxSnapshotTaskRunes = 200

type TurnSubagent struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Task  string `json:"task,omitempty"`
}

func (tc *toolCoordinatorImpl) snapshotRunningSubagents(sessionKey string) []*tools.SubagentTask {
	if tc == nil || sessionKey == "" {
		return nil
	}
	keys := tc.sessionCancellationKeys(sessionKey)
	var out []*tools.SubagentTask
	for _, task := range tc.listRunningSubagentTasks() {
		if tools.TaskBelongsToSession(task, keys) {
			out = append(out, task)
		}
	}
	return out
}

func (al *AgentLoop) snapshotRunningSubagents(sessionKey string) []*tools.SubagentTask {
	if al == nil || al.toolCoordinator == nil {
		return nil
	}
	return al.toolCoordinator.snapshotRunningSubagents(sessionKey)
}

func (al *AgentLoop) writeTurnSubagentSnapshot(sessionKey string) {
	if al == nil || sessionKey == "" || !al.turnMarkerEnabled() {
		return
	}
	repo := al.sessionStateKV()
	if repo == nil {
		return
	}
	key := turnSubsKeyPrefix + sessionKey
	tasks := al.snapshotRunningSubagents(sessionKey)
	if len(tasks) == 0 {
		if err := repo.Delete(key); err != nil {
			logger.WarnCF("session", "resume: failed to clear subagent snapshot", map[string]any{
				"session_key": sessionKey,
				"error":       err.Error(),
			})
		}
		return
	}
	if len(tasks) > maxSnapshottedSubagents {
		tasks = tasks[:maxSnapshottedSubagents]
	}
	subs := make([]TurnSubagent, 0, len(tasks))
	for _, t := range tasks {
		subs = append(subs, TurnSubagent{ID: t.ID, Label: t.Label, Task: truncateRunes(t.Task, maxSnapshotTaskRunes)})
	}
	data, err := json.Marshal(subs)
	if err != nil {
		return
	}
	if err := repo.Set(key, string(data)); err != nil {
		logger.WarnCF("session", "resume: failed to persist subagent snapshot", map[string]any{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
	}
}

func (al *AgentLoop) readTurnSubagentSnapshot(sessionKey string) []TurnSubagent {
	if al == nil || sessionKey == "" {
		return nil
	}
	repo := al.sessionStateKV()
	if repo == nil {
		return nil
	}
	raw, ok, err := repo.Get(turnSubsKeyPrefix + sessionKey)
	if err != nil || !ok || raw == "" {
		return nil
	}
	var subs []TurnSubagent
	if err := json.Unmarshal([]byte(raw), &subs); err != nil {
		logger.WarnCF("session", "resume: corrupt subagent snapshot, dropping", map[string]any{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
		_ = repo.Delete(turnSubsKeyPrefix + sessionKey)
		return nil
	}
	return subs
}

func (al *AgentLoop) clearTurnSubagentSnapshot(sessionKey string) {
	if al == nil || sessionKey == "" {
		return
	}
	repo := al.sessionStateKV()
	if repo == nil {
		return
	}
	if err := repo.Delete(turnSubsKeyPrefix + sessionKey); err != nil {
		logger.WarnCF("session", "resume: failed to clear subagent snapshot", map[string]any{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
	}
}

func (al *AgentLoop) clearTurnState(sessionKey, dedupeID string) {
	if dedupeID != "" {
		if m, ok := al.getTurnMarker(sessionKey); ok && m.DedupeID != "" && m.DedupeID != dedupeID {
			return
		}
	}
	al.clearTurnMarker(sessionKey, dedupeID)
	al.clearTurnSubagentSnapshot(sessionKey)
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
