# Goal Command: Subagent-Based Evaluation Loop

Plan for redesigning the `/goal` command so it behaves like a normal chat
with an autonomous continuation loop, where goal completion is evaluated by
a **separate subagent** that reads the **conversation summary** instead of
the current inline judge that only sees the latest response.

## Status

- Branch: `main` (WIP code exists in `stash@{0}`, not yet merged)
- **NOT implemented on main.** The stash contains a working prototype of
  Phases 1-4 but was never merged. This plan supersedes the previous
  version of this document.

---

## Problem

The current goal loop (`runGoalContinuation` in `pkg/agent/llm_runner.go`)
uses an inline LLM judge (`llmGoalJudge` in `pkg/agent/goal.go`) to decide
whether the goal is done. The judge only receives:

1. The goal text
2. The **latest** agent response (truncated to 4k chars)
3. The raw session history (passed but largely unused)

### Drawbacks

- **Narrow view**: The judge only sees the last response, not the trajectory
  of the whole conversation. The agent may have completed the goal several
  turns ago, but the judge can't tell from the final message alone.
- **Token waste**: Raw history is passed even though it may be huge. The
  session already maintains a compressed summary (`GetSummary` /
  `SetSummary`) that the judge ignores.
- **Single-role judge**: The judge is a small LLM call with a fixed prompt
  and `max_tokens: 10`. Hard to make it reason carefully or use tools.
- **Semaphore deadlock**: `runGoalContinuation` calls `runAgentLoop`
  recursively, but `runAgentLoop` acquires a per-session semaphore
  (`sessionProcessing`, capacity-1 channel) at the top. The outer call
  still holds the semaphore when the continuation fires, so the recursive
  call blocks on `case semCh <- struct{}{}` indefinitely. This is a
  **critical bug** that must be fixed before the subagent evaluator can
  work reliably.

### Desired behavior

1. `/goal <text>` sets the goal and the agent starts working on it —
   **exactly like a normal chat**. The user can send messages at any time.
2. When the agent finishes a turn, a **subagent** is launched with the
   **conversation summary** + latest response.
3. The subagent evaluates whether the goal is achieved.
4. If achieved -> mark done, notify user, **stop the loop**.
5. If not achieved -> inject a continuation prompt and run another agent turn.
6. Budget (max turns) is still enforced as a safety net.

---

## Design

### 1. Normal chat behavior

`/goal <text>` sets the goal via `GoalManager.Set()`. From that point:

- The user can send messages at any time — they are processed normally.
- The goal continuation loop only fires **autonomously** after each agent
  turn completes (after the outbound message is published).
- Any user message interrupts the current continuation cycle. The loop
  re-evaluates after the next agent turn.
- `/goal clear` stops the loop. `/goal pause` / `/goal resume` control it.

No changes needed to message routing or session management — the existing
`processMessage` -> `runAgentLoop` pipeline handles this.

### 2. Fix semaphore deadlock (critical prerequisite)

**Root cause**: `runAgentLoop` acquires a per-session semaphore at the top:

```go
sem, _ := lr.al.sessionProcessing.LoadOrStore(opts.SessionKey, make(chan struct{}, 1))
semCh := sem.(chan struct{})
select {
case semCh <- struct{}{}:
    defer func() { <-semCh }()
case <-ctx.Done():
    return "", ctx.Err()
}
```

`runGoalContinuation` is called **before** the `defer` releases the
semaphore. The recursive `runAgentLoop` call then blocks on the same
channel.

**Fix**: Move the goal continuation loop OUT of `runAgentLoop`. Extract it
so it is called from the **caller** of `runAgentLoop` (e.g.,
`processMessage` in `message_processor.go`), after `runAgentLoop` returns
and the semaphore is released:

```
processMessage:
  response = runAgentLoop(opts)     // acquires + releases semaphore
  if goalActive && !opts.SkipGoalLoop:
      runGoalContinuation(...)       // no semaphore contention
```

Alternative (simpler, more invasive): release the semaphore explicitly
before calling `runGoalContinuation` inside `runAgentLoop`, and skip
re-acquisition for continuation turns via a flag. The caller-side approach
is cleaner and keeps `runAgentLoop` single-responsibility.

### 3. Summary-based inline judge (Phase 1)

Replace `llmGoalJudge` with `SummaryGoalJudge`:

- Fetches the session's conversation summary via a `SummaryProvider`
  interface (satisfied by `*session.SessionManager.GetSummary`).
- Builds the evaluation prompt from:
  - the goal text,
  - the current session summary,
  - the latest agent response (truncated to 4k chars).
- Makes a single inline LLM call and returns `DONE` / `CONTINUE`.

