package channels

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
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

	// Advisory agent_id for this send; see HintSessionAgent for why this is not
	// SetSessionAgent.
	if req.AgentID != "" {
		n.agentLoop.HintSessionAgent(sessionKey, req.AgentID)
	}

	messageID := uuid.New().String()

	attachments := n.processAttachments(req.Attachments, sessionKey)

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

	// Parse pagination params: before_id for cursor-based pagination
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

	// Build a map of tool_call_id -> tool name from assistant messages
	// This is used to populate ToolName for tool result messages
	builder := newChatHistoryBuilder(history)

	// Evicted history is served on demand from SQLite instead of being
	// materialized into memory: a client scrolling to the top of the resident
	// window pages into messages that stay out of the agent's context, so
	// reading them back never inflates RAM nor re-injects them into prompts.
	// Cursor forms:
	//   - "evicted:<seq>": the client is already in the evicted region; serve
	//     the page immediately older than that seq.
	//   - a resident ID matching the FIRST resident message (or no resident
	//     messages at all): the client just exhausted the resident window;
	//     serve the newest evicted page.
	if strings.HasPrefix(beforeID, evictedIDPrefix) {
		seq, err := strconv.Atoi(strings.TrimPrefix(beforeID, evictedIDPrefix))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid evicted cursor", "cursor_invalid")
			return
		}
		n.writeEvictedHistory(w, sessionKey, seq, limit, processing, builder)
		return
	}

	// Resident window: ONE pass that records, per visible message, its index in
	// `history` plus the occurrence index of its content key. Nothing else is
	// kept — no message copy, no id string — so the pass costs one bounded key
	// per message (residentMessageKeyBytes samples the content instead of
	// hashing it whole) rather than a SHA-256 over every message's full body.
	// The key only becomes the wire id of a message when that message lands on
	// the returned page (≤ limit, max 200).
	//
	// The cursor is resolved inside the same pass: parseResidentHistoryCursor
	// decodes the (key, occurrence) pair out of the `<sessionKey>:<key>[-<n>]`
	// id the client sent back, and the walk reports the position whose pair
	// matches. An unmatched cursor keeps the previous behaviour (start at the
	// end of the resident window, i.e. serve the newest page and/or cross into
	// the evicted region).
	cursorKey, cursorOccurrence, hasResidentCursor := parseResidentHistoryCursor(sessionKey, beforeID)

	type residentEntry struct {
		msgIdx     int
		key        residentKey
		occurrence int
	}
	entries := make([]residentEntry, 0, len(history))
	// Occurrence counters per content key: messages with identical content must
	// still receive DISTINCT ids (see residentMessageID).
	residentOccurrences := make(map[residentKey]int, len(history))
	matchedIdx := -1
	for i := range history {
		msg := &history[i]
		if !residentHistoryVisible(msg) {
			continue
		}
		// NEW: skip the in-progress streaming assistant message while the
		// session is actively processing. It is delivered live over the
		// WebSocket (streaming events + reconnect catchup); leaking it into
		// HTTP history races with the live frontend state and causes
		// ordering glitches and disappearing/reappearing messages. When the
		// session is NOT processing, a leftover Streaming=true message is a
		// crash-recovery orphan (process died mid-stream) and MUST stay
		// visible or it would be hidden forever.
		if msg.Streaming && processing {
			continue
		}
		// Stable, position-independent content key + occurrence index. The
		// first occurrence keeps the legacy id so existing pagination cursors
		// keep working; repeats get a `-<n>` suffix so ids stay unique.
		key := residentMessageKeyBytes(msg)
		occurrence := residentOccurrences[key]
		residentOccurrences[key]++
		if hasResidentCursor && matchedIdx < 0 && occurrence == cursorOccurrence && key == cursorKey {
			matchedIdx = len(entries)
		}
		entries = append(entries, residentEntry{msgIdx: i, key: key, occurrence: occurrence})
	}

	// Find the starting point based on before_id cursor
	startIdx := len(entries)
	if matchedIdx >= 0 {
		startIdx = matchedIdx
	}

	// Calculate the range to return (messages before the cursor)
	endIdx := startIdx
	if endIdx > len(entries) {
		endIdx = len(entries)
	}
	resultStartIdx := endIdx - limit
	if resultStartIdx < 0 {
		resultStartIdx = 0
	}

	// Resident window exhausted (cursor was the oldest visible resident):
	// continue paging in the evicted region. Only when a cursor was supplied:
	// the very first request (no cursor) must return the newest resident page.
	// An unmatched cursor (startIdx unchanged) means the referenced message
	// left the resident window since the last page — it was evicted or pruned.
	// Falling through to the evicted region keeps history reachable; in the
	// rare evicted case the message may reappear once under its seq-based ID.
	if beforeID != "" && resultStartIdx == 0 && len(entries) > 0 && (startIdx == 0 || startIdx == len(entries)) {
		if n.agentLoop != nil && n.agentLoop.GetEvictedMessageCount(sessionKey) > 0 {
			n.writeEvictedHistory(w, sessionKey, -1, limit, processing, builder)
			return
		}
	}
	// No resident messages at all (entire transcript evicted and the cold
	// tail is empty): the first page must come from the evicted region even
	// without a cursor, or the session would look permanently empty.
	if len(entries) == 0 && n.agentLoop != nil && n.agentLoop.GetEvictedMessageCount(sessionKey) > 0 {
		n.writeEvictedHistory(w, sessionKey, beforeSeqFromCursor(beforeID), limit, processing, builder)
		return
	}

	// Build response messages. Only this loop materializes messages (and their
	// ids) — everything older is left as an index into `history`.
	messages := make([]ChatHistoryMessage, 0, endIdx-resultStartIdx)
	for i := resultStartIdx; i < endIdx; i++ {
		e := entries[i]
		msgID := residentMessageID(sessionKey, residentKeyHex(e.key), e.occurrence)
		messages = append(messages, builder.message(msgID, history[e.msgIdx]))
	}

	// Check if there are more messages available
	hasMore := resultStartIdx > 0
	// Older messages still exist in the (unmaterialized) evicted region.
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

