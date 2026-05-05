package channels

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	lelectx "github.com/xilistudios/lele/pkg/context"
	"github.com/xilistudios/lele/pkg/providers"
)

var subagentIDRegex = regexp.MustCompile(`^subagent-\d+$`)

func (n *NativeChannel) handleGetPIN(w http.ResponseWriter, r *http.Request) {
	deviceName := getQueryParam(r, "device_name")

	pending, err := n.auth.GeneratePIN(deviceName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "pin_error")
		return
	}

	writeJSON(w, http.StatusOK, AuthPINResponse{
		PIN:     pending.PIN,
		Expires: pending.Expires.Format(time.RFC3339),
	})
}

func (n *NativeChannel) handlePair(w http.ResponseWriter, r *http.Request) {
	var req AuthPairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	client, token, refreshToken, err := n.auth.PairWithPIN(req.PIN, req.DeviceName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "pair_error")
		return
	}

	writeJSON(w, http.StatusCreated, AuthPairResponse{
		Token:        token,
		RefreshToken: refreshToken,
		Expires:      client.Expires.Format(time.RFC3339),
		ClientID:     client.ClientID,
	})
}

func (n *NativeChannel) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req AuthRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	client, token, refreshToken, err := n.auth.RefreshToken(req.RefreshToken)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "refresh_error")
		return
	}

	writeJSON(w, http.StatusOK, AuthRefreshResponse{
		Token:        token,
		RefreshToken: refreshToken,
		Expires:      client.Expires.Format(time.RFC3339),
	})
}

func (n *NativeChannel) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		writeJSON(w, http.StatusOK, AuthStatusResponse{Valid: false})
		return
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == authHeader {
		writeJSON(w, http.StatusOK, AuthStatusResponse{Valid: false})
		return
	}

	client, valid := n.auth.ValidateToken(token)
	resp := AuthStatusResponse{Valid: valid}
	if valid && client != nil {
		resp.ClientID = client.ClientID
		resp.DeviceName = client.DeviceName
		resp.Expires = client.Expires.Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (n *NativeChannel) handleChatSend(w http.ResponseWriter, r *http.Request) {
	var req ChatSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required", "content_missing")
		return
	}

	clientID := getClientID(r)
	sessionKey := req.SessionKey
	if sessionKey == "" {
		sessionKey = clientID
	}
	n.auth.TrackSessionKey(clientID, sessionKey)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	if req.AgentID != "" {
		n.agentLoop.SetSessionAgent(sessionKey, req.AgentID)
	}

	messageID := uuid.New().String()

	attachments := n.processAttachments(req.Attachments, sessionKey)

	n.bus.PublishInbound(bus.InboundMessage{
		Channel:     ChannelName,
		SenderID:    clientID,
		ChatID:      sessionKey,
		Content:     req.Content,
		Attachments: attachments,
		SessionKey:  sessionKey,
		Metadata:    map[string]string{"message_id": messageID},
	})

	writeJSON(w, http.StatusCreated, ChatSendResponse{
		MessageID:  messageID,
		SessionKey: sessionKey,
	})
}

