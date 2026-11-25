package channels

import (
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

type ClientInfo struct {
	ClientID    string    `json:"client_id"`
	TokenHash   string    `json:"token_hash"`
	RefreshHash string    `json:"refresh_hash"`
	DeviceName  string    `json:"device_name"`
	Created     time.Time `json:"created"`
	Expires     time.Time `json:"expires"`
	LastSeen    time.Time `json:"last_seen"`
	SessionKeys []string  `json:"session_keys,omitempty"`

	PrevRefreshHash string    `json:"prev_refresh_hash,omitempty"`
	PrevTokenHash   string    `json:"prev_token_hash,omitempty"`
	RotatedAt       time.Time `json:"rotated_at,omitempty"`

	lastSeenNanos atomic.Int64 `json:"-"`
}

type SafeClientInfo struct {
	ClientID    string    `json:"client_id"`
	DeviceName  string    `json:"device_name"`
	Created     time.Time `json:"created"`
	Expires     time.Time `json:"expires"`
	LastSeen    time.Time `json:"last_seen"`
	SessionKeys []string  `json:"session_keys,omitempty"`
}

type PendingPIN struct {
	PIN        string    `json:"pin"`
	DeviceName string    `json:"device_name"`
	Created    time.Time `json:"created"`
	Expires    time.Time `json:"expires"`
	Attempts   int       `json:"attempts,omitempty"`
}

type ClientStore struct {
	Clients      map[string]*ClientInfo `json:"clients"`
	PendingPINs  map[string]*PendingPIN `json:"pending_pins"`
	LastModified time.Time              `json:"last_modified"`
}

type WSMessage struct {
	Version int             `json:"v,omitempty"`
	ID      string          `json:"id,omitempty"`
	Event   string          `json:"event"`
	Data    json.RawMessage `json:"data"`
}

type WSMessagePayload struct {
	Content     string   `json:"content,omitempty"`
	Attachments []string `json:"attachments,omitempty"`
	SessionKey  string   `json:"session_key,omitempty"`
	AgentID     string   `json:"agent_id,omitempty"`
}

type WSApprovePayload struct {
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
}

type WSSubscribePayload struct {
	SessionKey string `json:"session_key"`
}

type WSStreamPayload struct {
	MessageID  string `json:"message_id"`
	SessionKey string `json:"session_key,omitempty"`
	Chunk      string `json:"chunk"`
	Done       bool   `json:"done"`

	Tokens    int     `json:"tokens"`
	ElapsedMS int64   `json:"elapsed_ms"`
	TokPerSec float64 `json:"tok_per_sec"`
	Estimated bool    `json:"estimated"`
}

type WSThinkingPayload struct {
	MessageID  string `json:"message_id"`
	SessionKey string `json:"session_key,omitempty"`
	Chunk      string `json:"chunk"`

	Tokens    int     `json:"tokens"`
	ElapsedMS int64   `json:"elapsed_ms"`
	TokPerSec float64 `json:"tok_per_sec"`
	Estimated bool    `json:"estimated"`
}

type WSMessageDiscardPayload struct {
	MessageID  string `json:"message_id"`
	SessionKey string `json:"session_key,omitempty"`
	Reason     string `json:"reason"`
}

type WSMessageCompletePayload struct {
	MessageID   string                   `json:"message_id"`
	SessionKey  string                   `json:"session_key,omitempty"`
	Content     string                   `json:"content"`
	Attachments []map[string]interface{} `json:"attachments,omitempty"`
	Error       bool                     `json:"error,omitempty"`
}

type WSApprovalRequestPayload struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Reason  string `json:"reason,omitempty"`
}

type WSToolExecutingPayload struct {
	SessionKey         string                 `json:"session_key,omitempty"`
	Tool               string                 `json:"tool"`
	Action             string                 `json:"action"`
	Arguments          map[string]interface{} `json:"arguments,omitempty"`
	SubagentSessionKey string                 `json:"subagent_session_key,omitempty"`
	ToolCallID         string                 `json:"tool_call_id,omitempty"`
}

type WSToolResultPayload struct {
	SessionKey         string `json:"session_key,omitempty"`
	Tool               string `json:"tool"`
	Result             string `json:"result"`
	SubagentSessionKey string `json:"subagent_session_key,omitempty"`
	ToolCallID         string `json:"tool_call_id,omitempty"`
}

