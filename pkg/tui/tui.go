package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tui/i18n"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type paneType int

const (
	ChatsPane paneType = iota
	ChatViewPane
)

type modalType int

const (
	ModalNone modalType = iota
	ModalSessions
	ModalAgent
	ModalModel
	ModalThink
	ModalLang
	ModalSubagents
)

// Timeout for ESC hint display (double-press to cancel)
const escHintTimeout = 1 * time.Second

type commandInfo struct {
	name        string
	description string
}

var allCommands = []commandInfo{
	{name: "/sessions", description: "Switch session"},
	{name: "/new", description: "New session"},
	{name: "/agents", description: "Switch agent"},
	{name: "/models", description: "Switch model"},
	{name: "/clear", description: "Clear session history"},
	{name: "/think", description: "Toggle thinking level (off/low/medium/high)"},
	{name: "/lang", description: "Change language (es/en/pt)"},
	{name: "/subagents", description: "Switch to subagent"},
	{name: "/quit", description: "Exit TUI"},
}

// Messages for Bubbletea loop
type sessionListMsg []*session.Session
type outboundMsg struct {
	msg bus.OutboundMessage
}
type completeMsg struct {
	sessionKey string
}
type tickMsg time.Time

type Model struct {
	agentLoop  *agent.AgentLoop
	sessionMgr *session.SessionManager
	cfg        *config.Config
	ctx        context.Context
	cancel     context.CancelFunc

	// UI state
	activePane         paneType
	selectedSessionIdx int
	visibleSessions    []*session.Session
	currentKey         string
	showWelcome        bool // true when showing the welcome/new-chat screen

	// Autocomplete dropdown menu state
	showAutocomplete  bool
	autocompleteItems []commandInfo
	autocompleteIdx   int

	// Selection modals
	modalMode         modalType
	modalItems        []string
	modalSessionKeys  []string // maps modal items to session keys (for /sessions)
	modalSubagentKeys []string // maps modal items to subagent session keys (for /subagents)
	modalSelectedIdx  int
	modalScrollOffset int // scroll offset for long modal lists

	// Sub-components
	viewport  viewport.Model
	textInput textinput.Model

	// Pending user message (shown immediately before agent responds)
	pendingUserMessage string

	// Message processing / streaming
	processing       bool
	currentMessageID string
	currentStream    string
	currentThinking  string
	startTime        time.Time
	elapsedTime      time.Duration
	lastDuration     time.Duration
	animationTick    int

	// Double-ESC cancel tracking
	escPressCount int
	escLastPress  time.Time
	escHint       bool // true when showing "press ESC again to cancel" hint

	// Workspace Git info
	gitBranch     string
	workspacePath string

	// Pending model/agent for welcome screen (applied on session creation)
	pendingModel string
	pendingAgent string
	pendingThink string

	// Terminal size
	width  int
	height int
}

func NewModel(cfg *config.Config, agentLoop *agent.AgentLoop, sessionMgr *session.SessionManager) *Model {
	// Initialize i18n with configured language
	i18n.InitWithLanguage(cfg.GetLanguage())

	ti := textinput.New()
	ti.Placeholder = i18n.T("tui.placeholder")
	ti.Focus()
	ti.CharLimit = 4096
	ti.Width = 80
	ti.Prompt = " "

	vp := viewport.New(80, 20)
	vp.SetContent(i18n.T("tui.selectOrCreateChat"))

	ctx, cancel := context.WithCancel(context.Background())
	workspacePath := cfg.WorkspacePath()
	if workspacePath == "" {
		workspacePath, _ = os.Getwd()
	}

	m := &Model{
		agentLoop:     agentLoop,
		sessionMgr:    sessionMgr,
		cfg:           cfg,
		ctx:           ctx,
		cancel:        cancel,
		viewport:      vp,
		textInput:     ti,
		activePane:    ChatViewPane,
		showWelcome:   true,
		workspacePath: workspacePath,
		gitBranch:     getGitBranch(workspacePath),
	}

	return m
}

