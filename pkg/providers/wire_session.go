// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

// OptWireSessionID is the per-call option key (see protocoltypes).
const OptWireSessionID = protocoltypes.OptWireSessionID

// WireSessionID derives the opaque, fixed-length, stable-per-conversation value
// a session header carries. Hashing keeps internal identifiers (channel, chat
// id, agent routing) out of a third party's logs while staying stable across
// turns and restarts — which is what the provider needs for routing and prompt
// caching. Empty session key ⇒ "" (caller must not stamp anything).
func WireSessionID(sessionKey string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionKey))
	return hex.EncodeToString(sum[:])
}

// WithWireSessionID returns opts with the conversation-scoped wire session id
// stamped in, or opts unchanged when there is no session key.
//
// The input map is never mutated: buildLLMOptions' map is shared by the retry
// chain and by every fallback candidate, so an in-place write would let one
// candidate's identity leak into another's request.
func WithWireSessionID(opts map[string]interface{}, sessionKey string) map[string]interface{} {
	id := WireSessionID(sessionKey)
	if id == "" {
		return opts
	}
	out := make(map[string]interface{}, len(opts)+1)
	for k, v := range opts {
		out[k] = v
	}
	out[OptWireSessionID] = id
	return out
}
