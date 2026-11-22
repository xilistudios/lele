package tools

import (
	"sort"
	"strconv"
	"strings"
)

type SubagentLedgerEntry struct {
	ID             string
	Label          string
	AgentID        string
	Status         string
	Summary        string
	Progress       string
	ContextRequest string
	Created        int64
	Updated        int64
	Iterations     int
	RetryCount     int
	MaxRetries     int
	Persisted      bool
	SpawnerKey     string
	OriginKey      string
	SessionKey     string
}

type PersistedSubagentTask struct {
	TaskID     string
	SessionKey string
	ParentKey  string
	Label      string
	Status     string
	AgentID    string
	Created    int64
	Updated    int64
}

type PersistedSubagentDetail struct {
	Iterations int
	Summary    string
}

func (sm *SubagentManager) SetLedgerWiring(
	lister func(parentPrefixes []string) []PersistedSubagentTask,
	sessionExists func(sessionKey string) bool,
) {
	if sm == nil {
		return
	}
	if lister == nil || sessionExists == nil {
		lister, sessionExists = nil, nil
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.persistedLister = lister
	sm.sessionExists = sessionExists
}

func (sm *SubagentManager) LedgerWiringEnabled() bool {
	if sm == nil {
		return false
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.persistedLister != nil && sm.sessionExists != nil
}

func (sm *SubagentManager) SetPersistedTaskDetail(fn func(keys []string) map[string]PersistedSubagentDetail) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.persistedDetail = fn
}

func (sm *SubagentManager) PersistedTaskDetail() func(keys []string) map[string]PersistedSubagentDetail {
	if sm == nil {
		return nil
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.persistedDetail
}

func (sm *SubagentManager) SetPersistedTaskExecutorFallback(fn func(sessionKey string) string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.persistedExecutor = fn
}

func (sm *SubagentManager) PersistedTaskExecutorFallback() func(sessionKey string) string {
	if sm == nil {
		return nil
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.persistedExecutor
}

func (sm *SubagentManager) LedgerEntries(scopeKeys []string) []SubagentLedgerEntry {
	if sm == nil {
		return nil
	}
	return sm.ledgerEntriesFrom(sm.ListTasks(), scopeKeys)
}

func (sm *SubagentManager) ledgerEntriesFrom(tasks []*SubagentTask, scopeKeys []string) []SubagentLedgerEntry {
	if sm == nil {
		return nil
	}

	sm.mu.RLock()
	lister := sm.persistedLister
	sm.mu.RUnlock()

	var persisted []PersistedSubagentTask
	if lister != nil {
		persisted = lister(scopePrefixesFor(scopeKeys))
	}

	entries := make([]SubagentLedgerEntry, 0, len(tasks)+len(persisted))
	seen := make(map[string]bool, len(tasks)+len(persisted))

	for _, task := range tasks {
		if task == nil {
			continue
		}
		if !ledgerScopeMatches(task, scopeKeys) {
			continue
		}
		entry := ledgerEntryFromTask(task)
		if seen[entry.SessionKey] {
			continue
		}
		seen[entry.SessionKey] = true
		entries = append(entries, entry)
	}

	for _, row := range persisted {
		if row.SessionKey == "" || seen[row.SessionKey] {
			continue
		}
		seen[row.SessionKey] = true
		entries = append(entries, ledgerEntryFromPersisted(row))
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return ledgerEntryLess(entries[i], entries[j])
	})

	return entries
}

func scopePrefixesFor(scopeKeys []string) []string {
	if len(scopeKeys) == 0 {
		return nil
	}
	var prefixes []string
	seen := make(map[string]bool, len(scopeKeys)*2)
	for _, key := range scopeKeys {
		for _, prefix := range SubagentScopePrefixes(key) {
			if seen[prefix] {
				continue
			}
			seen[prefix] = true
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}

func ledgerScopeMatches(task *SubagentTask, scopeKeys []string) bool {
	if len(scopeKeys) == 0 {
		return true
	}
	if task == nil {
		return false
	}
	return taskBelongsToSession(task.SpawnerSessionKey, task.OriginSessionKey, task.ID, scopeKeys)
}

func SubagentScopePrefixes(sessionKey string) []string {
	if sessionKey == "" {
		return nil
	}

	var prefixes []string
	seen := make(map[string]bool, 8)
	add := func(prefix string) {
		if prefix == "" || seen[prefix] {
			return
		}
		seen[prefix] = true
		prefixes = append(prefixes, prefix)
	}

	add(sessionKey)
	for i := 0; i < len(sessionKey); i++ {
		if sessionKey[i] == ':' {
			add(sessionKey[:i])
		}
	}
	for i := 0; i < len(sessionKey); i++ {
		if sessionKey[i] != ':' {
			continue
		}
		suffix := sessionKey[i+1:]
		if !strings.Contains(suffix, ":") {
			continue
		}
		add(suffix)
	}
	if !strings.Contains(sessionKey, ":") {
		qualified := "native:" + sessionKey
		add(qualified)
		add("native")
	}

	return prefixes
}

func ledgerEntryLess(a, b SubagentLedgerEntry) bool {
	if a.Created != b.Created {
		return a.Created > b.Created
	}
	aNum, aOK := subagentTaskIDNumber(a.ID)
	bNum, bOK := subagentTaskIDNumber(b.ID)
	if aOK != bOK {
		return aOK
	}
	if aOK && aNum != bNum {
		return aNum > bNum
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.SessionKey < b.SessionKey
}

func subagentTaskIDNumber(id string) (int, bool) {
	const prefix = "subagent-"
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(id[len(prefix):])
	if err != nil {
		return 0, false
	}
	return n, true
}

func ledgerEntryFromTask(task *SubagentTask) SubagentLedgerEntry {
	return SubagentLedgerEntry{
		ID:             task.ID,
		Label:          task.Label,
		AgentID:        task.AgentID,
		Status:         task.Status,
		Summary:        task.Summary,
		Progress:       task.Progress,
		ContextRequest: task.ContextRequest,
		Created:        task.Created,
		Updated:        task.Updated,
		Iterations:     task.Iterations,
		RetryCount:     task.RetryCount,
		MaxRetries:     task.MaxRetries,
		Persisted:      false,
		SpawnerKey:     task.SpawnerSessionKey,
		OriginKey:      task.OriginSessionKey,
		SessionKey:     task.OriginSessionKey + ":" + task.ID,
	}
}

func ledgerEntryFromPersisted(p PersistedSubagentTask) SubagentLedgerEntry {
	status := p.Status
	if status == "" {
		status = SubagentStatusUnknown
	}
	sessionKey := p.SessionKey
	if sessionKey == "" && p.ParentKey != "" && p.TaskID != "" {
		sessionKey = p.ParentKey + ":" + p.TaskID
	}
	return SubagentLedgerEntry{
		ID:         p.TaskID,
		Label:      p.Label,
		AgentID:    p.AgentID,
		Status:     status,
		Created:    p.Created,
		Updated:    p.Updated,
		Persisted:  true,
		OriginKey:  p.ParentKey,
		SessionKey: sessionKey,
	}
}
