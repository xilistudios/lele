package tui

import (
	"context"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/session"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
)

type paneType int

const (
	ChatsPane paneType = iota
	ChatViewPane
)

// chatMode represents the active TUI mode (Agent, Chat, or Group).
type chatMode int

const (
	ModeAgent chatMode = iota // default
	ModeChat
	ModeGroup
)

func (m chatMode) String() string {
	switch m {
	case ModeChat:
		return "chat"
	case ModeGroup:
		return "group"
	default:
		return "agent"
	}
}

// leftColumnRatio is the fraction of the terminal width used by the left
// (chat) column. The right sidebar takes the remaining space. Shared by
// view.go (layout) and handlers.go (mouse hit-testing) so they stay in sync.
const leftColumnRatio = 0.72

// groupTurn represents a single turn in a group chat (Mixture of Agents).
type groupTurn struct {
	index   int
	layer   int
	speaker string
	label   string
	role    string
	content string
}

// groupMeta stores accumulated metadata for a completed group session.
type groupMeta struct {
	strategy     string
	layers       int
	totalTokens  int
	participants string // comma-separated participant list
	synthesis    string // final synthesis content from group.complete
}

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
	ModalBackgroundExecs
	ModalProviders      // list of providers
	ModalProviderDetail // provider detail (edit/delete/add model)
	ModalAddProvider    // form to add a new provider
	ModalAddModel       // form to add a new model to a provider
	ModalCron           // list of cron jobs
	ModalSecrets        // list of keyring secrets
	ModalAddSecret      // form to add a new secret
)

type formStep int

