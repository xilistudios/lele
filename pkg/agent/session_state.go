// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/store"
)

// KV key namespaces for durable per-session routing state. These maps used to
// be memory-only, which meant a gateway restart silently moved a session back
// to its pre-restart agent and conversation.
//
// sess:agent:<key> carries both flavours of the agent mapping: user sessions
// (written by /agent and startFreshConversation, read by getSessionAgent) and
// subagent sessions (written by the subagent session-key callback and by the
// self-healing scans in agent_providable). Subagent keys always contain a
// "subagent" segment, which is reserved in every key builder
// (routing.IsSubagentSessionKey), so the two flavours can never collide and
// loadDurableSessionState can route each entry back to the right map without a
// second namespace.
const (
	sessAliasKeyPrefix = "sess:alias:" // baseKey    -> active session key
	sessAgentKeyPrefix = "sess:agent:" // sessionKey -> agent ID
	sessSeqKeyPrefix   = "sess:seq:"   // baseKey    -> last chat:N used
)

// sessionStateKV returns the shared SQLite KV repository, or nil when SQLite is
// not available. Every durable session-state operation is guarded on this so
// the feature degrades silently to the previous memory-only behaviour.
func (al *AgentLoop) sessionStateKV() *store.KVRepo {
	if al.dbStore == nil {
		return nil
	}
	return al.dbStore.KV()
}

// setSessionAlias records base->active and mirrors it to durable storage.
func (al *AgentLoop) setSessionAlias(baseKey, activeKey string) {
	if baseKey == "" || activeKey == "" {
		return
	}
	al.sessionAliases.Store(baseKey, activeKey)
	al.kvSet(sessAliasKeyPrefix+baseKey, activeKey)
}

// setSessionAgent records session->agent and mirrors it to durable storage.
func (al *AgentLoop) setSessionAgent(sessionKey, agentID string) {
	if sessionKey == "" || agentID == "" {
		return
	}
	al.sessionAgents.Store(sessionKey, agentID)
	al.kvSet(sessAgentKeyPrefix+sessionKey, agentID)
}

// setSubagentSessionAgent is the durable variant used for subagent sessions.
// It fills subagentSessionAgent (the O(1) map the read paths consult for
// subagent keys) instead of sessionAgents, and persists it under the shared
// agent namespace.
func (al *AgentLoop) setSubagentSessionAgent(sessionKey, agentID string) {
	if sessionKey == "" || agentID == "" {
		return
	}
	al.subagentSessionAgent.Store(sessionKey, agentID)
	al.kvSet(sessAgentKeyPrefix+sessionKey, agentID)
}

// kvSet writes through to the durable KV store. A write failure is logged and
// swallowed: the in-memory map is already correct, so the session keeps working
// for this process — it only loses durability until the next write.
func (al *AgentLoop) kvSet(key, value string) {
	repo := al.sessionStateKV()
	if repo == nil {
		return
	}
	if err := repo.Set(key, value); err != nil {
		logger.WarnCF("agent", "session state: failed to persist", map[string]interface{}{
			"key":   key,
			"error": err.Error(),
		})
	}
}

// kvDelete removes a durable session-state key. Deleting a missing key is not
// an error.
func (al *AgentLoop) kvDelete(key string) {
	repo := al.sessionStateKV()
	if repo == nil {
		return
	}
	if err := repo.Delete(key); err != nil {
		logger.WarnCF("agent", "session state: failed to delete", map[string]interface{}{
			"key":   key,
			"error": err.Error(),
		})
	}
}

// deleteDurableSessionAgent drops the persisted agent override for a session key
// from both in-memory maps and KV.
func (al *AgentLoop) deleteDurableSessionAgent(sessionKey string) {
	if sessionKey == "" {
		return
	}
	al.sessionAgents.Delete(sessionKey)
	al.subagentSessionAgent.Delete(sessionKey)
	al.kvDelete(sessAgentKeyPrefix + sessionKey)
}

