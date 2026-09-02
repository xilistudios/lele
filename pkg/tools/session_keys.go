// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/lele
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package tools

import "strings"

// SessionKeysRelated reports whether two session keys belong to the same
// logical session family.
//
// Session keys are colon-qualified strings built by different subsystems with
// different levels of detail for the SAME logical session:
//
//	routed runtime key   "agent:main:telegram:123"
//	conversation alias   "agent:main:telegram:123:chat:2"
//	subagent child key   "telegram:123:subagent-1"
//	spawn origin key     BuildOriginSessionKey(channel, chatID) = "telegram:123"
//	native bare key      "<uuid>" (frontend) vs "native:<uuid>" (origin)
//
// Equality alone never matches across these forms, which is the root cause of
// issue #230 (/stop not cancelling subagents and background processes). This is
// the single shared predicate that treats two keys as related when either one
// is a segment-aligned suffix or prefix of the other (the channel/agent prefix
// is metadata, not identity).
//
// Rules:
//   - empty keys are never related (guards against a "" matching everything);
//   - exact equality is related;
//   - "b" relates to "a" when b ends with a, or starts with a, aligned to a
//     ':' segment boundary (and symmetrically). The suffix direction covers
//     channel-qualified vs bare keys ("native:<uuid>" vs "<uuid>",
//     "agent:main:telegram:123" vs "telegram:123"); the prefix direction
//     covers parent→alias ("k" vs "k:chat:1") and parent→subagent child
//     ("k" vs "k:subagent-1").
//   - siblings stay unrelated: "k:chat:1" vs "k:chat:2" and
//     "native:<uuid>" vs "telegram:<uuid>" share no segment-aligned prefix or
//     suffix.
func SessionKeysRelated(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return sessionKeyHasSuffix(a, b) || sessionKeyHasSuffix(b, a) ||
		sessionKeyHasPrefix(a, b) || sessionKeyHasPrefix(b, a)
}

// sessionKeyHasSuffix reports whether long ends with short aligned to a ':'
// segment boundary.
func sessionKeyHasSuffix(long, short string) bool {
	if len(long) <= len(short) {
		return false
	}
	if !strings.HasSuffix(long, short) {
		return false
	}
	return long[len(long)-len(short)-1] == ':'
}

// sessionKeyHasPrefix reports whether long starts with short aligned to a ':'
// segment boundary.
func sessionKeyHasPrefix(long, short string) bool {
	if len(long) <= len(short) {
		return false
	}
	if !strings.HasPrefix(long, short) {
		return false
	}
	return long[len(short)] == ':'
}

// taskBelongsToSession reports whether a subagent task was spawned by (or is
// otherwise part of) any of the given session keys. It checks every identity
// the task carries:
//   - SpawnerSessionKey: the runtime session key captured at spawn time (most
//     precise; empty for tasks spawned outside an agent turn);
//   - OriginSessionKey: the "<channel>:<chatID>" key built from message
//     routing;
//   - OriginSessionKey + ":" + task ID: the subagent's own child session key,
//     so a stop issued against the child session also reaches the task.
func taskBelongsToSession(spawnerKey, originKey, taskID string, keys []string) bool {
	childKey := ""
	if originKey != "" {
		childKey = originKey + ":" + taskID
	}
	for _, k := range keys {
		if SessionKeysRelated(spawnerKey, k) ||
			SessionKeysRelated(originKey, k) ||
			SessionKeysRelated(childKey, k) {
			return true
		}
	}
	return false
}

// TaskBelongsToSession is the exported form of taskBelongsToSession used by
// the agent layer to match tasks against a session (and its aliases).
func TaskBelongsToSession(task *SubagentTask, keys []string) bool {
	if task == nil {
		return false
	}
	return taskBelongsToSession(task.SpawnerSessionKey, task.OriginSessionKey, task.ID, keys)
}

// taskOwnershipKey returns the session key a running subagent's own tool
// loop should attribute its work to: the spawner's runtime key when known,
// otherwise the routing-derived origin key. Nested spawns and background
// processes started by the subagent inherit this ownership, so cancelling the
// owner's session cascades through the whole tree (#230).
func taskOwnershipKey(task *SubagentTask) string {
	if task == nil {
		return ""
	}
	if task.SpawnerSessionKey != "" {
		return task.SpawnerSessionKey
	}
	return task.OriginSessionKey
}
