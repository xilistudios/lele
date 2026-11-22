package session

import "time"

type InProgressTool struct {
	Tool               string    `json:"tool"`
	Action             string    `json:"action,omitempty"`
	Arguments          string    `json:"arguments,omitempty"`
	ToolCallID         string    `json:"tool_call_id,omitempty"`
	SubagentSessionKey string    `json:"subagent_session_key,omitempty"`
	StartedAt          time.Time `json:"started_at"`
}

func (t *InProgressTool) Clone() *InProgressTool {
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}
