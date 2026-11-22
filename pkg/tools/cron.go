package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/utils"
)

type JobExecutor interface {
	ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error)
}

type CronTool struct {
	cronService   *cron.CronService
	executor      JobExecutor
	msgBus        *bus.MessageBus
	execTool      *ExecTool
	channel       string
	chatID        string
	sessionKey    string
	sessionExists func(sessionKey string) bool
	mu            sync.RWMutex
}

func NewCronTool(cronService *cron.CronService, executor JobExecutor, msgBus *bus.MessageBus, workspace string, restrict bool, execTimeout time.Duration, config *config.Config) *CronTool {
	execTool := NewExecToolWithConfig(workspace, restrict, config)
	execTool.SetTimeout(execTimeout)
	return &CronTool{
		cronService: cronService,
		executor:    executor,
		msgBus:      msgBus,
		execTool:    execTool,
	}
}

func (t *CronTool) Name() string {
	return "cron"
}

func (t *CronTool) Description() string {
	return "Schedule reminders, tasks, or system commands. IMPORTANT: When user asks to be reminded or scheduled, you MUST call this tool. Use 'at_seconds' for one-time reminders (e.g., 'remind me in 10 minutes' → at_seconds=600). Use 'every_seconds' ONLY for recurring tasks (e.g., 'every 2 hours' → every_seconds=7200). Use 'cron_expr' for complex recurring schedules. Use 'command' to execute shell commands directly."
}

func (t *CronTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"add", "list", "remove", "enable", "disable"},
				"description": "Action to perform. Use 'add' when user wants to schedule a reminder or task.",
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "The reminder/task message to display when triggered. If 'command' is used, this describes what the command does.",
			},
			"command": map[string]interface{}{
				"type":        "string",
				"description": "Optional: Shell command to execute directly (e.g., 'df -h'). If set, the agent will run this command and report output instead of just showing the message. 'deliver' will be forced to false for commands.",
			},
			"at_seconds": map[string]interface{}{
				"type":        "integer",
				"description": "One-time reminder: seconds from now when to trigger (e.g., 600 for 10 minutes later). Use this for one-time reminders like 'remind me in 10 minutes'.",
			},
			"every_seconds": map[string]interface{}{
				"type":        "integer",
				"description": "Recurring interval in seconds (e.g., 3600 for every hour). Use this ONLY for recurring tasks like 'every 2 hours' or 'daily reminder'.",
			},
			"cron_expr": map[string]interface{}{
				"type":        "string",
				"description": "Cron expression for complex recurring schedules (e.g., '0 9 * * *' for daily at 9am). Use this for complex recurring schedules.",
			},
			"job_id": map[string]interface{}{
				"type":        "string",
				"description": "Job ID (for remove/enable/disable)",
			},
			"deliver": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, send message directly to channel. If false, let agent process message (for complex tasks). Default: true",
			},
			"scope": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"global", "session"},
				"description": "Optional: Job scope. 'global' (default) jobs are visible to all sessions. 'session' jobs are tied to the current session and will notify the session when completed.",
			},
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Optional: Explicit session key for session-scoped jobs. If not provided, uses the current session context.",
			},
			"spawn": map[string]interface{}{
				"type":        "object",
				"description": "Optional: Configuration to spawn a subagent for this task. When set, the job will execute via a spawned subagent instead of processing directly.",
				"properties": map[string]interface{}{
					"task": map[string]interface{}{
						"type":        "string",
						"description": "The task description for the subagent",
					},
					"label": map[string]interface{}{
						"type":        "string",
						"description": "Optional short label for the task (for display)",
					},
					"agent_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional target agent ID to delegate the task to",
					},
					"guidance": map[string]interface{}{
						"type":        "string",
						"description": "Additional guidance for the subagent",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"description": "Optional model to use for the subagent (e.g., 'anthropic:claude-opus'). Defaults to the agent's configured model.",
					},
				},
				"required": []string{"task"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *CronTool) SetContext(channel, chatID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.channel = channel
	t.chatID = chatID
}

