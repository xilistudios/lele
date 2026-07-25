// Package group implements multi-agent collaboration ("Mixture of Agents").
// manager.go contains GroupManager: lifecycle management for active groups.
package group

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

// DefaultGroupRounds is the number of rounds (MoA layers / round_robin
// cycles) applied when the caller specifies neither Rounds nor MaxTurns.
const DefaultGroupRounds = 2

// MaxGroupTurnsCeiling is the absolute hard ceiling on total turns for any
// group session. Even if Rounds*participants exceeds this value, the actual
// MaxTurns is capped to this ceiling.
const MaxGroupTurnsCeiling = 50

// GroupOptions holds runtime limits for a group session.
type GroupOptions struct {
	Rounds           int      // MoA layers / round_robin cycles; 0 = unlimited
	MaxTurns         int      // hard cap on total turns; 0 = derived from Rounds
	Parallel         bool     // whether speakers within a batch run concurrently
	Moderator        string   // agent ID of the aggregator/moderator
	StopKeywords     []string // keywords that trigger convergence
	MaxTokensPerTurn int      // per-turn token cap
	TotalTokenBudget int      // hard cap on cumulative tokens; 0 = unlimited
}

// managedGroup is the runtime state of an active group (not exported).
type managedGroup struct {
	state      *GroupState
	originCh   string // channel origin for events
	originChat string // chat ID origin for events
	cancel     context.CancelFunc
	result     string        // final synthesis (set on completion)
	done       chan struct{} // closed when the group finishes
	err        error
}

// GroupManager manages the lifecycle of active group conversations.
type GroupManager struct {
	mu               sync.Mutex
	groups           map[string]*managedGroup
	resolve          ResolveAgentFunc
	executor         TurnExecutor
	publish          Publisher
	moderatorDecider ModeratorDecider // optional; nil → defaultModeratorDecider
	storeDir         string           // persistence directory; empty = no persistence
}

// NewGroupManager creates a GroupManager with the given injected dependencies.
func NewGroupManager(resolve ResolveAgentFunc, executor TurnExecutor, publish Publisher) *GroupManager {
	return &GroupManager{
		groups:   make(map[string]*managedGroup),
		resolve:  resolve,
		executor: executor,
		publish:  publish,
	}
}

// SetStoreDir configures the directory used by SaveGroup/LoadGroup for
// JSON persistence of group state.  An empty dir disables persistence.
func (gm *GroupManager) SetStoreDir(dir string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.storeDir = dir
}

// SetModeratorDecider overrides the default moderator decider used by the
// moderator strategy. Pass nil to reset to the built-in default.
func (gm *GroupManager) SetModeratorDecider(d ModeratorDecider) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.moderatorDecider = d
}

// Start validates participants, constructs a GroupState, publishes a
// group.status=started event, and launches the group loop in a goroutine.
// Returns the groupID on success.
func (gm *GroupManager) Start(
	ctx context.Context,
	groupID, profileID, task, strategy string,
	participants []Participant,
	opts GroupOptions,
	originChannel, originChatID string,
) (string, error) {
	// Validate strategy exists.
	if _, err := NewStrategy(strategy); err != nil {
		return "", fmt.Errorf("group start: %w", err)
	}

	if len(participants) == 0 {
		return "", fmt.Errorf("group start: at least one participant required")
	}

	// Validate every participant resolves.
	agentIDs := make([]string, 0, len(participants))
	for _, p := range participants {
		if _, ok := gm.resolve(p.AgentID); !ok {
			return "", fmt.Errorf("group start: participant %q not found", p.AgentID)
		}
		agentIDs = append(agentIDs, p.AgentID)
	}

	now := time.Now()

	// Derive limits: apply defaults and enforce the hard ceiling.
	rounds := opts.Rounds
	maxTurns := opts.MaxTurns
	appliedDefault := false

	// If neither Rounds nor MaxTurns was specified, apply sensible defaults.
	if maxTurns == 0 && rounds == 0 {
		rounds = DefaultGroupRounds
		appliedDefault = true
	}

	// Derive MaxTurns from Rounds if not explicitly set.
	if maxTurns == 0 && rounds > 0 {
		maxTurns = rounds * len(participants)
	}

	// Always enforce the hard ceiling.
	if maxTurns <= 0 || maxTurns > MaxGroupTurnsCeiling {
		maxTurns = MaxGroupTurnsCeiling
		appliedDefault = true
	}

	if appliedDefault {
		log.Printf("group %s: applied default limits (rounds=%d, max_turns=%d)", groupID, rounds, maxTurns)
	}

	state := &GroupState{
		ID:               groupID,
		ProfileID:        profileID,
		Task:             task,
		Participants:     participants,
		Strategy:         strategy,
		Status:           StatusRunning,
		CreatedAt:        now,
		UpdatedAt:        now,
		Rounds:           rounds,
		MaxTurns:         maxTurns,
		Parallel:         opts.Parallel,
		Moderator:        opts.Moderator,
		StopKeywords:     opts.StopKeywords,
		MaxTokensPerTurn: opts.MaxTokensPerTurn,
		TotalTokenBudget: opts.TotalTokenBudget,
		OriginChannel:    originChannel,
		OriginChatID:     originChatID,
	}

	gctx, cancel := context.WithCancel(ctx)

	mg := &managedGroup{
		state:      state,
		originCh:   originChannel,
		originChat: originChatID,
		cancel:     cancel,
		done:       make(chan struct{}),
	}

	gm.mu.Lock()
	if _, exists := gm.groups[groupID]; exists {
		gm.mu.Unlock()
		cancel()
		return "", fmt.Errorf("group start: group %q already exists", groupID)
	}
	gm.groups[groupID] = mg
	storeDir := gm.storeDir // capture under lock
	gm.mu.Unlock()

	// Publish started event.
	gm.publishGroupStatus(mg, "started", agentIDs)

	// Best-effort: persist the started state.
	if storeDir != "" {
		if err := SaveGroup(storeDir, state); err != nil {
			log.Printf("group %s: failed to persist started state: %v", groupID, err)
		}
	}

	go gm.runGroup(gctx, mg)

	return groupID, nil
}

