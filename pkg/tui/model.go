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
	// Initialize i18n with configured language. Downloaded packs live under
	// <leleDir>/locales/tui and are merged before language detection.
	i18n.SetPackDir(locales.JoinCacheDir(config.GetLeleDir()))
	i18n.LoadExternalPacks()
	i18n.InitWithLanguage(cfg.GetLanguage())

	// Multi-line chat input
	ta := textarea.New()
	ta.Placeholder = i18n.T("tui.placeholder")
	ta.Focus()
	ta.CharLimit = 0 // unlimited
	// Welcome screen renders the input inside a 60-col box (2 cols of
	// container padding); the chat view re-sets the width per frame.
	ta.SetWidth(58)
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
	// Minimal styling — blend with the TUI theme. Every sub-style of the
	// bubbles defaults must be overridden: the stock defaults emit raw basic
	// ANSI colors (\x1b[40m black background, \x1b[37m white foreground) that
	// clash with the app background and, after paintFrame's per-reset
	// background re-emission, show up as black patches inside the input box.
	// All styles stay foreground-only so the enclosing container background
	// (InputBarContainer) shows through. The styles are applied by
	// m.applyThemeToInputs() (set on the created Model below).

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
		templateInput:          newCommandTemplateInput(),
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

	// Load TUI theme from tui.json (never fatal — defaults to Dracula)
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

	// Apply theme colors to the input widgets (textarea + textinput). Must
	// run after m is created since it accesses m.chatInput and m.textInput.
	m.applyThemeToInputs()

	// Apply TUI config overrides. TUI defaults are hardcoded above (mouse on,
	// 200 messages, 32ms throttle). When the "tui" section exists in the config
	// (indicated reliably by MaxRenderedMessages > 0, since 0 is never a valid
	// value), read all fields from it so persisted user settings win.
	if cfg != nil {
		if cfg.TUI.MaxRenderedMessages > 0 {
			m.maxRenderedMessages = cfg.TUI.MaxRenderedMessages
			// TUI section is configured → the persisted mouse state is truth.
			m.mouseEnabled = cfg.TUI.MouseEnabled
		}
		if cfg.TUI.StreamThrottleMS > 0 {
			m.streamThrottleInterval = time.Duration(cfg.TUI.StreamThrottleMS) * time.Millisecond
		}
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

	// Detect first-run: no usable provider → activate onboarding wizard.
	// Only activate on a true first-run (no session resumed). If the user
	// resumed into an existing chat, stay in that chat view — onboarding is
	// meant for brand-new users (and must not hijack a resumed session's keys).
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
	// Invalidate rendered content cache — session history may have changed
	m.renderedBaseKey = ""

	m.visibleSessions = nil
	all := m.cachedSessionListing()

	// The TUI has no session-delete path of its own — a chat can disappear
	// from the list because it was deleted elsewhere (WebUI, backend). This is
	// the choke point that observes that, so stale backlogs are dropped here
	// instead of living in memory (or resurrecting if a key is ever reused).
	if len(m.messageQueue) > 0 {
		liveKeys := make([]string, 0, len(all))
		for _, s := range all {
			liveKeys = append(liveKeys, s.Key)
		}
		m.pruneQueueToSessions(liveKeys)
	}

	// Batch-fetch message counts once (single SQLite query for cold sessions)
	// instead of calling GetTotalMessageCount per session (N+1 queries). T8:
	// the cold half of this lookup is memoized inside pkg/session (see
	// storeCountsCache) and the resident half is computed from live slices, so
	// the chat on screen always reports its exact count while a burst of events
	// no longer re-scans the message table.
	msgCounts := m.sessionMgr.AllTotalMessageCounts()

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
		// A cold (Lru/TTL-evicted) session has nil Messages; visibility then
		// depends on the SQLite message count. Keep the current key and any
		// session that still has a name so idle eviction cannot make a chat
		// vanish from the list just because the count query missed.
		if len(s.Messages) > 0 || msgCounts[s.Key] > 0 || s.Key == m.currentKey || s.Name != "" {
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
				m.setCurrentChatKey(m.visibleSessions[0].Key)
			}
		}
	} else if len(m.visibleSessions) > 0 {
		m.selectedSessionIdx = 0
		m.setCurrentChatKey(m.visibleSessions[0].Key)
		m.showWelcome = false
	}

	// Clear pending user message if it now appears in the session history
	if m.pendingUserMessage != "" && m.currentKey != "" {
		history := m.historyView()
		for _, msg := range history {
			if msg.Role == "user" && msg.Content == m.pendingUserMessage {
				m.pendingUserMessage = ""
				break
			}
		}
	}

	// Clear streaming state if the assistant message is fully saved in history
	m.cleanupStreamingIfComplete()

	// Refresh the display-only archived prefix. Compaction (manual /compact or
	// the automatic threshold) evicts out-of-context messages from the in-memory
	// session while keeping them in SQLite; this reloads them for display only.
	// Safe here because reloadSessions runs exclusively on Update()/Init() paths
	// — never from View(), which would put SQLite I/O on the render hot path.
	// Placed before shouldSkipViewportUpdate so that archivedPrefix changes are
	// captured in getViewportContentKey and trigger a viewport rebuild.
	m.refreshArchivedHistory()

	// Skip the re-render if nothing user-visible changed. reloadSessions is
	// called on many events (including unrelated outbound events) and without
	// this guard every one of them would rebuild and re-render the entire
	// history — the dominant CPU cost for long conversations.
	if m.shouldSkipViewportUpdate() {
		return
	}

	m.updateViewport()
}