func getGitBranch(dir string) string {
	headPath := filepath.Join(dir, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err == nil {
		content := strings.TrimSpace(string(data))
		if strings.HasPrefix(content, "ref: refs/heads/") {
			return strings.TrimPrefix(content, "ref: refs/heads/")
		}
	}
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return "main"
}

func wrapText(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	var wrappedLines []string

	for _, line := range lines {
		if len(line) <= limit {
			wrappedLines = append(wrappedLines, line)
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			wrappedLines = append(wrappedLines, "")
			continue
		}
		currentLine := words[0]
		for _, word := range words[1:] {
			if len(currentLine)+1+len(word) <= limit {
				currentLine += " " + word
			} else {
				wrappedLines = append(wrappedLines, currentLine)
				currentLine = word
			}
		}
		wrappedLines = append(wrappedLines, currentLine)
	}

	return strings.Join(wrappedLines, "\n")
}

func (m *Model) Init() tea.Cmd {
	m.reloadSessions()
	return tea.Batch(
		textinput.Blink,
		m.startOutboundListener(),
	)
}

func (m *Model) reloadSessions() {
	m.visibleSessions = nil
	all := m.sessionMgr.ListSessions()

	for _, s := range all {
		if len(s.Messages) > 0 || s.Key == m.currentKey {
			m.visibleSessions = append(m.visibleSessions, s)
		}
	}

	// Don't switch away from the welcome screen during reload
	if m.showWelcome {
		return
	}

	if m.currentKey != "" {
		found := false
		for i, s := range m.visibleSessions {
			if s.Key == m.currentKey {
				m.selectedSessionIdx = i
				found = true
				break
			}
		}
		if !found && len(m.visibleSessions) > 0 {
			m.selectedSessionIdx = 0
			m.currentKey = m.visibleSessions[0].Key
		}
	} else if len(m.visibleSessions) > 0 {
		m.selectedSessionIdx = 0
		m.currentKey = m.visibleSessions[0].Key
		m.showWelcome = false
	}

	// Clear pending user message if it now appears in the session history
	if m.pendingUserMessage != "" && m.currentKey != "" {
		history := m.agentLoop.GetProvidable().GetSessionHistory(m.currentKey)
		for _, msg := range history {
			if msg.Role == "user" && msg.Content == m.pendingUserMessage {
				m.pendingUserMessage = ""
				break
			}
		}
	}

	m.updateViewport()
}

func (m *Model) createNewChat() {
	newKey := fmt.Sprintf("tui:chat:%s", uuid.New().String())
	m.sessionMgr.GetOrCreate(newKey)

	agentID := m.pendingAgent
	if agentID == "" {
		agentID = m.agentLoop.GetProvidable().GetDefaultAgentID()
	}
	m.agentLoop.GetProvidable().SetSessionAgent(newKey, agentID)

	if m.pendingModel != "" {
		m.agentLoop.GetProvidable().SetSessionModel(newKey, m.pendingModel)
	}

	if m.pendingThink != "" {
		m.agentLoop.GetProvidable().SetThinkLevel(newKey, m.pendingThink)
	}

	m.currentKey = newKey
}

func (m *Model) updateViewport() {
	if m.currentKey == "" {
		m.viewport.SetContent("")
		return
	}

	history := m.agentLoop.GetProvidable().GetSessionHistory(m.currentKey)

	var sb strings.Builder
	lastRole := ""
	for i, msg := range history {
		// Skip the last message if it's a streaming assistant message during
		// processing — the TUI is already rendering the live stream via currentStream.
		if m.processing && msg.Role == "assistant" && msg.Streaming && i == len(history)-1 {
			continue
		}

		if msg.Role == "user" {
			sb.WriteString(UserRoleStyle.Render(i18n.T("tui.you")) + "\n")
			sb.WriteString(UserMessageStyle.Render(wrapText(msg.Content, m.viewport.Width-4)) + "\n\n")
		} else if msg.Role == "assistant" {
			// Only show agent name when coming from user (start of a turn)
			if lastRole == "" || lastRole == "user" || lastRole == "system" {
				agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
				agentInfo, ok := m.agentLoop.GetProvidable().GetAgentInfo(agentID)
				agentName := agentID
				if ok && agentInfo.Name != "" {
					agentName = agentInfo.Name
				}
				sb.WriteString(AssistantRoleStyle.Render(agentName) + "\n")
			}

			if msg.ReasoningContent != "" {
				sb.WriteString(ThinkingContentStyle.Render(wrapText(msg.ReasoningContent, m.viewport.Width-6)) + "\n")
			}

			if msg.Content != "" {
				sb.WriteString(AssistantMessageStyle.Render(wrapText(msg.Content, m.viewport.Width-4)) + "\n")
			}

			// Render tool calls from assistant message (compact: tool_name: params)
			for _, tc := range msg.ToolCalls {
				toolName := tc.Name
				if toolName == "" && tc.Function != nil {
					toolName = tc.Function.Name
				}
				args := formatToolCallArgsCompact(tc)
				line := toolName
				if args != "" {
					line += ": " + args
				}
				sb.WriteString(ToolCallLabel.Render("  ") + ToolCallName.Render(line) + "\n")
			}
			sb.WriteString("\n")
		} else if msg.Role == "tool" {
			summary := truncateToolResult(msg.Content, 150)
			sb.WriteString(ToolResultLabel.Render("  → ") + ToolResultBox.Render(summary) + "\n")
		}
		// Skip system messages — they are internal prompts, not user-facing
		lastRole = msg.Role
	}

	// Show pending user message immediately (before agent responds)
	if m.pendingUserMessage != "" {
		sb.WriteString(UserRoleStyle.Render(i18n.T("tui.you")) + "\n")
		sb.WriteString(UserMessageStyle.Render(wrapText(m.pendingUserMessage, m.viewport.Width-4)) + "\n\n")
		lastRole = "user"
	}

	if m.processing && (m.currentStream != "" || m.currentThinking != "") {
		// Only show agent name when coming from user (start of a turn)
		if lastRole == "" || lastRole == "user" || lastRole == "system" {
			agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
			agentInfo, ok := m.agentLoop.GetProvidable().GetAgentInfo(agentID)
			agentName := agentID
			if ok && agentInfo.Name != "" {
				agentName = agentInfo.Name
			}
			sb.WriteString(AssistantRoleStyle.Render(agentName) + "\n")
		}

		if m.currentThinking != "" {
			sb.WriteString(ThinkingContentStyle.Render(wrapText(m.currentThinking, m.viewport.Width-6)) + "\n")
		}
		if m.currentStream != "" {
			sb.WriteString(AssistantMessageStyle.Render(wrapText(m.currentStream, m.viewport.Width-4)) + "\n")
		}
		sb.WriteString("\n")
	}

	m.viewport.SetContent(sb.String())
	if sb.Len() > 0 && m.viewport.Height > 0 {
		m.viewport.GotoBottom()
	}
}

func formatToolCallArgs(tc providers.ToolCall) string {
	// Try structured arguments first
	if tc.Arguments != nil {
		var parts []string
		for k, v := range tc.Arguments {
			val := fmt.Sprintf("%v", v)
			if len(val) > 120 {
				val = val[:120] + "…"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", k, val))
		}
		sort.Strings(parts)
		return strings.Join(parts, "  ")
	}
	// Try function.arguments (JSON string)
	if tc.Function != nil && tc.Function.Arguments != "" {
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err == nil {
			var parts []string
			for k, v := range args {
				val := fmt.Sprintf("%v", v)
				if len(val) > 120 {
					val = val[:120] + "…"
				}
				parts = append(parts, fmt.Sprintf("%s: %s", k, val))
			}
			sort.Strings(parts)
			return strings.Join(parts, "  ")
		}
		// Fallback: show raw JSON
		raw := tc.Function.Arguments
		if len(raw) > 200 {
			raw = raw[:200] + "…"
		}
		return raw
	}
	return ""
}

// extractToolCallArgs extracts arguments from a ToolCall, handling different formats.
func extractToolCallArgs(tc providers.ToolCall) map[string]interface{} {
	if tc.Arguments != nil {
		return tc.Arguments
	}
	if tc.Function != nil && tc.Function.Arguments != "" {
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err == nil {
			return args
		}
	}
	return nil
}

// formatToolCallArgsCompact returns a single-line compact representation of
// tool call arguments: key=val pairs joined by commas, values truncated to 80 chars.
func formatToolCallArgsCompact(tc providers.ToolCall) string {
	extract := func(args map[string]interface{}) string {
		var parts []string
		for k, v := range args {
			val := fmt.Sprintf("%v", v)
			// Flatten newlines for compact display
			val = strings.ReplaceAll(val, "\n", " ")
			if len(val) > 80 {
				val = val[:80] + "…"
			}
			parts = append(parts, fmt.Sprintf("%s=%s", k, val))
		}
		sort.Strings(parts)
		return strings.Join(parts, ", ")
	}

	args := extractToolCallArgs(tc)
	if args != nil {
		return extract(args)
	}
	// Fallback: try to extract raw string from Function.Arguments
	if tc.Function != nil && tc.Function.Arguments != "" {
		raw := tc.Function.Arguments
		if len(raw) > 120 {
			raw = raw[:120] + "…"
		}
		return raw
	}
	return ""
}

// truncateToolResult returns a collapsed single-line summary of a tool result.
func truncateToolResult(content string, maxLen int) string {
	if content == "" {
		return ""
	}

	// Try to extract meaningful content from JSON if present
	if len(content) > 0 && content[0] == '{' {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(content), &parsed); err == nil {
			// Try common fields that contain the main result
			for _, key := range []string{"output", "result", "error", "message"} {
				if val, ok := parsed[key]; ok {
					if str, ok := val.(string); ok && str != "" {
						content = str
						break
					}
				}
			}
		}
	}

	// Try to extract first meaningful line
	lines := strings.Split(content, "\n")
	summary := ""
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" && l != "{" && l != "}" {
			summary = l
			break
		}
	}
	if summary == "" {
		summary = content
	}
	// Flatten and truncate
	summary = strings.ReplaceAll(summary, "\n", " ")
	if len(summary) > maxLen {
		summary = summary[:maxLen] + "…"
	}
	return summary
}

