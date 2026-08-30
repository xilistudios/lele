package group

import (
	"fmt"
	"time"
)

// Group status constants.
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusStopped = "stopped"
	StatusError   = "error"
)

// Participant role constants.
const (
	RoleProposer   = "proposer"
	RoleAggregator = "aggregator"
	RoleModerator  = "moderator"
	RoleCritic     = "critic"
)

// Participant represents an agent with a role within a group.
type Participant struct {
	AgentID string `json:"agent_id"`        // resolved against AgentRegistry
	Role    string `json:"role,omitempty"`  // proposer | aggregator | moderator | critic | ""
	Label   string `json:"label,omitempty"` // display name shown in the transcript
}

// GroupToolCall represents a tool call made during a group turn.
type GroupToolCall struct {
	ToolCallID string `json:"tool_call_id"`
	Tool       string `json:"tool"`
	Status     string `json:"status"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
}

// Turn represents a single intervention in the shared transcript.
type Turn struct {
	// Index is the unique turn number, reserved at turn start (see
	// GroupState.NextTurnIndex) and never renumbered afterwards. Under
	// Parallel execution turns may be appended in completion order, so the
	// position of a turn inside Transcript can differ from its Index. What
	// the WS contract guarantees is uniqueness and stability: the same Index
	// appears in group.turn, group.tool and the persisted transcript.
	Index     int             `json:"index"`
	Layer     int             `json:"layer"`                // MoA layer (0..L); 0 for round_robin/pipeline
	Speaker   string          `json:"speaker"`              // AgentID of the participant who spoke
	Label     string          `json:"label,omitempty"`      // display label of the speaker
	Content   string          `json:"content"`              // the actual text produced
	CreatedAt time.Time       `json:"created_at"`           // when this turn was created
	Tokens    int             `json:"tokens"`               // token count for this turn
	ToolCalls []GroupToolCall `json:"tool_calls,omitempty"` // tool calls made during this turn
}

// GroupState holds the live and persistable state of a group conversation.
type GroupState struct {
	ID           string        `json:"id"`           // unique group identifier (e.g. "group:<id>")
	ProfileID    string        `json:"profile_id"`   // GroupProfile that started this group
	Task         string        `json:"task"`         // the objective/prompt for the group
	Participants []Participant `json:"participants"` // agents participating in this group
	Strategy     string        `json:"strategy"`     // name of the strategy driving the group
	Transcript   []Turn        `json:"transcript"`   // ordered shared transcript
	Status       string        `json:"status"`       // running | done | stopped | error
	CreatedAt    time.Time     `json:"created_at"`   // when the group was created
	UpdatedAt    time.Time     `json:"updated_at"`   // last modification time
	TotalTokens  int           `json:"total_tokens"` // cumulative token count across all turns

	// Runtime configuration (populated from GroupProfile at group start).
	// Strategies read these from GroupState since StrategyFactory receives no params.
	Rounds           int      `json:"rounds"`                        // MoA layers / round_robin cycles; 0 = unlimited (capped by MaxTurns)
	MaxTurns         int      `json:"max_turns"`                     // hard cap on total turns; 0 = unlimited
	Parallel         bool     `json:"parallel"`                      // whether proposers within a layer speak in parallel
	Moderator        string   `json:"moderator,omitempty"`           // agent ID of the aggregator/moderator
	StopKeywords     []string `json:"stop_keywords,omitempty"`       // keywords that trigger convergence
	MaxTokensPerTurn int      `json:"max_tokens_per_turn,omitempty"` // per-turn token cap (informational for the runner)
	TotalTokenBudget int      `json:"total_token_budget,omitempty"`  // hard cap on TotalTokens; 0 = unlimited

	// NextTurnIndex is the monotonic counter used to reserve a unique
	// Turn.Index at turn start (under gm.mu), before the speaker runs. It
	// replaces the old len(Transcript)-at-append-time calculation, which under
	// Parallel execution could hand the same index to two concurrent turns and
	// made the index depend on completion order. 0 is a valid first index, so
	// a state rehydrated from an older persisted snapshot (field absent → 0)
	// must be re-based to len(Transcript) before use; see
	// GroupManager.prepareTurn.
	NextTurnIndex int `json:"next_turn_index,omitempty"`

	// Origin identifies the chat session that started this group, so the group
	// can be looked up by session for history rehydration.
	OriginChannel string `json:"origin_channel,omitempty"`
	OriginChatID  string `json:"origin_chat_id,omitempty"`
}

// AddTurn appends a turn to the transcript, updates UpdatedAt, and
// accumulates the turn's token count into TotalTokens.
func (g *GroupState) AddTurn(t Turn) {
	g.Transcript = append(g.Transcript, t)
	g.UpdatedAt = time.Now()
	g.TotalTokens += t.Tokens
}

// LastTurn returns the most recent turn in the transcript.
// If the transcript is empty, it returns a zero Turn and false.
func (g *GroupState) LastTurn() (Turn, bool) {
	if len(g.Transcript) == 0 {
		return Turn{}, false
	}
	return g.Transcript[len(g.Transcript)-1], true
}

// ParticipantByAgent looks up a participant by their AgentID.
// Returns the participant and true if found, or a zero Participant and false otherwise.
func (g *GroupState) ParticipantByAgent(agentID string) (Participant, bool) {
	for _, p := range g.Participants {
		if p.AgentID == agentID {
			return p, true
		}
	}
	return Participant{}, false
}

// Snapshot returns a deep copy of the GroupState safe to read without holding
// the manager lock. Slice fields (Participants, Transcript, StopKeywords) are
// copied so the snapshot does not share backing arrays with the live state.
//
// Each Turn's ToolCalls slice is copied too: GroupToolCall holds only value
// fields, so a shallow slice copy is a full copy of the data, and it is what
// keeps the snapshot race-free while a running turn appends tool calls.
func (s *GroupState) Snapshot() *GroupState {
	if s == nil {
		return nil
	}
	cp := *s // shallow copy of value fields
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

// String returns a human-readable summary of the group state.
func (g *GroupState) String() string {
	return fmt.Sprintf("Group(%s status=%s participants=%d turns=%d tokens=%d)",
		g.ID, g.Status, len(g.Participants), len(g.Transcript), g.TotalTokens)
}
