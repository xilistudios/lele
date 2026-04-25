package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/utils"
)

type ContextBuilder struct {
	workspace    string
	skillsLoader *skills.SkillsLoader
	memory       *MemoryStore
	tools        *tools.ToolRegistry

	// cachedSystemPrompt stores the built system prompt per session key.
	// It is populated on the first call for a session and reused on every
	// subsequent turn so that providers with prompt caching (Anthropic, etc.)
	// see a byte-for-byte identical system message.
	cachedSystemPrompt map[string]string
	cacheMu            sync.RWMutex
}

const summaryMessageHeader = "## Summary of Previous Conversation\n\n"

func getGlobalConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".lele")
}

func NewContextBuilder(workspace string) *ContextBuilder {
	wd, _ := os.Getwd()
	builtinSkillsDir := filepath.Join(wd, "skills")
	globalSkillsDir := filepath.Join(getGlobalConfigDir(), "skills")

	return &ContextBuilder{
		workspace:          workspace,
		skillsLoader:       skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir),
		memory:             NewMemoryStore(workspace),
		cachedSystemPrompt: make(map[string]string),
	}
}

// SetToolsRegistry sets the tools registry for dynamic tool summary generation.
func (cb *ContextBuilder) SetToolsRegistry(registry *tools.ToolRegistry) {
	cb.tools = registry
}

// GetInitialContext returns the initial context files (AGENT.md, SOUL.md, etc.)
// to be loaded at session start. This ensures consistent context across /new and subagents.
func (cb *ContextBuilder) GetInitialContext() string {
	parts := []string{}

	// Core identity section
	parts = append(parts, cb.getIdentity())

	// Bootstrap files - ALWAYS included for consistent context
	bootstrapContent := cb.LoadBootstrapFiles()
	if bootstrapContent != "" {
		parts = append(parts, bootstrapContent)
	}

	// Skills summary
	skillsSummary := cb.skillsLoader.BuildSkillsSummary()
	if skillsSummary != "" {
		parts = append(parts, fmt.Sprintf(`# Skills

The following skills extend your capabilities. To use a skill, read its SKILL.md file using the read_file tool.

%s`, skillsSummary))
	}

	// Join with "---" separator
	return strings.Join(parts, "\n\n---\n\n")
}

func (cb *ContextBuilder) getIdentity() string {
	workspacePath, _ := filepath.Abs(filepath.Join(cb.workspace))
	rt := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// Build tools section dynamically
	toolsSection := cb.buildToolsSection()

	return fmt.Sprintf(`# lele 🦞

You are lele, a helpful AI assistant.

## Runtime
%s

## Workspace
Your workspace is at: %s
- Memory: %s/MEMORY.md
- Skills: %s/skills/{skill-name}/SKILL.md

%s

## Important Rules

	1. **ALWAYS use tools for actions** - When you need to perform an action (schedule reminders, execute commands, create files, etc.), you MUST call the appropriate tool. If you just need to talk to the user, respond normally.

2. **Be helpful and accurate** - When using tools, briefly explain what you're doing.

3. **Memory** - When remembering something, write to %s/MEMORY.md`,
		rt, workspacePath, workspacePath, workspacePath, toolsSection, workspacePath)
}

