# Client Channel API Documentation

The native client channel provides a local REST + WebSocket API for the built-in web UI and other desktop/local clients.

It is centered around:

- PIN-based pairing
- bearer-token auth
- session-scoped chat operations
- local file upload support
- real-time streaming over WebSocket
- RESTful resource-oriented URL patterns

## CLI Commands

Manage the native client channel from the CLI:

```bash
lele client pin
lele client pin --device "My Desktop"
lele client list
lele client pending
lele client remove <client_id>
lele client status
```

## Configuration

Add or adjust the `channels.native` block in `~/.lele/config.json`:

```json
{
  "channels": {
    "native": {
      "enabled": true,
      "host": "127.0.0.1",
      "port": 18793,
      "token_expiry_days": 30,
      "pin_expiry_minutes": 5,
      "max_clients": 5,
      "cors_origins": [
        "http://localhost",
        "http://localhost:3005",
        "http://127.0.0.1:3005",
        "tauri://localhost",
        "https://tauri.localhost"
      ],
      "session_expiry_days": 30,
      "max_upload_size_mb": 50,
      "upload_ttl_hours": 24
    }
  }
}
```

## Auth Flow

### 1. Generate a PIN

CLI:

```bash
lele client pin --device "My Desktop"
```

REST:

```http
GET /api/v1/auth/pin?device_name=My%20Desktop
```

Example response:

```json
{
  "pin": "123456",
  "expires": "2026-04-05T12:05:00Z"
}
```

### 2. Pair With The PIN

```http
POST /api/v1/auth/pair
Content-Type: application/json

{
  "pin": "123456",
  "device_name": "My Desktop"
}
```

Response (201 Created):

```json
{
  "token": "a1b2c3...",
  "refresh_token": "d4e5f6...",
  "expires": "2026-05-05T12:00:00Z",
  "client_id": "client-uuid"
}
```

### 3. Refresh A Token

```http
POST /api/v1/auth/refresh
Content-Type: application/json

{
  "refresh_token": "d4e5f6..."
}
```

### 4. Check Auth Status

```http
GET /api/v1/auth/status
Authorization: Bearer <token>
```

If the header is missing or invalid, the endpoint returns `valid: false` instead of failing hard.

## REST API

All endpoints below require `Authorization: Bearer <token>` unless stated otherwise.

### API Conventions

- URLs follow RESTful resource-oriented patterns with path parameters (e.g., `/api/v1/chat/session/{sessionKey}/model`)
- HTTP methods are specific per route (`GET`, `POST`, `PUT`, `PATCH`, `DELETE`)
- Responses use proper HTTP status codes (200 OK, 201 Created, 422 Unprocessable Entity for validation)
- Errors return a structured `APIError` payload:
  ```json
  {
    "code": "error_code",
    "message": "Human-readable description"
  }
  ```
- List endpoints support pagination via `?offset=0&limit=50` query params

### Chat

#### Send Message

```http
POST /api/v1/chat/send
Content-Type: application/json

{
  "content": "Hello",
  "attachments": ["/home/user/.lele/tmp/uploads/file.pdf"],
  "session_key": "native:client-id:1712339123",
  "agent_id": "main"
}
```

Response (201 Created):

```json
{
  "message_id": "uuid",
  "session_key": "native:client-id:1712339123"
}
```

If `session_key` is omitted, the default session is `native:<client_id>`.

#### Send Message With REST Streaming

```http
POST /api/v1/chat/send/stream
Accept: text/event-stream
Content-Type: application/json

{
  "content": "Hello",
  "attachments": ["/home/user/.lele/tmp/uploads/file.pdf"],
  "session_key": "native:client-id:1712339123",
  "agent_id": "main"
}
```

The response is an SSE stream. Each event uses the same event names and payloads as the WebSocket API, beginning with `message.ack`:

```text
event: message.ack
data: {"message_id":"uuid","session_key":"native:client-id:1712339123"}

event: message.stream
data: {"message_id":"uuid","session_key":"native:client-id:1712339123","chunk":"partial","done":false}

event: message.complete
data: {"message_id":"uuid","session_key":"native:client-id:1712339123","content":"Complete response text"}
```

Example:

```bash
curl -N \
  -H "Authorization: Bearer <token>" \
  -H "Accept: text/event-stream" \
  -H "Content-Type: application/json" \
  -d '{"content":"Hello","session_key":"native:client-id:1712339123","agent_id":"main"}' \
  http://127.0.0.1:18793/api/v1/chat/send/stream
```

Initial provider support is OpenAI-compatible chat completions streaming (`stream: true`). Non-streaming providers still return the final response through the same SSE event sequence.

#### Get History

```http
GET /api/v1/chat/sessions/{sessionKey}/history?offset=0&limit=50

GET /api/v1/chat/sessions/{sessionKey}/history/{subagentId}
```

Returned messages may include `user`, `assistant`, and `tool` roles. Pagination is supported via `offset` and `limit` query parameters.

#### List Sessions

```http
GET /api/v1/chat/sessions?offset=0&limit=50
```

Response items include:

- `key`
- `name`
- `created`
- `updated`
- `message_count`

#### Create Session

```http
POST /api/v1/chat/sessions
Content-Type: application/json

{
  "session_key": "native:client-id:1712339123"
}
```

Response (201 Created).

The session key must belong to the authenticated client namespace.

#### Get Session

```http
GET /api/v1/chat/session/{sessionKey}
```

Returns session metadata (agent_id, model, name, think_level).

#### Delete Session

```http
DELETE /api/v1/chat/sessions/{sessionKey}
```

#### Clear Session History

```http
POST /api/v1/chat/sessions/{sessionKey}/clear
```

Removes the session mapping and clears history.

#### Clear Session History

```http
POST /api/v1/chat/{sessionKey}/clear
```

Clears the conversation history without deleting the session.

#### Session Sub-resources

Session properties are accessed via sub-resource paths:

| Method | Path | Behavior |
| --- | --- | --- |
| `GET` | `/api/v1/chat/sessions/{key}/model` | Get current model and available models |
| `PATCH` | `/api/v1/chat/sessions/{key}/model` | Set the current session model |
| `GET` | `/api/v1/chat/sessions/{key}/agent` | Get the current session agent |
| `PATCH` | `/api/v1/chat/sessions/{key}/agent` | Set the current session agent |
| `GET` | `/api/v1/chat/sessions/{key}/thinking` | Get thinking level |
| `PATCH` | `/api/v1/chat/sessions/{key}/thinking` | Set thinking level (off, low, medium, high) |
| `GET` | `/api/v1/chat/sessions/{key}/name` | Read the session name |
| `PATCH` | `/api/v1/chat/sessions/{key}/name` | Update the session name |
| `GET` | `/api/v1/chat/sessions/{key}/context` | Get context token usage |
| `GET` | `/api/v1/chat/sessions/{key}/summary` | Return session summary |
| `POST` | `/api/v1/chat/sessions/{key}/compact` | Compact the session |

### File Uploads

```http
POST /api/v1/files/upload
Content-Type: multipart/form-data
```

Send one or more files using the `files` field.

Example:

```bash
curl -X POST \
  -H "Authorization: Bearer <token>" \
  -F "files=@/path/to/document.pdf" \
  -F "files=@/path/to/image.png" \
  http://127.0.0.1:18793/api/v1/files/upload
```

Response:

```json
{
  "files": [
    {
      "id": "a1b2c3d4",
      "path": "/home/user/.lele/tmp/uploads/a1b2c3d4_document.pdf",
      "name": "document.pdf",
      "mime_type": "application/pdf",
      "size": 1024
    }
  ]
}
```

Current behavior:

- default max upload size: `50MB`
- files are stored under `~/.lele/tmp/uploads/`
- cleanup runs based on `upload_ttl_hours`
- uploaded file paths can be passed to chat as `attachments`

