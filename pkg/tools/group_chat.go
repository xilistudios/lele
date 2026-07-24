package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
)

// GroupChatTool delegates a problem to a multi-agent panel using one of
// the supported strategies (round_robin, moa, moderator, pipeline) and
// returns the final synthesis. It is synchronous (Start + Wait).
type GroupChatTool struct {
	manager        *group.GroupManager
	originChannel  string
	originChatID   string
	allowlistCheck func(targetAgentID string) bool
}

// NewGroupChatTool creates a GroupChatTool backed by the given GroupManager.
// Default origin is cli/direct.
func NewGroupChatTool(manager *group.GroupManager) *GroupChatTool {
	return &GroupChatTool{
		manager:       manager,
		originChannel: "cli",
		originChatID:  "direct",
	}
}

func (t *GroupChatTool) Name() string {
	return "group_chat"
}

func (t *GroupChatTool) Description() string {
	return "Delegates a problem to a multi-agent panel (round_robin/moa/moderator/pipeline) and returns the final synthesis."
}

func (t *GroupChatTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task": map[string]interface{}{
				"type":        "string",
				"description": "Objective / prompt for the multi-agent panel",
			},
			"participants": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Agent IDs to include in the panel",
			},
			"strategy": map[string]interface{}{
				"type":        "string",
				"description": "Collaboration strategy",
				"enum":        []interface{}{"round_robin", "moa", "moderator", "pipeline"},
				"default":     "round_robin",
			},
			"rounds": map[string]interface{}{
				"type":        "integer",
				"description": "Number of rounds / cycles (0 = unlimited)",
			},
			"moderator": map[string]interface{}{
				"type":        "string",
				"description": "Agent ID to use as moderator/aggregator (moa and moderator strategies)",
			},
			"parallel": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether proposers within a batch run concurrently",
			},
			"max_turns": map[string]interface{}{
				"type":        "integer",
				"description": "Hard cap on total turns",
			},
			"max_tokens_per_turn": map[string]interface{}{
				"type":        "integer",
				"description": "Per-turn token cap",
			},
			"total_token_budget": map[string]interface{}{
				"type":        "integer",
				"description": "Hard cap on cumulative tokens; 0 = unlimited",
			},
			"stop_keywords": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Keywords that trigger convergence",
			},
		},
		"required": []interface{}{"task", "participants"},
	}
}

// SetContext implements ContextualTool so the coordinator can update origin info.
func (t *GroupChatTool) SetContext(channel, chatID string) {
	t.originChannel = channel
	t.originChatID = chatID
}

// SetAllowlistChecker registers a callback that determines whether the caller
// may include a specific agent in the panel.
func (t *GroupChatTool) SetAllowlistChecker(check func(targetAgentID string) bool) {
	t.allowlistCheck = check
}

// Execute launches the group and blocks until the synthesis is produced.
func (t *GroupChatTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	// --- Parse args ---
	task, _ := args["task"].(string)
	if strings.TrimSpace(task) == "" {
		return ErrorResult("group_chat: 'task' is required and must be non-empty")
	}

	participantsRaw, ok := args["participants"].([]interface{})
	if !ok || len(participantsRaw) == 0 {
		return ErrorResult("group_chat: 'participants' is required and must be a non-empty array of agent IDs")
	}

	var agentIDs []string
	for _, p := range participantsRaw {
		if s, ok := p.(string); ok && strings.TrimSpace(s) != "" {
			agentIDs = append(agentIDs, strings.TrimSpace(s))
		}
	}
	if len(agentIDs) == 0 {
		return ErrorResult("group_chat: 'participants' must contain at least one non-empty agent ID")
	}

	strategy := "round_robin"
	if s, ok := args["strategy"].(string); ok && s != "" {
		strategy = s
	}
	if !config.ValidStrategy(strategy) {
		return ErrorResult(fmt.Sprintf("group_chat: invalid strategy %q (valid: round_robin, moa, moderator, pipeline)", strategy))
	}

	// --- Check allowlist ---
	if t.allowlistCheck != nil {
		var denied []string
		for _, id := range agentIDs {
			if !t.allowlistCheck(id) {
				denied = append(denied, id)
			}
		}
		if len(denied) > 0 {
			return ErrorResult(fmt.Sprintf("group_chat: agent(s) not allowed: %s", strings.Join(denied, ", ")))
		}
	}

	// --- Build participants with roles ---
	participants := make([]group.Participant, 0, len(agentIDs))
	moderator, _ := args["moderator"].(string)

	switch strategy {
	case "moa":
		for _, id := range agentIDs {
			role := group.RoleProposer
			if moderator != "" && id == moderator {
				role = group.RoleAggregator
			}
			participants = append(participants, group.Participant{AgentID: id, Role: role, Label: id})
		}
	case "moderator":
		for _, id := range agentIDs {
			role := group.RoleProposer
			if moderator != "" && id == moderator {
				role = group.RoleModerator
			}
			participants = append(participants, group.Participant{AgentID: id, Role: role, Label: id})
		}
	default:
		for _, id := range agentIDs {
			participants = append(participants, group.Participant{AgentID: id, Role: group.RoleProposer, Label: id})
		}
	}

	// --- Build GroupOptions ---
	opts := group.GroupOptions{
		Moderator: moderator,
	}
	if v, ok := args["rounds"].(float64); ok {
		opts.Rounds = int(v)
	}
	if v, ok := args["parallel"].(bool); ok {
		opts.Parallel = v
	}
	if v, ok := args["max_turns"].(float64); ok {
		opts.MaxTurns = int(v)
	}
	if v, ok := args["max_tokens_per_turn"].(float64); ok {
		opts.MaxTokensPerTurn = int(v)
	}
	if v, ok := args["total_token_budget"].(float64); ok {
		opts.TotalTokenBudget = int(v)
	}
	if raw, ok := args["stop_keywords"].([]interface{}); ok {
		for _, kw := range raw {
			if s, ok := kw.(string); ok && strings.TrimSpace(s) != "" {
				opts.StopKeywords = append(opts.StopKeywords, strings.TrimSpace(s))
			}
		}
	}

	// --- Start the group ---
	groupID := group.NewGroupID("tool")
	_, err := t.manager.Start(ctx, groupID, "", task, strategy, participants, opts, t.originChannel, t.originChatID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("group_chat: failed to start group: %v", err))
	}

	// --- Wait for synthesis (synchronous) ---
	synthesis, err := t.manager.Wait(groupID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("group_chat: group failed: %v", err))
	}

	// --- Build result with brief header for LLM context ---
	header := fmt.Sprintf("Group chat completed (strategy=%s, participants=%s):\n\n", strategy, strings.Join(agentIDs, ", "))
	return &ToolResult{ForLLM: header + synthesis}
}
