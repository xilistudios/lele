# Model Routing

Lele supports several layers of model selection and resolution.

This document explains how the runtime chooses models and how aliases resolve.

## Sources Of Truth

Model selection can come from:

- `agents.defaults.model`
- `agents.defaults.model_fallbacks`
- `agents.list[].model`
- `agents.list[].subagents.model`
- per-session model overrides
- provider model aliases under `providers.*.models`

## Common Model Forms

Lele accepts these common forms:

- explicit provider form: `provider/model`
- provider alias form: `my-provider/fast`
- raw model names in contexts where the provider is already known

## Named Provider Models

Example:

```json
{
  "providers": {
    "my-openai-compatible": {
      "type": "openai",
      "api_key": "...",
      "api_base": "https://example.com/v1",
      "models": {
        "fast": {
          "model": "gpt-4o-mini"
        },
        "reasoning": {
          "model": "o4-mini"
        }
      }
    }
  }
}
```

This enables references such as:

- `my-openai-compatible/fast`
- `my-openai-compatible/reasoning`

## Resolution Behavior

At a high level, the runtime attempts to:

1. respect an explicit provider prefix when present
2. resolve aliases inside that provider first
3. normalize names when possible
4. fall back to searching across configured providers when needed

## Why Explicit Provider Prefixes Are Better

Recommended:

- `openrouter/auto`
- `openai/gpt-4o-mini`
- `anthropic/claude-sonnet`
- `my-openai-compatible/fast`

This makes session switching, UI model lists, and debugging much clearer.

## Session-Level Overrides

The native/web flow supports session model updates through:

```text
PATCH /api/v1/chat/session/<session_key>?action=model
```

This lets a client switch the active model for a specific session without changing the global config.

## Fallbacks

Fallbacks can be configured globally or per named agent.

Examples:

```json
{
  "agents": {
    "defaults": {
      "model": "openrouter/auto",
      "model_fallbacks": [
        "openai/gpt-4o-mini",
        "anthropic/claude-sonnet"
      ]
    }
  }
}
```

```json
{
  "agents": {
    "list": [
      {
        "id": "coder",
        "model": {
          "primary": "my-openai-compatible/fast",
          "fallbacks": ["openrouter/auto"]
        }
      }
    ]
  }
}
```

## Image Models

Image generation or vision-oriented defaults can be configured separately via:

- `image_model`
- `image_model_fallbacks`

## Related Docs

- `docs/agents-models-providers.md`
- `docs/client-api.md`
