package acp

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// AgentInfo is the minimal agent description the ACP layer needs.
type AgentInfo struct {
	ID             string
	Name           string
	Description    string
	Model          string
	SupportsImages bool
}

// AgentBridge adapts lele's agent runtime to the ACP protocol.
// Implementations live outside this package (gateway wiring) so pkg/acp
// stays free of provider/tool dependencies.
type AgentBridge interface {
	// ListAgents returns every agent exposed over ACP.
	ListAgents() []AgentInfo
	// GetAgent resolves an agent by ACP name (already sanitized).
	GetAgent(name string) (AgentInfo, bool)
	// Process runs the agent for one turn and returns the assistant text.
	// sessionKey is the lele session key derived from the ACP session.
	Process(ctx context.Context, agentName, sessionKey, content string) (string, error)
	// SessionExists reports whether a lele session already has history.
	SessionExists(sessionKey string) bool
}

// RunManager owns run state, cancellation, and event fan-out.
type RunManager struct {
	bridge AgentBridge

	mu        sync.RWMutex
	runs      map[string]*runState
	sessByACP map[string]string // acp session_id -> lele session key
	maxKept   int
	ttl       time.Duration
}

type runState struct {
	mu sync.Mutex

	run      Run
	cancel   context.CancelFunc
	events   []Event
	subs     map[chan Event]struct{}
	done     chan struct{}
	finished bool
}

// NewRunManager creates a manager with a default retention policy.
func NewRunManager(bridge AgentBridge) *RunManager {
	return &RunManager{
		bridge:    bridge,
		runs:      make(map[string]*runState),
		sessByACP: make(map[string]string),
		maxKept:   500,
		ttl:       time.Hour,
	}
}

// SessionKeyFor maps an ACP session id to a lele session key, creating one
// when the id is empty (new session).
func (m *RunManager) SessionKeyFor(sessionID string) (acpID string, sessionKey string) {
	if sessionID != "" {
		m.mu.RLock()
		key, ok := m.sessByACP[sessionID]
		m.mu.RUnlock()
		if ok {
			return sessionID, key
		}
		key = "acp:" + sessionID
		m.mu.Lock()
		m.sessByACP[sessionID] = key
		m.mu.Unlock()
		return sessionID, key
	}
	id := uuid.New().String()
	key := "acp:" + id
	m.mu.Lock()
	m.sessByACP[id] = key
	m.mu.Unlock()
	return id, key
}

// CreateRun validates the request, allocates a run, and starts execution.
func (m *RunManager) CreateRun(ctx context.Context, req *RunCreateRequest) (*Run, *Event, *Error) {
	if req == nil {
		return nil, nil, &Error{Code: ErrCodeInvalidInput, Message: "request body is required"}
	}
	mode, modeErr := NormalizeMode(req.Mode)
	if modeErr != nil {
		return nil, nil, modeErr
	}
	if !ValidateAgentName(req.AgentName) {
		return nil, nil, &Error{Code: ErrCodeInvalidInput, Message: "invalid agent_name"}
	}
	if _, ok := m.bridge.GetAgent(req.AgentName); !ok {
		return nil, nil, &Error{Code: ErrCodeNotFound, Message: "agent not found: " + req.AgentName}
	}
	if len(req.Input) == 0 {
		return nil, nil, &Error{Code: ErrCodeInvalidInput, Message: "input must contain at least one message"}
	}

	sessionID, sessionKey := m.SessionKeyFor(req.SessionID)
	if req.Session != nil && req.Session.ID != "" && sessionID != req.Session.ID {
		sessionID, sessionKey = m.SessionKeyFor(req.Session.ID)
	}

	runID := uuid.New().String()
	now := time.Now().UTC()
	run := Run{
		AgentName: req.AgentName,
		SessionID: sessionID,
		RunID:     runID,
		Status:    RunStatusCreated,
		Output:    []Message{},
		CreatedAt: now,
	}

	// Detach from the HTTP request context so async/stream runs survive
	// client disconnects. Cancellation is explicit via CancelRun.
	_ = ctx
	runCtx, cancel := context.WithCancel(context.Background())
	st := &runState{
		run:    run,
		cancel: cancel,
		subs:   make(map[chan Event]struct{}),
		done:   make(chan struct{}),
	}

	m.mu.Lock()
	m.runs[runID] = st
	m.evictLocked()
	m.mu.Unlock()

	createdEvent := Event{Type: EventRunCreated, Run: runSnapshotPtr(run)}
	m.publish(st, createdEvent)

	go m.execute(runCtx, st, sessionKey, req.Input, mode)

	return runSnapshotPtr(run), &createdEvent, nil
}