func (n *NativeChannel) handleChatHistory(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	subagentID := r.PathValue("subagentId")

	if subagentID != "" {
		if len(subagentID) > 64 {
			writeError(w, http.StatusBadRequest, "subagent id too long", "subagent_id_invalid")
			return
		}
		if !subagentIDRegex.MatchString(subagentID) {
			writeError(w, http.StatusBadRequest, "invalid subagent id format", "subagent_id_invalid")
			return
		}
		if !strings.HasPrefix(sessionKey, "native:") {
			sessionKey = "native:" + sessionKey
		}
		sessionKey = sessionKey + ":" + subagentID
	}

	clientID := getClientID(r)
	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	// Parse pagination params: before_id for cursor-based pagination
	beforeID := getQueryParam(r, "before_id")
	limitStr := getQueryParam(r, "limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	history := n.agentLoop.GetSessionHistory(sessionKey)

	// Build a map of tool_call_id -> tool name from assistant messages
	// This is used to populate ToolName for tool result messages
	toolCallIDToName := make(map[string]string)
	for _, msg := range history {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					// Use tc.Name if available, otherwise try tc.Function.Name
					toolName := tc.Name
					if toolName == "" && tc.Function != nil {
						toolName = tc.Function.Name
					}
					if toolName != "" {
						toolCallIDToName[tc.ID] = toolName
					}
				}
			}
		}
	}

	// Build a list of valid messages (user, assistant, tool) with their IDs
	type indexedMessage struct {
		id  string
		msg providers.Message
	}
	validMessages := make([]indexedMessage, 0, len(history))
	for _, msg := range history {
		if msg.Role != "user" && msg.Role != "assistant" && msg.Role != "tool" {
			continue
		}
		// Generate a stable ID from message content hash (position-independent).
		// This ensures cursor-based pagination survives history mutations (pruning,
		// new messages appended, etc.) because the ID only depends on the message itself.
		hasher := sha256.New()
		hasher.Write([]byte(msg.Role))
		hasher.Write([]byte(msg.Content))
		if msg.ToolCallID != "" {
			hasher.Write([]byte(msg.ToolCallID))
		}
		for _, tc := range msg.ToolCalls {
			hasher.Write([]byte(tc.ID))
			hasher.Write([]byte(tc.Name))
		}
		msgID := fmt.Sprintf("%s:%x", sessionKey, hasher.Sum(nil)[:8])
		validMessages = append(validMessages, indexedMessage{id: msgID, msg: msg})
	}

	// Find the starting point based on before_id cursor
	startIdx := len(validMessages)
	if beforeID != "" {
		for i, vm := range validMessages {
			if vm.id == beforeID {
				startIdx = i
				break
			}
		}
	}

	// Calculate the range to return (messages before the cursor)
	endIdx := startIdx
	if endIdx > len(validMessages) {
		endIdx = len(validMessages)
	}
	resultStartIdx := endIdx - limit
	if resultStartIdx < 0 {
		resultStartIdx = 0
	}

	// Build response messages
	messages := make([]ChatHistoryMessage, 0)
	for i := resultStartIdx; i < endIdx; i++ {
		vm := validMessages[i]
		historyMsg := ChatHistoryMessage{
			ID:               vm.id,
			Role:             vm.msg.Role,
			Content:          vm.msg.Content,
			ReasoningContent: vm.msg.ReasoningContent,
			ToolCallID:       vm.msg.ToolCallID,
		}
		// For tool messages, look up the tool name from the assistant message that initiated the call
		if vm.msg.Role == "tool" && vm.msg.ToolCallID != "" {
			if toolName, ok := toolCallIDToName[vm.msg.ToolCallID]; ok {
				historyMsg.ToolName = toolName
			}
		}
		if len(vm.msg.ToolCalls) > 0 {
			historyMsg.ToolCalls = make([]HistoryToolCall, 0, len(vm.msg.ToolCalls))
			for _, tc := range vm.msg.ToolCalls {
				args := tc.Arguments
				if len(args) == 0 && tc.Function != nil && tc.Function.Arguments != "" {
					var parsed map[string]interface{}
					if json.Unmarshal([]byte(tc.Function.Arguments), &parsed) == nil {
						args = parsed
					}
				}
				historyMsg.ToolCalls = append(historyMsg.ToolCalls, HistoryToolCall{
					ID:               tc.ID,
					Type:             tc.Type,
					Name:             tc.Name,
					Arguments:        args,
					ThoughtSignature: tc.ThoughtSignature,
				})
			}
		}
		messages = append(messages, historyMsg)
	}

	// Check if there are more messages available
	hasMore := resultStartIdx > 0

	processing := false
	if n.agentLoop != nil {
		processing = n.agentLoop.IsSessionProcessing(sessionKey)
	}

	writeJSON(w, http.StatusOK, ChatHistoryResponse{
		SessionKey: sessionKey,
		Messages:   messages,
		Processing: processing,
		HasMore:    hasMore,
	})
}

