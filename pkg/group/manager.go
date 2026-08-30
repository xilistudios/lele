// Package group implements multi-agent collaboration ("Mixture of Agents").
// manager.go contains GroupManager: lifecycle management for active groups.
package group

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/store"
)

// DefaultGroupRounds is the number of rounds (MoA layers / round_robin
// cycles) applied when the caller specifies neither Rounds nor MaxTurns.
const DefaultGroupRounds = 2

// MaxGroupTurnsCeiling is the absolute hard ceiling on total turns for any
// group session. Even if Rounds*participants exceeds this value, the actual
// MaxTurns is capped to this ceiling.
const MaxGroupTurnsCeiling = 50

// DefaultGroupRetention is how long a finished group (done | stopped | error)
// stays tracked by the GroupManager after it terminated. While retained it is
// still visible to List/Status/AllSnapshots, which is what lets the WebUI
// welcome payload and "/group status <id>" show the group that just ended.
// After the retention window elapses the group — together with its full
// transcript — is dropped from memory (B6: without this the map grew for the
// whole process lifetime).
const DefaultGroupRetention = 30 * time.Minute

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
	finishedAt time.Time // when the group reached a terminal status (zero while running)

	// finalizeOnce guards the single terminal signal pair (group.status +
	// group.complete) and the close of done, so no exit path can emit twice.
	finalizeOnce sync.Once
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
	enabled          func() bool      // optional feature gate; nil → always allowed
	retention        time.Duration    // how long finished groups stay tracked; 0 → DefaultGroupRetention
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

// SetStore configures SQLite persistence for this manager. When set,
// group state is persisted through the repository instead of the JSON
// dir. The repository is stored at package level (consistent with
// pkg/auth.UseStore), so all managers in the process share it.
func (gm *GroupManager) SetStore(repo *store.GroupRepo) {
	UseStore(repo)
}

// SetModeratorDecider overrides the default moderator decider used by the
// moderator strategy. Pass nil to reset to the built-in default.
func (gm *GroupManager) SetModeratorDecider(d ModeratorDecider) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.moderatorDecider = d
}

// SetRetention overrides how long a finished group stays tracked before the
// lazy sweeper drops it (see DefaultGroupRetention). Values <= 0 restore the
// default. It exists mainly so tests can exercise eviction without waiting 30
// minutes; call it before starting groups.
func (gm *GroupManager) SetRetention(d time.Duration) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.retention = d
}

// retentionLocked returns the effective retention window. gm.mu must be held.
func (gm *GroupManager) retentionLocked() time.Duration {
	if gm.retention > 0 {
		return gm.retention
	}
	return DefaultGroupRetention
}

// isTerminalGroupStatus reports whether a group status ends the group's life
// cycle (done | stopped | error).
func isTerminalGroupStatus(status string) bool {
	return status == StatusDone || status == StatusStopped || status == StatusError
}

// evictExpiredLocked drops finished groups whose retention window has elapsed.
// gm.mu must be held by the caller; now is injected so the rule stays testable.
//
// It is deliberately called lazily from the read paths (Start/Status/List/
// AllSnapshots) instead of running a permanent sweeper goroutine: the only
// consumers of the map are those reads, so an expired group can never be
// observed after eviction, and a manager that is never queried holds no extra
// goroutine or timer.
func (gm *GroupManager) evictExpiredLocked(now time.Time) {
	if len(gm.groups) == 0 {
		return
	}
	retention := gm.retentionLocked()
	for id, mg := range gm.groups {
		if !isTerminalGroupStatus(mg.state.Status) {
			continue
		}
		if mg.finishedAt.IsZero() || now.Before(mg.finishedAt.Add(retention)) {
			continue
		}
		// Belt and braces: finalize stamps finishedAt before it closes done (it
		// publishes the terminal pair in between), so a group can be terminal and
		// expired yet still mid-finalize if publishing is slow. Never drop it
		// before done is closed — that would make a concurrent Wait report
		// "evicted" for a group whose result is still being computed.
		select {
		case <-mg.done:
		default:
			continue
		}
		delete(gm.groups, id)
	}
}