type WSGroupStatusPayload struct {
	SessionKey   string `json:"session_key,omitempty"`
	GroupID      string `json:"group_id"`
	Status       string `json:"status"`
	Participants string `json:"participants"`
}

type WSGroupTurnPayload struct {
	SessionKey string `json:"session_key,omitempty"`
	GroupID    string `json:"group_id"`
	Speaker    string `json:"speaker"`
	Label      string `json:"label"`
	Role       string `json:"role"`
	Layer      int    `json:"layer"`
	TurnIndex  int    `json:"turn_index"`
	Content    string `json:"content"`
}

type WSGroupCompletePayload struct {
	SessionKey  string `json:"session_key,omitempty"`
	GroupID     string `json:"group_id"`
	Strategy    string `json:"strategy"`
	Layers      int    `json:"layers"`
	TotalTokens int    `json:"total_tokens"`
	Content     string `json:"content"`
}

type WSGroupToolPayload struct {
	SessionKey string `json:"session_key"`
	GroupID    string `json:"group_id"`
	Speaker    string `json:"speaker"`
	Label      string `json:"label"`
	Layer      int    `json:"layer"`
	TurnIndex  int    `json:"turn_index"`
	ToolCallID string `json:"tool_call_id"`
	Tool       string `json:"tool"`
	Status     string `json:"status"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
}

type WSCommandAppliedPayload struct {
	SessionKey  string `json:"session_key"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	Args        string `json:"args,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Model       string `json:"model,omitempty"`
	Source      string `json:"source,omitempty"`
}

type WSErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type WSCancelPayload struct {
	SessionKey string `json:"session_key,omitempty"`
	Scope      string `json:"scope,omitempty"`
}

const (
	CancelScopeSession = "session"
	CancelScopeTurn    = "turn"
)

type WSStatusPayload struct {
	Agents   []map[string]interface{} `json:"agents"`
	Channels []map[string]interface{} `json:"channels"`
}

type AuthPINResponse struct {
	PIN     string `json:"pin"`
	Expires string `json:"expires"`
}

type AuthPairRequest struct {
	PIN        string `json:"pin"`
	DeviceName string `json:"device_name"`
}

type AuthPairResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	Expires      string `json:"expires"`
	ClientID     string `json:"client_id"`
}

type AuthRefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type AuthRefreshResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	Expires      string `json:"expires"`
}

type AuthStatusResponse struct {
	Valid      bool   `json:"valid"`
	ClientID   string `json:"client_id"`
	DeviceName string `json:"device_name"`
	Expires    string `json:"expires"`
}

type ChatSendRequest struct {
	Content     string   `json:"content"`
	Attachments []string `json:"attachments,omitempty"`
	SessionKey  string   `json:"session_key,omitempty"`
	AgentID     string   `json:"agent_id,omitempty"`
}

type ChatSendResponse struct {
	MessageID  string `json:"message_id"`
	SessionKey string `json:"session_key"`
}

type ChatHistoryResponse struct {
	SessionKey string                `json:"session_key"`
	Messages   []ChatHistoryMessage  `json:"messages"`
	Processing bool                  `json:"processing"`
	HasMore    bool                  `json:"has_more"`
	Groups     []group.GroupSnapshot `json:"groups,omitempty"`
}

type ChatHistoryMessage struct {
	ID                 string                        `json:"id"`
	Role               string                        `json:"role"`
	Content            string                        `json:"content"`
	ReasoningContent   string                        `json:"reasoning_content,omitempty"`
	ToolCalls          []HistoryToolCall             `json:"tool_calls,omitempty"`
	ToolCallID         string                        `json:"tool_call_id,omitempty"`
	ToolName           string                        `json:"tool_name,omitempty"`
	ExcludeFromContext bool                          `json:"exclude_from_context,omitempty"`
	Attachments        []providers.MessageAttachment `json:"attachments,omitempty"`
	DisplayContent     string                        `json:"display_content,omitempty"`
	Command            *providers.CommandApplied     `json:"harness_command,omitempty"`
	ErrorDetail        string                        `json:"error_detail,omitempty"`
}

type HistoryToolCall struct {
	ID               string                 `json:"id"`
	Type             string                 `json:"type,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Arguments        map[string]interface{} `json:"arguments,omitempty"`
	ThoughtSignature string                 `json:"thought_signature,omitempty"`
}

