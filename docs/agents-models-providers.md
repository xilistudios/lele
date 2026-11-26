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
- `thinking_level`
- `max_tool_iterations`
- `prompt_cache`

### Prompt caching

`prompt_cache` enables explicit prompt-cache breakpoints (Anthropic-style
`cache_control`) for providers that support them: `anthropic`, the
`anthropic_messages` HTTP provider, and `bedrock`. The agent marks the end of
the three stable prefixes of every request — system prompt, tool definitions,
and conversation history — so repeated turns read the cached prefix instead of
re-processing it (typically 90% cheaper input tokens on cache hits).

```json
{
  "agents": {
    "defaults": {
      "prompt_cache": {
        "enabled": true,
        "ttl": "5m"
      }
    }
  }
}
```

- `enabled` (default `false`): master switch.
- `ttl`: `"5m"` (default) or `"1h"`. `"1h"` is billed at a higher rate and
  requires extended-TTL support on your plan/region.

Providers that cache implicitly (OpenAI, OpenRouter, DeepSeek, Codex) ignore
the setting but now report `cache_read_input_tokens` in usage, visible in
debug-level token tracking logs.

### Thinking level

`thinking_level` sets the default reasoning effort for an agent. It is accepted
both in `agents.defaults` and per agent in `agents.list`, and can also be set
from the Web UI (agent settings, general defaults, add-agent wizard) or the TUI
(Settings → Agents → `Thinking`).

Allowed values are `"off"`, `"low"`, `"medium"`, `"high"`, `"xhigh"` and
`"max"`. The field is
tri-state: omitting it (or clearing it back to "(default)" in the UI) means
*inherit*, so the level is resolved from the layers below instead. It is never
materialized as an empty string on disk.

```json
{
  "agents": {
    "defaults": {
      "thinking_level": "low"
    },
    "list": [
      {
        "id": "coder",
        "thinking_level": "high"
      }
    ]
  }
}
```

The effort actually sent to the provider is resolved from the first layer that
has an opinion:

| Priority | Layer | Source |
| --- | --- | --- |
| 1 (highest) | session override | `/think` command, Web UI thinking chip |
| 2 | per-agent level | `agents.list[].thinking_level` |
| 3 | global level | `agents.defaults.thinking_level` (`LELE_AGENTS_DEFAULTS_THINKING_LEVEL`) |
| 4 (lowest) | model level | `providers.<name>.models.<alias>.reasoning` |

Layer 2 already folds layer 3 into it at agent construction, so an agent that
sets `thinking_level` replaces the global default rather than merging with it.
An invalid value is rejected by config validation; at runtime it is logged and
treated as unset (inherit) instead of failing startup.

What reaches the wire depends on the resolved level:

- `"off"` (from a session override or either config layer) sends an explicit
  disable (`reasoning: {"enabled": false}`) rather than omitting the key, so
  transparent proxies with a server-side reasoning default cannot re-enable it.
  It also suppresses the DeepSeek `thinking` flag.
- `"low"` / `"medium"` / `"high"` / `"xhigh"` / `"max"` always send `enabled: true` together with the
  effort, so providers that gate emission on `enabled` do not drop a level the
  model's own `reasoning` config never enabled.
- No level at any layer keeps the previous behavior: the model `reasoning`
  config is sent as-is.

Note that `"off"` is not an accepted value for the per-model
`reasoning.effort` field; it is specific to `thinking_level` and the `/think`
override. The TUI edit-model form rejects it as an effort for that reason.

`/think` stays a per-session override on top of all of this. `/think default`
clears the override and falls back to the agent level; `/think off` does not
mean "default" — it explicitly disables reasoning. `/clear` and `/new` reset the
override along with the rest of the session state.

Subagents resolve the level from the **target** agent's configured
`thinking_level` (falling back to the parent's configured level only when no
agent context is available, e.g. standalone managers). The parent's per-session
`/think` override is deliberately not propagated — it belongs to the parent's
conversation, not to the delegated task.

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
- `thinking_level`