func (n *NativeChannel) handleChatSessions(w http.ResponseWriter, r *http.Request) {
	clientID := getClientID(r)
	client, ok := n.auth.GetClient(clientID)
	if !ok {
		writeJSON(w, http.StatusOK, ChatSessionsResponse{Sessions: []ChatSession{}})
		return
	}

	offset, limit := parsePagination(r)

	sessions := make([]ChatSession, 0, len(client.SessionKeys))
	for _, sk := range client.SessionKeys {
		history := n.agentLoop.GetSessionHistory(sk)
		messageCount := 0
		for _, msg := range history {
			if msg.Role == "user" || msg.Role == "assistant" {
				messageCount++
			}
		}

		sessions = append(sessions, ChatSession{
			Key:          sk,
			Name:         n.agentLoop.GetName(sk),
			Created:      client.Created,
			Updated:      n.agentLoop.GetUpdated(sk),
			MessageCount: messageCount,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Updated.After(sessions[j].Updated)
	})

	total := len(sessions)
	start := offset
	if start > total {
		start = total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	writeJSON(w, http.StatusOK, ChatSessionsResponse{
		Sessions: sessions[start:end],
		Total:    total,
		HasMore:  end < total,
	})
}

func (n *NativeChannel) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	if req.SessionKey == "" {
		writeError(w, http.StatusBadRequest, "session_key is required", "session_key_missing")
		return
	}

	clientID := getClientID(r)

	// Validate that the session key hasn't been claimed by another client.
	// Session keys are UUIDs generated by the frontend — they must be unique
	// to prevent one client from hijacking another's session.
	if !n.auth.IsSessionKeyAvailable(req.SessionKey, clientID) {
		writeError(w, http.StatusForbidden, "session key already in use", "session_key_taken")
		return
	}

	n.auth.TrackSessionKey(clientID, req.SessionKey)

	writeJSON(w, http.StatusCreated, CreateSessionResponse(req))
}

func (n *NativeChannel) handleChatSessionGet(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)
	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	agentID := n.agentLoop.GetSessionAgent(sessionKey)
	model := n.agentLoop.GetSessionModel(sessionKey)
	name := n.agentLoop.GetName(sessionKey)
	thinkLevel := n.agentLoop.GetThinkLevel(sessionKey)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session_key": sessionKey,
		"agent_id":    agentID,
		"model":       model,
		"name":        name,
		"think_level": thinkLevel,
	})
}

func (n *NativeChannel) handleChatSessionDelete(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	if err := n.auth.RemoveSessionKey(clientID, sessionKey); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "session_not_found")
		return
	}
	n.agentLoop.ClearSession(sessionKey)

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (n *NativeChannel) handleChatClear(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	n.agentLoop.ClearSession(sessionKey)
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

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

func (n *NativeChannel) handleAgents(w http.ResponseWriter, r *http.Request) {
	agentIDs := n.agentLoop.ListAvailableAgentIDs()
	agents := make([]NativeAgentInfo, 0, len(agentIDs))
	defaultID := n.agentLoop.GetDefaultAgentID()

	for _, id := range agentIDs {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if ok {
			agents = append(agents, NativeAgentInfo{
				ID:        info.ID,
				Name:      info.Name,
				Workspace: info.Workspace,
				Model:     info.Model,
				Default:   info.ID == defaultID,
				Reasoning: info.Reasoning,
			})
		}
	}

	writeJSON(w, http.StatusOK, AgentsResponse{Agents: agents})
}

func (n *NativeChannel) handleAgentInfo(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}

	info, ok := n.agentLoop.GetAgentInfo(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}

	writeJSON(w, http.StatusOK, NativeAgentInfo{
		ID:        info.ID,
		Name:      info.Name,
		Workspace: info.Workspace,
		Model:     info.Model,
		Default:   info.ID == n.agentLoop.GetDefaultAgentID(),
	})
}

func (n *NativeChannel) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}

	_, ok := n.agentLoop.GetAgentInfo(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}

	status := n.agentLoop.GetStatus(getClientID(r))
	writeJSON(w, http.StatusOK, AgentStatusResponse{
		ID:             agentID,
		Status:         status,
		ActiveSessions: 0,
	})
}

func (n *NativeChannel) resolveAgentWorkspace(agentID string) (string, error) {
	info, ok := n.agentLoop.GetAgentInfo(agentID)
	if !ok {
		return "", fmt.Errorf("agent not found: %s", agentID)
	}

	workspace := info.Workspace
	if workspace == "" {
		workspace = filepath.Join(config.GetLeleDir(), "workspace")
	} else {
		workspace = expandHomePath(workspace)
	}

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace path: %w", err)
	}
	if !isAllowedWorkspacePath(absWorkspace) {
		return "", fmt.Errorf("workspace path is outside allowed directories")
	}

	// Initialize workspace if needed
	if err := lelectx.InitializeWorkspace(absWorkspace); err != nil {
		return "", fmt.Errorf("failed to initialize workspace: %w", err)
	}

	return absWorkspace, nil
}

