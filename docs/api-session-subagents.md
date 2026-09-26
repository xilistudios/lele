# API: Session Subagents

## Endpoint

```
GET /api/v1/chat/sessions/{sessionKey}/subagents
```

## Description

Returns the list of subagent tasks spawned from a given session, ordered by creation time (newest first). Each entry includes the task status, label, agent ID, and timestamps.

## Authentication

Requires Bearer token (same as all `/api/v1/*` endpoints).

## Path Parameters

| Parameter    | Type   | Description                          |
|-------------|--------|--------------------------------------|
| `sessionKey` | string | The parent session key (URL-encoded) |

## Response

### 200 OK

```json
{
  "session_key": "native:client-1:1",
  "subagents": [
    {
      "task_id": "subagent-3",
      "session_key": "native:client-1:1:subagent-3",
      "label": "Research Go logging",
      "agent_id": "",
      "status": "running",
      "summary": "",
      "created": 1716748800000,
      "updated": 1716748810000,
      "iterations": 0
    },
    {
      "task_id": "subagent-2",
      "session_key": "native:client-1:1:subagent-2",
      "label": "Analyze pkg/agent",
      "agent_id": "coder",
      "status": "completed",
      "summary": "Found 3 undocumented exported functions",
      "created": 1716748700000,
      "updated": 1716748750000,
      "iterations": 5
    },
    {
      "task_id": "subagent-1",
      "session_key": "native:client-1:1:subagent-1",
      "label": "",
      "agent_id": "",
      "status": "failed",
      "summary": "Subagent execution failed",
      "created": 1716748600000,
      "updated": 1716748610000,
      "iterations": 0
    }
  ]
}
```

### Response Fields

| Field                          | Type     | Description                                      |
|-------------------------------|----------|--------------------------------------------------|
| `session_key`                  | string   | The parent session key                           |
| `subagents`                    | array    | List of subagent tasks, newest first             |
| `subagents[].task_id`          | string   | Unique task identifier (e.g. `subagent-1`)       |
| `subagents[].session_key`      | string   | The subagent's own session key for history access |
| `subagents[].label`            | string   | Human-readable label (empty if unnamed)          |
| `subagents[].agent_id`         | string   | Target agent ID (empty = default)                |
| `subagents[].status`           | string   | Current status (see Status Values)               |
| `subagents[].summary`          | string   | One-line summary of the result                   |
| `subagents[].created`          | int64    | Creation time (Unix milliseconds)                |
| `subagents[].updated`          | int64    | Last update time (Unix milliseconds)             |
| `subagents[].iterations`       | int      | Number of LLM iterations completed               |

### Status Values

| Status           | Description                                                        |
|-----------------|--------------------------------------------------------------------|
| `running`        | Task is actively executing                                         |
| `completed`      | Task finished successfully (also used as default for past subagents) |
| `not_done`       | Task could not complete with current constraints                   |
| `needs_context`  | Task paused, waiting for guidance                                  |
| `failed`         | Task failed with an error                                          |
| `cancelled`      | Task was cancelled                                                 |

### Error Responses

Every error uses the shared envelope `{"error": "<message>", "code": "<code>"}`.

#### 400 Bad Request
```json
{
  "error": "session_key required",
  "code": "missing_session_key"
}
```
Returned when the `sessionKey` path parameter resolves to an empty value.

#### 403 Forbidden
```json
{
  "error": "access denied to this session",
  "code": "session_forbidden"
}
```
Returned when the token is valid but the session ownership check fails.

#### 500 Internal Server Error
```json
{
  "error": "failed to load subagents",
  "code": "subagents_unavailable"
}
```
Returned when the subagents provider panics or fails (the cache converts a panic into an error and never caches it), or when the caller's request context is cancelled while another fill is in flight for the same session.

The endpoint never answers 404: a session with no subagent tasks — including one that does not exist — returns 200 with an empty `subagents` array, not an error.

## Backend Implementation

### Route Registration (native.go)

```go
mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/subagents", withAuth(n.handleSessionSubagents))
```

### Handler (rest_session.go)