func (cb *ContextBuilder) buildToolsSection() string {
	if cb.tools == nil {
		return ""
	}

	summaries := cb.tools.GetSummaries()
	if len(summaries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Tools\n\n")
	sb.WriteString("**CRITICAL**: You MUST use tools to perform actions. Do NOT pretend to execute commands or schedule tasks.\n\n")
	sb.WriteString("You have access to the following tools:\n\n")
	for _, s := range summaries {
		sb.WriteString(s)
		sb.WriteString("\n")
	}

	return sb.String()
}

func (cb *ContextBuilder) BuildSystemPrompt() string {
	return cb.GetInitialContext()
}

// ResetMemoryContext clears the in-memory cache of the memory store
// to force a fresh reload of memory files on next access.
// Used when creating a new session with /new.
func (cb *ContextBuilder) ResetMemoryContext() {
	// Memory store reads from disk each time, so no cache to clear
	// But we could add caching here in the future if needed
}

// ResetSystemPromptCache clears the cached system prompt for the given session key,
// forcing a fresh build on the next call. Call this on /new or ephemeral reset.
func (cb *ContextBuilder) ResetSystemPromptCache(sessionKey string) {
	cb.cacheMu.Lock()
	defer cb.cacheMu.Unlock()
	delete(cb.cachedSystemPrompt, sessionKey)
}

// ResetAllSystemPromptCaches clears every cached system prompt.
func (cb *ContextBuilder) ResetAllSystemPromptCaches() {
	cb.cacheMu.Lock()
	defer cb.cacheMu.Unlock()
	cb.cachedSystemPrompt = make(map[string]string)
}

func (cb *ContextBuilder) loadCachedSystemPrompt(sessionKey string) string {
	if sessionKey == "" {
		return ""
	}
	cb.cacheMu.RLock()
	defer cb.cacheMu.RUnlock()
	return cb.cachedSystemPrompt[sessionKey]
}

func (cb *ContextBuilder) storeCachedSystemPrompt(sessionKey, prompt string) {
	if sessionKey == "" {
		return
	}
	cb.cacheMu.Lock()
	defer cb.cacheMu.Unlock()
	cb.cachedSystemPrompt[sessionKey] = prompt
}

func (cb *ContextBuilder) LoadBootstrapFiles() string {
	bootstrapFiles := []string{"AGENT.md", "SOUL.md", "USER.md", "IDENTITY.md", "MEMORY.md"}

	var result string
	for i, filename := range bootstrapFiles {
		filePath := filepath.Join(cb.workspace, filename)
		if data, err := os.ReadFile(filePath); err == nil {
			if i > 0 {
				result += "\n----\n"
			}
			result += fmt.Sprintf("## %s\n%s\n", filename, string(data))
		}
	}

	return result
}

// BuildMessages constructs the full message list for the LLM.
//
// To keep prompt caching effective on providers like Anthropic, the system prompt
// MUST be byte-for-byte identical across turns inside the same session.  We therefore
// cache the computed system prompt per session key and reuse it on subsequent calls
// (e.g. tool-turns).
func (cb *ContextBuilder) BuildMessages(history []providers.Message, summary string, currentMessage string, attachments []bus.FileAttachment, channel, chatID, sessionKey string) []providers.Message {
	messages := []providers.Message{}
	renderedUserMessage := cb.BuildCurrentUserMessage(currentMessage, attachments, channel, chatID)

	// --- Build the static system prompt ---
	// On the first turn for a session we build it fresh and cache it.
	// On every later turn we reuse the exact same string.
	var systemPrompt string
	if sessionKey != "" {
		if cached := cb.loadCachedSystemPrompt(sessionKey); cached != "" {
			systemPrompt = cached
			logger.DebugCF("agent", "Reusing cached system prompt",
				map[string]interface{}{
					"session_key": sessionKey,
					"length":      len(systemPrompt),
				})
		} else {
			systemPrompt = cb.buildSystemPromptForTurn(currentMessage, channel, chatID)
			cb.storeCachedSystemPrompt(sessionKey, systemPrompt)
			logger.DebugCF("agent", "Built and cached new system prompt",
				map[string]interface{}{
					"session_key": sessionKey,
					"length":      len(systemPrompt),
				})
		}
	} else {
		// No session tracking — build fresh every time (safe fallback)
		systemPrompt = cb.buildSystemPromptForTurn(currentMessage, channel, chatID)
	}

	// Debug logging
	logger.DebugCF("agent", "System prompt ready",
		map[string]interface{}{
			"total_chars":   len(systemPrompt),
			"total_lines":   strings.Count(systemPrompt, "\n") + 1,
			"section_count": strings.Count(systemPrompt, "\n\n---\n\n") + 1,
		})
	preview := systemPrompt
	if len(preview) > 500 {
		preview = preview[:500] + "... (truncated)"
	}
	logger.DebugCF("agent", "System prompt preview",
		map[string]interface{}{"preview": preview})

	messages = append(messages, providers.Message{
		Role:    "system",
		Content: systemPrompt,
	})

	// --- Strip orphaned leading tool messages ---
	for len(history) > 0 && history[0].Role == "tool" {
		logger.DebugCF("agent", "Removing orphaned tool message from history to prevent LLM error",
			map[string]interface{}{"role": history[0].Role})
		history = history[1:]
	}

	messages = append(messages, history...)

	if summary != "" && !hasSummaryMessage(history, summary) {
		messages = append(messages, buildSummaryMessage(summary))
	}

	if renderedUserMessage != "" {
		messages = append(messages, providers.Message{
			Role:    "user",
			Content: renderedUserMessage,
		})
	}

	return messages
}

func (cb *ContextBuilder) buildSystemPromptForTurn(currentMessage, channel, chatID string) string {
	systemPrompt := cb.BuildSystemPrompt()
	requestContext := cb.renderRequestContext(currentMessage, channel, chatID)
	if requestContext == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + requestContext
}

func buildSummaryMessage(summary string) providers.Message {
	return providers.Message{
		Role:    "user",
		Content: summaryMessageHeader + summary,
	}
}

func isSummaryMessage(msg providers.Message) bool {
	return msg.Role == "user" && strings.HasPrefix(msg.Content, summaryMessageHeader)
}

func hasSummaryMessage(history []providers.Message, summary string) bool {
	if summary == "" {
		return false
	}
	expected := summaryMessageHeader + summary
	for _, msg := range history {
		if msg.Role == "user" && msg.Content == expected {
			return true
		}
	}
	return false
}

func stripSummaryMessages(history []providers.Message) []providers.Message {
	filtered := make([]providers.Message, 0, len(history))
	for _, msg := range history {
		if isSummaryMessage(msg) {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

func (cb *ContextBuilder) BuildCurrentUserMessage(currentMessage string, attachments []bus.FileAttachment, channel, chatID string) string {
	return cb.RenderUserMessage(currentMessage, attachments)
}

func (cb *ContextBuilder) renderRequestContext(currentMessage, channel, chatID string) string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(currentMessage) != "" {
		parts = append(parts, fmt.Sprintf("Current Time: %s", time.Now().Format("2006-01-02 15:04 (Monday)")))
	}
	if sessionContext := cb.renderSessionContext(channel, chatID); sessionContext != "" {
		parts = append(parts, sessionContext)
	}
	return strings.Join(parts, "\n\n")
}

func (cb *ContextBuilder) renderSessionContext(channel, chatID string) string {
	if channel == "" && chatID == "" {
		return ""
	}

	if channel == channels.ChannelName {
		chatID = normalizeNativeChatID(chatID)
	}

	lines := []string{"## Current Session"}
	if channel != "" {
		lines = append(lines, fmt.Sprintf("Channel: %s", channel))
	}
	if chatID != "" {
		lines = append(lines, fmt.Sprintf("Chat ID: %s", chatID))
	}

	return strings.Join(lines, "\n")
}

func normalizeNativeChatID(chatID string) string {
	if !strings.HasPrefix(chatID, "native:") {
		return chatID
	}

	parts := strings.Split(chatID, ":")
	if len(parts) < 3 {
		return chatID
	}

	last := parts[len(parts)-1]
	allDigits := last != ""
	for _, ch := range last {
		if ch < '0' || ch > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return strings.Join(parts[:len(parts)-1], ":")
	}

	if len(parts) >= 4 && parts[len(parts)-2] == "chat" {
		return strings.Join(parts[:len(parts)-2], ":")
	}

	return chatID
}
func (cb *ContextBuilder) RenderUserMessage(currentMessage string, attachments []bus.FileAttachment) string {
	content := strings.TrimSpace(currentMessage)
	attachmentContext := utils.BuildAttachmentContext(attachments)
	if attachmentContext == "" {
		return content
	}
	if content == "" {
		return attachmentContext
	}
	return content + "\n\n" + attachmentContext
}

func (cb *ContextBuilder) AddToolResult(messages []providers.Message, toolCallID, toolName, result string) []providers.Message {
	messages = append(messages, providers.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: toolCallID,
	})
	return messages
}

func (cb *ContextBuilder) AddAssistantMessage(messages []providers.Message, content string, toolCalls []map[string]interface{}) []providers.Message {
	msg := providers.Message{
		Role:    "assistant",
		Content: content,
	}
	// Always add assistant message, whether or not it has tool calls
	messages = append(messages, msg)
	return messages
}

func (cb *ContextBuilder) loadSkills() string {
	allSkills := cb.skillsLoader.ListSkills()
	if len(allSkills) == 0 {
		return ""
	}

	var skillNames []string
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}

	content := cb.skillsLoader.LoadSkillsForContext(skillNames)
	if content == "" {
		return ""
	}

	return "# Skill Definitions\n\n" + content
}

// GetSkillsInfo returns information about loaded skills.
func (cb *ContextBuilder) GetSkillsInfo() map[string]interface{} {
	allSkills := cb.skillsLoader.ListSkills()
	skillNames := make([]string, 0, len(allSkills))
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}
	return map[string]interface{}{
		"total":     len(allSkills),
		"available": len(allSkills),
		"names":     skillNames,
	}
}
