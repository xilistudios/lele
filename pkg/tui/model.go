package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tui/i18n"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func NewModel(cfg *config.Config, agentLoop *agent.AgentLoop, sessionMgr *session.SessionManager, initialSessionID ...string) *Model {
	// Initialize i18n with configured language
	i18n.InitWithLanguage(cfg.GetLanguage())

	// Multi-line chat input
	ta := textarea.New()
	ta.Placeholder = i18n.T("tui.placeholder")
	ta.Focus()
	ta.CharLimit = 0 // unlimited
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.Prompt = " "
	ta.ShowLineNumbers = false
	ta.EndOfBufferCharacter = ' '
	// Custom KeyMap: remove bindings that conflict with TUI shortcuts.
	// Enter sends the message (handled in handlers.go), Alt+Enter inserts newline.
	ta.KeyMap = textarea.KeyMap{
		CharacterForward:        key.NewBinding(key.WithKeys("right"), key.WithHelp("right", "character forward")),
		CharacterBackward:       key.NewBinding(key.WithKeys("left"), key.WithHelp("left", "character backward")),
		WordForward:             key.NewBinding(key.WithKeys("alt+right", "alt+f"), key.WithHelp("alt+right", "word forward")),
		WordBackward:            key.NewBinding(key.WithKeys("alt+left", "alt+b"), key.WithHelp("alt+left", "word backward")),
		InsertNewline:           key.NewBinding(key.WithKeys("alt+enter"), key.WithHelp("alt+enter", "insert newline")),
		DeleteCharacterBackward: key.NewBinding(key.WithKeys("backspace"), key.WithHelp("backspace", "delete character backward")),
		DeleteCharacterForward:  key.NewBinding(key.WithKeys("delete"), key.WithHelp("delete", "delete character forward")),
		DeleteWordBackward:      key.NewBinding(key.WithKeys("alt+backspace", "ctrl+w"), key.WithHelp("alt+backspace", "delete word backward")),
		DeleteWordForward:       key.NewBinding(key.WithKeys("alt+delete", "alt+d"), key.WithHelp("alt+delete", "delete word forward")),
		DeleteAfterCursor:       key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("ctrl+k", "delete after cursor")),
		DeleteBeforeCursor:      key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "delete before cursor")),
		Paste:                   key.NewBinding(key.WithKeys("ctrl+v"), key.WithHelp("ctrl+v", "paste")),
		// Intentionally omitted (conflict with TUI shortcuts):
		// LineNext/LinePrevious (up/down → viewport scroll)
		// LineStart/LineEnd (home/end → viewport scroll)
		// InputBegin/InputEnd (ctrl+home/ctrl+end)
		// CharacterForward ctrl+f, CharacterBackward ctrl+b (ctrl+b → go back to parent)
		// LineNext ctrl+n, LinePrevious ctrl+p (ctrl+p → autocomplete)
		// LineStart ctrl+a (ctrl+a → /agents)
		// LineEnd ctrl+e
		// DeleteCharacterBackward ctrl+h, DeleteCharacterForward ctrl+d
		// TransposeCharacterBackward ctrl+t (ctrl+t → mouse toggle)
		// UppercaseWordForward, LowercaseWordForward, CapitalizeWordForward
	}
	// Minimal styling — blend with the TUI theme.
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	ta.FocusedStyle.EndOfBuffer = lipgloss.NewStyle()
	ta.BlurredStyle = ta.FocusedStyle // same style when not focused (always focused anyway)

	// Single-line input for modal forms (AddProvider, AddModel)
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Focus()
	ti.CharLimit = 0
	ti.Width = 40
	ti.Prompt = " "

	vp := newLineViewport(80, 20)
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
		chatInput:              ta,
		textInput:              ti,
		activePane:             ChatViewPane,
		showWelcome:            true,
		workspacePath:          workspacePath,
		gitBranch:              getGitBranch(workspacePath),
		sessionStartTime:       now,
		subagentProgress:       make(map[string]string),
		streamThrottleInterval: 32 * time.Millisecond,
		mouseEnabled:           true,
		maxRenderedMessages:    200, // render at most 200 messages to bound memory usage
		renderStartIdx:         -1,  // uninitialized — compute default on first render
	}

	// Initialize a read/manage-only cron service backed by the same store the
	// gateway uses. We intentionally do NOT call Start() so the TUI never
	// schedules or fires jobs — it only lists, enables/disables, runs-now and
	// deletes them.
	cronStorePath := filepath.Join(cfg.WorkspacePath(), "cron", "jobs.json")
	m.cronService = cron.NewCronService(cronStorePath, nil)

	// If an initial session ID was provided, try to open it
	if len(initialSessionID) > 0 && initialSessionID[0] != "" {
		sid := initialSessionID[0]
		sessions := sessionMgr.ListSessions()
		found := false
		for _, s := range sessions {
			if s.Key == sid || strings.TrimPrefix(s.Key, "tui:chat:") == sid || s.Key == "tui:chat:"+sid {
				m.currentKey = s.Key
				m.showWelcome = false
				switch s.Mode {
				case "chat":
					m.currentMode = ModeChat
				case "group":
					m.currentMode = ModeGroup
				case "agent":
					m.currentMode = ModeAgent
				}
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
		textarea.Blink,
		m.startOutboundListener(),
		tea.EnableMouseCellMotion,
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
		// Filter by current mode (empty mode = "agent" for backward compat).
		sessionMode := s.Mode
		if sessionMode == "" {
			sessionMode = "agent"
		}
		if sessionMode != m.currentMode.String() {
			continue
		}
		if len(s.Messages) > 0 || m.sessionMgr.GetTotalMessageCount(s.Key) > 0 || s.Key == m.currentKey {
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
		history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
		for _, msg := range history {
			if msg.Role == "user" && msg.Content == m.pendingUserMessage {
				m.pendingUserMessage = ""
				break
			}
		}
	}

	// Clear streaming state if the assistant message is fully saved in history
	m.cleanupStreamingIfComplete()

	// Skip the re-render if nothing user-visible changed. reloadSessions is
	// called on many events (including unrelated outbound events) and without
	// this guard every one of them would rebuild and re-render the entire
	// history — the dominant CPU cost for long conversations.
	if m.shouldSkipViewportUpdate() {
		return
	}

	m.updateViewport()
}

// getViewportContentKey returns a compact fingerprint of the state that affects
// the rendered viewport. It is used to skip redundant updateViewport() calls
// (and therefore expensive re-renders of the whole history) when nothing
// user-visible changed. This keeps idle CPU low even for very long sessions.
func (m *Model) getViewportContentKey() string {
	msgCount := m.getHistoryMessageCount()
	return fmt.Sprintf("%s|%d|%d|%d|%s|%s|%s|%s|%s|%v|%v|%d",
		m.currentKey,
		m.viewport.Width,
		msgCount,
		len(m.currentStream)+len(m.currentThinking),
		m.currentToolAction,
		m.pendingUserMessage,
		m.pendingApprovalID,
		m.approvalResult,
		m.activeGroupID,
		m.processing,
		m.compactFeedback != "",
		m.renderStartIdx,
	)
}

// shouldSkipViewportUpdate reports whether the current model state would
// produce exactly the same viewport content as the last render. Used to avoid
// redundant re-renders triggered by events that don't change visible output.
func (m *Model) shouldSkipViewportUpdate() bool {
	if m.currentKey == "" || m.showWelcome || m.selecting {
		return false
	}
	if m.parentSessionKey != "" {
		return false
	}
	// Always render when a modal is open (its content depends on more state).
	if m.modalMode != ModalNone {
		return false
	}
	key := m.getViewportContentKey()
	if key == m.lastViewportKey && m.renderedBaseValid {
		return true
	}
	m.lastViewportKey = key
	return false
}

func (m *Model) createNewChat() {
	newKey := fmt.Sprintf("tui:chat:%s", uuid.New().String())
	m.sessionMgr.GetOrCreate(newKey)
	_ = m.sessionMgr.SetMode(newKey, m.currentMode.String())

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
	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
	m.cleanupStreamingIfCompleteWithHistory(history)
}

// cleanupStreamingIfCompleteWithHistory is the variant that accepts an already-fetched
// history slice, avoiding a redundant GetHistoryView call.
func (m *Model) cleanupStreamingIfCompleteWithHistory(history []providers.Message) {
	if (m.currentStream == "" && m.currentThinking == "") || m.currentKey == "" {
		return
	}
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
	m.streamRenderedJoined = ""
	m.thinkingRenderedJoined = ""
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
	if isActive {
		m.startTime = time.Now()
		m.elapsedTime = 0
	}

	m.resetStreamState()
	m.currentToolAction = ""
	m.currentMessageID = ""
	m.currentAssistantMsgID = ""
	m.pendingSubagentCompletions = 0
	m.parentCompletionObserved = false
	m.pendingUserMessage = ""
	m.escHint = false
	m.escPressCount = 0
	m.escLastPress = time.Time{}

	// Clear pending approval state when switching sessions
	m.pendingApprovalID = ""
	m.pendingApprovalCmd = ""
	m.pendingApprovalReason = ""
	m.approvalResult = ""
	if !m.hasRunningSubagents() && !isActive {
		m.subagentProgress = make(map[string]string)
	}
	// Clear active group display when switching sessions (group maps persist)
	m.activeGroupID = ""
	// Invalidate rendered cache to force a full rebuild on next updateViewport
	m.renderedBaseValid = false
	m.renderedBaseKey = ""
	m.msgRenderCacheLines = nil // clear per-message cache on session switch
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

// subagentsCacheTTL bounds how often the expensive GetSessionSubagents lookup
// runs. It is called multiple times per frame (sidebar + processing checks)
// and scans all agents' subagent managers and session storage, taking write
// locks and possibly loading sessions from disk. Subagent lifecycle events
// (subagent.result, spawn tool.result, subagent progress) invalidate the
// cache immediately, so the TTL only throttles redundant lookups within a
// single burst of frames.
const subagentsCacheTTL = 500 * time.Millisecond

// getSessionSubagentsCached returns subagent tasks for the given session key,
// refreshing the underlying (expensive) backend call at most once per
// subagentsCacheTTL. Call invalidateSubagentsCache() when a subagent event
// changes the expected result.
func (m *Model) getSessionSubagentsCached(queryKey string) []channels.SubagentTaskInfo {
	if m.agentLoop == nil || queryKey == "" {
		return nil
	}
	if m.subagentsCacheKey == queryKey && time.Since(m.subagentsCacheTime) < subagentsCacheTTL {
		return m.subagentsCacheValue
	}
	subagents := m.agentLoop.GetProvidable().GetSessionSubagents(queryKey)
	m.subagentsCacheKey = queryKey
	m.subagentsCacheTime = time.Now()
	m.subagentsCacheValue = subagents
	return subagents
}

// invalidateSubagentsCache forces the next getSessionSubagentsCached call to
// hit the backend. Called on subagent lifecycle events.
func (m *Model) invalidateSubagentsCache() {
	m.subagentsCacheKey = ""
}

func (m *Model) hasRunningSubagents() bool {
	if m.agentLoop == nil || m.currentKey == "" {
		return false
	}
	subagentQueryKey := m.currentKey
	if !strings.HasPrefix(subagentQueryKey, "native:") {
		subagentQueryKey = "native:" + subagentQueryKey
	}
	subagents := m.getSessionSubagentsCached(subagentQueryKey)
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
	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)

	// Cache the O(n) role scan: the count only changes when len(history)
	// changes, and this is called multiple times per frame.
	if m.historyCountKey == m.currentKey && m.historyCountLen == len(history) {
		return m.historyCountValue
	}

	count := 0
	for _, msg := range history {
		if msg.Role == "user" || msg.Role == "assistant" {
			count++
		}
	}
	m.historyCountKey = m.currentKey
	m.historyCountLen = len(history)
	m.historyCountValue = count
	return count
}

// ResumeSessionID returns the session ID suitable for resuming the current chat session from the CLI.
// Returns an empty string if there is no active session.
func (m *Model) ResumeSessionID() string {
	key := m.currentKey
	if m.parentSessionKey != "" {
		key = m.parentSessionKey
	}
	if key == "" {
		return ""
	}
	if m.showWelcome && m.getHistoryMessageCount() == 0 {
		return ""
	}
	return strings.TrimPrefix(key, "tui:chat:")
}
