# Multi-Agent Group Chat (Mixture of Agents)

Lele supports multi-agent group chat — a feature where 2 or more agents collaborate in a shared conversation, building on each other's contributions. This is sometimes called "Mixture of Agents" (MoA), after the [original paper from Together AI](https://arxiv.org/abs/2406.04692).

## When To Use Group Chat vs Subagents

| Feature | Subagents (`spawn`) | Group Chat (`/group`, `group_chat`) |
| --- | --- | --- |
| Model | Parent → child (isolated) | N peers in shared transcript |
| Context | Each subagent has its own context | All participants see the same shared transcript |
| Output | One result back to parent | Multiple turns, visible intermediate output, final synthesis |
| Interaction | No inter-subagent communication | Participants build on each other's responses |
| Use case | Delegated parallel work | Debate, review panels, collaborative problem-solving |

Use **subagents** when you need independent parallel tasks. Use **group chat** when you need agents to see and respond to each other.

## Strategies

Lele supports four collaboration strategies:

| Strategy | Description | Typical Use |
| --- | --- | --- |
| `round_robin` | Each agent speaks in turn, cycling through all participants. Everyone sees all prior turns. | Debate, cross-review, general discussion |
| `moa` | Proposers generate responses in parallel per layer, then an aggregator synthesizes them. Repeats for N layers. | High-quality responses, multi-perspective synthesis |
| `moderator` | A coordinator agent dynamically decides who speaks next and when to stop. | Expert panels, open brainstorming |
| `pipeline` | Sequential relay: output of agent A feeds into agent B, then C. Each agent is a specialist in the chain. | Chained specialization, multi-stage processing |

### How MoA works (layer by layer)

```
Layer 0:  [proposer A]  [proposer B]  [proposer C]   ← all propose in parallel
                         ↓
          [aggregator]                                  ← synthesizes proposals
                         ↓
Layer 1:  [proposer A]  [proposer B]  [proposer C]   ← refine based on synthesis
                         ↓
          [aggregator]                                  ← final synthesis
```

## Configuration

Group profiles are defined in the `groups` block of `~/.lele/config.json`:

```json
{
  "groups": {
    "list": [
      {
        "id": "review-panel",
        "participants": ["architect", "coder", "security-auditor"],
        "strategy": "moa",
        "rounds": 2,
        "moderator": "architect",
        "max_turns": 12,
        "max_tokens_per_turn": 4096,
        "total_token_budget": 60000,
        "stop_keywords": ["CONSENSUS", "FINAL"],
        "parallel": true
      },
      {
        "id": "quick-debate",
        "participants": ["agent-a", "agent-b"],
        "strategy": "round_robin",
        "rounds": 3
      }
    ]
  }
}
```

### Configuration fields

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes | Unique identifier for the group profile |
| `participants` | string[] | yes | Agent IDs that must exist in `agents.list` |
| `strategy` | string | yes | One of: `round_robin`, `moa`, `moderator`, `pipeline` |
| `rounds` | int | no | Number of layers (MoA) or cycles (round_robin). 0 = unlimited (capped by `max_turns`) |
| `moderator` | string | no | Agent ID to serve as aggregator/moderator. Must be in `participants` |
| `max_turns` | int | no | Hard cap on total turns. If 0 and `rounds` is set, derived as `rounds × participants` |
| `max_tokens_per_turn` | int | no | Maximum tokens per individual turn |
| `total_token_budget` | int | no | Hard cap on cumulative tokens across all turns. 0 = unlimited |
| `stop_keywords` | string[] | no | Keywords (case-insensitive) in any turn that trigger early stop (e.g. `["CONSENSUS", "FINAL"]`) |
| `parallel` | bool | no | Whether proposers within a MoA layer run concurrently |

### Validation rules

- `participants` must not be empty
- All participant agent IDs must exist in `agents.list`
- `strategy` must be one of the four valid values
- If `moderator` is set, it must be in the `participants` list

## Using the `/group` Command

The `/group` command manages group conversations from any chat channel or the CLI.

### Start from a profile

```
/group start <profileID> <task description>
```

