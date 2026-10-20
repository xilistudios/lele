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
// caching.
//
// The empty key (or a blank one) is NOT a special "no id" case: it hashes to
// hex(sha256("")) — the shared UNKEYED BUCKET. Egresses that genuinely have no
// session key (cron jobs, standalone tool loops, compaction of an unknown
// session) all land in that one stable bucket, so they still satisfy providers
// that require the header instead of aborting locally. The scheme is the
// simplest deterministic one: trim, then SHA-256 the key — a blank key is
// simply the well-known empty-input digest below.
func WireSessionID(sessionKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(sessionKey)))
	return hex.EncodeToString(sum[:])
}

// WithWireSessionID returns opts with the conversation-scoped wire session id
// stamped in. It ALWAYS stamps: the presence of the option is the caller's
// statement "this egress remembered to stamp", the value is the conversation
// bucket (see WireSessionID — an empty key yields the shared unkeyed bucket).
// There is no skip-on-empty path anymore; a required provider would otherwise
// reject every unkeyed egress (cron, standalone loops) with
// common.ErrMissingWireSession.
//
// The input map is never mutated: buildLLMOptions' map is shared by the retry
// chain and by every fallback candidate, so an in-place write would let one
// candidate's identity leak into another's request.
func WithWireSessionID(opts map[string]interface{}, sessionKey string) map[string]interface{} {
	id := WireSessionID(sessionKey)
	out := make(map[string]interface{}, len(opts)+1)
	for k, v := range opts {
		out[k] = v
	}
	out[OptWireSessionID] = id
	return out
}
