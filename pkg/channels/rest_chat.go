package channels

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
)

var subagentIDRegex = regexp.MustCompile(`^subagent-\d+$`)

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

		// Skip empty sessions that were never actually used
		if messageCount == 0 {
			continue
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

func (n *NativeChannel) handleChatApprove(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	var req ApproveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	if req.RequestID == "" {
		writeError(w, http.StatusBadRequest, "request_id is required", "request_id_missing")
		return
	}

	if n.approvalManager == nil {
		writeError(w, http.StatusInternalServerError, "approval manager not available", "approval_unavailable")
		return
	}

	// HandleApproval atomically finds, removes and returns the approval
	handledApproval, err := n.approvalManager.HandleApproval(req.RequestID, req.Approved)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error(), "approval_not_found")
		return
	}

	command := handledApproval.Command
	reason := handledApproval.Reason

	// Persist the approval decision in session history
	n.persistApprovalMessage(sessionKey, req.RequestID, req.Approved, command, reason)

	// Build the user-facing message
	approvalContent := "✅ Command approved"
	if !req.Approved {
		approvalContent = "❌ Command rejected"
	}

	// Broadcast approval result via WebSocket for real-time UI update
	n.emitNativeEvent(sessionKey, "approve.result", map[string]interface{}{
		"request_id": req.RequestID,
		"approved":   req.Approved,
		"command":    command,
	}, "")

	writeJSON(w, http.StatusOK, ApproveResponse{
		RequestID: req.RequestID,
		Approved:  req.Approved,
		Message:   approvalContent,
	})
}

// persistApprovalMessage stores the approval/rejection decision as a tool message in session history.
func (n *NativeChannel) persistApprovalMessage(sessionKey, requestID string, approved bool, command, reason string) {
	content := "✅ Command approved"
	if !approved {
		content = "❌ Command rejected"
	}
	if command != "" {
		content += ": `" + command + "`"
	}

	n.agentLoop.AddSessionMessage(sessionKey, providers.Message{
		Role:               "tool",
		Content:            content,
		ToolCallID:         "approval:" + requestID,
		ExcludeFromContext: true,
	})
}
