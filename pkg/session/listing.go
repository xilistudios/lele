package session

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

func (sm *SessionManager) GetEvictedMessageCount(key string) int {
	sm.ensureLoaded()
	sm.mu.RLock()
	if session, ok := sm.sessions[key]; ok {
		n := session.evictedTotal
		sm.mu.RUnlock()
		return n
	}
	repo := sm.sessionRepo
	sm.mu.RUnlock()

	if repo == nil {
		return 0
	}
	meta, err := repo.GetSessionMeta(key)
	if err != nil || meta == nil || meta.FirstInMemorySeq <= 0 {
		return 0
	}
	n, err := repo.CountMessagesBefore(key, meta.FirstInMemorySeq)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (sm *SessionManager) HasMessages(key string) bool {
	if key == "" {
		return false
	}
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if session, ok := sm.sessions[key]; ok {
		return len(session.Messages) > 0 || session.evictedTotal > 0
	}
	if sm.sessionRepo != nil {
		if n, err := sm.sessionRepo.MessageCount(key); err == nil {
			return n > 0
		}
	}
	return false
}

func (sm *SessionManager) GetTotalMessageCount(key string) int {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if session, ok := sm.sessions[key]; ok {
		return len(session.Messages) + session.evictedTotal
	}
	if sm.sessionRepo != nil {
		if n, err := sm.sessionRepo.MessageCount(key); err == nil {
			return n
		}
	}
	return 0
}

func (sm *SessionManager) GetUpdated(key string) time.Time {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if session, ok := sm.sessions[key]; ok {
		return session.Updated
	}
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Updated
	}
	return time.Time{}
}

func (sm *SessionManager) GetCreated(key string) time.Time {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if session, ok := sm.sessions[key]; ok {
		return session.Created
	}
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Created
	}
	return time.Time{}
}

func (sm *SessionManager) ListSessionsByMode(mode string) []*Session {
	normalizedMode := mode
	if normalizedMode == "" {
		normalizedMode = "agent"
	}

	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	res := make([]*Session, 0)

	seen := make(map[string]bool)
	for key := range sm.sessions {
		seen[key] = true
		session := sm.sessions[key]
		effectiveMode := session.Mode
		if effectiveMode == "" {
			effectiveMode = "agent"
		}
		if effectiveMode == normalizedMode {
			res = append(res, session)
		}
	}
	for key, meta := range sm.sessionMeta {
		if !seen[key] {
			effectiveMode := meta.Mode
			if effectiveMode == "" {
				effectiveMode = "agent"
			}
			if effectiveMode == normalizedMode {
				res = append(res, &Session{
					Key:     meta.Key,
					Name:    meta.Name,
					Mode:    meta.Mode,
					Created: meta.Created,
					Updated: meta.Updated,
				})
			}
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Updated.After(res[j].Updated)
	})
	return res
}

func (sm *SessionManager) SessionExists(key string) bool {
	if key == "" {
		return false
	}
	sm.ensureLoaded()
	sm.mu.RLock()
	_, inMemory := sm.sessions[key]
	_, inMeta := sm.sessionMeta[key]
	sm.mu.RUnlock()
	if inMemory || inMeta {
		return true
	}

	if sm.sessionRepo != nil {
		exists, err := sm.sessionRepo.SessionExists(key)
		if err == nil && exists {
			return true
		}
		return false
	}

	return false
}

func (sm *SessionManager) ActiveCount() int {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessionMeta)
}

func (sm *SessionManager) ListSessions() []*Session {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	res := make([]*Session, 0, len(sm.sessionMeta))

	seen := make(map[string]bool)
	for key := range sm.sessions {
		seen[key] = true
		res = append(res, sm.sessions[key])
	}
	for key, meta := range sm.sessionMeta {
		if !seen[key] {
			res = append(res, &Session{
				Key:     meta.Key,
				Name:    meta.Name,
				Mode:    meta.Mode,
				Folder:  meta.Folder,
				Created: meta.Created,
				Updated: meta.Updated,
			})
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Updated.After(res[j].Updated)
	})
	return res
}