### Agents

#### List Agents

```http
GET /api/v1/agents
```

#### Get Agent Info

```http
GET /api/v1/agents/{agentId}
```

#### Get Agent Status

```http
GET /api/v1/agents/{agentId}/status
```

#### Get Agent Files

```http
GET /api/v1/agents/{agentId}/files
```

#### Save Agent File

```http
PUT /api/v1/agents/{agentId}/files/{fileName}
Content-Type: application/json

{
  "content": "file contents..."
}
```

### Config

#### Get Editable Config Document

```http
GET /api/v1/config
```

#### Save Editable Config Document

```http
PUT /api/v1/config
Content-Type: application/json

{
  "config": { ... }
}
```

#### Validate Editable Config Document

```http
POST /api/v1/config/validate
Content-Type: application/json

{
  "config": { ... }
}
```

Validation responses include structured errors with `path`, `message`, and `code`.

### Tools, Models, Skills, Status, Channels

```http
GET /api/v1/tools
GET /api/v1/models?agent_id=<agent_id>&session_key=<session_key>
GET /api/v1/skills
GET /api/v1/status
GET /api/v1/channels
```

Notes:

- `/api/v1/tools` currently returns a compact static tool list for client UX
- `/api/v1/skills` currently returns an empty list in the native channel implementation
- `/api/v1/status` reports runtime status, uptime, agents, channels, and version

## WebSocket API

Connect with bearer auth:

```text
ws://127.0.0.1:18793/api/v1/ws
Authorization: Bearer <token>
```

Or via query parameter (legacy):

```text
ws://127.0.0.1:18793/api/v1/ws?token=<token>
```

Optional query parameter:

```text
session_key=native:<client_id>[:suffix]
```

### Protocol

Messages follow a versioned envelope:

```json
{
  "v": 1,
  "id": "cmd-1-1712339123000",
  "event": "message",
  "data": { ... }
}
```

- `v` (number, optional) — protocol version (currently `1`, defined as `WSProtocolVersion` in the server). New clients should send `v: 1`. The field is optional for backward compatibility — if omitted, the server treats the message as v0.
- `id` (string, optional) — correlation ID for request-response pairing. When a client includes an `id` on an outbound event, the server echoes the same `id` in the corresponding ack response (`*.ack` events), enabling the client to correlate responses to requests.
- `event` (string) — event type.
- `data` (object) — event payload.

The server sends `v: 1` on all outbound events (`welcome`, `ack`s, `error`, streaming events, etc.). Clients should send `v: 1` on outbound events for forward compatibility. Clients that omit `v` will have their messages processed with v0 semantics (backward compatible fallback).

### Client Events

#### `message`

```json
{
  "event": "message",
  "data": {
    "content": "Hello",
    "attachments": ["/home/user/.lele/tmp/uploads/file.pdf"],
    "session_key": "native:client-id:1712339123",
    "agent_id": "main"
  }
}
```

#### `approve`

```json
{
  "event": "approve",
  "data": {
    "request_id": "approval-uuid",
    "approved": true
  }
}
```

#### `subscribe`

```json
{
  "event": "subscribe",
  "data": {
    "session_key": "native:client-id:1712339123"
  }
}
```

#### `unsubscribe`

```json
{
  "event": "unsubscribe",
  "data": {
    "session_key": "native:client-id:1712339123"
  }
}
```

#### `cancel`

```json
{
  "event": "cancel",
  "data": {}
}
```

#### `ping`

```json
{
  "event": "ping",
  "data": {}
}
```

#### `typing`

Indicates the user is typing in a session. Currently a no-op on the server side (reserved for future use).

```json
{
  "event": "typing",
  "data": {
    "session_key": "native:client-id:1712339123"
  }
}
```

### Server Events

#### `welcome`

Sent immediately after connect. The envelope `v` field is set to `1` (the current `WSProtocolVersion`).

Includes:

- `client_id`
- `device_name`
- `session_key`
- `status`
- `agents`
- `server_time`
- `processing`

