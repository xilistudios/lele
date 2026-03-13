package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/tools"
)

func publishSubagentAsyncResult(al *AgentLoop, sessionKey, channel, chatID string, result *tools.ToolResult) {
	if al == nil || al.bus == nil || result == nil {
		return
	}

	content := strings.TrimSpace(result.ForLLM)
	if content == "" && result.Err != nil {
		content = strings.TrimSpace(result.Err.Error())
	}
	if content == "" {
		return
	}

	al.bus.PublishInbound(bus.InboundMessage{
		Channel:    "system",
		SenderID:   "subagent",
		ChatID:     fmt.Sprintf("%s:%s", channel, chatID),
		Content:    content,
		SessionKey: sessionKey,
	})
}

func formatSubagentsCommand(ctx context.Context, tc toolCoordinator, sessionKey string, args []string) string {
	if len(args) == 0 {
		return formatSubagentTaskList(tc.listRunningSubagentTasks())
	}

	switch args[0] {
	case "info":
		if len(args) < 2 {
			return "Usage: /subagents info <task_id>"
		}
		task, ok := tc.getSubagentTask(args[1])
		if !ok {
			return fmt.Sprintf("Subagent task not found: %s", args[1])
		}
		return formatSubagentTaskInfo(task)
	case "stop":
		if len(args) < 2 {
			return "Usage: /subagents stop <task_id>"
		}
		if tc.stopSubagentTask(args[1]) {
			return fmt.Sprintf("Stopping subagent task: %s", args[1])
		}
		return fmt.Sprintf("Subagent task not running: %s", args[1])
	case "continue":
		if len(args) < 3 {
			return "Usage: /subagents continue <task_id> <guidance>"
		}
		guidance := strings.TrimSpace(strings.Join(args[2:], " "))
		if guidance == "" {
			return "Usage: /subagents continue <task_id> <guidance>"
		}
		response, err := tc.continueSubagentTask(ctx, sessionKey, args[1], guidance)
		if err != nil {
			return fmt.Sprintf("Unable to continue subagent task: %v", err)
		}
		return response
	default:
		return "Usage: /subagents [info|stop|continue]"
	}
}

func formatSubagentTaskList(tasks []*tools.SubagentTask) string {
	if len(tasks) == 0 {
		return "No active or waiting subagents.\nUse /subagents info <task_id>, /subagents stop <task_id>, or /subagents continue <task_id> <guidance>."
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Created < tasks[j].Created
	})

	lines := make([]string, 0, len(tasks)+2)
	lines = append(lines, "Subagents:")
	for _, task := range tasks {
		lines = append(lines, fmt.Sprintf("- %s [%s] %s", task.ID, task.Status, formatSubagentLabel(task.Label)))
	}
	lines = append(lines, "Use /subagents info <task_id>, /subagents stop <task_id>, or /subagents continue <task_id> <guidance>.")
	return strings.Join(lines, "\n")
}

func formatSubagentTaskInfo(task *tools.SubagentTask) string {
	if task == nil {
		return "Subagent task not found"
	}

	lines := []string{
		fmt.Sprintf("Task %s", task.ID),
		fmt.Sprintf("Status: %s", task.Status),
		fmt.Sprintf("Agent: %s", formatSubagentAgent(task.AgentID)),
		fmt.Sprintf("Label: %s", formatSubagentLabel(task.Label)),
	}
	if task.Summary != "" {
		lines = append(lines, fmt.Sprintf("Summary: %s", task.Summary))
	}
	if task.ContextRequest != "" {
		lines = append(lines, fmt.Sprintf("Context needed: %s", task.ContextRequest))
	}
	if len(task.Guidance) > 0 {
		lines = append(lines, fmt.Sprintf("Guidance entries: %d", len(task.Guidance)))
	}
	if task.Result != "" {
		lines = append(lines, "Details:\n"+truncateSubagentText(task.Result, 1200))
	}
	return strings.Join(lines, "\n")
}

func formatSubagentLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "(unnamed)"
	}
	return label
}

func formatSubagentAgent(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "(default)"
	}
	return agentID
}

func truncateSubagentText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit || limit <= 0 {
		return text
	}
	return text[:limit] + "..."
}