The judge now sees the **full trajectory** via the summary instead of only
the final message, and no longer receives raw history.

**Interface change**:

```go
// Before
JudgeGoal(ctx, goalText, lastResponse string, history []providers.Message) (bool, string, error)

// After
JudgeGoal(ctx, sessionKey, goalText, lastResponse string) (bool, string, error)
```

The judge fetches the summary itself via `SummaryProvider`.

### 4. Subagent evaluator (Phase 2 — main ask)

The goal judge can be configured to run as a **subagent**
(`goal.judge.mode = "subagent"`):

- `SubagentGoalJudge` implements `GoalJudge` and spawns the evaluator via
  the existing `SubagentManager` (`SpawnWithOptions`).
- Uses a dedicated agent ID (config `goal.judge.agent`), or falls back to
  the default agent.
- The task prompt contains the goal text, the session summary, and the
  latest response (via the shared `buildGoalJudgePrompt`).
- The subagent replies with `DONE` / `CONTINUE`; the result is read back
  synchronously via the task's done channel, bounded by a timeout.
- Reuses the existing `SubagentManager` wired into the agent loop for
  tools, retries, model override, and a clean separate agent context.

**SubagentRunner interface** (satisfied by `*tools.SubagentManager`):

```go
type SubagentRunner interface {
    SpawnWithOptions(ctx context.Context, task, label, agentID, originChannel, originChatID string,
        callback tools.AsyncCallback, opts tools.SpawnOptions) (string, error)
    GetTask(taskID string) (*tools.SubagentTask, bool)
}
```

**Evaluation flow**:

```
Agent turn completes
  -> runGoalContinuation fires
    -> IncrementTurn (budget check)
    -> SubagentGoalJudge.JudgeGoal:
        1. Fetch session summary via SummaryProvider
        2. Build prompt: goal + summary + last response
        3. SpawnWithOptions("goal-evaluator", prompt, ...)
        4. Wait on task.DoneChannel() with timeout
        5. Parse DONE / CONTINUE from task.Result
    -> If DONE: MarkDone, notify user, stop
    -> If CONTINUE: inject continuation prompt, run next turn
    -> If error/timeout: log warning, continue loop (don't block)
```

### 5. Config (Phase 3)

`pkg/config/config.go` adds a `goal.judge` section:

```json
{
  "goal": {
    "judge": {
      "mode": "inline",
      "agent": ""
    }
  }
}
```

- `mode`: `"inline"` (default) | `"subagent"`
- `agent`: agent ID for the subagent evaluator (empty = default agent)

Environment variables: `LELE_GOAL_JUDGE_MODE`, `LELE_GOAL_JUDGE_AGENT`.

### 6. TUI: `/goal` on welcome screen

Currently, `/goal` on the welcome screen (no active session) returns an
error. Fix: create a new session automatically, same as `submitMessage`.

```go
case "/goal":
    if m.currentKey == "" {
        m.createNewChat()
        m.showWelcome = false
    }
    m.goalFeedback = m.agentLoop.HandleGoalCommand(m.currentKey, parts[1:])
```


---

## Implementation Phases

### Phase 0 — Fix semaphore deadlock (CRITICAL, not in stash)

Restructure so the goal continuation loop does not re-enter `runAgentLoop`
while the session semaphore is held. Move the continuation trigger to the
caller of `runAgentLoop` (after it returns).

**Files**: `pkg/agent/llm_runner.go`, `pkg/agent/message_processor.go`

### Phase 1 — Inline summary-based judge

Replace `llmGoalJudge` with `SummaryGoalJudge`. Update `JudgeGoal`
interface signature. Add `SummaryProvider` interface and pure
`buildGoalJudgePrompt` helper.

**Files**: `pkg/agent/goal.go`, `pkg/agent/llm_runner.go`, `pkg/agent/loop.go`

### Phase 2 — Subagent evaluator

Add `SubagentGoalJudge` + `SubagentRunner` interface. Wire via config
`goal.judge.mode`. The evaluator spawns a subagent that reads the summary
and returns DONE/CONTINUE.

**Files**: `pkg/agent/goal.go`, `pkg/agent/loop.go`

### Phase 3 — Config

Add `GoalConfig` / `GoalJudgeConfig` to `pkg/config/config.go` with
defaults. Wire in `loop.go` after subagent managers are created (the
judge needs the subagent map, so wiring must move below
`newToolCoordinatorWithSubagents`).

**Files**: `pkg/config/config.go`, `pkg/agent/loop.go`

### Phase 4 — TUI fix

Allow `/goal` on the welcome screen by auto-creating a session.

