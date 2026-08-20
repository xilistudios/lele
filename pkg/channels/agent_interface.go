package channels

import (
	"context"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

// AgentSessionManager define la interfaz necesaria para gestionar agentes por sesión
// Esta interfaz es implementada por agent.AgentLoop para evitar ciclos de importación
type AgentSessionManager interface {
	GetSessionAgent(sessionKey string) string
	SetSessionAgent(sessionKey, agentID string)
	ListAvailableAgentIDs() []string
	GetDefaultAgentID() string
}

// AgentProvidable extiende la interfaz con métodos para obtener información de agentes
type AgentProvidable interface {
	AgentSessionManager
	// GetAgentInfo devuelve información básica de un agente
	GetAgentInfo(agentID string) (AgentBasicInfo, bool)
	// GetSessionHistory devuelve el historial persistido de una sesión
	GetSessionHistory(sessionKey string) []providers.Message
	// GetHistoryView returns the history slice without copying. The caller
	// MUST NOT modify the returned slice or any message in it. Use this on hot
	// read paths (TUI rendering, token estimation) to avoid a full copy of the
	// message slice on every render — copies become expensive (tens of MB) for
	// long conversations.
	GetHistoryView(sessionKey string) []providers.Message
	// LoadEvictedMessages re-inserts evicted (excluded) messages from SQLite
	// back into memory, restoring full display history. Idempotent; no-op when
	// nothing was evicted. Returns the number of messages loaded.
	LoadEvictedMessages(sessionKey string) int
	// GetEvictedMessageCount returns the number of messages that were evicted
	// from memory (excluded + persisted in SQLite but not in the in-memory slice).
	GetEvictedMessageCount(sessionKey string) int
	// GetTotalMessageCount returns the total persisted message count for a
	// session: in-memory slice length plus evicted messages.
	GetTotalMessageCount(sessionKey string) int
	// HasMessages returns true if a session has any user/assistant messages,
	// WITHOUT materializing its full history. Lightweight metadata check used
	// by the session-listing hot path (WebUI sidebar) to avoid the N+1 full
	// history load performed by GetSessionHistory.
	HasMessages(sessionKey string) bool
	// AddSessionMessage añade un mensaje al historial persistido de una sesión
	AddSessionMessage(sessionKey string, msg providers.Message) error
	// GetSessionModel devuelve el modelo efectivo de una sesión
	GetSessionModel(sessionKey string) string
	// GetSessionMode devuelve el modo de una sesión ("chat", "agent", "group", o "")
	GetSessionMode(sessionKey string) string
	// SetSessionMode establece el modo de una sesión
	SetSessionMode(sessionKey, mode string) error
	// GetSessionModelSupportsImages returns true if the session's current model supports vision
	GetSessionModelSupportsImages(sessionKey string) bool
	// SetSessionModel establece el modelo de una sesión
	SetSessionModel(sessionKey, model string) string
	// ListAvailableModels devuelve los modelos configurados para un agente/sesión
	ListAvailableModels(agentID string) []string
	// GetConfigSnapshot devuelve la configuración actual
	GetConfigSnapshot() *config.Config
	// GetStatus devuelve el estado actual del agente para una sesión
	GetStatus(sessionKey string) string
	// StopAgent detiene el procesamiento del agente para una sesión
	StopAgent(sessionKey string) string
	// CompactSession compacta la sesión para ahorrar tokens
	CompactSession(sessionKey string) string
	// ToggleVerbose cambia el modo verbose para una sesión
	ToggleVerbose(sessionKey string) string
	// GetVerboseLevel devuelve el nivel de verbose actual para una sesión
	GetVerboseLevel(sessionKey string) string
	// SetVerboseLevel establece el nivel de verbose para una sesión
	SetVerboseLevel(sessionKey string, level string) bool
	// GetThinkLevel devuelve el nivel de razonamiento actual para una sesión
	GetThinkLevel(sessionKey string) string
	// SetThinkLevel establece el nivel de razonamiento para una sesión
	SetThinkLevel(sessionKey string, level string) bool
	// GetSubagents list los subagentes activos
	GetSubagents() string
	// GetSessionSubagents returns subagent tasks that belong to a given session
	GetSessionSubagents(sessionKey string) []SubagentTaskInfo
	// ClearSession limpia el historial de una sesión
	ClearSession(sessionKey string) string
	// GetName devuelve el nombre de una sesión
	GetName(sessionKey string) string
	// GetSessionSummary devuelve el resumen de una sesión
	GetSessionSummary(sessionKey string) string
	// GetUpdated devuelve el timestamp de última actualización de una sesión
	GetUpdated(sessionKey string) time.Time
	// GetCreated devuelve el timestamp de creación de una sesión
	GetCreated(sessionKey string) time.Time
	// SetName establece el nombre de una sesión
	SetName(sessionKey string, name string) error
	// ResolveSessionKey resuelve el alias de session_key si existe
	ResolveSessionKey(sessionKey string) string
	// GetSubagentParentSessionKey devuelve la sesión padre de un subagente
	GetSubagentParentSessionKey(sessionKey string) string
	// IsSessionProcessing devuelve true si hay un procesamiento LLM activo para la sesión
	IsSessionProcessing(sessionKey string) bool
	// GetTokenCounts returns the cumulative input/output token counts and context window for a session
	GetTokenCounts(sessionKey string) (inputTokens, outputTokens int, contextWindow int)
	// GetCompactionCount returns the number of context compactions performed on a session
	GetCompactionCount(sessionKey string) int
	// GetCurrentContextUsage returns the actual current context size (history + summary + system prompt)
	// and the context window for a session. Unlike GetTokenCounts which returns cumulative totals,
	// this reflects what would actually be sent to the LLM on the next turn.
	GetCurrentContextUsage(sessionKey string) (currentTokens, contextWindow int)
	// ResolveRoute computes the unified session key for a channel+peer combination
	// using the configured DM scope and identity links.
	ResolveRoute(channel, peerKind, peerID string) string

	// ProcessDirect processes a message directly without going through the message bus.
	ProcessDirect(ctx context.Context, content, sessionKey string) (string, error)
	// ProcessDirectWithChannel processes a message directly with channel information.
	ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error)
	// ProcessHeartbeat processes a heartbeat request without session history.
	ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error)

	// ListAllSessions returns a summary of every persisted session across all
	// agents (including system sessions such as heartbeat and cron). This is
	// used by the session-history UI which needs to surface sessions that are
	// not tracked by a native client.
	ListAllSessions() []SessionKindInfo

	// ========================================================================
	// Streaming support — persists assistant message chunks in the session file
	// ========================================================================

	// AppendAssistantChunk appends a content chunk to the in-progress assistant message.
	AppendAssistantChunk(sessionKey, chunk string)
	// AppendReasoningChunk appends a reasoning chunk to the in-progress assistant message.
	AppendReasoningChunk(sessionKey, chunk string)
	// FinalizeAssistantMessage marks the in-progress assistant message as complete.
	FinalizeAssistantMessage(sessionKey string)
	// HasStreamedContent returns true if the session has an in-progress streaming message with content.
	HasStreamedContent(sessionKey string) bool
	// GetInProgressAssistant returns the in-progress assistant message, if any.
	GetInProgressAssistant(sessionKey string) *providers.Message

	// ========================================================================
	// Background exec management
	// ========================================================================

	// GetBackgroundExecs returns all background processes across all agents
	GetBackgroundExecs(includeCompleted bool) []BackgroundExecInfo
	// GetBackgroundExecOutput returns the output of a background process
	GetBackgroundExecOutput(id string, tail int) (output string, status string, elapsedMs int64, err error)
	// StopBackgroundExec stops a running background process
	StopBackgroundExec(id string) error

	// ========================================================================
	// Group snapshot support
	// ========================================================================

	// AllGroupSnapshots returns a GroupSnapshot for every tracked group.
	AllGroupSnapshots() []group.GroupSnapshot
}

// AgentBasicInfo contiene información pública de un agente
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
}

// SubagentTaskInfo contains information about a subagent task for the API.
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

// BackgroundExecInfo contains information about a background execution.
type BackgroundExecInfo struct {
	ID         string     `json:"id"`
	AgentID    string     `json:"agent_id"`
	Command    string     `json:"command"`
	WorkingDir string     `json:"working_dir"`
	Status     string     `json:"status"`
	StartTime  time.Time  `json:"start_time"`
	EndTime    *time.Time `json:"end_time,omitempty"`
	ExitCode   int        `json:"exit_code"`
	Elapsed    int64      `json:"elapsed_ms"` // milliseconds
}
