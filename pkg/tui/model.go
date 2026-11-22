package tui

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/keepawake"
	"github.com/xilistudios/lele/pkg/locales"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tui/i18n"
	"github.com/xilistudios/lele/pkg/tui/theme"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func NewModel(cfg *config.Config, agentLoop *agent.AgentLoop, sessionMgr *session.SessionManager, initialSessionID ...string) *Model {
	i18n.SetPackDir(locales.JoinCacheDir(config.GetLeleDir()))
	i18n.LoadExternalPacks()
	i18n.InitWithLanguage(cfg.GetLanguage())

	ta := textarea.New()
	ta.Placeholder = i18n.T("tui.placeholder")
	ta.Focus()
	ta.CharLimit = 0
	ta.SetWidth(58)
	ta.SetHeight(3)
	ta.Prompt = " "
	ta.ShowLineNumbers = false
	ta.EndOfBufferCharacter = ' '
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
	}

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
		templateInput:          newCommandTemplateInput(),
		activePane:             ChatViewPane,
		showWelcome:            true,
		workspacePath:          workspacePath,
		gitBranch:              getGitBranch(workspacePath),
		sessionStartTime:       now,
		subagentProgress:       make(map[string]string),
		streamThrottleInterval: 32 * time.Millisecond,
		mouseEnabled:           true,
		keepAwake:              keepawake.New(),
		mcpAuthOpener:          mcpAuthBrowserOpener,
		cmdUsage:               newCommandUsageStore(commandUsagePath()),
		maxRenderedMessages:    200,
		renderStartIdx:         -1,
	}

	themePath := theme.DefaultPath()
	themeName, customThemes, installedCommunity, err := theme.Load(themePath)
	if err != nil {
		log.Printf("warning: could not load tui.json: %v", err)
	}
	if themeName == "" {
		themeName = "dracula"
	}
	m.currentThemeName = themeName
	m.customThemes = customThemes
	m.installedCommunity = installedCommunity
	ApplyTheme(theme.Get(themeName, customThemes))

	m.applyThemeToInputs()

	if cfg != nil {
		if cfg.TUI.MaxRenderedMessages > 0 {
			m.maxRenderedMessages = cfg.TUI.MaxRenderedMessages
			m.mouseEnabled = cfg.TUI.MouseEnabled
		}
		if cfg.TUI.StreamThrottleMS > 0 {
			m.streamThrottleInterval = time.Duration(cfg.TUI.StreamThrottleMS) * time.Millisecond
		}
		m.keepAwakeEnabled = cfg.TUI.KeepAwake
	}

	cronStorePath := filepath.Join(cfg.WorkspacePath(), "cron", "jobs.json")
	m.cronService = cron.NewCronService(cronStorePath, nil)

	if len(initialSessionID) > 0 && initialSessionID[0] != "" {
		sid := initialSessionID[0]
		sessions := sessionMgr.ListSessions()
		found := false
		for _, s := range sessions {
			if s.Key == sid || strings.TrimPrefix(s.Key, "tui:chat:") == sid || s.Key == "tui:chat:"+sid {
				m.setCurrentChatKey(s.Key)
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

	if cfg != nil && !cfg.HasUsableProvider() && !cfg.TUI.OnboardingCompleted && m.currentKey == "" {
		m.onboardingActive = true
		m.onboardingStep = obWelcome
		m.showWelcome = true
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
	m.renderedBaseKey = ""

	m.visibleSessions = nil
	all := m.cachedSessionListing()

	if len(m.messageQueue) > 0 {
		liveKeys := make([]string, 0, len(all))
		for _, s := range all {
			liveKeys = append(liveKeys, s.Key)
		}
		m.pruneQueueToSessions(liveKeys)
	}

	msgCounts := m.sessionMgr.AllTotalMessageCounts()

	for _, s := range all {
		if isSubagentSessionKey(s.Key) {
			continue
		}
		sessionMode := s.Mode
		if sessionMode == "" {
			sessionMode = "agent"
		}
		if sessionMode != m.currentMode.String() {
			continue
		}
		if len(s.Messages) > 0 || msgCounts[s.Key] > 0 || s.Key == m.currentKey || s.Name != "" {
			m.visibleSessions = append(m.visibleSessions, s)
		}
	}

	if m.showWelcome {
		return
	}

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
			if m.currentKey == "" {
				m.setCurrentChatKey(m.visibleSessions[0].Key)
			}
		}
	} else if len(m.visibleSessions) > 0 {
		m.selectedSessionIdx = 0
		m.setCurrentChatKey(m.visibleSessions[0].Key)
		m.showWelcome = false
	}

	if m.pendingUserMessage != "" && m.currentKey != "" {
		history := m.historyView()
		for _, msg := range history {
			if isPendingUserMessage(msg, m.pendingUserMessage) {
				m.pendingUserMessage = ""
				break
			}
		}
	}

	m.cleanupStreamingIfComplete()

	m.refreshArchivedHistory()

	if m.shouldSkipViewportUpdate() {
		return
	}

	m.updateViewport()
}

const sessionsRefreshTTL = 250 * time.Millisecond

func (m *Model) refreshSessionsCache(force bool) {
	c := &m.sessionsCache
	if c.loaded && !force && time.Since(c.at) < sessionsRefreshTTL {
		return
	}
	if m.onSessionsRefresh != nil {
		m.onSessionsRefresh()
	}
	c.all = m.sessionMgr.ListSessions()
	c.at = time.Now()
	c.loaded = true
}

func (m *Model) cachedSessionListing() []*session.Session {
	m.refreshSessionsCache(false)
	return m.sessionsCache.all
}

func (m *Model) freshSessionListing() []*session.Session {
	m.refreshSessionsCache(true)
	return m.sessionsCache.all
}

func (m *Model) sessionListedInCache(key string) bool {
	if !m.sessionsCache.loaded {
		return false
	}
	for _, s := range m.sessionsCache.all {
		if s.Key == key {
			return true
		}
	}
	return false
}

func (m *Model) getViewportContentKey() string {
	return m.viewportContentKey(m.getHistoryMessageCount())
}

func (m *Model) shouldSkipViewportUpdate() bool {
	if m.currentKey == "" || m.showWelcome || m.selecting {
		return false
	}
	if m.parentSessionKey != "" {
		return false
	}
	if m.modalMode != ModalNone {
		return false
	}
	return m.getViewportContentKey() == m.lastViewportKey && m.renderedBaseValid
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

	m.setCurrentChatKey(newKey)
	m.forceGotoBottom = true
}

func (m *Model) cleanupStreamingIfComplete() {
	if (m.currentStream == "" && m.currentThinking == "") || m.currentKey == "" {
		return
	}
	history := m.historyView()
	m.cleanupStreamingIfCompleteWithHistory(history)
}

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

func (m *Model) resetStreamState() {
	m.currentStream = ""
	m.currentThinking = ""
	m.streamRenderedLines = nil
	m.thinkingRenderedLines = nil
	m.streamRenderedJoined = ""
	m.thinkingRenderedJoined = ""
	m.streamTokens = 0
	m.streamElapsedMs = 0
	m.streamTokPerSec = 0
	m.streamMetricsEstimated = false
	m.lastTokPerSec = 0
}

func (m *Model) setCurrentChatKey(newKey string) {
	if newKey == m.currentKey {
		return
	}
	if m.pendingApprovalID != "" {
		if m.pendingApprovals == nil {
			m.pendingApprovals = make(map[string]pendingApprovalSnapshot)
		}
		m.pendingApprovals[m.currentKey] = pendingApprovalSnapshot{
			id:     m.pendingApprovalID,
			cmd:    m.pendingApprovalCmd,
			reason: m.pendingApprovalReason,
		}
	}
	m.currentKey = newKey
	m.resetArchivedHistory()
	m.refreshArchivedHistory()
	if !m.sessionListedInCache(newKey) {
		m.refreshSessionsCache(true)
	}
	m.refreshSubagentsCache()
	m.resetStreamState()
	m.currentAssistantMsgID = ""
	m.subagentProgress = make(map[string]string)
}

func (m *Model) queueApprovalForCurrentChat(id, cmd, reason string) {
	delete(m.pendingApprovals, m.currentKey)
	m.pendingApprovalID = id
	m.pendingApprovalCmd = cmd
	m.pendingApprovalReason = reason
	m.approvalShowFull = false
	m.approvalResult = ""
}

func (m *Model) stashApprovalForSession(chatID, id, cmd, reason string) {
	if m.pendingApprovals == nil {
		m.pendingApprovals = make(map[string]pendingApprovalSnapshot)
	}
	m.pendingApprovals[chatID] = pendingApprovalSnapshot{id: id, cmd: cmd, reason: reason}
}

func (m *Model) clearStreamingState() {
	m.streamThrottleActive = false
	m.streamPendingUpdate = false
	m.compactFeedback = ""
	m.statusFeedback = ""

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
	if isActive {
		m.currentToolAction = m.inProgressToolAction(m.currentKey)
	}
	m.currentMessageID = ""
	m.currentAssistantMsgID = ""
	m.subagentContinuationPending = false
	m.pendingUserMessage = ""
	m.escHint = false
	m.escPressCount = 0
	m.escLastPress = time.Time{}

	m.pendingApprovalID = ""
	m.pendingApprovalCmd = ""
	m.pendingApprovalReason = ""
	m.approvalShowFull = false
	m.approvalResult = ""

	if snap, ok := m.pendingApprovals[m.currentKey]; ok {
		m.pendingApprovalID = snap.id
		m.pendingApprovalCmd = snap.cmd
		m.pendingApprovalReason = snap.reason
		delete(m.pendingApprovals, m.currentKey)
	}
	if !m.hasRunningSubagents() && !isActive {
		m.subagentProgress = make(map[string]string)
	}
	m.activeGroupID = ""
	m.renderedBaseValid = false
	m.renderedBaseKey = ""
	m.msgRenderCacheLines = nil
	m.forceGotoBottom = true
}

func (m *Model) inProgressToolAction(sessionKey string) string {
	if m.agentLoop == nil || sessionKey == "" {
		return ""
	}
	return formatInProgressToolAction(m.agentLoop.GetProvidable().GetInProgressTool(sessionKey))
}

func formatInProgressToolAction(tool *session.InProgressTool) string {
	if tool == nil {
		return ""
	}
	if tool.Action != "" {
		return sanitizeDisplayText(tool.Action)
	}
	return sanitizeDisplayText(tool.Tool)
}

func isSubagentSessionKey(key string) bool {
	return strings.Contains(key, ":subagent-")
}

func (m *Model) isSubagentOfCurrentChat(chatID string) bool {
	nativePrefixed := "native:" + m.currentKey
	return strings.HasPrefix(chatID, nativePrefixed+":subagent-")
}

func (m *Model) currentSubagentTaskID(chatID string) string {
	nativePrefixed := "native:" + m.currentKey + ":"
	if strings.HasPrefix(chatID, nativePrefixed) {
		return chatID[len(nativePrefixed):]
	}
	return ""
}

const maxSubagentProgressLines = 3

const subagentProgressCap = 16

func (m *Model) recordSubagentProgress(taskID, action string) {
	if m.subagentProgress == nil {
		m.subagentProgress = make(map[string]string)
	}
	if _, exists := m.subagentProgress[taskID]; !exists && len(m.subagentProgress) >= subagentProgressCap {
		oldest := m.oldestSubagentTaskID()
		if oldest != "" {
			delete(m.subagentProgress, oldest)
		}
	}
	m.subagentProgress[taskID] = sanitizeDisplayText(action)
}

func (m *Model) oldestSubagentTaskID() string {
	oldest := ""
	oldestNum := int64(-1)
	for id := range m.subagentProgress {
		num := int64(1<<62 - 1)
		if suffix, ok := strings.CutPrefix(id, "subagent-"); ok {
			if parsed, err := strconv.ParseInt(suffix, 10, 64); err == nil {
				num = parsed
			}
		}
		if oldest == "" || num < oldestNum {
			oldest = id
			oldestNum = num
		}
	}
	return oldest
}

func (m *Model) isChatSidebarVisible() bool {
	return !m.showWelcome && !m.onboardingActive && m.modalMode == ModalNone
}

func (m *Model) hasSubagentProgressOverlay() bool {
	return len(m.subagentProgress) > 0 && !m.isChatSidebarVisible()
}

func (m *Model) renderSubagentProgress() string {
	if m.isChatSidebarVisible() {
		return ""
	}
	if len(m.subagentProgress) == 0 {
		return ""
	}

	ids := make([]string, 0, len(m.subagentProgress))
	for id := range m.subagentProgress {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ni, ji := int64(1<<62-1), int64(1<<62-1)
		if s, ok := strings.CutPrefix(ids[i], "subagent-"); ok {
			if parsed, err := strconv.ParseInt(s, 10, 64); err == nil {
				ni = parsed
			}
		}
		if s, ok := strings.CutPrefix(ids[j], "subagent-"); ok {
			if parsed, err := strconv.ParseInt(s, 10, 64); err == nil {
				ji = parsed
			}
		}
		return ni < ji
	})

	var sb strings.Builder
	sb.WriteString(ToolCallLabel.Render("  ⏳ subagents") + "\n")
	shown := min(len(ids), maxSubagentProgressLines)
	for _, id := range ids[:shown] {
		short := sanitizeDisplayText(strings.TrimPrefix(id, "subagent-"))
		row := short + " " + sanitizeDisplayText(m.subagentProgress[id])
		sb.WriteString(renderToolCallRow(row, m.viewport.Width) + "\n")
	}
	if extra := len(ids) - shown; extra > 0 {
		sb.WriteString(ToolCallLabel.Render("  ") + ToolCallName.Render(fmt.Sprintf("+%d more", extra)) + "\n")
	}
	return sb.String()
}