**Files**: `pkg/tui/commands.go`

### Phase 5 — Tests

- Unit tests for `SummaryGoalJudge` (DONE/CONTINUE, no-provider, error).
- Tests that the judge fetches the session summary via `SummaryProvider`.
- Pure `buildGoalJudgePrompt` tests (summary present, missing, truncation).
- `SubagentGoalJudge` tests (DONE/CONTINUE, no-runner, spawn error, timeout)
  via mock runner.
- Semaphore deadlock regression test (continuation must not block on the
  session semaphore).
- TUI test for `/goal` on welcome screen (already exists in
  `pkg/tui/goal_command_test.go`, currently untracked).
- Existing goal loop tests updated to the new interface signature.
- Full suite green.

---

## Existing WIP code (stash@{0})

A working prototype of Phases 1-4 exists in `stash@{0}` (WIP on
`3675fb9`). It includes:

- `SummaryGoalJudge` + `SubagentGoalJudge` in `pkg/agent/goal.go`
- Updated `JudgeGoal` call site in `pkg/agent/llm_runner.go`
- Judge wiring (inline/subagent) in `pkg/agent/loop.go`
- `GoalConfig` / `GoalJudgeConfig` in `pkg/config/config.go`
- TUI `/goal` welcome-screen fix in `pkg/tui/commands.go`
- Updated + new tests in `pkg/agent/goal_test.go`

**The stash does NOT fix the semaphore deadlock (Phase 0).** That must be
done separately.

Recommended approach:

1. Create branch `feat/goal-subagent-evaluator`.
2. Apply the stash: `git stash apply 'stash@{0}'`.
3. Implement Phase 0 on top.
4. Add the untracked `pkg/tui/goal_command_test.go`.
5. Run full test suite, commit per phase.

---

## Files changed (expected)

| File | Change |
|---|---|
| `pkg/agent/goal.go` | `SummaryGoalJudge`, `SubagentGoalJudge`, `SummaryProvider`, `SubagentRunner`, `buildGoalJudgePrompt`; new `JudgeGoal` signature |
| `pkg/agent/llm_runner.go` | Updated `JudgeGoal` call site; move goal-loop trigger out of `runAgentLoop` (Phase 0) |
| `pkg/agent/message_processor.go` | Call goal continuation after `runAgentLoop` returns (Phase 0) |
| `pkg/agent/loop.go` | Wire judge (inline or subagent) after subagents are created |
| `pkg/config/config.go` | `GoalConfig` + `GoalJudgeConfig` with `mode`/`agent`; default mode `inline` |
| `pkg/tui/commands.go` | `/goal` auto-creates session on welcome screen |
| `pkg/agent/goal_test.go` | Updated mock judge signature; new judge tests |
| `pkg/tui/goal_command_test.go` | TUI tests (existing, untracked) |

---

## Risks & mitigations

1. **Semaphore deadlock (Phase 0)** — highest risk. Without the fix, the
   continuation loop hangs on the second turn. Mitigation: regression test
   that runs two continuation turns with a timeout.

2. **Subagent concurrency limit** — `SubagentManager` enforces
   `maxConcurrent`. If the limit is reached, the judge spawn fails.
   Mitigation: on spawn error, log and continue the loop (don't block);
   optionally fall back to inline judge for that turn.

3. **Summary not yet generated** — on early turns the session summary may
   be empty (summarization triggers above a token threshold). Mitigation:
   `buildGoalJudgePrompt` handles empty summary gracefully; the judge also
   receives the latest response.

4. **Judge timeout** — the subagent may take longer than expected.
   Mitigation: bounded wait (default 60s); on timeout, continue the loop.

5. **Recursion depth** — the continuation loop is iterative (for-loop), not
   recursive, so stack depth is not a concern once Phase 0 is done.

6. **User interruption** — if the user sends a message while a continuation
   turn is running, the session semaphore serializes them. This is the
   desired behavior (no interleaved turns).

---

## Testing checklist

- [ ] `SummaryGoalJudge` returns DONE/CONTINUE correctly
- [ ] Judge fetches summary via `SummaryProvider`
- [ ] `buildGoalJudgePrompt` handles missing summary and truncation
- [ ] `SubagentGoalJudge` DONE/CONTINUE via mock runner
- [ ] `SubagentGoalJudge` handles spawn error and timeout
- [ ] No semaphore deadlock across 2+ continuation turns
- [ ] Budget exhaustion marks goal blocked and notifies
- [ ] `/goal clear` stops the loop mid-cycle
- [ ] TUI `/goal` on welcome screen creates session
- [ ] Full test suite green (`go test ./...`)