func (n *NativeChannel) handleAgentFiles(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}

	absWorkspace, err := n.resolveAgentWorkspace(agentID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "agent not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "outside allowed") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error(), "workspace_error")
		return
	}

	n.handleAgentFileList(w, r, absWorkspace)
}

func (n *NativeChannel) handleAgentFileRead(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	fileName := r.PathValue("fileName")

	if agentID == "" || fileName == "" {
		writeError(w, http.StatusBadRequest, "agent id and file name required", "params_missing")
		return
	}

	absWorkspace, err := n.resolveAgentWorkspace(agentID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "agent not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "outside allowed") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error(), "workspace_error")
		return
	}

	if !lelectx.IsContextFile(fileName) {
		writeError(w, http.StatusForbidden, "file not allowed", "file_not_allowed")
		return
	}

	filePath := filepath.Join(absWorkspace, fileName)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, AgentFilesResponse{
				Content: "",
				Files: []AgentFileInfo{{
					Name:     fileName,
					Size:     0,
					Editable: true,
				}},
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read file", "read_error")
		return
	}

	writeJSON(w, http.StatusOK, AgentFilesResponse{
		Content: string(data),
		Files: []AgentFileInfo{{
			Name:     fileName,
			Size:     int64(len(data)),
			Editable: true,
		}},
	})
}

func (n *NativeChannel) handleAgentFileSave(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	fileName := r.PathValue("fileName")

	if agentID == "" || fileName == "" {
		writeError(w, http.StatusBadRequest, "agent id and file name required", "params_missing")
		return
	}

	absWorkspace, err := n.resolveAgentWorkspace(agentID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "agent not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "outside allowed") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error(), "workspace_error")
		return
	}

	if !lelectx.IsContextFile(fileName) {
		writeError(w, http.StatusForbidden, "file not allowed", "file_not_allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body", "body_invalid")
		return
	}

	var req AgentFilesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	filePath := filepath.Join(absWorkspace, fileName)
	if err := os.WriteFile(filePath, []byte(req.Content), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write file", "write_error")
		return
	}

	writeJSON(w, http.StatusOK, AgentFilesResponse{
		Files: []AgentFileInfo{{
			Name:     fileName,
			Size:     int64(len(req.Content)),
			Editable: true,
		}},
	})
}

func (n *NativeChannel) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	configPath := n.getConfigPath()

	doc, meta, err := config.LoadEditableDocument(configPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load config: "+err.Error(), "config_load_failed")
		return
	}

	writeJSON(w, http.StatusOK, ConfigResponse{
		Config: doc,
		Metadata: ConfigMetadata{
			ConfigPath:              meta.ConfigPath,
			Source:                  meta.Source,
			CanSave:                 meta.CanSave,
			RestartRequiredSections: meta.RestartRequiredSections,
			SecretsByPath:           meta.SecretsByPath,
		},
	})
}

func (n *NativeChannel) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	configPath := n.getConfigPath()

	var req ConfigUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "body_invalid")
		return
	}

	body, err := json.Marshal(req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config payload", "body_invalid")
		return
	}

	var doc config.EditableDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid config payload: "+err.Error(), "body_invalid")
		return
	}

	validationErrors := config.ValidateEditableDocument(&doc)
	if len(validationErrors) > 0 {
		httpErrors := make([]ConfigError, len(validationErrors))
		for i, err := range validationErrors {
			httpErrors[i] = ConfigError{
				Path:    err.Path,
				Message: err.Message,
				Code:    err.Code,
			}
		}
		writeJSON(w, http.StatusUnprocessableEntity, ConfigUpdateResponse{
			Errors: httpErrors,
		})
		return
	}

	if _, err := doc.ToConfig(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "config validation failed: "+err.Error(), "config_invalid")
		return
	}

	if err := config.SaveEditableDocument(configPath, &doc); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config: "+err.Error(), "config_save_failed")
		return
	}

	_, meta, err := config.LoadEditableDocument(configPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reload config: "+err.Error(), "config_reload_failed")
		return
	}

	writeJSON(w, http.StatusOK, ConfigUpdateResponse{
		Config: &doc,
		Metadata: ConfigMetadata{
			ConfigPath:              meta.ConfigPath,
			Source:                  meta.Source,
			CanSave:                 meta.CanSave,
			RestartRequiredSections: meta.RestartRequiredSections,
			SecretsByPath:           meta.SecretsByPath,
		},
	})
}

