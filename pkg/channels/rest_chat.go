package channels

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
)

var subagentIDRegex = regexp.MustCompile(`^subagent-\d+$`)

func (n *NativeChannel) handleChatSend(w http.ResponseWriter, r *http.Request) {
	var req ChatSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	if !hasSendablePayload(req.Content, req.Attachments) {
		writeError(w, http.StatusBadRequest, "message content or an attachment is required", "content_missing")
		return
	}

	clientID := getClientID(r)
	sessionKey := req.SessionKey
	if sessionKey == "" {
		sessionKey = clientID
	}

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	n.auth.TrackSessionKey(clientID, sessionKey)

	if req.AgentID != "" {
		n.agentLoop.HintSessionAgent(sessionKey, req.AgentID)
	}

	messageID := uuid.New().String()

	attachments := n.processAttachments(req.Attachments, sessionKey)

	if !hasSendablePayload(req.Content, attachments) {
		writeError(w, http.StatusBadRequest, "attachments could not be read; add text or re-upload", "attachments_rejected")
		return
	}

	msg := bus.InboundMessage{
		Channel:     ChannelName,
		SenderID:    clientID,
		ChatID:      sessionKey,
		Content:     req.Content,
		Attachments: attachments,
		SessionKey:  sessionKey,
		Metadata:    map[string]string{"message_id": messageID},
	}
	n.base.publishInbound(&msg)

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

	beforeID := getQueryParam(r, "before_id")
	limitStr := getQueryParam(r, "limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	history := n.agentLoop.GetSessionHistory(sessionKey)

	processing := false
	if n.agentLoop != nil {
		processing = n.agentLoop.IsSessionProcessing(sessionKey)
	}

	builder := newChatHistoryBuilder(history)

	if strings.HasPrefix(beforeID, evictedIDPrefix) {
		seq, err := strconv.Atoi(strings.TrimPrefix(beforeID, evictedIDPrefix))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid evicted cursor", "cursor_invalid")
			return
		}
		n.writeEvictedHistory(w, sessionKey, seq, limit, processing, builder)
		return
	}

	cursorKey, cursorOccurrence, hasResidentCursor := parseResidentHistoryCursor(sessionKey, beforeID)

	type residentEntry struct {
		msgIdx     int
		key        residentKey
		occurrence int
	}
	entries := make([]residentEntry, 0, len(history))
	residentOccurrences := make(map[residentKey]int, len(history))
	matchedIdx := -1
	for i := range history {
		msg := &history[i]
		if !residentHistoryVisible(msg) {
			continue
		}
		if msg.Streaming && processing {
			continue
		}
		key := residentMessageKeyBytes(msg)
		occurrence := residentOccurrences[key]
		residentOccurrences[key]++
		if hasResidentCursor && matchedIdx < 0 && occurrence == cursorOccurrence && key == cursorKey {
			matchedIdx = len(entries)
		}
		entries = append(entries, residentEntry{msgIdx: i, key: key, occurrence: occurrence})
	}

	startIdx := len(entries)
	if matchedIdx >= 0 {
		startIdx = matchedIdx
	}

	endIdx := startIdx
	if endIdx > len(entries) {
		endIdx = len(entries)
	}
	resultStartIdx := endIdx - limit
	if resultStartIdx < 0 {
		resultStartIdx = 0
	}

	if beforeID != "" && resultStartIdx == 0 && len(entries) > 0 && (startIdx == 0 || startIdx == len(entries)) {
		if n.agentLoop != nil && n.agentLoop.GetEvictedMessageCount(sessionKey) > 0 {
			n.writeEvictedHistory(w, sessionKey, -1, limit, processing, builder)
			return
		}
	}
	if len(entries) == 0 && n.agentLoop != nil && n.agentLoop.GetEvictedMessageCount(sessionKey) > 0 {
		n.writeEvictedHistory(w, sessionKey, beforeSeqFromCursor(beforeID), limit, processing, builder)
		return
	}

	messages := make([]ChatHistoryMessage, 0, endIdx-resultStartIdx)
	for i := resultStartIdx; i < endIdx; i++ {
		e := entries[i]
		msgID := residentMessageID(sessionKey, residentKeyHex(e.key), e.occurrence)
		messages = append(messages, builder.message(msgID, history[e.msgIdx]))
	}

	hasMore := resultStartIdx > 0
	if !hasMore && n.agentLoop != nil && n.agentLoop.GetEvictedMessageCount(sessionKey) > 0 {
		hasMore = true
	}

	writeJSON(w, http.StatusOK, ChatHistoryResponse{
		SessionKey: sessionKey,
		Messages:   messages,
		Processing: processing,
		HasMore:    hasMore,
		Groups:     n.sessionGroupSnapshots(sessionKey),
	})
}

