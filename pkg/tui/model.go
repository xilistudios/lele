package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tui/i18n"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func NewModel(cfg *config.Config, agentLoop *agent.AgentLoop, sessionMgr *session.SessionManager, initialSessionID ...string) *Model {
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
	workspacePath, _ := os.Getwd()

	now := time.Now()
	m := &Model{
		agentLoop:              agentLoop,
		sessionMgr:             sessionMgr,
		cfg:                    cfg,
		ctx:                    ctx,
		cancel:                 cancel,
		viewport:               vp,
		textInput:              ti,
		activePane:             ChatViewPane,
		showWelcome:            true,
		workspacePath:          workspacePath,
		gitBranch:              getGitBranch(workspacePath),
		sessionStartTime:       now,
		subagentProgress:       make(map[string]string),
		streamThrottleInterval: 32 * time.Millisecond,
		mouseEnabled:           true,
	}

	// If an initial session ID was provided, try to open it
	if len(initialSessionID) > 0 && initialSessionID[0] != "" {
		sid := initialSessionID[0]
		sessions := sessionMgr.ListSessions()
		found := false
		for _, s := range sessions {
			if s.Key == sid {
				m.currentKey = sid
				m.showWelcome = false
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "Session %q not found, starting new session\n", sid)
		}
	}

	return m
}

func (m *Model) Init() tea.Cmd {
	m.reloadSessions()
	return tea.Batch(
		textinput.Blink,
		m.startOutboundListener(),
	)
}

func (m *Model) reloadSessions() {
	// Invalidate rendered content cache — session history may have changed
	m.renderedBaseKey = ""

	m.visibleSessions = nil
	all := m.sessionMgr.ListSessions()

	for _, s := range all {
		// Exclude subagent sessions from the main session list — they have
		// their own navigation via /subagents and are not top-level chats.
		if isSubagentSessionKey(s.Key) {
			continue
		}
		if len(s.Messages) > 0 || s.Key == m.currentKey {
			m.visibleSessions = append(m.visibleSessions, s)
		}
	}

	// Don't switch away from the welcome screen during reload
	if m.showWelcome {
		return
	}

	// When viewing a subagent chat, currentKey is intentionally NOT in
	// visibleSessions (subagents are filtered out). Skip the sync so we
	// don't get forced back to the first regular session.
	if m.parentSessionKey != "" {
		m.updateViewport()
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
			// Only override currentKey if it's truly empty (not when returning
			// from a subagent back to a parent chat with valid history)
			if m.currentKey == "" {
				m.currentKey = m.visibleSessions[0].Key
			}
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

	// Clear streaming state if the assistant message is fully saved in history
	m.cleanupStreamingIfComplete()

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

	modelID := m.pendingModel
	if modelID == "" && m.currentKey != "" {
		modelID = m.agentLoop.GetProvidable().GetSessionModel(m.currentKey)
	}
	if modelID != "" {
		m.agentLoop.GetProvidable().SetSessionModel(newKey, modelID)
	}

	if m.pendingThink != "" {
		m.agentLoop.GetProvidable().SetThinkLevel(newKey, m.pendingThink)
	}

	m.currentKey = newKey
	m.forceGotoBottom = true
}

// cleanupStreamingIfComplete clears streaming/thinking state if the last assistant
// message in history is no longer in streaming mode. This avoids stale content
// leaking into the viewport after the message is fully saved.
func (m *Model) cleanupStreamingIfComplete() {
	if (m.currentStream == "" && m.currentThinking == "") || m.currentKey == "" {
		return
	}
	history := m.agentLoop.GetProvidable().GetSessionHistory(m.currentKey)
	var lastAssistantMsg *providers.Message
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" {
			lastAssistantMsg = &history[i]
			break
		}
	}
	if lastAssistantMsg != nil && !lastAssistantMsg.Streaming {
		streamMatched := m.currentStream == "" || strings.Contains(lastAssistantMsg.Content, m.currentStream)
		thinkingMatched := m.currentThinking == "" || strings.Contains(lastAssistantMsg.ReasoningContent, m.currentThinking)
		if streamMatched && thinkingMatched {
			m.currentStream = ""
			m.currentThinking = ""
			m.currentAssistantMsgID = ""
		}
	}
}

// resetStreamState clears the current streaming strings and line caches.
func (m *Model) resetStreamState() {
	m.currentStream = ""
	m.currentThinking = ""
	m.streamRenderedLines = nil
	m.thinkingRenderedLines = nil
}

