package tools

import (
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/providers"
)

func (task *SubagentTask) displayLabel() string {
	if strings.TrimSpace(task.Label) == "" {
		return "(unnamed)"
	}
	return task.Label
}

// Delivered returns whether this task's result has been marked as delivered.
func (task *SubagentTask) Delivered() bool {
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	return task.delivered
}

func (task *SubagentTask) buildMessages(systemPrompt string) []providers.Message {
	messages := []providers.Message{
		{
			Role:    "system",
			Content: systemPrompt,
		},
		{
			Role:    "user",
			Content: task.Task,
		},
	}

	if task.Result != "" || task.ContextRequest != "" || task.Summary != "" {
		previous := []string{
			"Previous progress report:",
			fmt.Sprintf("STATUS: %s", task.Status),
		}
		if task.Summary != "" {
			previous = append(previous, fmt.Sprintf("SUMMARY: %s", task.Summary))
		}
		if task.ContextRequest != "" {
			previous = append(previous, fmt.Sprintf("CONTEXT_NEEDED: %s", task.ContextRequest))
		}
		if task.Result != "" {
			previous = append(previous, "DETAILS:", task.Result)
		}
		messages = append(messages, providers.Message{
			Role:    "assistant",
			Content: strings.Join(previous, "\n"),
		})
	}

	if len(task.Guidance) > 0 {
		messages = append(messages, providers.Message{
			Role: "user",
			Content: "Additional guidance from the parent agent/user:\n" +
				strings.Join(task.Guidance, "\n\n") +
				"\n\nContinue the original task without repeating completed work.",
		})
	}

	return messages
}

func (task *SubagentTask) statusMessage() string {
	lines := []string{
		"Subagent status update.",
		fmt.Sprintf("Task ID: %s", task.ID),
		fmt.Sprintf("Label: %s", task.displayLabel()),
	}
	if task.AgentID != "" {
		lines = append(lines, fmt.Sprintf("Agent: %s", task.AgentID))
	}
	lines = append(lines, fmt.Sprintf("Status: %s", task.Status))
	if task.Summary != "" {
		lines = append(lines, fmt.Sprintf("Summary: %s", task.Summary))
	}
	if task.ContextRequest != "" {
		lines = append(lines, fmt.Sprintf("Context needed: %s", task.ContextRequest))
	}
	if task.Result != "" {
		lines = append(lines, "Details:\n"+task.Result)
	}

	switch task.Status {
	case SubagentStatusNeedsContext:
		lines = append(lines,
			fmt.Sprintf("The subagent is paused waiting for guidance. Continue it with /subagents continue %s <guidance> once the missing context is available.", task.ID),
		)
	case SubagentStatusNotDone:
		lines = append(lines,
			"The subagent could not complete the task with the current constraints. Decide whether to retry, re-scope, or ask the user for a different plan.",
		)
	case SubagentStatusCompleted:
		lines = append(lines,
			"The subagent finished successfully. Decide whether to reply to the user directly or keep processing the result.",
		)
	}

	return strings.Join(lines, "\n")
}
