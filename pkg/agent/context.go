package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
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

type subagentInfo struct {
	ID          string
	Description string
}

type mcpDiscoveryPerms struct {
	List bool
	Get  bool
}

type ContextBuilder struct {
	workspace          string
	skillsLoader       *skills.SkillsLoader
	memory             *MemoryStore
	tools              *tools.ToolRegistry
	availableSubagents []subagentInfo

	mcpServers []tools.MCPServerInfo

	mcpDiscovery mcpDiscoveryPerms

	harnessContext      string
	harnessContextValid bool
	harnessMu           sync.RWMutex

	initialContext string
	initialMu      sync.RWMutex

	visionSupported bool

	videoSupported bool

	skillsFilter    []string
	skillUsage      *skills.UsageStore
	skillUsageAgent string
	loadedSkills    []string

	folderResolver func(sessionKey string) string
	folderMu       sync.RWMutex

	subagentLedgerResolver func(sessionKey string) string
	ledgerMu               sync.RWMutex
}

const summaryMessageHeader = "## Summary of Previous Conversation\n\n"

const maxContextSkills = 5

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

func (cb *ContextBuilder) SetToolsRegistry(registry *tools.ToolRegistry) {
	cb.tools = registry
}

func (cb *ContextBuilder) SetVisionSupported(v bool) {
	cb.initialMu.Lock()
	if cb.visionSupported == v {
		cb.initialMu.Unlock()
		return
	}
	cb.visionSupported = v
	cb.initialContext = ""
	cb.initialMu.Unlock()
}

func (cb *ContextBuilder) SetVideoSupported(v bool) {
	cb.initialMu.Lock()
	if cb.videoSupported == v {
		cb.initialMu.Unlock()
		return
	}
	cb.videoSupported = v
	cb.initialContext = ""
	cb.initialMu.Unlock()
}

func (cb *ContextBuilder) SetAvailableSubagents(subagents []subagentInfo) {
	cb.availableSubagents = subagents
	cb.initialMu.Lock()
	cb.initialContext = ""
	cb.initialMu.Unlock()
}

func (cb *ContextBuilder) SetMCPServers(servers []tools.MCPServerInfo) {
	cb.initialMu.Lock()
	defer cb.initialMu.Unlock()
	if mcpServersEqual(cb.mcpServers, servers) {
		return
	}
	cb.mcpServers = servers
	cb.initialContext = ""
}

func (cb *ContextBuilder) SetMCPDiscovery(list, get bool) {
	next := mcpDiscoveryPerms{List: list, Get: get}
	cb.initialMu.Lock()
	defer cb.initialMu.Unlock()
	if cb.mcpDiscovery == next {
		return
	}
	cb.mcpDiscovery = next
	cb.initialContext = ""
}

func mcpServersSection(servers []tools.MCPServerInfo, perms mcpDiscoveryPerms) string {
	var sb strings.Builder
	sb.WriteString("## MCP Servers\n\n")
	switch {
	case perms.List && perms.Get:
		sb.WriteString("Use `list_mcp_tools` with a server name to browse its tools (paginated, most-used first), " +
			"then `get_mcp_tool` with {\"server\": \"<name>\", \"tool\": \"<tool>\"} — or \"tools\": [\"<t1>\", \"<t2>\"] for several — " +
			"to load full schemas and make them callable.\n\n")
	case perms.List:
		sb.WriteString("Use `list_mcp_tools` with a server name to browse its tools (paginated, most-used first).\n\n")
	case perms.Get:
		sb.WriteString("Use `get_mcp_tool` with {\"server\": \"<name>\", \"tool\": \"<tool>\"} — or \"tools\": [\"<t1>\", \"<t2>\"] for several — " +
			"to load full schemas and make them callable.\n\n")
	}
	for _, s := range servers {
		if s.Description != "" {
			sb.WriteString(fmt.Sprintf("- **%s** — %s\n", s.Name, s.Description))
		} else {
			sb.WriteString(fmt.Sprintf("- **%s**\n", s.Name))
		}
	}
	return sb.String()
}

func mcpServersEqual(a, b []tools.MCPServerInfo) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(s tools.MCPServerInfo) string {
		return s.Name + "\x00" + s.Description + "\x00" + s.Layer
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[key(s)]++
	}
	for _, s := range b {
		k := key(s)
		if seen[k] == 0 {
			return false
		}
		seen[k]--
	}
	return true
}

func (cb *ContextBuilder) SetSkillsFilter(filter []string) {
	cb.initialMu.Lock()
	cb.skillsFilter = filter
	cb.initialContext = ""
	cb.initialMu.Unlock()
}

func (cb *ContextBuilder) SkillsLoader() *skills.SkillsLoader {
	return cb.skillsLoader
}

func (cb *ContextBuilder) SetSkillUsage(agentID string, store *skills.UsageStore) {
	cb.initialMu.Lock()
	cb.skillUsageAgent = agentID
	cb.skillUsage = store
	cb.initialContext = ""
	cb.initialMu.Unlock()
}

