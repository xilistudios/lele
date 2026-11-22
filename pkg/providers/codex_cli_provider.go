package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type CodexCliProvider struct {
	command   string
	workspace string
}

func NewCodexCliProvider(workspace string) *CodexCliProvider {
	return &CodexCliProvider{
		command:   "codex",
		workspace: workspace,
	}
}

func (p *CodexCliProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	if p.command == "" {
		return nil, fmt.Errorf("codex command not configured")
	}

	prompt := p.buildPrompt(messages, tools)

	args := []string{
		"exec",
		"--json",
		"--dangerously-bypass-approvals-and-sandbox",
		"--skip-git-repo-check",
		"--color", "never",
	}
	if model != "" && model != "codex-cli" {
		args = append(args, "-m", model)
	}
	if p.workspace != "" {
		args = append(args, "-C", p.workspace)
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, p.command, args...)
	cmd.Stdin = bytes.NewReader([]byte(prompt))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if stdoutStr := stdout.String(); stdoutStr != "" {
		resp, parseErr := p.parseJSONLEvents(stdoutStr)
		if parseErr == nil && resp != nil && (resp.Content != "" || len(resp.ToolCalls) > 0) {
			return resp, nil
		}
	}

	if err != nil {
		if ctx.Err() == context.Canceled {
			return nil, ctx.Err()
		}
		if stderrStr := stderr.String(); stderrStr != "" {
			return nil, fmt.Errorf("codex cli error: %s", stderrStr)
		}
		return nil, fmt.Errorf("codex cli error: %w", err)
	}

	return p.parseJSONLEvents(stdout.String())
}

func (p *CodexCliProvider) GetDefaultModel() string {
	return "codex-cli"
}

func (p *CodexCliProvider) buildPrompt(messages []Message, tools []ToolDefinition) string {
	var systemParts []string
	var conversationParts []string

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			systemParts = append(systemParts, msg.Content)
		case "user":
			conversationParts = append(conversationParts, msg.Content)
		case "assistant":
			conversationParts = append(conversationParts, "Assistant: "+msg.Content)
		case "tool":
			conversationParts = append(conversationParts,
				fmt.Sprintf("[Tool Result for %s]: %s", msg.ToolCallID, msg.Content))
		}
	}

	var sb strings.Builder

	if len(systemParts) > 0 {
		sb.WriteString("## System Instructions\n\n")
		sb.WriteString(strings.Join(systemParts, "\n\n"))
		sb.WriteString("\n\n## Task\n\n")
	}

	if len(tools) > 0 {
		sb.WriteString(p.buildToolsPrompt(tools))
		sb.WriteString("\n\n")
	}

	if len(conversationParts) == 1 && len(systemParts) == 0 && len(tools) == 0 {
		return conversationParts[0]
	}

	sb.WriteString(strings.Join(conversationParts, "\n"))
	return sb.String()
}

func (p *CodexCliProvider) buildToolsPrompt(tools []ToolDefinition) string {
	var sb strings.Builder

	sb.WriteString("## Available Tools\n\n")
	sb.WriteString("When you need to use a tool, respond with ONLY a JSON object:\n\n")
	sb.WriteString("```json\n")
	sb.WriteString(`{"tool_calls":[{"id":"call_xxx","type":"function","function":{"name":"tool_name","arguments":"{...}"}}]}`)
	sb.WriteString("\n```\n\n")
	sb.WriteString("CRITICAL: The 'arguments' field MUST be a JSON-encoded STRING.\n\n")
	sb.WriteString("### Tool Definitions:\n\n")

	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		sb.WriteString(fmt.Sprintf("#### %s\n", tool.Function.Name))
		if tool.Function.Description != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", tool.Function.Description))
		}
		if len(tool.Function.Parameters) > 0 {
			paramsJSON, _ := json.Marshal(tool.Function.Parameters)
			sb.WriteString(fmt.Sprintf("Parameters:\n```json\n%s\n```\n", string(paramsJSON)))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

type codexEvent struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id,omitempty"`
	Message  string          `json:"message,omitempty"`
	Item     *codexEventItem `json:"item,omitempty"`
	Usage    *codexUsage     `json:"usage,omitempty"`
	Error    *codexEventErr  `json:"error,omitempty"`
}

type codexEventItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Command  string `json:"command,omitempty"`
	Status   string `json:"status,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Output   string `json:"output,omitempty"`
}

type codexUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

type codexEventErr struct {
	Message string `json:"message"`
}

func (p *CodexCliProvider) parseJSONLEvents(output string) (*LLMResponse, error) {
	var contentParts []string
	var usage *UsageInfo
	var lastError string

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event codexEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		switch event.Type {
		case "item.completed":
			if event.Item != nil && event.Item.Type == "agent_message" && event.Item.Text != "" {
				contentParts = append(contentParts, event.Item.Text)
			}
		case "turn.completed":
			if event.Usage != nil {
				promptTokens := event.Usage.InputTokens + event.Usage.CachedInputTokens
				usage = &UsageInfo{
					PromptTokens:     promptTokens,
					CompletionTokens: event.Usage.OutputTokens,
					TotalTokens:      promptTokens + event.Usage.OutputTokens,
				}
			}
		case "error":
			lastError = event.Message
		case "turn.failed":
			if event.Error != nil {
				lastError = event.Error.Message
			}
		}
	}

	if lastError != "" && len(contentParts) == 0 {
		return nil, fmt.Errorf("codex cli: %s", lastError)
	}

	content := strings.Join(contentParts, "\n")

	toolCalls := extractToolCallsFromText(content)

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		content = stripToolCallsFromText(content)
	}

	return &LLMResponse{
		Content:      strings.TrimSpace(content),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage:        usage,
	}, nil
}
