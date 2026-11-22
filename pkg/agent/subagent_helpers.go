package agent

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

var (
	toolLineRegex         = regexp.MustCompile(`(?m)^🛠️ \w+:.*$`)
	multipleNewlinesRegex = regexp.MustCompile(`\n{3,}`)
)

func publishSubagentAsyncResult(al *AgentLoop, sessionKey, channel, chatID, taskID string, result *tools.ToolResult) {
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

	if al.verboseManager != nil {
		level := al.verboseManager.GetLevel(sessionKey)
		if level == session.VerboseBasic {
			content = formatBasicVerboseSubagentContent(content)
		}
	}

	metadata := map[string]string{
		"task_id": taskID,
		"result":  content,
	}
	if result.Metadata != nil {
		metadata["subagent_session_key"] = result.Metadata["subagent_session_key"]
	}

	resumed := al.toolCoordinator != nil &&
		al.toolCoordinator.claimSubagentResultForInjection(taskID)

	injected := false
	if resumed {
		injected = al.enqueueMidTurnNotice(sessionKey, content, taskID)
	}

	if resumed {
		metadata["resumed"] = "true"
	} else {
		metadata["resumed"] = "false"
	}
	if injected {
		metadata["injected"] = "true"
	}

	al.bus.PublishOutbound(bus.OutboundMessage{
		Channel:  channel,
		ChatID:   sessionKey,
		Event:    "subagent.result",
		Metadata: metadata,
	})

	if !resumed || injected {
		return
	}

	al.bus.PublishInbound(bus.InboundMessage{
		Channel:    "system",
		SenderID:   midTurnNoticeSender,
		ChatID:     fmt.Sprintf("%s:%s", channel, chatID),
		Content:    content,
		SessionKey: sessionKey,
		Metadata:   map[string]string{"task_id": taskID},
	})
}

func formatBasicVerboseSubagentContent(content string) string {
	if content == "" {
		return content
	}

	lines := strings.Split(content, "\n")
	var resultLines []string
	var inDetails bool

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if toolLineRegex.MatchString(trimmed) {
			if i == len(lines)-1 {
				resultLines = append(resultLines, line)
			}
			continue
		}

		if trimmed == "" && i > 0 && toolLineRegex.MatchString(strings.TrimSpace(lines[i-1])) {
			continue
		}

		if strings.HasPrefix(trimmed, "📤") || strings.HasPrefix(trimmed, "📥") {
			continue
		}

		if strings.HasPrefix(trimmed, "→") || strings.HasPrefix(trimmed, "Result:") {
			continue
		}

		isDetailsHeader := strings.HasPrefix(trimmed, "Details:") || strings.HasPrefix(trimmed, "DETAILS:")

		if strings.HasPrefix(trimmed, "STATUS:") ||
			strings.HasPrefix(trimmed, "Summary:") ||
			strings.HasPrefix(trimmed, "SUMMARY:") ||
			strings.HasPrefix(trimmed, "Details:") ||
			strings.HasPrefix(trimmed, "DETAILS:") ||
			strings.HasPrefix(trimmed, "Context needed:") ||
			strings.HasPrefix(trimmed, "CONTEXT_NEEDED:") {
			resultLines = append(resultLines, line)
			if isDetailsHeader {
				inDetails = true
			}
			continue
		}

		if inDetails {
			if len(trimmed) > 300 {
				leadingSpaces := len(line) - len(strings.TrimLeft(line, " \t"))
				if leadingSpaces > 0 {
					resultLines = append(resultLines, line[:leadingSpaces]+trimmed[:297]+"...")
				} else {
					resultLines = append(resultLines, trimmed[:297]+"...")
				}
			} else {
				resultLines = append(resultLines, line)
			}
			continue
		}

		resultLines = append(resultLines, line)
	}

	result := strings.Join(resultLines, "\n")
	result = multipleNewlinesRegex.ReplaceAllString(result, "\n\n")

	return strings.TrimSpace(result)
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