func (m *Model) startOutboundListener() tea.Cmd {
	return func() tea.Msg {
		for {
			select {
			case <-m.ctx.Done():
				return nil
			default:
				outMsg, ok := m.agentLoop.MessageBus().SubscribeOutbound(m.ctx)
				if !ok {
					return nil
				}
				return outboundMsg{msg: outMsg}
			}
		}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *Model) submitMessage() tea.Cmd {
	content := strings.TrimSpace(m.textInput.Value())
	if content == "" {
		return nil
	}

	// If we're on the welcome screen with no session, create one now
	if m.currentKey == "" || m.showWelcome {
		m.createNewChat()
		m.showWelcome = false
	}

	m.textInput.SetValue("")
	m.processing = true
	m.startTime = time.Now()
	m.elapsedTime = 0
	m.currentMessageID = uuid.New().String()
	m.currentStream = ""
	m.currentThinking = ""

	// Store the user message so it renders immediately in the viewport.
	// The LLM runner will also add it to the session; we clear our copy
	// once we see it appear in the session history (on reloadSessions).
	m.pendingUserMessage = content
	m.reloadSessions()

	m.agentLoop.MessageBus().PublishInbound(bus.InboundMessage{
		Channel:    "native",
		SenderID:   "tui",
		ChatID:     m.currentKey,
		Content:    content,
		SessionKey: m.currentKey,
		Metadata:   map[string]string{"message_id": m.currentMessageID},
	})

	return tickCmd()
}

func (m *Model) filterAutocomplete(val string) {
	m.autocompleteItems = nil
	for _, cmd := range allCommands {
		if strings.HasPrefix(cmd.name, val) {
			m.autocompleteItems = append(m.autocompleteItems, cmd)
		}
	}
	if m.autocompleteIdx >= len(m.autocompleteItems) {
		m.autocompleteIdx = 0
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.modalMode != ModalNone {
			switch msg.String() {
			case "up", "k":
				if m.modalSelectedIdx > 0 {
					m.modalSelectedIdx--
					if m.modalSelectedIdx < m.modalScrollOffset {
						m.modalScrollOffset = m.modalSelectedIdx
					}
				}
			case "down", "j":
				if m.modalSelectedIdx < len(m.modalItems)-1 {
					m.modalSelectedIdx++
					maxVisible := m.height - 8 // title + borders + padding
					if maxVisible < 3 {
						maxVisible = 3
					}
					if m.modalSelectedIdx >= m.modalScrollOffset+maxVisible {
						m.modalScrollOffset = m.modalSelectedIdx - maxVisible + 1
					}
				}
			case "enter":
				if len(m.modalItems) > 0 {
					selectedVal := m.modalItems[m.modalSelectedIdx]
					if m.modalMode == ModalAgent {
						if m.showWelcome && m.currentKey == "" {
							m.pendingAgent = selectedVal
						} else {
							m.agentLoop.GetProvidable().SetSessionAgent(m.currentKey, selectedVal)
						}
					} else if m.modalMode == ModalModel {
						if m.showWelcome && m.currentKey == "" {
							m.pendingModel = selectedVal
						} else {
							m.agentLoop.GetProvidable().SetSessionModel(m.currentKey, selectedVal)
						}
					} else if m.modalMode == ModalThink {
						if m.showWelcome && m.currentKey == "" {
							m.pendingThink = selectedVal
						} else {
							m.agentLoop.GetProvidable().SetThinkLevel(m.currentKey, selectedVal)
						}
					} else if m.modalMode == ModalSessions {
						if m.modalSelectedIdx < len(m.modalSessionKeys) {
							m.currentKey = m.modalSessionKeys[m.modalSelectedIdx]
							m.showWelcome = false
						}
					} else if m.modalMode == ModalSubagents {
						if m.modalSelectedIdx < len(m.modalSubagentKeys) {
							m.currentKey = m.modalSubagentKeys[m.modalSelectedIdx]
							m.showWelcome = false
						}
					} else if m.modalMode == ModalLang {
						// Extract language code from "Name (code)" format
						langCode := selectedVal
						if idx := strings.LastIndex(selectedVal, "("); idx != -1 {
							langCode = strings.TrimRight(selectedVal[idx+1:], ")")
						}
						m.cfg.SetLanguage(langCode)
						i18n.SetLanguage(langCode)
						m.textInput.Placeholder = i18n.T("tui.placeholder")
					} else if m.modalMode == ModalLang {
						// Map selection to language code
						langCodes := []string{"es", "en", "pt"}
						if m.modalSelectedIdx < len(langCodes) {
							lang := langCodes[m.modalSelectedIdx]
							i18n.SetLanguage(lang)
							m.cfg.SetLanguage(lang)
							// Note: Language change takes effect immediately in TUI
							// Config persistence would need to be handled separately
						}
					}
					m.modalMode = ModalNone
					m.reloadSessions()
				}
			case "esc", "q":
				m.modalMode = ModalNone
			}
			return m, nil
		}

		if m.showAutocomplete {
			switch msg.String() {
			case "up", "ctrl+k":
				if m.autocompleteIdx > 0 {
					m.autocompleteIdx--
				} else {
					m.autocompleteIdx = len(m.autocompleteItems) - 1
				}
				return m, nil
			case "down", "ctrl+j":
				if m.autocompleteIdx < len(m.autocompleteItems)-1 {
					m.autocompleteIdx++
				} else {
					m.autocompleteIdx = 0
				}
				return m, nil
			case "tab", "enter":
				if len(m.autocompleteItems) > 0 {
					completed := m.autocompleteItems[m.autocompleteIdx].name
					m.textInput.SetValue(completed)
					m.showAutocomplete = false
					if msg.String() == "enter" {
						m.executeCommand(completed)
						m.textInput.SetValue("")
					}
				}
				return m, nil
			case "esc":
				m.showAutocomplete = false
				return m, nil
			}
		}

		switch msg.String() {
		case "esc":
			if m.processing {
				now := time.Now()
				if now.Sub(m.escLastPress) < 1*time.Second {
					// Double press detected - cancel the agent
					m.escPressCount = 0
					m.escHint = false
					m.agentLoop.GetProvidable().StopAgent(m.currentKey)
					m.processing = false
					m.pendingUserMessage = ""
					m.currentStream = ""
					m.currentThinking = ""
					m.currentMessageID = ""
					m.reloadSessions()
				} else {
					// First press - show hint
					m.escPressCount = 1
					m.escHint = true
				}
				m.escLastPress = now
			}

		case "ctrl+c":
			m.cancel()
			return m, tea.Quit

		case "ctrl+p":
			m.showAutocomplete = true
			m.filterAutocomplete("/")
			return m, nil

		case "ctrl+m":
			m.executeCommand("/models")
			return m, nil

		case "ctrl+a":
			m.executeCommand("/agents")
			return m, nil

		case "ctrl+s":
			m.executeCommand("/sessions")
			return m, nil

		case "up", "down":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)

		case "enter":
			inputVal := m.textInput.Value()
			if strings.HasPrefix(inputVal, "/") {
				m.executeCommand(inputVal)
				m.textInput.SetValue("")
			} else if !m.processing {
				cmd := m.submitMessage()
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}

	case tickMsg:
		if m.processing {
			m.elapsedTime = time.Since(m.startTime)
			m.animationTick++
			cmds = append(cmds, tickCmd())
		}
		if m.escHint && time.Since(m.escLastPress) > escHintTimeout {
			m.escHint = false
			m.escPressCount = 0
		}

	case outboundMsg:
		if msg.msg.ChatID == m.currentKey {
			switch msg.msg.Event {
			case "message.stream":
				m.currentStream += msg.msg.Content
				m.updateViewport()
			case "message.thinking":
				m.currentThinking += msg.msg.Content
				m.updateViewport()
			case "":
				if m.processing && (msg.msg.MessageID == m.currentMessageID || msg.msg.ReplyTo == m.currentMessageID) {
					cmds = append(cmds, func() tea.Msg {
						return completeMsg{sessionKey: m.currentKey}
					})
				}
			}
		}
		cmds = append(cmds, m.startOutboundListener())

	case completeMsg:
		if msg.sessionKey == m.currentKey {
			m.processing = false
			m.pendingUserMessage = ""
			m.lastDuration = time.Since(m.startTime)
			m.currentStream = ""
			m.currentThinking = ""
			m.currentMessageID = ""
			m.reloadSessions()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = int(float64(m.width)*0.78) - 4
		m.viewport.Height = m.height - 8
		m.textInput.Width = int(float64(m.width)*0.78) - 4
		m.updateViewport()
	}

	if m.modalMode == ModalNone {
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		cmds = append(cmds, cmd)

		val := m.textInput.Value()
		if strings.HasPrefix(val, "/") {
			m.showAutocomplete = true
			m.filterAutocomplete(val)
		} else {
			m.showAutocomplete = false
		}
	}

	return m, tea.Batch(cmds...)
}

// resetModal resets the modal state for a new modal.
func (m *Model) resetModal(mode modalType) {
	m.modalMode = mode
	m.modalScrollOffset = 0
	m.modalItems = nil
	m.modalSessionKeys = nil
	m.modalSubagentKeys = nil
	m.modalSelectedIdx = 0
}

func (m *Model) executeCommand(cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return
	}

	switch parts[0] {
	case "/sessions":
		m.resetModal(ModalSessions)
		allSessions := m.sessionMgr.ListSessions()
		for _, s := range allSessions {
			name := s.Name
			if name == "" {
				name = i18n.T("tui.newChatDefault")
			}
			count := len(s.Messages)
			if count == 0 {
				m.modalItems = append(m.modalItems, name)
			} else {
				m.modalItems = append(m.modalItems, fmt.Sprintf("%s (%d msgs)", name, count))
			}
			m.modalSessionKeys = append(m.modalSessionKeys, s.Key)
		}

	case "/new":
		m.createNewChat()
		m.showWelcome = true
		m.reloadSessions()

	case "/agents":
		m.resetModal(ModalAgent)
		m.modalItems = m.agentLoop.GetProvidable().ListAvailableAgentIDs()

	case "/models":
		m.resetModal(ModalModel)
		cfgSnapshot := m.agentLoop.GetProvidable().GetConfigSnapshot()
		if cfgSnapshot != nil && cfgSnapshot.Providers != nil {
			providersMap := cfgSnapshot.Providers.ListNamed()
			var pNames []string
			for k := range providersMap {
				pNames = append(pNames, k)
			}
			sort.Strings(pNames)
			for _, pName := range pNames {
				pCfg := providersMap[pName]
				var aliases []string
				for mAlias := range pCfg.Models {
					aliases = append(aliases, mAlias)
				}
				sort.Strings(aliases)
				for _, mAlias := range aliases {
					m.modalItems = append(m.modalItems, fmt.Sprintf("%s:%s", pName, mAlias))
				}
			}
		}

	case "/clear":
		m.agentLoop.GetProvidable().ClearSession(m.currentKey)
		if m.pendingModel != "" && m.currentKey != "" {
			m.agentLoop.GetProvidable().SetSessionModel(m.currentKey, m.pendingModel)
		}
		m.reloadSessions()

	case "/think":
		m.resetModal(ModalThink)
		m.modalItems = []string{"off", "low", "medium", "high"}

	case "/lang":
		m.resetModal(ModalLang)
		// Show language names with codes
		m.modalItems = []string{
			"Español (es)",
			"English (en)",
			"Português (pt)",
		}

	case "/subagents":
		m.resetModal(ModalSubagents)
		subagents := m.agentLoop.GetProvidable().GetSessionSubagents(m.currentKey)
		if len(subagents) == 0 {
			m.modalItems = append(m.modalItems, i18n.T("tui.noSubagents"))
		} else {
			for _, sa := range subagents {
				label := sa.Label
				if label == "" {
					label = sa.TaskID
				}
				m.modalItems = append(m.modalItems, fmt.Sprintf("%s [%s] %s", sa.TaskID, sa.Status, label))
				m.modalSubagentKeys = append(m.modalSubagentKeys, sa.SessionKey)
			}
		}

	case "/quit":
		m.cancel()
		os.Exit(0)
	}
}

func (m *Model) currentSessionKey() string {
	return m.currentKey
}

func (m *Model) getHistoryMessageCount() int {
	if m.currentKey == "" {
		return 0
	}
	history := m.agentLoop.GetProvidable().GetSessionHistory(m.currentKey)
	count := 0
	for _, msg := range history {
		if msg.Role == "user" || msg.Role == "assistant" {
			count++
		}
	}
	return count
}

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return i18n.T("tui.initializing")
	}

	// --------------------------------------------------------------------------
	// WELCOME HOME SCREEN LAYOUT
	// --------------------------------------------------------------------------
	if m.showWelcome {
		var contentBuilder strings.Builder

		logo := "  _      ______ _      ______\n" +
			" | |    |  ____| |    |  ____|\n" +
			" | |    | |__  | |    | |__   \n" +
			" | |    |  __| | |    |  __|\n" +
			" | |____| |____| |____| |____\n" +
			" |______|______|______|______|"
		contentBuilder.WriteString(WelcomeLogo.Render(logo) + "\n\n")

		var autocompleteView string
		if m.showAutocomplete && len(m.autocompleteItems) > 0 {
			var autoSb strings.Builder
			for i, cmd := range m.autocompleteItems {
				line := fmt.Sprintf("%-12s %s", cmd.name, cmd.description)
				if i == m.autocompleteIdx {
					autoSb.WriteString(ModalItemActive.Render(line) + "\n")
				} else {
					autoSb.WriteString(ModalItemInactive.Render(line) + "\n")
				}
			}
			autocompleteView = ModalContainer.Width(60).Render(autoSb.String())
			contentBuilder.WriteString(autocompleteView + "\n")
		}

		inputView := InputBarContainer.Width(60).Render(m.textInput.View())
		contentBuilder.WriteString(inputView + "\n\n")

		agentID := ""
		modelName := ""
		thinkLevel := "off"
		if m.currentKey != "" {
			agentID = m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
			modelName = m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
			tl := m.agentLoop.GetProvidable().GetThinkLevel(m.currentKey)
			if tl != "" {
				thinkLevel = tl
			}
		} else {
			agentID = m.agentLoop.GetProvidable().GetDefaultAgentID()
			if m.pendingModel != "" {
				modelName = m.pendingModel
			}
			if m.pendingAgent != "" {
				agentID = m.pendingAgent
			}
			if m.pendingThink != "" {
				thinkLevel = m.pendingThink
			}
		}
		if modelName == "" {
			if agentInfo, ok := m.agentLoop.GetProvidable().GetAgentInfo(agentID); ok {
				modelName = agentInfo.Model
			}
		}

		// Model selector line
		selectorLine := fmt.Sprintf("%s %s  %s %s",
			ModelSelectorLabel.Render(i18n.T("tui.model")),
			ModelSelectorStyle.Render(modelName),
			ModelSelectorLabel.Render(i18n.T("tui.agent")),
			ModelSelectorStyle.Render(agentID),
		)
		contentBuilder.WriteString(selectorLine + "\n")

		infoText := fmt.Sprintf("%s %s  ·  %s  ·  %s  ·  %s", i18n.T("tui.thinking"), thinkLevel, i18n.T("tui.ctrlModel"), i18n.T("tui.ctrlAgent"), i18n.T("tui.ctrlChats"))
		contentBuilder.WriteString(HelpStyle.Render(infoText) + "\n\n")

		tip := i18n.T("tui.typeMessage")
		contentBuilder.WriteString(WelcomeTip.Render(tip) + "\n")

		// Render modal overlay on welcome screen if active
		if m.modalMode != ModalNone {
			var modalTitle string
			switch m.modalMode {
			case ModalAgent:
				modalTitle = i18n.T("tui.selectAgent")
			case ModalModel:
				modalTitle = i18n.T("tui.selectModel")
			case ModalSessions:
				modalTitle = i18n.T("tui.selectChat")
			case ModalSubagents:
				modalTitle = i18n.T("tui.selectSubagent")
			case ModalThink:
				modalTitle = i18n.T("tui.selectThinkLevel")
			case ModalLang:
				modalTitle = i18n.T("tui.selectLanguage")
			}

			return m.renderModal(modalTitle)
		}

		// Center the entire welcome content block in the terminal
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, contentBuilder.String())
	}

	// --------------------------------------------------------------------------
	// SPLIT COLUMN CONVERSATIONAL LAYOUT
	// --------------------------------------------------------------------------
	leftWidth := int(float64(m.width) * 0.78)
	rightWidth := m.width - leftWidth - 3
	contentHeight := m.height - 2

	// Render Left Column (Chat Contents)
	var leftBuilder strings.Builder

	agentID := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey)
	modelName := m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
	thinkLevel := m.agentLoop.GetProvidable().GetThinkLevel(m.currentKey)
	if thinkLevel == "" {
		thinkLevel = "off"
	}

	m.viewport.Width = leftWidth - 2
	m.viewport.Height = contentHeight - 6
	leftBuilder.WriteString(ViewportStyle.Render(m.viewport.View()) + "\n")

	var statusLine string
	if m.processing {
		if m.escHint {
			statusLine = fmt.Sprintf("%s %s", m.getBouncingDots(), i18n.T("tui.pressEscAgain"))
		} else {
			statusLine = fmt.Sprintf("%s %s", m.getBouncingDots(), i18n.T("tui.processing"))
		}
	} else if m.lastDuration > 0 {
		statusLine = fmt.Sprintf(i18n.T("tui.doneIn"), m.lastDuration.Seconds())
	} else {
		statusLine = i18n.T("tui.ready")
	}

	var autocompleteView string
	if m.showAutocomplete && len(m.autocompleteItems) > 0 {
		var autoSb strings.Builder
		for i, cmd := range m.autocompleteItems {
			line := fmt.Sprintf("%-12s %s", cmd.name, cmd.description)
			if i == m.autocompleteIdx {
				autoSb.WriteString(ModalItemActive.Render(line) + "\n")
			} else {
				autoSb.WriteString(ModalItemInactive.Render(line) + "\n")
			}
		}
		autocompleteView = ModalContainer.Width(leftWidth-4).Render(autoSb.String()) + "\n"
	}

	leftBuilder.WriteString(StatusLineStyle.Render(statusLine) + "\n")
	if autocompleteView != "" {
		leftBuilder.WriteString(autocompleteView)
	}

	m.textInput.Width = leftWidth - 4
	inputBar := InputBarContainer.Width(leftWidth - 2).Render(m.textInput.View())
	leftBuilder.WriteString(inputBar + "\n")

	tokens, limit := m.agentLoop.GetProvidable().GetCurrentContextUsage(m.currentKey)
	pct := 0.0
	if limit > 0 {
		pct = float64(tokens) / float64(limit) * 100
	}
	tokensText := fmt.Sprintf("%d (%.1f%%)", tokens, pct)
	bottomBar := lipgloss.JoinHorizontal(lipgloss.Top,
		BottomBarLeft.Width((leftWidth-2)/2).Render(fmt.Sprintf("%s · %s · %s", agentID, modelName, thinkLevel)),
		BottomBarRight.Width((leftWidth-2)/2).Align(lipgloss.Right).Render(fmt.Sprintf("%s | %s", tokensText, i18n.T("tui.ctrlCommands"))),
	)
	leftBuilder.WriteString(bottomBar)

	leftPane := LeftColumnStyle.Width(leftWidth).Render(leftBuilder.String())

	// Render Right Column (Sidebar Panel)
	var rightBuilder strings.Builder

	sessionName := m.sessionMgr.GetName(m.currentKey)
	if sessionName == "" {
		sessionName = i18n.T("tui.newChatDefault")
	}
	rightBuilder.WriteString(SidebarTitle.Render(sessionName) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.context")) + "\n")
	cumInput, cumOutput, limit := m.agentLoop.GetProvidable().GetTokenCounts(m.currentKey)
	cumPct := 0.0
	if limit > 0 {
		cumPct = float64(cumInput) / float64(limit) * 100
	}
	rightBuilder.WriteString(SidebarValue.Render(fmt.Sprintf("%s %s", formatNumber(cumInput+cumOutput), i18n.T("tui.tokens"))) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(fmt.Sprintf(i18n.T("tui.used"), cumPct)) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(i18n.T("tui.spent")) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.mcp")) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(SidebarConnectedDot.Render("●")+" "+i18n.T("tui.workspaceConnected")) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(SidebarConnectedDot.Render("●")+" "+i18n.T("tui.systemConnected")) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.lsp")) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(i18n.T("tui.lspDisabled")) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.workspace")) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(m.workspacePath) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(m.gitBranch) + "\n\n")

	rightBuilder.WriteString(SidebarHeader.Render(i18n.T("tui.status")) + "\n")
	rightBuilder.WriteString(SidebarValue.Render(SidebarConnectedDot.Render("●")+" Lele "+agent.GatewayVersion()) + "\n")

	rightPane := RightSidebar.Width(rightWidth).Height(contentHeight).Render(rightBuilder.String())

	mainLayout := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	if m.modalMode != ModalNone {
		var modalTitle string
		switch m.modalMode {
		case ModalAgent:
			modalTitle = i18n.T("tui.selectAgent")
		case ModalModel:
			modalTitle = i18n.T("tui.selectModel")
		case ModalSessions:
			modalTitle = i18n.T("tui.selectChat")
		case ModalSubagents:
			modalTitle = i18n.T("tui.selectSubagent")
		case ModalThink:
			modalTitle = i18n.T("tui.selectThinkLevel")
		case ModalLang:
			modalTitle = i18n.T("tui.selectLanguage")
		}

		return m.renderModal(modalTitle)
	}

	return AppContainer.Width(m.width).Height(m.height).Render(mainLayout)
}