func (n *NativeChannel) handleValidateConfig(w http.ResponseWriter, r *http.Request) {
	var req ConfigValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "body_invalid")
		return
	}

	body, err := json.Marshal(req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config payload", "body_invalid")
		return
	}

	var doc config.EditableDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid config payload: "+err.Error(), "body_invalid")
		return
	}

	validationErrors := config.ValidateEditableDocument(&doc)
	if _, err := doc.ToConfig(); err != nil {
		validationErrors = append(validationErrors, config.ValidationError{
			Path:    "config",
			Message: err.Error(),
			Code:    "config_invalid",
		})
	}

	if len(validationErrors) > 0 {
		httpErrors := make([]ConfigError, len(validationErrors))
		for i, err := range validationErrors {
			httpErrors[i] = ConfigError{
				Path:    err.Path,
				Message: err.Message,
				Code:    err.Code,
			}
		}
		writeJSON(w, http.StatusOK, ConfigValidateResponse{
			Valid:  false,
			Errors: httpErrors,
		})
		return
	}

	writeJSON(w, http.StatusOK, ConfigValidateResponse{
		Valid:  true,
		Errors: nil,
	})
}

func (n *NativeChannel) handleTools(w http.ResponseWriter, r *http.Request) {
	sessionKey := getQueryParam(r, "session_key")
	if sessionKey == "" {
		sessionKey = getClientID(r)
	}

	supportsImages := n.agentLoop.GetSessionModelSupportsImages(sessionKey)

	tools := []ToolInfo{
		{Name: "read_file", Description: "Read file from workspace", Enabled: true},
		{Name: "write_file", Description: "Write file to workspace", Enabled: true},
		{Name: "list_dir", Description: "List directory contents", Enabled: true},
		{Name: "exec", Description: "Execute shell commands", Enabled: true},
		{Name: "web_search", Description: "Search the web", Enabled: true},
		{Name: "web_fetch", Description: "Fetch web content", Enabled: true},
		{Name: "spawn", Description: "Create subagent", Enabled: true},
	}

	if supportsImages {
		tools = append(tools, ToolInfo{Name: "read_image", Description: "Read and analyze images", Enabled: true})
	}

	writeJSON(w, http.StatusOK, ToolsResponse{Tools: tools})
}

func (n *NativeChannel) handleModels(w http.ResponseWriter, r *http.Request) {
	agentID := getQueryParam(r, "agent_id")
	if agentID == "" {
		agentID = n.agentLoop.GetSessionAgent(getClientID(r))
	}

	models := n.listAllModels()
	modelGroups := n.buildModelGroups(agentID, models)
	model := ""
	if sessionKey := getQueryParam(r, "session_key"); sessionKey != "" {
		model = n.agentLoop.GetSessionModel(sessionKey)
	}

	writeJSON(w, http.StatusOK, ModelsResponse{
		AgentID:     agentID,
		Model:       model,
		Models:      models,
		ModelGroups: modelGroups,
	})
}

func (n *NativeChannel) handleSkills(w http.ResponseWriter, r *http.Request) {
	skills := n.skillsLoader.ListSkills()

	skillInfos := make([]SkillInfo, 0, len(skills))
	for _, s := range skills {
		skillInfos = append(skillInfos, SkillInfo{
			ID:          s.Name,
			Name:        s.Name,
			Description: s.Description,
			Installed:   true,
			Source:      s.Source,
		})
	}

	writeJSON(w, http.StatusOK, SkillsResponse{Skills: skillInfos})
}

func (n *NativeChannel) handleSkillInstall(w http.ResponseWriter, r *http.Request) {
	var req SkillInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_request")
		return
	}

	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required", "missing_url")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := n.skillInstaller.InstallFromGitHub(ctx, req.URL); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to install skill: %v", err), "install_failed")
		return
	}

	skillName := filepath.Base(req.URL)
	writeJSON(w, http.StatusCreated, SkillInstallResponse{
		SkillID: skillName,
		Message: fmt.Sprintf("Skill '%s' installed successfully", skillName),
	})
}