const subagentsCacheTTL = 3 * time.Second

const subagentsCacheRunning = "running"

func (m *Model) subagentQueryKey() string {
	if m.agentLoop == nil || m.currentKey == "" {
		return ""
	}
	key := m.currentKey
	if m.parentSessionKey != "" {
		key = m.parentSessionKey
	}
	if !strings.HasPrefix(key, "native:") {
		key = "native:" + key
	}
	return key
}

func (m *Model) refreshSubagentsCache() bool {
	key := m.subagentQueryKey()
	if key == "" {
		return false
	}
	c := &m.subagentsCache
	if c.key == key && time.Since(c.at) < subagentsCacheTTL {
		return false
	}
	if m.onSubagentsRefresh != nil {
		m.onSubagentsRefresh()
	}
	list := m.agentLoop.GetProvidable().GetSessionSubagents(key)
	sortSubagents(list)
	c.key = key
	c.list = list
	c.running = countRunningSubagents(list)
	c.at = time.Now()
	return true
}

func (m *Model) cachedSessionSubagents() []channels.SubagentTaskInfo {
	key := m.subagentQueryKey()
	if key == "" || m.subagentsCache.key != key {
		return nil
	}
	return m.subagentsCache.list
}

func countRunningSubagents(list []channels.SubagentTaskInfo) int {
	running := 0
	for i := range list {
		if list[i].Status == subagentsCacheRunning {
			running++
		}
	}
	return running
}

func (m *Model) hasRunningSubagents() bool {
	key := m.subagentQueryKey()
	if key == "" || m.subagentsCache.key != key {
		return false
	}
	return m.subagentsCache.running > 0
}

func (m *Model) isSubagentSession(sessionKey string) bool {
	return strings.Contains(sessionKey, ":subagent-") || strings.HasPrefix(sessionKey, "subagent:")
}

func (m *Model) isSessionProcessing() bool {
	backendProcessing := m.currentKey != "" && m.agentLoop != nil &&
		m.agentLoop.GetProvidable().IsSessionProcessing(m.currentKey)
	if backendProcessing {
		return true
	}

	if m.processing && !m.startTime.IsZero() && time.Since(m.startTime) < 3*time.Second {
		return true
	}

	if m.processing {
		m.processing = false
	}
	return false
}

func (m *Model) currentSessionKey() string {
	return m.currentKey
}

func (m *Model) getHistoryMessageCount() int {
	return m.historyCount()
}

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