func (t *CronTool) SetSessionContext(channel, chatID, sessionKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.channel = channel
	t.chatID = chatID
	t.sessionKey = sessionKey
}

func (t *CronTool) SetSessionExistsCallback(callback func(sessionKey string) bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionExists = callback
}

func (t *CronTool) designatedSessionKey(channel, to string) string {
	if to == "" {
		return ""
	}
	if channel != "" && channel != "native" {
		return ""
	}
	t.mu.RLock()
	exists := t.sessionExists
	t.mu.RUnlock()
	if exists == nil {
		return ""
	}
	if exists(to) {
		return to
	}
	if prefixed := "native:" + to; exists(prefixed) {
		return prefixed
	}
	return ""
}

func (t *CronTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	action, ok := args["action"].(string)
	if !ok {
		return ErrorResult("action is required")
	}

	switch action {
	case "add":
		return t.addJob(ctx, args)
	case "list":
		return t.listJobs()
	case "remove":
		return t.removeJob(args)
	case "enable":
		return t.enableJob(args, true)
	case "disable":
		return t.enableJob(args, false)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

func (t *CronTool) addJob(ctx context.Context, args map[string]interface{}) *ToolResult {
	creatorAgentID, _ := AgentToolContextFromCtx(ctx)

	t.mu.RLock()
	channel := t.channel
	chatID := t.chatID
	currentSessionKey := t.sessionKey
	t.mu.RUnlock()

	if channel == "" || chatID == "" {
		return ErrorResult("no session context (channel/chat_id not set). Use this tool in an active conversation.")
	}

	message, ok := args["message"].(string)
	if !ok || message == "" {
		return ErrorResult("message is required for add")
	}

	var schedule cron.CronSchedule

	atSeconds, hasAt := args["at_seconds"].(float64)
	everySeconds, hasEvery := args["every_seconds"].(float64)
	cronExpr, hasCron := args["cron_expr"].(string)

	if hasAt {
		atMS := time.Now().UnixMilli() + int64(atSeconds)*1000
		schedule = cron.CronSchedule{
			Kind: "at",
			AtMS: &atMS,
		}
	} else if hasEvery {
		everyMS := int64(everySeconds) * 1000
		schedule = cron.CronSchedule{
			Kind:    "every",
			EveryMS: &everyMS,
		}
	} else if hasCron {
		schedule = cron.CronSchedule{
			Kind: "cron",
			Expr: cronExpr,
		}
	} else {
		return ErrorResult("one of at_seconds, every_seconds, or cron_expr is required")
	}

	deliver := true
	if d, ok := args["deliver"].(bool); ok {
		deliver = d
	}

	command, _ := args["command"].(string)
	if command != "" {
		deliver = false
	}

	var spawnConfig *cron.SpawnConfig
	if spawnRaw, ok := args["spawn"].(map[string]interface{}); ok && spawnRaw != nil {
		spawnConfig = &cron.SpawnConfig{}
		if task, ok := spawnRaw["task"].(string); ok {
			spawnConfig.Task = task
		}
		if label, ok := spawnRaw["label"].(string); ok {
			spawnConfig.Label = label
		}
		if agentID, ok := spawnRaw["agent_id"].(string); ok {
			spawnConfig.AgentID = agentID
		}
		if guidance, ok := spawnRaw["guidance"].(string); ok {
			spawnConfig.Guidance = guidance
		}
		if model, ok := spawnRaw["model"].(string); ok {
			spawnConfig.Model = model
		}
		if spawnConfig.AgentID == "" && creatorAgentID != "" {
			spawnConfig.AgentID = creatorAgentID
		}
		deliver = false
	}

	scope := "global"
	if s, ok := args["scope"].(string); ok && s != "" {
		scope = s
	}

	sessionKey := currentSessionKey
	if sk, ok := args["session_key"].(string); ok && sk != "" {
		sessionKey = sk
	}

	if sessionKey != "" && scope == "global" {
		scope = "session"
	}

	if scope == "session" && sessionKey == "" {
		return ErrorResult("session_key is required for session-scoped jobs")
	}

	messagePreview := utils.Truncate(message, 30)

	job, err := t.cronService.AddJobWithOptions(
		messagePreview,
		schedule,
		message,
		deliver,
		channel,
		chatID,
		scope,
		sessionKey,
	)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Error adding job: %v", err))
	}

	payloadSet := false
	if command != "" {
		job.Payload.Command = command
		payloadSet = true
	}
	if spawnConfig != nil {
		job.Payload.Spawn = spawnConfig
		payloadSet = true
	}
	if creatorAgentID != "" {
		job.Payload.AgentID = creatorAgentID
		payloadSet = true
	}
	if payloadSet {
		t.cronService.UpdateJob(job)
	}

	scopeInfo := ""
	if scope == "session" {
		scopeInfo = fmt.Sprintf(", scope: session, session: %s", sessionKey)
	}

	return SilentResult(fmt.Sprintf("Cron job added: %s (id: %s%s)", job.Name, job.ID, scopeInfo))
}

func (t *CronTool) listJobs() *ToolResult {
	jobs := t.cronService.ListJobs(false)

	if len(jobs) == 0 {
		return SilentResult("No scheduled jobs")
	}

	result := "Scheduled jobs:\n"
	for _, j := range jobs {
		var scheduleInfo string
		if j.Schedule.Kind == "every" && j.Schedule.EveryMS != nil {
			scheduleInfo = fmt.Sprintf("every %ds", *j.Schedule.EveryMS/1000)
		} else if j.Schedule.Kind == "cron" {
			scheduleInfo = j.Schedule.Expr
		} else if j.Schedule.Kind == "at" {
			scheduleInfo = "one-time"
		} else {
			scheduleInfo = "unknown"
		}

		scopeInfo := ""
		if j.Scope == "session" {
			scopeInfo = fmt.Sprintf(", session: %s", j.Payload.SessionKey)
		}

		result += fmt.Sprintf("- %s (id: %s, %s%s)\n", j.Name, j.ID, scheduleInfo, scopeInfo)
	}

	return SilentResult(result)
}

func (t *CronTool) removeJob(args map[string]interface{}) *ToolResult {
	jobID, ok := args["job_id"].(string)
	if !ok || jobID == "" {
		return ErrorResult("job_id is required for remove")
	}

	if t.cronService.RemoveJob(jobID) {
		return SilentResult(fmt.Sprintf("Cron job removed: %s", jobID))
	}
	return ErrorResult(fmt.Sprintf("Job %s not found", jobID))
}

func (t *CronTool) enableJob(args map[string]interface{}, enable bool) *ToolResult {
	jobID, ok := args["job_id"].(string)
	if !ok || jobID == "" {
		return ErrorResult("job_id is required for enable/disable")
	}

	job := t.cronService.EnableJob(jobID, enable)
	if job == nil {
		return ErrorResult(fmt.Sprintf("Job %s not found", jobID))
	}

	status := "enabled"
	if !enable {
		status = "disabled"
	}
	return SilentResult(fmt.Sprintf("Cron job '%s' %s", job.Name, status))
}

func (t *CronTool) ExecuteJob(ctx context.Context, job *cron.CronJob) string {
	if job.Payload.AgentID != "" {
		ctx = WithAgentToolContext(ctx, job.Payload.AgentID, job.Payload.SessionKey)
	}

	channel := job.Payload.Channel
	chatID := job.Payload.To

	isSessionScoped := job.Payload.SessionKey != ""
	effectiveSessionKey := job.Payload.SessionKey
	if effectiveSessionKey == "" {
		effectiveSessionKey = fmt.Sprintf("cron-%s", job.ID)
	}

	if channel == "" {
		channel = "native"
	}
	if chatID == "" {
		chatID = effectiveSessionKey
	}

	if job.Payload.Command != "" {
		args := map[string]interface{}{
			"command": job.Payload.Command,
		}

		result := t.execTool.Execute(ctx, args)
		var output string
		if result.IsError {
			output = fmt.Sprintf("Error executing scheduled command: %s", result.ForLLM)
		} else {
			output = fmt.Sprintf("Scheduled command '%s' executed:\n%s", job.Payload.Command, result.ForLLM)
		}

		t.msgBus.PublishOutbound(bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: output,
		})

		if isSessionScoped {
			t.notifySession(ctx, job, effectiveSessionKey, channel, chatID, output, result.IsError)
		}
		return "ok"
	}

	if job.Payload.Deliver {
		t.msgBus.PublishOutbound(bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: job.Payload.Message,
		})

		if isSessionScoped {
			t.notifySession(ctx, job, effectiveSessionKey, channel, chatID, job.Payload.Message, false)
		}
		return "ok"
	}

	if job.Payload.Spawn != nil {
		spawnMsg := formatSystemSpawnMessage(job)
		sessionKey := t.designatedSessionKey(channel, chatID)
		if sessionKey == "" {
			sessionKey = fmt.Sprintf("cron-spawn-%s", job.ID)
		}

		response, err := t.executor.ProcessDirectWithChannel(
			ctx,
			spawnMsg,
			sessionKey,
			channel,
			chatID,
		)

		if err != nil {
			if isSessionScoped {
				t.notifySession(ctx, job, effectiveSessionKey, channel, chatID, fmt.Sprintf("Error: %v", err), true)
			}
			return fmt.Sprintf("Error: %v", err)
		}

		if isSessionScoped {
			t.notifySession(ctx, job, effectiveSessionKey, channel, chatID, response, false)
		}
		return response
	}

	sessionKey := effectiveSessionKey
	if !isSessionScoped {
		sessionKey = fmt.Sprintf("cron-%s", job.ID)
		if designated := t.designatedSessionKey(channel, chatID); designated != "" {
			sessionKey = designated
		}
	}

	response, err := t.executor.ProcessDirectWithChannel(
		ctx,
		job.Payload.Message,
		sessionKey,
		channel,
		chatID,
	)

	if err != nil {
		if isSessionScoped {
			t.notifySession(ctx, job, effectiveSessionKey, channel, chatID, fmt.Sprintf("Error: %v", err), true)
		}
		return fmt.Sprintf("Error: %v", err)
	}

	if isSessionScoped {
		t.notifySession(ctx, job, effectiveSessionKey, channel, chatID, response, false)
	}

	return "ok"
}