// sessionsRefreshTTL is the coalescing window of the session listing (T8).
//
// Re-walking the listing costs a ListSessions() pass plus AllTotalMessageCounts
// (444 µs / 179 KB / 815 allocs at 200 sessions, measured in
// docs/perf/tui-long-chat-baseline.md; the cold half is a full scan of
// session_messages). reloadSessions — the choke point that owns it — is called
// once per completed turn, per tab/ESC, per sidebar click and by /new, /clear,
// /compact, so during the many-finished-subagents scenario (every finished
// subagent restarts a turn on the visible chat, i.e. fires completeMsg) the cost
// multiplied by the event rate.
//
// Inside the window the cached listing is served instead: at most 4 walks/s
// (≈1.8 ms/s ≈ 0.2 % of a core at 200 sessions) rather than one per event. 250 ms
// is the shortest window that still collapses a burst a user perceives as
// instantaneous, and the observable staleness is limited to a chat that appeared
// or was renamed elsewhere (WebUI, cron) showing up in the sidebar at most that
// late — resident entries hold live *session.Session pointers, so no message
// content is ever stale. Every TUI action that must be exact bypasses the window
// instead of waiting for it (refreshSessionsCache(true): a switch onto a chat
// the snapshot does not know yet, and the /sessions picker).
const sessionsRefreshTTL = 250 * time.Millisecond

// refreshSessionsCache re-walks the session listing into sessionsCache. It is the
// ONLY caller of SessionManager.ListSessions on the TUI side — the constructor
// resolves the session to resume before this model exists, and the /sessions
// picker goes through freshSessionListing — so every other reader serves the
// snapshot (View() reads visibleSessions, which reloadSessions derives from it).
//
// Coalescing: at most one walk per sessionsRefreshTTL, however many events reach
// reloadSessions in between. force bypasses the window and is reserved for the
// two paths where the very next frame must observe the listing exactly (see the
// doc of sessionsCacheState and the callers).
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

// cachedSessionListing returns the session listing, re-walking it at most once
// per sessionsRefreshTTL. It must never be called from View(): the render path
// reads the derived visibleSessions instead.
func (m *Model) cachedSessionListing() []*session.Session {
	m.refreshSessionsCache(false)
	return m.sessionsCache.all
}

// freshSessionListing returns a just-walked listing, bypassing the window. Used
// by paths where a stale snapshot would be user-visible — currently the
// /sessions picker, whose whole purpose is to list the chats as they are now.
func (m *Model) freshSessionListing() []*session.Session {
	m.refreshSessionsCache(true)
	return m.sessionsCache.all
}

// sessionListedInCache reports whether the snapshot already contains key. It is
// the switch choke point's cheap check (O(sessions) pointer compares, no
// allocation): a switch between chats the listing already knows is fully
// represented by the snapshot, while a chat created after it was taken (a brand
// new chat's first message, /new) is the one case a stale listing cannot render,
// and forces a walk.
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