// evictedIDPrefix marks a chat history message ID as belonging to a message
// persisted in SQLite but NOT resident in memory. The suffix is the message's
// SQLite seq, which doubles as the pagination cursor for the evicted region.
const evictedIDPrefix = "evicted:"

// beforeSeqFromCursor extracts the seq from an "evicted:<seq>" cursor,
// returning -1 (meaning "no cursor: newest evicted page") for anything else.
func beforeSeqFromCursor(cursor string) int {
	if strings.HasPrefix(cursor, evictedIDPrefix) {
		if seq, err := strconv.Atoi(strings.TrimPrefix(cursor, evictedIDPrefix)); err == nil {
			return seq
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Resident message identity
//
// A resident message's wire id is `<sessionKey>:<key>[-<occurrence>]`, where
// `key` discriminates messages by CONTENT and `occurrence` keeps the ids of
// genuinely identical messages unique (see residentMessageID). Minting those
// ids is the resident half of GET /api/v1/chat/history, which the WebUI polls
// every ~4s while a turn streams.
//
// The key must satisfy, in order of importance:
//
//  1. Deterministic and position-independent: the same message must mint the
//     same key on every request, after appends, after pruning and after a
//     restart, or a cursor a client holds stops resolving.
//  2. Identical content MUST produce an identical key: the `-<n>` suffix only
//     exists because copies collide, so it can only work if they do.
//  3. Bounded cost per message: the key is computed for EVERY visible message
//     on every request, so its cost must not grow with len(content). Hashing
//     the full body with SHA-256 was O(session bytes) — ~15MB copied and
//     hashed per request on a 3.5k-message session to return 50 messages.
//
// It is therefore a 64-bit digest over the role, the content length, a bounded
// sample of the content and the tool-call identity fields. Sampling can only
// make two DIFFERENT messages share a key (never split identical ones), which
// is benign by construction: messages sharing a key are treated as occurrences
// of one identity, so they still get unique ids (`-1`, `-2`, …) and each id
// still resolves back to its own message through the occurrence index. This is
// a UI identity, not a security primitive.
//
// Upgrade note: wire ids are minted from this digest, so changing the digest
// changes the VALUE of every resident message's id — not merely its form. The
// layout (`<sessionKey>:<16 hex>[-<occurrence>]`) is untouched, but the 16 hex
// characters differ for every message of every session, so ids minted before
// the change (the truncated SHA-256 over the full content this key replaced)
// stop resolving. A client that is mid-scroll across such a change sends a
// cursor that no longer resolves and falls back to the newest page, which is
// the documented behaviour for any stale cursor (see
// parseResidentHistoryCursor): older pages can therefore appear once more
// until the client reloads, and no message is lost. Within one version, ids
// are stable.
// ---------------------------------------------------------------------------

// residentKeyBytes is the digest width. 8 bytes (16 hex characters) keeps the
// wire id shape — and its length — unchanged.
const residentKeyBytes = 8

// residentKey is the fixed-size identity key of a resident message. A fixed
// array (not a string) keeps the per-request occurrence index free of one
// allocation per message.
type residentKey [residentKeyBytes]byte

const (
	// residentKeySampleWindow is the size of each content window folded into
	// the key; residentKeySampleWindows is how many are spread over the
	// content (head, quarter, middle, three-quarter, tail).
	residentKeySampleWindow  = 32
	residentKeySampleWindows = 5
	// residentKeySampleSpan is where sampling starts: content up to
	// window*windows bytes is hashed whole, and that is also the maximum
	// number of content bytes any message contributes.
	residentKeySampleSpan = residentKeySampleWindow * residentKeySampleWindows
)

// residentKeyHash* are the FNV-1a 64-bit constants. The mixer only has to be
// deterministic and well spread, not collision resistant.
const (
	residentKeyHashOffset = 14695981039346656037
	residentKeyHashPrime  = 1099511628211
)

// residentHashBlock folds a byte span into the running digest, one 8-byte word
// at a time: no allocation, no unsafe, no per-byte multiply. The windows are
// short and hashed for every message on every request, so the word loop is the
// one that matters — the byte loop only ever handles the 0-7 trailing bytes
// (TestResidentMessageKeyBoundedWork pins the zero-allocation property).
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

// residentHashField folds a whole (short) field into the digest, separating
// fields by length so that ("ab","c") cannot produce the digest of ("a","bc").
func residentHashField(h uint64, field string) uint64 {
	h = (h ^ uint64(len(field))) * residentKeyHashPrime
	return residentHashBlock(h, field)
}

// residentHashContent folds the message content into the digest in bounded
// time: its length plus at most residentKeySampleSpan bytes sampled at the
// head, the quarter points, the exact middle and the tail.
//
// Two messages that share a length and differ ONLY inside an unsampled span
// therefore share a key. That is the documented, benign failure mode of a
// bounded key (see the block comment above); the sampled offsets include the
// exact middle precisely because "same prefix, different middle" is the common
// near-duplicate shape.
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

// residentMessageKeyBytes derives the identity key of a message. Callers that
// only compare keys (the per-request occurrence index and cursor match) use
// this form; wire ids embed residentKeyHex of the very same value.
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

// residentKeyHex renders a key in the exact form embedded in a wire id.
func residentKeyHex(key residentKey) string {
	return hex.EncodeToString(key[:])
}

// residentMessageKey derives the content-based discriminator of a message, as
// embedded in its wire id. It is position-independent on purpose: cursor
// pagination must survive pruning and appends. Two DIFFERENT messages can
// therefore share a key — residentMessageID appends an occurrence suffix to
// keep their ids unique.
func residentMessageKey(msg providers.Message) string {
	return residentKeyHex(residentMessageKeyBytes(&msg))
}

// parseResidentHistoryCursor decodes a resident wire id back into the key and
// occurrence it was composed from (see residentMessageID), so a cursor can be
// resolved without minting an id for every message.
//
// ok is false when the cursor is empty, belongs to the evicted region, was
// minted for another session or is not a well-formed resident id; the caller
// then treats it as an unmatched cursor, exactly like the previous id-equality
// scan did.
func parseResidentHistoryCursor(sessionKey, cursor string) (residentKey, int, bool) {
	var key residentKey
	if cursor == "" || strings.HasPrefix(cursor, evictedIDPrefix) {
		return key, 0, false
	}
	rest, ok := strings.CutPrefix(cursor, sessionKey+":")
	if !ok {
		return key, 0, false
	}
	// The occurrence suffix is `-<n>`; nothing else in the id contains a dash.
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

// residentHistoryVisible reports whether a stored message is part of the
// transcript the WebUI renders. Shared by the resident window walk and the
// evicted page so both filter alike.
func residentHistoryVisible(msg *providers.Message) bool {
	if msg.Role != "user" && msg.Role != "assistant" && msg.Role != "tool" {
		return false
	}
	// Skip injected context messages (e.g. from read_image tool)
	return !(msg.Role == "user" && msg.Content == "" && len(msg.ContentParts) > 0)
}

// residentMessageID builds the wire id of a resident message. `occurrence` is
// the 0-based index of this message among the resident messages sharing the
// same content key: occurrence 0 keeps the historical
// `<sessionKey>:<hash>` form (existing cursors keep resolving), later
// occurrences get a `-<n>` suffix. Without the suffix two identical assistant
// answers received the SAME id: the WebUI rendered them as duplicate React
// keys (one bubble silently disappeared) and `before_id` matched the first
// occurrence instead of the intended one.
func residentMessageID(sessionKey, key string, occurrence int) string {
	if occurrence <= 0 {
		return sessionKey + ":" + key
	}
	return sessionKey + ":" + key + "-" + strconv.Itoa(occurrence)
}

// newChatHistoryBuilder converts providers.Message values into the wire format,
// resolving tool_call_id -> tool name from the assistant messages that
// initiated them.
//
// The map spans the whole slice on purpose: a tool result and its tool_call can
// land in different pages (and even on different sides of the resident/evicted
// boundary — see writeEvictedHistory), so restricting it to the returned page
// would silently drop ToolName for those groups. The walk is content-free
// (role + tool-call identity only) and iterates by index, so it neither hashes
// nor copies the messages it passes over.
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
	}
	// For tool messages, look up the tool name from the assistant message that initiated the call
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

// writeEvictedHistory serves one page of the session's non-resident messages
// straight from SQLite. beforeSeq selects the page (newest evicted page when
// < 0). Messages are never added to the session's in-memory slice, so this
// keeps eviction semantics intact while making the full transcript visible.
func (n *NativeChannel) writeEvictedHistory(w http.ResponseWriter, sessionKey string, beforeSeq, limit int, processing bool, builder *chatHistoryBuilder) {
	page := n.agentLoop.LoadEvictedMessagesPage(sessionKey, beforeSeq, 0, limit)
	messages := make([]ChatHistoryMessage, 0)
	hasMore := false
	if page != nil {
		// Tool-call names usually live inside the same page (exclusion never
		// splits tool_use/tool_result groups); fall back to the resident
		// builder for groups a page boundary split.
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
			// IDs carry the persisted seq (not a slice offset): the evicted
			// region can contain gaps after PruneExcluded, and the seq is the
			// cursor the next page request needs.
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
	_, ok := n.auth.GetClient(clientID)
	if !ok {
		writeJSON(w, http.StatusOK, ChatSessionsResponse{Sessions: []ChatSession{}})
		return
	}

	offset, limit := parsePagination(r)

	// Read optional mode filter
	modeFilter := r.URL.Query().Get("mode")
	// Read optional kind filter ("chat", "heartbeat", "cron", "cron-spawn", "subagent")
	kindFilter := r.URL.Query().Get("kind")
	// include_system=true also merges persisted system sessions (heartbeat,
	// cron, subagents) even when no kind filter is given (used by the
	// session-history "all" view).
	includeSystem := r.URL.Query().Get("include_system") == "true"

	// Collect session keys from ALL native clients (unified view).
	// Native sessions are shared across all clients on the same machine.
	allClients := n.auth.ListClients()
	sessionKeySet := make(map[string]bool)
	for _, c := range allClients {
		for _, sk := range c.SessionKeys {
			sessionKeySet[sk] = true
		}
	}

	sessions := make([]ChatSession, 0, len(sessionKeySet))
	for sk := range sessionKeySet {
		// Lightweight existence check: never loads full history for the
		// message-presence test. HasMessages() checks in-memory lengths and a
		// cold SQLite count, avoiding the N+1 GetSessionHistory call.
		if !n.agentLoop.HasMessages(sk) {
			continue
		}

		// Get session mode
		sessionMode := n.agentLoop.GetSessionMode(sk)
		kind := classifySessionKeyKind(sk)

		// Apply mode filter if specified
		if modeFilter != "" {
			effectiveMode := sessionMode
			if effectiveMode == "" {
				effectiveMode = "agent"
			}
			if effectiveMode != modeFilter {
				continue
			}
		}
		// Apply kind filter if specified
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

	// Merge in every persisted session from the shared session manager
	// (heartbeat, cron, subagents, etc.) so the session-history UI can see
	// sessions that are not tracked by any native client. Duplicate keys are
	// skipped (the tracked entry above wins, preserving any client metadata).
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
			// System sessions (heartbeat/cron) may legitimately have zero
			// messages; keep them as-is so the history shows the runs happened.
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

// handleChatSessionsMeta returns lightweight session metadata WITHOUT loading
// the full history for any session. This is the fast path used by the WebUI
// sidebar.
//
// Cost model: the endpoint is paged (the client requests META_PAGE_SIZE rows at
// a time) but the result set must be globally sorted and filtered, so the whole
// inventory has to be considered on every request. That is fine as long as the
// per-request work is one pass over a prebuilt index. It used to be ~8 calls per
// session (HasMessages, GetSessionMode, GetName, GetCreated, GetUpdated,
// GetSessionFolder, classify, plus a registry walk for subagent keys), each of
// which took the session-manager lock, ran ensureLoaded and re-resolved the
// owning agent — making limit=1 as expensive as limit=200 and the sidebar load
// several seconds long on a 1k-session instance.
//
// Now: ListAllSessions() builds every field in one pass under one lock, and a
// single batched store query answers "has messages?" for all non-resident
// sessions at once.
func (n *NativeChannel) handleChatSessionsMeta(w http.ResponseWriter, r *http.Request) {
	clientID := getClientID(r)
	_, ok := n.auth.GetClient(clientID)
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

	// One pass over the shared session manager builds the whole inventory.
	index := n.agentLoop.ListAllSessions()
	indexByKey := make(map[string]SessionKindInfo, len(index))
	for _, info := range index {
		indexByKey[info.Key] = info
	}

	sessions := make([]ChatSession, 0, len(index))
	seen := make(map[string]bool, len(index))

	// Pass 1: sessions tracked by native clients (unified view across clients).
	// These are the chats the user has actually opened from the WebUI, so they
	// win over the merged inventory below and keep their HasMessages filter:
	// a tracked-but-unused session must not show up.
	for _, c := range n.auth.ListClients() {
		for _, sk := range c.SessionKeys {
			if seen[sk] {
				continue
			}
			seen[sk] = true

			// Getters resolve aliases internally, so look the resolved key up
			// in the index to stay equivalent to the per-session path.
			resolved := n.agentLoop.ResolveSessionKey(sk)
			info, known := indexByKey[resolved]
			// Kind is derived from the key the client actually tracks, exactly
			// as before: aliases normally classify the same, but keeping the
			// original input removes any doubt.
			kind := classifySessionKeyKind(sk)
			if !known {
				// Not in the persisted inventory (a key with message rows but
				// no session row). Rare enough to afford the per-key path,
				// which preserves the previous behaviour exactly.
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
	}

	// Pass 2: merge in every persisted session (heartbeat, cron, subagents,
	// chats from other channels) so the session-history UI can see sessions not
	// tracked by any native client. System sessions may have zero messages and
	// are kept as-is, matching the previous behaviour.
	if kindFilter != "" || includeSystem {
		for _, info := range index {
			if seen[info.Key] {
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

// sessionPassesFilters applies the optional mode/kind query filters. An empty
// mode is normalised to "agent", matching every other mode comparison in the
// codebase.
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

// Mirrors agent.classifySessionKind; both must stay in sync.
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
	// cron jobs that spawned a subagent: native:cron-<jobID>:subagent-<N>
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

	n.auth.TrackSessionKey(clientID, req.SessionKey)

	if req.Mode != "" {
		n.agentLoop.SetSessionMode(req.SessionKey, req.Mode)
	}

	writeJSON(w, http.StatusCreated, CreateSessionResponse{
		SessionKey: req.SessionKey,
	})
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

	// Remove the session key from ALL native clients (shared namespace).
	// Even though the requesting client initiated the deletion, the session
	// may be tracked under multiple client entries.
	removed := false
	for _, c := range n.auth.ListClients() {
		if err := n.auth.RemoveSessionKey(c.ClientID, sessionKey); err == nil {
			removed = true
		}
	}
	if !removed {
		// Session key wasn't tracked by any client — that's fine, clear it anyway
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

	// HandleApprovalForSession atomically verifies that the validated session
	// owns the approval, then finds, removes and returns it. Resolving by ID
	// alone would let any authenticated client approve another session's
	// pending exec (TUI-H2). sessionKey was already validated above via
	// validateSessionOwnership.
	handledApproval, err := n.approvalManager.HandleApprovalForSession(req.RequestID, req.Approved, sessionKey)
	if err != nil {
		if strings.Contains(err.Error(), "approval session mismatch") {
			logger.WarnCF("native", "Approval session mismatch rejected", map[string]interface{}{
				"client_id":  clientID,
				"session":    sessionKey,
				"request_id": req.RequestID,
			})
			writeError(w, http.StatusForbidden, "approval belongs to a different session", "approval_forbidden")
			return
		}
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
