package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	lelecontext "github.com/xilistudios/lele/pkg/context"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/utils"
)

// subagentInfo holds the ID and description of an available subagent for
// inclusion in the system prompt.
type subagentInfo struct {
	ID          string
	Description string
}

type ContextBuilder struct {
	workspace          string
	skillsLoader       *skills.SkillsLoader
	memory             *MemoryStore
	tools              *tools.ToolRegistry
	availableSubagents []subagentInfo

	// harnessContext caches the harness module context (working directory
	// listing + AGENTS.md + skills directories). It depends only on the
	// process working directory, so it is identical for every session and is
	// cached once globally instead of once per session. Invalidated by
	// ResetMemoryContext (on /new) so a fresh conversation picks up edits.
	harnessContext      string
	harnessContextValid bool
	harnessMu           sync.RWMutex

	// initialContext caches the result of GetInitialContext() (identity +
	// bootstrap files + skills summary). It is constant for the lifetime of
	// the process, so this avoids re-reading files on every token estimation.
	initialContext string
	initialMu      sync.RWMutex

	// visionSupported indicates whether the current model supports vision.
	// When false, the read_image tool is hidden from the system prompt's
	// tools section. Defaults to false (safe default).
	visionSupported bool

	// folderResolver maps a session key to the folder the user selected for
	// that session (WebUI "per-session folder context"). When set, the
	// resolved folder's path + first-level listing are appended to the
	// session's system prompt. Guarded by folderMu because the loop wires it
	// after construction and prompt builders read it concurrently.
	folderResolver func(sessionKey string) string
	folderMu       sync.RWMutex
}

const summaryMessageHeader = "## Summary of Previous Conversation\n\n"

func getGlobalConfigDir() string {
	return config.GetLeleDir()
}

func NewContextBuilder(workspace string) *ContextBuilder {
	wd, _ := os.Getwd()
	builtinSkillsDir := filepath.Join(wd, "skills")
	globalSkillsDir := filepath.Join(getGlobalConfigDir(), "skills")

	return &ContextBuilder{
		workspace:    workspace,
		skillsLoader: skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir),
		memory:       NewMemoryStore(workspace),
	}
}

// SetToolsRegistry sets the tools registry for dynamic tool summary generation.
func (cb *ContextBuilder) SetToolsRegistry(registry *tools.ToolRegistry) {
	cb.tools = registry
}

// SetVisionSupported sets whether the current model supports vision.
// When false, the read_image tool is hidden from the system prompt's
// tools section. Invalidates cached prompts so the next turn rebuilds.
func (cb *ContextBuilder) SetVisionSupported(v bool) {
	if cb.visionSupported == v {
		return
	}
	cb.visionSupported = v
	// Invalidate caches so the tools section is rebuilt
	cb.initialMu.Lock()
	cb.initialContext = ""
	cb.initialMu.Unlock()
}

// SetAvailableSubagents sets the list of subagents that this agent can delegate
// tasks to. The list is rendered in the system prompt so the LLM knows which
// agent_id values are valid for the spawn tool.
func (cb *ContextBuilder) SetAvailableSubagents(subagents []subagentInfo) {
	cb.availableSubagents = subagents
	// Invalidate the cached initial context so the next turn picks up the new list.
	cb.initialMu.Lock()
	cb.initialContext = ""
	cb.initialMu.Unlock()
}

