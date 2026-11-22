package channels

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/mcp"
)

const (
	mcpAuthFlowTTL = 5 * time.Minute

	mcpAuthFlowIDLen = 32
)

var startMCPAuthFlow = mcp.StartAuth

var errMCPAuthFlowNotFound = errors.New("mcp auth flow not found")

type mcpAuthEntry struct {
	agentID      string
	name         string
	flowID       string
	serverConfig mcp.ServerConfig
	flow         *mcp.AuthFlow
	deadline     time.Time
	reloaded     bool
}

func (e *mcpAuthEntry) ServerConfig() mcp.ServerConfig { return e.serverConfig }

type mcpAuthFlows struct {
	mu         sync.Mutex
	entries    map[string]*mcpAuthEntry
	pairs      map[string]string
	startLocks map[string]*pairStartLock
	storeFor   func(agentID string) *mcp.AuthStore
}

type pairStartLock struct {
	mu   sync.Mutex
	refs int
}

func newMCPAuthFlows(storeFor func(agentID string) *mcp.AuthStore) *mcpAuthFlows {
	return &mcpAuthFlows{
		entries:    make(map[string]*mcpAuthEntry),
		pairs:      make(map[string]string),
		startLocks: make(map[string]*pairStartLock),
		storeFor:   storeFor,
	}
}

func (r *mcpAuthFlows) lockPair(key string) func() {
	r.mu.Lock()
	l := r.startLocks[key]
	if l == nil {
		l = &pairStartLock{}
		r.startLocks[key] = l
	}
	l.refs++
	r.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		r.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(r.startLocks, key)
		}
		r.mu.Unlock()
	}
}

func mcpAuthPairKey(agentID, name string) string { return agentID + "\x00" + name }

func isValidMCPFlowID(s string) bool {
	if len(s) != mcpAuthFlowIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (r *mcpAuthFlows) sweepLocked(now time.Time) []*mcp.AuthFlow {
	var expired []*mcp.AuthFlow
	for id, e := range r.entries {
		if now.Before(e.deadline) {
			continue
		}
		delete(r.entries, id)
		key := mcpAuthPairKey(e.agentID, e.name)
		if cur, ok := r.pairs[key]; ok && cur == id {
			delete(r.pairs, key)
		}
		expired = append(expired, e.flow)
	}
	return expired
}

func cancelFlows(flows []*mcp.AuthFlow) {
	for _, f := range flows {
		if f != nil {
			_ = f.Cancel()
		}
	}
}

func (r *mcpAuthFlows) Start(ctx context.Context, agentID, name string, srv mcp.ServerConfig) (*mcp.AuthFlow, string, time.Time, error) {
	key := mcpAuthPairKey(agentID, name)
	unlock := r.lockPair(key)
	defer unlock()

	r.mu.Lock()
	expired := r.sweepLocked(time.Now())
	var released *mcp.AuthFlow
	if id, ok := r.pairs[key]; ok {
		if prev := r.entries[id]; prev != nil && !prev.flow.Done() {
			delete(r.entries, id)
			delete(r.pairs, key)
			released = prev.flow
		}
	}
	r.mu.Unlock()
	cancelFlows(append(expired, released))

	var store *mcp.AuthStore
	if r.storeFor != nil {
		store = r.storeFor(agentID)
	}
	flow, err := startMCPAuthFlow(ctx, name, srv, store, mcp.AuthOptions{})
	if err != nil {
		return nil, "", time.Time{}, err
	}

	id := generateID()
	deadline := time.Now().Add(mcpAuthFlowTTL)
	entry := &mcpAuthEntry{
		agentID:      agentID,
		name:         name,
		flowID:       id,
		serverConfig: srv,
		flow:         flow,
		deadline:     deadline,
	}

	r.mu.Lock()
	for r.entries[id] != nil {
		id = generateID()
		entry.flowID = id
	}
	var replaced *mcp.AuthFlow
	if prevID, ok := r.pairs[key]; ok {
		if prev := r.entries[prevID]; prev != nil {
			replaced = prev.flow
		}
		delete(r.entries, prevID)
		delete(r.pairs, key)
	}
	r.entries[id] = entry
	r.pairs[key] = id
	r.mu.Unlock()

	cancelFlows([]*mcp.AuthFlow{replaced})
	return flow, id, deadline, nil
}

func (r *mcpAuthFlows) Get(agentID, flowID string) (*mcpAuthEntry, bool) {
	r.mu.Lock()
	expired := r.sweepLocked(time.Now())
	e, ok := r.entries[flowID]
	if ok && e.agentID != agentID {
		e, ok = nil, false
	}
	r.mu.Unlock()
	cancelFlows(expired)
	if !ok {
		return nil, false
	}
	return e, true
}

func (r *mcpAuthFlows) Complete(ctx context.Context, agentID, flowID, code, state string) error {
	e, ok := r.Get(agentID, flowID)
	if !ok {
		return errMCPAuthFlowNotFound
	}
	return e.flow.Submit(ctx, code, state)
}

func (r *mcpAuthFlows) Cancel(agentID, flowID string) error {
	e, ok := r.Get(agentID, flowID)
	if !ok {
		return errMCPAuthFlowNotFound
	}
	return e.flow.Cancel()
}

func (r *mcpAuthFlows) MarkReloaded(agentID, flowID string) bool {
	r.mu.Lock()
	expired := r.sweepLocked(time.Now())
	e := r.entries[flowID]
	first := false
	if e != nil && e.agentID == agentID && !e.reloaded {
		state, _ := e.flow.Status()
		first = state == mcpAuthStateDone
	}
	if first {
		e.reloaded = true
	}
	r.mu.Unlock()
	cancelFlows(expired)
	return first
}

func (r *mcpAuthFlows) CancelPair(agentID, name string) error {
	key := mcpAuthPairKey(agentID, name)
	r.mu.Lock()
	expired := r.sweepLocked(time.Now())
	id := r.pairs[key]
	entry := r.entries[id]
	r.mu.Unlock()
	cancelFlows(expired)
	if entry == nil {
		return nil
	}
	return entry.flow.Cancel()
}

func (n *NativeChannel) mcpAuthFlowRegistry() *mcpAuthFlows {
	n.mcpAuthFlowsOnce.Do(func() {
		n.mcpAuthFlows = newMCPAuthFlows(n.mcpAuthStoreFor)
	})
	return n.mcpAuthFlows
}

func (n *NativeChannel) mcpAuthStoreFor(agentID string) *mcp.AuthStore {
	source, ok := n.agentLoop.(mcpPathsSource)
	if !ok {
		return nil
	}
	paths, ok := source.MCPPathsFor(agentID)
	if !ok {
		return nil
	}
	return mcp.NewAuthStore(paths.LeleDir)
}