// deleteDurableSessionAlias drops the persisted base->active alias for a base
// session key: the in-memory map entry, the KV key, and the per-base
// conversation sequence seed (so that base can start over from chat:1).
func (al *AgentLoop) deleteDurableSessionAlias(baseKey string) {
	if baseKey == "" {
		return
	}
	al.sessionAliases.Delete(baseKey)
	al.kvDelete(sessAliasKeyPrefix + baseKey)

	al.seqSeedMu.Lock()
	delete(al.seqSeeds, baseKey)
	delete(al.seqSeedPersisted, baseKey)
	al.seqSeedMu.Unlock()

	al.kvDelete(sessSeqKeyPrefix + baseKey)
}

// Garbage collection of durable session state
//
// Aliases, agent overrides and sequence seeds are never deleted today: there is
// no per-session cleanup path for routing state anywhere in the loop (no
// sessionAliases/sessionAgents Delete call sites exist; /clear and
// resetAgentSession only wipe history, models and thinking). The durable keys
// therefore live as long as the database does, mirroring in-memory behaviour.
//
// TODO(session-state): when a session-deletion path lands (a /forget command or
// a store-side session purge), call deleteDurableSessionAlias plus
// deleteDurableSessionAgent from it so this KV namespace cannot grow
// unboundedly. Deliberately no TTL/GC here: a stale alias is harmless, while
// deleting a live one would move the user back to an old conversation.

// loadDurableSessionState rehydrates the alias/agent maps and the per-base
// conversation-key sequence from KV. Called once during NewAgentLoop, AFTER the
// shared session manager is wired. Returns counts for logging.
//
// It is best-effort: a KV error is logged and whatever could be read is kept,
// because a missing alias only degrades back to pre-fix behaviour (the session
// lands on its base conversation) instead of breaking something that works.
func (al *AgentLoop) loadDurableSessionState() (aliases, agents int) {
	repo := al.sessionStateKV()
	if repo == nil {
		return 0, 0
	}

	var maxSeed uint64
	var seqKeys, aliasKeys, agentKeys []string
	var seqErr, aliasErr, agentErr error

	// Sequence seeds first: they bound the conversation-key generator, and
	// raising the global counter here guarantees that no later /new can hand out
	// a number that was already used before the restart.
	if seqKeys, seqErr = repo.Keys(sessSeqKeyPrefix); seqErr == nil {
		for _, key := range seqKeys {
			base := strings.TrimPrefix(key, sessSeqKeyPrefix)
			raw, found, err := repo.Get(key)
			if base == "" || err != nil || !found {
				continue
			}
			n, err := strconv.ParseUint(raw, 10, 64)
			if err != nil || n == 0 {
				logger.WarnCF("agent", "session state: ignoring malformed sequence seed", map[string]interface{}{
					"key":   key,
					"value": raw,
				})
				continue
			}
			al.seqSeedMu.Lock()
			if al.seqSeeds == nil {
				al.seqSeeds = make(map[string]uint64)
			}
			if al.seqSeedPersisted == nil {
				al.seqSeedPersisted = make(map[string]uint64)
			}
			al.seqSeeds[base] = n
			// Remember it as already on disk so the first /new after a restart
			// does not rewrite the same value.
			al.seqSeedPersisted[base] = n
			al.seqSeedMu.Unlock()
			if n > maxSeed {
				maxSeed = n
			}
		}
	}
	if maxSeed > 0 {
		al.bumpSessionKeySeq(maxSeed)
	}

	if aliasKeys, aliasErr = repo.Keys(sessAliasKeyPrefix); aliasErr == nil {
		for _, key := range aliasKeys {
			base := strings.TrimPrefix(key, sessAliasKeyPrefix)
			active, found, err := repo.Get(key)
			if base == "" || err != nil || !found || active == "" {
				continue
			}
			al.sessionAliases.Store(base, active)
			aliases++
		}
	}

	if agentKeys, agentErr = repo.Keys(sessAgentKeyPrefix); agentErr == nil {
		for _, key := range agentKeys {
			sessionKey := strings.TrimPrefix(key, sessAgentKeyPrefix)
			agentID, found, err := repo.Get(key)
			if sessionKey == "" || err != nil || !found || agentID == "" {
				continue
			}
			// Subagent mappings live in their own map (the read paths only ever
			// consult subagentSessionAgent for subagent keys), so put every
			// entry back where the running code will find it.
			if routing.IsSubagentSessionKey(sessionKey) {
				al.subagentSessionAgent.Store(sessionKey, agentID)
			} else {
				al.sessionAgents.Store(sessionKey, agentID)
			}
			agents++
		}
	}

	for _, failed := range []struct {
		prefix string
		err    error
	}{{sessSeqKeyPrefix, seqErr}, {sessAliasKeyPrefix, aliasErr}, {sessAgentKeyPrefix, agentErr}} {
		if failed.err != nil {
			logger.WarnCF("agent", "session state: failed to list keys", map[string]interface{}{
				"prefix": failed.prefix,
				"error":  failed.err.Error(),
			})
		}
	}

	logger.InfoCF("agent", "Durable session state loaded", map[string]interface{}{
		"aliases":     aliases,
		"agents":      agents,
		"seq_seeds":   len(seqKeys),
		"session_seq": al.sessionKeySeq.Load(),
	})
	return aliases, agents
}

