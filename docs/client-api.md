# Client Channel API Documentation

Lele Client Channel provides a REST + WebSocket API for native desktop clients (Tauri/Electron) to communicate with the Lele AI agent.

## CLI Commands

Manage client channel from command line:

```bash
# Generate pairing PIN
lele client pin
lele client pin --device "My Desktop"

# List paired clients
lele client list

# List pending pairing requests
lele client pending

# Remove a paired client
lele client remove <client_id>

# Show channel status
lele client status
```

## Configuration

Add to `~/.lele/config.json`:

```json
{
  "channels": {
    "native": {
      "enabled": true,
      "host": "127.0.0.1",
      "port": 18792,
      "token_expiry_days": 30,
      "pin_expiry_minutes": 5,
      "max_clients": 5,
      "cors_origins": ["http://localhost", "tauri://localhost"],
      "session_expiry_days": 30
    }
  }
}
```

## Authentication Flow

### 1. Generate PIN (CLI)

```bash
lele client pin --device "My Desktop"
```

Output:
```
🦞 Pairing PIN Generated
------------------------
  PIN:     123456
  Expires: 2026-04-05 12:05:00

Enter this PIN in your native client to pair.
```

### 2. Pair with PIN (API)

```
POST /api/v1/auth/pair
Content-Type: application/json

{
  "pin": "123456",
  "device_name": "MyDesktop"
}
```

Response:
```json
{
  "token": "a1b2c3d4...",
  "refresh_token": "e5f6g7h8...",
  "expires": "2026-05-05T12:00:00Z",
  "client_id": "client-uuid"
}
```

### 3. Refresh Token

```
POST /api/v1/auth/refresh
Content-Type: application/json

{
  "refresh_token": "e5f6g7h8..."
}
```

Response:
```json
{
  "token": "new-token...",
  "refresh_token": "new-refresh...",
  "expires": "2026-06-05T12:00:00Z"
}
```

### 4. Check Auth Status

```
GET /api/v1/auth/status
Authorization: Bearer <token>
```

Response:
```json
{
  "valid": true,
  "client_id": "client-uuid",
  "device_name": "MyDesktop",
  "expires": "2026-05-05T12:00:00Z"
}
```

## REST API Endpoints

All endpoints (except auth) require `Authorization: Bearer <token>` header.

### Chat

#### Send Message

```
POST /api/v1/chat/send
Authorization: Bearer <token>
Content-Type: application/json

{
  "content": "Hello, how are you?",
  "session_key": "optional-session-key",
  "agent_id": "optional-agent-id"
}
```

Response:
```json
{
  "message_id": "uuid",
  "session_key": "native:client-id"
}
```

#### Get History

```
GET /api/v1/chat/history?session_key=<session>
Authorization: Bearer <token>
```

Response:
```json
{
  "session_key": "native:client-id",
  "messages": [
    {"role": "user", "content": "Hello"},
    {"role": "assistant", "content": "Hi there!"}
  ]
}
```

#### Clear Session

```
DELETE /api/v1/chat/session/<session_key>
Authorization: Bearer <token>
```

### Agents

#### List Agents

```
GET /api/v1/agents
Authorization: Bearer <token>
```

Response:
```json
{
  "agents": [
    {
      "id": "main",
      "name": "Main Agent",
      "workspace": "~/.lele/workspace",
      "model": "gpt-4",
      "default": true
    }
  ]
}
```

#### Get Agent Info

```
GET /api/v1/agents/<agent_id>
Authorization: Bearer <token>
```

### Config

#### Get Config

```
GET /api/v1/config
Authorization: Bearer <token>
```

### Tools

#### List Tools

```
GET /api/v1/tools
Authorization: Bearer <token>
```

### System

#### Get Status

```
GET /api/v1/status
Authorization: Bearer <token>
```

Response:
```json
{
  "status": "running",
  "uptime": "1h30m",
  "agents": [...],
  "channels": [...],
  "version": "1.0.0"
}
```

## WebSocket API

### Connection

```
ws://127.0.0.1:18792/api/v1/ws?token=<token>
```

Or use header:
```
Authorization: Bearer <token>
```

### Client Events

#### Send Message

```json
{
  "event": "message",
  "data": {
    "content": "Hello",
    "session_key": "optional",
    "agent_id": "optional"
  }
}
```

