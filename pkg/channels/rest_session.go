package channels

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (n *NativeChannel) handleSessionModel(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	if r.Method == http.MethodGet {
		agentID := n.agentLoop.GetSessionAgent(sessionKey)
		models := n.listAllModels()
		writeJSON(w, http.StatusOK, SessionModelResponse{
			SessionKey:  sessionKey,
			AgentID:     agentID,
			Model:       n.agentLoop.GetSessionModel(sessionKey),
			Models:      models,
			ModelGroups: n.buildModelGroups(agentID, models),
		})
		return
	}

	var req SessionModelUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		writeError(w, http.StatusBadRequest, "model is required", "model_missing")
		return
	}

	agentID := n.agentLoop.GetSessionAgent(sessionKey)
	models := n.listAllModels()
	writeJSON(w, http.StatusOK, SessionModelResponse{
		SessionKey:  sessionKey,
		AgentID:     agentID,
		Model:       n.agentLoop.SetSessionModel(sessionKey, req.Model),
		Models:      models,
		ModelGroups: n.buildModelGroups(agentID, models),
	})
}

func (n *NativeChannel) handleSessionAgent(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, SessionAgentResponse{
			SessionKey: sessionKey,
			AgentID:    n.agentLoop.GetSessionAgent(sessionKey),
		})
		return
	}

	var req SessionAgentUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required", "agent_id_missing")
		return
	}

	n.agentLoop.SetSessionAgent(sessionKey, req.AgentID)
	writeJSON(w, http.StatusOK, SessionAgentResponse{
		SessionKey: sessionKey,
		AgentID:    n.agentLoop.GetSessionAgent(sessionKey),
	})
}

func (n *NativeChannel) handleSessionThinking(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, SessionThinkingResponse{
			SessionKey: sessionKey,
			Level:      n.agentLoop.GetThinkLevel(sessionKey),
		})
		return
	}

	var req SessionThinkingUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}
	if !n.agentLoop.SetThinkLevel(sessionKey, req.Level) {
		writeError(w, http.StatusBadRequest, "invalid level (valid: off, low, medium, high)", "level_invalid")
		return
	}

	writeJSON(w, http.StatusOK, SessionThinkingResponse{
		SessionKey: sessionKey,
		Level:      n.agentLoop.GetThinkLevel(sessionKey),
	})
}

func (n *NativeChannel) handleSessionName(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, SessionNameResponse{
			SessionKey: sessionKey,
			Name:       n.agentLoop.GetName(sessionKey),
		})
		return
	}

	var req SessionNameUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required", "name_missing")
		return
	}

	if err := n.agentLoop.SetName(sessionKey, req.Name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "name_update_failed")
		return
	}

	writeJSON(w, http.StatusOK, SessionNameResponse{
		SessionKey: sessionKey,
		Name:       n.agentLoop.GetName(sessionKey),
	})
}

func (n *NativeChannel) handleSessionContext(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	currentTokens, contextWindow := n.agentLoop.GetCurrentContextUsage(sessionKey)
	cumInputTokens, cumOutputTokens, _ := n.agentLoop.GetTokenCounts(sessionKey)
	var usagePercent float64
	if contextWindow > 0 {
		usagePercent = float64(currentTokens) / float64(contextWindow) * 100.0
		if usagePercent > 100 {
			usagePercent = 100
		}
	}

	writeJSON(w, http.StatusOK, SessionContextResponse{
		SessionKey:             sessionKey,
		InputTokens:            currentTokens,
		TotalTokens:            currentTokens,
		CumulativeInputTokens:  cumInputTokens,
		CumulativeOutputTokens: cumOutputTokens,
		CumulativeTotalTokens:  cumInputTokens + cumOutputTokens,
		ContextWindow:          contextWindow,
		UsagePercent:           usagePercent,
	})
}

func (n *NativeChannel) handleSessionSummary(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	summary := n.agentLoop.GetSessionSummary(sessionKey)

	writeJSON(w, http.StatusOK, SessionSummaryResponse{
		SessionKey: sessionKey,
		Summary:    summary,
	})
}

func (n *NativeChannel) handleSessionCompact(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	result := n.agentLoop.CompactSession(sessionKey)
	writeJSON(w, http.StatusOK, map[string]string{"result": result})
}
