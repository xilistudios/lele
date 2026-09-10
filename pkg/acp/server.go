package acp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Config configures the ACP HTTP surface.
type Config struct {
	// Enabled gates route registration. When false, Register is a no-op.
	Enabled bool
	// Token, when non-empty, requires Authorization: Bearer <token>.
	Token string
	// AgentName is an optional label reported on /ping (e.g. "lele").
	AgentName string
}

// Server exposes the ACP REST API.
type Server struct {
	cfg     Config
	manager *RunManager
}

// NewServer builds an ACP server around a run manager.
func NewServer(cfg Config, manager *RunManager) *Server {
	return &Server{cfg: cfg, manager: manager}
}

// Manager returns the underlying run manager.
func (s *Server) Manager() *RunManager { return s.manager }

// Register wires ACP routes onto mux. Paths match the ACP OpenAPI 0.2.0
// root (not /api/v1).
func (s *Server) Register(mux *http.ServeMux) {
	if s == nil || !s.cfg.Enabled {
		return
	}
	mux.HandleFunc("GET /ping", s.auth(s.handlePing))
	mux.HandleFunc("GET /agents", s.auth(s.handleListAgents))
	mux.HandleFunc("GET /agents/{name}", s.auth(s.handleGetAgent))
	mux.HandleFunc("POST /runs", s.auth(s.handleCreateRun))
	mux.HandleFunc("GET /runs/{run_id}", s.auth(s.handleGetRun))
	mux.HandleFunc("POST /runs/{run_id}", s.auth(s.handleResumeRun))
	mux.HandleFunc("POST /runs/{run_id}/cancel", s.auth(s.handleCancelRun))
	mux.HandleFunc("GET /runs/{run_id}/events", s.auth(s.handleListEvents))
	mux.HandleFunc("GET /session/{session_id}", s.auth(s.handleGetSession))
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token == "" {
			next(w, r)
			return
		}
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) || strings.TrimSpace(h[len(prefix):]) != s.cfg.Token {
			writeACPErr(w, http.StatusUnauthorized, &Error{
				Code:    ErrCodeServerError,
				Message: "unauthorized",
			})
			return
		}
		next(w, r)
	}
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, PingResponse{
		Protocol: "acp",
		Version:  ProtocolVersion,
		Agent:    s.cfg.AgentName,
	})
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	infos := s.manager.bridge.ListAgents()
	limit := 10
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	agents := make([]AgentManifest, 0, len(infos))
	for _, info := range infos {
		agents = append(agents, manifestFromInfo(info))
	}
	if offset >= len(agents) {
		agents = []AgentManifest{}
	} else {
		end := offset + limit
		if end > len(agents) {
			end = len(agents)
		}
		agents = agents[offset:end]
	}
	writeJSON(w, http.StatusOK, AgentsListResponse{Agents: agents})
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	info, ok := s.manager.bridge.GetAgent(name)
	if !ok {
		writeACPErr(w, http.StatusNotFound, &Error{
			Code:    ErrCodeNotFound,
			Message: "agent not found: " + name,
		})
		return
	}
	writeJSON(w, http.StatusOK, manifestFromInfo(info))
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req RunCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeACPErr(w, http.StatusBadRequest, &Error{
			Code:    ErrCodeInvalidInput,
			Message: "invalid JSON body: " + err.Error(),
		})
		return
	}
	if req.Mode == "" {
		req.Mode = ModeSync
	}
	// Client disconnect should not kill a long sync/stream run immediately
	// after accept; use background + explicit cancel.
	run, createdEvent, apiErr := s.manager.CreateRun(r.Context(), &req)
	if apiErr != nil {
		status := http.StatusBadRequest
		switch apiErr.Code {
		case ErrCodeNotFound:
			status = http.StatusNotFound
		case ErrCodeServerError:
			status = http.StatusInternalServerError
		}
		writeACPErr(w, status, apiErr)
		return
	}

	mode, _ := NormalizeMode(req.Mode)
	switch mode {
	case ModeAsync:
		writeJSON(w, http.StatusAccepted, run)
	case ModeStream:
		s.streamRun(w, r, run.RunID, createdEvent)
	default: // sync
		ctx := r.Context()
		if err := s.manager.WaitDone(ctx, run.RunID); err != nil {
			writeACPErr(w, http.StatusInternalServerError, err)
			return
		}
		final, gerr := s.manager.GetRun(run.RunID)
		if gerr != nil {
			writeACPErr(w, http.StatusInternalServerError, gerr)
			return
		}
		writeJSON(w, http.StatusOK, final)
	}
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	run, apiErr := s.manager.GetRun(runID)
	if apiErr != nil {
		writeACPErr(w, http.StatusNotFound, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleResumeRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	run, apiErr := s.manager.GetRun(runID)
	if apiErr != nil {
		writeACPErr(w, http.StatusNotFound, apiErr)
		return
	}
	// ACP resume is only meaningful for awaiting runs. Lele does not yet
	// pause runs into awaiting, so completed/failed runs error and
	// in-progress runs are returned as-is (idempotent observe).
	switch run.Status {
	case RunStatusAwaiting:
		writeACPErr(w, http.StatusNotImplemented, &Error{
			Code:    ErrCodeServerError,
			Message: "await/resume is not implemented by this agent host",
		})
	case RunStatusInProgress, RunStatusCreated, RunStatusCancelling:
		writeJSON(w, http.StatusOK, run)
	default:
		writeACPErr(w, http.StatusBadRequest, &Error{
			Code:    ErrCodeInvalidInput,
			Message: "run is already terminal: " + run.Status,
		})
	}
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	run, apiErr := s.manager.CancelRun(runID)
	if apiErr != nil {
		writeACPErr(w, http.StatusNotFound, apiErr)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	events, apiErr := s.manager.Events(runID)
	if apiErr != nil {
		writeACPErr(w, http.StatusNotFound, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, RunEventsListResponse{Events: events})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("session_id")
	sess, apiErr := s.manager.SessionByID(id)
	if apiErr != nil {
		writeACPErr(w, http.StatusNotFound, apiErr)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// streamRun writes an SSE stream of ACP events until the run finishes.
func (s *Server) streamRun(w http.ResponseWriter, r *http.Request, runID string, created *Event) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeACPErr(w, http.StatusInternalServerError, &Error{
			Code:    ErrCodeServerError,
			Message: "streaming unsupported",
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsub, apiErr := s.manager.Subscribe(runID)
	if apiErr != nil {
		writeSSE(w, flusher, Event{Type: EventError, Error: apiErr})
		return
	}
	defer unsub()

	// Subscribe already replays history including run.created.
	// Keep a keepalive ticker so idle in-flight runs do not trip proxies.
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			writeSSE(w, flusher, ev)
			if isTerminalEvent(ev.Type) {
				// Drain any remaining buffered events quickly.
				for {
					select {
					case more, open2 := <-ch:
						if !open2 {
							return
						}
						writeSSE(w, flusher, more)
					default:
						return
					}
				}
			}
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func isTerminalEvent(t string) bool {
	switch t {
	case EventRunCompleted, EventRunFailed, EventRunCancelled, EventError:
		return true
	default:
		return false
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, ev Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\n", ev.Type)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeACPErr(w http.ResponseWriter, status int, e *Error) {
	writeJSON(w, status, e)
}

func manifestFromInfo(info AgentInfo) AgentManifest {
	input := []string{"text/plain", "text/markdown", "application/json"}
	output := []string{"text/plain", "text/markdown"}
	if info.SupportsImages {
		input = append(input, "image/*")
	}
	name := info.Name
	if name == "" || !ValidateAgentName(name) {
		name = SanitizeAgentName(info.ID)
		if name == "" {
			name = "agent"
		}
	}
	desc := info.Description
	if desc == "" {
		desc = "Lele agent " + info.ID
	}
	caps := []Capability{
		{Name: "Conversational AI", Description: "Multi-turn chat with tools and workspace context."},
	}
	meta := &Metadata{
		Framework:           "lele",
		ProgrammingLanguage: "Go",
		NaturalLanguages:    []string{"en", "es", "zh", "ja", "pt", "fr", "vi"},
		Capabilities:        caps,
		Tags:                []string{"Chat"},
		License:             "MIT",
	}
	if info.Model != "" {
		meta.RecommendedModels = []string{info.Model}
	}
	if info.ID != "" {
		meta.Annotations = map[string]interface{}{
			"lele_agent_id": info.ID,
		}
	}
	return AgentManifest{
		Name:               name,
		Description:        desc,
		InputContentTypes:  input,
		OutputContentTypes: output,
		Metadata:           meta,
	}
}
