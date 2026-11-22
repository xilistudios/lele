package group

import (
	"strings"
	"time"
)

type SnapshotTurn struct {
	TurnIndex int             `json:"turn_index"`
	Speaker   string          `json:"speaker"`
	Label     string          `json:"label"`
	Role      string          `json:"role"`
	Layer     int             `json:"layer"`
	Content   string          `json:"content"`
	ToolCalls []GroupToolCall `json:"tool_calls"`
}

type GroupSnapshot struct {
	GroupID       string         `json:"group_id"`
	Status        string         `json:"status"`
	Strategy      string         `json:"strategy"`
	Participants  string         `json:"participants"`
	Layers        int            `json:"layers"`
	TotalTokens   int            `json:"total_tokens"`
	CreatedAt     string         `json:"created_at"`
	Synthesis     string         `json:"synthesis"`
	OriginChannel string         `json:"-"`
	OriginChatID  string         `json:"-"`
	Turns         []SnapshotTurn `json:"turns"`
}

func BuildSnapshot(state *GroupState, synthesis string) GroupSnapshot {
	ids := make([]string, len(state.Participants))
	for i, p := range state.Participants {
		ids[i] = p.AgentID
	}

	layers := 1
	if len(state.Transcript) > 0 {
		maxLayer := 0
		for _, t := range state.Transcript {
			if t.Layer > maxLayer {
				maxLayer = t.Layer
			}
		}
		layers = maxLayer + 1
	}

	roleByAgent := make(map[string]string, len(state.Participants))
	for _, p := range state.Participants {
		roleByAgent[p.AgentID] = p.Role
	}

	turns := make([]SnapshotTurn, len(state.Transcript))
	for i, t := range state.Transcript {
		role := mapRole(roleByAgent[t.Speaker])
		var tc []GroupToolCall
		if len(t.ToolCalls) > 0 {
			tc = make([]GroupToolCall, len(t.ToolCalls))
			copy(tc, t.ToolCalls)
		}
		turns[i] = SnapshotTurn{
			TurnIndex: t.Index,
			Speaker:   t.Speaker,
			Label:     t.Label,
			Role:      role,
			Layer:     t.Layer,
			Content:   t.Content,
			ToolCalls: tc,
		}
	}

	return GroupSnapshot{
		GroupID:       state.ID,
		Status:        state.Status,
		Strategy:      state.Strategy,
		Participants:  strings.Join(ids, ","),
		Layers:        layers,
		TotalTokens:   state.TotalTokens,
		CreatedAt:     state.CreatedAt.Format(time.RFC3339),
		Synthesis:     synthesis,
		OriginChannel: state.OriginChannel,
		OriginChatID:  state.OriginChatID,
		Turns:         turns,
	}
}

func mapRole(role string) string {
	switch role {
	case RoleAggregator:
		return "aggregator"
	case RoleModerator:
		return "moderator"
	case RoleCritic:
		return "critic"
	case RoleProposer, "":
		return "proposer"
	default:
		return "proposer"
	}
}