#### Approve/Reject Command

```json
{
  "event": "approve",
  "data": {
    "request_id": "approval-uuid",
    "approved": true
  }
}
```

#### Subscribe to Session

```json
{
  "event": "subscribe",
  "data": {
    "session_key": "native:client-id"
  }
}
```

#### Cancel Current Operation

```json
{
  "event": "cancel",
  "data": {}
}
```

### Server Events

#### Welcome (on connect)

```json
{
  "event": "welcome",
  "data": {
    "client_id": "uuid",
    "device_name": "MyDesktop",
    "session_key": "native:client-id",
    "status": "idle",
    "agents": [...],
    "server_time": "2026-04-05T12:00:00Z"
  }
}
```

#### Message Ack

```json
{
  "event": "message.ack",
  "data": {
    "message_id": "uuid",
    "session_key": "native:client-id"
  }
}
```

#### Message Stream (response chunks)

```json
{
  "event": "message.stream",
  "data": {
    "message_id": "uuid",
    "chunk": "partial response...",
    "done": false
  }
}
```

#### Message Complete

```json
{
  "event": "message.complete",
  "data": {
    "message_id": "uuid",
    "content": "full response",
    "attachments": [...]
  }
}
```

#### Tool Executing

```json
{
  "event": "tool.executing",
  "data": {
    "tool": "read_file",
    "action": "Reading /path/to/file"
  }
}
```

#### Tool Result

```json
{
  "event": "tool.result",
  "data": {
    "tool": "read_file",
    "result": "file contents..."
  }
}
```

#### Approval Request

```json
{
  "event": "approval.request",
  "data": {
    "id": "approval-uuid",
    "command": "rm -rf /path",
    "reason": "Dangerous command requires approval"
  }
}
```

#### Error

```json
{
  "event": "error",
  "data": {
    "code": "error_code",
    "message": "Error description"
  }
}
```

## Error Codes

| Code | Description |
|------|-------------|
| `auth_missing` | Missing authorization header |
| `auth_invalid_format` | Invalid authorization format |
| `auth_invalid_token` | Invalid or expired token |
| `pin_error` | PIN generation error |
| `pair_error` | Pairing error |
| `content_missing` | Message content required |
| `method_invalid` | HTTP method not allowed |
| `body_invalid` | Invalid request body |
| `agent_not_found` | Agent ID not found |
| `unknown_event` | Unknown WebSocket event |

## Client Implementation Example (Tauri)

```typescript
// auth.ts
async function getPIN(): Promise<{pin: string, expires: string}> {
  const res = await fetch('http://127.0.0.1:18792/api/v1/auth/pin');
  return res.json();
}

async function pair(pin: string, deviceName: string): Promise<AuthPairResponse> {
  const res = await fetch('http://127.0.0.1:18792/api/v1/auth/pair', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({pin, device_name: deviceName})
  });
  return res.json();
}

// websocket.ts
class LeleWS {
  private ws: WebSocket;
  
  constructor(token: string) {
    this.ws = new WebSocket(`ws://127.0.0.1:18792/api/v1/ws?token=${token}`);
    
    this.ws.onmessage = (event) => {
      const msg = JSON.parse(event.data);
      this.handleEvent(msg.event, msg.data);
    };
  }
  
  send(content: string, sessionKey?: string) {
    this.ws.send(JSON.stringify({
      event: 'message',
      data: {content, session_key: sessionKey}
    }));
  }
  
  approve(requestId: string, approved: boolean) {
    this.ws.send(JSON.stringify({
      event: 'approve',
      data: {request_id: requestId, approved}
    }));
  }
  
  private handleEvent(event: string, data: any) {
    switch(event) {
      case 'message.stream':
        this.onStream?.(data.chunk, data.done);
        break;
      case 'message.complete':
        this.onComplete?.(data.content);
        break;
      case 'tool.executing':
        this.onTool?.(data.tool, data.action);
        break;
      case 'approval.request':
        this.onApproval?.(data.id, data.command, data.reason);
        break;
    }
  }
}
```

## Security Notes

- Tokens are stored hashed (SHA256) in `~/.lele/native_clients.json`
- PINs expire after 5 minutes by default
- Maximum 5 clients can be paired by default
- No TLS in development mode (localhost only)
- CORS restricted to configured origins