// GetInitialContext returns the initial context files (AGENT.md, SOUL.md, etc.)
// to be loaded at session start. This ensures consistent context across /new and subagents.
func (cb *ContextBuilder) GetInitialContext() string {
	// Fast path: the static system prompt (identity + bootstrap + skills
	// summary) is constant per process, so cache it. The only dynamic part is
	// the tools section, which is appended in buildSystemPromptForTurn via
	// GetSystemPromptForSession. This makes the very hot TUI path
	// (GetCurrentContextUsage → BuildSystemPromptForSession) read a single
	// cached string instead of re-reading all bootstrap files and re-scanning
	// skills directories on every sidebar refresh.
	cb.initialMu.RLock()
	if cb.initialContext != "" {
		cached := cb.initialContext
		cb.initialMu.RUnlock()
		return cached
	}
	cb.initialMu.RUnlock()

	cb.initialMu.Lock()
	defer cb.initialMu.Unlock()
	// Double-check under the write lock: another goroutine may have built it
	// while we waited.
	if cb.initialContext != "" {
		return cb.initialContext
	}

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

	// Subagents section
	if len(cb.availableSubagents) > 0 {
		var sb strings.Builder
		sb.WriteString("## Subagents Available\n\n")
		sb.WriteString("You can delegate tasks to these subagents using the `spawn` tool with the `agent_id` parameter.\n\n")
		for _, sa := range cb.availableSubagents {
			if sa.Description != "" {
				sb.WriteString(fmt.Sprintf("- **%s** — %s\n", sa.ID, sa.Description))
			} else {
				sb.WriteString(fmt.Sprintf("- **%s**\n", sa.ID))
			}
		}
		parts = append(parts, sb.String())
	}

	// Join with "---" separator
	cb.initialContext = strings.Join(parts, "\n\n---\n\n")
	return cb.initialContext
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

3. **Memory** - When remembering something, write to %s/MEMORY.md

4. **HTML/SVG output** - When generating HTML or SVG content for the user to view, always wrap it in a markdown code block with the appropriate language tag (e.g. html or svg).`,
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
		// Hide read_image when the model doesn't support vision
		if !cb.visionSupported && strings.Contains(s, "`read_image`") {
			continue
		}
		sb.WriteString(s)
		sb.WriteString("\n")
	}

	return sb.String()
}

func (cb *ContextBuilder) BuildSystemPrompt() string {
	return cb.GetInitialContext()
}

// BuildSystemPromptForSession returns the system prompt, appending harness module context
// if the session runs on the native/TUI channel.
func (cb *ContextBuilder) BuildSystemPromptForSession(sessionKey, channel string) string {
	prompt := cb.BuildSystemPrompt()
	isNative := channel == "native" || strings.HasPrefix(sessionKey, "tui:") || strings.HasPrefix(sessionKey, "native:")
	if isNative {
		if harnessCtx := cb.getHarnessContext(); harnessCtx != "" {
			prompt = prompt + "\n\n---\n\n" + harnessCtx
		}
	}
	return prompt
}

// SetFolderResolver wires a function that maps a session key to the folder the
// user selected for that session. Passing nil disables folder injection.
func (cb *ContextBuilder) SetFolderResolver(fn func(sessionKey string) string) {
	cb.folderMu.Lock()
	defer cb.folderMu.Unlock()
	cb.folderResolver = fn
}

// resolveFolder reads the folder resolver under the guard. Returns "" when no
// resolver is wired or the session has no folder selected.
func (cb *ContextBuilder) resolveFolder(sessionKey string) string {
	cb.folderMu.RLock()
	resolver := cb.folderResolver
	cb.folderMu.RUnlock()
	if resolver == nil {
		return ""
	}
	return resolver(sessionKey)
}

// BuildSystemPromptForSessionWithFolder returns the session's system prompt
// with the "## Selected Folder" section appended when the user selected a
// folder for the session. It extends BuildSystemPromptForSession (harness
// context included) so the turn prompt and every token estimation agree.
func (cb *ContextBuilder) BuildSystemPromptForSessionWithFolder(sessionKey, channel string) string {
	prompt := cb.BuildSystemPromptForSession(sessionKey, channel)
	if folder := cb.resolveFolder(sessionKey); folder != "" {
		if folderCtx := lelecontext.BuildFolderContext(folder); folderCtx != "" {
			prompt = prompt + "\n\n---\n\n" + folderCtx
		}
	}
	return prompt
}

// ResetMemoryContext clears the in-memory cache of the memory store
// to force a fresh reload of memory files on next access.
// Used when creating a new session with /new.
func (cb *ContextBuilder) ResetMemoryContext() {
	// Memory store reads from disk each time, so no cache to clear.
	// The initial context cache must be invalidated here too: /new re-reads
	// bootstrap files (AGENT.md, SOUL.md, ...) from disk so the fresh
	// conversation reflects any edits made while the process was running.
	cb.initialMu.Lock()
	cb.initialContext = ""
	cb.initialMu.Unlock()

	// Same for the harness context (cwd listing + AGENTS.md): re-read it so a
	// fresh conversation reflects any files created/removed while running.
	cb.harnessMu.Lock()
	cb.harnessContext = ""
	cb.harnessContextValid = false
	cb.harnessMu.Unlock()
}

// getHarnessContext returns the harness module context (cwd listing + AGENTS.md
// + skills directories), caching it globally. It is identical for every session
// in the process (depends only on os.Getwd()), so building it once and reusing
// the cached string avoids re-reading the directory and AGENTS.md on every turn.
func (cb *ContextBuilder) getHarnessContext() string {
	cb.harnessMu.RLock()
	if cb.harnessContextValid {
		cached := cb.harnessContext
		cb.harnessMu.RUnlock()
		return cached
	}
	cb.harnessMu.RUnlock()

	cb.harnessMu.Lock()
	defer cb.harnessMu.Unlock()
	if cb.harnessContextValid {
		return cb.harnessContext
	}
	cb.harnessContext, _ = lelecontext.BuildHarnessContext()
	cb.harnessContextValid = true
	return cb.harnessContext
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

// BuildMinimalSystemPrompt returns a lightweight system prompt for "chat" mode
// sessions, where only web_search and web_fetch tools are available.
func (cb *ContextBuilder) BuildMinimalSystemPrompt() string {
	return "You are a helpful AI assistant. You can search the web and fetch web pages to answer questions.\n\n" +
		"## Available Tools\n\n" +
		"- `web_search` - Search the web for current information\n" +
		"- `web_fetch` - Fetch a URL and extract readable content\n\n" +
		"## Rules\n\n" +
		"1. Use tools when you need current information or to read a specific URL.\n" +
		"2. Be helpful, accurate, and concise.\n" +
		"3. Cite sources when using web search results."
}

// BuildMessages constructs the full message list for the LLM.
//
// The expensive parts of the system prompt (identity, bootstrap files, skills,
// subagents, and the harness context) are cached globally by GetInitialContext
// and getHarnessContext, so building the prompt per turn is cheap. The session
// context (channel and chat ID) is stable per session and is appended fresh on
// every call. There is no per-turn timestamp in the prompt, so the system
// message is byte-identical across turns within a session (prompt-cache
// friendly).
func (cb *ContextBuilder) BuildMessages(history []providers.Message, summary string, currentMessage string, attachments []bus.FileAttachment, channel, chatID, sessionKey string, mode string) []providers.Message {
	messages := []providers.Message{}
	renderedUserMessage := cb.BuildCurrentUserMessage(currentMessage, attachments, channel, chatID)

	// --- Build the system prompt ---
	// The static parts (identity + bootstrap + skills + subagents + harness)
	// are cached globally, so this is cheap. The session context (channel +
	// chat ID) is stable per session and is appended fresh.
	var systemPrompt string
	if mode == "chat" {
		systemPrompt = cb.BuildMinimalSystemPrompt()
	} else {
		systemPrompt = cb.buildSystemPromptForTurn(channel, chatID, sessionKey)
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

	contextMessages := filterContextMessages(history)
	contextMessages = sanitizeToolMessages(contextMessages)
	messages = append(messages, contextMessages...)

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

func (cb *ContextBuilder) buildSystemPromptForTurn(channel, chatID, sessionKey string) string {
	systemPrompt := cb.BuildSystemPromptForSessionWithFolder(sessionKey, channel)
	sessionContext := cb.renderSessionContext(channel, chatID)
	if sessionContext == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + sessionContext
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

func filterContextMessages(history []providers.Message) []providers.Message {
	filtered := make([]providers.Message, 0, len(history))
	for _, msg := range history {
		if msg.ExcludeFromContext {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

// sanitizeToolMessages repairs tool_use/tool_result pairing in message history.
// It removes orphaned tool_results (without matching tool_use) and adds placeholder
// tool_results for missing ones. This prevents 400 errors from Anthropic/AWS Bedrock
// when tool execution is cancelled mid-way or when summarization breaks pairing.
//
// It also strips malformed tool_call entries that lack a function name. Some
// OpenAI-compatible providers (e.g. Xiaomi MiMo) reject assistant messages with
// `tool_calls[i] is missing a function name` (HTTP 400). This can happen when a
// historical message was persisted with an incomplete tool_call (e.g. a streaming
// interruption that captured only an ID, or a provider that emitted a tool_call
// placeholder without a name).
func sanitizeToolMessages(history []providers.Message) []providers.Message {
	if len(history) == 0 {
		return history
	}

	// First pass: collect ALL tool_use IDs from ALL assistant messages.
	allToolUseIDs := make(map[string]bool)
	for _, msg := range history {
		if msg.Role == "assistant" {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					allToolUseIDs[tc.ID] = true
				}
			}
		}
	}

	output := make([]providers.Message, 0, len(history))
	i := 0
	for i < len(history) {
		msg := history[i]

		if msg.Role == "tool" {
			// Remove orphaned tool_results whose ToolCallID has no matching tool_use.
			if msg.ToolCallID != "" && !allToolUseIDs[msg.ToolCallID] {
				logger.DebugCF("agent", "Removing orphaned tool_result message",
					map[string]interface{}{"tool_call_id": msg.ToolCallID})
				i++
				continue
			}
			output = append(output, msg)
			i++
			continue
		}

		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			// Strip tool_call entries that lack a function name. Some providers
			// reject assistant messages whose tool_calls omit the function name
			// (HTTP 400 "tool_calls[i] is missing a function name"). This can
			// occur in history from a streaming interruption or a provider that
			// emitted a tool_call placeholder without a name.
			if cleaned := stripNamelessToolCalls(msg.ToolCalls); len(cleaned) != len(msg.ToolCalls) {
				logger.DebugCF("agent", "Stripping tool_call entries missing a function name",
					map[string]interface{}{
						"removed": len(msg.ToolCalls) - len(cleaned),
						"kept":    len(cleaned),
					})
				msg.ToolCalls = cleaned
			}

			// Collect tool_call IDs from this assistant message.
			assistantToolCallIDs := make(map[string]bool)
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					assistantToolCallIDs[tc.ID] = true
				}
			}

			// Collect tool_result IDs from consecutive following tool messages.
			resultIDs := make(map[string]bool)
			j := i + 1
			for j < len(history) && history[j].Role == "tool" {
				if history[j].ToolCallID != "" {
					resultIDs[history[j].ToolCallID] = true
				}
				j++
			}

			// Determine which tool_use IDs are missing results.
			missingIDs := []string{}
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" && !resultIDs[tc.ID] {
					missingIDs = append(missingIDs, tc.ID)
				}
			}

			if len(missingIDs) == len(msg.ToolCalls) {
				// ALL tool_use blocks are missing results — strip them.
				logger.DebugCF("agent", "All tool results missing, stripping tool_use blocks from assistant message",
					map[string]interface{}{"missing_count": len(missingIDs)})
				msg.ToolCalls = nil
				if msg.Content == "" && msg.ReasoningContent == "" {
					// Nothing left in this message, skip it entirely.
					i = j
					continue
				}
				output = append(output, msg)
			} else if len(missingIDs) > 0 {
				// SOME tool results are missing — keep assistant message and add placeholders.
				logger.DebugCF("agent", "Some tool results missing, adding placeholder tool_result messages",
					map[string]interface{}{"missing_count": len(missingIDs)})
				output = append(output, msg)
				// Append existing tool_result messages.
				for k := i + 1; k < j; k++ {
					output = append(output, history[k])
				}
				// Add placeholders for missing IDs.
				for _, id := range missingIDs {
					output = append(output, providers.Message{
						Role:       "tool",
						Content:    "[Tool execution was cancelled]",
						ToolCallID: id,
					})
				}
			} else {
				// All results present — keep as-is.
				output = append(output, msg)
				for k := i + 1; k < j; k++ {
					output = append(output, history[k])
				}
			}
			i = j
			continue
		}

		// Regular message — keep as-is.
		output = append(output, msg)
		i++
	}

	return output
}

// stripNamelessToolCalls removes tool_call entries that lack a function name.
// A valid tool_call must have either a top-level Name or a Function.Name.
// Malformed entries (e.g. from a streaming interruption that captured only an
// ID, or a provider that emitted a placeholder without a name) cause HTTP 400
// errors on OpenAI-compatible APIs and must be filtered out before sending.
func stripNamelessToolCalls(toolCalls []providers.ToolCall) []providers.ToolCall {
	if len(toolCalls) == 0 {
		return toolCalls
	}
	cleaned := make([]providers.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		name := tc.Name
		if name == "" && tc.Function != nil {
			name = tc.Function.Name
		}
		if strings.TrimSpace(name) == "" {
			continue
		}
		cleaned = append(cleaned, tc)
	}
	return cleaned
}

// dropBlankAssistantMessages removes persisted assistant messages that carry
// neither content nor tool calls. Such blanks are the footprint of an older
// bug (empty responses were saved to the session before the empty-retry
// check), and models imitate them: replaying blanks triggers fresh blanks,
// permanently stalling the session. Dropping them is safe for the API because
// reasoning_content is never re-serialized to providers. Returns the cleaned
// slice and whether anything was removed.
func dropBlankAssistantMessages(history []providers.Message) ([]providers.Message, bool) {
	isBlank := func(m providers.Message) bool {
		return m.Role == "assistant" && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0
	}
	removed := false
	cleaned := make([]providers.Message, 0, len(history))
	for _, m := range history {
		if isBlank(m) {
			removed = true
			continue
		}
		cleaned = append(cleaned, m)
	}
	if !removed {
		return history, false
	}
	return cleaned, true
}

// ensureSummaryMaterialized ensures the summary is materialized as a message in the history.
// Returns the updated history (may be the same slice if no changes needed).
func ensureSummaryMaterialized(agent *AgentInstance, sessionKey string, history []providers.Message, summary string) []providers.Message {
	if agent == nil || agent.Sessions == nil || sessionKey == "" || summary == "" || hasSummaryMessage(history, summary) {
		return history
	}

	agent.Sessions.GetOrCreate(sessionKey)
	updatedHistory := make([]providers.Message, 0, len(history)+1)
	updatedHistory = append(updatedHistory, history...)
	updatedHistory = append(updatedHistory, buildSummaryMessage(summary))
	agent.Sessions.SetHistory(sessionKey, updatedHistory)
	return updatedHistory
}

func (cb *ContextBuilder) BuildCurrentUserMessage(currentMessage string, attachments []bus.FileAttachment, channel, chatID string) string {
	return cb.RenderUserMessage(currentMessage, attachments)
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

	// Strip :chat: alias if present
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
