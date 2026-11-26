package group

import (
	"fmt"
	"time"
)

const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusStopped = "stopped"
	StatusError   = "error"
)

const (
	RoleProposer   = "proposer"
	RoleAggregator = "aggregator"
	RoleModerator  = "moderator"
	RoleCritic     = "critic"
)

type Participant struct {
	AgentID string `json:"agent_id"`
	Role    string `json:"role,omitempty"`
	Label   string `json:"label,omitempty"`
}

type GroupToolCall struct {
	ToolCallID string `json:"tool_call_id"`
	Tool       string `json:"tool"`
	Status     string `json:"status"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
}

type Turn struct {
	Index     int             `json:"index"`
	Layer     int             `json:"layer"`
	Speaker   string          `json:"speaker"`
	Label     string          `json:"label,omitempty"`
	Content   string          `json:"content"`
	CreatedAt time.Time       `json:"created_at"`
	Tokens    int             `json:"tokens"`
	ToolCalls []GroupToolCall `json:"tool_calls,omitempty"`
}

type GroupState struct {
	ID           string        `json:"id"`
	ProfileID    string        `json:"profile_id"`
	Task         string        `json:"task"`
	Participants []Participant `json:"participants"`
	Strategy     string        `json:"strategy"`
	Transcript   []Turn        `json:"transcript"`
	Status       string        `json:"status"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	TotalTokens  int           `json:"total_tokens"`

	Rounds           int      `json:"rounds"`
	MaxTurns         int      `json:"max_turns"`
	Parallel         bool     `json:"parallel"`
	Moderator        string   `json:"moderator,omitempty"`
	StopKeywords     []string `json:"stop_keywords,omitempty"`
	MaxTokensPerTurn int      `json:"max_tokens_per_turn,omitempty"`
	TotalTokenBudget int      `json:"total_token_budget,omitempty"`

	NextTurnIndex int `json:"next_turn_index,omitempty"`

	OriginChannel  string `json:"origin_channel,omitempty"`
	OriginChatID   string `json:"origin_chat_id,omitempty"`
	OriginSenderID string `json:"origin_sender_id,omitempty"`
}

func (g *GroupState) AddTurn(t Turn) {
	g.Transcript = append(g.Transcript, t)
	g.UpdatedAt = time.Now()
	g.TotalTokens += t.Tokens
}

func (g *GroupState) LastTurn() (Turn, bool) {
	if len(g.Transcript) == 0 {
		return Turn{}, false
	}
	return g.Transcript[len(g.Transcript)-1], true
}

func (g *GroupState) ParticipantByAgent(agentID string) (Participant, bool) {
	for _, p := range g.Participants {
		if p.AgentID == agentID {
			return p, true
		}
	}
	return Participant{}, false
}

func (s *GroupState) Snapshot() *GroupState {
	if s == nil {
		return nil
	}
	cp := *s
	if s.Participants != nil {
		cp.Participants = make([]Participant, len(s.Participants))
		copy(cp.Participants, s.Participants)
	}
	if s.Transcript != nil {
		cp.Transcript = make([]Turn, len(s.Transcript))
		copy(cp.Transcript, s.Transcript)
		for i := range cp.Transcript {
			if tc := cp.Transcript[i].ToolCalls; tc != nil {
				cp.Transcript[i].ToolCalls = make([]GroupToolCall, len(tc))
				copy(cp.Transcript[i].ToolCalls, tc)
			}
		}
	}
	if s.StopKeywords != nil {
		cp.StopKeywords = make([]string, len(s.StopKeywords))
		copy(cp.StopKeywords, s.StopKeywords)
	}
	return &cp
}

func (g *GroupState) String() string {
	return fmt.Sprintf("Group(%s status=%s participants=%d turns=%d tokens=%d)",
		g.ID, g.Status, len(g.Participants), len(g.Transcript), g.TotalTokens)
}
