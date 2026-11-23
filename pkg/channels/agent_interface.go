package channels

import (
	"context"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

type AgentSessionManager interface {
	GetSessionAgent(sessionKey string) string
	SetSessionAgent(sessionKey, agentID string)
	HintSessionAgent(sessionKey, agentID string)
	ListAvailableAgentIDs() []string
	GetDefaultAgentID() string
}

type AgentProvidable interface {
	AgentSessionManager
	GetAgentInfo(agentID string) (AgentBasicInfo, bool)
	ListAgentTools(agentID string) ([]AgentToolInfo, bool)
	GetSessionHistory(sessionKey string) []providers.Message
	GetHistoryView(sessionKey string) []providers.Message
	GetEvictedMessageCount(sessionKey string) int
	LoadEvictedMessagesPage(sessionKey string, before, after, limit int) *session.EvictedMessagesPage
	GetTotalMessageCount(sessionKey string) int
	HasMessages(sessionKey string) bool
	AddSessionMessage(sessionKey string, msg providers.Message) error
	GetSessionModel(sessionKey string) string
	GetSessionMode(sessionKey string) string
	SetSessionMode(sessionKey, mode string) error
	GetSessionModelSupportsImages(sessionKey string) bool
	GetSessionModelSupportsVideo(sessionKey string) bool
	SetSessionModel(sessionKey, model string) string
	SetSessionFolder(sessionKey, folder string) string
	GetSessionFolder(sessionKey string) string
	ListAvailableModels(agentID string) []string
	GetConfigSnapshot() *config.Config
	GetStatus(sessionKey string) string
	StopAgent(sessionKey string) string
	InterruptAgent(sessionKey string) string
	CompactSession(sessionKey string) string
	ToggleVerbose(sessionKey string) string
	GetVerboseLevel(sessionKey string) string
	SetVerboseLevel(sessionKey string, level string) bool
	GetThinkLevel(sessionKey string) string
	GetEffectiveThinkLevel(sessionKey string) string
	SetThinkLevel(sessionKey string, level string) bool
	GetSubagents() string
	GetSessionSubagents(sessionKey string) []SubagentTaskInfo
	ClearSession(sessionKey string) string
	GetName(sessionKey string) string
	GetSessionSummary(sessionKey string) string
	GetUpdated(sessionKey string) time.Time
	GetCreated(sessionKey string) time.Time
	SetName(sessionKey string, name string) error
	ResolveSessionKey(sessionKey string) string
	GetSubagentParentSessionKey(sessionKey string) string
	IsSessionProcessing(sessionKey string) bool
	GetTokenCounts(sessionKey string) (inputTokens, outputTokens int, contextWindow int)
	GetCompactionCount(sessionKey string) int
	GetCurrentContextUsage(sessionKey string) (currentTokens, contextWindow int)
	ResolveRoute(channel, peerKind, peerID string) string

	ProcessDirect(ctx context.Context, content, sessionKey string) (string, error)
	ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error)
	ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error)

	ListAllSessions() []SessionKindInfo

	AppendAssistantChunk(sessionKey, chunk string)
	AppendReasoningChunk(sessionKey, chunk string)
	FinalizeAssistantMessage(sessionKey string)
	AttachFilesToLastAssistant(sessionKey string, attachments []providers.MessageAttachment)
	HasStreamedContent(sessionKey string) bool
	GetInProgressAssistant(sessionKey string) *providers.Message
	GetInProgressTool(sessionKey string) *session.InProgressTool

	GetBackgroundExecs(includeCompleted bool) []BackgroundExecInfo
	GetBackgroundExecOutput(id string, tail int) (output string, status string, elapsedMs int64, err error)
	StopBackgroundExec(id string) error

	AllGroupSnapshots() []group.GroupSnapshot
	GroupSnapshotsForSession(sessionKey string) []group.GroupSnapshot
}

type AgentBasicInfo struct {
	ID             string
	Name           string
	Description    string
	Model          string
	Workspace      string
	MaxIterations  int
	MaxTokens      int
	Temperature    float64
	Fallbacks      []string
	SkillsFilter   []string
	Reasoning      *config.ReasoningConfig
	SupportsImages bool
	SupportsVideo  bool
}

type AgentToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type SubagentTaskInfo struct {
	TaskID     string
	SessionKey string
	Label      string
	AgentID    string
	Status     string
	Summary    string
	Created    int64
	Updated    int64
	Iterations int
}

type BackgroundExecInfo struct {
	ID         string     `json:"id"`
	AgentID    string     `json:"agent_id"`
	Command    string     `json:"command"`
	WorkingDir string     `json:"working_dir"`
	Status     string     `json:"status"`
	StartTime  time.Time  `json:"start_time"`
	EndTime    *time.Time `json:"end_time,omitempty"`
	ExitCode   int        `json:"exit_code"`
	Elapsed    int64      `json:"elapsed_ms"`
}