func (t *CronTool) notifySession(ctx context.Context, job *cron.CronJob, sessionKey, channel, chatID, result string, isError bool) {
	status := "✅"
	if isError {
		status = "❌"
	}

	notification := fmt.Sprintf("%s Cron job '%s' (%s) completed:\n%s", status, job.Name, job.ID, result)

	t.msgBus.PublishOutbound(bus.OutboundMessage{
		Channel: channel,
		ChatID:  chatID,
		Content: notification,
	})
}

func formatSystemSpawnMessage(job *cron.CronJob) string {
	spawn := job.Payload.Spawn
	if spawn == nil {
		return job.Payload.Message
	}

	var parts []string
	parts = append(parts, "SYSTEM_SPAWN:")
	parts = append(parts, fmt.Sprintf("TASK: %s", spawn.Task))

	if spawn.Label != "" {
		parts = append(parts, fmt.Sprintf("LABEL: %s", spawn.Label))
	}

	if spawn.AgentID != "" {
		parts = append(parts, fmt.Sprintf("AGENT_ID: %s", spawn.AgentID))
	}

	if spawn.Guidance != "" {
		parts = append(parts, fmt.Sprintf("GUIDANCE: %s", spawn.Guidance))
	}

	if spawn.Model != "" {
		parts = append(parts, fmt.Sprintf("MODEL: %s", spawn.Model))
	}

	if job.Payload.Message != spawn.Task && job.Payload.Message != "" {
		parts = append(parts, fmt.Sprintf("CONTEXT: %s", job.Payload.Message))
	}

	return strings.Join(parts, "\n")
}
