// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import (
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

// OptWireSessionID is the per-call option key (see protocoltypes).
const OptWireSessionID = protocoltypes.OptWireSessionID

// UnkeyedSessionBucket is the session id carried by egresses that have no
// session key: WireSessionID("") — hex(sha256("")), the well-known
// empty-input digest, shared by every unkeyed egress (cron jobs, standalone
// tool loops, compaction of an unknown session).
//
// The definition lives in protocoltypes — the stdlib-only leaf both transports
// can import — so the session scheme has a single source of truth; this alias
// keeps the providers API stable.
var UnkeyedSessionBucket = protocoltypes.UnkeyedSessionBucket

// WireSessionID derives the opaque, fixed-length, stable-per-conversation value
// a session header carries: blank keys map to UnkeyedSessionBucket, everything
// else to hex(sha256(trimmed key)). Hashing keeps internal identifiers (channel,
// chat id, agent routing) out of a third party's logs while staying stable
// across turns and restarts.
//
// Defined in protocoltypes (see there for the full rationale); this is a thin
// delegation so callers of the providers API keep working.
func WireSessionID(sessionKey string) string { return protocoltypes.WireSessionID(sessionKey) }

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