type ChatSession struct {
	Key     string    `json:"key"`
	Name    string    `json:"name,omitempty"`
	Mode    string    `json:"mode,omitempty"`
	Kind    string    `json:"kind,omitempty"`
	Folder  string    `json:"folder,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

type SessionKindInfo struct {
	Key         string
	Name        string
	Mode        string
	Kind        string
	Folder      string
	Created     time.Time
	Updated     time.Time
	Resident    bool
	HasMessages bool
}

type ChatSessionsResponse struct {
	Sessions []ChatSession `json:"sessions"`
	Total    int           `json:"total"`
	HasMore  bool          `json:"has_more"`
}

type CreateSessionRequest struct {
	SessionKey string `json:"session_key"`
	Mode       string `json:"mode,omitempty"`
}

type CreateSessionResponse struct {
	SessionKey string `json:"session_key"`
}

type SessionModelResponse struct {
	SessionKey  string       `json:"session_key"`
	AgentID     string       `json:"agent_id,omitempty"`
	Model       string       `json:"model"`
	Models      []string     `json:"models,omitempty"`
	ModelGroups []ModelGroup `json:"model_groups,omitempty"`
}

type SessionModelUpdateRequest struct {
	Model string `json:"model"`
}

type SessionThinkingResponse struct {
	SessionKey string `json:"session_key"`
	Level      string `json:"level"`
}

type SessionThinkingUpdateRequest struct {
	Level string `json:"level"`
}

type SessionAgentResponse struct {
	SessionKey string `json:"session_key"`
	AgentID    string `json:"agent_id"`
}

type SessionAgentUpdateRequest struct {
	AgentID string `json:"agent_id"`
}

type SessionNameUpdateRequest struct {
	Name string `json:"name"`
}

type SessionNameResponse struct {
	SessionKey string `json:"session_key"`
	Name       string `json:"name"`
}

type SessionSummaryResponse struct {
	SessionKey string `json:"session_key"`
	Summary    string `json:"summary"`
}

type SessionContextResponse struct {
	SessionKey             string  `json:"session_key"`
	InputTokens            int     `json:"input_tokens"`
	OutputTokens           int     `json:"output_tokens"`
	TotalTokens            int     `json:"total_tokens"`
	CumulativeInputTokens  int     `json:"cumulative_input_tokens"`
	CumulativeOutputTokens int     `json:"cumulative_output_tokens"`
	CumulativeTotalTokens  int     `json:"cumulative_total_tokens"`
	ContextWindow          int     `json:"context_window"`
	UsagePercent           float64 `json:"usage_percent"`
	CompactionCount        int     `json:"compaction_count"`
}

type SubagentTaskEntry struct {
	TaskID     string `json:"task_id"`
	SessionKey string `json:"session_key"`
	Label      string `json:"label"`
	AgentID    string `json:"agent_id"`
	Status     string `json:"status"`
	Summary    string `json:"summary"`
	Created    int64  `json:"created"`
	Updated    int64  `json:"updated"`
	Iterations int    `json:"iterations"`
}

type SessionSubagentsResponse struct {
	SessionKey string              `json:"session_key"`
	Subagents  []SubagentTaskEntry `json:"subagents"`
}

type NativeAgentInfo struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	Workspace   string                  `json:"workspace"`
	Model       string                  `json:"model"`
	Default     bool                    `json:"default"`
	Reasoning   *config.ReasoningConfig `json:"reasoning,omitempty"`
}

type AgentsResponse struct {
	Agents []NativeAgentInfo `json:"agents"`
}

type AgentStatusResponse struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	ActiveSessions int    `json:"active_sessions"`
}

type AgentCatalogTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type AgentCatalogSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Enabled     bool   `json:"enabled"`
	Deletable   bool   `json:"deletable"`
}

type AgentCatalogResponse struct {
	AgentID   string              `json:"agent_id"`
	Tools     []AgentCatalogTool  `json:"tools"`
	Skills    []AgentCatalogSkill `json:"skills"`
	Workspace string              `json:"workspace"`
}

type ConfigResponse struct {
	Config   interface{}    `json:"config"`
	Metadata ConfigMetadata `json:"meta"`
}

type ConfigMetadata struct {
	ConfigPath              string            `json:"config_path"`
	Source                  string            `json:"source"`
	CanSave                 bool              `json:"can_save"`
	RestartRequiredSections []string          `json:"restart_required_sections"`
	SecretsByPath           map[string]string `json:"secrets_by_path"`
}

type ConfigUpdateRequest struct {
	Config interface{} `json:"config"`
}

type ConfigUpdateResponse struct {
	Config   interface{}    `json:"config"`
	Metadata ConfigMetadata `json:"meta"`
	Errors   []ConfigError  `json:"errors,omitempty"`
}

type ConfigValidateRequest struct {
	Config interface{} `json:"config"`
}

type ConfigValidateResponse struct {
	Valid  bool          `json:"valid"`
	Errors []ConfigError `json:"errors,omitempty"`
}

type ConfigError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

type ToolsResponse struct {
	Tools []ToolInfo `json:"tools"`
}

type ModelsResponse struct {
	AgentID     string       `json:"agent_id,omitempty"`
	Model       string       `json:"model,omitempty"`
	Models      []string     `json:"models"`
	ModelGroups []ModelGroup `json:"model_groups,omitempty"`
}

type ModelGroup struct {
	Provider string        `json:"provider"`
	Models   []ModelOption `json:"models"`
}

type ModelOption struct {
	Value     string                  `json:"value"`
	Label     string                  `json:"label"`
	Reasoning *config.ReasoningConfig `json:"reasoning,omitempty"`
}

type ProviderModelsResponse struct {
	Provider string              `json:"provider"`
	Source   string              `json:"source,omitempty"`
	Models   []ProviderModelInfo `json:"models"`
}

type ProviderModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`

	Name           string   `json:"name,omitempty"`
	ContextWindow  int      `json:"context_window,omitempty"`
	MaxOutput      int      `json:"max_output,omitempty"`
	Vision         bool     `json:"vision,omitempty"`
	ThinkingLevels []string `json:"thinking_levels,omitempty"`
	Reasoning      bool     `json:"reasoning,omitempty"`
	ToolCall       bool     `json:"tool_call,omitempty"`
	// Protocol is the catalog's transport marker. Empty means chat/completions,
	// or that the id is unknown to the catalog (a live-only id).
	Protocol string `json:"protocol,omitempty"`
}