// maxModalVisible returns the maximum number of items visible in a modal given terminal height.
func (m *Model) maxModalVisible() int {
	maxVisible := m.height - 8 // room for title, borders, padding
	if maxVisible < 3 {
		maxVisible = 3
	}
	if maxVisible > len(m.modalItems) {
		maxVisible = len(m.modalItems)
	}
	return maxVisible
}

// renderModal renders a modal overlay with scroll support for long lists.
func (m *Model) renderModal(modalTitle string) string {
	maxVisible := m.maxModalVisible()

	// Clamp scroll offset so selected item is always visible
	if m.modalSelectedIdx < m.modalScrollOffset {
		m.modalScrollOffset = m.modalSelectedIdx
	}
	if m.modalSelectedIdx >= m.modalScrollOffset+maxVisible {
		m.modalScrollOffset = m.modalSelectedIdx - maxVisible + 1
	}
	if m.modalScrollOffset < 0 {
		m.modalScrollOffset = 0
	}

	var modalSb strings.Builder
	modalSb.WriteString(TitleStyle.Render(modalTitle) + "\n")

	// Scroll indicator: show ↑ if there are items above
	if m.modalScrollOffset > 0 {
		modalSb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.moreAbove")) + "\n")
	} else {
		modalSb.WriteString("\n")
	}

	// Render only the visible window of items
	endIdx := m.modalScrollOffset + maxVisible
	if endIdx > len(m.modalItems) {
		endIdx = len(m.modalItems)
	}
	for i := m.modalScrollOffset; i < endIdx; i++ {
		item := m.modalItems[i]
		if i == m.modalSelectedIdx {
			modalSb.WriteString(ModalItemActive.Render("> "+item) + "\n")
		} else {
			modalSb.WriteString(ModalItemInactive.Render("  "+item) + "\n")
		}
	}

	// Scroll indicator: show ↓ if there are items below
	if endIdx < len(m.modalItems) {
		modalSb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.moreBelow")) + "\n")
	}

	modalView := ModalContainer.Render(modalSb.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modalView)
}

func formatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var res []string
	for len(s) > 3 {
		res = append([]string{s[len(s)-3:]}, res...)
		s = s[:len(s)-3]
	}
	res = append([]string{s}, res...)
	return strings.Join(res, ",")
}

func (m *Model) getBouncingDots() string {
	width := 12
	pos := m.animationTick % (2 * (width - 3))
	var offset int
	if pos < width-3 {
		offset = pos
	} else {
		offset = 2*(width-3) - pos
	}

	var sb strings.Builder
	sb.WriteRune('[')
	for i := 0; i < width; i++ {
		if i == offset || i == offset+1 || i == offset+2 {
			sb.WriteString(lipgloss.NewStyle().Foreground(SecondaryColor).Render("●"))
		} else {
			sb.WriteRune(' ')
		}
	}
	sb.WriteRune(']')
	return sb.String()
}
