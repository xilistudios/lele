# Agent Communication Protocol (ACP)

Lele can expose its agents over the [Agent Communication Protocol](https://agentcommunicationprotocol.dev) (ACP 0.2.0), a REST standard for agent interoperability.

## Enable

Add to `~/.lele/config.json`:

```json
{
  "acp": {
    "enabled": true,
    "token": ""
  }
}
```

Or via environment:

```bash
export LELE_ACP_ENABLED=true
# optional bearer token
export LELE_ACP_TOKEN=secret
```

Routes are mounted on the unified gateway server (same host/port as the web UI).

## Endpoints

| Method | Path | Description |
| --- | --- | --- |
| GET | `/ping` | Protocol handshake |
| GET | `/agents` | Discover agents (`limit`, `offset`) |
| GET | `/agents/{name}` | Agent manifest |
| POST | `/runs` | Create a run (`sync` / `async` / `stream`) |
| GET | `/runs/{run_id}` | Run status |
| POST | `/runs/{run_id}` | Resume (await not implemented) |
| POST | `/runs/{run_id}/cancel` | Cancel a run |
| GET | `/runs/{run_id}/events` | Event history |
| GET | `/session/{session_id}` | Session descriptor |

Agent names are RFC 1123 labels. Lele agent IDs are sanitized (`My Agent` → `my-agent`); the original ID is kept in `metadata.annotations.lele_agent_id`.

## Examples

```bash
# Discover agents
curl -s http://127.0.0.1:18790/agents | jq

# Synchronous run
curl -s -X POST http://127.0.0.1:18790/runs \
  -H 'Content-Type: application/json' \
  -d '{
    "agent_name": "chat",
    "mode": "sync",
    "input": [{
      "role": "user",
      "parts": [{"content_type": "text/plain", "content": "What can you do?"}]
    }]
  }' | jq

# Streaming run (SSE)
curl -N -X POST http://127.0.0.1:18790/runs \
  -H 'Content-Type: application/json' \
  -d '{
    "agent_name": "chat",
    "mode": "stream",
    "input": [{"role": "user", "parts": [{"content_type": "text/plain", "content": "hello"}]}]
  }'
```

## Implementation notes

- Package: `pkg/acp`
- Gateway wiring: `cmd/lele/acp.go`
- Sessions map to lele session keys `acp:{session_id}`
- Runs call the agent loop through `ProcessDirectWithChannel` with channel `acp`
- `await` / resume is not implemented yet (501)
- Event stream uses SSE (`text/event-stream`) with ACP event types

## Related

- Spec: https://agentcommunicationprotocol.dev/spec/openapi.yaml
- OpenAPI version implemented: 0.2.0
