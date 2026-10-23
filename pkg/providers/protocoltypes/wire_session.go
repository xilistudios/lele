// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package protocoltypes

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// OptWireSessionID carries the conversation-scoped id a transport may need to
// stamp on the wire (value: string).
//
// Transport-neutral on purpose: the transport knows the HEADER NAME (static per
// provider instance), the caller knows the VALUE (conversation-scoped, so it
// must travel per call — providers are constructed per call, see
// pkg/providers/factory.go createProviderFromSelection).
//
// Deliberately NOT "session_id": that key is already read by the Gemini Code
// Assist transport as a BODY field (geminiSessionIDFromOptions), so reusing it
// would silently change another provider's wire format.
const OptWireSessionID = "wire_session_id"

// WireSessionFromOptions returns the caller-supplied wire session id, or "".
func WireSessionFromOptions(options map[string]interface{}) string {
	if options == nil {
		return ""
	}
	v, ok := options[OptWireSessionID].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// hashSessionKey is the whole session-id scheme: SHA-256 the key and hex-encode
// it (callers pre-trim; the empty key is handled by WireSessionID via
// UnkeyedSessionBucket).
func hashSessionKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// UnkeyedSessionBucket is the session id carried by egresses that have no
// session key: WireSessionID("") — hex(sha256("")), the well-known
// empty-input digest. It is a named symbol (review NIT-3) so a reader who
// sees e3b0c442…b855 in a gateway log has something to grep back to this
// definition.
//
// Egresses that genuinely have no session key (cron jobs, standalone tool
// loops, compaction of an unknown session) all land in that one stable
// bucket, so they still satisfy providers that require the header instead of
// aborting locally. The value is identical across installations by design —
// there is no per-install salt (review INFO-1: unkeyed egresses are
// indistinguishable between installs).
var UnkeyedSessionBucket = hashSessionKey("")

// WireSessionID derives the opaque, fixed-length, stable-per-conversation value
// a session header carries. Hashing keeps internal identifiers (channel, chat
// id, agent routing) out of a third party's logs while staying stable across
// turns and restarts — which is what the provider needs for routing and prompt
// caching.
//
// The empty key (or a blank one) is NOT a special "no id" case: it maps to
// UnkeyedSessionBucket — the shared UNKEYED BUCKET, hex(sha256("")). Egresses
// that genuinely have no session key (cron jobs, standalone tool loops,
// compaction of an unknown session) all land in that one stable bucket, so
// they still satisfy providers that require the header instead of aborting
// locally. The scheme is the simplest deterministic one: trim, then SHA-256
// the key — a blank key is simply the well-known empty-input digest named
// above.
func WireSessionID(sessionKey string) string {
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return UnkeyedSessionBucket
	}
	return hashSessionKey(key)
}

// WireSessionForHeaders returns the wire session id a request's headers should
// carry for this call's options map: the caller-supplied id when present,
// otherwise UnkeyedSessionBucket.
//
// It exists because a header value can never be empty — an empty value would
// drop the header, and a provider that requires one would then reject every
// unkeyed egress (cron jobs, standalone tool loops) — while
// WireSessionFromOptions deliberately reports "no id" as "". Callers hand it
// the same options map the transport reads, so an unstamped call lands in the
// exact bucket WireSessionID("") yields: one scheme, one value, whether the
// header came from gateway detection or from a user's custom header template.
func WireSessionForHeaders(options map[string]interface{}) string {
	if id := WireSessionFromOptions(options); id != "" {
		return id
	}
	return UnkeyedSessionBucket
}