Starts a group using a pre-configured profile from `groups.list`.

```
/group start review-panel Analyze this PR for security issues and architectural concerns
```

The `task` is everything after the profile ID.

### Start ad-hoc

```
/group start --agents architect,coder,auditor --strategy moa --rounds 2 --moderator architect --parallel Review this code for security vulnerabilities
```

Flags:

| Flag | Required | Description |
| --- | --- | --- |
| `--agents <id,id,...>` | yes | Comma-separated list of agent IDs |
| `--strategy <name>` | no | Strategy name (default: `round_robin`) |
| `--rounds <N>` | no | Number of rounds/layers |
| `--moderator <id>` | no | Agent ID for aggregator/moderator role |
| `--parallel` | no | Run proposers concurrently within a layer |

### List active groups

```
/group list
```

Shows all currently running groups with their ID, strategy, status, and participant count.

### Check group status

```
/group status <groupID>
```

Shows the current state, turn count, token usage, and participants for a specific group.

### Stop a group

```
/group stop <groupID>
```

Cancels the group by stopping its context. The group ends and its transcript is preserved.

## Using the `group_chat` Tool

The `group_chat` tool allows an orchestrator agent to delegate a problem to a multi-agent panel and receive the final synthesis synchronously. It is analogous to `spawn` but uses the group chat framework.

### Parameters

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| `task` | string | yes | Objective/prompt for the panel |
| `participants` | string[] | yes | Agent IDs to include |
| `strategy` | string | no | Strategy name (default: `round_robin`) |
| `rounds` | int | no | Number of rounds/layers |
| `moderator` | string | no | Agent ID for aggregator/moderator |
| `parallel` | bool | no | Run proposers concurrently |
| `max_turns` | int | no | Hard cap on total turns |
| `max_tokens_per_turn` | int | no | Per-turn token cap |
| `total_token_budget` | int | no | Cumulative token cap (0 = unlimited) |
| `stop_keywords` | string[] | no | Keywords that trigger convergence |

### Example tool invocation

```json
{
  "task": "Review this Go function for race conditions and suggest fixes",
  "participants": ["coder", "reviewer", "security-auditor"],
  "strategy": "moa",
  "rounds": 2,
  "moderator": "reviewer",
  "parallel": true
}
```

### Behavior

- **Synchronous**: the tool blocks until the group completes and returns the final synthesis as its result.
- The returned text includes a brief header identifying the strategy and participants, followed by the synthesis.
- Permission checks apply: the calling agent must be allowed to spawn/access the requested participant agents (enforced via the subagent allowlist).

## How It Looks

### TUI (Terminal)

When a group is running, intermediate turns appear as labeled blocks in the terminal:

```
┌ [architect · layer 0 · proposer]
│ Here's my architectural analysis of the function...
└

┌ [coder · layer 0 · proposer]
│ From an implementation perspective, I see these issues...
└

┌ [auditor · layer 0 · proposer]
│ The security implications are...
└

┌ [architect · layer 0 · aggregator]
│ Synthesizing the proposals above: the consensus is...
└
```

Each turn shows the speaker name, layer number, and role. The final synthesis is rendered as a regular assistant response. Layers are visually separated.

### WebUI

In the web interface, group chat turns appear in the chat panel organized by layer:

- Each turn is a bubble with the speaker name, role badge (proposer/aggregator/moderator), and layer indicator
- Turns within a layer are grouped together; layers are collapsible
- A participants panel on the side shows all agents in the group with their roles and current status
- The final synthesis is highlighted as the canonical response

## Limits And Safety

### Hard limits (automatic stops)

The group runner enforces these limits in priority order:

1. **MaxTurns**: If the total number of turns reaches this cap, the group stops.
2. **TotalTokenBudget**: If cumulative token usage reaches this cap, the group stops.
3. **StopKeywords**: If any turn contains a stop keyword (case-insensitive), the group stops with reason `stop_keyword:<keyword>`.
4. **Converged repetition**: If the last 3 turns have identical normalized content (lowercased, trimmed), the group stops with reason `converged_repetition`. This prevents infinite loops where agents keep repeating themselves.