func (sm *SessionManager) AllMessageCounts() map[string]int {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	counts := make(map[string]int, len(sm.sessionMeta))

	for key, session := range sm.sessions {
		count := 0
		for _, msg := range session.Messages {
			if msg.Role == "user" || msg.Role == "assistant" {
				if msg.Role == "user" && msg.Content == "" && len(msg.ContentParts) > 0 {
					continue
				}
				count++
			}
		}
		counts[key] = count
	}

	if sm.sessionRepo != nil && sm.hasColdSessionsLocked() {
		storeCounts := sm.coldStoreCounts()
		for key := range sm.sessionMeta {
			if _, ok := sm.sessions[key]; ok {
				continue
			}
			if c, ok := storeCounts[key]; ok {
				counts[key] = c
			}
		}
	}

	return counts
}

type storeCountsCache struct {
	mu     sync.Mutex
	counts map[string]int
	cold   map[string]struct{}
	at     time.Time
	valid  bool
}

const storeCountsTTL = time.Second

func (sm *SessionManager) coldStoreCounts() map[string]int {
	c := &sm.countsCache
	c.mu.Lock()
	if c.valid && time.Since(c.at) < storeCountsTTL && sm.coldSetMatchesLocked(c.cold) {
		counts := c.counts
		c.mu.Unlock()
		return counts
	}
	c.mu.Unlock()

	sm.mu.RUnlock()
	storeCounts, err := sm.sessionRepo.AllMessageCounts()
	sm.mu.RLock()
	if err != nil {
		return nil
	}

	c.mu.Lock()
	c.counts = storeCounts
	c.cold = sm.coldKeySetLocked()
	c.at = time.Now()
	c.valid = true
	c.mu.Unlock()
	return storeCounts
}

func (sm *SessionManager) coldSetMatchesLocked(cached map[string]struct{}) bool {
	if cached == nil {
		return false
	}
	cold := 0
	for key := range sm.sessionMeta {
		if _, ok := sm.sessions[key]; ok {
			continue
		}
		cold++
		if _, ok := cached[key]; !ok {
			return false
		}
	}
	return cold == len(cached)
}

func (sm *SessionManager) coldKeySetLocked() map[string]struct{} {
	cold := make(map[string]struct{})
	for key := range sm.sessionMeta {
		if _, ok := sm.sessions[key]; ok {
			continue
		}
		cold[key] = struct{}{}
	}
	return cold
}

func (sm *SessionManager) hasColdSessionsLocked() bool {
	for key := range sm.sessionMeta {
		if _, ok := sm.sessions[key]; !ok {
			return true
		}
	}
	return false
}

func (sm *SessionManager) AllTotalMessageCounts() map[string]int {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	counts := make(map[string]int, len(sm.sessionMeta))

	for key, session := range sm.sessions {
		counts[key] = len(session.Messages) + session.evictedTotal
	}

	if sm.sessionRepo != nil && sm.hasColdSessionsLocked() {
		storeCounts := sm.coldStoreCounts()
		for key := range sm.sessionMeta {
			if _, ok := sm.sessions[key]; ok {
				continue
			}
			if c, ok := storeCounts[key]; ok {
				counts[key] = c
			}
		}
	}

	return counts
}

type SubagentSessionInfo struct {
	Key        string
	TaskID     string
	Created    time.Time
	Updated    time.Time
	Iterations int
	Summary    string
	Name       string
	AgentID    string
	Status     string
}

func subagentSummary(summary, lastAssistantContent string) string {
	if summary != "" {
		return summary
	}
	content := strings.TrimSpace(lastAssistantContent)
	if len(content) > 200 {
		content = content[:200] + "…"
	}
	return content
}

func decodeLastAssistantContent(messageJSON string) string {
	if messageJSON == "" {
		return ""
	}
	var msg providers.Message
	if err := json.Unmarshal([]byte(messageJSON), &msg); err != nil {
		return ""
	}
	return msg.Content
}

