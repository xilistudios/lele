package channels

import (
	"encoding/json"
	"time"
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
	MessageID string `json:"message_id"`
	Chunk     string `json:"chunk"`
	Done      bool   `json:"done"`
}

type WSMessageCompletePayload struct {
	MessageID   string                   `json:"message_id"`
	Content     string                   `json:"content"`
	Attachments []map[string]interface{} `json:"attachments,omitempty"`
}

type WSApprovalRequestPayload struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Reason  string `json:"reason,omitempty"`
}

type WSToolExecutingPayload struct {
	Tool   string `json:"tool"`
	Action string `json:"action"`
}

type WSToolResultPayload struct {
	Tool   string `json:"tool"`
	Result string `json:"result"`
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
	SessionKey string                   `json:"session_key"`
	Messages   []map[string]interface{} `json:"messages"`
}

type ChatSession struct {
	Key          string    `json:"key"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
	MessageCount int       `json:"message_count"`
}

type ChatSessionsResponse struct {
	Sessions []ChatSession `json:"sessions"`
}

type NativeAgentInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Workspace string `json:"workspace"`
	Model     string `json:"model"`
	Default   bool   `json:"default"`
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
	Config map[string]interface{} `json:"config"`
}

type ConfigUpdateRequest struct {
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

type ToolsResponse struct {
	Tools []ToolInfo `json:"tools"`
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
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}