// getViewportContentKey returns the fingerprint of the state that affects the
// rendered viewport, for the Update-side skip guard (shouldSkipViewportUpdate):
// it lets an event that cannot change the visible output (most outbound events,
// unrelated reloadSessions) avoid an O(history) rebuild, which is the dominant
// CPU cost of a long conversation.
//
// It is the Update-side spelling of viewportContentKey — the very same function
// the render path compares and noteViewportMaterialized records — so a rebuild
// decision can never drift between the two sides. The Update path has no
// hoisted frame snapshot, so the resident count comes from historyCount:
// outside a frame that is one read, inside a frame it is the frame's snapshot.
func (m *Model) getViewportContentKey() string {
	return m.viewportContentKey(m.getHistoryMessageCount())
}

// shouldSkipViewportUpdate reports whether the current model state would
// produce exactly the same viewport content as the last render. Used to avoid
// redundant re-renders triggered by events that don't change visible output:
// the fingerprint is the one shared with the render path (getViewportContentKey
// delegates to viewportContentKey) and only the rebuild records it
// (noteViewportMaterialized), so "up to date" means the same thing here and in
// View().
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

// cleanupStreamingIfComplete clears streaming/thinking state if the last assistant
// message in history is no longer in streaming mode. This avoids stale content
// leaking into the viewport after the message is fully saved.
func (m *Model) cleanupStreamingIfComplete() {
	if (m.currentStream == "" && m.currentThinking == "") || m.currentKey == "" {
		return
	}
	history := m.historyView()
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

// setCurrentChatKey switches the visible session. It is the single choke point
// for currentKey assignments: if the outgoing session has a live visible
// approval prompt, it is stashed in pendingApprovals (instead of being
// silently abandoned by clearStreamingState) so it can be restored when the
// user navigates back to that session.
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
	// The archived prefix belongs to the session we are LEAVING. Reset it
	// immediately so the next frame cannot render stale archived messages
	// from the outgoing session, then load the one for the session we are
	// entering. Refreshing here — at the single choke point every switch
	// converges on — is deliberate: the modal session picker and the
	// subagent navigation switch keys WITHOUT calling reloadSessions(), so
	// relying on that convention silently left compacted history invisible
	// after switching chats. refreshArchivedHistory is a no-op (two O(1) stat
	// reads) when there is nothing evicted, and this stays off the render
	// path because every caller is an Update()-path route — never View().
	m.resetArchivedHistory()
	m.refreshArchivedHistory()
	// T8: the frame that follows reads visibleSessions — derived from the cached
	// session listing — for the sidebar rows and the selected index, so a switch
	// is one of the two places where the snapshot must be exact. The window is
	// only bypassed when the snapshot cannot represent the switch at all (a chat
	// created after it was taken: /new, the first message of a new chat); a
	// switch between two chats the listing already knows costs no walk, which is
	// what keeps a burst of navigation from re-walking the listing each time.
	if !m.sessionListedInCache(newKey) {
		m.refreshSessionsCache(true)
	}
	// T6: the subagent listing is session-scoped, so the switch is one of the
	// two places that refresh it eagerly (the other is the Update() tail in
	// Update): the new key makes the cached entry stale by construction, and
	// the rows are read by the frames that follow — plus by
	// clearStreamingState (hasRunningSubagents) a few lines below, which every
	// caller runs right after this function. Same rule as everything else here:
	// Update()-path only, never View().
	m.refreshSubagentsCache()
	// TUI-H2: the streaming overlay (currentStream/currentThinking) is
	// session-scoped presentation state. Every other session boundary clears
	// it (publishUserMessage, /compact, message.complete cleanup), but this
	// choke point did not — so frames buffered for the outgoing session kept
	// painting into the viewport of the session that came on screen (most
	// visibly after /new, which leaves the buffer populated while the welcome
	// view is replaced). Reset it here so no switch can inherit stale frames,
	// and drop the assistant-message id as well: the next turn on this session
	// may legitimately reuse it, and the append sites only reset the buffer
	// when the id *changes*.
	m.resetStreamState()
	m.currentAssistantMsgID = ""
	// TUI-M4: subagent progress is presentation state of the session we are
	// LEAVING — clear it on switch so the new session cannot inherit stale
	// progress lines (fresh entries are re-recorded by its own subagent
	// events). Deliberately separate from resetStreamState, which owns only
	// the stream text buffers.
	m.subagentProgress = make(map[string]string)
}

