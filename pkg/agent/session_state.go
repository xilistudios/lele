package agent

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/store"
)

const (
	sessAliasKeyPrefix = "sess:alias:"
	sessAgentKeyPrefix = "sess:agent:"
	sessSeqKeyPrefix   = "sess:seq:"
)

func (al *AgentLoop) sessionStateKV() *store.KVRepo {
	if al.dbStore == nil {
		return nil
	}
	return al.dbStore.KV()
}

func (al *AgentLoop) setSessionAlias(baseKey, activeKey string) {
	if baseKey == "" || activeKey == "" {
		return
	}
	al.sessionAliases.Store(baseKey, activeKey)
	al.kvSet(sessAliasKeyPrefix+baseKey, activeKey)
}

func (al *AgentLoop) setSessionAgent(sessionKey, agentID string) {
	if sessionKey == "" || agentID == "" {
		return
	}
	al.sessionAgents.Store(sessionKey, agentID)
	al.kvSet(sessAgentKeyPrefix+sessionKey, agentID)
}

func (al *AgentLoop) setSubagentSessionAgent(sessionKey, agentID string) {
	if sessionKey == "" || agentID == "" {
		return
	}
	al.subagentSessionAgent.Store(sessionKey, agentID)
	al.kvSet(sessAgentKeyPrefix+sessionKey, agentID)
}

func (al *AgentLoop) persistSubagentSessionAgent(ownerAgentID, sessionKey, executorAgentID string) {
	if al.registry == nil || sessionKey == "" || executorAgentID == "" {
		return
	}
	agent, ok := al.registry.GetAgent(ownerAgentID)
	if !ok || agent == nil || agent.Sessions == nil {
		return
	}
	agent.Sessions.SetSubagentAgentID(sessionKey, executorAgentID)
}

const maxSessionOriginsPerKey = 4

func (al *AgentLoop) setSessionOrigin(sessionKey, originKey string) {
	if sessionKey == "" || originKey == "" {
		return
	}
	al.sessionOriginsMu.Lock()
	defer al.sessionOriginsMu.Unlock()
	if al.sessionOrigins == nil {
		al.sessionOrigins = make(map[string][]string)
	}

	origins := al.sessionOrigins[sessionKey]
	kept := make([]string, 0, len(origins)+1)
	kept = append(kept, originKey)
	for _, existing := range origins {
		if existing != originKey {
			kept = append(kept, existing)
		}
	}
	if len(kept) > maxSessionOriginsPerKey {
		kept = kept[:maxSessionOriginsPerKey]
	}
	al.sessionOrigins[sessionKey] = kept
}

func (al *AgentLoop) sessionOriginsFor(sessionKey string) []string {
	if sessionKey == "" {
		return nil
	}
	al.sessionOriginsMu.Lock()
	defer al.sessionOriginsMu.Unlock()
	origins := al.sessionOrigins[sessionKey]
	if len(origins) == 0 {
		return nil
	}
	out := make([]string, len(origins))
	copy(out, origins)
	return out
}

func (al *AgentLoop) sessionOrigin(sessionKey string) string {
	origins := al.sessionOriginsFor(sessionKey)
	if len(origins) == 0 {
		return ""
	}
	return origins[0]
}

func (al *AgentLoop) subagentSessionPin(sessionKey string) string {
	if v, ok := al.subagentSessionAgent.Load(sessionKey); ok {
		if agentID, isString := v.(string); isString {
			return agentID
		}
	}
	return ""
}

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

func (al *AgentLoop) deleteDurableSessionAgent(sessionKey string) {
	if sessionKey == "" {
		return
	}
	al.sessionAgents.Delete(sessionKey)
	al.subagentSessionAgent.Delete(sessionKey)
	al.kvDelete(sessAgentKeyPrefix + sessionKey)
}

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

func (al *AgentLoop) loadDurableSessionState() (aliases, agents int) {
	repo := al.sessionStateKV()
	if repo == nil {
		return 0, 0
	}

	var maxSeed uint64
	var seqKeys, aliasKeys, agentKeys []string
	var seqErr, aliasErr, agentErr error

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

func (al *AgentLoop) nextChatSession(baseKey string) uint64 {
	al.seqSeedMu.Lock()
	defer al.seqSeedMu.Unlock()

	global := al.sessionKeySeq.Add(1)

	n := global
	if seed := al.seqSeeds[baseKey]; seed > n {
		n = seed
	}
	al.bumpSessionKeySeq(n)

	if al.seqSeeds == nil {
		al.seqSeeds = make(map[string]uint64)
	}
	if al.seqSeedPersisted == nil {
		al.seqSeedPersisted = make(map[string]uint64)
	}
	al.seqSeeds[baseKey] = n
	if al.seqSeedPersisted[baseKey] != n {
		al.seqSeedPersisted[baseKey] = n
		al.kvSet(sessSeqKeyPrefix+baseKey, strconv.FormatUint(n, 10))
	}
	return n
}

func (al *AgentLoop) nextConversationSessionKey(baseSessionKey string) string {
	if baseSessionKey == "" {
		return ""
	}
	return fmt.Sprintf("%s:chat:%d", baseSessionKey, al.nextChatSession(baseSessionKey))
}