const evictedIDPrefix = "evicted:"

func beforeSeqFromCursor(cursor string) int {
	if strings.HasPrefix(cursor, evictedIDPrefix) {
		if seq, err := strconv.Atoi(strings.TrimPrefix(cursor, evictedIDPrefix)); err == nil {
			return seq
		}
	}
	return -1
}

const residentKeyBytes = 8

type residentKey [residentKeyBytes]byte

const (
	residentKeySampleWindow  = 32
	residentKeySampleWindows = 5
	residentKeySampleSpan    = residentKeySampleWindow * residentKeySampleWindows
)

const (
	residentKeyHashOffset = 14695981039346656037
	residentKeyHashPrime  = 1099511628211
)

func residentHashBlock(h uint64, block string) uint64 {
	i := 0
	for ; i+8 <= len(block); i += 8 {
		h = (h ^ binary.LittleEndian.Uint64([]byte(block[i:i+8]))) * residentKeyHashPrime
	}
	for ; i < len(block); i++ {
		h = (h ^ uint64(block[i])) * residentKeyHashPrime
	}
	return h
}

func residentHashField(h uint64, field string) uint64 {
	h = (h ^ uint64(len(field))) * residentKeyHashPrime
	return residentHashBlock(h, field)
}

func residentHashContent(h uint64, content string) uint64 {
	h = (h ^ uint64(len(content))) * residentKeyHashPrime
	if len(content) <= residentKeySampleSpan {
		return residentHashBlock(h, content)
	}
	offsets := [residentKeySampleWindows]int{
		0,
		len(content) / 4,
		len(content) / 2,
		3 * len(content) / 4,
		len(content) - residentKeySampleWindow,
	}
	for _, start := range offsets {
		h = residentHashBlock(h, content[start:start+residentKeySampleWindow])
	}
	return h
}

func residentMessageKeyBytes(msg *providers.Message) residentKey {
	h := uint64(residentKeyHashOffset)
	h = residentHashField(h, msg.Role)
	h = residentHashContent(h, msg.Content)
	if msg.ToolCallID != "" {
		h = residentHashField(h, msg.ToolCallID)
	}
	for _, tc := range msg.ToolCalls {
		h = residentHashField(h, tc.ID)
		h = residentHashField(h, tc.Name)
	}
	var key residentKey
	for i := range key {
		key[i] = byte(h >> (8 * uint(i)))
	}
	return key
}

func residentKeyHex(key residentKey) string {
	return hex.EncodeToString(key[:])
}

func residentMessageKey(msg providers.Message) string {
	return residentKeyHex(residentMessageKeyBytes(&msg))
}

func parseResidentHistoryCursor(sessionKey, cursor string) (residentKey, int, bool) {
	var key residentKey
	if cursor == "" || strings.HasPrefix(cursor, evictedIDPrefix) {
		return key, 0, false
	}
	rest, ok := strings.CutPrefix(cursor, sessionKey+":")
	if !ok {
		return key, 0, false
	}
	occurrence := 0
	if idx := strings.LastIndexByte(rest, '-'); idx >= 0 {
		parsed, err := strconv.Atoi(rest[idx+1:])
		if err != nil || parsed < 0 {
			return key, 0, false
		}
		occurrence = parsed
		rest = rest[:idx]
	}
	if len(rest) != 2*residentKeyBytes {
		return key, 0, false
	}
	raw, err := hex.DecodeString(rest)
	if err != nil {
		return key, 0, false
	}
	copy(key[:], raw)
	return key, occurrence, true
}

func residentHistoryVisible(msg *providers.Message) bool {
	if msg.Role != "user" && msg.Role != "assistant" && msg.Role != "tool" {
		return false
	}
	return !(msg.Role == "user" && msg.Content == "" && len(msg.ContentParts) > 0)
}

func residentMessageID(sessionKey, key string, occurrence int) string {
	if occurrence <= 0 {
		return sessionKey + ":" + key
	}
	return sessionKey + ":" + key + "-" + strconv.Itoa(occurrence)
}

type chatHistoryBuilder struct {
	toolCallIDToName map[string]string
}

