package tools

// The subagent ledger: one row model for live tasks and for tasks recovered
// from their persisted session.
//
// A task lives in two places. While it is tracked by a SubagentManager it is an
// in-memory SubagentTask; once retention reaps it (or the process restarts) the
// only trace left is its session "{originSessionKey}:{ID}", which the session
// layer keeps on disk. Neither source alone is a complete history, so the
// ledger merges them: memory wins for a task that is still alive (it carries
// the authoritative status, progress and summary), disk fills in everything
// that outlived it.
//
// Two readers consume this model: the paginated list_active_subagents tool and
// the per-turn system-prompt digest. Both must be cheap, so the disk side is
// metadata-only (see PersistedSubagentTask) and the merge is a single pass.

import (
	"sort"
	"strconv"
	"strings"
)

// SubagentLedgerEntry is one row of a session's subagent ledger: either a live
// task (in-memory SubagentManager) or a task recovered from its persisted
// subagent session on disk, which survives retention cleanup and restarts.
// It is the single data model behind both the paginated list_active_subagents
// tool and the per-turn system-prompt digest.
type SubagentLedgerEntry struct {
	ID      string
	Label   string
	AgentID string
	Status  string
	Summary string
	// Progress and ContextRequest are model-authored too (the "PROGRESS:" line
	// and the "CONTEXT_NEEDED:" body of a subagent report), so both readers
	// sanitize them before interpolating them into a prompt or a tool result.
	Progress       string
	ContextRequest string
	Created        int64 // unix ms
	Updated        int64 // unix ms
	Iterations     int
	RetryCount     int
	MaxRetries     int
	Persisted      bool // recovered from disk; no live task object
	// SpawnerKey is the runtime session key of the agent loop that spawned the
	// task (SubagentTask.SpawnerSessionKey). It is the most precise identity a
	// row carries and the only one that bridges a routed runtime key
	// ("agent:main:telegram:direct:12345") to the key the task was recorded
	// under ("telegram:12345"); see ledgerScopeMatches. It is never persisted —
	// the session row does not carry it — so it is empty for Persisted rows.
	SpawnerKey string
	// OriginKey is the "<channel>:<chatID>" key the task was recorded under
	// (SubagentTask.OriginSessionKey, or the parent key of a persisted row).
	OriginKey string
	// SessionKey is "{OriginKey}:{ID}" — the task's own child session key, and
	// the dedupe key of the merge.
	SessionKey string
}

// PersistedSubagentTask is the disk-side row supplied by the wired lister.
// It MUST be obtainable from session metadata only (no message loading, no
// store query), so the per-turn system prompt can render the digest cheaply.
// Status and AgentID are carried because the session layer already persists
// them (sessionMetadata.SubagentStatus / .AgentID); hard-coding "completed"
// would misreport every failed or cancelled task.
type PersistedSubagentTask struct {
	TaskID     string
	SessionKey string
	ParentKey  string
	Label      string // session Name
	Status     string // "" = unknown; rendered as SubagentStatusUnknown, never completed
	AgentID    string // "" = unknown; the caller applies the pin/owner fallback
	Created    int64  // unix ms
	Updated    int64  // unix ms
}

// PersistedSubagentDetail carries the two fields that only the session store
// can answer for a persisted subagent session: the iteration proxy and the
// summary. It is fetched for a bounded key set — the rows a page actually
// returns — never for the whole history, because unlike PersistedSubagentTask
// it costs a store query.
type PersistedSubagentDetail struct {
	Iterations int    // in-context assistant messages; an undercount after compaction
	Summary    string // stored summary, else the last assistant text truncated to 200 bytes
}