// bumpSessionKeySeq raises the global conversation-key counter to at least min,
// never lowering it. Safe to call with or without seqSeedMu held (the counter
// itself is atomic).
func (al *AgentLoop) bumpSessionKeySeq(min uint64) {
	for {
		cur := al.sessionKeySeq.Load()
		if cur >= min {
			return
		}
		if al.sessionKeySeq.CompareAndSwap(cur, min) {
			return
		}
	}
}

// nextChatSession returns the highest chat:N ever handed out for baseKey and
// persists it. It is the durable half of the fix for conversation-key reuse
// across restarts.
//
// Why BOTH the global counter and the per-base seed are consulted:
//   - sessionKeySeq is one global atomic shared by every base key, so on its own
//     it is only a lower bound for a given base: another session's /new can push
//     it far past what THIS base has used (harmless — that number was never a
//     key of this base). But it resets to 0 on every process start, so after a
//     restart it would re-issue base:chat:1, which may already exist in SQLite
//     with old history; GetOrCreate would revive that stale conversation as if
//     it were brand new.
//   - seqSeeds[base] is the exact per-base high-water mark restored from KV, but
//     it is absent for bases seen for the first time and only as fresh as the
//     last write for that base.
//
// Taking the max is correct in both directions. The whole read-modify-write
// runs under seqSeedMu, so two concurrent /new calls for the same base can
// never be handed the same number.
func (al *AgentLoop) nextChatSession(baseKey string) uint64 {
	// The counter is incremented INSIDE the lock. Drawing a number outside it
	// lets two callers finish in the opposite order: A draws 45, B draws 46, B
	// runs first and sets the base's seed to 46, then A clamps its own 45 up to
	// that seed and both hand out chat:46. Because bumpSessionKeySeq below also
	// runs under the lock, the counter is always above every number already
	// handed out, so the next Add is guaranteed to be strictly larger.
	al.seqSeedMu.Lock()
	defer al.seqSeedMu.Unlock()

	global := al.sessionKeySeq.Add(1)

	n := global
	if seed := al.seqSeeds[baseKey]; seed > n {
		n = seed
	}
	// Keep the global counter monotonic across bases as well, so a base that was
	// seeded from disk cannot later be leapfrogged by an unrelated /new.
	al.bumpSessionKeySeq(n)

	if al.seqSeeds == nil {
		al.seqSeeds = make(map[string]uint64)
	}
	if al.seqSeedPersisted == nil {
		al.seqSeedPersisted = make(map[string]uint64)
	}
	al.seqSeeds[baseKey] = n
	if al.seqSeedPersisted[baseKey] != n {
		// Write through only when the value actually moved, so a /new never
		// touches SQLite twice for the same number.
		al.seqSeedPersisted[baseKey] = n
		al.kvSet(sessSeqKeyPrefix+baseKey, strconv.FormatUint(n, 10))
	}
	return n
}

// nextConversationSessionKey builds the key for a brand-new conversation on top
// of baseSessionKey: "<base>:chat:<N>".
func (al *AgentLoop) nextConversationSessionKey(baseSessionKey string) string {
	if baseSessionKey == "" {
		return ""
	}
	return fmt.Sprintf("%s:chat:%d", baseSessionKey, al.nextChatSession(baseSessionKey))
}