### Permissions

Group chat respects the subagent allowlist:

- An agent can only start a group with participants it is allowed to spawn/access (checked via `CanSpawnSubagent`).
- If a caller tries to include a denied agent, the start request is rejected with a list of denied agents.
- This applies to both the `/group` command and the `group_chat` tool.

### Context cancellation

Groups can be stopped at any time with `/group stop <id>`. The stop is cooperative — the current turn finishes, and the group transitions to `stopped` status. The transcript up to that point is preserved.

## Cost And Latency

Group chat, especially the `moa` strategy, multiplies LLM calls:

- **MoA**: `N proposers × L layers + L aggregator calls` (e.g. 3 proposers × 2 layers + 2 aggregator calls = 8 LLM calls total)
- **Round robin**: `N participants × R rounds` calls
- **Pipeline**: exactly `N participants` calls (one per agent in the chain)
- **Moderator**: variable, bounded by `max_turns`

### Recommendations

- **Use cheaper/faster models for proposers**, and a stronger model for the aggregator. Configure this via per-agent model settings in `agents.list`.
- **Set `parallel: true`** for MoA to reduce wall-clock time (proposers run concurrently).
- **Set `total_token_budget`** as a hard spending cap.
- **Set `max_turns`** to prevent runaway groups.
- **Keep rounds low** — 2 rounds is usually enough for MoA to converge.
- **Use `round_robin` or `pipeline`** when you don't need the full MoA overhead — they use fewer calls.

## End-To-End Example

### Goal

Run a "review panel" with 3 agents (architect, coder, security-auditor) using the MoA strategy to review code.

### Step 1: Configure the agents and profile

In `~/.lele/config.json`:

```json
{
  "agents": {
    "list": [
      {
        "id": "architect",
        "name": "Architect",
        "model": "claude-sonnet-4-20250514"
      },
      {
        "id": "coder",
        "name": "Coder",
        "model": "glm-4.7"
      },
      {
        "id": "security-auditor",
        "name": "Security Auditor",
        "model": "claude-sonnet-4-20250514"
      }
    ]
  },
  "groups": {
    "list": [
      {
        "id": "review-panel",
        "participants": ["architect", "coder", "security-auditor"],
        "strategy": "moa",
        "rounds": 2,
        "moderator": "architect",
        "max_turns": 12,
        "max_tokens_per_turn": 4096,
        "total_token_budget": 60000,
        "stop_keywords": ["CONSENSUS", "FINAL"],
        "parallel": true
      }
    ]
  }
}
```

### Step 2: Launch the group

```
/group start review-panel Review the authentication middleware in auth.go for security vulnerabilities, race conditions, and architectural issues
```

### Step 3: What happens

1. **Layer 0**: All three agents (architect, coder, security-auditor) propose in parallel. Each sees the task and their role in the panel.
2. **Layer 0 aggregation**: The architect (as moderator/aggregator) reads all three proposals and synthesizes them.
3. **Layer 1**: The three agents propose again, now seeing the Layer 0 proposals and synthesis.
4. **Layer 1 aggregation**: The architect synthesizes the refined proposals into the final answer.
5. **Group complete**: The final synthesis is sent as the group's response.

Total LLM calls: 3 proposers × 2 layers + 2 aggregator calls = 8 calls.

### Step 4: Observe in real-time

As turns happen, you see streaming output labeled by speaker and layer. The final synthesis is the group's answer to the task.

## Architecture Notes

- Group sessions use the key pattern `group:<groupId>` (distinct from subagent sessions).
- The shared transcript is the source of truth — each agent sees a rendered version of all prior turns with speaker labels.
- Group state is persisted on disk and survives restarts.
- The `group_chat` tool is registered alongside other tools and respects the same permission model as `spawn`.

## Related Docs

- `docs/SKILL_SUBAGENTS.md` — subagent system (spawning, sync/async delegation)
- `docs/architecture.md` — overall system architecture
- `docs/config-reference.md` — full configuration reference
- `docs/moa-group-chat-plan.md` — the original design plan
