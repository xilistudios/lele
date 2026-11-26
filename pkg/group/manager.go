package group

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/store"
)

const DefaultGroupRounds = 2

const MaxGroupTurnsCeiling = 50

const DefaultGroupRetention = 30 * time.Minute

type GroupOptions struct {
	Rounds           int
	MaxTurns         int
	Parallel         bool
	Moderator        string
	StopKeywords     []string
	MaxTokensPerTurn int
	TotalTokenBudget int
	OriginSenderID   string
}

type managedGroup struct {
	state        *GroupState
	originCh     string
	originChat   string
	originSender string
	cancel       context.CancelFunc
	result       string
	done         chan struct{}
	err          error
	finishedAt   time.Time

	finalizeOnce sync.Once

	runWg sync.WaitGroup
}

type GroupManager struct {
	mu               sync.Mutex
	groups           map[string]*managedGroup
	resolve          ResolveAgentFunc
	executor         TurnExecutor
	publish          Publisher
	moderatorDecider ModeratorDecider
	storeDir         string
	enabled          func() bool
	retention        time.Duration
	resolveSession   func(string) string
}

func NewGroupManager(resolve ResolveAgentFunc, executor TurnExecutor, publish Publisher) *GroupManager {
	return &GroupManager{
		groups:   make(map[string]*managedGroup),
		resolve:  resolve,
		executor: executor,
		publish:  publish,
	}
}

func (gm *GroupManager) SetStoreDir(dir string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.storeDir = dir
}

func (gm *GroupManager) SetStore(repo *store.GroupRepo) {
	UseStore(repo)
}

func (gm *GroupManager) SetModeratorDecider(d ModeratorDecider) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.moderatorDecider = d
}

func (gm *GroupManager) SetRetention(d time.Duration) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.retention = d
}

func (gm *GroupManager) retentionLocked() time.Duration {
	if gm.retention > 0 {
		return gm.retention
	}
	return DefaultGroupRetention
}

func isTerminalGroupStatus(status string) bool {
	return status == StatusDone || status == StatusStopped || status == StatusError
}

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
		select {
		case <-mg.done:
		default:
			continue
		}
		delete(gm.groups, id)
	}
}

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
			state:        st,
			originCh:     st.OriginChannel,
			originChat:   st.OriginChatID,
			originSender: st.OriginSenderID,
			cancel:       func() {},
			done:         make(chan struct{}),
			err:          rehydratedErr,
			finishedAt:   st.UpdatedAt,
		}
		if mg.finishedAt.IsZero() {
			mg.finishedAt = now
		}
		mg.result = gm.synthesisLocked(mg)
		close(mg.done)

		gm.groups[st.ID] = mg
		loaded++
	}

	return loaded, nil
}

func (gm *GroupManager) SetEnabledHook(fn func() bool) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.enabled = fn
}

func (gm *GroupManager) SetSessionAliasResolver(fn func(chatID string) string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.resolveSession = fn
}

var ErrGroupsDisabled = errors.New("group start: groups feature is disabled (set groups.enabled = true in config)")

func (gm *GroupManager) featureEnabled() bool {
	gm.mu.Lock()
	fn := gm.enabled
	gm.mu.Unlock()
	return fn == nil || fn()
}

func (gm *GroupManager) Start(
	ctx context.Context,
	groupID, profileID, task, strategy string,
	participants []Participant,
	opts GroupOptions,
	originChannel, originChatID string,
) (string, error) {
	if !gm.featureEnabled() {
		return "", ErrGroupsDisabled
	}

	if _, err := NewStrategy(strategy); err != nil {
		return "", fmt.Errorf("group start: %w", err)
	}

	if len(participants) == 0 {
		return "", fmt.Errorf("group start: at least one participant required")
	}

	agentIDs := make([]string, 0, len(participants))
	for _, p := range participants {
		if _, ok := gm.resolve(p.AgentID); !ok {
			return "", fmt.Errorf("group start: participant %q not found", p.AgentID)
		}
		agentIDs = append(agentIDs, p.AgentID)
	}

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

	rounds := opts.Rounds
	maxTurns := opts.MaxTurns
	appliedDefault := false

	if maxTurns == 0 && rounds == 0 {
		rounds = DefaultGroupRounds
		appliedDefault = true
	}

	if maxTurns == 0 && rounds > 0 {
		maxTurns = rounds * len(participants)
	}

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
		OriginSenderID:   opts.OriginSenderID,
	}

	gctx, cancel := context.WithCancel(ctx)

	mg := &managedGroup{
		state:        state,
		originCh:     originChannel,
		originChat:   originChatID,
		originSender: opts.OriginSenderID,
		cancel:       cancel,
		done:         make(chan struct{}),
	}

	gm.mu.Lock()
	gm.evictExpiredLocked(now)
	if _, exists := gm.groups[groupID]; exists {
		gm.mu.Unlock()
		cancel()
		return "", fmt.Errorf("group start: group %q already exists", groupID)
	}
	gm.groups[groupID] = mg
	gm.mu.Unlock()

	gm.publishGroupStatus(mg, "started", agentIDs)

	gm.saveStateBestEffort(mg)

	mg.runWg.Add(1)
	go func() {
		defer mg.runWg.Done()
		gm.runGroup(gctx, mg)
	}()

	return groupID, nil
}