func subagentInfoFromSession(key, parentPrefix string, session *Session) SubagentSessionInfo {
	iterations := 0
	lastAssistantContent := ""
	haveLastAssistant := false
	for i := len(session.Messages) - 1; i >= 0; i-- {
		if session.Messages[i].Role != "assistant" {
			continue
		}
		iterations++
		if !haveLastAssistant {
			lastAssistantContent = session.Messages[i].Content
			haveLastAssistant = true
		}
	}
	return SubagentSessionInfo{
		Key:        key,
		TaskID:     key[len(parentPrefix)+1:],
		Created:    session.Created,
		Updated:    session.Updated,
		Iterations: iterations,
		Summary:    subagentSummary(session.Summary, lastAssistantContent),
		Name:       session.Name,
		AgentID:    session.AgentID,
		Status:     session.SubagentStatus,
	}
}

func (sm *SessionManager) FindSubagentSessions(parentPrefix string) []SubagentSessionInfo {
	sm.ensureLoaded()

	prefix := parentPrefix + ":subagent-"

	var (
		results  []SubagentSessionInfo
		cold     []SubagentSessionInfo
		coldKeys []string
	)

	sm.mu.RLock()
	for key, session := range sm.sessions {
		if session == nil || !strings.HasPrefix(key, prefix) {
			continue
		}
		results = append(results, subagentInfoFromSession(key, parentPrefix, session))
	}
	for key, meta := range sm.sessionMeta {
		if meta == nil || !strings.HasPrefix(key, prefix) {
			continue
		}
		if _, resident := sm.sessions[key]; resident {
			continue
		}
		cold = append(cold, SubagentSessionInfo{
			Key:     key,
			TaskID:  key[len(parentPrefix)+1:],
			Created: meta.Created,
			Updated: meta.Updated,
			Name:    meta.Name,
			AgentID: meta.AgentID,
			Status:  meta.SubagentStatus,
		})
		coldKeys = append(coldKeys, key)
	}
	repo := sm.sessionRepo
	sm.mu.RUnlock()

	if len(coldKeys) == 0 || repo == nil {
		return results
	}

	stats, err := repo.SessionListingStats(coldKeys)
	if err != nil {
		logger.WarnCF("session", "Failed to read subagent listing stats; omitting metadata-only subagents", map[string]interface{}{
			"error": err.Error(),
			"count": len(coldKeys),
		})
		return results
	}

	for i, key := range coldKeys {
		stat, ok := stats[key]
		if !ok {
			continue
		}
		info := cold[i]
		info.Iterations = stat.AssistantCount
		info.Summary = subagentSummary(stat.Summary, decodeLastAssistantContent(stat.LastAssistantJSON))
		results = append(results, info)
	}

	return results
}

type SessionIndexEntry struct {
	Key         string
	Name        string
	Mode        string
	Folder      string
	Created     time.Time
	Updated     time.Time
	Resident    bool
	HasMessages bool
}

func (sm *SessionManager) ListSessionIndex() []SessionIndexEntry {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	res := make([]SessionIndexEntry, 0, len(sm.sessions)+len(sm.sessionMeta))
	seen := make(map[string]bool, len(sm.sessions)+len(sm.sessionMeta))

	for key, session := range sm.sessions {
		if session == nil {
			continue
		}
		seen[key] = true
		res = append(res, SessionIndexEntry{
			Key:         key,
			Name:        session.Name,
			Mode:        session.Mode,
			Folder:      session.Folder,
			Created:     session.Created,
			Updated:     session.Updated,
			Resident:    true,
			HasMessages: len(session.Messages) > 0 || session.evictedTotal > 0,
		})
	}

	for key, meta := range sm.sessionMeta {
		if seen[key] || meta == nil {
			continue
		}
		res = append(res, SessionIndexEntry{
			Key:      key,
			Name:     meta.Name,
			Mode:     meta.Mode,
			Folder:   meta.Folder,
			Created:  meta.Created,
			Updated:  meta.Updated,
			Resident: false,
		})
	}

	return res
}

func (sm *SessionManager) SessionKeysWithMessages() map[string]bool {
	sm.ensureLoaded()
	sm.mu.RLock()
	repo := sm.sessionRepo
	sm.mu.RUnlock()

	if repo == nil {
		return nil
	}
	keys, err := repo.SessionKeysWithMessages()
	if err != nil {
		return nil
	}
	return keys
}

