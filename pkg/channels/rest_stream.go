package channels

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
)

const (
	// restStreamDeadline is the maximum time an SSE stream stays open waiting for
	// the agent to finish. If the agent hangs, the stream is closed with an error
	// event instead of leaking a connection indefinitely.
	restStreamDeadline = 5 * time.Minute
)

type restStreamEvent struct {
	event string
	data  interface{}
}

type restStreamSubscriber struct {
	id         string
	sessionKey string
	messageID  string
	ch         chan restStreamEvent
}

func (n *NativeChannel) registerRESTStreamSubscriber(sessionKey, messageID string) *restStreamSubscriber {
	sub := &restStreamSubscriber{
		id:         uuid.New().String(),
		sessionKey: sessionKey,
		messageID:  messageID,
		// Buffer 128 events — large enough to absorb bursts of streaming chunks,
		// tool events, and completion signals without blocking the publisher.
		ch: make(chan restStreamEvent, 128),
	}

	n.mu.Lock()
	if n.restStreams == nil {
		n.restStreams = make(map[string]*restStreamSubscriber)
	}
	n.restStreams[sub.id] = sub
	n.mu.Unlock()

	return sub
}

func (n *NativeChannel) unregisterRESTStreamSubscriber(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	sub, ok := n.restStreams[id]
	if !ok {
		return
	}
	delete(n.restStreams, id)
	close(sub.ch)
}

func (n *NativeChannel) emitNativeEvent(sessionKey, event string, data interface{}, messageID string) {
	n.sendWSEvent(sessionKey, event, data)
	n.fanoutRESTStream(sessionKey, event, data, messageID)
}

func (n *NativeChannel) fanoutRESTStream(sessionKey, event string, data interface{}, messageID string) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	for _, sub := range n.restStreams {
		if !sessionKeyMatches(sub.sessionKey, sessionKey) {
			continue
		}
		if sub.messageID != "" && messageID != "" && sub.messageID != messageID {
			continue
		}

		select {
		case sub.ch <- restStreamEvent{event: event, data: data}:
		default:
		}
	}
}

func writeSSE(w http.ResponseWriter, event string, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
	return err
}

func (n *NativeChannel) handleChatSendStream(w http.ResponseWriter, r *http.Request) {
	var req ChatSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required", "content_missing")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported", "stream_unavailable")
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
	sub := n.registerRESTStreamSubscriber(sessionKey, messageID)
	defer n.unregisterRESTStreamSubscriber(sub.id)

	attachments := n.processAttachments(req.Attachments, sessionKey)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if err := writeSSE(w, "message.ack", ChatSendResponse{
		MessageID:  messageID,
		SessionKey: sessionKey,
	}); err != nil {
		return
	}
	flusher.Flush()

	n.bus.PublishInbound(bus.InboundMessage{
		Channel:     ChannelName,
		SenderID:    clientID,
		ChatID:      sessionKey,
		Content:     req.Content,
		Attachments: attachments,
		SessionKey:  sessionKey,
		Metadata:    map[string]string{"message_id": messageID},
	})

	// Stream events to the client until the message is complete,
	// an error occurs, the client disconnects, or the deadline expires.
	deadline := time.After(restStreamDeadline)
	completeSeen := false
	historySeen := false

	streamDone := func() bool {
		return completeSeen && historySeen
	}

	for {
		select {
		case evt, ok := <-sub.ch:
			if !ok {
				return
			}
			if err := writeSSE(w, evt.event, evt.data); err != nil {
				return
			}
			flusher.Flush()

			switch evt.event {
			case "message.complete":
				completeSeen = true
			case "history.updated":
				historySeen = true
			case "error", "cancel.ack":
				return
			}

			if streamDone() {
				return
			}

		case <-deadline:
			logger.WarnCF("native", "SSE stream deadline reached; closing connection", map[string]interface{}{
				"session_key":   sessionKey,
				"message_id":    messageID,
				"complete_seen": completeSeen,
				"history_seen":  historySeen,
			})
			writeSSE(w, "error", map[string]interface{}{
				"message_id":  messageID,
				"session_key": sessionKey,
				"error":       "stream timeout",
				"error_code":  "stream_timeout",
			})
			flusher.Flush()
			return

		case <-r.Context().Done():
			return
		}
	}
}