func (gm *GroupManager) finalize(mg *managedGroup, status string, err error) {
	mg.finalizeOnce.Do(func() {
		defer close(mg.done)

		gm.mu.Lock()
		if status == StatusStopped || status == StatusError || status == StatusDone {
			mg.state.Status = status
			mg.state.UpdatedAt = time.Now()
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

		gm.saveStateBestEffort(mg)

		gm.publishGroupStatus(mg, status, agentIDs)
		gm.publishGroupComplete(mg)
	})
}

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

func (gm *GroupManager) DrainRunGoroutine(groupID string) {
	gm.mu.Lock()
	mg, ok := gm.groups[groupID]
	gm.mu.Unlock()
	if !ok {
		return
	}
	mg.runWg.Wait()
}

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

func (gm *GroupManager) SnapshotsForSession(sessionKey string) []GroupSnapshot {
	type sessionEntry struct {
		snap      GroupSnapshot
		updatedAt time.Time
	}

	gm.mu.Lock()
	gm.evictExpiredLocked(time.Now())
	dir := gm.storeDir
	resolve := gm.resolveSession
	entries := make([]sessionEntry, 0, len(gm.groups))
	seen := make(map[string]bool, len(gm.groups))
	for _, mg := range gm.groups {
		seen[mg.state.ID] = true
		entries = append(entries, sessionEntry{
			snap:      BuildSnapshot(mg.state, mg.result),
			updatedAt: mg.state.UpdatedAt,
		})
	}
	gm.mu.Unlock()

	var persisted []*GroupState
	if dir != "" || getGroupRepo() != nil {
		states, err := ListGroups(dir)
		if err != nil {
			log.Printf("group: session history read failed, serving memory only: %v", err)
		} else {
			persisted = states
		}
	}

	matches := func(originChatID string) bool {
		if originChatID == "" || sessionKey == "" {
			return false
		}
		if originChatID == sessionKey {
			return true
		}
		if resolve == nil {
			return false
		}
		return resolve(originChatID) == sessionKey
	}

	out := entries[:0]
	for _, e := range entries {
		if matches(e.snap.OriginChatID) {
			out = append(out, e)
		}
	}

	for _, st := range persisted {
		if st == nil || seen[st.ID] || !matches(st.OriginChatID) {
			continue
		}
		seen[st.ID] = true

		synthesis := synthesisFor(st)
		if !isTerminalGroupStatus(st.Status) {
			st.Status = StatusError
			synthesis = fmt.Sprintf("group %s: terminated by process restart", st.ID)
		}
		out = append(out, sessionEntry{snap: BuildSnapshot(st, synthesis), updatedAt: st.UpdatedAt})
	}

	sort.Slice(out, func(i, j int) bool {
		return groupSortsBefore(out[i].snap.GroupID, out[i].updatedAt,
			out[j].snap.GroupID, out[j].updatedAt)
	})

	snapshots := make([]GroupSnapshot, len(out))
	for i, e := range out {
		snapshots[i] = e.snap
	}
	return snapshots
}

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

func (gm *GroupManager) DrainAllRunGoroutines(grace time.Duration) bool {
	gm.mu.Lock()
	groups := make([]*managedGroup, 0, len(gm.groups))
	for _, mg := range gm.groups {
		groups = append(groups, mg)
	}
	gm.mu.Unlock()

	if len(groups) == 0 {
		return true
	}

	done := make(chan struct{})
	go func() {
		for _, mg := range groups {
			mg.runWg.Wait()
		}
		close(done)
	}()

	if grace <= 0 {
		<-done
		return true
	}

	select {
	case <-done:
		return true
	case <-time.After(grace):
		return false
	}
}

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

func (gm *GroupManager) StopByAgent(agentID string) int {
	if agentID == "" {
		return 0
	}

	gm.mu.Lock()
	var toStop []string
	for _, mg := range gm.groups {
		if mg.state.Status != StatusRunning {
			continue
		}
		if groupIncludesAgent(mg.state, agentID) {
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

func groupIncludesAgent(state *GroupState, agentID string) bool {
	if state == nil {
		return false
	}
	if state.Moderator == agentID {
		return true
	}
	for _, p := range state.Participants {
		if p.AgentID == agentID {
			return true
		}
	}
	return false
}

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

func (gm *GroupManager) agentIDs(mg *managedGroup) []string {
	ids := make([]string, len(mg.state.Participants))
	for i, p := range mg.state.Participants {
		ids[i] = p.AgentID
	}
	return ids
}

func (gm *GroupManager) saveStateBestEffort(mg *managedGroup) {
	gm.mu.Lock()
	dir := gm.storeDir
	gm.mu.Unlock()
	if dir == "" && getGroupRepo() == nil {
		return
	}
	if err := SaveGroup(dir, mg.state); err != nil {
		log.Printf("group %s: failed to persist state: %v", mg.state.ID, err)
	}
}