// LoadHistorical rehydrates the groups persisted under storeDir into the
// in-memory map (B7). Without it the manager started empty after every process
// restart: chat sessions survived restarts but "/group list" and the WebSocket
// welcome payload lost every finished group, because Start saved state to disk
// and nothing ever read it back.
//
// The rules that make a rehydrated group safe to expose:
//
//   - it is inert: no runGroup goroutine, no group.status/group.complete event,
//     a no-op cancel. It exists to be listed and inspected, not to be resumed;
//   - its done channel is already closed, so Wait returns immediately with the
//     recomputed synthesis instead of blocking on a run that will never happen;
//   - a status that is not terminal on disk ("running"/"started" — the process
//     died mid-turn) is re-marked StatusError with a synthetic err, otherwise
//     such a group would look permanently active, be immune to retention, and
//     hang any Wait forever;
//   - finishedAt is stamped from state.UpdatedAt, so the retention sweep (see
//     evictExpiredLocked) expires weeks-old groups on the first read instead of
//     flooding the welcome payload with history.
//
// The synthesis is not persisted in GroupState, so it is recomputed from the
// transcript with the same function finalize uses (synthesisLocked): the
// aggregator's last turn for moa, the last turn otherwise, "" for an empty
// transcript. The per-group error is likewise not persisted, so a group that
// ended in error or stopped comes back with err == nil; only the re-marked,
// restart-orphaned groups carry the synthetic error.
//
// It is idempotent: a group whose ID is already tracked (an active run with the
// same ID, or a previous LoadHistorical call) is skipped and logged, never
// overwritten. Returns the number of groups loaded. With no storeDir configured
// it is a no-op returning (0, nil).
//
// Feature gate (deliberate): LoadHistorical does NOT consult the enabled hook.
// Disabling groups refuses new runs (Start → ErrGroupsDisabled) but must not
// erase or hide the operator's existing history — same semantics as disabling
// a chat channel not deleting its stored conversations. Hydrated groups are
// inert read-only state; they expire from memory via the normal retention.
func (gm *GroupManager) LoadHistorical() (int, error) {
	gm.mu.Lock()
	dir := gm.storeDir
	gm.mu.Unlock()

	if dir == "" {
		return 0, nil
	}

	states, err := ListGroups(dir)
	if err != nil {
		return 0, fmt.Errorf("group hydrate: %w", err)
	}

	gm.mu.Lock()
	defer gm.mu.Unlock()

	now := time.Now()
	loaded := 0
	for _, st := range states {
		if st == nil {
			continue
		}
		if _, exists := gm.groups[st.ID]; exists {
			log.Printf("group %s: hydrate skipped, ID already tracked", st.ID)
			continue
		}

		var rehydratedErr error
		if !isTerminalGroupStatus(st.Status) {
			st.Status = StatusError
			rehydratedErr = fmt.Errorf("group %s: terminated by process restart", st.ID)
		}

		mg := &managedGroup{
			state:      st,
			originCh:   st.OriginChannel,
			originChat: st.OriginChatID,
			cancel:     func() {}, // inert: there is no context to cancel
			done:       make(chan struct{}),
			err:        rehydratedErr,
			finishedAt: st.UpdatedAt,
		}
		if mg.finishedAt.IsZero() {
			// Unknown end time: age the group from now so it still stays
			// visible for one retention window instead of expiring instantly.
			mg.finishedAt = now
		}
		mg.result = gm.synthesisLocked(mg)
		close(mg.done)

		gm.groups[st.ID] = mg
		loaded++
	}

	return loaded, nil
}

// SetEnabledHook installs a feature-gate predicate consulted by Start. When
// the hook returns false, Start refuses with ErrGroupsDisabled. Passing nil
// restores the default (always allowed), which is what existing tests rely on
// when they construct managers directly. The host (pkg/agent) injects the real
// config-backed predicate (B10: single gating point).
func (gm *GroupManager) SetEnabledHook(fn func() bool) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.enabled = fn
}

// ErrGroupsDisabled is returned by Start when the groups feature is disabled
// by configuration.
var ErrGroupsDisabled = errors.New("group start: groups feature is disabled (set groups.enabled = true in config)")

