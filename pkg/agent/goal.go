package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
	"github.com/xilistudios/lele/pkg/tools"
)

type GoalStatus string

const (
	GoalActive  GoalStatus = "active"
	GoalPaused  GoalStatus = "paused"
	GoalDone    GoalStatus = "done"
	GoalBlocked GoalStatus = "blocked"
)

const DefaultGoalMaxTurns = 20

type Goal struct {
	Text       string     `json:"text"`
	Status     GoalStatus `json:"status"`
	TurnsUsed  int        `json:"turns_used"`
	MaxTurns   int        `json:"max_turns"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	SessionKey string     `json:"session_key"`
}

type GoalManager struct {
	mu       sync.RWMutex
	goals    map[string]*Goal
	storeDir string
	judge    GoalJudge
	repo     *store.GoalRepo
}

type GoalJudge interface {
	JudgeGoal(ctx context.Context, sessionKey, goalText string, lastResponse string) (bool, string, error)
}

func NewGoalManager(storeDir string) *GoalManager {
	gm := &GoalManager{
		goals:    make(map[string]*Goal),
		storeDir: storeDir,
	}
	gm.loadFromDisk()
	return gm
}

func (gm *GoalManager) SetJudge(judge GoalJudge) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.judge = judge
}

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

func (gm *GoalManager) Get(sessionKey string) *Goal {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return gm.goals[sessionKey]
}

func (gm *GoalManager) IsActive(sessionKey string) bool {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	g, ok := gm.goals[sessionKey]
	return ok && g.Status == GoalActive
}

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

func (gm *GoalManager) goalFilePath(sessionKey string) string {
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
		if goal.Status == GoalActive || goal.Status == GoalPaused {
			gm.goals[goal.SessionKey] = &goal
		}
	}
}

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
	if len(result) > 128 {
		result = result[:128]
	}
	return string(result)
}

type SummaryProvider interface {
	GetSummary(sessionKey string) string
}

type ModelResolver func(sessionKey string) string

type SummaryGoalJudge struct {
	provider      providers.LLMProvider
	model         string
	summary       SummaryProvider
	modelResolver ModelResolver
	cfg           *config.Config
}

func NewSummaryGoalJudge(provider providers.LLMProvider, model string, summary SummaryProvider, resolver ModelResolver) *SummaryGoalJudge {
	return &SummaryGoalJudge{
		provider:      provider,
		model:         model,
		summary:       summary,
		modelResolver: resolver,
	}
}

func (j *SummaryGoalJudge) SetConfig(cfg *config.Config) {
	j.cfg = cfg
}

const goalJudgeSystemPrompt = `You are a goal completion judge. Your ONLY job is to evaluate whether an AI agent has achieved its assigned goal, based on the conversation summary and the agent's latest response.

Rules:
- Respond with EXACTLY one word: "DONE" if the goal is fully achieved, or "CONTINUE" if more work is needed.
- Be strict: only say DONE when the goal is genuinely and completely fulfilled.
- Consider the FULL trajectory in the summary, not just the latest response. The agent may have completed the goal several turns ago.
- If the agent is making progress but hasn't finished, say CONTINUE.
- If the agent is stuck or going in circles, say CONTINUE (the budget system handles exhaustion).
- Do NOT explain your reasoning. Only output DONE or CONTINUE.`

func buildGoalJudgePrompt(goalText, summary, lastResponse string) string {
	truncated := lastResponse
	if len(truncated) > 4000 {
		truncated = truncated[:4000] + "\n...[truncated]"
	}

	prompt := "GOAL: " + goalText + "\n\n"
	if summary != "" {
		prompt += "=== CONVERSATION SUMMARY ===\n" + summary + "\n\n"
	} else {
		prompt += "(No conversation summary available yet.)\n\n"
	}
	prompt += "=== AGENT'S LATEST RESPONSE ===\n" + truncated + "\n\n"
	prompt += "Is the goal achieved? Reply DONE or CONTINUE."
	return prompt
}

func buildSubagentJudgePrompt(goalText, summary, lastResponse string) string {
	truncated := lastResponse
	if len(truncated) > 4000 {
		truncated = truncated[:4000] + "\n...[truncated]"
	}

	prompt := "GOAL: " + goalText + "\n\n"
	if summary != "" {
		prompt += "=== CONVERSATION SUMMARY ===\n" + summary + "\n\n"
	} else {
		prompt += "(No conversation summary available yet.)\n\n"
	}
	prompt += "=== AGENT'S LATEST RESPONSE ===\n" + truncated + "\n\n"
	prompt += "Reply with EXACTLY one of these two formats:\n"
	prompt += "- DONE if the goal is fully achieved.\n"
	prompt += "- CONTINUE: <specific next step> if more work is needed, where <specific next step> is a concrete, actionable instruction for the next iteration (what to do next, what to check, what to fix, etc.).\n"
	prompt += "Be strict: only reply DONE when the goal is genuinely and completely fulfilled. Consider the full trajectory in the summary, not just the latest response."
	return prompt
}

var continueGuidanceRe = regexp.MustCompile(`(?i)CONTINUE\s*:`)

func extractContinuationGuidance(answer string) string {
	if answer == "" {
		return ""
	}
	if !strings.Contains(strings.ToUpper(answer), "CONTINUE") {
		return ""
	}
	loc := continueGuidanceRe.FindStringIndex(answer)
	if loc == nil {
		return ""
	}
	return strings.TrimSpace(answer[loc[1]:])
}

func (j *SummaryGoalJudge) JudgeGoal(ctx context.Context, sessionKey, goalText string, lastResponse string) (bool, string, error) {
	if j.provider == nil {
		return false, "", fmt.Errorf("goal judge: no provider configured")
	}

	model := j.model
	if j.modelResolver != nil {
		if m := j.modelResolver(sessionKey); m != "" {
			model = m
		}
	}

	summary := ""
	if j.summary != nil {
		summary = j.summary.GetSummary(sessionKey)
	}

	userPrompt := buildGoalJudgePrompt(goalText, summary, lastResponse)

	messages := []providers.Message{
		{Role: "system", Content: goalJudgeSystemPrompt},
		{Role: "user", Content: userPrompt},
	}

	options := map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.0,
		"reasoning": map[string]interface{}{
			"enabled": false,
		},
	}
	options = providers.WithWireSessionID(options, sessionKey)
	if j.cfg != nil {
		if tt := getThinkingType(j.cfg, model, j.cfg.Agents.Defaults.Provider); tt != "" {
			options["thinking_type"] = tt
		}
	}

	apiModel := providers.StripProviderPrefix(model)
	callProvider := j.provider
	if ref := providers.ParseModelRef(model, ""); ref != nil && ref.Provider != "" {
		agentRef := providers.ParseModelRef(j.model, "")
		if agentRef == nil || agentRef.Provider != ref.Provider {
			if j.cfg != nil {
				if newProv, err := providers.CreateProviderForCandidate(j.cfg, ref.Provider); err == nil {
					callProvider = newProv
				}
			}
		}
	}

	resp, err := providers.ChatIdle(ctx, callProvider, messages, nil, apiModel, options)
	if err != nil {
		return false, "", fmt.Errorf("goal judge LLM call failed: %w", err)
	}

	answer := strings.TrimSpace(resp.Content)
	if answer == "" {
		return false, "", fmt.Errorf("goal judge returned empty response (model %q)", apiModel)
	}
	isDone := containsDone(answer)

	logger.DebugCF("agent", "Goal judge evaluation", map[string]interface{}{
		"session_key": sessionKey,
		"goal":        goalText,
		"has_summary": summary != "",
		"answer":      answer,
		"is_done":     isDone,
	})

	return isDone, answer, nil
}

func containsDone(s string) bool {
	upper := strings.ToUpper(s)
	return strings.Contains(upper, "DONE") && !strings.Contains(upper, "NOT DONE")
}

type SubagentRunner interface {
	SpawnWithOptions(ctx context.Context, task, label, agentID, originChannel, originChatID string, callback tools.AsyncCallback, opts tools.SpawnOptions) (string, error)
	GetTask(taskID string) (*tools.SubagentTask, bool)
}

type SubagentGoalJudge struct {
	runner     SubagentRunner
	summary    SummaryProvider
	agentID    string
	originChan string
	originChat string
	timeout    time.Duration
	retries    int
}

func NewSubagentGoalJudge(runner SubagentRunner, summary SummaryProvider, agentID, originChannel, originChatID string, timeout time.Duration) *SubagentGoalJudge {
	return &SubagentGoalJudge{
		runner:     runner,
		summary:    summary,
		agentID:    agentID,
		originChan: originChannel,
		originChat: originChatID,
		timeout:    timeout,
	}
}

func (j *SubagentGoalJudge) WithRetries(n int) *SubagentGoalJudge {
	j.retries = n
	return j
}

func (j *SubagentGoalJudge) JudgeGoal(ctx context.Context, sessionKey, goalText string, lastResponse string) (bool, string, error) {
	if j.runner == nil {
		return false, "", fmt.Errorf("goal subagent judge: no subagent runner configured")
	}

	summary := ""
	if j.summary != nil {
		summary = j.summary.GetSummary(sessionKey)
	}

	taskPrompt := buildSubagentJudgePrompt(goalText, summary, lastResponse)

	originChannel := j.originChan
	if originChannel == "" {
		originChannel = "goal"
	}
	originChat := j.originChat
	if originChat == "" {
		originChat = sessionKey
	}

	taskID, err := j.runner.SpawnWithOptions(ctx, taskPrompt, "goal-evaluator", j.agentID, originChannel, originChat, nil, tools.SpawnOptions{
		MaxRetries: j.retries,
	})
	if err != nil {
		return false, "", fmt.Errorf("goal subagent judge: spawn failed: %w", err)
	}

	waitCtx := ctx
	cancel := func() {}
	if j.timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, j.timeout)
	}
	defer cancel()

	task, ok := j.runner.GetTask(taskID)
	if !ok {
		return false, "", fmt.Errorf("goal subagent judge: task %s not found", taskID)
	}

	select {
	case <-waitCtx.Done():
		return false, "", fmt.Errorf("goal subagent judge: timed out waiting for task %s", taskID)
	case <-task.DoneChannel():
	}

	task, ok = j.runner.GetTask(taskID)
	if !ok {
		return false, "", fmt.Errorf("goal subagent judge: task %s not found after completion", taskID)
	}

	answer := task.Result
	isDone := containsDone(answer)

	logger.DebugCF("agent", "Goal subagent judge evaluation", map[string]interface{}{
		"session_key": sessionKey,
		"goal":        goalText,
		"task_id":     taskID,
		"status":      task.Status,
		"has_summary": summary != "",
		"answer":      answer,
		"is_done":     isDone,
	})

	return isDone, answer, nil
}
