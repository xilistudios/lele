# Plan: Include Available Subagents in System Prompt

## Goal
When an agent has subagents enabled (`subagents` config), include a concise list of available subagents (ID + description) in the system prompt. Also add a new `description` field to each agent config.

## Changes

### 1. Config: Add `Description` field to `AgentConfig`

**File:** `pkg/config/config.go`

```go
type AgentConfig struct {
    ID          string            `json:"id"`
    Default     bool              `json:"default,omitempty"`
    Name        string            `json:"name,omitempty"`
    Description string            `json:"description,omitempty"` // NEW
    Workspace   string            `json:"workspace,omitempty"`
    // ... rest unchanged
}
```

### 2. ContextBuilder: Add subagents context support

**File:** `pkg/agent/context.go`

Add a field to `ContextBuilder`:

```go
type ContextBuilder struct {
    // ... existing fields
    availableSubagents []subagentInfo // NEW
}

type subagentInfo struct {
    ID          string
    Description string
}
```

Add a setter method:

```go
func (cb *ContextBuilder) SetAvailableSubagents(subagents []subagentInfo) {
    cb.availableSubagents = subagents
}
```

In `GetInitialContext()`, after the skills summary, append a subagents section if non-empty:

```go
// Subagents section
if len(cb.availableSubagents) > 0 {
    var sb strings.Builder
    sb.WriteString("## Subagents Available\n\n")
    sb.WriteString("You can delegate tasks to these subagents using the `spawn` tool with the `agent_id` parameter.\n\n")
    for _, sa := range cb.availableSubagents {
        if sa.Description != "" {
            sb.WriteString(fmt.Sprintf("- **%s** — %s\n", sa.ID, sa.Description))
        } else {
            sb.WriteString(fmt.Sprintf("- **%s**\n", sa.ID))
        }
    }
    parts = append(parts, sb.String())
}
```

### 3. Wire subagents info in NewAgentInstance

**File:** `pkg/agent/instance.go`

After creating the ContextBuilder in `NewAgentInstance`, resolve and set available subagents:

```go
contextBuilder := NewContextBuilder(workspace)

// Resolve available subagents from config
if ac.Subagents != nil && len(ac.Subagents.AllowAgents) > 0 {
    subagents := resolveAvailableSubagents(ac, cfg)
    contextBuilder.SetAvailableSubagents(subagents)
}
```

Add helper `resolveAvailableSubagents` that:
- If `allow_agents` contains `"*"` → list all agents from `cfg.Agents.List` (except self)
- Otherwise → list only the explicitly allowed agents
- For each, look up the `Description` from the agent config in `cfg.Agents.List`

### 4. Handle registry reload

**File:** `pkg/agent/registry.go`

In `ReloadAgents`, when preserving an existing agent's ContextBuilder (line ~218), also re-resolve subagents since the agent list may have changed:

```go
// After preserving ContextBuilder, refresh subagents
subagents := resolveAvailableSubagents(ac, cfg)
instance.ContextBuilder.SetAvailableSubagents(subagents)
```

Also invalidate the system prompt cache so the next turn picks up the new subagent list:

```go
instance.ContextBuilder.ResetAllSystemPromptCaches()
```

### 5. Tests

**File:** `pkg/agent/context_test.go`

- `TestSubagentsInSystemPrompt_ExplicitList` — agent with `allow_agents: ["sales", "support"]` → prompt includes both
- `TestSubagentsInSystemPrompt_Wildcard` — agent with `allow_agents: ["*"]` → prompt includes all except self
- `TestSubagentsInSystemPrompt_NoSubagents` — agent without subagents config → no section in prompt
- `TestSubagentsInSystemPrompt_WithDescription` — verifies description is rendered
- `TestSubagentsInSystemPrompt_EmptyDescription` — verifies only ID shown when no description

**File:** `pkg/config/config_test.go`

- `TestAgentConfig_Description` — JSON marshaling/unmarshaling with description field

## Example Output in System Prompt

```markdown
## Subagents Available

You can delegate tasks to these subagents using the `spawn` tool with the `agent_id` parameter.

- **sales** — Handles sales inquiries and product recommendations
- **support** — Technical support and troubleshooting
```

## Files to Modify

| File | Change |
|------|--------|
| `pkg/config/config.go` | Add `Description` field to `AgentConfig` |
| `pkg/agent/context.go` | Add `subagentInfo` struct, `SetAvailableSubagents`, render section in `GetInitialContext` |
| `pkg/agent/instance.go` | Wire subagents info after ContextBuilder creation, add `resolveAvailableSubagents` helper |
| `pkg/agent/registry.go` | Refresh subagents on reload + invalidate caches |
| `pkg/agent/context_test.go` | 5 new tests |
| `pkg/config/config_test.go` | 1 new test |

## Estimated Effort
~150 lines of new code + ~100 lines of tests. Small, focused change.
