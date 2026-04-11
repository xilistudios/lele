# Client Channel API Documentation

The native client channel provides a local REST + WebSocket API for the built-in web UI and other desktop/local clients.

It is centered around:

- PIN-based pairing
- bearer-token auth
- session-scoped chat operations
- local file upload support
- real-time streaming over WebSocket

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

Response:

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

Response:

```json
{
  "message_id": "uuid",
  "session_key": "native:client-id:1712339123"
}
```

If `session_key` is omitted, the default session is `native:<client_id>`.

#### Get History

```http
GET /api/v1/chat/history?session_key=<session_key>
```

Returned messages may include `user`, `assistant`, and `tool` roles.

#### List Sessions

```http
GET /api/v1/chat/sessions
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

The session key must belong to the authenticated client namespace.

#### Session Actions

Session-scoped actions are handled under:

```text
/api/v1/chat/session/<session_key>
```

Supported actions:

| Method | Query | Behavior |
| --- | --- | --- |
| `DELETE` | none | Clear session history |
| `DELETE` | `action=delete` | Delete the session mapping and clear history |
| `GET` | `action=model` | Get current model and available models |
| `PATCH` | `action=model` | Set the current session model |
| `GET` | `action=agent` | Get the current session agent |
| `PATCH` | `action=agent` | Set the current session agent |
| `POST` or `GET` | `action=compact` | Compact the session |
| `GET` | `action=name` | Read the session name |
| `PATCH` | `action=name` | Update the session name |
| `GET` | `action=summary` | Return session summary placeholder |

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
GET /api/v1/agents/<agent_id>
```

#### Get Agent Status

```http
GET /api/v1/agents/<agent_id>?action=status
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

Connect with either query token or bearer auth:

```text
ws://127.0.0.1:18793/api/v1/ws?token=<token>
```

Optional query parameter:

```text
session_key=native:<client_id>[:suffix]
```

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

### Server Events

#### `welcome`

Sent immediately after connect.

Includes:

- `client_id`
- `device_name`
- `session_key`
- `status`
- `agents`
- `server_time`
- `processing`

#### `message.ack`

Acknowledges accepted inbound messages.

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

#### `message.complete`

Final assembled message payload, including attachments when present.

#### `tool.executing`

Emitted when a tool starts.

Includes optional `subagent_session_key`.

#### `tool.result`

Emitted when a tool returns.

Includes optional `subagent_session_key`.

#### `subagent.result`

Emitted for async subagent outcomes when surfaced through the native channel.

#### `approval.request`

Sent when user approval is required for a guarded action.

#### `attachments`

Sent when attachment metadata is delivered separately from text.

#### `subscribe.ack`, `unsubscribe.ack`, `approve.ack`, `cancel.ack`, `pong`

Acknowledgement and control events for client-side state handling.

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
- WebSocket access requires a valid token
- session ownership is validated on both REST and WebSocket paths

## Related Docs

- `docs/agents-models-providers.md`
- `docs/tools_configuration.md`
- `docs/SKILL_SUBAGENTS.md`