type SubagentSessionMeta struct {
	Key       string
	TaskID    string
	ParentKey string
	Name      string
	Status    string
	AgentID   string
	Created   time.Time
	Updated   time.Time
	Resident  bool
}

type SubagentSessionDetail struct {
	Iterations int
	Summary    string
}

const subagentSessionMarker = ":subagent-"

func subagentParentKey(key string) string {
	i := strings.LastIndex(key, subagentSessionMarker)
	if i <= 0 {
		return ""
	}
	return key[:i]
}

func (sm *SessionManager) ListSubagentSessionMeta(parentPrefixes []string) []SubagentSessionMeta {
	if len(parentPrefixes) == 0 {
		return nil
	}

	prefixes := make([]string, 0, len(parentPrefixes))
	for _, parent := range parentPrefixes {
		if parent != "" {
			prefixes = append(prefixes, parent+":subagent-")
		}
	}
	if len(prefixes) == 0 {
		return nil
	}

	matches := func(key string) bool {
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
		return false
	}

	sm.ensureLoaded()

	results := make([]SubagentSessionMeta, 0, len(prefixes))
	seen := make(map[string]bool, len(prefixes))

	sm.mu.RLock()
	for key, session := range sm.sessions {
		if session == nil || !matches(key) {
			continue
		}
		parent := subagentParentKey(key)
		if parent == "" {
			continue
		}
		seen[key] = true
		results = append(results, SubagentSessionMeta{
			Key:       key,
			TaskID:    key[len(parent)+1:],
			ParentKey: parent,
			Name:      session.Name,
			Status:    session.SubagentStatus,
			AgentID:   session.AgentID,
			Created:   session.Created,
			Updated:   session.Updated,
			Resident:  true,
		})
	}
	for key, meta := range sm.sessionMeta {
		if meta == nil || seen[key] || !matches(key) {
			continue
		}
		parent := subagentParentKey(key)
		if parent == "" {
			continue
		}
		seen[key] = true
		results = append(results, SubagentSessionMeta{
			Key:       key,
			TaskID:    key[len(parent)+1:],
			ParentKey: parent,
			Name:      meta.Name,
			Status:    meta.SubagentStatus,
			AgentID:   meta.AgentID,
			Created:   meta.Created,
			Updated:   meta.Updated,
			Resident:  false,
		})
	}
	sm.mu.RUnlock()

	return results
}

func (sm *SessionManager) SubagentSessionDetails(keys []string) map[string]SubagentSessionDetail {
	out := make(map[string]SubagentSessionDetail, len(keys))
	if len(keys) == 0 {
		return out
	}

	unique := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, key)
	}
	if len(unique) == 0 {
		return out
	}

	sm.ensureLoaded()

	repo := repoFor(sm)

	var coldKeys []string
	sm.mu.RLock()
	for _, key := range unique {
		session, resident := sm.sessions[key]
		if !resident || session == nil {
			coldKeys = append(coldKeys, key)
			continue
		}
		iterations := 0
		lastAssistantContent := ""
		haveLastAssistant := false
		for i := len(session.Messages) - 1; i >= 0; i-- {
			if session.Messages[i].Role != "assistant" {
				continue
			}
			iterations++
			if !haveLastAssistant {
				lastAssistantContent = session.Messages[i].Content
				haveLastAssistant = true
			}
		}
		out[key] = SubagentSessionDetail{
			Iterations: iterations,
			Summary:    subagentSummary(session.Summary, lastAssistantContent),
		}
	}
	sm.mu.RUnlock()

	if len(coldKeys) == 0 || repo == nil {
		return out
	}

	stats, err := repo.SessionListingStats(coldKeys)
	if err != nil {
		logger.WarnCF("session", "Failed to read subagent session details; omitting metadata-only subagents", map[string]interface{}{
			"error": err.Error(),
			"count": len(coldKeys),
		})
		return out
	}

	for _, key := range coldKeys {
		stat, ok := stats[key]
		if !ok {
			continue
		}
		out[key] = SubagentSessionDetail{
			Iterations: stat.AssistantCount,
			Summary:    subagentSummary(stat.Summary, decodeLastAssistantContent(stat.LastAssistantJSON)),
		}
	}

	return out
}