func (n *NativeChannel) handleSkillsAvailable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	available, err := n.skillInstaller.ListAvailableSkills(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to fetch available skills: %v", err), "fetch_failed")
		return
	}

	type AvailableSkillInfo struct {
		Name        string   `json:"name"`
		Repository  string   `json:"repository"`
		Description string   `json:"description"`
		Author      string   `json:"author"`
		Tags        []string `json:"tags"`
	}

	result := make([]AvailableSkillInfo, 0, len(available))
	for _, s := range available {
		result = append(result, AvailableSkillInfo{
			Name:        s.Name,
			Repository:  s.Repository,
			Description: s.Description,
			Author:      s.Author,
			Tags:        s.Tags,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"skills": result,
	})
}

func (n *NativeChannel) handleSkillRemove(w http.ResponseWriter, r *http.Request) {
	skillName := r.PathValue("name")
	if skillName == "" {
		writeError(w, http.StatusBadRequest, "skill name is required", "missing_name")
		return
	}

	if err := n.skillInstaller.Uninstall(skillName); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to remove skill: %v", err), "remove_failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": fmt.Sprintf("Skill '%s' removed successfully", skillName),
	})
}

func (n *NativeChannel) handleStatus(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(n.startTime).String()

	agents := make([]map[string]interface{}, 0)
	for _, id := range n.agentLoop.ListAvailableAgentIDs() {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if ok {
			agents = append(agents, map[string]interface{}{
				"id":     info.ID,
				"name":   info.Name,
				"status": "running",
			})
		}
	}

	channels := make([]map[string]interface{}, 0)
	channels = append(channels, map[string]interface{}{
		"name":    "native",
		"enabled": true,
		"running": n.running,
	})

	writeJSON(w, http.StatusOK, SystemStatusResponse{
		Status:   "running",
		Uptime:   uptime,
		Agents:   agents,
		Channels: channels,
		Version:  "1.0.0",
	})
}

func (n *NativeChannel) handleChannels(w http.ResponseWriter, r *http.Request) {
	channels := []ChannelInfo{
		{Name: "native", Enabled: true, Running: n.running},
	}

	writeJSON(w, http.StatusOK, ChannelsResponse{Channels: channels})
}

func (n *NativeChannel) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	providerName, err := url.PathUnescape(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid provider name", "name_invalid")
		return
	}
	if providerName == "" {
		writeError(w, http.StatusBadRequest, "provider name required", "name_missing")
		return
	}

	cfg := n.cfgSnapshot()
	named, ok := cfg.Providers.GetNamed(providerName)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("provider %q not found", providerName), "provider_not_found")
		return
	}

	apiKey := named.APIKey
	apiBase := strings.TrimRight(named.APIBase, "/")
	providerType := named.Type
	if providerType == "" {
		providerType = providerName
	}

	if apiBase == "" {
		apiBase = defaultAPIBaseByTypePublic(providerType)
	}
	if apiBase == "" {
		writeError(w, http.StatusBadRequest, "provider has no api_base configured", "no_api_base")
		return
	}

	if apiKey == "" {
		writeError(w, http.StatusBadRequest, "provider has no api_key configured", "no_api_key")
		return
	}

	// SSRF guard: validate the provider URL before making any outbound request.
	// Block requests to private/internal IPs and enforce HTTPS.
	if !isAllowedProviderURL(apiBase) {
		writeError(w, http.StatusBadRequest, "provider api_base is not allowed: must be a public HTTPS URL", "url_not_allowed")
		return
	}

	modelsURL := apiBase + "/models"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, modelsURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create request", "request_error")
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to fetch models: %v", err), "upstream_error")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		writeError(w, resp.StatusCode, fmt.Sprintf("upstream returned %d: %s", resp.StatusCode, string(body)), "upstream_error")
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read response", "read_error")
		return
	}

	var modelsResp struct {
		Data []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse models response", "parse_error")
		return
	}

	models := make([]ProviderModelInfo, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		models = append(models, ProviderModelInfo{
			ID:      m.ID,
			Object:  m.Object,
			Created: m.Created,
			OwnedBy: m.OwnedBy,
		})
	}

	writeJSON(w, http.StatusOK, ProviderModelsResponse{
		Provider: providerName,
		Models:   models,
	})
}

// isAllowedProviderURL validates that a provider API base URL is safe to connect to.
// It blocks SSRF attacks by rejecting private/internal IPs and non-HTTPS schemes.
func isAllowedProviderURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	// Only allow HTTPS to prevent man-in-the-middle and credential leakage.
	if u.Scheme != "https" {
		return false
	}

	host := u.Hostname()
	// Block hostnames that resolve to localhost.
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return false
	}

	// Block private, loopback, and link-local IP addresses.
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return false
	}

	return true
}