func (m *RunManager) execute(ctx context.Context, st *runState, sessionKey string, input []Message, mode string) {
	defer close(st.done)
	defer st.cancel()

	m.setStatus(st, RunStatusInProgress, EventRunInProgress)

	content := InputText(input)
	result, err := m.bridge.Process(ctx, m.agentName(st), sessionKey, content)

	if ctx.Err() != nil && err != nil {
		fin := time.Now().UTC()
		m.mu.Lock()
		st.mu.Lock()
		st.run.Status = RunStatusCancelled
		st.run.FinishedAt = &fin
		st.finished = true
		snap := runSnapshotPtr(st.run)
		st.mu.Unlock()
		m.mu.Unlock()
		m.publish(st, Event{Type: EventRunCancelled, Run: snap})
		return
	}

	if err != nil {
		fin := time.Now().UTC()
		acpErr := &Error{Code: ErrCodeServerError, Message: err.Error()}
		st.mu.Lock()
		st.run.Status = RunStatusFailed
		st.run.Error = acpErr
		st.run.FinishedAt = &fin
		st.finished = true
		snap := runSnapshotPtr(st.run)
		st.mu.Unlock()
		m.publish(st, Event{Type: EventRunFailed, Run: snap})
		return
	}

	msg := MessageFromText("agent/"+m.agentName(st), result)
	now := time.Now().UTC()
	msg.CompletedAt = &now

	// Stream-friendly events first, then the completed run.
	m.publish(st, Event{Type: EventMessageCreated, Message: &msg})
	part := msg.Parts[0]
	m.publish(st, Event{Type: EventMessagePart, Part: &part})
	m.publish(st, Event{Type: EventMessageCompleted, Message: &msg})

	fin := time.Now().UTC()
	st.mu.Lock()
	st.run.Status = RunStatusCompleted
	st.run.Output = append(st.run.Output, msg)
	st.run.FinishedAt = &fin
	st.finished = true
	snap := runSnapshotPtr(st.run)
	st.mu.Unlock()
	m.publish(st, Event{Type: EventRunCompleted, Run: snap})
}

func (m *RunManager) agentName(st *runState) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.run.AgentName
}

func (m *RunManager) setStatus(st *runState, status string, eventType string) {
	st.mu.Lock()
	st.run.Status = status
	snap := runSnapshotPtr(st.run)
	st.mu.Unlock()
	if eventType != "" {
		m.publish(st, Event{Type: eventType, Run: snap})
	}
}

// GetRun returns a snapshot of a run.
func (m *RunManager) GetRun(runID string) (*Run, *Error) {
	m.mu.RLock()
	st, ok := m.runs[runID]
	m.mu.RUnlock()
	if !ok {
		return nil, &Error{Code: ErrCodeNotFound, Message: "run not found"}
	}
	st.mu.Lock()
	snap := runSnapshot(st.run)
	st.mu.Unlock()
	return &snap, nil
}

// CancelRun requests cancellation. Terminal runs are a no-op.
func (m *RunManager) CancelRun(runID string) (*Run, *Error) {
	m.mu.RLock()
	st, ok := m.runs[runID]
	m.mu.RUnlock()
	if !ok {
		return nil, &Error{Code: ErrCodeNotFound, Message: "run not found"}
	}

	st.mu.Lock()
	status := st.run.Status
	st.mu.Unlock()
	switch status {
	case RunStatusCompleted, RunStatusFailed, RunStatusCancelled:
		st.mu.Lock()
		snap := runSnapshot(st.run)
		st.mu.Unlock()
		return &snap, nil
	}

	st.mu.Lock()
	st.run.Status = RunStatusCancelling
	st.mu.Unlock()

	if st.cancel != nil {
		st.cancel()
	}

	// Wait briefly for the worker to settle into cancelled/failed.
	select {
	case <-st.done:
	case <-time.After(2 * time.Second):
	}

	st.mu.Lock()
	snap2 := runSnapshot(st.run)
	st.mu.Unlock()
	return &snap2, nil
}

