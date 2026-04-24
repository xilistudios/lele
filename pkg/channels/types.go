package channels

import (
	"encoding/json"
	"time"

	"github.com/xilistudios/lele/pkg/config"
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
}

type PendingPIN struct {
	PIN        string    `json:"pin"`
	DeviceName string    `json:"device_name"`
	Created    time.Time `json:"created"`
	Expires    time.Time `json:"expires"`
}

type ClientStore struct {
	Clients      map[string]*ClientInfo `json:"clients"`
	PendingPINs  map[string]*PendingPIN `json:"pending_pins"`
	LastModified time.Time              `json:"last_modified"`
}

type WSMessage struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
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
}

type WSMessageCompletePayload struct {
	MessageID   string                   `json:"message_id"`
	SessionKey  string                   `json:"session_key,omitempty"`
	Content     string                   `json:"content"`
	Attachments []map[string]interface{} `json:"attachments,omitempty"`
}

type WSApprovalRequestPayload struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Reason  string `json:"reason,omitempty"`
}

type WSToolExecutingPayload struct {
	SessionKey         string `json:"session_key,omitempty"`
	Tool               string `json:"tool"`
	Action             string `json:"action"`
	SubagentSessionKey string `json:"subagent_session_key,omitempty"`
}

type WSToolResultPayload struct {
	SessionKey         string `json:"session_key,omitempty"`
	Tool               string `json:"tool"`
	Result             string `json:"result"`
	SubagentSessionKey string `json:"subagent_session_key,omitempty"`
}

type WSErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

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
	SessionKey string               `json:"session_key"`
	Messages   []ChatHistoryMessage `json:"messages"`
	Processing bool                 `json:"processing"`
}

type ChatHistoryMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	ToolCalls  []HistoryToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

type HistoryToolCall struct {
	ID               string                 `json:"id"`
	Type             string                 `json:"type,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Arguments        map[string]interface{} `json:"arguments,omitempty"`
	ThoughtSignature string                 `json:"thought_signature,omitempty"`
}

type ChatSession struct {
	Key          string    `json:"key"`
	Name         string    `json:"name,omitempty"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
	MessageCount int       `json:"message_count"`
}

type ChatSessionsResponse struct {
	Sessions []ChatSession `json:"sessions"`
}

type CreateSessionRequest struct {
	SessionKey string `json:"session_key"`
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
}

type NativeAgentInfo struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	Workspace string                  `json:"workspace"`
	Model     string                  `json:"model"`
	Default   bool                    `json:"default"`
	Reasoning *config.ReasoningConfig `json:"reasoning,omitempty"`
}

type AgentsResponse struct {
	Agents []NativeAgentInfo `json:"agents"`
}

type AgentStatusResponse struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	ActiveSessions int    `json:"active_sessions"`
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
	Value      string                  `json:"value"`
	Label      string                  `json:"label"`
	Reasoning  *config.ReasoningConfig `json:"reasoning,omitempty"`
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
}

type SkillInstallRequest struct {
	URL string `json:"url"`
}

type SkillInstallResponse struct {
	SkillID string `json:"skill_id"`
	Message string `json:"message"`
}

type SystemStatusResponse struct {
	Status   string                   `json:"status"`
	Uptime   string                   `json:"uptime"`
	Agents   []map[string]interface{} `json:"agents"`
	Channels []map[string]interface{} `json:"channels"`
	Version  string                   `json:"version"`
}

type ChannelsResponse struct {
	Channels []ChannelInfo `json:"channels"`
}

type ChannelInfo struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
}

type ErrorResponse struct {
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

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