func (n *NativeChannel) handleAgentFileList(w http.ResponseWriter, _ *http.Request, workspace string) {
	files := make([]AgentFileInfo, 0, len(lelectx.ContextFiles))

	for _, name := range lelectx.ContextFiles {
		absFilePath := filepath.Join(workspace, name)
		info, err := os.Stat(absFilePath)
		if err != nil {
			files = append(files, AgentFileInfo{
				Name:     name,
				Size:     0,
				Editable: true,
			})
			continue
		}
		files = append(files, AgentFileInfo{
			Name:     name,
			Size:     info.Size(),
			Editable: true,
		})
	}

	writeJSON(w, http.StatusOK, AgentFilesResponse{Files: files})
}

func parsePagination(r *http.Request) (offset, limit int) {
	offsetStr := getQueryParam(r, "offset")
	limitStr := getQueryParam(r, "limit")

	offset, _ = strconv.Atoi(offsetStr)
	if offset < 0 {
		offset = 0
	}

	limit, _ = strconv.Atoi(limitStr)
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	return offset, limit
}

func (n *NativeChannel) buildModelGroups(_ string, _ []string) []ModelGroup {
	cfg := n.cfgSnapshot()
	if cfg == nil {
		return nil
	}

	providers := cfg.Providers.ListNamed()
	providerNames := make([]string, 0, len(providers))
	for name := range providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)

	groups := make([]ModelGroup, 0, len(providerNames))
	for _, providerName := range providerNames {
		provider := providers[providerName]
		aliases := make([]string, 0, len(provider.Models))
		for alias := range provider.Models {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		if len(aliases) == 0 {
			continue
		}

		group := ModelGroup{
			Provider: providerName,
			Models:   make([]ModelOption, 0, len(aliases)),
		}
		for _, alias := range aliases {
			modelCfg := provider.Models[alias]
			resolved := strings.TrimSpace(modelCfg.Model)
			var value string
			if resolved != "" {
				value = providerName + "/" + resolved
			} else {
				value = providerName + "/" + alias
			}
			group.Models = append(group.Models, ModelOption{
				Value:     value,
				Label:     alias,
				Reasoning: modelCfg.Reasoning,
			})
		}
		groups = append(groups, group)
	}

	if len(groups) == 0 {
		return nil
	}
	return groups
}

func (n *NativeChannel) listAllModels() []string {
	cfg := n.cfgSnapshot()
	if cfg == nil {
		return nil
	}

	providers := cfg.Providers.ListNamed()
	providerNames := make([]string, 0, len(providers))
	for name := range providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)

	models := make([]string, 0)
	seen := make(map[string]bool)
	for _, providerName := range providerNames {
		provider := providers[providerName]
		aliases := make([]string, 0, len(provider.Models))
		for alias := range provider.Models {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		for _, alias := range aliases {
			key := providerName + "/" + alias
			if seen[key] {
				continue
			}
			models = append(models, key)
			seen[key] = true
		}
	}

	return models
}

func (n *NativeChannel) cfgSnapshot() *config.Config {
	if n.agentLoop != nil {
		if cfg := n.agentLoop.GetConfigSnapshot(); cfg != nil {
			return cfg
		}
	}

	cfg := config.DefaultConfig()
	if n.cfg != nil {
		cfg.Channels.Native = *n.cfg
	}
	return cfg
}

func defaultAPIBaseByTypePublic(providerType string) string {
	switch providerType {
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "openai", "gpt":
		return "https://api.openai.com/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "nanogpt":
		return "https://nano-gpt.com/api/v1"
	case "chutes":
		return "https://llm.chutes.ai/v1"
	case "alibaba", "alibaba_coding_plan":
		return "https://coding-intl.dashscope.aliyuncs.com/v1"
	case "zhipu":
		return "https://open.bigmodel.cn/api/paas/v4"
	case "gemini", "google":
		return "https://generativelanguage.googleapis.com/v1beta"
	case "shengsuanyun":
		return "https://router.shengsuanyun.com/api/v1"
	case "nvidia":
		return "https://integrate.api.nvidia.com/v1"
	case "moonshot":
		return "https://api.moonshot.cn/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "vllm":
		return ""
	default:
		return ""
	}
}