// clearStreamingState resets all streaming/processing state.
// Called when switching sessions to avoid stale content leaking into the new session.
// It preserves m.processing when the target session has an active LLM loop,
// so the loading animation continues when switching to a busy session/subagent.
func (m *Model) clearStreamingState() {
	m.streamThrottleActive = false
	m.streamPendingUpdate = false
	m.compactFeedback = ""

	// Check if the current session (already set to the target) is actively
	// being processed by the LLM before resetting the flag.
	isActive := false
	if m.currentKey != "" {
		isActive = m.agentLoop.GetProvidable().IsSessionProcessing(m.currentKey)
	}
	m.processing = isActive

	m.resetStreamState()
	m.currentToolAction = ""
	m.currentMessageID = ""
	m.currentAssistantMsgID = ""
	m.pendingSubagentCompletions = 0
	m.parentCompletionObserved = false
	m.pendingUserMessage = ""
	if !m.hasRunningSubagents() && !isActive {
		m.subagentProgress = make(map[string]string)
	}
	// Invalidate rendered cache to force a full rebuild on next updateViewport
	m.renderedBase = ""
	m.renderedBaseKey = ""
	m.forceGotoBottom = true
}

// isSubagentSessionKey returns true if the given session key belongs to a subagent.
// Subagent session keys follow the pattern "<origin>:subagent-<n>".
func isSubagentSessionKey(key string) bool {
	return strings.Contains(key, ":subagent-")
}

// isSubagentOfCurrentChat returns true when chatID belongs to one of the
// subagents spawned from the current parent session.
func (m *Model) isSubagentOfCurrentChat(chatID string) bool {
	// Subagent sessions are keyed as "native:<parentChatID>:subagent-<n>".
	// The parent chatID stored in m.currentKey is the bare "tui:chat:<uuid>" key,
	// so the native-prefixed version is what appears in the subagent session key.
	nativePrefixed := "native:" + m.currentKey
	return strings.HasPrefix(chatID, nativePrefixed+":subagent-")
}

// currentSubagentTaskID extracts the task ID (e.g. "subagent-1") from a
// subagent chatID that belongs to the current parent chat.
func (m *Model) currentSubagentTaskID(chatID string) string {
	nativePrefixed := "native:" + m.currentKey + ":"
	if strings.HasPrefix(chatID, nativePrefixed) {
		return chatID[len(nativePrefixed):]
	}
	return ""
}

func (m *Model) hasRunningSubagents() bool {
	if m.agentLoop == nil || m.currentKey == "" {
		return false
	}
	subagentQueryKey := m.currentKey
	if !strings.HasPrefix(subagentQueryKey, "native:") {
		subagentQueryKey = "native:" + subagentQueryKey
	}
	subagents := m.agentLoop.GetProvidable().GetSessionSubagents(subagentQueryKey)
	for _, sa := range subagents {
		if sa.Status == "running" {
			return true
		}
	}
	return false
}

// isSubagentSession returns true if the session key corresponds to a subagent session.
func (m *Model) isSubagentSession(sessionKey string) bool {
	return strings.Contains(sessionKey, ":subagent-") || strings.HasPrefix(sessionKey, "subagent:")
}

func (m *Model) isSessionProcessing() bool {
	// If the backend has an active processing loop for the current session, we are processing.
	backendProcessing := m.currentKey != "" && m.agentLoop != nil &&
		m.agentLoop.GetProvidable().IsSessionProcessing(m.currentKey)
	if backendProcessing {
		return true
	}

	// For parent sessions, if there are running subagents, we are also processing.
	if !m.isSubagentSession(m.currentKey) && m.hasRunningSubagents() {
		return true
	}

	// Otherwise, check if we are in the brief startup phase after sending a message.
	if m.processing && !m.startTime.IsZero() && time.Since(m.startTime) < 3*time.Second {
		return true
	}

	// Stale/stuck local processing state
	if m.processing {
		m.processing = false
	}
	return false
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

// printSessionSummary prints a summary of the session to stderr before exiting.
func (m *Model) printSessionSummary() {
	sessionID := m.currentKey
	if sessionID == "" {
		sessionID = "(none)"
	}

	// Count role name repetitions in history
	userCount := 0
	assistantCount := 0
	if m.currentKey != "" {
		history := m.agentLoop.GetProvidable().GetSessionHistory(m.currentKey)
		for _, msg := range history {
			switch msg.Role {
			case "user":
				userCount++
			case "assistant":
				assistantCount++
			}
		}
	}
	totalNames := userCount + assistantCount

	// Calculate session duration
	duration := time.Since(m.sessionStartTime).Seconds()

	fmt.Fprintf(os.Stderr, "Session: %s\n", sessionID)
	fmt.Fprintf(os.Stderr, "Repeticiones de nombres: %d\n", totalNames)
	fmt.Fprintf(os.Stderr, "Segundos: %.1f\n", duration)
}
