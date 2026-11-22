package tui

import (
	"context"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/keepawake"
	"github.com/xilistudios/lele/pkg/locales"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tui/theme"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/glamour"
)

type paneType int

const (
	ChatsPane paneType = iota
	ChatViewPane
)

type chatMode int

const (
	ModeAgent chatMode = iota
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

const leftColumnRatio = 0.72
const chatSidebarGutter = 2

type groupTurn struct {
	index   int
	layer   int
	speaker string
	label   string
	role    string
	content string
}

type groupMeta struct {
	strategy     string
	layers       int
	totalTokens  int
	participants string
	synthesis    string
}

type subagentClickTarget struct {
	yStart int
	yEnd   int
	key    string
}

type subagentsCacheState struct {
	key     string
	list    []channels.SubagentTaskInfo
	running int
	at      time.Time
}

type sessionsCacheState struct {
	all    []*session.Session
	at     time.Time
	loaded bool
}

type historyCountMemo struct {
	valid bool
	head  *providers.Message
	n     int
	count int
}

type modalType int

const (
	ModalNone modalType = iota
	ModalSessions
	ModalAgent
	ModalModel
	ModalThink
	ModalLang
	ModalLangRemote
	ModalSubagents
	ModalBackgroundExecs
	ModalProviders
	ModalProviderDetail
	ModalAddProvider
	ModalAddModel
	ModalEditModel
	ModalEditProvider
	ModalCron
	ModalSecrets
	ModalAddSecret
	ModalSkills
	ModalSkillInstall
	ModalSkillPicker
	ModalCommands
	ModalCommandDetail
	ModalAddCommand
	ModalSettings
	ModalSettingsAgents
	ModalSettingsAgentEdit
	ModalSettingsSystem
	ModalSettingsSystemEdit
	ModalSettingsTUI
	ModalMCP
	ModalMCPDetail
	ModalMCPAuth
	ModalMCPAdd
	ModalConnectOAuth
)

type formStep int

const (
	formStepInput formStep = iota
	formStepConfirm
)

const escHintTimeout = 1 * time.Second

type commandInfo struct {
	name        string
	description string
}

var allCommands = []commandInfo{
	{name: "/sessions", description: "Switch session"},
	{name: "/new", description: "New session"},
	{name: "/agents", description: "Switch agent (TAB cycles)"},
	{name: "/mode", description: "Switch mode (agent/chat/group)"},
	{name: "/models", description: "Switch model"},
	{name: "/add-model", description: "Add a model to a provider"},
	{name: "/clear", description: "Clear session history"},
	{name: "/clearq", description: "Drop queued messages for this session"},
	{name: "/flushq", description: "Send the next queued message now (cancels a busy turn after confirm)"},
	{name: "/think", description: "Toggle thinking level (off/low/medium/high)"},
	{name: "/lang", description: "Change language or download packs"},
	{name: "/subagents", description: "Switch to subagent"},
	{name: "/bg", description: "View background processes"},
	{name: "/cron", description: "Manage scheduled cron jobs"},
	{name: "/providers", description: "Manage providers"},
	{name: "/connect", description: "Connect a new provider"},
	{name: "/secrets", description: "Manage secrets (keyring)"},
	{name: "/skills", description: "Manage agent skills"},
	{name: "/mcp", description: "Manage MCP servers"},
	{name: "/commands", description: "Manage custom slash commands"},
	{name: "/settings", description: "Open settings"},
	{name: "/compact", description: "Compact conversation history"},
	{name: "/status", description: "Show context usage and compactions"},
	{name: "/goal", description: "Set a persistent goal (status/pause/resume/clear)"},
	{name: "/quit", description: "Exit TUI"},
}

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

type communityIndexMsg struct {
	entries []theme.CommunityThemeEntry
	err     string
}

type installThemeMsg struct {
	name  string
	theme theme.Theme
	err   string
}

type onboardStep int

const (
	obWelcome onboardStep = iota
	obLanguage
	obTheme
	obProviderPicker
	obConnect
	obVerify
	obDone
)

type Model struct {
	agentLoop  *agent.AgentLoop
	sessionMgr *session.SessionManager
	cfg        *config.Config
	ctx        context.Context
	cancel     context.CancelFunc

	activePane         paneType
	selectedSessionIdx int
	visibleSessions    []*session.Session
	currentKey         string
	currentMode        chatMode
	groupProfileIdx    int
	showWelcome        bool
	onboardingActive   bool
	onboardingStep     onboardStep
	obSelectedPreset   int
	obSkipConfirm      bool
	obVerifying        bool
	obVerifyFailed     bool
	obProviderName     string
	obModelName        string
	obMaskedKey        string

	currentThemeName  string
	themePickerActive bool
	themePreviewName  string

	customThemes       map[string]theme.Theme
	installedCommunity []string
	communityIndex     []theme.CommunityThemeEntry
	communityLoading   bool
	communityErr       string
	themePickerItems   []themePickerItem

	showAutocomplete  bool
	autocompleteItems []commandInfo
	autocompleteIdx   int

	customCmds   []commandInfo
	customCmdsAt time.Time

	cmdUsage *commandUsageStore

	modalMode         modalType
	modalItems        []string
	modalSessionKeys  []string
	modalSubagentKeys []string
	modalLangCodes    []string
	modalSelectedIdx  int
	modalScrollOffset int

	localesMgr      *locales.Manager
	langInstallBusy bool
	langInstallMsg  string

	bgExecViewMode   bool
	bgExecViewID     string
	bgExecViewOutput string
	bgExecViewStatus string
	bgExecModalKeys  []string

	cronService     *cron.CronService
	cronModalKeys   []string
	cronDetailMode  bool
	cronDetailJobID string

	secretsModalKeys  []string
	secretsDetailMode bool
	secretsDetailName string
	secretsReveal     bool

	skillsModalKeys   []string
	skillsScanResults []channels.ScannedSkill
	skillsScanRepo    string
	skillsSelectedMap map[int]bool
	skillsFeedback    string

	mcpModalKeys      []string
	mcpFilter         int
	mcpFeedback       string
	mcpInventory      mcp.Inventory
	mcpInventoryValid bool
	mcpDetailName     string
	mcpAuth           map[string]mcp.AuthStatus
	mcpAuthFlow       mcpAuthFlow
	mcpAuthState      int
	mcpAuthURL        string
	mcpAuthName       string
	mcpAuthFeedback   string
	mcpAuthError      string
	mcpAuthSeq        int
	mcpAuthOpener     func(string) error

	commandsModalKeys   []string
	commandsRows        []commandRow
	commandsDetailMode  bool
	commandsDetailKey   string
	commandsFeedback    string
	commandsEditKey     string
	commandsEditScope   string
	commandsDeleteKey   string
	commandsDeleteArmed time.Time
	templateInput       textarea.Model

	settingsSection   string
	settingsEditField string
	settingsAgentID   string
	settingsAgentKeys []string

	settingsSelectorActive bool
	settingsSelectorItems  []string
	settingsSelectorValues []string
	settingsSelectorIdx    int
	settingsSelectorField  string
	settingsSelectorOrig   string

	subagentPickerActive   bool
	subagentPickerItems    []string
	subagentPickerLabels   []string
	subagentPickerSelected map[int]bool
	subagentPickerIdx      int

	providerModalKeys    []string
	providerSelectedName string
	providerEditMode     bool
	providerSavedInFlow  bool

	connectOAuthFlow     connectOAuthFlow
	connectOAuthState    int
	connectOAuthURL      string
	connectOAuthName     string
	connectOAuthFeedback string
	connectOAuthError    string
	connectOAuthSeq      int

	modelEditAlias        string
	modelEditOrigModel    string
	providerDetailRows    map[int]string
	providerDetailEditIdx int
	providerDetailAddIdx  int
	providerDetailDelIdx  int
	providerDeleteKey     string
	providerDeleteArmed   time.Time
	providerFeedback      string

	formStepIndex   int
	formValues      []string
	formError       string
	formConfirmMode bool

	addModelCatalogActive bool
	addModelCatalogIDs    []string
	addModelCatalogLabels []string
	addModelCatalogIdx    int
	addModelCatalogThink  string

	providerTypePicker    bool
	providerTypePickerIdx int
	providerTypePickerMax int

	connectRowPicker     bool
	connectRowIdx        int
	connectSkipAPIBase   bool
	connectAlreadyUsable bool

	connectSuccess bool

	providerTypeFromPreset bool

	viewport  lineViewport
	textInput textinput.Model
	chatInput textarea.Model

	pendingUserMessage string

	parentSessionKey string

	subagentProgress map[string]string

	groupTranscripts map[string][]groupTurn
	groupStatus      map[string]string
	activeGroupID    string
	groupMeta        map[string]groupMeta

	processing                  bool
	messageQueue                map[string][]queuedMessage
	queueFeedback               string
	currentMessageID            string
	currentAssistantMsgID       string
	subagentContinuationPending bool
	currentStream               string
	currentThinking             string
	currentToolAction           string
	startTime                   time.Time
	elapsedTime                 time.Duration
	lastDuration                time.Duration
	streamTokens                int
	streamElapsedMs             int64
	streamTokPerSec             float64
	streamMetricsEstimated      bool
	lastTokPerSec               float64
	animationTick               int
	tickPending                 bool

	escPressCount int
	escLastPress  time.Time
	escHint       bool

	flushLastPress time.Time

	selecting           bool
	selStartX           int
	selStartY           int
	selEndX             int
	selEndY             int
	selectionFeedback   bool
	selectionFeedbackAt time.Time

	gitBranch     string
	workspacePath string

	pendingModel string
	pendingAgent string
	pendingThink string

	lastEscTime      time.Time
	sessionStartTime time.Time

	escSeqActive   bool
	escSeqLastRune time.Time
	escBuffer      []rune

	width  int
	height int

	streamThrottleActive     bool
	streamPendingUpdate      bool
	streamThrottleInterval   time.Duration
	streamRenderedLines      []string
	thinkingRenderedLines    []string
	streamRenderCacheWidth   int
	thinkingRenderCacheWidth int
	streamRenderedJoined     string
	thinkingRenderedJoined   string

	cachedRenderer      *glamour.TermRenderer
	cachedRendererWidth int
	cachedRendererStyle string

	themeIsLight bool

	msgRenderCacheLines map[string][]string
	msgRenderCacheWidth int

	renderedBaseValid         bool
	renderedBaseKey           string
	renderedBaseMsgCount      int
	renderedBaseLastStreaming bool
	renderedBaseHistoryLen    int
	renderedBaseArchiveKey    string

	lastViewportKey string

	viewportBuiltWidth  int
	viewportBuiltHeight int

	viewportBuiltAt time.Time

	onViewportRebuild func(inFrame bool)

	renderedMsgStartIdx int
	renderedMsgEndIdx   int
	maxRenderedMessages int
	renderStartIdx      int

	renderWindowSessionKey string

	archivedPrefix       []providers.Message
	archivedKey          string
	archivedTotal        int
	archivedOldestSeq    int
	archivedHasOlder     bool
	archivedVisibleCount int

	tokenCacheKey       string
	tokenCacheTime      time.Time
	tokenCacheCurrent   int
	tokenCacheWindow    int
	tokenCacheCumInput  int
	tokenCacheCumOutput int

	frameOpen          bool
	frameValid         bool
	frameKey           string
	frameView          []providers.Message
	frameCount         int
	frameHistoryLen    int
	frameLastStreaming bool

	countMemo historyCountMemo

	onHistoryRead func(sessionKey string)

	onHistoryCountScan func()

	onSubagentsRefresh func()

	subagentsCache subagentsCacheState

	onSessionsRefresh func()

	sessionsCache sessionsCacheState

	subagentClickTargets []subagentClickTarget

	backToParentClick    subagentClickTarget
	hasBackToParentClick bool

	mouseEnabled     bool
	keepAwakeEnabled bool
	keepAwake        *keepawake.Inhibitor
	keepAwakeErr     string

	forceGotoBottom bool

	frameLayout          frameLayout
	lastPaintedLayout    frameLayout
	frameLayoutRepainted bool

	compactFeedback string

	statusFeedback string

	pendingApprovalID     string
	pendingApprovalCmd    string
	pendingApprovalReason string
	approvalShowFull      bool
	approvalResult        string

	pendingApprovals map[string]pendingApprovalSnapshot
}

type pendingApprovalSnapshot struct {
	id     string
	cmd    string
	reason string
}