// queueApprovalForCurrentChat surfaces an approval.request that belongs to the
// session on screen: set the visible prompt fields and drop any stale snapshot
// for the same key (e.g. an earlier prompt for this session that was already
// answered or superseded) so it cannot be resurrected on the next switch.
func (m *Model) queueApprovalForCurrentChat(id, cmd, reason string) {
	delete(m.pendingApprovals, m.currentKey)
	m.pendingApprovalID = id
	m.pendingApprovalCmd = cmd
	m.pendingApprovalReason = reason
	m.approvalShowFull = false
	m.approvalResult = ""
}

// stashApprovalForSession parks an approval.request raised while its session
// is in the background, keyed by that session, so switching to it later
// restores the prompt (clearStreamingState) instead of the event being lost.
func (m *Model) stashApprovalForSession(chatID, id, cmd, reason string) {
	if m.pendingApprovals == nil {
		m.pendingApprovals = make(map[string]pendingApprovalSnapshot)
	}
	m.pendingApprovals[chatID] = pendingApprovalSnapshot{id: id, cmd: cmd, reason: reason}
}

// clearStreamingState resets all streaming/processing state.
// Called when switching sessions to avoid stale content leaking into the new session.
// It preserves m.processing when the target session has an active LLM loop,
// so the loading animation continues when switching to a busy session/subagent.
func (m *Model) clearStreamingState() {
	m.streamThrottleActive = false
	m.streamPendingUpdate = false
	m.compactFeedback = ""
	m.statusFeedback = ""

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
	// Switching to a chat that is still running a (possibly long) tool used to
	// lose its "running tool" row: the row was set exclusively by live
	// tool.executing events, and none is replayed on a switch. Ask the agent
	// loop — the same source of truth those events feed — for the tool it is
	// executing right now and restore the row, so the busy session looks
	// exactly as it did before the switch.
	if isActive {
		m.currentToolAction = m.inProgressToolAction(m.currentKey)
	}
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
	m.approvalShowFull = false
	m.approvalResult = ""

	// ...then restore an approval that belongs to the session we just switched
	// TO, if one was stashed (either abandoned via setCurrentChatKey or queued
	// by the outbound listener while the session was in the background).
	if snap, ok := m.pendingApprovals[m.currentKey]; ok {
		m.pendingApprovalID = snap.id
		m.pendingApprovalCmd = snap.cmd
		m.pendingApprovalReason = snap.reason
		delete(m.pendingApprovals, m.currentKey)
	}
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

// inProgressToolAction returns the row the chat's running tool must display,
// in the same "tool: arguments" form the live tool.executing handler uses (the
// pre-formatted action, falling back to the bare tool name), or "" when the
// session is not running a tool.
//
// It exists for the session-switch path: the row is otherwise only ever
// written by live tool.executing events, so a switch to a busy chat left the
// spinner running with no indication of what it was running.
func (m *Model) inProgressToolAction(sessionKey string) string {
	if m.agentLoop == nil || sessionKey == "" {
		return ""
	}
	// The loop is the same source of truth the live tool.executing events are
	// mirrored into, so the restored row and the live row agree.
	return formatInProgressToolAction(m.agentLoop.GetProvidable().GetInProgressTool(sessionKey))
}

// formatInProgressToolAction turns a recorded in-flight tool into the overlay
// activity row, preferring the backend's pre-formatted action ("tool:
// arguments") over the bare tool name. It returns "" for a nil record.
//
// Pure (no Model, no loop) on purpose: the record lives unexported inside the
// agent loop and is only ever populated by a real tool execution, so this is
// the layer of the session-switch restore a TUI test can pin — the fallback
// rule and the sanitization of a value that is LLM-controlled either way
// (same ingress sanitization as the live tool.executing handler).
func formatInProgressToolAction(tool *session.InProgressTool) string {
	if tool == nil {
		return ""
	}
	if tool.Action != "" {
		return sanitizeDisplayText(tool.Action)
	}
	return sanitizeDisplayText(tool.Tool)
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

// maxSubagentProgressLines caps how many subagent progress entries the
// viewport overlay shows per frame; the remainder is summarized as "+N more".
const maxSubagentProgressLines = 3

// subagentProgressCap bounds the size of the subagentProgress map. Each entry
// is tiny, but unauthenticated growth per parent turn would let the map leak
// across long sessions, so old entries are evicted (FIFO by task ID suffix
// number) once the cap is hit (TUI-M4).
const subagentProgressCap = 16

// recordSubagentProgress stores the latest action for a running subagent task
// and keeps the map bounded. Written from subagent tool.executing /
// message.stream events; read by renderSubagentProgress in the overlay.
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
	// Sanitize at record time: the action comes from subagent tool.executing /
	// message.stream events (agent/LLM-controlled) and may carry control
	// chars, ANSI escapes or bidi/zero-width Cf that desync painted columns
	// from measured widths. Eviction above stays untouched.
	m.subagentProgress[taskID] = sanitizeDisplayText(action)
}

// oldestSubagentTaskID returns the task ID with the smallest "subagent-<n>"
// suffix, i.e. the longest-running tracked task. Unparsable IDs sort last.
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

// isChatSidebarVisible reports whether the right sidebar (which already lists
// subagent statuses) is being rendered. The inline subagent progress overlay is
// redundant while the sidebar is visible, so it is only shown on screens that
// hide the sidebar: the welcome screen, the onboarding wizard, and active modals.
func (m *Model) isChatSidebarVisible() bool {
	return !m.showWelcome && !m.onboardingActive && m.modalMode == ModalNone
}

// hasSubagentProgressOverlay reports whether the inline subagent progress block
// contributes overlay content (data present AND the sidebar not showing it).
func (m *Model) hasSubagentProgressOverlay() bool {
	return len(m.subagentProgress) > 0 && !m.isChatSidebarVisible()
}

// renderSubagentProgress renders one line per running subagent task so the
// parent viewport shows real-time subagent activity under the streaming
// overlay (TUI-M4). Shows at most maxSubagentProgressLines entries, with the
// rest summarized as "+N more".
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
	// Deterministic numeric order: subagent-1, subagent-2, …
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
		// id suffix and the stored action both originate from bus event
		// metadata: sanitize both so no control/bidi char reaches the frame.
		short := sanitizeDisplayText(strings.TrimPrefix(id, "subagent-"))
		// Wrap-aware row construction: renderToolCallRow is the same primitive
		// the streaming tool-action row uses — it wraps BEFORE styling with a
		// 2-cell continuation indent and keeps the exact current row-0 style
		// (ToolCallLabel "  " + ToolCallName), so an over-wide action can
		// never hand lineViewport a row it would re-wrap at paint time (that
		// re-wrap drops the indent and shifts every row below: frame corruption
		// cascade). Chosen over a local wrapText+prefix loop because the
		// wrap-before-style rule already lives in renderToolCallRow (DRY: one
		// owner instead of a second copy of the indent/continuation logic).
		// sanitizeDisplayText runs again here as defense in depth:
		// recordSubagentProgress cleans event ingress, but this map is
		// presentation state and must not leak controls/bidi into the frame
		// regardless of writer (pinned by TestSpecialChars_SubagentProgressOverWide).
		row := short + " " + sanitizeDisplayText(m.subagentProgress[id])
		sb.WriteString(renderToolCallRow(row, m.viewport.Width) + "\n")
	}
	if extra := len(ids) - shown; extra > 0 {
		sb.WriteString(ToolCallLabel.Render("  ") + ToolCallName.Render(fmt.Sprintf("+%d more", extra)) + "\n")
	}
	return sb.String()
}