type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type SkillsResponse struct {
	Skills []SkillInfo `json:"skills"`
}

type SkillInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Installed   bool   `json:"installed"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source,omitempty"`
}

type SkillInstallRequest struct {
	URL   string `json:"url"`
	Scope string `json:"scope,omitempty"`
}

type SkillInstallResponse struct {
	SkillID string `json:"skill_id"`
	Message string `json:"message"`
}

type ScanSkillsResponse struct {
	Skills []ScannedSkill `json:"skills"`
	Repo   string         `json:"repo"`
}

type ScannedSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	HasSKILL    bool   `json:"has_skill"`
}

type SkillScanRequest struct {
	Repo string `json:"repo"`
}

type SkillInstallBatchRequest struct {
	Repo   string   `json:"repo"`
	Skills []string `json:"skills"`
	Scope  string   `json:"scope,omitempty"`
}

type SkillToggleRequest struct {
	Enabled bool `json:"enabled"`
}

type SystemStatusResponse struct {
	Status   string                   `json:"status"`
	Uptime   string                   `json:"uptime"`
	Agents   []map[string]interface{} `json:"agents"`
	Channels []map[string]interface{} `json:"channels"`
	Version  string                   `json:"version"`
	Storage  *StorageStatus           `json:"storage,omitempty"`
}

type StorageStatus struct {
	Backend  string `json:"backend"`
	Degraded bool   `json:"degraded"`
	Error    string `json:"error,omitempty"`
}

type ChannelsResponse struct {
	Channels []ChannelInfo `json:"channels"`
}

type ChannelInfo struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Error   string `json:"error"`
	Details any    `json:"details,omitempty"`
}

type PaginationParams struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

type PaginatedResponse struct {
	Items   any  `json:"items"`
	Total   int  `json:"total"`
	Offset  int  `json:"offset"`
	Limit   int  `json:"limit"`
	HasMore bool `json:"has_more"`
}

const WSProtocolVersion = 1

type FileUploadResponse struct {
	Files []UploadedFile `json:"files"`
}

type UploadedFile struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

type ApproveRequest struct {
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
}

type ApproveResponse struct {
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
	Message   string `json:"message,omitempty"`
}

type FetchModelsRequest struct {
	APIBase string `json:"api_base"`
	APIKey  string `json:"api_key,omitempty"`
}

type FetchModelsResponse struct {
	Models []string `json:"models"`
}