```go
func (n *NativeChannel) handleSessionSubagents(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	if sessionKey == "" {
		writeError(w, http.StatusBadRequest, "session_key required", "missing_session_key")
		return
	}

	// Normalize session key: subagent tasks store OriginSessionKey as
	// "native:<sessionKey>", but the REST API receives the bare UUID.
	// Match the format used in handleChatHistory for subagent lookups.
	if !strings.HasPrefix(sessionKey, "native:") {
		sessionKey = "native:" + sessionKey
	}

	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	// Defence in depth: a stale WebUI bundle can hammer this read endpoint
	// (a buggy client was observed at ~250 req/s) and every miss runs
	// GetSessionSubagents, which scans all subagent managers and calls
	// FindSubagentSessions on each agent's SessionManager - that call takes the
	// session manager write lock and may load full subagent sessions from disk.
	// The cache collapses repeated identical reads within subagentsCacheTTL
	// into one provider call per session. The validateSessionOwnership above
	// still runs on every request and is cheap (in-memory lookups only), and no
	// mutating endpoint is cached.
	//
	// The request context is passed through so a client that goes away while
	// another fill is in flight stops waiting instead of pinning this handler
	// goroutine; the fill itself finishes for its other waiters.
	tasks, err := n.subagentsReadCache().get(r.Context(), sessionKey, func() ([]SubagentTaskInfo, error) {
		// GetSessionSubagents has no error return, so the cache's error branch
		// is unreachable in production; it stays because a provider panic is
		// converted into an error there (and never cached) instead of leaving
		// concurrent waiters blocked.
		return n.agentLoop.GetSessionSubagents(sessionKey), nil
	})
	if err != nil {
		// Either the provider failed/panicked (already logged with a stack by
		// the cache) or the caller's context was cancelled; writing the error to
		// a connection that is gone is a harmless no-op.
		writeError(w, http.StatusInternalServerError, "failed to load subagents", "subagents_unavailable")
		return
	}

	// The cache stores the list already sorted by Created descending (newest
	// first), so the payload is ordered for every request without re-sorting.

	// Convert to API response type
	subagents := make([]SubagentTaskEntry, len(tasks))
	origSessionKey := r.PathValue("sessionKey")
	for i, task := range tasks {
		// Strip "native:" prefix from subagent session keys so the
		// frontend can match them against the bare-UUID parent session keys.
		cleanSessionKey := task.SessionKey
		if strings.HasPrefix(cleanSessionKey, "native:") {
			cleanSessionKey = cleanSessionKey[len("native:"):]
		}
		subagents[i] = SubagentTaskEntry{
			TaskID:     task.TaskID,
			SessionKey: cleanSessionKey,
			Label:      task.Label,
			AgentID:    task.AgentID,
			Status:     task.Status,
			Summary:    task.Summary,
			Created:    task.Created,
			Updated:    task.Updated,
			Iterations: task.Iterations,
		}
	}

	// Return the original (non-normalized) session key for frontend consistency
	writeJSON(w, http.StatusOK, SessionSubagentsResponse{
		SessionKey: origSessionKey,
		Subagents:  subagents,
	})
}
```

### Caching (subagents_cache.go)

`handleSessionSubagents` never calls `agentLoop.GetSessionSubagents` directly: it goes through a per-channel read cache (`n.subagentsReadCache()`), the small in-process map implemented by `pkg/channels/subagents_cache.go`. A cache miss runs the expensive provider path — scanning every subagent manager and calling `SessionManager.FindSubagentSessions` for each agent, which takes the session manager write lock and can load full subagent sessions from disk — so the cache exists to stop one misbehaving reader from multiplying that load (a stale WebUI bundle was observed issuing ~250 requests per second against this endpoint).

Properties:

- **1 s TTL** (`subagentsCacheTTL`) — one successful read is reused for at most one second, collapsing that request storm into a single provider call per session per second. Live subagent progress reaches the browser over SSE, so this endpoint only needs to be roughly current and a sub-second staleness is imperceptible in the sidebar.
- **Single-flight** — concurrent identical reads for the same key collapse into one fetch. Waiters block on a per-entry channel that is closed exactly once, on success, provider error and recovered panic alike, so no waiter can be stranded.
- **Caller cancellation** — the request context is handed to the cache, so a client that goes away while another fill is in flight stops waiting (`ctx.Err()`) instead of pinning the handler goroutine. The fill itself is never abandoned: its other waiters still receive a value.
- **Event-driven invalidation** — `invalidateSubagentsCache(sessionKey)` drops the session's entry *before* the event is emitted, on `tool.result` with `tool == "spawn"` (a new task now exists) and on `subagent.result` (a task reached a terminal state); see `dispatchOutboundMessage` in `pkg/channels/native.go`. The TTL therefore only covers quiet periods. An in-flight fill is dropped from the map as well, so a read arriving after the event cannot join a fetch that started before it.
- **Failures are never cached** — a provider error or panic drops the entry (and is logged with a stack, because `net/http` never sees a panic recovered inside the cache) and the next request retries.
- **Keyed by the normalised session key** — the handler applies the `native:` prefix before the lookup, so two sessions can never share an entry.
- **Authorisation is not part of the cache** — `validateSessionOwnership` runs on every request, before the lookup, and only touches in-memory state, so the TTL can never change auth semantics; the cache only affects latency.
- **Bounded memory** — at most `subagentsCacheMaxKeys` (256) entries at rest, expiring the stale ones first and then the earliest expiry. In-flight entries are never evicted (that would break single-flight), so the map may overshoot transiently before the next completed fill evicts back down.
- **Ordered once** — the cached value is stored already sorted by `created` descending, so no caller re-sorts (the handler used to sort the provider's slice in place).

### Backend Logic (agent_providable.go)

`GetSessionSubagents` merges two data sources:

1. **In-memory tasks** from `SubagentManager.ListTasks()` — rich data with label, agent ID, live status.
2. **Persisted past sessions** from all agents' `SessionManager.FindSubagentSessions()` — scans session storage for keys matching `{parentSessionKey}:subagent-*`, recovering task ID, timestamps, iteration count, and summary from the saved session files.

In-memory tasks always take precedence (tracked via `seen` map on task ID). Past sessions that are no longer in memory get status `"completed"` by default.

### Session Storage Scanning (session/manager.go)

```go
func (sm *SessionManager) FindSubagentSessions(parentPrefix string) []SubagentSessionInfo {
	prefix := parentPrefix + ":subagent-"
	for key, session := range sm.sessions {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		// Count assistant messages as iteration proxy
		// Use session.Summary or fallback to last assistant message
		// ...
	}
}
```

Sessions are loaded from disk on startup by `SessionManager.loadSessions()`, so subagent sessions persisted from previous runs are automatically available.

### Frontend Integration

**New API client method** (`client.ts`):
```typescript
sessionSubagents: (sessionKey: string) =>
  request<SessionSubagentsResponse>(
    endpoints.chat.session(sessionKey, 'subagents'),
    { method: 'GET' },
  ),
```

**New endpoint** (`endpoints.ts`):
The existing `session()` function already supports subresource strings, so just add `'subagents'` to the union type.

**New type** (`types.ts`):
```typescript
export type SubagentTaskInfo = {
  task_id: string
  session_key: string
  label: string
  agent_id: string
  status: 'running' | 'completed' | 'not_done' | 'needs_context' | 'failed' | 'cancelled'
  summary: string
  created: number
  updated: number
  iterations: number
}

export type SessionSubagentsResponse = {
  session_key: string
  subagents: SubagentTaskInfo[]
}
```

**Updated hook** (`useSubagents.ts`):
Replace message-scraping with API call:
```typescript
export function useSubagents(sessionKey: string | null) {
  const { api } = useAuthContext()
  const [subagents, setSubagents] = useState<SubagentTaskInfo[]>([])
  const [loading, setLoading] = useState(false)

  const fetchSubagents = useCallback(async () => {
    if (!sessionKey) { setSubagents([]); return }
    setLoading(true)
    try {
      const data = await api.sessionSubagents(sessionKey)
      setSubagents(data.subagents)
    } catch {
      setSubagents([])
    } finally {
      setLoading(false)
    }
  }, [sessionKey, api])

  useEffect(() => { fetchSubagents() }, [fetchSubagents])

  // Poll every 5s while any subagent is running
  useEffect(() => {
    const hasRunning = subagents.some(s => s.status === 'running')
    if (!hasRunning) return
    const id = setInterval(fetchSubagents, 5000)
    return () => clearInterval(id)
  }, [subagents, fetchSubagents])

  return { subagents, loading, refresh: fetchSubagents }
}
```

## Design Decisions

1. **Server-side filtering by OriginSessionKey** — the backend already tracks which parent session spawned each task via `task.OriginSessionKey`. No need to scrape messages on the frontend.

2. **Status comes from SubagentTask.Status** — authoritative source. The backend updates it through the task lifecycle (`running` → `completed`/`failed`/`cancelled`/`needs_context`).

3. **Session key for history access** — each subagent's history is stored at `{parent_session_key}:{task_id}`, matching the pattern already used by `handleChatHistory` with the `{subagentId}` path parameter.

4. **Polling while running** — the frontend polls every 5s only when at least one subagent has `status: "running"`. Stops polling once all are terminal.

5. **No WebSocket event needed** — the existing `tool.executing` / `tool.result` / `subagent.result` events already notify the frontend of subagent activity. The HTTP endpoint is a supplementary data source for the sidebar list.

6. **Past subagents survive restarts** — `GetSessionSubagents` merges in-memory tasks with persisted session data. Subagent history is saved to disk by `SessionRecorder.Save()`. On restart, `SessionManager.loadSessions()` reloads all session files, and `FindSubagentSessions()` discovers past subagent sessions by matching keys with the `{parent}:subagent-` prefix. In-memory tasks always take precedence over persisted data.