// subagentsCacheTTL is the refresh cadence of the subagent listing cache (T6).
//
// GetSessionSubagents scans every manager's tasks (deep-copying each one) and
// every agent's session storage (O(total sessions), plus one SQLite stats query
// per chunk of 200 cold keys): 2.0 ms / 414 KB at 200 subagents and 6.9 ms /
// 1.2 MB at 500 cold ones (docs/perf/tui-long-chat-baseline.md). The sidebar and
// the status line both used to call it from View() — i.e. on every frame — and
// every subagent lifecycle event invalidated the cache, so a burst of finished
// subagents paid the full lookup again and again.
//
// The refresh now happens ONLY from Update() (see refreshSubagentsCache) and at
// most once per window, so 50 subagent.result events in a second coalesce into
// a single lookup. The render path serves the last known listing, which may
// therefore be up to subagentsCacheTTL stale. That bound is deliberate and
// acceptable: it is a sidebar listing (and the "running" indicator of the
// status line, which stops within the same bound because the spinner's own tick
// chain keeps feeding Update until the refreshed count reaches 0).
const subagentsCacheTTL = 3 * time.Second

// subagentsCacheRunning is the cached status value of a running subagent task.
// It is compared literally (as the sidebar rows already do) to stay independent
// of pkg/tools' constants in this package.
const subagentsCacheRunning = "running"

