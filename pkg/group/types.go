// Package group implements multi-agent collaboration ("Mixture of Agents").
// It defines the shared types and state for group conversations where
// multiple agents collaborate in a shared transcript.
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
	AgentID string // resolved against AgentRegistry
	Role    string // proposer | aggregator | moderator | critic | ""
	Label   string // display name shown in the transcript
}

// Turn represents a single intervention in the shared transcript.
type Turn struct {
	Index     int       // sequential turn number within the group
	Layer     int       // MoA layer (0..L); 0 for round_robin/pipeline
	Speaker   string    // AgentID of the participant who spoke
	Label     string    // display label of the speaker
	Content   string    // the actual text produced
	CreatedAt time.Time // when this turn was created
	Tokens    int       // token count for this turn
}

// GroupState holds the live and persistable state of a group conversation.
type GroupState struct {
	ID           string        // unique group identifier (e.g. "group:<id>")
	ProfileID    string        // GroupProfile that started this group
	Task         string        // the objective/prompt for the group
	Participants []Participant // agents participating in this group
	Strategy     string        // name of the strategy driving the group
	Transcript   []Turn        // ordered shared transcript
	Status       string        // running | done | stopped | error
	CreatedAt    time.Time     // when the group was created
	UpdatedAt    time.Time     // last modification time
	TotalTokens  int           // cumulative token count across all turns

	// Runtime configuration (populated from GroupProfile at group start).
	// Strategies read these from GroupState since StrategyFactory receives no params.
	Rounds           int      // MoA layers / round_robin cycles; 0 = unlimited (capped by MaxTurns)
	MaxTurns         int      // hard cap on total turns; 0 = unlimited
	Parallel         bool     // whether proposers within a layer speak in parallel
	Moderator        string   // agent ID of the aggregator/moderator
	StopKeywords     []string // keywords that trigger convergence
	MaxTokensPerTurn int      // per-turn token cap (informational for the runner)
	TotalTokenBudget int      // hard cap on TotalTokens; 0 = unlimited
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