// SetLedgerWiring installs the disk side of the subagent ledger.
//
// The lister and the session-existence probe are ONE call on purpose. Listing a
// persisted task ID is only safe because the probe makes claimTaskID skip an ID
// whose session already exists; a manager that lists disk rows without probing
// would hand a later spawn an ID that a persisted row still holds, and the two
// runs would merge into one session file. Keeping them as two setters let that
// half-wired state exist; here it is unrepresentable.
//
// NIL POLICY — all or nothing. Either argument nil (a nil func value, or a
// non-nil interface holding a nil func) clears BOTH callbacks: the ledger is
// disabled and the probe is removed. There is no reachable state in which one
// is installed without the other, and LedgerWiringEnabled reports exactly that
// state. Passing nil for both is the documented way to unwire (reload path).
//
// lister receives the parent-prefix set derived by SubagentScopePrefixes and
// returns metadata-only rows. sessionExists answers whether a session key
// already exists (memory, metadata or disk) and is used at spawn time only.
//
// Both callbacks run OUTSIDE sm.mu: they reach the SessionManager, whose lock
// ordering against sm.mu is the hazard documented in CleanupTerminalTasks.
func (sm *SubagentManager) SetLedgerWiring(
	lister func(parentPrefixes []string) []PersistedSubagentTask,
	sessionExists func(sessionKey string) bool,
) {
	if sm == nil {
		return
	}
	// Normalize a nil func behind a non-nil variable to a real nil, so the
	// all-or-nothing test below cannot be fooled by a typed nil.
	if lister == nil || sessionExists == nil {
		lister, sessionExists = nil, nil
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.persistedLister = lister
	sm.sessionExists = sessionExists
}

// LedgerWiringEnabled reports whether the required pair of SetLedgerWiring is
// installed, i.e. whether the ledger is disk-backed AND the task-ID collision
// probe is live. It is the only exported state query for the wiring; the
// optional callbacks are read through their typed getters
// (PersistedTaskDetail, PersistedTaskExecutorFallback), which return nil when
// unwired.
//
// The pair is installed by a single call, so this is a consistency check that
// can only fail if a future change splits the setter again — which is exactly
// what the half-wiring regression test pins.
func (sm *SubagentManager) LedgerWiringEnabled() bool {
	if sm == nil {
		return false
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.persistedLister != nil && sm.sessionExists != nil
}

// SetPersistedTaskDetail wires the windowed detail source of the subagent
// ledger. fn receives the session keys of the persisted rows on the page being
// returned and answers with their summary and iteration count.
//
// It is deliberately SEPARATE from SetLedgerWiring and optional: nil is a
// legitimate degradation (persisted rows render their label only), not a
// mis-wiring, because the detail source never feeds the task-ID collision
// probe. Every standalone manager constructed in tests has it unwired.
//
// The callback runs OUTSIDE sm.mu, for the same lock-ordering reason as the
// lister: it reaches the SessionManager and may query the store.
func (sm *SubagentManager) SetPersistedTaskDetail(fn func(keys []string) map[string]PersistedSubagentDetail) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.persistedDetail = fn
}

// PersistedTaskDetail returns the currently wired detail source, or nil. It
// exists so the tool can read the callback without reaching into the manager's
// lock-protected field.
func (sm *SubagentManager) PersistedTaskDetail() func(keys []string) map[string]PersistedSubagentDetail {
	if sm == nil {
		return nil
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.persistedDetail
}

// SetPersistedTaskExecutorFallback wires the executor resolver applied to
// persisted ledger rows whose AgentID is empty — the rows written before the
// session row carried agent_id. fn receives a row's session key and answers with
// the agent recorded at spawn time (the durable routing pin), or "".
//
// It exists so the tool and the system-prompt digest name the same executor for
// the same row: the digest resolves the pin itself (pkg/agent), and without this
// callback one row would show "coder" in the prompt and no agent at all in
// list_active_subagents. It is a map read on the caller's side — no I/O, no
// store query — so it is safe on the per-turn path.
//
// It is optional and separate from SetLedgerWiring: nil disables the fallback
// (rows keep their empty AgentID), which is the behaviour of every standalone
// manager constructed in tests. Like the detail source it never feeds the
// task-ID collision probe, so an unwired fallback is a degradation, not a
// mis-wiring.
func (sm *SubagentManager) SetPersistedTaskExecutorFallback(fn func(sessionKey string) string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.persistedExecutor = fn
}

// PersistedTaskExecutorFallback returns the currently wired executor fallback,
// or nil. Like PersistedTaskDetail it exists so the tool can read the callback
// without reaching into the manager's lock-protected field.
func (sm *SubagentManager) PersistedTaskExecutorFallback() func(sessionKey string) string {
	if sm == nil {
		return nil
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.persistedExecutor
}

// LedgerEntries returns the session's complete subagent ledger: live tasks
// merged with persisted rows, deduplicated by SessionKey (memory wins), sorted
// newest-first. scopeKeys are the session-key forms to scope by; an empty slice
// disables scoping and returns every task, matching the tool's precedence 3.
//
// The order is total — Created desc, then numeric task-ID desc, then SessionKey
// asc — because Created is unix milliseconds and several spawns in one turn
// routinely collide; without the tie-breaks pagination would duplicate or drop
// rows. The disk lister is invoked outside sm.mu.
//
// SCOPE CONTRACT — read this before scoping a caller. A live task is matched by
// its three identities (spawner key, origin key, origin:taskID) through the
// symmetric SessionKeysRelated, so a caller holding only a routed runtime key
// ("agent:main:telegram:direct:12345") still finds the tasks spawned from that
// conversation. A PERSISTED row carries no spawner key — the session row does
// not store one — so it is only reachable through its origin key or a
// ':'-aligned suffix of it. Consequence: a caller that passes the routed
// runtime key alone sees the live tasks but not the reaped ones. Callers that
// need the full history must pass the origin form
// (BuildOriginSessionKey(channel, chatID)) as well; the prompt digest does.
//
// A task that flips from memory to disk between two page fetches can move by
// the few milliseconds between task.Created and the session row's Created. That
// drift is page-boundary-only and unavoidable without a stable ledger ID.
func (sm *SubagentManager) LedgerEntries(scopeKeys []string) []SubagentLedgerEntry {
	if sm == nil {
		return nil
	}
	return sm.ledgerEntriesFrom(sm.ListTasks(), scopeKeys)
}

// ledgerEntriesFrom is LedgerEntries over an already-taken task snapshot. The
// tool takes the snapshot once and passes it here, so the hidden-by-scope count
// and the rows it renders are computed from the same instant; calling
// ListTasks twice (once for the count, once inside the merge) lets a concurrent
// reap make the two disagree.
func (sm *SubagentManager) ledgerEntriesFrom(tasks []*SubagentTask, scopeKeys []string) []SubagentLedgerEntry {
	if sm == nil {
		return nil
	}

	// The lister is read and invoked with NO manager lock held: it calls into
	// the SessionManager, and SubagentManager.mu -> SessionManager.mu is the
	// lock order CleanupTerminalTasks goes out of its way to avoid (the reverse
	// path, session cancel -> SubagentManager.mu, would otherwise deadlock and
	// block every subsequent spawn).
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
		// Dedupe by the full session key, never by the bare task ID: two chats
		// legitimately both have "subagent-1" (R4).
		if seen[entry.SessionKey] {
			continue
		}
		seen[entry.SessionKey] = true
		entries = append(entries, entry)
	}

	// Disk rows are already prefix-scoped by the lister (the prefix set is the
	// closed form of the symmetric relation), so they are admitted as-is;
	// memory wins for a key that is both alive and persisted.
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

// scopePrefixesFor unions SubagentScopePrefixes over every scope key, keeping
// first-seen order and dropping duplicates. An empty scope yields nil, which
// makes the lister return nothing: no scoping means memory-only, exactly the
// tool's precedence-3 behaviour.
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

// ledgerScopeMatches reports whether a live task belongs to any of scopeKeys.
//
// It is deliberately the SAME three-identity test the rest of the codebase uses
// for this question — taskBelongsToSession (session_keys.go) — rather than a
// relation re-derived for the ledger:
//
//   - SpawnerKey: the runtime session key captured at spawn. This is the only
//     identity that bridges a routed runtime key ("agent:main:telegram:direct:12345",
//     or "agent:main:main" with the default DM scope) to the key the task was
//     recorded under ("telegram:12345"). Without it the prompt digest, which
//     holds only the runtime key, renders nothing on every routed channel.
//   - OriginKey: the "<channel>:<chatID>" key, which is what the disk side is
//     stored under.
//   - OriginKey + ":" + ID: the task's own child session key, so a stop or a
//     listing issued against the child session reaches the parent's task.
//
// The relation is SYMMETRIC (SessionKeysRelated: either side may be a
// ':'-aligned prefix or suffix of the other), because the two sides are built by
// different subsystems with different amounts of detail for the same logical
// session. The previous directional form (origin is a prefix of the caller's
// key) matched neither of the two production shapes that matter.
//
// An empty scopeKeys disables scoping (the tool's precedence 3).
func ledgerScopeMatches(task *SubagentTask, scopeKeys []string) bool {
	if len(scopeKeys) == 0 {
		return true
	}
	if task == nil {
		return false
	}
	return taskBelongsToSession(task.SpawnerSessionKey, task.OriginSessionKey, task.ID, scopeKeys)
}

// SubagentScopePrefixes returns every session-key form the disk side can be
// stored under for a caller holding sessionKey. It is the closed form of the
// symmetric SessionKeysRelated relation on the PARENT side, so the memory filter
// and the disk prefix scan admit the same rows by construction:
//
//   - the key itself;
//   - every ':'-aligned PREFIX of it, shortest first ("a:b:c" yields "a",
//     "a:b"). This is what makes a parent find its ":chat:N" alias and its
//     ":subagent-N" children, and what makes an ancestor key ("agent:main")
//     find a deeper parent's tasks;
//   - every ':'-aligned SUFFIX of it that contains at least one ':'
//     ("agent:main:telegram:12345" yields "main:telegram:12345",
//     "telegram:12345", "12345" is NOT yielded). The suffix direction is what
//     bridges a routed runtime key to the origin key a task is recorded under.
//     A bare single-segment suffix is excluded on purpose: "12345" is a chat ID
//     with no channel, a namespace nothing is ever written under, and scanning
//     it would only cost prefix tests;
//   - for a key with no ':' at all, its "native:"-qualified form. A bare uuid is
//     a native chat ID (native.go sets sessionKey := msg.ChatID), so
//     "native:<uuid>" is the key the task was actually recorded under. This
//     special case is load-bearing: without it a bare-uuid caller finds no
//     persisted row at all. It is additive only — a uuid is unique per chat.
//
// The result is deduplicated and ordered first-seen (key, then prefixes
// shortest-first, then suffixes longest-first). Returns nil for an empty key.
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
	// Every proper prefix ending at a ':' boundary, shortest first. This
	// subsumes the ":chat:N" alias and ":subagent-N" child strips: both cut at a
	// ':' and their output is one of these prefixes.
	for i := 0; i < len(sessionKey); i++ {
		if sessionKey[i] == ':' {
			add(sessionKey[:i])
		}
	}
	// Every proper suffix starting at a ':' boundary and containing at least one
	// ':', longest first (so the most specific form is scanned before its tail).
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
	// A bare uuid is a native chat ID, so its qualified form is the key the task
	// was recorded under. The qualified key is added BEFORE the bare form so the
	// most specific candidate is scanned first, and its own ':'-aligned prefixes
	// ("native") are added too — the same closure rule as every other key.
	if !strings.Contains(sessionKey, ":") {
		qualified := "native:" + sessionKey
		add(qualified)
		add("native")
	}

	return prefixes
}

// ledgerEntryLess orders two ledger rows newest-first: Created desc, numeric
// task-ID desc, SessionKey asc. Non-numeric IDs sort after numeric ones.
func ledgerEntryLess(a, b SubagentLedgerEntry) bool {
	if a.Created != b.Created {
		return a.Created > b.Created
	}
	aNum, aOK := subagentTaskIDNumber(a.ID)
	bNum, bOK := subagentTaskIDNumber(b.ID)
	if aOK != bOK {
		return aOK // numeric IDs first
	}
	if aOK && aNum != bNum {
		return aNum > bNum
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	// SessionKey is the last tie-break so the order is total even for rows that
	// share an ID (two origins) — without it, pagination could duplicate or
	// drop a row between pages.
	return a.SessionKey < b.SessionKey
}

// subagentTaskIDNumber parses the numeric suffix of "subagent-N".
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

// ledgerEntryFromTask converts a live task snapshot to a ledger row. All three
// identities the scope matcher uses are carried over, so the row can be matched
// the same way taskBelongsToSession matches the task.
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

// ledgerEntryFromPersisted converts a disk row to a ledger row.
//
// An empty persisted status becomes SubagentStatusUnknown, NOT completed. The
// session row is created at spawn (SetSubagentAgentID) and subagent_status is
// written only on a terminal transition, so an empty value means the task was
// interrupted (restart) or its live row now lives in another agent's manager
// (session re-pinned with /agent) — both non-terminal. Fabricating "completed"
// told the model the work was finished while wait_for_subagent still blocked.
//
// The WebUI reader (agentProvidableImpl.GetSessionSubagents) keeps its own
// historical completed fallback for the same rows and is deliberately
// untouched: it is a UI listing whose shape predates this model, while the
// ledger feeds the system prompt and must not guess.
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