// subagentQueryKey returns the key GetSessionSubagents must be queried with for
// the visible chat: a subagent view lists its PARENT's subagents (the sidebar
// swaps the view, see renderSidebarSubagents), and a task's OriginSessionKey is
// always "native:<chatID>". Returns "" when there is nothing to query.
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

// refreshSubagentsCache loads the subagent listing for the visible chat into
// subagentsCache. It is the ONLY writer of the cache and the only subagent
// listing call reachable from pkg/tui's Update()-path entry points — View()
// must never call it (TestView_SubagentListingIsCacheOnly pins that).
//
// Coalescing: the backend is queried at most once per subagentsCacheTTL, no
// matter how many lifecycle events (subagent.result, spawn tool.result, ticks,
// keystrokes, mouse motion) arrive in between. A session switch changes the
// query key and refreshes immediately, so the new session never serves the
// previous listing.
//
// It runs on the Update() side only, which is where the cost belongs: up to
// ~7 ms of SQLite-backed work paid once per window instead of inside every
// frame. Returns true when the backend was actually queried.
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
	// Sort here, once: the render path only reads the slice, so it can neither
	// re-sort a 200-entry listing every frame nor mutate what the cache holds.
	sortSubagents(list)
	c.key = key
	c.list = list
	c.running = countRunningSubagents(list)
	c.at = time.Now()
	return true
}

// cachedSessionSubagents returns the cached subagent listing of the visible
// chat. O(1) and allocation-free: the retained slice, never a backend call, a
// copy or a sort. The result may be up to subagentsCacheTTL stale — the cache
// is refreshed from Update(), not from here.
func (m *Model) cachedSessionSubagents() []channels.SubagentTaskInfo {
	key := m.subagentQueryKey()
	if key == "" || m.subagentsCache.key != key {
		return nil
	}
	return m.subagentsCache.list
}

// countRunningSubagents counts the entries of a listing that are running. It is
// the O(n) half of the cache refresh: hasRunningSubagents then answers in O(1)
// from the cached count instead of building and scanning the listing.
func countRunningSubagents(list []channels.SubagentTaskInfo) int {
	running := 0
	for i := range list {
		if list[i].Status == subagentsCacheRunning {
			running++
		}
	}
	return running
}

// hasRunningSubagents reports whether the visible chat has a running subagent
// (the status-line spinner and several Update()-side busy gates ask this every
// frame). O(1) and allocation-free: the count materialized by the last cache
// refresh — never the listing, never a backend call.
//
// "Visible chat" is the same listing the sidebar shows, i.e. the parent chat
// while a subagent view is on screen. That is intentionally wider than the old
// per-session query (which looked up the subagent's own sub-subagents): the
// status line describes the session being viewed, and isSessionProcessing
// already skips this check for subagent sessions (isSubagentSession).
func (m *Model) hasRunningSubagents() bool {
	key := m.subagentQueryKey()
	if key == "" || m.subagentsCache.key != key {
		return false
	}
	return m.subagentsCache.running > 0
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

// getHistoryMessageCount returns the number of user+assistant messages in the
// current session's resident history. It is a thin alias over the render
// frame's snapshot (historyCount): the history is read at most once per frame,
// so calling this several times in a frame — as the token cache key and the
// viewport-skip fingerprint do — costs nothing and always yields the same
// value. Outside a frame it reads the history once per call, as before.
func (m *Model) getHistoryMessageCount() int {
	return m.historyCount()
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
