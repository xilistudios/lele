# Examples

This page provides concrete configuration snippets for common setups.

## Minimal Local CLI Setup

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "restrict_to_workspace": true,
      "provider": "openrouter",
      "model": "openrouter/auto",
      "max_tokens": 8192,
      "max_tool_iterations": 20
    }
  },
  "providers": {
    "openrouter": {
      "type": "openrouter",
      "api_key": "YOUR_API_KEY"
    }
  }
}
```

## Coder Agent With Separate Workspace

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "provider": "openrouter",
      "model": "openrouter/auto",
      "max_tokens": 8192,
      "max_tool_iterations": 20
    },
    "list": [
      {
        "id": "coder",
        "name": "Coding Agent",
        "workspace": "~/.lele/workspace-coder",
        "model": {
          "primary": "my-openai-compatible/fast",
          "fallbacks": ["openrouter/auto"]
        }
      }
    ]
  }
}
```

## OpenAI-Compatible Provider With Aliases

```json
{
  "providers": {
    "my-openai-compatible": {
      "type": "openai",
      "api_key": "YOUR_API_KEY",
      "api_base": "https://example.com/v1",
      "models": {
        "fast": {
          "model": "gpt-4o-mini"
        },
        "reasoning": {
          "model": "o4-mini",
          "reasoning": {
            "effort": "medium"
          }
        }
      }
    }
  }
}
```

## Web UI + Native API

```json
{
  "channels": {
    "web": {
      "enabled": true,
      "host": "0.0.0.0",
      "port": 3005
    },
    "native": {
      "enabled": true,
      "host": "127.0.0.1",
      "port": 18793,
      "max_clients": 5,
      "token_expiry_days": 30,
      "pin_expiry_minutes": 5,
      "max_upload_size_mb": 50,
      "upload_ttl_hours": 24
    }
  }
}
```

Run:

```bash
lele gateway
lele web start
```

## Telegram Gateway Example

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allow_from": ["123456789"]
    }
  }
}
```

Run:

```bash
lele gateway
```

## Recurring Cron Job

```bash
lele cron add --name reminder --message "Check backups" --every 7200
```

## Daily Cron Job

```bash
lele cron add --name daily --message "Summarize errors" --cron "0 9 * * *"
```

## Related Docs

- `docs/agents-models-providers.md`
- `docs/channel-setup.md`
- `docs/installation-and-onboarding.md`
