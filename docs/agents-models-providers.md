# Agents, Models, And Providers

This document explains how Lele configures agent defaults, named agents, provider definitions, and model selection.

## Where This Lives

All of this is configured in `~/.lele/config.json`.

The main sections are:

- `agents.defaults`
- `agents.list`
- `bindings`
- `providers`

## Agent Defaults

`agents.defaults` defines the base runtime for the main agent unless a more specific agent/session setting overrides it.

Supported fields include:

- `workspace`
- `restrict_to_workspace`
- `provider`
- `model`
- `model_fallbacks`
- `image_model`
- `image_model_fallbacks`
- `max_tokens`
- `temperature`
- `max_tool_iterations`

### Example

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "restrict_to_workspace": true,
      "provider": "openrouter",
      "model": "openrouter/auto",
      "model_fallbacks": [
        "anthropic/claude-sonnet",
        "openai/gpt-4o-mini"
      ],
      "image_model": "openai/gpt-4.1-mini",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20
    }
  }
}
```

## Named Agents

`agents.list` lets you define additional agent profiles with their own identity, workspace, model, skills, and subagent rules.

Supported agent fields include:

- `id`
- `default`
- `name`
- `workspace`
- `model`
- `skills`
- `subagents`
- `temperature`

### Model Format In Named Agents

Named agents can use either:

1. a plain string model
2. a structured model object with fallbacks

### Example

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
          "fallbacks": [
            "openrouter/auto"
          ]
        },
        "skills": [
          "github",
          "tmux"
        ],
        "subagents": {
          "allow_agents": [
            "coder"
          ],
          "model": {
            "primary": "my-openai-compatible/o4-mini"
          }
        },
        "temperature": 0.2
      }
    ]
  }
}
```

## Agent Bindings

`bindings` route external conversations to a specific agent.

This is useful when different channels, accounts, guilds, or peers should use different workspaces or behaviors.

### Example

```json
{
  "bindings": [
    {
      "agent_id": "coder",
      "match": {
        "channel": "telegram",
        "account_id": "123456789"
      }
    }
  ]
}
```

## Providers

Lele treats provider configuration as a named map under `providers`.

Built-in providers such as `openai`, `anthropic`, `openrouter`, `groq`, `zhipu`, `gemini`, `vllm`, `ollama`, `moonshot`, `deepseek`, and `github_copilot` are all represented there.

The runtime also supports additional named provider entries beyond the built-ins.

### Common Provider Fields

Most provider entries support:

- `type`
- `api_key`
- `api_base`
- `proxy`
- `auth_method`
- `connect_mode`

The `openai` provider family also supports `web_search`.

## Named Provider Models

Providers can declare model aliases in a `models` map. This is the preferred way to define short, stable names for models you want to expose in the UI and config.

Supported per-model fields include:

- `model`
- `context_window`
- `max_tokens`
- `temperature`
- `vision`
- `reasoning`

### Example

```json
{
  "providers": {
    "my-openai-compatible": {
      "type": "openai",
      "api_key": "{{ENV_MY_OPENAI_API_KEY}}",
      "api_base": "https://example.com/v1",
      "models": {
        "fast": {
          "model": "gpt-4o-mini",
          "vision": true,
          "temperature": 0.7
        },
        "o4-mini": {
          "model": "o4-mini",
          "context_window": 200000,
          "max_tokens": 100000,
          "reasoning": {
            "effort": "medium",
            "summary": "auto"
          }
        }
      }
    }
  }
}
```

## How Model Selection Works

In practice, Lele can resolve models from:

- the agent default model
- a named agent model override
- a session model override
- provider model aliases

Common forms are:

- `provider/model`
- a provider alias like `my-openai-compatible/fast`
- a raw model name that is resolved inside the active provider

### Recommended Convention

Use explicit `provider/model` values whenever possible. This keeps agent behavior predictable and makes the UI model list clearer.

Examples:

- `openrouter/auto`
- `my-openai-compatible/fast`
- `anthropic/claude-sonnet`

## Fallback Models

Fallbacks can be defined at the default-agent level and at the named-agent level.

### Default Fallbacks

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

### Named-Agent Fallbacks

```json
{
  "agents": {
    "list": [
      {
        "id": "research",
        "model": {
          "primary": "gemini/gemini-2.5-pro",
          "fallbacks": [
            "openrouter/auto"
          ]
        }
      }
    ]
  }
}
```

## Subagent Model Control

Named agents can also define which target agents they are allowed to spawn and which model profile subagents should use.

### Example

```json
{
  "agents": {
    "list": [
      {
        "id": "ops",
        "subagents": {
          "allow_agents": [
            "ops",
            "coder"
          ],
          "model": {
            "primary": "openai/o4-mini"
          }
        }
      }
    ]
  }
}
```

## Notes And Recommendations

- keep provider names stable once you start using them in agent configs
- prefer aliases such as `my-openai-compatible/fast` over copying long raw model IDs everywhere
- use per-agent workspaces only when you want real separation of files, state, or prompts
- use explicit fallbacks for important agents instead of relying on ad-hoc manual switching

## Related Docs

- `docs/tools_configuration.md`
- `docs/client-api.md`
- `README.md`
