package tui

import (
	"context"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/session"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
)

type paneType int

const (
	ChatsPane paneType = iota
	ChatViewPane
)

// leftColumnRatio is the fraction of the terminal width used by the left
// (chat) column. The right sidebar takes the remaining space. Shared by
// view.go (layout) and handlers.go (mouse hit-testing) so they stay in sync.
const leftColumnRatio = 0.72

// subagentClickTarget tracks the position of a subagent item in the sidebar for mouse click handling
type subagentClickTarget struct {
	yStart int    // Starting Y position in the sidebar (0-indexed from top of content area)
	yEnd   int    // Ending Y position in the sidebar
	key    string // Session key for this subagent
}

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

	// Navigation: non-empty when viewing a subagent chat (stores the parent key)
	parentSessionKey string

	// Subagent progress: taskID -> last action string (for real-time display in parent)
	subagentProgress map[string]string

	// Message processing / streaming
	processing            bool
	currentMessageID      string
	currentAssistantMsgID string
	currentStream         string
	currentThinking       string
	currentToolAction     string // active tool call shown during streaming ("tool: args")
	startTime             time.Time
	elapsedTime           time.Duration
	lastDuration          time.Duration
	animationTick         int

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

	// Session summary tracking
	lastEscTime      time.Time
	sessionStartTime time.Time

	// CSI escape sequence parser state — filters leaked mouse/escape
	// fragments that bubbletea fails to parse as tea.MouseMsg.
	escSeqActive   bool
	escSeqLastRune time.Time

	// Terminal size
	width  int
	height int

	// Stream rendering throttle — avoids re-rendering the viewport on every
	// single streaming chunk (which can arrive dozens of times per second).
	// Only message.stream and message.thinking are throttled; tool events
	// and subagent progress continue to update immediately.
	streamThrottleActive   bool
	streamPendingUpdate    bool
	streamThrottleInterval time.Duration
	streamRenderedLines    []string
	thinkingRenderedLines  []string

	// Cached glamour renderer (keyed by width)
	cachedRenderer      *glamour.TermRenderer
	cachedRendererWidth int

	// Cached rendered content for completed messages (avoids re-rendering
	// all messages through glamour on every streaming chunk).
	renderedBase         string
	renderedBaseKey      string // session key the cache belongs to
	renderedBaseMsgCount int    // number of history messages when cache was built

	// Subagent click targets in sidebar — tracks Y positions for mouse clicks
	subagentClickTargets []subagentClickTarget

	// mouseEnabled tracks whether mouse capture is active. When false, the
	// terminal handles native text selection/copy. Toggled with ctrl+t.
	mouseEnabled bool
}