// Stop cancels the context of an active group and marks it stopped.
// Returns true if the group was found and stopped.
func (gm *GroupManager) Stop(groupID string) bool {
	gm.mu.Lock()
	mg, ok := gm.groups[groupID]
	if !ok {
		gm.mu.Unlock()
		return false
	}
	gm.mu.Unlock()

	mg.cancel()

	gm.mu.Lock()
	if mg.state.Status == StatusRunning {
		mg.state.Status = StatusStopped
		mg.state.UpdatedAt = time.Now()
	}
	agentIDs := gm.agentIDsLocked(mg)
	gm.mu.Unlock()

	gm.publishGroupStatus(mg, "stopped", agentIDs)
	return true
}

// Status returns an immutable snapshot of the GroupState and true if the
// group exists. The returned pointer is a deep copy safe to read without
// holding the manager lock.
func (gm *GroupManager) Status(groupID string) (*GroupState, bool) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	mg, ok := gm.groups[groupID]
	if !ok {
		return nil, false
	}
	return mg.state.Snapshot(), true
}

// List returns a snapshot of the GroupState for every tracked group
// (active and finished). Each entry is a deep copy safe to read without
// holding the manager lock.
func (gm *GroupManager) List() []*GroupState {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	out := make([]*GroupState, 0, len(gm.groups))
	for _, mg := range gm.groups {
		out = append(out, mg.state.Snapshot())
	}
	return out
}

// AllSnapshots returns a GroupSnapshot for every tracked group (active and
// finished). The caller gets a snapshot-safe copy; it does not share backing
// arrays with the live state.
func (gm *GroupManager) AllSnapshots() []GroupSnapshot {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	out := make([]GroupSnapshot, 0, len(gm.groups))
	for _, mg := range gm.groups {
		out = append(out, BuildSnapshot(mg.state, mg.result))
	}
	return out
}

// Wait blocks until the group finishes and returns the final synthesis.
// Returns an error if the group ended with an error.
func (gm *GroupManager) Wait(groupID string) (string, error) {
	gm.mu.Lock()
	mg, ok := gm.groups[groupID]
	gm.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("group wait: group %q not found", groupID)
	}

	<-mg.done
	return mg.result, mg.err
}

// StopAll cancels every active group. Returns the number of groups stopped.
func (gm *GroupManager) StopAll() int {
	gm.mu.Lock()
	var toStop []*managedGroup
	for _, mg := range gm.groups {
		if mg.state.Status == StatusRunning {
			toStop = append(toStop, mg)
		}
	}
	gm.mu.Unlock()

	for _, mg := range toStop {
		gm.Stop(mg.state.ID)
	}
	return len(toStop)
}

// StopByOrigin cancels every running group whose origin chat matches
// chatID. When channel is non-empty it must also match the group's origin
// channel; an empty channel matches any. Returns the number of groups stopped.
func (gm *GroupManager) StopByOrigin(channel, chatID string) int {
	if chatID == "" {
		return 0
	}

	gm.mu.Lock()
	var toStop []string
	for _, mg := range gm.groups {
		if mg.state.Status == StatusRunning &&
			mg.originChat == chatID &&
			(channel == "" || mg.originCh == channel) {
			toStop = append(toStop, mg.state.ID)
		}
	}
	gm.mu.Unlock()

	var stopped int
	for _, id := range toStop {
		if gm.Stop(id) {
			stopped++
		}
	}
	return stopped
}

// publishGroupStatus publishes a group.status event.
func (gm *GroupManager) publishGroupStatus(mg *managedGroup, status string, agentIDs []string) {
	gm.publish(bus.OutboundMessage{
		Channel:        mg.originCh,
		ChatID:         mg.originChat,
		Event:          "group.status",
		Content:        fmt.Sprintf("Group %s: %s", mg.state.ID, status),
		IsIntermediate: true,
		Metadata: map[string]string{
			"group_id":     mg.state.ID,
			"status":       status,
			"participants": strings.Join(agentIDs, ","),
		},
	})
}

// agentIDsLocked returns the agent IDs of participants. Caller must hold gm.mu.
func (gm *GroupManager) agentIDsLocked(mg *managedGroup) []string {
	ids := make([]string, len(mg.state.Participants))
	for i, p := range mg.state.Participants {
		ids[i] = p.AgentID
	}
	return ids
}

// saveStateBestEffort persists the group state to disk if a storeDir is
// configured.  It reads storeDir under gm.mu.  Errors are logged but never
// returned (best-effort semantics).
func (gm *GroupManager) saveStateBestEffort(mg *managedGroup) {
	gm.mu.Lock()
	dir := gm.storeDir
	gm.mu.Unlock()
	if dir == "" {
		return
	}
	if err := SaveGroup(dir, mg.state); err != nil {
		log.Printf("group %s: failed to persist state: %v", mg.state.ID, err)
	}
}