// featureEnabled reports the current value of the injected feature gate.
// nil hook → allowed (default, keeps direct-construction tests working).
func (gm *GroupManager) featureEnabled() bool {
	gm.mu.Lock()
	fn := gm.enabled
	gm.mu.Unlock()
	return fn == nil || fn()
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
	// Feature gate (B10): every path that can start a group (group_chat tool,
	// /group start, internal callers) funnels through here, so a single check
	// makes it structurally impossible to run a group while the feature is off.
	if !gm.featureEnabled() {
		return "", ErrGroupsDisabled
	}

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

	// A moderator/aggregator that is not among the participants could never
	// speak, so the strategies that rely on it would silently degrade: MoA
	// falls back to participants[0] as its aggregator and returns the last
	// proposer's raw turn as the "synthesis". config.GroupProfile.Validate()
	// already enforces this rule for profiles; enforce it here as well so the
	// ad-hoc paths (group_chat tool, /group start) get the same guarantee.
	if opts.Moderator != "" {
		found := false
		for _, p := range participants {
			if p.AgentID == opts.Moderator {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("group start: moderator %q is not in participants list", opts.Moderator)
		}
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
	// Lazy sweep (B6): before admitting a new group, drop finished groups whose
	// retention window has elapsed. Start is the write path, so this also bounds
	// the map for long-lived processes that read it rarely.
	gm.evictExpiredLocked(now)
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

// finalize is the single terminal exit path for a group. Exactly one call per
// managedGroup takes effect (guarded by finalizeOnce), and it always emits the
// same pair of client-facing signals: one terminal group.status followed by one
// group.complete, then closes mg.done to release Wait.
//
// status must be one of the terminal statuses (done | stopped | error); err is
// recorded when non-nil and is what Wait returns. The synthesis is computed
// here (not by the caller) so every path — including early errors and panics
// that never reached the post-loop code — reports a consistent result.
//
// Callers must NOT hold gm.mu.
func (gm *GroupManager) finalize(mg *managedGroup, status string, err error) {
	mg.finalizeOnce.Do(func() {
		// Close done unconditionally, even if a publisher panics, so Wait can
		// never hang on a half-completed finalize.
		defer close(mg.done)

		gm.mu.Lock()
		if status == StatusStopped || status == StatusError || status == StatusDone {
			mg.state.Status = status
			mg.state.UpdatedAt = time.Now()
			// Stamp the terminal instant once; it anchors the retention window
			// evaluated by evictExpiredLocked.
			if mg.finishedAt.IsZero() {
				mg.finishedAt = mg.state.UpdatedAt
			}
		}
		if err != nil {
			mg.err = err
		}
		mg.result = gm.synthesisLocked(mg)
		agentIDs := gm.agentIDs(mg)
		gm.mu.Unlock()

		gm.publishGroupStatus(mg, status, agentIDs)
		gm.publishGroupComplete(mg)
	})
}

// Stop requests that an active group stop. It only cancels the group's context
// and returns; the run loop observes the cancellation and emits the terminal
// signal pair (group.status=stopped + group.complete) through finalize.
// Returns true if the group was found.
func (gm *GroupManager) Stop(groupID string) bool {
	gm.mu.Lock()
	mg, ok := gm.groups[groupID]
	gm.mu.Unlock()
	if !ok {
		return false
	}

	mg.cancel()
	return true
}

// Status returns an immutable snapshot of the GroupState and true if the
// group exists. The returned pointer is a deep copy safe to read without
// holding the manager lock. A finished group stops being reported here once
// its retention window has elapsed.
func (gm *GroupManager) Status(groupID string) (*GroupState, bool) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.evictExpiredLocked(time.Now())
	mg, ok := gm.groups[groupID]
	if !ok {
		return nil, false
	}
	return mg.state.Snapshot(), true
}

// List returns a snapshot of the GroupState for every tracked group
// (active and finished). Each entry is a deep copy safe to read without
// holding the manager lock.
//
// Retention (B6): a finished group (done | stopped | error) keeps appearing
// here until GroupManager.retention (DefaultGroupRetention when unset) has
// elapsed since it closed — that grace window is what lets the WebSocket
// welcome payload and "/group list" still show a just-terminated group — and
// is then dropped from memory. Running groups are never evicted.
func (gm *GroupManager) List() []*GroupState {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.evictExpiredLocked(time.Now())
	out := make([]*GroupState, 0, len(gm.groups))
	for _, mg := range gm.groups {
		out = append(out, mg.state.Snapshot())
	}
	return out
}

// AllSnapshots returns a GroupSnapshot for every tracked group (active and
// finished). The caller gets a snapshot-safe copy; it does not share backing
// arrays with the live state. The same retention rule as List applies, so
// expired finished groups are absent here too.
func (gm *GroupManager) AllSnapshots() []GroupSnapshot {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.evictExpiredLocked(time.Now())
	out := make([]GroupSnapshot, 0, len(gm.groups))
	for _, mg := range gm.groups {
		out = append(out, BuildSnapshot(mg.state, mg.result))
	}
	return out
}

// Wait blocks until the group finishes and returns the final synthesis.
// Returns an error if the group ended with an error.
//
// Retention: waiting on a group that finished but is still inside its
// retention window keeps working (the lifecycle tests rely on it); once the
// window has elapsed the group is swept here like everywhere else and Wait
// fails fast with "not found or evicted" instead of returning state nobody can
// see any more. Every real caller (the group_chat tool, /group flows) waits
// inline right after Start, long before retention can elapse, so no live flow
// can hit that error.
func (gm *GroupManager) Wait(groupID string) (string, error) {
	gm.mu.Lock()
	gm.evictExpiredLocked(time.Now())
	mg, ok := gm.groups[groupID]
	gm.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("group wait: group %q not found or evicted", groupID)
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

// agentIDs returns the agent IDs of the group's participants. Participants is
// immutable after Start (the slice is never mutated once the state is built),
// so this is safe to call without the lock.
func (gm *GroupManager) agentIDs(mg *managedGroup) []string {
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