const (
	formStepInput formStep = iota
	formStepConfirm
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
	{name: "/bg", description: "View background processes"},
	{name: "/cron", description: "Manage scheduled cron jobs"},
	{name: "/providers", description: "Manage providers"},
	{name: "/connect", description: "Connect a new provider"},
	{name: "/secrets", description: "Manage secrets (keyring)"},
	{name: "/compact", description: "Compact conversation history"},
	{name: "/goal", description: "Set a persistent goal (status/pause/resume/clear)"},
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
type compactResultMsg struct {
	result     string
	sessionKey string
}

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
	currentMode        chatMode // current mode filter: Agent (default), Chat, or Group
	groupProfileIdx    int      // selected profile index in Group mode welcome screen
	showWelcome        bool     // true when showing the welcome/new-chat screen

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

	// Background exec view state
	bgExecViewMode   bool     // true when showing output of a selected process
	bgExecViewID     string   // ID of the process being viewed
	bgExecViewOutput string   // current output text
	bgExecViewStatus string   // current status
	bgExecModalKeys  []string // maps modal items to process IDs

	// Cron job management state
	cronService     *cron.CronService // read/manage access to the cron store
	cronModalKeys   []string          // maps modal items to job IDs
	cronDetailMode  bool              // true when showing detail of a selected job
	cronDetailJobID string            // ID of the job being viewed in detail

	// Secrets (keyring) management state
	secretsModalKeys  []string // maps modal items to secret names
	secretsDetailMode bool     // true when showing detail of a selected secret
	secretsDetailName string   // name of the secret being viewed in detail
	secretsReveal     bool     // true when the secret value is temporarily revealed

	// Provider management state
	providerModalKeys    []string // maps modal items to provider names (for /providers)
	providerSelectedName string   // currently selected provider name in detail view
	providerEditMode     bool     // true when editing an existing provider
	providerSavedInFlow  bool     // true after /connect saves provider (enables model config steps)

	// Form state for add-provider / add-model flows
	formStepIndex   int      // current step in the form
	formValues      []string // collected values per step
	formError       string   // validation error to display
	formConfirmMode bool     // true when showing confirmation step

	// Provider-type picker state (step 2 of /connect). When true, the form
	// shows a selectable list of known provider presets instead of a raw text
	// input; up/down + enter pick a preset, "custom" allows a free-form type.
	providerTypePicker    bool // true while picking a provider type from the preset list
	providerTypePickerIdx int  // currently highlighted preset index
	providerTypePickerMax int  // number of presets shown (including "custom" entry)

	// Success screen shown after the /connect flow completes. When true, the
	// form modal renders a confirmation with the provider/model that was saved
	// instead of the step list.
	connectSuccess bool // true when showing the post-save success screen

	// providerTypeFromPreset remembers whether the chosen provider type came
	// from a preset picker selection. When true, the API Base step is
	// pre-filled and optional; a custom type requires an explicit base URL.
	providerTypeFromPreset bool // true when the type was picked from the preset list

	// Sub-components
	viewport  viewport.Model
	textInput textinput.Model // single-line input for modal forms (AddProvider, AddModel)
	chatInput textarea.Model  // multi-line input for chat messages

	// Pending user message (shown immediately before agent responds)
	pendingUserMessage string

	// Navigation: non-empty when viewing a subagent chat (stores the parent key)
	parentSessionKey string

	// Subagent progress: taskID -> last action string (for real-time display in parent)
	subagentProgress map[string]string

	// Group chat (Mixture of Agents) state
	groupTranscripts map[string][]groupTurn // groupID -> ordered turns
	groupStatus      map[string]string      // groupID -> status (started/done/stopped/error)
	activeGroupID    string                 // group currently being displayed
	groupMeta        map[string]groupMeta   // groupID -> accumulated metadata

	// Message processing / streaming
	processing            bool
	currentMessageID      string
	currentAssistantMsgID string
	// A subagent result is queued as a follow-up message for the parent. Keep
	// the parent turn active across its completion and the continuation turn.
	pendingSubagentCompletions int
	parentCompletionObserved   bool
	currentStream              string
	currentThinking            string
	currentToolAction          string // active tool call shown during streaming ("tool: args")
	startTime                  time.Time
	elapsedTime                time.Duration
	lastDuration               time.Duration
	animationTick              int
	tickPending                bool // prevents multiple tick chains from accumulating

	// Double-ESC cancel tracking
	escPressCount int
	escLastPress  time.Time
	escHint       bool // true when showing "press ESC again to cancel" hint

	// Text selection state (in-app click+drag selection in the viewport)
	selecting           bool      // whether a selection drag is in progress
	selStartX           int       // screen X (column) where selection started
	selStartY           int       // screen Y where selection started
	selEndX             int       // screen X (column) where selection currently ends
	selEndY             int       // screen Y where selection currently ends
	selectionFeedback   bool      // show brief "Copied!" feedback
	selectionFeedbackAt time.Time // when the feedback was triggered

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
	escBuffer      []rune // accumulates incomplete SGR mouse escape fragments

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
	// Accumulated joined string of completed rendered lines. Avoids O(n²)
	// strings.Join on every streaming chunk — new lines are appended in O(1).
	streamRenderedJoined   string
	thinkingRenderedJoined string

	// Cached glamour renderer (keyed by width)
	cachedRenderer      *glamour.TermRenderer
	cachedRendererWidth int

	// Cached rendered content for completed messages (avoids re-rendering
	// all messages through glamour on every streaming chunk).
	renderedBase         string
	renderedBaseKey      string // session key the cache belongs to
	renderedBaseMsgCount int    // number of history messages when cache was built

	// lastViewportKey is a fingerprint of the last rendered viewport state.
	// shouldSkipViewportUpdate compares against it to skip redundant re-renders.
	lastViewportKey string

	// Virtualized rendering: only render messages visible in the viewport
	// plus a small buffer above/below for smooth scrolling.
	renderedMsgStartIdx int // first message index included in renderedBase
	renderedMsgEndIdx   int // last message index included in renderedBase (exclusive)
	maxRenderedMessages int // max messages to render at once (0 = unlimited, backward compat)

	// Cached token/context usage for the sidebar. GetCurrentContextUsage is
	// expensive (it rebuilds the system prompt from disk and estimates tokens
	// over the whole history), so it must NOT run on every View() render.
	// The cache is refreshed at most once per tokenCacheTTL and is invalidated
	// immediately when the history message count changes.
	tokenCacheKey       string    // sessionKey:msgCount the cache belongs to
	tokenCacheTime      time.Time // when the cache was last refreshed
	tokenCacheCurrent   int       // cached current context tokens
	tokenCacheWindow    int       // cached context window
	tokenCacheCumInput  int       // cached cumulative input tokens
	tokenCacheCumOutput int       // cached cumulative output tokens

	// Subagent click targets in sidebar — tracks Y positions for mouse clicks
	subagentClickTargets []subagentClickTarget

	// mouseEnabled tracks whether mouse capture is active (default: true).
	// When enabled, scroll-wheel and sidebar clicks work; users hold Shift
	// to bypass capture for native text selection. ctrl+t toggles as fallback.
	mouseEnabled bool

	// forceGotoBottom forces the viewport to scroll to bottom on the next
	// render. Set when switching sessions or creating a new chat.
	forceGotoBottom bool

	// compactFeedback holds the result of /compact to display in the viewport.
	// Cleared when the user sends the next message.
	compactFeedback string

	// goalFeedback holds the result of a /goal command to display in the
	// viewport. Cleared when the user sends the next message.
	goalFeedback string

	// Pending command approval state — set when the agent requests approval
	// for a potentially dangerous exec command. The user must approve (y) or
	// reject (n) before the agent can continue.
	pendingApprovalID     string
	pendingApprovalCmd    string
	pendingApprovalReason string
	approvalResult        string // brief feedback after user decision ("✅ ..." or "❌ ...")
}