func newChatHistoryBuilder(history []providers.Message) *chatHistoryBuilder {
	b := &chatHistoryBuilder{toolCallIDToName: make(map[string]string)}
	for i := range history {
		msg := &history[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID == "" {
				continue
			}
			toolName := tc.Name
			if toolName == "" && tc.Function != nil {
				toolName = tc.Function.Name
			}
			if toolName != "" {
				b.toolCallIDToName[tc.ID] = toolName
			}
		}
	}
	return b
}

func (b *chatHistoryBuilder) message(id string, msg providers.Message) ChatHistoryMessage {
	historyMsg := ChatHistoryMessage{
		ID:                 id,
		Role:               msg.Role,
		Content:            msg.Content,
		ReasoningContent:   msg.ReasoningContent,
		ToolCallID:         msg.ToolCallID,
		ExcludeFromContext: msg.ExcludeFromContext,
		Attachments:        msg.Attachments,
		DisplayContent:     msg.DisplayContent,
		Command:            msg.Command,
		ErrorDetail:        msg.ErrorDetail,
	}
	if msg.Role == "tool" && msg.ToolCallID != "" {
		if toolName, ok := b.toolCallIDToName[msg.ToolCallID]; ok {
			historyMsg.ToolName = toolName
		}
	}
	if len(msg.ToolCalls) > 0 {
		historyMsg.ToolCalls = make([]HistoryToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			args := tc.Arguments
			if len(args) == 0 && tc.Function != nil && tc.Function.Arguments != "" {
				var parsed map[string]interface{}
				if json.Unmarshal([]byte(tc.Function.Arguments), &parsed) == nil {
					args = parsed
				}
			}
			tcName := tc.Name
			if tcName == "" && tc.Function != nil {
				tcName = tc.Function.Name
			}
			historyMsg.ToolCalls = append(historyMsg.ToolCalls, HistoryToolCall{
				ID:               tc.ID,
				Type:             tc.Type,
				Name:             tcName,
				Arguments:        args,
				ThoughtSignature: tc.ThoughtSignature,
			})
		}
	}
	return historyMsg
}

