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

#### 403 Forbidden
```json
{
  "error": "access denied to this session",
  "code": "session_forbidden"
}
```

#### 404 Not Found
```json
{
  "error": "session not found",
  "code": "session_not_found"
}
```

## Backend Implementation

### Route Registration (native.go)

```go
mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/subagents", withAuth(n.handleSessionSubagents))
```

### Handler (rest_session.go)

```go
func (n *NativeChannel) handleSessionSubagents(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	tasks := n.agentLoop.GetSessionSubagents(sessionKey)

	// Sort by Created descending (newest first)
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Created > tasks[j].Created
	})

	// Convert to API response type
	subagents := make([]SubagentTaskEntry, len(tasks))
	for i, task := range tasks {
		subagents[i] = SubagentTaskEntry{
			TaskID:     task.TaskID,
			SessionKey: task.SessionKey,
			Label:      task.Label,
			AgentID:    task.AgentID,
			Status:     task.Status,
			Summary:    task.Summary,
			Created:    task.Created,
			Updated:    task.Updated,
			Iterations: task.Iterations,
		}
	}

	writeJSON(w, http.StatusOK, SessionSubagentsResponse{
		SessionKey: sessionKey,
		Subagents:  subagents,
	})
}
```

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