func (cb *ContextBuilder) AddLoadedSkills(names []string) {
	cb.initialMu.Lock()
	defer cb.initialMu.Unlock()
	for _, name := range names {
		if name == "" || slices.Contains(cb.loadedSkills, name) {
			continue
		}
		cb.loadedSkills = append(cb.loadedSkills, name)
	}
	cb.initialContext = ""
}

func (cb *ContextBuilder) SkillsInContext() []string {
	cb.initialMu.RLock()
	filter := cb.skillsFilter
	usage := cb.skillUsage
	agentID := cb.skillUsageAgent
	loaded := cb.loadedSkills
	cb.initialMu.RUnlock()

	var names []string
	for _, s := range cb.skillsLoader.TopSkills(filter, usage.CountsFor(agentID), maxContextSkills) {
		names = append(names, s.Name)
	}
	for _, name := range loaded {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

func (cb *ContextBuilder) InvalidatePromptCache() {
	cb.initialMu.Lock()
	cb.initialContext = ""
	cb.initialMu.Unlock()

	cb.harnessMu.Lock()
	cb.harnessContext = ""
	cb.harnessContextValid = false
	cb.harnessMu.Unlock()
}

func (cb *ContextBuilder) GetInitialContext() string {
	cb.initialMu.RLock()
	if cb.initialContext != "" {
		cached := cb.initialContext
		cb.initialMu.RUnlock()
		return cached
	}
	cb.initialMu.RUnlock()

	cb.initialMu.Lock()
	defer cb.initialMu.Unlock()
	if cb.initialContext != "" {
		return cb.initialContext
	}

	parts := []string{}

	parts = append(parts, cb.getIdentity())

	bootstrapContent := cb.LoadBootstrapFiles()
	if bootstrapContent != "" {
		parts = append(parts, bootstrapContent)
	}

	skillsSummary := cb.skillsLoader.BuildTopSkillsSummary(cb.skillsFilter, cb.skillUsage.CountsFor(cb.skillUsageAgent), maxContextSkills)
	if skillsSummary != "" {
		parts = append(parts, fmt.Sprintf(`# Skills

The skills below are the ones this agent uses most. The catalog has more: use `+"`list_skills`"+` to browse it (most used first) or `+"`search_skill`"+` to find skills by keyword, and `+"`load_skill`"+` with {"skill": "<name>"} — or "skills": ["<a>", "<b>"] for several — to load their full instructions into the context.

%s`, skillsSummary))
	}

	if len(cb.loadedSkills) > 0 {
		if content := cb.skillsLoader.LoadSkillsForContext(cb.loadedSkills); content != "" {
			parts = append(parts, "# Loaded Skills\n\n"+content)
		}
	}

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

	if len(cb.mcpServers) > 0 {
		servers := make([]tools.MCPServerInfo, len(cb.mcpServers))
		copy(servers, cb.mcpServers)
		sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
		parts = append(parts, mcpServersSection(servers, cb.mcpDiscovery))
	}

	cb.initialContext = strings.Join(parts, "\n\n---\n\n")
	return cb.initialContext
}

func (cb *ContextBuilder) getIdentity() string {
	workspacePath, _ := filepath.Abs(filepath.Join(cb.workspace))
	rt := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	toolsSection := cb.buildToolsSection()

	return fmt.Sprintf(`# lele

You are lele, a helpful AI assistant.

## Runtime
%s

## Workspace
Your workspace is at: %s
- Skills: %s/skills/{skill-name}/SKILL.md

%s

## Important Rules

	1. **ALWAYS use tools for actions** - When you need to perform an action (schedule reminders, execute commands, create files, etc.), you MUST call the appropriate tool. If you just need to talk to the user, respond normally.

2. **Be helpful and accurate** - When using tools, briefly explain what you're doing.

3. **Memory** - When remembering something, use the `+"`update_memory`"+` tool

4. **HTML/SVG output** - When generating HTML or SVG content for the user to view, always wrap it in a markdown code block with the appropriate language tag (e.g. html or svg).`,
		rt, workspacePath, workspacePath, toolsSection)
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
		if !cb.visionSupported && strings.Contains(s, "`read_image`") {
			continue
		}
		if !cb.videoSupported && !cb.visionSupported && strings.Contains(s, "`read_video`") {
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

func (cb *ContextBuilder) SetFolderResolver(fn func(sessionKey string) string) {
	cb.folderMu.Lock()
	defer cb.folderMu.Unlock()
	cb.folderResolver = fn
}

func (cb *ContextBuilder) resolveFolder(sessionKey string) string {
	cb.folderMu.RLock()
	resolver := cb.folderResolver
	cb.folderMu.RUnlock()
	if resolver == nil {
		return ""
	}
	return resolver(sessionKey)
}

func (cb *ContextBuilder) SetSubagentLedgerResolver(fn func(sessionKey string) string) {
	cb.ledgerMu.Lock()
	defer cb.ledgerMu.Unlock()
	cb.subagentLedgerResolver = fn
}

func (cb *ContextBuilder) resolveSubagentLedger(sessionKey string) string {
	cb.ledgerMu.RLock()
	resolver := cb.subagentLedgerResolver
	cb.ledgerMu.RUnlock()
	if resolver == nil {
		return ""
	}
	return resolver(sessionKey)
}

func (cb *ContextBuilder) BuildSystemPromptForSessionWithFolder(sessionKey, channel string) string {
	prompt := cb.BuildSystemPromptForSession(sessionKey, channel)
	if folder := cb.resolveFolder(sessionKey); folder != "" {
		if folderCtx := lelecontext.BuildFolderContext(folder); folderCtx != "" {
			prompt = prompt + "\n\n---\n\n" + folderCtx
		}
	}
	if ledger := cb.resolveSubagentLedger(sessionKey); ledger != "" {
		prompt = prompt + "\n\n---\n\n" + ledger
	}
	return prompt
}

func (cb *ContextBuilder) ResetMemoryContext() {
	cb.initialMu.Lock()
	cb.initialContext = ""
	cb.initialMu.Unlock()

	cb.harnessMu.Lock()
	cb.harnessContext = ""
	cb.harnessContextValid = false
	cb.harnessMu.Unlock()
}

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
	bootstrapFiles := lelecontext.BootstrapFiles

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

func (cb *ContextBuilder) BuildMessages(history []providers.Message, summary string, currentMessage string, attachments []bus.FileAttachment, channel, chatID, sessionKey string, mode string) []providers.Message {
	messages := []providers.Message{}
	renderedUserMessage := cb.BuildCurrentUserMessage(currentMessage, attachments, channel, chatID)

	var systemPrompt string
	if mode == "chat" {
		systemPrompt = cb.BuildMinimalSystemPrompt()
	} else {
		systemPrompt = cb.buildSystemPromptForTurn(channel, chatID, sessionKey)
	}

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

	messages = append(messages, filterContextMessages(history)...)

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

func healAssistantToolCalls(history []providers.Message) ([]providers.Message, bool) {
	droppedIDs := make(map[string]struct{})
	changed := false

	cleaned := make([]providers.Message, len(history))
	for i, m := range history {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			cleaned[i] = m
			continue
		}
		canonical := providers.CanonicalToolCalls(m.ToolCalls)

		if len(canonical) != len(m.ToolCalls) {
			kept := make(map[string]struct{}, len(canonical))
			for _, tc := range canonical {
				if tc.ID != "" {
					kept[tc.ID] = struct{}{}
				}
			}
			for _, tc := range m.ToolCalls {
				if _, ok := kept[tc.ID]; !ok && tc.ID != "" {
					droppedIDs[tc.ID] = struct{}{}
				}
			}
			changed = true
		}

		if len(canonical) == len(m.ToolCalls) && !sameToolCalls(m.ToolCalls, canonical) {
			changed = true
		}

		nm := m
		nm.ToolCalls = canonical
		cleaned[i] = nm
	}

	if len(droppedIDs) == 0 {
		if !changed {
			return history, false
		}
		return cleaned, true
	}

	final := make([]providers.Message, 0, len(cleaned))
	for _, m := range cleaned {
		if m.Role == "tool" && m.ToolCallID != "" {
			if _, ok := droppedIDs[m.ToolCallID]; ok {
				continue
			}
		}
		if m.Role == "assistant" && len(m.ToolCalls) == 0 && strings.TrimSpace(m.TextContent()) == "" {
			continue
		}
		final = append(final, m)
	}
	return final, true
}

func sameToolCalls(stored, canonical []providers.ToolCall) bool {
	for i := range stored {
		a, b := stored[i], canonical[i]
		if a.ID != b.ID || a.Type != b.Type || a.Name != b.Name {
			return false
		}
		if a.ThoughtSignature != b.ThoughtSignature {
			return false
		}
		if rawArguments(a) != rawArguments(b) {
			return false
		}
		if len(a.Arguments) != len(b.Arguments) {
			return false
		}
		for k, v := range b.Arguments {
			av, ok := a.Arguments[k]
			if !ok || !jsonEqual(av, v) {
				return false
			}
		}
	}
	return true
}

func rawArguments(tc providers.ToolCall) string {
	if tc.Function == nil {
		return "\x00nil"
	}
	return tc.Function.Arguments
}

func jsonEqual(a, b interface{}) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}

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
	messages = append(messages, msg)
	return messages
}

func (cb *ContextBuilder) currentSkillsFilter() []string {
	cb.initialMu.RLock()
	defer cb.initialMu.RUnlock()
	return cb.skillsFilter
}

func (cb *ContextBuilder) loadSkills() string {
	allSkills := cb.skillsLoader.ListSkills()
	if len(allSkills) == 0 {
		return ""
	}

	var skillNames []string
	filter := cb.currentSkillsFilter()
	for _, s := range allSkills {
		if !skills.SkillAllowed(filter, s.Name) {
			continue
		}
		skillNames = append(skillNames, s.Name)
	}
	if len(skillNames) == 0 {
		return ""
	}

	content := cb.skillsLoader.LoadSkillsForContext(skillNames)
	if content == "" {
		return ""
	}

	return "# Skill Definitions\n\n" + content
}

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