See [Thinking level](#thinking-level) for how `thinking_level` resolves against
`agents.defaults.thinking_level`, the model `reasoning` config, and `/think`.

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

Built-in providers such as `openai`, `anthropic`, `openrouter`, `groq`, `zhipu`, `gemini`, `vllm`, `ollama`, `moonshot`, `deepseek`, and `github_copilot` are all represented there, along with catalog-backed providers ported from hermes-agent: `xai`, `nous`, `lmstudio`, `stepfun`, `minimax`, `vercel`, `opencode`, `opencode_go`, `huggingface`, `novita`, `xiaomi`, `tencent_tokenhub`, `arcee`, `gmi`, `cerebras`, `together`, `fireworks`, `mistral`, `siliconflow`, `perplexity`, `ollama_cloud`, and `kimi_for_coding`.

The runtime also supports additional named provider entries beyond the built-ins.

Two families do not speak HTTP to a model endpoint at all: the local **CLI
transports** `claude-code` (Claude Code, via the Claude Agent SDK) and
`codex-cli` (Codex CLI). They have no `api_key` and no `api_base`, which is why
the catalog marks them `base_less`; see **Claude Code** below.

### Model catalog and prefetch

`pkg/catalog` downloads a curated catalog from this repository's `catalog/` directory (not embedded in the binary):

- `catalog/index.json` — provider manifest (id, name, api_base, file, model_count)
- `catalog/providers/<id>.json` — per-provider models with `context_window`, `max_output`, `vision`, `thinking_levels` and, when the model is not served over `chat/completions`, `protocol`

On gateway start (and on demand) lele loads `~/.lele/cache/catalog/` and, if missing or stale, downloads the files in the background from GitHub raw (`LELE_CATALOG_BASE_URL` overrides the base). Only the requested provider file is read when listing models for one provider.

HTTP API:

- `GET /api/v1/catalog/providers` — every known provider (disk index, or the hardcoded offline set) with `id`, `name`, `type`, `api_base`, `model_count` and `base_less`. The `api_base` is always filled when the type has a known endpoint (`catalog.DefaultAPIBaseByType`), so a UI can pre-fill it instead of shipping its own table; `base_less: true` marks the providers whose endpoint cannot be derived (`custom`, `claude-code`, `codex-cli`, `bedrock`, `azure_foundry`). The WebUI's Add-provider screen builds its tile grid from this response, which is what keeps a provider added in Go selectable in the UI (issue #404).
- `GET /api/v1/catalog/models?provider=<id>&q=<query>` — the filtered catalog. Entries whose `protocol` is set to anything other than `chat_completions` are filtered out server-side, because lele's provider transports only speak `chat/completions`: offering them would hand the user an id that fails on the first message (issue #405). An absent/empty `protocol` means `chat/completions`, so catalog files written before the marker existed behave exactly as before.
- `POST /api/v1/catalog/prefetch`
- `GET /api/v1/providers/{name}/models` — live `/v1/models` when credentials exist, enriched with catalog metadata, with offline catalog fallback. A name that is not configured yet but is a known catalog provider answers with the catalog list (`source: "catalog"`), so the Add-provider screen can populate its model picker before the provider is saved.

Every surface a user picks a model from applies that same filter, and each one is pinned by its own test:

| surface | where the filter lives |
|---|---|
| `GET /api/v1/catalog/models` | `catalog.ChatCompletionsModels` over `catalog.SearchModels` (`pkg/channels/rest_catalog.go`) |
| `GET /api/v1/providers/{name}/models`, `source: "catalog"` (unconfigured provider, missing key, blocked `api_base`, upstream failure) | `pickerCatalogModels` — every catalog fallback in `handleProviderModels` |
| `GET /api/v1/providers/{name}/models`, `source: "live"` | `liveProviderModels`: the catalog marker drops the ids it knows; ids the catalog does not know pass through, because they are what the endpoint actually serves |
| TUI `/add-model` and `/add-provider` pickers | `catalog.ChatCompletionsModels` at the call site and in `catalogPreset`'s default model |
| WebUI model picker (`ModelSearchInput`) | server-side for both branches, plus a `protocol` check on whatever arrives, so a stale gateway cannot put a rejected id on screen |

The marker therefore travels on the wire (`ProviderModelInfo.protocol`, `catalog.Model.protocol`); a client that renders it anyway is a client bug. `catalog.SearchModels` and `lele models list` keep the full, unfiltered list for the callers that need it.

CLI:

- `lele models refresh` — download/update the catalog cache from GitHub
- `lele models providers` / `lele models list [provider]` — inspect the cache

The catalog files are regenerated from [models.dev](https://models.dev) by `scripts/update_model_catalog.py`. A scheduled GitHub Actions workflow (`.github/workflows/update-model-catalog.yml`) runs weekly, and opens a PR when the catalog changes.

Everything the mirror cannot derive lives in that script, never in the JSON — a hand edit is reverted by the next weekly run:

- `PROTOCOL_MARKERS` — per-provider map of ids that the gateway serves over another wire protocol; it is the only source of the `protocol` field (models.dev has no transport data, and its `provider.npm` hints on the same endpoint are not reliable enough to drive it).
- `EXCLUDED_ID_SUFFIXES` — a **fail-safe policy**, not a measurement: the whole `-free` family is hidden for `opencode_go`, because the two measured free ids answer `400 ModelDeprecated` / `402 endpoints unavailable`, no free id can be *offered* by accident this way, and the weekly run cannot probe the gateway to tell the families apart. It has a known false positive (`beam-free`, measured 200) — a hidden suggestion, not a lost capability: the id still works if typed, and nothing validates config against the catalog.
- `PINNED_MODEL_IDS` — ids measured live on the gateway that models.dev's mirror does not list yet; they lead the model list, so the picker's default entry is a verified id. They bypass the deprecation and `-free` filters but **do** consume `MODEL_LIMITS` slots, which is why that cap has to cover pins plus the scored tail. A pin the snapshot cannot resolve aborts the run.
- `MODEL_METADATA_SIBLINGS` / `CURATED_MODEL_RECORDS` — where the metadata for a pinned id comes from when the provider itself does not list it. The sibling list is a priority hint, not a boundary: an id that neither the provider nor a sibling lists is taken from any provider in the snapshot (today `qwen3.7-max` comes from `302ai`, so those limits are another vendor's numbers, not a measurement), and the run says so on stderr. A curated record with no limits (`omen-alpha`) ships name-only — the wizard then falls back to the documented unknown-model defaults (`128000`/`8192`) and the TUI labels the id `no published limits`, instead of the catalog inventing numbers.

Regeneration is idempotent: `updated_at` in `index.json` only moves when the generated content actually changes, so rerunning the script on an up-to-date tree is a no-op. `--providers <id>...` limits a run to selected providers (the rest of the catalog and its index entries stay untouched), and `--source <api.json>` regenerates offline from a saved models.dev snapshot. An unreadable `index.json` aborts the run: swallowing it would rewrite the index with only the selected providers and drop the other 47 from every picker.

When adding a model to `providers.*.models` in the WebUI settings or TUI `/add-model`, suggestions come from this catalog and prefill context window, max tokens, vision, and reasoning defaults.

### Common Provider Fields

Most provider entries support:

- `type`
- `api_key`
- `api_base`
- `proxy`
- `auth_method`
- `connect_mode`
- `headers`

The `openai` provider family also supports `web_search`.

`headers` is optional: extra HTTP headers sent on that provider's inference calls — see **Custom headers** below.

### Editing a provider from the TUI

`/providers` → ENTER on a provider opens its detail view, where **Edit provider**
(the action row, or the `e` shortcut) walks every editable field of the provider
entry one step at a time, without touching the rest:

- **Provider type** — pre-filled with the stored `type`; required.
- **API Key** — always starts BLANK (a stored secret is never rendered back into
  the form, not even masked); leaving it blank keeps the stored key.
- **API Base URL** — pre-filled with the stored `api_base`; blank keeps it.
- **Proxy** — pre-filled with the stored `proxy`; blank keeps it, `-` clears it.
- **Auth method** — pre-filled with the stored `auth_method`; blank keeps it, `-`
  clears it.
- **Connect mode** — pre-filled with the stored `connect_mode` (`stdio` or
  `grpc`, GitHub Copilot only); blank keeps it, `-` clears it.
- **Web search** — pre-filled as `yes`/`no`/`default` from the stored
  `web_search`; `default` unsets it so the transport falls back to its own
  default. Any other answer is refused on the step.
- **Headers** — a JSON object of string→string, masked as it is typed and never
  pre-filled; blank keeps the stored map, `{}` clears it. The value is validated
  with the same `protocoltypes.ValidateCustomHeaders` the config loader runs, so
  a transport-owned header (`Host`, `Content-Type`, …) or a typo is refused where
  it is typed instead of failing the config load.

Blank means **keep** on every step: the form never renders a stored secret or a
stored header value back, so a step walked through with Enter cannot wipe what it
cannot show. Because blank cannot mean two things, `-` is the only way to ask for
an optional text field (proxy, auth method, connect mode) to be *removed*, and
the Headers step is masked like the API-key step because header values can carry
credentials (an `Authorization` override, a gateway token). Both validations run
as the form advances, so an `invalid web search value` or an `invalid headers`
error lands on the step that caused it.

The entry is updated in place, so `models` and every field the edit did not touch
survive the edit — editing a provider never drops its model list. Only the fields
the edit actually CHANGED are mirrored into the typed provider config that
transport selection reads first: re-submitting a pre-filled value counts as
untouched, so an `env:`-provided credential the typed copy is the sole carrier of
is never blanked or swapped. The detail view itself lists the optional fields
that are set — proxy, auth method, connect mode, web search and the custom header
**names** only, never their values — above the model list. The provider *name* is
not editable there: renaming would have to re-point every agent model reference,
so do it in `config.json` (or add the provider under the new name and delete the
old one). ESC discards the draft and returns to the detail view.

### Custom headers

`providers.<name>.headers` is an optional string→string map of extra HTTP
headers that lele adds to that provider's inference calls. It exists because a
unified proxy in front of several upstreams can need request headers lele has no
way to know about — the case that drove it: OpenCode Go rejects any inference
call that arrives without `x-opencode-session` (`400 MissingSessionID`,
validated before authentication; see **OpenCode Go** below), and a provider
pointed at a router is not detected as Go.

Scope: both transports — OpenAI-compatible `chat/completions` and Anthropic
Messages `messages` — streaming and non-streaming, applied on every call. It is
**not** applied to model listing (`GET /models` — neither the live listing fetch
behind `GET /api/v1/providers/{name}/models` nor the onboarding check) or to
catalog prefetches; those build their own requests and carry only the API key,
so a header set here never authenticates a listing.

```json
{
  "providers": {
    "llmproxy": {
      "type": "openai",
      "api_base": "https://router.example.com/v1",
      "api_key": "…",
      "headers": { "x-opencode-session": "{{session}}" }
    }
  }
}
```

Two token forms are legal in a value; anything else — an unknown name, an
unterminated `{{` — is a config error.

`{{session}}` expands per call to the same 64-hex value lele already sends
natively as `x-opencode-session`: the SHA-256 of the conversation session key,
so it is stable across turns and restarts and changes with `/new`. A call with
no session key (cron jobs, standalone tool loops) expands to the shared unkeyed
bucket — the hash of the empty key — so the token never resolves to `""`. That
is on purpose: Go sends a header with an empty value happily enough, and it is
the gateway that does not, because an empty `x-opencode-session` defeats its
session routing and it then answers its own opaque 400.

`{{ENV_NAME}}` and `{{ENV_NAME:default}}` are the second form — the same env
placeholder `config.json` supports everywhere else (`pkg/config`'s
`expandEnvVars` runs over the whole file at load) — so a gateway token never has
to sit in the file. The validator only recognises the shape and expands nothing:
substitution happens when the config is loaded, and a missing variable with no
default becomes empty, like every other `{{ENV_*}}` in the file. This is why the
editable settings document echoes the *raw* token and a WebUI save accepts it
back unchanged — expanding on the way out would put the secret in the browser. A
save path that writes the runtime config instead (the TUI) stores the expanded
value, exactly like an `api_key` written the same way.

`headers` is honoured by the two API-key HTTP transports described above. The
families that build their own clients — Claude and Codex OAuth, the
`claude-code`/`codex-cli` CLI transports, Gemini Code Assist, GitHub Copilot —
never read the map, so switching one of those provider entries to OAuth
(`auth_method: oauth`/`token`) silently stops sending the headers. Configure the
map on the provider whose transport can carry it.

The `claude-code` transport reads none of these fields: it holds no HTTP client
of its own, so `headers`, `api_key`, `api_base` and `proxy` are meaningless
there (`claude-cli` and `claudecode` are still accepted as aliases of the
provider name).

Precedence: custom headers are applied **last** — after the transport defaults
(`Content-Type`, `User-Agent`, and `Authorization` or `X-Api-Key` plus
`Anthropic-Version`) and after the auto-detected session header — so an explicit
entry wins. That is deliberate: a gateway whose scheme is not `Bearer` needs
`Authorization` overridable, and `x-opencode-session` can be forced onto a
target lele did not detect.

Validation runs when the provider JSON is decoded into the runtime config —
`config.json` load and every reload of it — and a rejected map aborts that load
naming both the provider and the header (a failed reload leaves the running
config untouched):

```
provider "llmproxy": custom header "bad name": invalid name: must be an RFC 7230 token
provider "llmproxy": custom header "x-a": unknown placeholder "{{foo}}" (supported: {{session}}, {{ENV_NAME}}, {{ENV_NAME:default}})
```

The editable settings document is a second door, and it is checked field by
field like every other provider setting: `ValidateEditableDocument` reports the
failure as `providers.<name>.headers` with code `invalid_header`, so
`POST /api/v1/config/validate` and a rejected `PUT /api/v1/config` point at the
culprit provider instead of answering a global config error the WebUI cannot
place. The decode-time check is not replaced by it — it is the single choke
point shared by `config.json` load and both handlers, and it stays as the
backstop behind them.

Names must be RFC 7230 tokens. Values must contain no CR, LF, NUL or other
control character. Transport-owned headers are rejected outright, because
overriding them would desynchronise the request from what Go actually writes on
the wire: `Host`, `Content-Type`, `Content-Length`, `Transfer-Encoding`,
`Connection`, `Keep-Alive`, `Proxy-Connection`, `Upgrade`, `TE`, `Trailer`.
Everything else is allowed on purpose, including `Authorization`,
`User-Agent`, `X-Api-Key`, `Anthropic-Version`, `Accept` and
`x-opencode-session` itself.

The map is editable from the TUI: `/providers` → *Edit provider* ends with a
**Headers** step that takes a JSON object of string→string, masked as it is
typed (values can carry credentials), never pre-filled, blank keeping the stored
map and `{}` clearing it, validated by the same `ValidateCustomHeaders` the
loader runs so a transport-owned header or a typo is refused where it is typed.
The Web UI still has no headers editor; it models the field in the settings
document and round-trips it, so a save from the browser — even one that only
edits an unrelated section — rewrites the providers block **with** `headers`.

Resolution: the map comes from the provider entry the request resolved to — the
Named entry, or the typed `providers.<builtin>` section, which
`ensureNamedDefaults` seeds into `Providers.Named` (so a map written under
`providers.anthropic` also reaches its aliases). A selection inferred purely
from the model name, with no configured provider name, carries no custom
headers.

### Claude Code (subscription, no API key)

`claude-code` is the **local CLI transport**: lele drives the user's own Claude
Code install through the official
[Claude Agent SDK](https://www.npmjs.com/package/@anthropic-ai/claude-agent-sdk)
(`@anthropic-ai/claude-agent-sdk`), and the request is billed to the **Claude
subscription that `claude` is logged in with** — never to an API key. There is
nothing to configure: no `api_key`, no `api_base`, no `proxy`. The catalog marks
the type `base_less` for exactly that reason.

```
lele agent loop (Go)
  └─ persistent child: node bridge.mjs  (external bridge repo, JSON lines over stdio)
       └─ @anthropic-ai/claude-agent-sdk query()
            └─ claude CLI  (subscription auth)
```

The bridge lives in its own repository,
[`xilistudios/lele-claude-bridge`](https://github.com/xilistudios/lele-claude-bridge),
and is installed once into `<lele-dir>/claude-bridge` with its installer:

```bash
curl -fsSL https://raw.githubusercontent.com/xilistudios/lele-claude-bridge/main/install.sh | sh
```

Pin a release with `... | sh -s -- --ref v0.1.0`, or point lele somewhere else
with `LELE_CLAUDE_BRIDGE_PATH`. lele spawns the installed `bridge.mjs` as a
persistent child and never writes or downloads JavaScript itself.

#### Requirements

- The bridge installed (the one-liner above). The install goes to
  `<lele-dir>/claude-bridge`, so `LELE_CONFIG_DIR` relocates it too;
  `LELE_CLAUDE_BRIDGE_PATH` overrides the location.
- Node.js **≥ 18.14** on `PATH` (the bridge is an ES module).
- The SDK installed **by that script**, next to the bridge: it runs `npm ci` into
  `<lele-dir>/claude-bridge/node_modules` with the SDK pinned (`0.3.329`). A
  global install still works as the manual alternative —
  `npm install -g @anthropic-ai/claude-agent-sdk` plus `CLAUDE_AGENT_SDK_PATH`
  pointing at its `node_modules`.
- A **logged-in** Claude Code session: run `claude` once and complete the login
  with the subscription. Either the `claude` on `PATH` or the CLI bundled with
  the SDK works — both read the same `~/.claude` credentials.

lele itself never runs `npm install`; the bridge installer does, once. All
requirements are checked the first time a model call is made: a missing install
comes back as an error naming the installer one-liner and
`LELE_CLAUDE_BRIDGE_PATH`, a missing SDK as the bridge's own actionable message
(verbatim), and a missing login as an auth-class failure whose message ends with
*"Run 'claude' and log in with your Claude subscription (no API key)."* — that
one is classified as `auth`, so `model_fallbacks` can route around it.

#### Configuration

No provider entry is needed: point the agent at the provider name and let the
model be the provider's own sentinel, `claude-code` (which means *let Claude Code
pick its default*).

```json
{
  "agents": {
    "defaults": {
      "provider": "claude-code",
      "model": "claude-code"
    }
  }
}
```

An explicit model is passed through to the SDK as a bare model id:

```json
{
  "agents": {
    "defaults": {
      "provider": "claude-code",
      "model": "claude-code:claude-sonnet-4-5"
    }
  }
}
```

An empty `providers.claude-code` entry is accepted too (it changes nothing), and
`claude-cli` / `claudecode` are accepted as aliases of the provider name.

#### Environment variables

| Variable | Meaning |
| --- | --- |
| `LELE_CLAUDE_BRIDGE_PATH` | Directory containing `bridge.mjs` + `bridge_lib.mjs` (or a direct path to `bridge.mjs`). Empty means `<lele-dir>/claude-bridge`, i.e. the installer default, with `LELE_CONFIG_DIR` honoured. |
| `CLAUDE_AGENT_SDK_PATH` | `node_modules` directory (or the package directory) containing `@anthropic-ai/claude-agent-sdk`. Searched **first**; it takes effect when the bridge has no side-by-side `node_modules`, i.e. with a global SDK install. |
| `CLAUDE_CODE_PATH` | Path to the `claude` CLI to use. Empty means `PATH`, then the SDK's own resolution. |
| `LELE_NODE_BIN` | Node binary to spawn. Empty means `node` from `PATH`. |

lele hands the bridge this candidate list for the SDK, in order:
`CLAUDE_AGENT_SDK_PATH` when set, the installed bridge's own `node_modules`
(where the installer puts the SDK), `npm root -g`,
`/opt/homebrew/lib/node_modules`, `/usr/local/lib/node_modules` and
`~/.npm-global/lib/node_modules`. The bridge tries the bare
`@anthropic-ai/claude-agent-sdk` specifier first, so in the canonical layout the
side-by-side copy resolves before any candidate is consulted — a set
`CLAUDE_AGENT_SDK_PATH` is only effective when that side-by-side copy is absent.
`NODE_PATH` entries are honoured last (the list is also re-exported to the child
as `NODE_PATH`, aggregated from the same candidates — an ES module `import`
needs the explicit paths, not just `NODE_PATH`).

#### Subscription only: API-key variables are stripped

Anything that could redirect the CLI to a **different billing path** is removed
from the bridge's environment before it is spawned: `ANTHROPIC_API_KEY`,
`ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_CUSTOM_HEADERS`,
`ANTHROPIC_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL`,
`ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL`,
`CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`,
`CLAUDE_CODE_SKIP_BEDROCK_AUTH`, `CLAUDE_CODE_SKIP_VERTEX_AUTH`. A key exported
for the `anthropic` provider is therefore never picked up here: this transport
authenticates with the subscription or not at all.

#### Tools run in lele, not in Claude Code

lele keeps its own agent loop; the SDK query is a **single model step**:

- every built-in Claude Code tool is disabled (`tools: []`), and lele's tool set
  is exposed to the model as an in-process MCP server named `lele`, with each
  tool's JSON Schema passed through unchanged;
- when the model emits a `tool_use` block the bridge **captures** it and
  **interrupts** the query, then answers with `finish_reason: "tool_calls"`;
  lele executes the tool itself (its own sandbox, approvals and workspace rules
  apply) and calls the model again with the appended history;
- as defence in depth, `permissionMode` stays `default` and a `canUseTool`
  callback denies everything, so even a racing interrupt cannot execute
  anything inside Claude Code. There is no `--dangerously-skip-permissions`
  anywhere in this path.

Consequence: a permission prompt, a sandbox decision or an approval in lele
applies exactly as it does for every other provider — Claude Code's own tool
policy never enters the picture.

#### Stateless transcript replay

Each model step starts a **fresh** SDK query with the whole conversation
serialised into the prompt (`persistSession: false`, `settingSources: []`, no
`--resume`). Nothing about the conversation is written to `~/.claude`, and
nothing from the machine leaks into the prompt: no `CLAUDE.md`, no
`settings.json`, no skills, no MCP servers from the user's Claude Code config.
The same conversation replayed by lele produces the same prompt, and `/new`
simply starts an empty transcript.

Images are carried through: a `data:` URL image in the last user message is sent
to the model as a real image block; images anywhere else in the history are
rendered as a short `[image: …]` placeholder (and video as `[video omitted]`).

#### Development / testing

The bridge is covered by its own Node test suite in
[`xilistudios/lele-claude-bridge`](https://github.com/xilistudios/lele-claude-bridge),
which needs no SDK, no login and no network:

```bash
git clone https://github.com/xilistudios/lele-claude-bridge.git
cd lele-claude-bridge
node --test bridge_test.mjs   # 45 tests
```

The Go side is covered by unit tests against a fake bridge (`go test
./pkg/providers/`), and the real end-to-end path by the integration suite, which
**skips** unless the bridge is installed, node is present, the SDK is resolvable
and a subscription is logged in:

```bash
go test -tags=integration ./pkg/providers/ -run ClaudeCode -v
```

### OpenCode Go

`opencode_go` is OpenCode's subscription LLM service
([official docs](https://opencode.ai/v2/docs/console/go/)). Get an API key
from the [OpenCode Console](https://opencode.ai/console); the key is a plain
bearer token (Go / Go Plus subscription — see the official docs for current
plans and limits).

```json
{
  "providers": {
    "opencode_go": {
      "type": "opencode_go",
      "api_key": "…",
      "api_base": "https://opencode.ai/zen/go/v1"
    }
  },
  "agents": { "defaults": { "provider": "opencode_go", "model": "deepseek-v4.1-flash" } }
}
```

`api_base` must be the `/zen/go/v1` base. Do not use the
`https://opencode.ai/inference/go/openai/v1` alias as `api_base`: it accepts
inference calls but answers `404` to `GET /models`, so live model listing and
onboarding validation degrade to the offline catalog. The `/zen/go/v1` base
serves both inference and `GET /models`.

lele detects an OpenCode Go target automatically — by provider name or type
(`opencode_go` / `opencode-go`), or by an `api_base` whose path is one of the
OpenCode Go routes (`opencode.ai/zen/go`, `opencode.ai/inference/go`;
case-insensitive). The path matters: the first-class Zen base
`https://opencode.ai/zen/v1` shares the host but is *not* detected as Go. Once
detected, lele sends two extra headers on inference calls. There is nothing to
configure:

- `User-Agent: lele/<version>` — sent by the OpenAI-compatible and Anthropic
  Messages transports (lele identifies itself instead of appearing as a generic
  HTTP client). The OAuth/CLI provider families deliberately keep their own
  User-Agents because those endpoints require impersonating the official
  clients — Claude OAuth (`claude-cli/2.x (external, sdk-cli)`, the UA the
  `anthropic` provider sends under `auth_method: oauth`), Gemini Code Assist
  (`GeminiCLI-tui/…`) and Antigravity (`antigravity/…`); a `lele/…` value
  there would be rejected, so the impersonation is a requirement, not an
  oversight. The `claude-code`/`codex-cli` CLI transports are outside this list
  entirely: lele writes no HTTP request for them, the official CLI does.
- `x-opencode-session: <64 hex chars>` — the gateway rejects requests without
  it (`400 MissingSessionID`, validated before authentication). The value is
  the SHA-256 hash of lele's internal conversation session key: stable across
  turns and restarts, changed by `/new`, and opaque — no channel, chat id or
  path leaves the machine. Calls with no session key (cron jobs, standalone
  tool loops) share one stable "unkeyed" bucket — the hash of the empty key —
  so they keep working instead of failing. If the target was configured by
  explicit name/type and a call still carries no session id, lele fails the
  call locally with a clear error instead of forwarding a request the gateway
  would reject; when detection came only from `api_base`, the header is
  opportunistic — sent when a session id is present, silently omitted when it
  is not.

The gateway exposes a single OpenAI-compatible `chat/completions` API, so no
per-model dialect has to be configured — but only for the model families that
speak that protocol (kimi, glm, qwen, deepseek, minimax, mimo, …); several
others are served over a different wire protocol and reject it (see **Model
coverage** below).

Configuring it as an `anthropic`-typed provider is not supported: the
Anthropic Messages transport authenticates with `x-api-key`, while OpenCode Go
expects `Authorization: Bearer`.

#### Model coverage on the shared chat/completions endpoint

Not every model listed by `GET /models` is served over that protocol. The
lists below were measured against `/zen/go/v1` on 2026-10-22 (one minimal
message per model; same result for streaming and non-streaming) and will
drift as the service changes.

- Answer `chat/completions` (HTTP 200) — 25 ids:
  - DeepSeek: `deepseek-v4-pro`, `deepseek-v4.1-flash`
  - Kimi: `kimi-k2.6`, `kimi-k2.7-code`, `kimi-k3`
  - GLM: `glm-5.1`, `glm-5.2`, `glm-5.3`, `glm-5.3-flash`
  - Qwen: `qwen3.6-plus`, `qwen3.7-max`, `qwen3.7-plus`, `qwen3.8-flash`,
    `qwen3.8-max`
  - MiMo: `mimo-v2.5`, `mimo-v2.6-flash`, `mimo-v2.6-pro`
  - MiniMax: `minimax-m2.5`, `minimax-m3`, `minimax-m3.1-flash`
    (`minimax-m2.7` rejects — see below)
  - Hermes: `hy3`, `hy4-preview`
  - Step: `step-5-preview`
  - Others: `beam-free`, `omen-alpha`
- Answer `400 ModelProtocolUnsupported` (`Model does not support this
  protocol.`) — 9 ids: `claude-haiku-5-5`, `gemini-4-argon-contributor`,
  `gpt-5.6-luna`, `gpt-6-luna`, `grok-4.6`, `grok-4.7`, `minimax-m2.7`,
  `muse-spark-1.2-contributor`, `muse-spark-1.3-contributor`. These are served
  over a different wire protocol (not `chat/completions`), so lele's
  OpenAI-compatible transport cannot drive them; they need provider-specific
  clients and are not usable through `opencode_go` today. Practical rule: if
  you see `ModelProtocolUnsupported`, switch to another model id from the
  working list.
- Advertised by `GET /models` but failing upstream on OpenCode's side — 2
  ids: `mimo-v2.5-pro` answers `Upstream request failed: [400] Unsupported
  model mimo-v2.5-pro` (plain, streaming, and tools+reasoning alike), and
  `longcat-2.0` answers `402 Upstream request failed: Endpoint is
  unavailable.` Both are upstream conditions on OpenCode's side, not lele's,
  and can change without notice — prefer `mimo-v2.5` or `mimo-v2.6-pro` until
  the first one is fixed.
- Advertised by the bundled catalog but deprecated upstream — 2 ids
  (measured 2026-10-22): `minimax-m3.1-flash-free` answers `400
  ModelDeprecated` ("Model minimax-m3.1-flash-free has been deprecated.")
  and `longcat-2.5-preview-free` answers `402 Upstream request failed:
  Endpoint is unavailable.` Free-tier ids no longer ship: the generator hides
  the whole `-free` family for this provider (`EXCLUDED_ID_SUFFIXES` in
  `scripts/update_model_catalog.py`) as a fail-safe policy, not because each of
  them was measured failing — `beam-free` actually answers 200, so it is a
  known false positive of the rule and simply not suggested.

`GET /models` is authoritative for *names*, not for protocol compatibility;
smoke-test a model with one message before making it the default in
`agents.defaults`.

#### How the catalog encodes this split

`catalog/providers/opencode_go.json` carries the measurement, not just this
page: every id in the `400 ModelProtocolUnsupported` list above is recorded
with a `protocol` marker in the generator (`responses` for the
OpenAI/Grok/Muse ids, `anthropic_messages` for `claude-haiku-5-5` and
`minimax-m2.7`, `gemini` for `gemini-4-argon-contributor`), and the catalog
files ship that marker for the ids they still list —
`gemini-4-argon-contributor` itself is deprecated upstream, so it is no longer
in the file. Every picker surface drops the marked entries before the user sees
them — the catalog endpoint, both branches of `GET /api/v1/providers/{name}/models`,
the TUI picker and the WebUI model search, see **Model catalog and prefetch** —
so the model that tops the Add-provider list is a verified `chat/completions` id
instead of `gpt-5.6-luna`. The ids measured live but
missing from models.dev (`glm-5.1`, `grok-4.6`, `kimi-k2.6`, `minimax-m2.5`,
`minimax-m2.7`, `minimax-m3.1-flash`, `omen-alpha`, `qwen3.6-plus`,
`qwen3.7-max`) are pinned by the generator and lead the list. `omen-alpha` has
no published limits anywhere, so its catalog entry is name-only: the wizard
falls back to lele's unknown-model defaults (`128000` context, `8192` max
tokens) and the picker says `no published limits` rather than inheriting
another vendor's numbers.

The marker is curated, not mirrored: models.dev has no transport field, so
behaviour changes (an id starts or stops answering `chat/completions`) must be
recorded in `PROTOCOL_MARKERS` in `scripts/update_model_catalog.py` — see
**Model catalog and prefetch**. Ids absent from both this page and the marker
map are unmeasured; smoke-test them before relying on them.

#### Requirements / troubleshooting

- Some Go models are gated on the workspace region. When the workspace is not
  on it, the gateway answers `400 "This Go model requires Global regions.
  Select Global in your workspace's Privacy settings to use it."` → select
  **Global** under the OpenCode Console workspace's Privacy settings. This
  400 comes from the workspace setting, not from lele; the gate is not
  uniform across models (a DeepSeek model returned it once and answered
  normally later), so retry once before assuming a misconfiguration.
- The `x-opencode-session` header is added by **lele's own outbound
  requests**. If OpenCode Go is reached through another gateway/proxy (e.g.
  llmproxy), that gateway must send the header itself; otherwise the request
  fails with `400 MissingSessionID`. A transparent router can also be handed
  the same value by lele — see **Through a unified proxy** below.

#### Through a unified proxy

Both routes are supported: the native `opencode_go` provider (direct) and an
OpenAI-compatible provider pointed at a router that fronts the Go gateway. The
proxy route is what keeps ONE provider and ONE key for every upstream, but the
router must forward — or inject — `x-opencode-session`, and it must not drop
unknown headers: the gateway answers `400 MissingSessionID` before it looks at
the key, whatever client is behind it.

When the router is transparent, hand it the id lele would have sent natively
with the `{{session}}` template (see **Custom headers** above):

```json
{
  "providers": {
    "llmproxy": {
      "type": "openai",
      "api_base": "https://router.example.com/v1",
      "api_key": "…",
      "headers": { "x-opencode-session": "{{session}}" }
    }
  }
}
```

Nothing else changes. Go detection does not fire for the router's own
`api_base`, so the header comes from this map instead of from the transport, and
the value is the same per-conversation hash — the router sees the affinity a
direct client would produce, per turn and across restarts.

### OpenCode Zen

`opencode` is OpenCode's pay-as-you-go model gateway
([OpenCode Console](https://opencode.ai/console)) — the metered sibling of
the Go subscription (`opencode_go` above). Get an API key from the console;
it is a plain bearer token sent as `Authorization: Bearer`.

```json
{
  "providers": {
    "opencode": {
      "type": "opencode",
      "api_key": "…",
      "api_base": "https://opencode.ai/zen/v1"
    }
  },
  "agents": { "defaults": { "provider": "opencode", "model": "kimi-k2.5" } }
}
```

`api_base` must be the `/zen/v1` base. Zen has its own wallet, separate
from the Go subscription: a Go-subscription key is accepted by
`GET /models` (HTTP 200) but inference fails with `500
{"error":{"type":"server_error","message":"Upstream request failed:
Insufficient account funds"}}` — a Go subscription does not fund Zen
calls. Configure this provider only with a key that carries Zen credits.

lele does **not** add the `x-opencode-session` header to Zen calls: the Go
detection deliberately excludes the `/zen/v1` base (see **OpenCode Go**
above), so requests go out as plain OpenAI-compatible calls. If the endpoint
ever starts requiring the header, adding it is a separate change.

The model list is catalog-backed (`catalog/providers/opencode.json`, 40
ids), but per-model protocol coverage on `/zen/v1` is unverified — the
coverage split documented for Go was measured on `/zen/go/v1` only. Smoke
test a model with one message before making it the default in
`agents.defaults`. For the same reason the Zen catalog carries no `protocol`
markers yet: the picker filter only hides ids that have been measured as
non-`chat/completions` on the gateway they are served from, and that has not
been done for `/zen/v1`.

## Named Provider Models

Providers can declare model aliases in a `models` map. This is the preferred way to define short, stable names for models you want to expose in the UI and config.

Supported per-model fields include:

- `model`
- `context_window`
- `max_tokens`
- `temperature`
- `vision`
- `video`
- `reasoning`
- `thinking_type`

### `thinking_type`

Names the wire-level **think system** the model's endpoint uses to turn
reasoning on and off. Optional; empty or `"auto"` keeps the legacy heuristics.

| value | wire format | used by |
| --- | --- | --- |
| `"auto"` (default) | legacy heuristics (endpoint/model name) | anything else |
| `"deepseek"` | `thinking: {"type": "enabled"\|"disabled"}` | DeepSeek V3.2+, Xiaomi MiMo, Zhipu GLM-4.5+ |
| `"openai"` | top-level `reasoning_effort` (`"none"` disables) | OpenAI o-series / gpt-5 |
| `"openrouter"` | `reasoning: {"effort", "enabled", ...}` object | OpenRouter |
| `"qwen"` | `enable_thinking: true\|false` | Alibaba DashScope / Qwen3 |
| `"none"` | never sends thinking parameters | models without a thinking switch |

This field is what makes `thinking_level: "off"` (TUI `/think`, Web UI chip)
actually disable reasoning: several endpoints (MiMo, DeepSeek, GLM) default
thinking **on** and ignore the generic `reasoning` object, so the disable has
to be rendered in their own dialect. Example for MiMo, where `off` sends
`thinking: {"type": "disabled"}`:

```json
{
  "providers": {
    "xiaomi": {
      "type": "openai",
      "api_key": "{{ENV_MIMO_API_KEY}}",
      "models": {
        "mimo-v2.5-pro": {
          "model": "mimo-v2.5-pro",
          "thinking_type": "deepseek"
        }
      }
    }
  }
}
```

With `"auto"` (or unset) Lele already sends the explicit disable for known
thinking-object model families (names containing `deepseek`, `mimo`, `glm`);
set `thinking_type` explicitly for anything else — e.g. Qwen3 on DashScope
needs `"qwen"`. For OpenAI models that reject a disable (`o1`, `o3`), use
`"none"` so Lele never sends thinking parameters at all.

`thinking_type` can also be set without touching JSON: the TUI asks for a
"Think system" when adding a provider or model (`/connect`, `/providers`), and
the Web UI exposes a per-model **Think system** dropdown in the provider
settings model editor. Leave the field empty (or pick "auto") to keep the
default heuristics.

The `reasoning` object (`enable`, `effort`, `max_tokens`, `exclude`, `summary`)
is editable from both UIs as well, so none of it needs `config.json`:

- **TUI** — `/providers` → provider detail → ENTER on a model row appends five
  reasoning steps to the edit form: **Reasoning** (`yes`/`no`), **effort**
  (`low`/`medium`/`high`/`xhigh`/`max`), **max tokens** (a positive integer), **exclude**
  (`yes`/`no`) and **summary** (`auto`/`detailed`/`concise`). `default` on any
  of the four optional ones clears that override back to unset; disabling
  reasoning with every override cleared drops the whole object, so the saved
  entry stays clean.
- **Web UI** — settings → providers → model editor exposes one control per
  option (the optional ones prune the key when left on "default").

Adding a model (`/add-model`, `/connect`) is unchanged: reasoning there is
seeded from the catalog. The resolution order above is unchanged too — a
per-model `reasoning` config remains the lowest-priority layer.

`vision: true` enables the `read_image` tool and `read_video`'s frames mode
(keyframes + audio transcript); `video: true` adds native `video_url`
delivery for `read_video` (see [Video input (read_video)](#video-input-read_video)).

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
        "gemini-pro": {
          "model": "google/gemini-2.5-pro",
          "context_window": 1048576,
          "vision": true,
          "video": true
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

### Video input (read_video)

`read_video` is offered to any model with **video or vision** capability.
The `mode` argument picks the delivery:

- **native** — the video is injected as a `video_url` multimodal content
  part (base64 `data:` URL for local files, raw URL for `url`). Requires the
  model's `video` flag.
- **frames** — N evenly-spaced JPEG keyframes injected as `image_url`
  content parts, plus the audio transcript as a text part. Requires the
  model's `vision` flag and `ffmpeg`/`ffprobe` on `PATH` (optional
  runtime-only dependency, never a build dependency).

The tool contract:

- `path` — a local video file (mp4, m4v, webm, mov, avi, mkv, mpeg, mpg,
  ogv). Exactly one of `path`/`url` must be given (path XOR url).
- `url` — a public `http(s)` URL, including YouTube URLs (only useful on
  Gemini-backed routes). **Native mode only**: frames mode rejects URLs and
  requires a local path.
- `mode` — `auto` (default) | `native` | `frames`. `auto` resolves
  against the model handling the current call — the session/turn model (or
  the subagent's own model), not a capability snapshot taken when the agent
  instance was created: native when that model has the `video` flag, frames
  when it only has `vision`, and fails when it has neither.
- `frames` — frames mode only: number of keyframes to extract, 1–16,
  default 8.
- `prompt` — optional text sent alongside the video.
- `fps` — optional frame-sampling hint, `0 < fps ≤ 60`. **Native mode
  only** — when set, lele always includes it in the `video_url` object;
  support is provider-specific (Qwen-VL family honors it; others may ignore
  or reject it).

Exposure and gating:

- The tool is offered when the model has `video` **or** `vision`:
  video-only → native; vision-only → frames; both → `auto` picks native;
  neither → the tool is hidden from the model and blocked at execution time
  (exactly like `read_image` is hidden without `vision`).
- Stripping of historical `video_url` content parts after switching to a
  model that cannot ingest them is still keyed on the `video` flag only.

#### Frames mode (keyframes + transcript)

- Requires `ffmpeg` and `ffprobe` on `PATH`, checked at call time. Install
  ffmpeg to use frames mode, or give the model native video support instead
  (`"video": true`).
- Extracts N evenly-spaced keyframes across the video as JPEGs capped at
  1280 px wide, injected as base64 `image_url` parts.
- Extracts the audio track (16 kHz mono WAV) and transcribes it when a Groq
  API key is configured (`providers.groq.apiKey` — the same signal the
  gateway uses for channel voice transcription); the transcript is injected
  as a trailing text part. The endpoint is `providers.groq.api_base`
  (default `https://api.groq.com/openai/v1`), so a self-hosted or proxied
  OpenAI-compatible Whisper server works by pointing that key at it; the
  transcription response is capped at 10 MiB and a failing endpoint's error
  body is logged truncated to 512 characters.
- Graceful degradation: a missing or failed transcript never aborts the
  read. When transcription is configured, the model sees a placeholder text
  part explaining the gap — `no audio track detected`, `no speech
  detected`, or `audio transcription failed`. When transcription is NOT
  configured (no Groq API key), no transcript part is added at all: the tool
  result summary notes `transcription not configured` instead, and the model
  never sees it as transcript content.
- Local file cap: **200 MiB** (`maxVideoFramesReadSize` in
  `pkg/tools/video.go`) — nothing from the video itself is base64'd; only
  the extracted keyframes (a few hundred KB each) enter the context. Each
  ffmpeg operation is bounded by a **60 s** timeout
  (`videoFramesTimeout`).
- Motion/temporal detail is limited to the N keyframes, and audio reaches
  the model only as transcript text (no native audio understanding).

#### Provider matrix

| Provider | `video_url` support | Notes |
| --- | --- | --- |
| OpenRouter | Yes — URL or base64 data URL | `google/gemini` video-capable models, `qwen-vl`, `kimi`, `grok`. Gemini via AI Studio through OpenRouter: YouTube URLs only. |
| xAI Grok | Yes — URL, base64 data URL, or `file_id` | |
| Qwen-VL (DashScope / vLLM / Ollama) | Yes — whole mp4 as `video_url` | Qwen3-VL understands the audio track; Qwen2.5-VL is frames-only (no audio). Optional `fps`. |
| MiMo (Xiaomi, OpenAI-compatible endpoint) | Yes — URL or base64 data URL | `video_url` with mp4/webm/avi/mkv/mov, ≤100 MB / ≤1000 s per call. The v2.5 docs state the audio track is not processed; `mimo-v2.6-pro`/`mimo-v2.6-flash` were verified to transcribe speech from the video's audio track. `fps` is accepted but undocumented (treated as ignored). |
| Moonshot Kimi | No (for lele v1) | Video via Files API `ms://` file ids is **not supported** by lele v1. |
| Gemini direct (`generativelanguage` OpenAI-compat path used by lele) | Unverified | `video_url` support on this path is **unverified**; for Gemini video understanding, configure the model through OpenRouter. Native `inline_data`/File API support is future work. |
| OpenCode Go (`opencode_go`) | Unverified | The OpenAI-compatible model families share one `chat/completions` endpoint, while several families (GPT, Grok, Claude, Gemini, Muse, and `minimax-m2.7`) reject that protocol with `ModelProtocolUnsupported` (see the Model coverage subsection of **OpenCode Go** above). `video_url` support is **unverified** — treat it like any OpenAI-compatible backend and do not rely on native video. |
| OpenAI / Anthropic | No | No native video — do not set `video: true`. With `vision: true` they now get `read_video` via frames mode, no video flag needed: keyframes as `image_url` parts plus the audio transcript as text. Audio reaches them only as transcript text, and visual motion/temporal detail is limited to the N keyframes. |

#### Tokens and limits

- Gemini 2.5/3.x sample video at 1 fps plus 16 kbps audio by default,
  ≈ **258 tokens per second** of video.
- Native mode inlines local files up to **20 MiB** (`maxVideoReadSize` in
  `pkg/tools/video.go`) as a base64 `data:` URL; larger files must be
  referenced with `url`.
- Frames mode accepts local files up to **200 MiB**
  (`maxVideoFramesReadSize`): nothing is base64'd from the video itself —
  only the extracted JPEG keyframes enter context — so the 20 MiB native cap
  does not apply. Each ffmpeg operation has a 60 s timeout.
- Base64 inflates the payload by ~33% on the wire (native mode), and — same
  as `read_image` — the inlined video is persisted in session history (known
  limitation).

#### Recommended config

```json
{
  "providers": {
    "openrouter": {
      "type": "openrouter",
      "api_key": "{{ENV_OPENROUTER_API_KEY}}",
      "models": {
        "gemini-2.5-pro": {
          "model": "google/gemini-2.5-pro",
          "context_window": 1048576,
          "vision": true,
          "video": true
        }
      }
    }
  }
}
```

Models without native video support (OpenAI, Anthropic) only need
`"vision": true`: `read_video` is then served in frames mode (keyframes +
transcript, requires `ffmpeg`/`ffprobe` on `PATH`).


## How Model Selection Works

In practice, Lele can resolve models from:

- the agent default model
- a named agent model override
- a session model override
- provider model aliases

Common forms are:

- `provider:model`
- a provider alias like `my-openai-compatible:fast`
- a raw model name that is resolved inside the active provider

### Recommended Convention

Use explicit `provider:model` values whenever possible. This keeps agent behavior predictable and makes the UI model list clearer.

Examples:

- `openrouter:auto`
- `my-openai-compatible:fast`
- `anthropic:claude-sonnet`
- `opencode_go:deepseek-v4.1-flash`

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
- set `thinking_level` per agent rather than globally when only some agents do
  deep reasoning work; it keeps the cost of the rest at the model default

## Related Docs

- `docs/tools_configuration.md`
- `docs/client-api.md`
- `README.md`
