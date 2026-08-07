// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// GoalStatus represents the state of a persistent goal.
type GoalStatus string

const (
	GoalActive  GoalStatus = "active"
	GoalPaused  GoalStatus = "paused"
	GoalDone    GoalStatus = "done"
	GoalBlocked GoalStatus = "blocked" // budget exhausted without completion
)

// DefaultGoalMaxTurns is the default turn budget for a goal loop.
const DefaultGoalMaxTurns = 20

// Goal represents a persistent objective that the agent works toward
// across multiple turns until achieved or the budget is exhausted.
type Goal struct {
	Text       string     `json:"text"`
	Status     GoalStatus `json:"status"`
	TurnsUsed  int        `json:"turns_used"`
	MaxTurns   int        `json:"max_turns"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	SessionKey string     `json:"session_key"`
}

// GoalManager manages persistent goals per session.
type GoalManager struct {
	mu       sync.RWMutex
	goals    map[string]*Goal // sessionKey -> active goal
	storeDir string
	judge    GoalJudge
	repo     *store.GoalRepo
}

// GoalJudge evaluates whether a goal has been achieved after each turn.
type GoalJudge interface {
	// JudgeGoal evaluates the agent's progress toward the goal using the
	// session's conversation summary (fetched by the judge) and the latest
	// agent response. Returns true if the goal is achieved, false if more
	// work is needed.
	JudgeGoal(ctx context.Context, sessionKey, goalText string, lastResponse string) (bool, string, error)
}

// NewGoalManager creates a new goal manager.
func NewGoalManager(storeDir string) *GoalManager {
	gm := &GoalManager{
		goals:    make(map[string]*Goal),
		storeDir: storeDir,
	}
	gm.loadFromDisk()
	return gm
}

// SetJudge sets the goal judge implementation.
func (gm *GoalManager) SetJudge(judge GoalJudge) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.judge = judge
}

// SetStore switches goal persistence to the given SQLite repository.
// If the repository is empty and legacy goal files exist, they are
// migrated lazily. Pass nil to revert to the JSON backend.
func (gm *GoalManager) SetStore(repo *store.GoalRepo) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	gm.repo = repo
	if repo == nil {
		return
	}

	goalsJSON, err := repo.ListGoals()
	if err != nil {
		logger.WarnCF("agent", "Failed to list goals from store", map[string]interface{}{"error": err.Error()})
		return
	}

	if len(goalsJSON) > 0 {
		for sessionKey, goalJSON := range goalsJSON {
			var goal Goal
			if err := json.Unmarshal([]byte(goalJSON), &goal); err != nil {
				logger.WarnCF("agent", "Skipping corrupt goal in store", map[string]interface{}{
					"session_key": sessionKey,
					"error":       err.Error(),
				})
				continue
			}
			if goal.Status == GoalActive || goal.Status == GoalPaused {
				gm.goals[goal.SessionKey] = &goal
			}
		}
		return
	}

	// Repository empty: migrate goals already loaded in memory by loadFromDisk.
	for sessionKey, goal := range gm.goals {
		data, err := json.Marshal(goal)
		if err != nil {
			logger.WarnCF("agent", "Failed to marshal goal during migration", map[string]interface{}{
				"session_key": sessionKey,
				"error":       err.Error(),
			})
			continue
		}
		if err := repo.SetGoal(sessionKey, string(data)); err != nil {
			logger.WarnCF("agent", "Failed to migrate goal to store", map[string]interface{}{
				"session_key": sessionKey,
				"error":       err.Error(),
			})
		}
	}
}

// Set creates or replaces the goal for a session.
func (gm *GoalManager) Set(sessionKey, text string, maxTurns int) *Goal {
	if maxTurns <= 0 {
		maxTurns = DefaultGoalMaxTurns
	}

	gm.mu.Lock()
	defer gm.mu.Unlock()

	goal := &Goal{
		Text:       text,
		Status:     GoalActive,
		TurnsUsed:  0,
		MaxTurns:   maxTurns,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		SessionKey: sessionKey,
	}
	gm.goals[sessionKey] = goal
	gm.persist(sessionKey, goal)

	logger.InfoCF("agent", "Goal set", map[string]interface{}{
		"session_key": sessionKey,
		"goal":        text,
		"max_turns":   maxTurns,
	})

	return goal
}

// Get returns the active goal for a session, or nil.
func (gm *GoalManager) Get(sessionKey string) *Goal {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return gm.goals[sessionKey]
}

// IsActive returns true if the session has an active (not paused/done) goal.
func (gm *GoalManager) IsActive(sessionKey string) bool {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	g, ok := gm.goals[sessionKey]
	return ok && g.Status == GoalActive
}

// Pause pauses the goal for a session.
func (gm *GoalManager) Pause(sessionKey string) bool {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.goals[sessionKey]
	if !ok || g.Status != GoalActive {
		return false
	}
	g.Status = GoalPaused
	g.UpdatedAt = time.Now()
	gm.persist(sessionKey, g)
	return true
}

// Resume resumes a paused goal for a session.
func (gm *GoalManager) Resume(sessionKey string) bool {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.goals[sessionKey]
	if !ok || g.Status != GoalPaused {
		return false
	}
	g.Status = GoalActive
	g.UpdatedAt = time.Now()
	gm.persist(sessionKey, g)
	return true
}

// Clear removes the goal for a session.
func (gm *GoalManager) Clear(sessionKey string) bool {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	_, ok := gm.goals[sessionKey]
	if !ok {
		return false
	}
	delete(gm.goals, sessionKey)
	gm.removePersisted(sessionKey)
	return true
}

// IncrementTurn increments the turn counter and returns whether the budget
// is exhausted. If exhausted, the goal status is set to Blocked.
func (gm *GoalManager) IncrementTurn(sessionKey string) (exhausted bool) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.goals[sessionKey]
	if !ok || g.Status != GoalActive {
		return true
	}

	g.TurnsUsed++
	g.UpdatedAt = time.Now()

	if g.TurnsUsed >= g.MaxTurns {
		g.Status = GoalBlocked
		gm.persist(sessionKey, g)
		logger.InfoCF("agent", "Goal budget exhausted", map[string]interface{}{
			"session_key": sessionKey,
			"turns_used":  g.TurnsUsed,
			"max_turns":   g.MaxTurns,
		})
		return true
	}

	gm.persist(sessionKey, g)
	return false
}

// MarkDone marks the goal as completed.
func (gm *GoalManager) MarkDone(sessionKey string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.goals[sessionKey]
	if !ok {
		return
	}
	g.Status = GoalDone
	g.UpdatedAt = time.Now()
	gm.persist(sessionKey, g)

	logger.InfoCF("agent", "Goal completed", map[string]interface{}{
		"session_key": sessionKey,
		"turns_used":  g.TurnsUsed,
	})
}

// FormatStatus returns a human-readable status string for the goal.
func (g *Goal) FormatStatus() string {
	statusEmoji := map[GoalStatus]string{
		GoalActive:  "🎯",
		GoalPaused:  "⏸️",
		GoalDone:    "✅",
		GoalBlocked: "🚫",
	}
	emoji := statusEmoji[g.Status]

	return fmt.Sprintf("%s Goal: %s\n   Estado: %s · Turnos: %d/%d · Creado: %s",
		emoji, g.Text, g.Status, g.TurnsUsed, g.MaxTurns,
		g.CreatedAt.Format("2006-01-02 15:04"))
}

// ============================================================================
// Persistence
// ============================================================================

func (gm *GoalManager) goalFilePath(sessionKey string) string {
	// Sanitize session key for filesystem use
	safe := sanitizeFileName(sessionKey)
	return filepath.Join(gm.storeDir, safe+".json")
}

func (gm *GoalManager) persist(sessionKey string, goal *Goal) {
	if gm.repo != nil {
		data, err := json.Marshal(goal)
		if err != nil {
			return
		}
		if err := gm.repo.SetGoal(sessionKey, string(data)); err != nil {
			logger.WarnCF("agent", "Failed to persist goal", map[string]interface{}{
				"session_key": sessionKey,
				"error":       err.Error(),
			})
		}
		return
	}

	if gm.storeDir == "" {
		return
	}
	if err := os.MkdirAll(gm.storeDir, 0755); err != nil {
		logger.WarnCF("agent", "Failed to create goals dir", map[string]interface{}{"error": err.Error()})
		return
	}

	data, err := json.MarshalIndent(goal, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(gm.goalFilePath(sessionKey), data, 0644); err != nil {
		logger.WarnCF("agent", "Failed to persist goal", map[string]interface{}{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
	}
}

func (gm *GoalManager) removePersisted(sessionKey string) {
	if gm.repo != nil {
		if err := gm.repo.DeleteGoal(sessionKey); err != nil {
			logger.WarnCF("agent", "Failed to delete goal from store", map[string]interface{}{
				"session_key": sessionKey,
				"error":       err.Error(),
			})
		}
		return
	}
	if gm.storeDir == "" {
		return
	}
	_ = os.Remove(gm.goalFilePath(sessionKey))
}

func (gm *GoalManager) loadFromDisk() {
	if gm.repo != nil {
		gm.loadFromRepo()
		return
	}
	gm.loadFromLegacyFiles()
}

// loadFromLegacyFiles loads goals from JSON files on disk. Only active
// or paused goals are restored.
func (gm *GoalManager) loadFromLegacyFiles() {
	if gm.storeDir == "" {
		return
	}
	entries, err := os.ReadDir(gm.storeDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(gm.storeDir, entry.Name()))
		if err != nil {
			continue
		}
		var goal Goal
		if err := json.Unmarshal(data, &goal); err != nil {
			continue
		}
		// Only restore active or paused goals
		if goal.Status == GoalActive || goal.Status == GoalPaused {
			gm.goals[goal.SessionKey] = &goal
		}
	}
}

// loadFromRepo loads goals from the SQLite repository. Corrupt entries
// are skipped with a warning. Only active or paused goals are restored.
// If the repository is empty and legacy JSON files exist in storeDir,
// they are loaded and migrated into the repository best-effort.
func (gm *GoalManager) loadFromRepo() {
	goalsJSON, err := gm.repo.ListGoals()
	if err != nil {
		logger.WarnCF("agent", "Failed to list goals from store", map[string]interface{}{"error": err.Error()})
		return
	}

	if len(goalsJSON) > 0 {
		for sessionKey, goalJSON := range goalsJSON {
			var goal Goal
			if err := json.Unmarshal([]byte(goalJSON), &goal); err != nil {
				logger.WarnCF("agent", "Skipping corrupt goal in store", map[string]interface{}{
					"session_key": sessionKey,
					"error":       err.Error(),
				})
				continue
			}
			if goal.Status == GoalActive || goal.Status == GoalPaused {
				gm.goals[goal.SessionKey] = &goal
			}
		}
		return
	}

	// Repository empty: load legacy JSON files and migrate them.
	gm.loadFromLegacyFiles()
	for sessionKey, goal := range gm.goals {
		data, err := json.Marshal(goal)
		if err != nil {
			logger.WarnCF("agent", "Failed to marshal goal during migration", map[string]interface{}{
				"session_key": sessionKey,
				"error":       err.Error(),
			})
			continue
		}
		if err := gm.repo.SetGoal(sessionKey, string(data)); err != nil {
			logger.WarnCF("agent", "Failed to migrate goal to store", map[string]interface{}{
				"session_key": sessionKey,
				"error":       err.Error(),
			})
		}
	}
}

// sanitizeFileName replaces characters that are unsafe for filenames.
func sanitizeFileName(s string) string {
	replacer := func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '_'
		}
	}
	result := make([]rune, 0, len(s))
	for _, r := range s {
		result = append(result, replacer(r))
	}
	// Limit length
	if len(result) > 128 {
		result = result[:128]
	}
	return string(result)
}

// ============================================================================
// LLM-based Goal Judge
// ============================================================================

// llmGoalJudge uses an LLM to evaluate whether a goal has been achieved.
type llmGoalJudge struct {
	provider providers.LLMProvider
	model    string
}

// NewLLMGoalJudge creates a goal judge that uses an LLM to evaluate progress.
func NewLLMGoalJudge(provider providers.LLMProvider, model string) GoalJudge {
	return &llmGoalJudge{
		provider: provider,
		model:    model,
	}
}

const goalJudgeSystemPrompt = `You are a goal completion judge. Your ONLY job is to evaluate whether an AI agent has achieved its assigned goal based on its latest response.

Rules:
- Respond with EXACTLY one word: "DONE" if the goal is fully achieved, or "CONTINUE" if more work is needed.
- Be strict: only say DONE when the goal is genuinely and completely fulfilled.
- If the agent is making progress but hasn't finished, say CONTINUE.
- If the agent is stuck or going in circles, say CONTINUE (the budget system handles exhaustion).
- Do NOT explain your reasoning. Only output DONE or CONTINUE.`

func (j *llmGoalJudge) JudgeGoal(ctx context.Context, sessionKey, goalText string, lastResponse string) (bool, string, error) {
	if j.provider == nil {
		return false, "", fmt.Errorf("goal judge: no provider configured")
	}

	// Truncate last response to avoid excessive token usage
	truncated := lastResponse
	if len(truncated) > 4000 {
		truncated = truncated[:4000] + "\n...[truncated]"
	}

	userPrompt := fmt.Sprintf("GOAL: %s\n\nAGENT'S LATEST RESPONSE:\n%s\n\nIs the goal achieved? Reply DONE or CONTINUE.", goalText, truncated)

	messages := []providers.Message{
		{Role: "system", Content: goalJudgeSystemPrompt},
		{Role: "user", Content: userPrompt},
	}

	options := map[string]interface{}{
		"max_tokens":  10,
		"temperature": 0.0,
	}

	resp, err := j.provider.Chat(ctx, messages, nil, j.model, options)
	if err != nil {
		return false, "", fmt.Errorf("goal judge LLM call failed: %w", err)
	}

	answer := resp.Content
	isDone := containsDone(answer)

	logger.DebugCF("agent", "Goal judge evaluation", map[string]interface{}{
		"goal":    goalText,
		"answer":  answer,
		"is_done": isDone,
	})

	return isDone, answer, nil
}

// containsDone checks if the judge response indicates completion.
func containsDone(s string) bool {
	upper := strings.ToUpper(s)
	return strings.Contains(upper, "DONE") && !strings.Contains(upper, "NOT DONE")
}