// Events returns the recorded event list for a run.
func (m *RunManager) Events(runID string) ([]Event, *Error) {
	m.mu.RLock()
	st, ok := m.runs[runID]
	m.mu.RUnlock()
	if !ok {
		return nil, &Error{Code: ErrCodeNotFound, Message: "run not found"}
	}
	st.mu.Lock()
	out := make([]Event, len(st.events))
	copy(out, st.events)
	st.mu.Unlock()
	return out, nil
}

// Subscribe attaches an event channel that receives subsequent events until
// the run finishes or Unsubscribe is called. The channel is buffered.
func (m *RunManager) Subscribe(runID string) (<-chan Event, func(), *Error) {
	m.mu.RLock()
	st, ok := m.runs[runID]
	m.mu.RUnlock()
	if !ok {
		return nil, nil, &Error{Code: ErrCodeNotFound, Message: "run not found"}
	}

	ch := make(chan Event, 256)
	st.mu.Lock()
	st.subs[ch] = struct{}{}
	// Replay history so late subscribers see prior events.
	for _, ev := range st.events {
		select {
		case ch <- ev:
		default:
		}
	}
	finished := st.finished
	st.mu.Unlock()

	unsub := func() {
		st.mu.Lock()
		if _, ok := st.subs[ch]; ok {
			delete(st.subs, ch)
			close(ch)
		}
		st.mu.Unlock()
	}

	if finished {
		// Leave channel open until unsub; caller should close after drain.
		return ch, unsub, nil
	}
	return ch, unsub, nil
}

// WaitDone blocks until the run finishes or ctx is done.
func (m *RunManager) WaitDone(ctx context.Context, runID string) *Error {
	m.mu.RLock()
	st, ok := m.runs[runID]
	m.mu.RUnlock()
	if !ok {
		return &Error{Code: ErrCodeNotFound, Message: "run not found"}
	}
	select {
	case <-st.done:
		return nil
	case <-ctx.Done():
		return &Error{Code: ErrCodeServerError, Message: "context cancelled while waiting for run"}
	}
}

// SessionByID returns the ACP session descriptor when known.
func (m *RunManager) SessionByID(sessionID string) (*Session, *Error) {
	m.mu.RLock()
	key, ok := m.sessByACP[sessionID]
	m.mu.RUnlock()
	if !ok {
		// Still accept acp:<uuid> keys that were created out of band.
		key = "acp:" + sessionID
		if !m.bridge.SessionExists(key) {
			return nil, &Error{Code: ErrCodeNotFound, Message: "session not found"}
		}
	}
	_ = key
	return &Session{
		ID:      sessionID,
		History: []string{},
	}, nil
}

func (m *RunManager) publish(st *runState, ev Event) {
	st.mu.Lock()
	st.events = append(st.events, ev)
	var targets []chan Event
	for ch := range st.subs {
		targets = append(targets, ch)
	}
	st.mu.Unlock()

	for _, ch := range targets {
		select {
		case ch <- ev:
		default:
			// Drop if subscriber is slow; history still holds the event.
		}
	}
}

func (m *RunManager) evictLocked() {
	if len(m.runs) <= m.maxKept {
		return
	}
	cutoff := time.Now().Add(-m.ttl)
	for id, st := range m.runs {
		st.mu.Lock()
		fin := st.run.FinishedAt
		status := st.run.Status
		st.mu.Unlock()
		if fin != nil && fin.Before(cutoff) {
			delete(m.runs, id)
			continue
		}
		// Hard cap: drop oldest finished runs regardless of TTL.
		if len(m.runs) > m.maxKept {
			switch status {
			case RunStatusCompleted, RunStatusFailed, RunStatusCancelled:
				delete(m.runs, id)
			}
		}
	}
}

func runSnapshot(r Run) Run {
	out := r
	if r.Output != nil {
		out.Output = make([]Message, len(r.Output))
		copy(out.Output, r.Output)
	}
	return out
}

func runSnapshotPtr(r Run) *Run {
	s := runSnapshot(r)
	return &s
}