func (n *NativeChannel) writeEvictedHistory(w http.ResponseWriter, sessionKey string, beforeSeq, limit int, processing bool, builder *chatHistoryBuilder) {
	page := n.agentLoop.LoadEvictedMessagesPage(sessionKey, beforeSeq, 0, limit)
	messages := make([]ChatHistoryMessage, 0)
	hasMore := false
	if page != nil {
		pageBuilder := newChatHistoryBuilder(page.Messages)
		for id, name := range builder.toolCallIDToName {
			if _, ok := pageBuilder.toolCallIDToName[id]; !ok {
				pageBuilder.toolCallIDToName[id] = name
			}
		}
		for i := range page.Messages {
			msg := &page.Messages[i]
			if !residentHistoryVisible(msg) {
				continue
			}
			messages = append(messages, pageBuilder.message(evictedIDPrefix+strconv.Itoa(page.Seqs[i]), *msg))
		}
		hasMore = page.HasOlder
	}
	writeJSON(w, http.StatusOK, ChatHistoryResponse{
		SessionKey: sessionKey,
		Messages:   messages,
		Processing: processing,
		HasMore:    hasMore,
		Groups:     n.sessionGroupSnapshots(sessionKey),
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

	modeFilter := r.URL.Query().Get("mode")
	kindFilter := r.URL.Query().Get("kind")
	includeSystem := r.URL.Query().Get("include_system") == "true"

	sessions := make([]ChatSession, 0, len(client.SessionKeys))
	for _, sk := range client.SessionKeys {
		if !n.agentLoop.HasMessages(sk) {
			continue
		}

		sessionMode := n.agentLoop.GetSessionMode(sk)
		kind := classifySessionKeyKind(sk)

		if modeFilter != "" {
			effectiveMode := sessionMode
			if effectiveMode == "" {
				effectiveMode = "agent"
			}
			if effectiveMode != modeFilter {
				continue
			}
		}
		if kindFilter != "" && kind != kindFilter {
			continue
		}

		sessions = append(sessions, ChatSession{
			Key:     sk,
			Name:    n.agentLoop.GetName(sk),
			Mode:    sessionMode,
			Kind:    kind,
			Folder:  n.agentLoop.GetSessionFolder(sk),
			Created: n.agentLoop.GetCreated(sk),
			Updated: n.agentLoop.GetUpdated(sk),
		})
	}

	mergeAllSessions := n.agentLoop != nil && (kindFilter != "" || includeSystem)
	if mergeAllSessions {
		allSessions := n.agentLoop.ListAllSessions()
		seen := make(map[string]bool, len(sessions))
		for _, s := range sessions {
			seen[s.Key] = true
		}
		for _, info := range allSessions {
			if seen[info.Key] {
				continue
			}
			if !n.validateSessionOwnership(clientID, info.Key) {
				continue
			}
			if modeFilter != "" {
				effectiveMode := info.Mode
				if effectiveMode == "" {
					effectiveMode = "agent"
				}
				if effectiveMode != modeFilter {
					continue
				}
			}
			if kindFilter != "" && info.Kind != kindFilter {
				continue
			}
			sessions = append(sessions, ChatSession{
				Key:     info.Key,
				Name:    info.Name,
				Mode:    info.Mode,
				Kind:    info.Kind,
				Created: info.Created,
				Updated: info.Updated,
			})
		}
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

func (n *NativeChannel) handleChatSessionsMeta(w http.ResponseWriter, r *http.Request) {
	clientID := getClientID(r)
	client, ok := n.auth.GetClient(clientID)
	if !ok {
		writeJSON(w, http.StatusOK, ChatSessionsResponse{Sessions: []ChatSession{}})
		return
	}

	offset, limit := parsePagination(r)
	modeFilter := r.URL.Query().Get("mode")
	kindFilter := r.URL.Query().Get("kind")
	includeSystem := r.URL.Query().Get("include_system") == "true"

	if n.agentLoop == nil {
		writeJSON(w, http.StatusOK, ChatSessionsResponse{Sessions: []ChatSession{}})
		return
	}

	index := n.agentLoop.ListAllSessions()
	indexByKey := make(map[string]SessionKindInfo, len(index))
	for _, info := range index {
		indexByKey[info.Key] = info
	}

	sessions := make([]ChatSession, 0, len(index))
	seen := make(map[string]bool, len(index))

	for _, sk := range client.SessionKeys {
		if seen[sk] {
			continue
		}
		seen[sk] = true

		resolved := n.agentLoop.ResolveSessionKey(sk)
		info, known := indexByKey[resolved]
		kind := classifySessionKeyKind(sk)
		if !known {
			if !n.agentLoop.HasMessages(resolved) {
				continue
			}
			info = SessionKindInfo{
				Name:        n.agentLoop.GetName(resolved),
				Mode:        n.agentLoop.GetSessionMode(resolved),
				Folder:      n.agentLoop.GetSessionFolder(resolved),
				Created:     n.agentLoop.GetCreated(resolved),
				Updated:     n.agentLoop.GetUpdated(resolved),
				Kind:        kind,
				HasMessages: true,
			}
		}
		if !info.HasMessages {
			continue
		}
		if !sessionPassesFilters(info.Mode, kind, modeFilter, kindFilter) {
			continue
		}
		sessions = append(sessions, ChatSession{
			Key:     sk,
			Name:    info.Name,
			Mode:    info.Mode,
			Kind:    kind,
			Folder:  info.Folder,
			Created: info.Created,
			Updated: info.Updated,
		})
	}

	if kindFilter != "" || includeSystem {
		for _, info := range index {
			if seen[info.Key] {
				continue
			}
			if !n.validateSessionOwnership(clientID, info.Key) {
				continue
			}
			if !sessionPassesFilters(info.Mode, info.Kind, modeFilter, kindFilter) {
				continue
			}
			sessions = append(sessions, ChatSession{
				Key:     info.Key,
				Name:    info.Name,
				Mode:    info.Mode,
				Kind:    info.Kind,
				Folder:  info.Folder,
				Created: info.Created,
				Updated: info.Updated,
			})
		}
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

func sessionPassesFilters(mode, kind, modeFilter, kindFilter string) bool {
	if modeFilter != "" {
		effectiveMode := mode
		if effectiveMode == "" {
			effectiveMode = "agent"
		}
		if effectiveMode != modeFilter {
			return false
		}
	}
	if kindFilter != "" && kind != kindFilter {
		return false
	}
	return true
}

func classifySessionKeyKind(sessionKey string) string {
	if sessionKey == "" {
		return "chat"
	}
	if sessionKey == "heartbeat" {
		return "heartbeat"
	}
	if strings.HasPrefix(sessionKey, "cron-spawn-") {
		return "cron-spawn"
	}
	if strings.Contains(sessionKey, ":cron-") && strings.Contains(sessionKey, ":subagent-") {
		return "cron-spawn"
	}
	if strings.HasPrefix(sessionKey, "cron-") {
		return "cron"
	}
	if routing.IsSubagentSessionKey(sessionKey) {
		return "subagent"
	}
	if idx := strings.LastIndex(sessionKey, ":subagent-"); idx > 0 {
		return "subagent"
	}
	return "chat"
}

var channelSessionKeyChannels = map[string]bool{
	"telegram": true,
	"discord":  true,
	"slack":    true,
	"whatsapp": true,
	"feishu":   true,
	"dingtalk": true,
	"onebot":   true,
	"qq":       true,
	"line":     true,
	"maixcam":  true,
}

func isInstallationScopedKey(sessionKey string) bool {
	switch classifySessionKeyKind(sessionKey) {
	case "heartbeat", "cron", "cron-spawn":
		return true
	}
	return isChannelScopedSessionKey(sessionKey)
}

func isChannelScopedSessionKey(sessionKey string) bool {
	parts := strings.Split(sessionKey, ":")
	if len(parts) < 2 {
		return false
	}
	if parts[0] == "agent" {
		if len(parts) < 4 {
			return false
		}
		return channelSessionKeyChannels[parts[2]]
	}
	return channelSessionKeyChannels[parts[0]]
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

	if !isValidSessionKeyFormat(req.SessionKey) {
		writeError(w, http.StatusBadRequest, "invalid session_key format", "session_key_invalid")
		return
	}
	if conflict := n.sessionCreateConflict(clientID, req.SessionKey); conflict != "" {
		writeError(w, http.StatusForbidden, "session key is not available to this client", conflict)
		return
	}

	n.auth.TrackSessionKey(clientID, req.SessionKey)

	if req.Mode != "" {
		n.agentLoop.SetSessionMode(req.SessionKey, req.Mode)
	}

	writeJSON(w, http.StatusCreated, CreateSessionResponse{
		SessionKey: req.SessionKey,
	})
}

// A client may only create sessions in its own namespace: a fresh bare UUID or
// native:<callerClientID>:<suffix>. Channel, system, subagent and alias-target keys are
// derived by the server, so rejecting them wholesale removes the possibility of
// pre-claiming a session that belongs to an external peer.
func isCreatableSessionKey(clientID, sessionKey string) bool {
	if rest, ok := strings.CutPrefix(sessionKey, "native:"); ok {
		ownerID, suffix, found := strings.Cut(rest, ":")
		return found && suffix != "" && ownerID == clientID
	}

	parsed, err := uuid.Parse(sessionKey)
	return err == nil && parsed.String() == sessionKey
}

func (n *NativeChannel) sessionCreateConflict(clientID, sessionKey string) string {
	if !isCreatableSessionKey(clientID, sessionKey) {
		return "session_key_unavailable"
	}

	for _, other := range n.auth.ListClients() {
		if other.ClientID == clientID {
			continue
		}
		for _, owned := range other.SessionKeys {
			if n.keysShareSession(owned, sessionKey) {
				return "session_key_unavailable"
			}
		}
	}

	if n.agentLoop != nil && n.agentLoop.HasMessages(n.resolveSessionAlias(sessionKey)) {
		return "session_key_unavailable"
	}

	return ""
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
		"folder":      n.agentLoop.GetSessionFolder(sessionKey),
	})
}

func (n *NativeChannel) handleChatSessionDelete(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	_ = n.auth.RemoveSessionKey(clientID, sessionKey)

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

	handledApproval, err := n.approvalManager.HandleApprovalForSession(req.RequestID, req.Approved, sessionKey)
	if err != nil {
		switch {
		case errors.Is(err, ErrApprovalSessionMismatch), errors.Is(err, ErrApprovalRequesterMismatch):
			logger.WarnCF("native", "Approval session mismatch rejected", map[string]interface{}{
				"client_id":  clientID,
				"session":    sessionKey,
				"request_id": req.RequestID,
			})
			writeError(w, http.StatusForbidden, "approval belongs to a different session", "approval_forbidden")
		case errors.Is(err, ErrApprovalNotFound):
			writeError(w, http.StatusNotFound, err.Error(), "approval_not_found")
		default:
			logger.ErrorCF("native", "Failed to handle approval", map[string]interface{}{
				"client_id":  clientID,
				"session":    sessionKey,
				"request_id": req.RequestID,
				"error":      err.Error(),
			})
			writeError(w, http.StatusInternalServerError, "approval could not be resolved", "approval_error")
		}
		return
	}

	command := handledApproval.Command
	reason := handledApproval.Reason

	n.persistApprovalMessage(sessionKey, req.RequestID, req.Approved, command, reason)

	approvalContent := "✅ Command approved"
	if !req.Approved {
		approvalContent = "❌ Command rejected"
	}

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