#### `message.ack`

Acknowledges accepted inbound messages. Includes `id` (correlation ID from the request) when provided.

#### `message.stream`

Streaming chunk payload:

```json
{
  "message_id": "uuid",
  "session_key": "native:client-id",
  "chunk": "partial response",
  "done": false
}
```

#### `message.thinking`

Emitted for streaming thinking/chain-of-thought content (when the model supports reasoning). Same shape as `message.stream` but conveys internal reasoning rather than the final response.

```json
{
  "message_id": "uuid",
  "session_key": "native:client-id",
  "chunk": "internal reasoning step..."
}
```

#### `message.complete`

Final assembled message payload (always emitted after streaming completes), including attachments when present.

```json
{
  "message_id": "uuid",
  "session_key": "native:client-id",
  "content": "Complete response text",
  "attachments": [
    {
      "name": "file.pdf",
      "mime_type": "application/pdf",
      "size": 1024,
      "path": "/home/user/.lele/tmp/uploads/uuid_file.pdf"
    }
  ]
}
```

#### `tool.executing`

Emitted when a tool starts. Includes tool name, action, arguments, and optional `subagent_session_key` / `tool_call_id`.

```json
{
  "session_key": "native:client-id:timestamp",
  "tool": "web_search",
  "action": "Searching the web for...",
  "arguments": {
    "query": "latest news"
  },
  "tool_call_id": "call_abc123",
  "subagent_session_key": "subagent:subagent-9876543210"
}
```

#### `tool.result`

Emitted when a tool returns.

```json
{
  "session_key": "native:client-id:timestamp",
  "tool": "web_search",
  "result": "Search results...",
  "tool_call_id": "call_abc123",
  "subagent_session_key": "subagent:subagent-9876543210"
}
```

#### `subagent.result`

Emitted for async subagent outcomes when surfaced through the native channel.

```json
{
  "session_key": "native:client-id:timestamp",
  "tool": "subagent",
  "result": "...",
  "tool_call_id": "...",
  "subagent_session_key": "subagent:subagent-9876543210"
}
```

#### `approval.request`

Sent when user approval is required for a guarded action.

```json
{
  "id": "approval-uuid",
  "command": "rm -rf /tmp/test",
  "reason": "This action requires your approval"
}
```

#### `attachments`

Sent when attachment metadata is delivered separately from text.

#### `history.updated`

Emitted after a message is fully processed and persisted to storage. Signals that the client can safely refetch session history.

```json
{
  "session_key": "native:client-id:timestamp",
  "name": "Session Name"
}
```

#### `subscribe.ack`, `unsubscribe.ack`, `approve.ack`, `cancel.ack`, `pong`

Acknowledgement and control events for client-side state handling.

Each ack event echoes the `id` field from the client's original request for correlation.

Example response to a `subscribe` event with `id: "sub-1-1712339123000"`:

```json
{
  "v": 1,
  "id": "sub-1-1712339123000",
  "event": "subscribe.ack",
  "data": {
    "session_key": "native:client-id:timestamp",
    "processing": false
  }
}
```

#### `error`

Structured server-side error payload:

```json
{
  "code": "error_code",
  "message": "Error description"
}
```

## Session Key Rules

Accepted session key forms include:

- `native:<client_id>`
- `native:<client_id>:<timestamp-or-suffix>`
- `subagent:<task_id>` for subagent-related flows

Clients may only access session keys that belong to their own namespace.

## Security Notes

- tokens are validated server-side and tied to paired clients
- CORS is restricted to configured origins
- uploads are size-limited and cleaned up periodically
- WebSocket access requires a valid token (via Authorization header or query param)
- session ownership is validated on both REST and WebSocket paths
- request bodies are limited to 1MB
- WebSocket messages use protocol versioning for forward compatibility

## Related Docs

- `docs/agents-models-providers.md`
- `docs/tools_configuration.md`
- `docs/SKILL_SUBAGENTS.md`
