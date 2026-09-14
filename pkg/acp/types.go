// Package acp implements the Agent Communication Protocol (ACP) 0.2.0
// REST API so lele agents are discoverable and runnable by ACP clients.
//
// Spec: https://agentcommunicationprotocol.dev
package acp

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// ProtocolVersion is the ACP revision this package implements.
const ProtocolVersion = "0.2.0"

// Run status values from the ACP spec.
const (
	RunStatusCreated    = "created"
	RunStatusInProgress = "in-progress"
	RunStatusAwaiting   = "awaiting"
	RunStatusCancelling = "cancelling"
	RunStatusCancelled  = "cancelled"
	RunStatusCompleted  = "completed"
	RunStatusFailed     = "failed"
)

// Run modes from the ACP spec.
const (
	ModeSync   = "sync"
	ModeAsync  = "async"
	ModeStream = "stream"
)

// Event type constants from the ACP spec.
const (
	EventMessageCreated   = "message.created"
	EventMessagePart      = "message.part"
	EventMessageCompleted = "message.completed"
	EventGeneric          = "generic"
	EventRunCreated       = "run.created"
	EventRunInProgress    = "run.in-progress"
	EventRunAwaiting      = "run.awaiting"
	EventRunCompleted     = "run.completed"
	EventRunCancelled     = "run.cancelled"
	EventRunFailed        = "run.failed"
	EventError            = "error"
)

// Error codes from the ACP spec.
const (
	ErrCodeServerError  = "server_error"
	ErrCodeInvalidInput = "invalid_input"
	ErrCodeNotFound     = "not_found"
)

var agentNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Error is the ACP error payload.
type Error struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// MessagePart is one content fragment of a Message.
type MessagePart struct {
	Name            string          `json:"name,omitempty"`
	ContentType     string          `json:"content_type"`
	Content         string          `json:"content,omitempty"`
	ContentEncoding string          `json:"content_encoding,omitempty"`
	ContentURL      string          `json:"content_url,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
}

// Message is a multi-part agent/user message.
type Message struct {
	Role        string        `json:"role"`
	Parts       []MessagePart `json:"parts"`
	CreatedAt   *time.Time    `json:"created_at,omitempty"`
	CompletedAt *time.Time    `json:"completed_at,omitempty"`
}

// Text concatenates text/* parts.
func (m *Message) Text() string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range m.Parts {
		if strings.HasPrefix(p.ContentType, "text/") || p.ContentType == "" {
			b.WriteString(p.Content)
		}
	}
	return b.String()
}

// Session is an ACP session reference.
type Session struct {
	ID      string   `json:"id"`
	History []string `json:"history"`
	State   string   `json:"state,omitempty"`
}

// AgentManifest describes one ACP agent.
type AgentManifest struct {
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	InputContentTypes  []string  `json:"input_content_types"`
	OutputContentTypes []string  `json:"output_content_types"`
	Metadata           *Metadata `json:"metadata,omitempty"`
	Status             *Status   `json:"status,omitempty"`
}

// Metadata is static discovery metadata for an agent.
type Metadata struct {
	Annotations         map[string]interface{} `json:"annotations,omitempty"`
	Documentation       string                 `json:"documentation,omitempty"`
	License             string                 `json:"license,omitempty"`
	ProgrammingLanguage string                 `json:"programming_language,omitempty"`
	NaturalLanguages    []string               `json:"natural_languages,omitempty"`
	Framework           string                 `json:"framework,omitempty"`
	Capabilities        []Capability           `json:"capabilities,omitempty"`
	Domains             []string               `json:"domains,omitempty"`
	Tags                []string               `json:"tags,omitempty"`
	CreatedAt           *time.Time             `json:"created_at,omitempty"`
	UpdatedAt           *time.Time             `json:"updated_at,omitempty"`
	Author              *Person                `json:"author,omitempty"`
	Contributors        []Person               `json:"contributors,omitempty"`
	Links               []Link                 `json:"links,omitempty"`
	Dependencies        []AgentDependency      `json:"dependencies,omitempty"`
	RecommendedModels   []string               `json:"recommended_models,omitempty"`
}

// Capability is a named capability entry in Agent metadata.
type Capability struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Person identifies a human associated with an agent.
type Person struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Link is an external resource reference.
type Link struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// AgentDependency is an experimental dependency reference.
type AgentDependency struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// Status holds optional runtime metrics for an agent.
type Status struct {
	AvgRunTokens      *float64 `json:"avg_run_tokens,omitempty"`
	AvgRunTimeSeconds *float64 `json:"avg_run_time_seconds,omitempty"`
	SuccessRate       *float64 `json:"success_rate,omitempty"`
}

// Run is an agent execution instance.
type Run struct {
	AgentName    string     `json:"agent_name"`
	SessionID    string     `json:"session_id,omitempty"`
	RunID        string     `json:"run_id"`
	Status       string     `json:"status"`
	AwaitRequest *struct{}  `json:"await_request"`
	Output       []Message  `json:"output"`
	Error        *Error     `json:"error"`
	CreatedAt    time.Time  `json:"created_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

// RunCreateRequest is the POST /runs body.
type RunCreateRequest struct {
	AgentName string    `json:"agent_name"`
	SessionID string    `json:"session_id,omitempty"`
	Session   *Session  `json:"session,omitempty"`
	Input     []Message `json:"input"`
	Mode      string    `json:"mode,omitempty"`
}

// RunResumeRequest is the POST /runs/{run_id} body.
type RunResumeRequest struct {
	RunID       string          `json:"run_id"`
	AwaitResume json.RawMessage `json:"await_resume"`
	Mode        string          `json:"mode,omitempty"`
}

// AgentsListResponse is GET /agents.
type AgentsListResponse struct {
	Agents []AgentManifest `json:"agents"`
}

// RunEventsListResponse is GET /runs/{run_id}/events.
type RunEventsListResponse struct {
	Events []Event `json:"events"`
}

// Event is a discriminated ACP event envelope. Only the fields matching
// Type are meaningful; others are omitted on marshal.
type Event struct {
	Type    string          `json:"type"`
	Message *Message        `json:"message,omitempty"`
	Part    *MessagePart    `json:"part,omitempty"`
	Run     *Run            `json:"run,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	Generic json.RawMessage `json:"generic,omitempty"`
}

// PingResponse is GET /ping.
type PingResponse struct {
	Protocol string `json:"protocol"`
	Version  string `json:"version"`
	Agent    string `json:"agent,omitempty"`
}

// NormalizeMode defaults empty mode to sync and validates the rest.
func NormalizeMode(mode string) (string, *Error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModeSync:
		return ModeSync, nil
	case ModeAsync:
		return ModeAsync, nil
	case ModeStream:
		return ModeStream, nil
	default:
		return "", &Error{
			Code:    ErrCodeInvalidInput,
			Message: "invalid mode: must be sync, async, or stream",
		}
	}
}

// ValidateAgentName reports whether name matches the ACP RFC1123 label rule.
func ValidateAgentName(name string) bool {
	return agentNamePattern.MatchString(name)
}

// SanitizeAgentName converts an arbitrary lele agent ID into an ACP-legal
// name (lowercase, hyphens). Empty input returns empty.
func SanitizeAgentName(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if b.Len() > 0 && b.String()[b.Len()-1] != '-' {
				b.WriteByte('-')
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = out[:63]
		out = strings.TrimRight(out, "-")
	}
	if !ValidateAgentName(out) {
		return ""
	}
	return out
}

// MessageFromText builds a single-part text message.
func MessageFromText(role, content string) Message {
	now := time.Now().UTC()
	return Message{
		Role: role,
		Parts: []MessagePart{{
			ContentType: "text/plain",
			Content:     content,
		}},
		CreatedAt: &now,
	}
}

// InputText concatenates all user-role messages from a run input.
func InputText(input []Message) string {
	var b strings.Builder
	for i, m := range input {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.Text())
	}
	return b.String()
}
